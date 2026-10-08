package main

// cmd_flows_test.go: `yakos flows schedule enable|disable` (K-172). Each test
// runs against a scratch HOME and a scratch project; none touches real state.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/workflow"
)

const flowsTestWF = `version: 1
name: hooked
triggers:
  cron: "0 9 * * *"
  webhook:
    secret_env: YAKOS_FLOWS_TEST_SECRET
nodes:
  - id: a
    agent: reviewer
    prompt: "p"
    output_limit: 100
`

type flowsRig struct {
	t        *testing.T
	project  string
	stateDir string
}

func newFlowsRig(t *testing.T, wfBody string) *flowsRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir()) // a project's decoy: must be ignored
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(state, 0o700)
	project := filepath.Join(t.TempDir(), "proj")
	dir := filepath.Join(project, "work", "current", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if wfBody != "" {
		if err := os.WriteFile(filepath.Join(dir, "hooked.yaml"), []byte(wfBody), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lastStateDir = state
	return &flowsRig{t: t, project: project, stateDir: state}
}

func (r *flowsRig) do(args ...string) (int, string, string) {
	r.t.Helper()
	var out, errb bytes.Buffer
	code := flowsMain(args, &out, &errb, r.project, statepath.TrustedDir())
	return code, out.String(), errb.String()
}

func TestFlowsScheduleEnableDisable(t *testing.T) {
	r := newFlowsRig(t, flowsTestWF)
	code, out, errs := r.do("schedule", "enable", "hooked")
	if code != 0 || !strings.HasPrefix(out, "ok: enabled hooked (cron+webhook), pinned to workflow sha ") {
		t.Fatalf("enable: exit %d out=%q err=%q", code, out, errs)
	}
	if strings.Contains(out+errs, r.stateDir) || strings.Contains(out+errs, r.project) {
		t.Errorf("output names a path: %s%s", out, errs)
	}
	s, err := workflow.LoadSchedules(r.project)
	if err != nil {
		t.Fatalf("daemon cannot load the file: %v", err)
	}
	e := s.Workflows["hooked"]
	if !e.Cron || !e.Webhook || e.SecretEnv != "YAKOS_FLOWS_TEST_SECRET" || len(e.WorkflowSHA) != 64 {
		t.Fatalf("entry = %+v", e)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(workflow.SchedulesPath(r.project)); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
	lines := configChangedLines(r.stateDir)
	if len(lines) != 1 || lines[0]["action"] != "flows.schedule.enable" || lines[0]["surface"] != "cli" || len(lines[0]["policy_sha_after"].(string)) != 64 {
		t.Fatalf("audit = %v", lines)
	}
	// Same again: nothing changes, nothing is audited.
	if code, out, _ := r.do("schedule", "enable", "hooked"); code != 0 || !strings.HasPrefix(out, "unchanged:") {
		t.Fatalf("second enable: exit %d out=%q", code, out)
	}
	if n := len(configChangedLines(r.stateDir)); n != 1 {
		t.Errorf("audit lines = %d after an unchanged enable, want 1", n)
	}

	code, out, errs = r.do("schedule", "disable", "hooked")
	if code != 0 || !strings.HasPrefix(out, "ok: disabled hooked") {
		t.Fatalf("disable: exit %d out=%q err=%q", code, out, errs)
	}
	if s, _ := workflow.LoadSchedules(r.project); len(s.Workflows) != 0 {
		t.Fatalf("still enabled: %v", s.Workflows)
	}
	if code, out, _ := r.do("schedule", "disable", "hooked"); code != 0 || !strings.HasPrefix(out, "unchanged:") {
		t.Fatalf("disable twice: exit %d out=%q", code, out)
	}
}

func TestFlowsScheduleEnableSingleTriggerAndProjectFlag(t *testing.T) {
	r := newFlowsRig(t, flowsTestWF)
	other := t.TempDir() // cwd elsewhere; --project names the project
	var out, errb bytes.Buffer
	code := flowsMain([]string{"schedule", "enable", "hooked", "--webhook", "--project", r.project}, &out, &errb, other, statepath.TrustedDir())
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errb.String())
	}
	s, _ := workflow.LoadSchedules(r.project)
	if e := s.Workflows["hooked"]; e.Cron || !e.Webhook {
		t.Fatalf("entry = %+v, want webhook only", e)
	}
}

func TestFlowsScheduleRefusals(t *testing.T) {
	r := newFlowsRig(t, flowsTestWF)
	for name, args := range map[string][]string{
		"no subcommand":    {"schedule"},
		"unknown sub":      {"schedule", "toggle", "hooked"},
		"missing workflow": {"schedule", "enable"},
		"bad name":         {"schedule", "enable", "../x"},
		"absent workflow":  {"schedule", "enable", "nope"},
		"extra argument":   {"schedule", "enable", "hooked", "second"},
		"not a flows verb": {"unschedule", "hooked"},
		"enable-only flag": {"schedule", "disable", "hooked", "--cron"},
		"project no value": {"schedule", "enable", "hooked", "--project"},
	} {
		if code, out, errs := r.do(args...); code == 0 {
			t.Errorf("%s: exit 0 (out=%q err=%q)", name, out, errs)
		}
	}
	if s, _ := workflow.LoadSchedules(r.project); len(s.Workflows) != 0 {
		t.Fatalf("a refused command wrote %v", s.Workflows)
	}
	// A workflow without the requested trigger.
	cronOnly := strings.Replace(flowsTestWF, "  webhook:\n    secret_env: YAKOS_FLOWS_TEST_SECRET\n", "", 1)
	if err := os.WriteFile(filepath.Join(r.project, "work", "current", "workflows", "hooked.yaml"), []byte(cronOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := r.do("schedule", "enable", "hooked", "--webhook"); code == 0 {
		t.Error("enabled an undeclared webhook")
	}
	if code, _, _ := r.do("schedule", "enable", "hooked"); code != 0 {
		t.Error("default enable of a cron-only workflow failed")
	}
	if s, _ := workflow.LoadSchedules(r.project); s.Workflows["hooked"].Webhook {
		t.Error("webhook switched on")
	}
}

func TestFlowsScheduleNoTriggersDeclared(t *testing.T) {
	plain := strings.Replace(flowsTestWF, "triggers:\n  cron: \"0 9 * * *\"\n  webhook:\n    secret_env: YAKOS_FLOWS_TEST_SECRET\n", "", 1)
	r := newFlowsRig(t, plain)
	if code, _, errs := r.do("schedule", "enable", "hooked"); code == 0 || !strings.Contains(errs, "declares no triggers") {
		t.Fatalf("exit %d err=%q", code, errs)
	}
}

func TestIsFlowsForceGo(t *testing.T) {
	if !isFlowsForceGo([]string{"flows", "schedule"}) {
		t.Error("flows must be force-go")
	}
	for _, a := range [][]string{nil, {"workflow"}, {"schedule", "flows"}} {
		if isFlowsForceGo(a) {
			t.Errorf("%v must not be", a)
		}
	}
}

func TestFlowsHelp(t *testing.T) {
	r := newFlowsRig(t, "")
	if code, out, _ := r.do("--help"); code != 0 || !strings.Contains(out, "schedule enable") {
		t.Errorf("help: exit %d %q", code, out)
	}
	if code, _, _ := r.do(); code != 1 {
		t.Errorf("no args: exit %d, want 1", code)
	}
}
