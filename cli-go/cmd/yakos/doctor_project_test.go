package main

// doctor_project_test.go — K-136: `yakos doctor --project <dir>` is wired through the real command,
// not just the doctor package. A new flag needs parsing in runDoctor, an entry in the command
// registry and a line in the help, and the working directory default must reach only the Agent
// budgets section: package-level tests miss all of that, so these drive the built binary, the way
// the other Go-native doctor tests do. TestDoctorProject_Q4Probe is rev-330's probe q4
// (doctor_project_probe_q4.sh) as a committed test.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runYakosIn runs the built binary with dir as its working directory, YAKOS_IMPL=go, and env laid
// over the test process's environment. It returns the merged output and the exit code.
func runYakosIn(t *testing.T, bin, dir string, args []string, env map[string]string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec
	cmd.Dir = dir
	var base []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if _, over := env[k]; over || k == "YAKOS_IMPL" {
			continue
		}
		base = append(base, kv)
	}
	base = append(base, "YAKOS_IMPL=go")
	for k, v := range env {
		base = append(base, k+"="+v)
	}
	cmd.Env = base
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// projectFixture is a scratch HOME whose state directory holds one subscription run of the
// renamed supervisor "watchdog" (70M tokens, $0: past the supervisor's 33M limit and its 66M stop,
// so a project that names watchdog its supervisor has it at a hard stop), and two projects: one
// naming watchdog as its supervisor, one that does not.
type projectFixture struct {
	home, ren, plain string
	env              map[string]string
}

func newProjectFixture(t *testing.T) projectFixture {
	t.Helper()
	f := projectFixture{home: t.TempDir(), ren: t.TempDir(), plain: t.TempDir()}
	state := filepath.Join(f.home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"watchdog","runtime":"claude","billing":"subscription","usage":{"input_tokens":1000000,"output_tokens":1000000,"cache_read":68000000,"cache_creation":0,"total_cost_usd":0},"api_equivalent_usd":12.5}`+"\n",
		time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(row), 0o600); err != nil {
		t.Fatal(err)
	}
	writeProjectYML(t, f.ren, "supervisor:\n  runtime: claude\n  agent: watchdog\n")
	writeProjectYML(t, f.plain, "name: plain\n")
	f.env = policyEnv(f.home)
	return f
}

func writeProjectYML(t *testing.T, dir, yml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
}

const watchdogStop = "LLM supervision disabled: watchdog budget exhausted (token limit reached: 70,000,000 of 33,000,000 tokens"

// rev-330's probe q4: the renamed supervisor and `yakos doctor`, in every form the docs promise
// (--project <dir>, else the working directory) and the form that already worked (a path).
func TestDoctorProject_Q4Probe(t *testing.T) {
	goBin := policyBinary(t)
	f := newProjectFixture(t)

	runs := []struct {
		name string
		dir  string
		args []string
	}{
		{"doctor from the project's own working directory", f.ren, []string{"doctor"}},
		{"doctor <path>", f.plain, []string{"doctor", f.ren}},
		{"doctor --project <dir>", f.plain, []string{"doctor", "--project", f.ren}},
		{"doctor --project=<dir>", f.plain, []string{"doctor", "--project=" + f.ren}},
	}
	for _, r := range runs {
		out, code := runYakosIn(t, goBin, r.dir, r.args, f.env)
		if !strings.Contains(out, watchdogStop) || code != 1 {
			t.Errorf("%s: want the watchdog stop listed and exit 1 (an [err] line); exit %d\n%s", r.name, code, out)
		}
	}

	// A plain project, from its own working directory: the renamed agent is nobody's supervisor here.
	if out, _ := runYakosIn(t, goBin, f.plain, []string{"doctor"}, f.env); strings.Contains(out, "watchdog") {
		t.Errorf("a project that names no supervisor lists no watchdog budget:\n%s", out)
	}

	// --help documents the flag.
	help, code := runYakosIn(t, goBin, f.plain, []string{"doctor", "--help"}, f.env)
	if code != 0 || !strings.Contains(help, "--project <dir>") || !strings.Contains(help, "If --project <dir> is passed") {
		t.Errorf("doctor --help must document --project (exit %d):\n%s", code, help)
	}

	// --policy takes no project, by flag or by path: a usage error, and the report is not run.
	for _, args := range [][]string{{"doctor", "--policy", "--project", f.ren}, {"doctor", "--policy", "--project=" + f.ren}} {
		out, code := runYakosIn(t, goBin, f.plain, args, f.env)
		if code != 1 || strings.Contains(out, "Risky configurations") || !strings.Contains(out, "takes no --project") {
			t.Errorf("%v: want a usage error (exit 1) that names --project and no report; exit %d\n%s", args, code, out)
		}
	}

	// The budget CLI the probe also runs: check names the hard stop (exit 4), status lists the agent.
	out, code := runYakosIn(t, goBin, f.plain, []string{"budget", "check", "watchdog", "--project", f.ren}, f.env)
	if code != 4 || !strings.HasPrefix(out, "reason=budget_exhausted state=hard_stop agent=watchdog ") {
		t.Errorf("budget check watchdog --project: exit %d\n%s", code, out)
	}
	if out, _ := runYakosIn(t, goBin, f.ren, []string{"budget", "status"}, f.env); !strings.Contains(out, "watchdog") {
		t.Errorf("budget status in the project lists the renamed supervisor:\n%s", out)
	}
}

// The working directory default reaches ONLY the budgets section. A plain `yakos doctor` started in
// a project directory must not start running the project-wide checks (hook drift, hook binaries,
// the pre-push gate, the project rules: 14 more lines on an empty project) that a path argument
// turns on, so its output is byte for byte what it is anywhere else; the path argument, as the
// control, still runs them.
func TestDoctorProject_WorkingDirectoryDefaultRunsNoProjectChecks(t *testing.T) {
	goBin := policyBinary(t)
	home := t.TempDir() // no spend: no budget line anywhere
	env := policyEnv(home)
	elsewhere, proj := t.TempDir(), t.TempDir()
	writeProjectYML(t, proj, "supervisor:\n  agent: watchdog\n")
	if err := os.MkdirAll(filepath.Join(proj, "scripts", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	runYakosIn(t, goBin, elsewhere, []string{"doctor"}, env) // the first run materializes the framework lib and says so
	base, _ := runYakosIn(t, goBin, elsewhere, []string{"doctor"}, env)
	inProject, _ := runYakosIn(t, goBin, proj, []string{"doctor"}, env)
	if inProject != base {
		t.Errorf("a plain doctor in a project directory must print what it prints anywhere else:\n--- elsewhere\n%s\n--- in the project\n%s", base, inProject)
	}
	viaFlag, _ := runYakosIn(t, goBin, elsewhere, []string{"doctor", "--project", proj}, env)
	if viaFlag != base {
		t.Errorf("--project alone must not turn the project-wide checks on either:\n--- elsewhere\n%s\n--- --project\n%s", base, viaFlag)
	}
	control, _ := runYakosIn(t, goBin, elsewhere, []string{"doctor", proj}, env)
	for _, heading := range []string{"Project hook drift", "Pre-push version gate", "Project rules"} {
		if strings.Contains(base, heading) || !strings.Contains(control, heading) {
			t.Errorf("%q: the path argument turns it on (control) and nothing else does", heading)
		}
	}
}

// The ways --project is refused, and a refused invocation runs no report.
func TestDoctorProject_UsageErrors(t *testing.T) {
	goBin := policyBinary(t)
	f := newProjectFixture(t)
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"a path and --project", []string{"doctor", f.ren, "--project", f.ren}, "name the project once"},
		{"--project without a value", []string{"doctor", "--project"}, "--project requires a directory"},
		{"a directory that does not exist", []string{"doctor", "--project", filepath.Join(f.home, "no", "such")}, "--project must name an existing directory"},
		{"a regular file", []string{"doctor", "--project", notADir}, "--project must name an existing directory"},
		{"an empty value", []string{"doctor", "--project", ""}, "--project must name an existing directory"},
		{"a bare equals", []string{"doctor", "--project="}, "unknown flag"},
	} {
		out, code := runYakosIn(t, goBin, f.plain, c.args, f.env)
		if code != 1 || !strings.Contains(out, c.want) || strings.Contains(out, "Required commands") {
			t.Errorf("%s: want exit 1, %q and no report; exit %d\n%s", c.name, c.want, code, out)
		}
	}
}

// Through the real command: the budget lines carry no path and no text from the project file, even
// for a malformed .yakos.yml whose YAML error would echo its keys, and the project directory
// itself is not printed (the project-wide checks, which do print it, are not run).
func TestDoctorProject_BudgetLinesCarryNothingFromTheProjectFile(t *testing.T) {
	goBin := policyBinary(t)
	f := newProjectFixture(t)
	bad := t.TempDir()
	writeProjectYML(t, bad, "supervisor:\n  agent: watchdog\nagent_budgets:\n  SENTINELKEY: 1\n  SENTINELKEY: 2\n")

	for _, args := range [][]string{{"doctor", "--project", bad}, {"doctor"}} {
		dir := f.plain
		if len(args) == 1 {
			dir = bad // the working directory default
		}
		out, _ := runYakosIn(t, goBin, dir, args, f.env)
		if !strings.Contains(out, watchdogStop) {
			t.Fatalf("%v: the supervisor name the line scan reads from the malformed file is listed:\n%s", args, out)
		}
		for _, leak := range []string{"SENTINEL", "already defined", "unmarshal", "agent_budgets", bad} {
			if strings.Contains(out, leak) {
				t.Errorf("%v: the report printed project-sourced text or the project path (%q):\n%s", args, leak, out)
			}
		}
	}
}

// doctor is always served by the Go implementation (the bash doctor has no --project), whatever
// YAKOS_IMPL says short of an explicit bash.
func TestIsDoctorForceGo_ProjectFlag(t *testing.T) {
	for _, impl := range []string{"", "go"} {
		for _, args := range [][]string{{"doctor", "--project", "/x"}, {"doctor", "--project=/x"}} {
			if !isDoctorForceGo(impl, args) {
				t.Errorf("YAKOS_IMPL=%q: %v must reach the Go implementation", impl, args)
			}
		}
	}
}
