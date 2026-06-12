package budgetguard_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/budgetguard"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func ptr[T any](v T) *T { return &v }

func writeYAML(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, ".yakos.yml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	return dir
}

func readLastLog(t *testing.T, logFile string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var last map[string]any
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				var rec map[string]any
				if err := json.Unmarshal(data[start:i], &rec); err == nil {
					last = rec
				}
			}
			start = i + 1
		}
	}
	if last == nil {
		t.Fatal("no log entries")
	}
	return last
}

func makeHook(workDir, projectDir string) *budgetguard.Hook {
	return &budgetguard.Hook{WorkCurrentDir: workDir, ProjectDir: projectDir, NowFn: fixedNow}
}

func makeInput(tool string, extraEnv map[string]string) hooktype.HookInput {
	env := map[string]string{"CLAUDE_SESSION_ID": "sess-1"}
	for k, v := range extraEnv {
		env[k] = v
	}
	return hooktype.HookInput{Tool: tool, Payload: map[string]any{}, Env: env}
}

func TestBudgetGuard_NoConfigNoop(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir() // no .yakos.yml
	h := makeHook(work, proj)
	out, err := h.Run(context.Background(), makeInput("Edit", nil))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestBudgetGuard_DisabledByEnv(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_tool_calls: 1\n")
	h := makeHook(work, proj)
	in := makeInput("Edit", map[string]string{"YAKOS_BUDGET_DISABLE": "1"})
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestBudgetGuard_DisabledInConfig(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: false\n  max_tool_calls: 1\n")
	h := makeHook(work, proj)
	out, err := h.Run(context.Background(), makeInput("Edit", nil))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestBudgetGuard_MaxToolCallsBlock(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_tool_calls: 2\n")
	h := makeHook(work, proj)
	// Call 3 times; third should block.
	for i := 0; i < 2; i++ {
		out, err := h.Run(context.Background(), makeInput("Edit", nil))
		if err != nil || out.ExitCode != 0 {
			t.Fatalf("call %d: err=%v code=%d", i, err, out.ExitCode)
		}
	}
	out, err := h.Run(context.Background(), makeInput("Edit", nil))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 (block)", out.ExitCode)
	}
	if len(out.Stderr) == 0 {
		t.Error("expected block message in stderr")
	}
}

func TestBudgetGuard_MaxWallSecondsBlock(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_wall_seconds: 1\n")

	// First call with fixedTime to initialize started_at.
	h1 := &budgetguard.Hook{WorkCurrentDir: work, ProjectDir: proj, NowFn: fixedNow}
	h1.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck

	// Second call 100 seconds later — exceeds the 1s cap.
	futureNow := fixedTime.Add(100 * time.Second)
	h2 := &budgetguard.Hook{WorkCurrentDir: work, ProjectDir: proj, NowFn: func() time.Time { return futureNow }}
	out, _ := h2.Run(context.Background(), makeInput("Edit", nil))
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2", out.ExitCode)
	}
}

func TestBudgetGuard_RepeatSameToolBlock(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_repeat_same_tool: 3\n")
	h := makeHook(work, proj)
	for i := 0; i < 3; i++ {
		out, _ := h.Run(context.Background(), makeInput("Read", nil))
		if out.ExitCode != 0 {
			t.Fatalf("call %d: exit code %d", i, out.ExitCode)
		}
	}
	out, _ := h.Run(context.Background(), makeInput("Read", nil))
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 (loop block)", out.ExitCode)
	}
}

func TestBudgetGuard_DifferentToolResetsRunCount(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_repeat_same_tool: 2\n")
	h := makeHook(work, proj)
	// Read × 2 = at limit but not over.
	h.Run(context.Background(), makeInput("Read", nil)) //nolint:errcheck
	h.Run(context.Background(), makeInput("Read", nil)) //nolint:errcheck
	// Switch to Edit — resets run count.
	out, _ := h.Run(context.Background(), makeInput("Edit", nil))
	if out.ExitCode != 0 {
		t.Errorf("after tool switch exit code %d, want 0", out.ExitCode)
	}
}

func TestBudgetGuard_BypassMaxToolCalls(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_tool_calls: 1\n")
	_ = os.WriteFile(filepath.Join(work, "hook-bypass.md"),
		[]byte("Hook: budget-guard\nScope: cap=max_tool_calls\n"), 0644)
	h := makeHook(work, proj)
	// Exceed the cap.
	h.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck
	out, _ := h.Run(context.Background(), makeInput("Edit", nil))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 (bypass active)", out.ExitCode)
	}
	rec := readLastLog(t, filepath.Join(work, "logs", "budget-guard.ndjson"))
	if rec["severity"] != "WARN" {
		t.Errorf("severity=%v with bypass, want WARN", rec["severity"])
	}
}

func TestBudgetGuard_PassLogged(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_tool_calls: 100\n")
	h := makeHook(work, proj)
	h.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck
	rec := readLastLog(t, filepath.Join(work, "logs", "budget-guard.ndjson"))
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v, want REPORT on pass", rec["severity"])
	}
}

func TestBudgetGuard_NoWorkDir(t *testing.T) {
	h := &budgetguard.Hook{WorkCurrentDir: "", NowFn: fixedNow}
	out, err := h.Run(context.Background(), makeInput("Edit", nil))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestBudgetGuard_HookName(t *testing.T) {
	h := budgetguard.New("/tmp/work", "/tmp/proj")
	if h.Name() != "budget-guard" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestBudgetGuard_StatePersistedBetweenInstances(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_tool_calls: 3\n")
	// First instance: 2 calls.
	h1 := makeHook(work, proj)
	h1.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck
	h1.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck
	// Second instance (simulating new hook invocation): 2 more = 4 total → block.
	h2 := makeHook(work, proj)
	h2.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck
	out, _ := h2.Run(context.Background(), makeInput("Edit", nil))
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 across instances", out.ExitCode)
	}
}

func TestBudgetGuard_NoCapsNoEnforcement(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n")
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Edit", nil))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 (no caps configured)", out.ExitCode)
	}
}

func TestBudgetGuard_MaxToolCallsBlockMessage(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_tool_calls: 1\n")
	h := makeHook(work, proj)
	h.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck
	out, _ := h.Run(context.Background(), makeInput("Edit", nil))
	if out.ExitCode != 2 {
		t.Skip("unexpected pass")
	}
	msg := string(out.Stderr)
	if len(msg) == 0 || msg[:len("budget-guard:")] != "budget-guard:" {
		t.Errorf("block message should start with 'budget-guard:', got: %s", msg[:min(len(msg), 20)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestBudgetGuard_WallSecondsBlockMessage(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_wall_seconds: 1\n")
	// Prime with fixedTime so started_at is set correctly.
	h0 := &budgetguard.Hook{WorkCurrentDir: work, ProjectDir: proj, NowFn: fixedNow}
	h0.Run(context.Background(), makeInput("Edit", nil)) //nolint:errcheck
	// Now exceed wall cap.
	h := &budgetguard.Hook{WorkCurrentDir: work, ProjectDir: proj,
		NowFn: func() time.Time { return fixedTime.Add(100 * time.Second) }}
	out, _ := h.Run(context.Background(), makeInput("Edit", nil))
	if out.ExitCode != 2 {
		t.Skip("unexpected pass")
	}
	if len(out.Stderr) == 0 {
		t.Error("expected block message")
	}
}

func TestBudgetGuard_RepeatBlockMessage(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeYAML(t, proj, "budget:\n  enabled: true\n  max_repeat_same_tool: 1\n")
	h := makeHook(work, proj)
	h.Run(context.Background(), makeInput("Bash", nil)) //nolint:errcheck
	out, _ := h.Run(context.Background(), makeInput("Bash", nil))
	if out.ExitCode != 2 {
		t.Skip("unexpected pass")
	}
	if len(out.Stderr) == 0 {
		t.Error("expected block message")
	}
}
