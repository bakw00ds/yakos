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
	home := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetHelperMain$")
	cmd.Env = append([]string{
		"YAKOS_TEST_MAIN_ARGS=" + string(b),
		"YAKOS_DISPATCH_LOG=" + state,
		"HOME=" + home, "USERPROFILE=" + home, // Windows reads the profile dir, not HOME
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
	got, _ := passthroughClamped([]string{"dispatch", "supervisor", "t", "--model", "opus", "--project", "/p"}, "supervisor", "/p", "")
	if modelOf(got) != "haiku" {
		t.Fatalf("%v", got)
	}
	got, _ = passthroughClamped([]string{"dispatch", "supervisor", "t", "--model=opus"}, "supervisor", "", "")
	if modelOf(got) != "haiku" {
		t.Fatalf("%v", got)
	}
	if got, _ := passthroughClamped([]string{"dispatch", "supervisor", "t", "--model", "haiku"}, "supervisor", "", ""); modelOf(got) != "haiku" {
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
	got, err := passthroughClamped([]string{"dispatch", "watchdog", "t", "--model", "opus", "--project", proj}, "watchdog", proj, "")
	if err != nil || modelOf(got) != "sonnet" {
		t.Fatalf("renamed supervisor not clamped: %v %v", got, err)
	}
	if _, err := passthroughClamped([]string{"dispatch", "supervisor", "t", "--model", "claude-opus-5-5"}, "supervisor", proj, ""); err == nil ||
		!strings.Contains(err.Error(), "refused") {
		t.Fatalf("an unranked id under a ceiling must be refused, got %v", err)
	}
	// An agent with no ceiling is untouched, unranked id or not.
	if got, err := passthroughClamped([]string{"dispatch", "backend", "t", "--model", "claude-opus-5-5"}, "backend", proj, ""); err != nil || got[4] != "claude-opus-5-5" {
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
	if modelOf(got) != "haiku" {
		t.Fatalf("gate returned %v, want --model haiku", got)
	}
	if in[4] != "opus" {
		t.Fatal("the caller's slice must not be mutated")
	}
	// The built-in supervisor ceiling applies with no policy file at all.
	got = budgetGateBeforePassthrough([]string{"dispatch", "supervisor", "t", "--model", "opus", "--project", t.TempDir()}, "")
	if modelOf(got) != "sonnet" {
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
	if modelOf(got) != "sonnet" {
		t.Fatalf("opus-pinned supervisor not clamped: %v", got)
	}
	got = budgetGateBeforePassthrough([]string{"dispatch", "backend", "t", "--project", proj}, root)
	if modelOf(got) != "" {
		t.Fatalf("agent without a ceiling must be untouched: %v", got)
	}
}

// K-168 (sec-339b M2): with no --model the bash dispatch runs model-policy, else
// model, else a fixed sonnet. The clamp ranks that effective model, so a no-model
// agent cannot run sonnet over a haiku ceiling and a model-policy line cannot lift
// an agent over its ceiling.
func TestPassthroughClampRanksTheBashEffectiveModel(t *testing.T) {
	state := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", state)
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := map[string]string{
		"nomodel": "id: nomodel\n",
		"pinned":  "id: pinned\nmodel: haiku\nmodel-policy: opus\n",
		"aliased": "id: aliased\nmodel: best\n",
		"cheap":   "id: cheap\nmodel: haiku\n",
	}
	for n, fm := range agents {
		body := "---\n" + fm + "---\n\n## Purpose\n\nx.\n"
		if err := os.WriteFile(filepath.Join(dir, n+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	proj := t.TempDir()
	for _, n := range []string{"nomodel", "pinned", "aliased"} {
		if err := budget.SetMaxModel(state, n, "haiku"); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"nomodel", "pinned", "aliased"} {
		got, err := passthroughClamped([]string{"dispatch", n, "t", "--project", proj}, n, proj, root)
		if err != nil || modelOf(got) != "haiku" {
			t.Errorf("%s: effective model not clamped to haiku: %v %v", n, got, err)
		}
	}
	// Within the ceiling, or no ceiling: argv untouched.
	got, err := passthroughClamped([]string{"dispatch", "cheap", "t"}, "cheap", proj, root)
	if err != nil || modelOf(got) != "" {
		t.Errorf("cheap has no ceiling: %v %v", got, err)
	}
	if err := budget.SetMaxModel(state, "pinned", "opus"); err != nil {
		t.Fatal(err)
	}
	// Within the ceiling the model is still pinned (K-168 sec-364 H1): bash must
	// not be left to resolve a dearer one from a file the clamp did not rank.
	got, err = passthroughClamped([]string{"dispatch", "pinned", "t"}, "pinned", proj, root)
	if err != nil || modelOf(got) != "opus" {
		t.Errorf("opus policy under an opus ceiling must be pinned to opus: %v %v", got, err)
	}
	// The built-in supervisor ceiling (sonnet) clamps a model-policy: opus line.
	body := "---\nid: watchdog\nmodel: haiku\nmodel-policy: opus\n---\n\n## Purpose\n\nx.\n"
	if err := os.WriteFile(filepath.Join(dir, "watchdog.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: watchdog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = passthroughClamped([]string{"dispatch", "watchdog", "t", "--project", proj}, "watchdog", proj, root)
	if err != nil || modelOf(got) != "sonnet" {
		t.Errorf("renamed supervisor with model-policy: opus: %v %v", got, err)
	}
}

// writeAgentFile writes a roster agent with the given frontmatter lines.
func writeAgentFile(t *testing.T, dir, file, fm string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\n" + fm + "---\n\n## Purpose\n\nx.\n"
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// K-168 sec-364 H1: under a ceiling a dispatch with no --model always carries a
// pin, so bash cannot run a dearer model from a file the clamp did not read.
func TestPassthroughClampAlwaysPinsUnderCeiling(t *testing.T) {
	state := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", state)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_RUNTIME", "")
	root, proj := t.TempDir(), t.TempDir()
	writeAgentFile(t, filepath.Join(root, "lib", "agents"), "backend.md", "id: backend\nmodel: haiku\n")
	if err := budget.SetMaxModel(state, "backend", "sonnet"); err != nil {
		t.Fatal(err)
	}
	got, err := passthroughClamped([]string{"dispatch", "backend", "t"}, "backend", proj, root)
	if err != nil || modelOf(got) != "haiku" || !strings.HasSuffix(strings.Join(got, " "), "--model haiku --runtime claude") {
		t.Fatalf("a within-ceiling agent must be pinned to model and runtime: %v %v", got, err)
	}
	// An agent the roster does not hold (bash may find it elsewhere) is pinned too,
	// to the model bash would default to.
	if err := budget.SetMaxModel(state, "ghost", "haiku"); err != nil {
		t.Fatal(err)
	}
	got, err = passthroughClamped([]string{"dispatch", "ghost", "t"}, "ghost", proj, root)
	if err != nil || modelOf(got) != "haiku" {
		t.Fatalf("unknown agent under a ceiling must be pinned: %v %v", got, err)
	}
	// An explicit runtime is respected, not overwritten.
	// An explicit runtime is re-pinned LAST (bash lets the last one win), so
	// `--runtime auto` cannot fall through to a fallback runtime (sec-364b N2).
	for _, rt := range []string{"claude", "auto"} {
		got, err = passthroughClamped([]string{"dispatch", "backend", "t", "--runtime", rt}, "backend", proj, root)
		if err != nil || !strings.HasSuffix(strings.Join(got, " "), "--runtime claude") {
			t.Fatalf("--runtime %s must be re-pinned last: %v %v", rt, got, err)
		}
	}
}

// K-168 sec-364 M1: the runtime resolves from frontmatter, .yakos.yml and
// YAKOS_RUNTIME before ranking, so a codex agent is never ranked as claude.
func TestPassthroughClampResolvesRuntimeBeforeRanking(t *testing.T) {
	state := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", state)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_RUNTIME", "")
	root, proj := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	writeAgentFile(t, dir, "cx-none.md", "id: cx-none\nruntime: codex\n")
	writeAgentFile(t, dir, "cx-opus.md", "id: cx-opus\nruntime: codex\nmodel: opus\n")
	writeAgentFile(t, dir, "cx-gpt.md", "id: cx-gpt\nruntime: codex\nmodel: gpt-5.5\n")
	writeAgentFile(t, dir, "plain.md", "id: plain\nmodel: sonnet\nrole-domain: x\ndomain: dev\n")
	for _, n := range []string{"cx-none", "cx-opus", "cx-gpt", "plain"} {
		if err := budget.SetMaxModel(state, n, "sonnet"); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"cx-none", "cx-opus", "cx-gpt"} {
		if got, err := passthroughClamped([]string{"dispatch", n, "t"}, n, proj, root); err == nil {
			t.Errorf("%s: a codex agent under a ceiling must be refused, got %v", n, got)
		}
	}
	// The same refusal from the project file and from the environment.
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("per-domain:\n  dev: codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := passthroughClamped([]string{"dispatch", "plain", "t"}, "plain", proj, root); err == nil {
		t.Errorf(".yakos.yml per-domain codex must be refused: %v", got)
	}
	if err := os.Remove(filepath.Join(proj, ".yakos.yml")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YAKOS_RUNTIME", "codex")
	if got, err := passthroughClamped([]string{"dispatch", "plain", "t"}, "plain", proj, root); err == nil {
		t.Errorf("YAKOS_RUNTIME=codex must be refused: %v", got)
	}
	t.Setenv("YAKOS_RUNTIME", "")
	if _, err := passthroughClamped([]string{"dispatch", "plain", "t"}, "plain", proj, root); err != nil {
		t.Errorf("a claude agent must pass: %v", err)
	}
}

// modelOf is the --model the bash dispatch's parser would read from argv.
func modelOf(argv []string) string { v, _ := argvFlag(argv, "--model"); return v }

// sec-364b N2/N3/N4: the argv is read the way bash reads it, and a ceilinged agent
// that resolves to a non-claude runtime is refused whatever the registry ranks.
func TestPassthroughClampBashParseAndNonClaude(t *testing.T) {
	state := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", state)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_RUNTIME", "")
	root, proj := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	writeAgentFile(t, dir, "cx.md", "id: cx\nruntime: codex\nmodel: gpt-5.4-mini\n")
	writeAgentFile(t, dir, "op.md", "id: op\nmodel: opus\n")
	for _, n := range []string{"cx", "op"} {
		if err := budget.SetMaxModel(state, n, "sonnet"); err != nil {
			t.Fatal(err)
		}
	}
	// N3: a flag-looking value of --eval-run-id is not a flag, so the codex pin stands.
	if got, err := passthroughClamped([]string{"dispatch", "cx", "t", "--eval-run-id", "--runtime=claude"}, "cx", proj, root); err == nil {
		t.Errorf("--eval-run-id value read as --runtime: %v", got)
	}
	// ...and `--eval-run-id --model` must not count as a model flag: the pin is still added.
	got, err := passthroughClamped([]string{"dispatch", "op", "t", "--eval-run-id", "--model"}, "op", proj, root)
	if err != nil || modelOf(got) != "sonnet" {
		t.Errorf("decoy --model hid the pin: %v %v", got, err)
	}
	// N4: an explicit codex runtime is refused even if an overlay could rank the model.
	for _, a := range [][]string{
		{"dispatch", "op", "t", "--runtime", "codex"},
		{"dispatch", "op", "t", "--runtime=agy"},
	} {
		if got, err := passthroughClamped(a, "op", proj, root); err == nil || !strings.Contains(err.Error(), "resolves to runtime") {
			t.Errorf("%v must be refused as a non-claude runtime under a ceiling: %v %v", a, got, err)
		}
	}
	// The gate switches the runtime fallbacks off for bash under a ceiling only.
	t.Setenv(noFallbackEnv, "")
	_ = os.Unsetenv(noFallbackEnv)
	budgetGateBeforePassthrough([]string{"dispatch", "op", "t", "--project", proj}, root)
	if os.Getenv(noFallbackEnv) != "1" {
		t.Errorf("%s not set for a ceilinged agent", noFallbackEnv)
	}
	_ = os.Unsetenv(noFallbackEnv)
	budgetGateBeforePassthrough([]string{"dispatch", "free", "t", "--project", proj}, root)
	if os.Getenv(noFallbackEnv) != "" {
		t.Errorf("%s set for an agent with no ceiling", noFallbackEnv)
	}
	_ = os.Unsetenv(noFallbackEnv)
}
