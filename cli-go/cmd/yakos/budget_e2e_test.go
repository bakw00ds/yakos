package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// TestBudgetHelperMain re-enters main() inside a subprocess of the test
// binary, so the real router (including the bash-passthrough gate and the
// dispatch exit-code mapping) runs without a prebuilt yakos.
func TestBudgetHelperMain(t *testing.T) {
	raw := os.Getenv("YAKOS_TEST_MAIN_ARGS")
	if raw == "" {
		t.Skip("helper process only")
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"yakos"}, args...)
	main()
	os.Exit(0)
}

// runYakos runs the router with args against an isolated state dir. PATH holds
// no claude or bash yakos, so a dispatch that is NOT refused fails harmlessly
// instead of spending money.
func runYakos(t *testing.T, state string, env []string, args ...string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetHelperMain$")
	cmd.Env = append([]string{
		"YAKOS_TEST_MAIN_ARGS=" + string(b),
		"YAKOS_DISPATCH_LOG=" + state,
		"HOME=" + t.TempDir(),
		"PATH=" + t.TempDir(),
	}, env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String()
}

func hardStopState(t *testing.T, agent string) string {
	t.Helper()
	state := t.TempDir()
	if err := budget.SetLimit(state, agent, 5, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":%q,"usage":{"total_cost_usd":6}}`+"\n", time.Now().UTC().Format(time.RFC3339), agent)
	if err := os.WriteFile(filepath.Join(state, "dispatch-log.ndjson"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}

// The bash passthrough has no budget of its own: the router must refuse
// (exit 4) before handing the dispatch to it. A deleted gate would reach the
// passthrough, which fails with exit 1 here (no bash yakos).
func TestBudgetGateBlocksBashPassthrough(t *testing.T) {
	state := hardStopState(t, "backend")
	proj := t.TempDir()
	for _, impl := range []string{"bash"} {
		code, out := runYakos(t, state, []string{"YAKOS_IMPL=" + impl}, "dispatch", "backend", "do it", "--project", proj)
		if code != budget.ExitHardStop || !strings.Contains(out, "dispatch refused") {
			t.Fatalf("YAKOS_IMPL=%s: want exit %d with a refusal, got %d:\n%s", impl, budget.ExitHardStop, code, out)
		}
	}
	// An agent with no budget is not touched by the gate.
	code, out := runYakos(t, state, []string{"YAKOS_IMPL=bash"}, "dispatch", "frontend", "do it", "--project", proj)
	if code == budget.ExitHardStop || strings.Contains(out, "dispatch refused") {
		t.Fatalf("unbudgeted agent must not be refused: %d\n%s", code, out)
	}
}

// The Go dispatch maps a refusal to exit 4, not the generic 1.
func TestDispatchExitCodeIsFourOnHardStop(t *testing.T) {
	state := hardStopState(t, "backend")
	code, out := runYakos(t, state, []string{"YAKOS_IMPL=go"}, "dispatch", "backend", "do it", "--project", t.TempDir())
	if code != budget.ExitHardStop || !strings.Contains(out, "dispatch refused") {
		t.Fatalf("want exit %d with a refusal, got %d:\n%s", budget.ExitHardStop, code, out)
	}
}

func TestBudgetCheckCLIMachineReason(t *testing.T) {
	state := hardStopState(t, "supervisor")
	code, out := runYakos(t, state, nil, "budget", "check", "supervisor")
	if code != budget.ExitHardStop || !strings.HasPrefix(out, "reason=budget_exhausted state=hard_stop agent=supervisor") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	code, out = runYakos(t, t.TempDir(), nil, "budget", "check", "frontend")
	if code != 0 || !strings.HasPrefix(out, "reason=budget_off") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
}

func TestPassthroughClampsExplicitModel(t *testing.T) {
	state := t.TempDir()
	if err := budget.SetMaxModel(state, "supervisor", "haiku"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YAKOS_DISPATCH_LOG", state)
	got, _ := clampDispatchModel([]string{"dispatch", "supervisor", "t", "--model", "opus", "--project", "/p"}, "supervisor", "/p")
	if strings.Join(got, " ") != "dispatch supervisor t --model haiku --project /p" {
		t.Fatalf("%v", got)
	}
	got, _ = clampDispatchModel([]string{"dispatch", "supervisor", "t", "--model=opus"}, "supervisor", "")
	if got[3] != "--model=haiku" {
		t.Fatalf("%v", got)
	}
	if got, _ := clampDispatchModel([]string{"dispatch", "supervisor", "t", "--model", "haiku"}, "supervisor", ""); got[4] != "haiku" {
		t.Fatalf("%v", got)
	}
}

// sec-339 M1: the passthrough clamp knows the project, so an agent the project
// names as its supervisor keeps the supervisor's ceiling, and a model the registry
// cannot rank under a ceiling is refused, as on the Go-native path.
func TestPassthroughClampKnowsTheProject(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: watchdog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := clampDispatchModel([]string{"dispatch", "watchdog", "t", "--model", "opus", "--project", proj}, "watchdog", proj)
	if err != nil || got[4] != "sonnet" {
		t.Fatalf("renamed supervisor not clamped: %v %v", got, err)
	}
	if _, err := clampDispatchModel([]string{"dispatch", "supervisor", "t", "--model", "claude-opus-5-5"}, "supervisor", proj); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Fatalf("an unranked id under a ceiling must be refused, got %v", err)
	}
	// An agent with no ceiling is untouched, unranked id or not.
	if got, err := clampDispatchModel([]string{"dispatch", "backend", "t", "--model", "claude-opus-5-5"}, "backend", proj); err != nil || got[4] != "claude-opus-5-5" {
		t.Fatalf("no ceiling: %v %v", got, err)
	}
}

// The gate must hand the CLAMPED argv to the passthrough (a surviving mutant
// returned args unchanged).
func TestBudgetGateReturnsClampedArgs(t *testing.T) {
	state := t.TempDir()
	if err := budget.SetMaxModel(state, "backend", "haiku"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YAKOS_DISPATCH_LOG", state)
	in := []string{"dispatch", "backend", "t", "--model", "opus", "--project", t.TempDir()}
	got := budgetGateBeforePassthrough(in, "")
	if got[4] != "haiku" {
		t.Fatalf("gate returned %v, want --model haiku", got)
	}
	if in[4] != "opus" {
		t.Fatal("the caller's slice must not be mutated")
	}
	// The built-in supervisor ceiling applies with no policy file at all.
	got = budgetGateBeforePassthrough([]string{"dispatch", "supervisor", "t", "--model", "opus", "--project", t.TempDir()}, "")
	if got[4] != "sonnet" {
		t.Fatalf("built-in supervisor ceiling not applied: %v", got)
	}
}

// K-116 round 2: a passthrough dispatch with NO --model whose agent is pinned
// to opus in frontmatter must still be clamped to the ceiling.
func TestBudgetGateClampsFrontmatterPin(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"supervisor", "backend"} {
		body := "---\nid: " + n + "\nmodel: opus\n---\n\n## Purpose\n\nx.\n"
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := t.TempDir()
	got := budgetGateBeforePassthrough([]string{"dispatch", "supervisor", "t", "--project", proj}, root)
	if strings.Join(got[len(got)-2:], " ") != "--model sonnet" {
		t.Fatalf("opus-pinned supervisor not clamped: %v", got)
	}
	got = budgetGateBeforePassthrough([]string{"dispatch", "backend", "t", "--project", proj}, root)
	if hasModelFlag(got) {
		t.Fatalf("agent without a ceiling must be untouched: %v", got)
	}
}
