package main

// hooks_impl_e2e_test.go — end-to-end proof that the hook commands written by
// `yakos refresh --hooks-impl go|hybrid` actually enforce when Claude Code
// runs them (K-87 A-3b).
//
// The original A-3 tests asserted only the SHAPE of the generated command.
// The shape was wrong: the command relied on YAKOS_HOOKS, whose default is
// bash mode, so every Go gate was a silent no-op (exit 0). These tests take
// the literal command string out of the refreshed settings.json, run it
// through `sh -c` with a blocking payload on stdin and NO YAKOS_HOOKS in the
// environment, and assert the gate blocks.
//
// Requires the make-build binary (bin/yakos or $YAKOS_GO_BINARY); skips when
// it is absent, like the other binary-driven tests in this package.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hooksImplEnv returns a hermetic environment: no YAKOS_ROOT/LIB/HOOKS, an
// isolated HOME and work dir.
func hooksImplEnv(home, workDir, proj string, extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		switch k {
		case "YAKOS_ROOT", "YAKOS_LIB", "YAKOS_HOOKS", "YAKOS_IMPL", "HOME",
			"YAKOS_WORK_DIR", "CLAUDE_PROJECT_DIR", "YAKOS_INPLACE_WORK",
			"YAKOS_HOOKS_FAIL_OPEN", "YAKOS_PLAN_QUALITY_DISABLE":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home, "YAKOS_WORK_DIR="+workDir, "CLAUDE_PROJECT_DIR="+proj)
	return append(env, extra...)
}

func hooksImplBinary(t *testing.T) string {
	t.Helper()
	bin := resolveGoBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("Go yakos binary not found at %q (run make build): %v", bin, err)
	}
	return bin
}

// hooksImplRefresh refreshes proj with --hooks-impl impl using the built
// binary and returns the settings.json bytes.
func hooksImplRefresh(t *testing.T, bin, home, proj, impl string) []byte {
	t.Helper()
	cmd := exec.Command(bin, "refresh", "--project", proj, "--hooks-impl", impl) //nolint:gosec
	cmd.Env = hooksImplEnv(home, filepath.Join(home, "work"), proj, "YAKOS_IMPL=go")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refresh --hooks-impl %s: %v\n%s", impl, err, out)
	}
	data, err := os.ReadFile(filepath.Join(proj, ".claude", "settings.json")) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// settingsCommand returns the literal registered command whose last word is
// name and which routes through `hook run` (Go form).
func settingsGoCommand(t *testing.T, settings []byte, name string) string {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(settings, &doc); err != nil {
		t.Fatal(err)
	}
	for _, entries := range doc.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				if strings.Contains(h.Command, " hook run ") && strings.HasSuffix(h.Command, " "+name) {
					return h.Command
				}
			}
		}
	}
	t.Fatalf("no Go-form command for %s in settings.json:\n%s", name, settings)
	return ""
}

func shRun(t *testing.T, command, stdin string, env []string) (int, string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", command) //nolint:gosec
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("sh -c %q: %v", command, err)
		}
		code = ee.ExitCode()
	}
	return code, stderr.String()
}

func hooksImplProject(t *testing.T) (home, proj string) {
	t.Helper()
	home = t.TempDir()
	proj = filepath.Join(home, "project")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	copyDirRecursive(t, filepath.Join(refreshFixtureDir(t), "proj-missing-settings"), proj)
	return home, proj
}

const (
	pqgPayload = `{"hook_event_name":"PreToolUse","tool_name":"Agent","tool_input":{}}`
)

func secretPayload() string {
	key := "AKIA" + "ABCDEFGHIJKLMNOP" // split so this file never trips secret-scan
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": "x.txt", "content": "aws_access_key_id = " + key},
	})
	return string(b)
}

func TestHooksImplE2E_GoModeCommandsBlock(t *testing.T) {
	bin := hooksImplBinary(t)
	home, proj := hooksImplProject(t)
	work := filepath.Join(home, "work")
	settings := hooksImplRefresh(t, bin, home, proj, "go")
	env := hooksImplEnv(home, work, proj) // deliberately no YAKOS_HOOKS

	if err := os.MkdirAll(filepath.Join(work, "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "current", ".plan-blocked"), []byte(`{"plan_id":"p1","reason":"low score"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, payload := range map[string]string{
		"plan-quality-gate": pqgPayload,
		"secret-scan":       secretPayload(),
	} {
		cmd := settingsGoCommand(t, settings, name)
		if !strings.Contains(cmd, "--impl go") {
			t.Errorf("%s: command does not pin the tier: %s", name, cmd)
		}
		code, stderr := shRun(t, cmd, payload, env)
		if code != 2 || strings.TrimSpace(stderr) == "" {
			t.Errorf("%s: want exit 2 + stderr from `sh -c %s`, got exit %d stderr %q", name, cmd, code, stderr)
		}
	}
}

func TestHooksImplE2E_HybridGoReadyHookRunsGoTier(t *testing.T) {
	bin := hooksImplBinary(t)
	home, proj := hooksImplProject(t)
	work := filepath.Join(home, "work")
	settings := hooksImplRefresh(t, bin, home, proj, "hybrid")
	env := hooksImplEnv(home, work, proj)

	// Non-GoReady gates stay bash under hybrid.
	if strings.Contains(string(settings), " hook run --impl go secret-scan") {
		t.Fatal("hybrid moved non-GoReady secret-scan to Go")
	}

	// cycle-counter is GoReady; its Go tier writes work/current/.cycle-count.
	// In bash mode with no user hook it would be a silent no-op.
	cmd := settingsGoCommand(t, settings, "cycle-counter")
	if !strings.Contains(cmd, "--impl go") {
		t.Fatalf("hybrid GoReady command does not pin the Go tier: %s", cmd)
	}
	if err := os.MkdirAll(filepath.Join(work, "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, stderr := shRun(t, cmd, `{"hook_event_name":"UserPromptSubmit"}`, env)
	if code != 0 {
		t.Fatalf("cycle-counter exit %d: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(work, "current", ".cycle-count")); err != nil {
		t.Fatalf("Go tier did not run for the hybrid command (no .cycle-count): %v", err)
	}
}

// The legacy flag-less command is what A-3 (#288) generated. With YAKOS_HOOKS
// unset it is a silent no-op — this documents the bug — and the next refresh
// must migrate it in place, then stay byte-stable.
func TestHooksImplE2E_LegacyCommandIsNoOpAndMigrates(t *testing.T) {
	bin := hooksImplBinary(t)
	home, proj := hooksImplProject(t)
	work := filepath.Join(home, "work")
	fixed := hooksImplRefresh(t, bin, home, proj, "go")
	env := hooksImplEnv(home, work, proj)

	legacy := strings.ReplaceAll(string(fixed), " hook run --impl go ", " hook run ")
	if legacy == string(fixed) {
		t.Fatal("test setup: no --impl go to strip")
	}
	settingsPath := filepath.Join(proj, ".claude", "settings.json")
	if err := os.WriteFile(settingsPath, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(work, "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "current", ".plan-blocked"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := settingsGoCommand(t, []byte(legacy), "plan-quality-gate")
	if code, _ := shRun(t, old, pqgPayload, env); code != 0 {
		t.Errorf("legacy flag-less command: documented behavior is exit 0 (fail-open), got %d", code)
	}

	migrated := hooksImplRefresh(t, bin, home, proj, "go")
	if string(migrated) != string(fixed) {
		t.Errorf("refresh did not migrate legacy commands to the pinned form")
	}
	if again := hooksImplRefresh(t, bin, home, proj, "go"); string(again) != string(migrated) {
		t.Errorf("refresh not byte-stable after migration")
	}
}

func TestHookRun_ImplFlagFailsClosedOnUnknownHook(t *testing.T) {
	bin := hooksImplBinary(t)
	home, proj := hooksImplProject(t)
	code, stderr := shRun(t, bin+" hook run --impl go no-such-hook", "{}", hooksImplEnv(home, filepath.Join(home, "work"), proj))
	if code != 2 || !strings.Contains(stderr, "no Go implementation") {
		t.Errorf("want exit 2 + reason, got %d %q", code, stderr)
	}
	// Flag beats env: YAKOS_HOOKS=bash must not defeat --impl go.
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(filepath.Join(work, "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "current", ".plan-blocked"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env := hooksImplEnv(home, work, proj, "YAKOS_HOOKS=bash")
	if c, _ := shRun(t, bin+" hook run --impl go plan-quality-gate", pqgPayload, env); c != 2 {
		t.Errorf("--impl go must beat YAKOS_HOOKS=bash, got exit %d", c)
	}
	if c, _ := shRun(t, bin+" hook run --impl bogus plan-quality-gate", pqgPayload, env); c != 2 {
		t.Errorf("invalid --impl must fail closed, got %d", c)
	}
}
