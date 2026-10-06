package main

// doctor_project_test.go — K-136: `yakos doctor --project <dir>`. The behaviour is tested in process, because
// a case that launches the 47 MB binary costs real time on the macOS CI runner (this package ran 301 s
// against the 5 minute ceiling): the arguments resolve through parseDoctorArgs, with the working directory
// injected, and the report runs through doctor.Run over an injected HOME, with a captured writer. Two cases
// drive the built binary (TestDoctorProject_BuiltBinary), the only ones that prove what package-level tests
// miss: that the help text and the flag are wired into the command people run, and its exit status.
// TestDoctorProject_Q4Probe is rev-330's probe q4 (doctor_project_probe_q4.sh) as a committed test.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/doctor"
)

// projectFixture is a scratch HOME whose state directory holds one subscription run of the renamed
// supervisor "watchdog" (70M tokens, $0: past the supervisor's 33M limit and its 66M stop, so a project
// that names watchdog its supervisor has it at a hard stop), and two projects: one naming watchdog as its
// supervisor, one that does not.
type projectFixture struct {
	home, ren, plain string
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
	return f
}

func writeProjectYML(t *testing.T, dir, yml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
}

const watchdogStop = "LLM supervision disabled: watchdog budget exhausted (token limit reached: 70,000,000 of 33,000,000 tokens"

// doctorRun runs `yakos doctor <args>` as if it were started in cwd, without a process: the arguments
// resolve through parseDoctorArgs and the report runs through doctor.Run over home, with every
// environment variable empty and no command on PATH, as the doctor package's own tests run it. An
// argument error is returned, not printed (runDoctor prints it and exits 1). The report's Errors is the
// exit status: runDoctor exits 1 when there is one.
func doctorRun(t *testing.T, home, cwd string, args ...string) (string, *doctor.Report, error) {
	t.Helper()
	a, err := parseDoctorArgs(args, func() (string, error) { return cwd, nil })
	if err != nil {
		return "", nil, err
	}
	cfg := a.config()
	var buf bytes.Buffer
	cfg.Writer, cfg.ErrWriter = &buf, &buf
	cfg.HomeDir = home
	cfg.Environ = func(string) string { return "" }
	cfg.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	rep, err := doctor.Run(cfg)
	if err != nil {
		t.Fatalf("doctor.Run: %v", err)
	}
	return buf.String(), rep, nil
}

// rev-330's probe q4: the renamed supervisor and `yakos doctor`, in every form the docs promise
// (--project <dir>, else the working directory) and the form that already worked (a path).
func TestDoctorProject_Q4Probe(t *testing.T) {
	f := newProjectFixture(t)

	runs := []struct {
		name string
		cwd  string
		args []string
	}{
		{"doctor from the project's own working directory", f.ren, nil},
		{"doctor <path>", f.plain, []string{f.ren}},
		{"doctor --project <dir>", f.plain, []string{"--project", f.ren}},
		{"doctor --project=<dir>", f.plain, []string{"--project=" + f.ren}},
	}
	for _, r := range runs {
		out, rep, err := doctorRun(t, f.home, r.cwd, r.args...)
		if err != nil {
			t.Errorf("%s: %v", r.name, err)
			continue
		}
		if !strings.Contains(out, watchdogStop) || rep.Errors == 0 {
			t.Errorf("%s: want the watchdog stop listed and an error finding (exit 1); %d errors\n%s", r.name, rep.Errors, out)
		}
	}

	// A plain project, from its own working directory: the renamed agent is nobody's supervisor here.
	if out, _, err := doctorRun(t, f.home, f.plain); err != nil || strings.Contains(out, "watchdog") {
		t.Errorf("a project that names no supervisor lists no watchdog budget (err %v):\n%s", err, out)
	}

	// --help is resolved before anything else, and documents the flag.
	if a, err := parseDoctorArgs([]string{"--help"}, os.Getwd); err != nil || !a.help {
		t.Errorf("--help: %+v, %v", a, err)
	}
	var help bytes.Buffer
	doctor.PrintHelp(&help)
	if !strings.Contains(help.String(), "--project <dir>") || !strings.Contains(help.String(), "If --project <dir> is passed") {
		t.Errorf("doctor --help must document --project:\n%s", help.String())
	}

	// --policy takes no project, by flag or by path: a usage error, and the report is not run.
	for _, args := range [][]string{{"--policy", "--project", f.ren}, {"--policy", "--project=" + f.ren}} {
		out, _, err := doctorRun(t, f.home, f.plain, args...)
		if err == nil || err.Error() != "doctor: --policy reads your user-level setup and takes no --project" || out != "" {
			t.Errorf("%v: want the usage error and no report; err %v\n%s", args, err, out)
		}
	}

	// The budget CLI half of the probe is what the doctor line is made of: the project's supervisor is at
	// its hard stop, a project that does not name it leaves it with no budget, and status lists it. The
	// CLI's own line and exit status (4) are pinned by TestBudgetCheck_RenamedSupervisorKeepsTheBuiltinBudget.
	state := filepath.Join(f.home, ".yakos-state")
	st, err := budget.Evaluate("watchdog", budget.Options{StateDir: state, Project: f.ren})
	if err != nil || st.State != budget.StateHardStop || st.Reason != budget.ReasonExhausted ||
		st.LimitTokens != 33_000_000 || st.SpentTokens != 70_000_000 {
		t.Errorf("watchdog under a project that names it: %+v, %v", st, err)
	}
	if st, err := budget.Evaluate("watchdog", budget.Options{StateDir: state, Project: f.plain}); err != nil || st.State != budget.StateOff {
		t.Errorf("watchdog under a project that does not name it has no budget: %+v, %v", st, err)
	}
	pol, _ := budget.LoadPolicy(state)
	if names := budget.AgentNamesForProject(pol, f.ren); !strings.Contains(strings.Join(names, " "), "watchdog") {
		t.Errorf("status lists the renamed supervisor: %v", names)
	}
}

// The working directory default reaches ONLY the budgets section. A plain `yakos doctor` started in
// a project directory must not start running the project-wide checks (hook drift, hook binaries,
// the pre-push gate, the project rules: 14 more lines on an empty project) that a path argument
// turns on, so its output is byte for byte what it is anywhere else; the path argument, as the
// control, still runs them.
func TestDoctorProject_WorkingDirectoryDefaultRunsNoProjectChecks(t *testing.T) {
	home := t.TempDir() // no spend: no budget line anywhere
	elsewhere, proj := t.TempDir(), t.TempDir()
	writeProjectYML(t, proj, "supervisor:\n  agent: watchdog\n")
	if err := os.MkdirAll(filepath.Join(proj, "scripts", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(cwd string, args ...string) string {
		t.Helper()
		out, _, err := doctorRun(t, home, cwd, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}
	base := run(elsewhere)
	if inProject := run(proj); inProject != base {
		t.Errorf("a plain doctor in a project directory must print what it prints anywhere else:\n--- elsewhere\n%s\n--- in the project\n%s", base, inProject)
	}
	if viaFlag := run(elsewhere, "--project", proj); viaFlag != base {
		t.Errorf("--project alone must not turn the project-wide checks on either:\n--- elsewhere\n%s\n--- --project\n%s", base, viaFlag)
	}
	control := run(elsewhere, proj)
	for _, heading := range []string{"Project hook drift", "Pre-push version gate", "Project rules"} {
		if strings.Contains(base, heading) || !strings.Contains(control, heading) {
			t.Errorf("%q: the path argument turns it on (control) and nothing else does", heading)
		}
	}
}

// The ways --project is refused, with the exact text runDoctor prints before it exits 1; and the
// refusals that were already there keep theirs, byte for byte (the argument handling moved into
// parseDoctorArgs so these can be tested without a process).
func TestDoctorProject_UsageErrors(t *testing.T) {
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
		{"a path and --project", []string{f.ren, "--project", f.ren}, "doctor: name the project once, as a path argument or with --project"},
		{"--project without a value", []string{"--project"}, "doctor: --project requires a directory"},
		{"a directory that does not exist", []string{"--project", filepath.Join(f.home, "no", "such")}, "doctor: --project must name an existing directory"},
		{"a regular file", []string{"--project", notADir}, "doctor: --project must name an existing directory"},
		{"an empty value", []string{"--project", ""}, "doctor: --project must name an existing directory"},
		{"a bare equals", []string{"--project="}, `doctor: unknown flag "--project="`},
		{"--policy with a path", []string{"--policy", "/some/project"}, "doctor: --policy reads your user-level setup and takes no project path"},
		{"--policy with another mode", []string{"--policy", "--preflight"}, "doctor: --policy runs on its own; it cannot be combined with --preflight, --probe-runtime, --probe-decision or --production"},
		{"--live alone", []string{"--live"}, "doctor: --live only applies to --probe-decision"},
		{"--fix", []string{"--fix"}, "doctor: --fix is not yet implemented in the Go port (see ideas wishlist rank 5)\n  Use 'YAKOS_IMPL=bash yakos doctor --fix' to reach the bash implementation."},
		{"an unknown flag", []string{"-x"}, `doctor: unknown flag "-x"`},
		{"two paths", []string{"/a", "/b"}, "doctor: too many positional args"},
	} {
		_, err := parseDoctorArgs(c.args, os.Getwd)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v\n got error %v\nwant error %q", c.name, c.args, err, c.want)
		}
	}
}

// parseDoctorArgs resolves the two projects the command knows: the positional path, which alone turns the
// project-wide checks on, and the one the Agent budgets section reads (--project, else the path, else the
// working directory; none when even that is unknown). --help wins before anything else is checked.
func TestParseDoctorArgs_ResolvesTheProjects(t *testing.T) {
	d := t.TempDir()
	cwd := func() (string, error) { return "/the/cwd", nil }
	for _, c := range []struct {
		name          string
		args          []string
		getwd         func() (string, error)
		path, project string
	}{
		{"nothing: the working directory, for the budgets section only", nil, cwd, "", "/the/cwd"},
		{"a path: both", []string{"/some/project"}, cwd, "/some/project", "/some/project"},
		{"--project: the budgets section only", []string{"--project", d}, cwd, "", d},
		{"--project=: the budgets section only", []string{"--project=" + d}, cwd, "", d},
		{"no working directory either", nil, func() (string, error) { return "", os.ErrNotExist }, "", ""},
	} {
		a, err := parseDoctorArgs(c.args, c.getwd)
		if err != nil || a.projectPath != c.path || a.budgetProject != c.project {
			t.Errorf("%s: %+v, %v; want project path %q and budgets project %q", c.name, a, err, c.path, c.project)
		}
	}
	// The configuration they ask for keeps the two apart.
	a, _ := parseDoctorArgs(nil, cwd)
	if cfg := a.config(); cfg.ProjectPath != "" || cfg.BudgetProject != "/the/cwd" {
		t.Errorf("the working directory default must reach BudgetProject only: %+v", cfg)
	}
	if a, err := parseDoctorArgs([]string{"--help", "--fix", "--live"}, cwd); err != nil || !a.help {
		t.Errorf("--help is answered before any other argument is checked: %+v, %v", a, err)
	}
}

// Through the doctor report: the budget lines carry no path and no text from the project file, even
// for a malformed .yakos.yml whose YAML error would echo its keys, and the project directory
// itself is not printed (the project-wide checks, which do print it, are not run).
func TestDoctorProject_BudgetLinesCarryNothingFromTheProjectFile(t *testing.T) {
	f := newProjectFixture(t)
	bad := t.TempDir()
	writeProjectYML(t, bad, "supervisor:\n  agent: watchdog\nagent_budgets:\n  SENTINELKEY: 1\n  SENTINELKEY: 2\n")

	for _, c := range []struct {
		name string
		cwd  string
		args []string
	}{
		{"--project", f.plain, []string{"--project", bad}},
		{"the working directory default", bad, nil},
	} {
		out, _, err := doctorRun(t, f.home, c.cwd, c.args...)
		if err != nil || !strings.Contains(out, watchdogStop) {
			t.Fatalf("%s: the supervisor name the line scan reads from the malformed file is listed (err %v):\n%s", c.name, err, out)
		}
		for _, leak := range []string{"SENTINEL", "already defined", "unmarshal", "agent_budgets", bad} {
			if strings.Contains(out, leak) {
				t.Errorf("%s: the report printed project-sourced text or the project path (%q):\n%s", c.name, leak, out)
			}
		}
	}
}

// The two cases that drive the built binary: the help text and the flag are wired into the command people
// run (the registry and runDoctor, which package-level tests do not reach), and its exit status is the
// contract hooks and scripts read: 1 when the report holds an error finding, here the project's supervisor
// at its hard stop, 0 for --help.
func TestDoctorProject_BuiltBinary(t *testing.T) {
	goBin := policyBinary(t)
	f := newProjectFixture(t)
	env := policyEnv(f.home)

	help, code := runGoDoctor(t, goBin, []string{"doctor", "--help"}, env)
	if code != 0 || !strings.Contains(help, "[--project <dir>]") || !strings.Contains(help, "If --project <dir> is passed") {
		t.Errorf("doctor --help must document --project (exit %d):\n%s", code, help)
	}

	out, code := runGoDoctor(t, goBin, []string{"doctor", "--project", f.ren}, env)
	if code != 1 || !strings.Contains(out, watchdogStop) {
		t.Errorf("doctor --project <dir>: want the watchdog stop listed and exit 1; exit %d\n%s", code, out)
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
