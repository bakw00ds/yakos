package contextinject_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/contextinject"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(workDir, projectDir string) *contextinject.Hook {
	return &contextinject.Hook{
		WorkCurrentDir: workDir,
		ProjectDir:     projectDir,
		NowFn:          fixedNow,
	}
}

func makeInput(event, tool string, env map[string]string) hooktype.HookInput {
	e := map[string]string{}
	for k, v := range env {
		e[k] = v
	}
	return hooktype.HookInput{
		Event:   event,
		Tool:    tool,
		Payload: map[string]any{},
		Env:     e,
	}
}

func writeYAML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
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

func TestDisabledByEnv(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", map[string]string{
		"YAKOS_CONTEXT_INJECT_DISABLE": "1",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	if len(out.Stderr) != 0 {
		t.Fatalf("expected no output when disabled")
	}
}

func TestNoYakosYML(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	if len(out.Stderr) != 0 {
		t.Fatalf("expected no output when yaml missing")
	}
}

func TestDisabledByYAML(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: false\n")
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Stderr) != 0 {
		t.Fatalf("expected no stderr when disabled in yaml")
	}
}

func TestDecisionsInjected(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")
	if err := os.MkdirAll(tmp, 0755); err != nil {
		t.Fatal(err)
	}
	decisions := "## Decision: use Go for hooks\nReason: performance\n"
	if err := os.WriteFile(filepath.Join(tmp, "decisions.md"), []byte(decisions), 0644); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out.Stderr), "decisions.md tail") {
		t.Fatalf("expected decisions.md in output, got: %s", out.Stderr)
	}
	if !strings.Contains(string(out.Stderr), "use Go for hooks") {
		t.Fatalf("expected decision content in output, got: %s", out.Stderr)
	}
}

func TestBudgetInjected(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\nbudget:\n  max_tool_calls: 100\n  max_wall_seconds: 3600\n")

	state := map[string]any{
		"started_at":      fixedTime.Unix() - 120, // 120s elapsed
		"tool_call_count": 42,
		"session_id":      "sess-1",
	}
	stateData, _ := json.Marshal(state)
	if err := os.WriteFile(filepath.Join(tmp, ".budget-state.json"), stateData, 0644); err != nil {
		t.Fatal(err)
	}

	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stderr := string(out.Stderr)
	if !strings.Contains(stderr, "Budget:") {
		t.Fatalf("expected Budget section in output, got: %s", stderr)
	}
	if !strings.Contains(stderr, "tool calls: 42 / 100") {
		t.Fatalf("expected tool call count in output, got: %s", stderr)
	}
	if !strings.Contains(stderr, "wall-clock: 120s / 3600s") {
		t.Fatalf("expected wall-clock in output, got: %s", stderr)
	}
}

func TestSupervisorCriticalInjected(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")

	finding := map[string]any{
		"ts":        "2026-01-15T09:00:00Z",
		"overall":   "CRITICAL",
		"rationale": "suspicious pattern detected",
	}
	data, _ := json.Marshal(finding)
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(tmp, "supervisor-findings.ndjson"), data, 0644); err != nil {
		t.Fatal(err)
	}

	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stderr := string(out.Stderr)
	if !strings.Contains(stderr, "CRITICAL") {
		t.Fatalf("expected CRITICAL in output, got: %s", stderr)
	}
	if !strings.Contains(stderr, "suspicious pattern detected") {
		t.Fatalf("expected rationale in output, got: %s", stderr)
	}
}

func TestNoCriticalFindingWhenOnlyWarn(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")

	finding := map[string]any{
		"ts":        "2026-01-15T09:00:00Z",
		"overall":   "WARN",
		"rationale": "minor warning",
	}
	data, _ := json.Marshal(finding)
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(tmp, "supervisor-findings.ndjson"), data, 0644); err != nil {
		t.Fatal(err)
	}

	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stderr := string(out.Stderr)
	if strings.Contains(stderr, "CRITICAL") {
		t.Fatalf("should not emit CRITICAL for WARN finding: %s", stderr)
	}
}

func TestAuditLogWritten(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	logFile := filepath.Join(tmp, "logs", "context-inject.ndjson")
	rec := readLastLog(t, logFile)
	if rec["hook"] != "context-inject" {
		t.Fatalf("expected hook=context-inject, got %v", rec["hook"])
	}
}

func TestSectionToggleDecisionsOff(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n  inject_decisions: false\n")
	decisions := "## Decision: use Go\n"
	if err := os.WriteFile(filepath.Join(tmp, "decisions.md"), []byte(decisions), 0644); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(out.Stderr), "decisions.md") {
		t.Fatalf("expected decisions suppressed when inject_decisions=false")
	}
}

func TestPeerInjectedWhenCoordEnabled(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")

	// Create a fake coord sessions dir with 3 session files.
	sessDir := filepath.Join(tmp, "coord", "sessions")
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a@host-1.ndjson", "b@host-2.ndjson", "c@host-3.ndjson"} {
		if err := os.WriteFile(filepath.Join(sessDir, name), []byte("{}\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", map[string]string{
		"YAKOS_COORD_ENABLED": "1",
		"YAKOS_COORD_DIR":     filepath.Join(tmp, "coord"),
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out.Stderr), "Multi-dev") {
		t.Fatalf("expected Multi-dev in output, got: %s", out.Stderr)
	}
}

func TestPeerSkippedWhenCoordDisabled(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil) // no YAKOS_COORD_ENABLED
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(out.Stderr), "Multi-dev") {
		t.Fatalf("should not emit Multi-dev when coord disabled")
	}
}

func TestNeverBlocks(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("context-inject must never block; got ExitCode=%d", out.ExitCode)
	}
}

func TestEmptyWorkCurrentDir(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")
	h := &contextinject.Hook{
		WorkCurrentDir: "", // empty
		ProjectDir:     tmp,
		NowFn:          fixedNow,
	}
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
}

func TestMissingDecisionsFile(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")
	// No decisions.md written.
	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(out.Stderr), "decisions.md") {
		t.Fatalf("should not reference decisions.md when file is absent")
	}
}

func TestMultipleCriticalFindings_LastWins(t *testing.T) {
	tmp := t.TempDir()
	writeYAML(t, tmp, "context_inject:\n  enabled: true\n")

	var sb strings.Builder
	for i, r := range []string{"first critical", "second critical"} {
		f := map[string]any{
			"ts":        "2026-01-15T0" + string(rune('8'+i)) + ":00:00Z",
			"overall":   "CRITICAL",
			"rationale": r,
		}
		data, _ := json.Marshal(f)
		sb.Write(data)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(tmp, "supervisor-findings.ndjson"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	h := newHook(tmp, tmp)
	in := makeInput("UserPromptSubmit", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out.Stderr), "second critical") {
		t.Fatalf("expected last CRITICAL finding in output, got: %s", out.Stderr)
	}
	if strings.Contains(string(out.Stderr), "first critical") {
		t.Fatalf("expected only last CRITICAL finding, but got first one too: %s", out.Stderr)
	}
}
