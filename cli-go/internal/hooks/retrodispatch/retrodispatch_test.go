package retrodispatch_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/retrodispatch"
)

// ---- helpers ----------------------------------------------------------------

func buildHook(t *testing.T) (*retrodispatch.Hook, string, string) {
	t.Helper()
	tmp := t.TempDir()
	workDir := filepath.Join(tmp, "work", "current")
	stateDir := filepath.Join(tmp, "state")
	for _, d := range []string{workDir, stateDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	h := retrodispatch.New(workDir, stateDir)
	h.State.NowFn = func() time.Time { return time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) }
	return h, workDir, stateDir
}

func touchMarker(t *testing.T, workDir string) {
	t.Helper()
	p := filepath.Join(workDir, ".retro-due")
	if err := os.WriteFile(p, []byte{}, 0644); err != nil {
		t.Fatalf("touch marker: %v", err)
	}
}

func markerExists(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, ".retro-due"))
	return err == nil
}

func run(t *testing.T, h *retrodispatch.Hook) hooktype.HookOutput {
	t.Helper()
	out, err := h.Run(context.Background(), hooktype.HookInput{Event: "UserPromptSubmit"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out
}

func readLog(t *testing.T, workDir string) []map[string]any {
	t.Helper()
	logFile := filepath.Join(workDir, "logs", "retro-dispatch.ndjson")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}
	var entries []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("invalid NDJSON: %v (line: %s)", err, line)
		}
		entries = append(entries, obj)
	}
	return entries
}

// ---- tests ------------------------------------------------------------------

func TestRetroDispatch_Name(t *testing.T) {
	h, _, _ := buildHook(t)
	if h.Name() != "retro-dispatch" {
		t.Errorf("Name() = %q; want 'retro-dispatch'", h.Name())
	}
}

func TestRetroDispatch_ExitCode0_Always(t *testing.T) {
	h, _, _ := buildHook(t)
	out := run(t, h)
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
}

func TestRetroDispatch_NoMarker_NoOp(t *testing.T) {
	h, workDir, _ := buildHook(t)
	// No .retro-due file.
	out := run(t, h)
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0; got %d", out.ExitCode)
	}
	// No log entry written.
	logFile := filepath.Join(workDir, "logs", "retro-dispatch.ndjson")
	if _, err := os.Stat(logFile); err == nil {
		t.Error("expected no log entry when no marker")
	}
}

func TestRetroDispatch_MarkerPresent_RemovesMarker(t *testing.T) {
	h, workDir, _ := buildHook(t)
	touchMarker(t, workDir)
	run(t, h)
	if markerExists(workDir) {
		t.Error("expected .retro-due to be removed after dispatch-ready")
	}
}

func TestRetroDispatch_MarkerPresent_WritesLog(t *testing.T) {
	h, workDir, _ := buildHook(t)
	touchMarker(t, workDir)
	run(t, h)
	entries := readLog(t, workDir)
	if len(entries) == 0 {
		t.Fatal("expected log entry after marker consumed")
	}
}

func TestRetroDispatch_MarkerPresent_LogAction_DispatchReady(t *testing.T) {
	h, workDir, _ := buildHook(t)
	touchMarker(t, workDir)
	run(t, h)
	entries := readLog(t, workDir)
	if len(entries) == 0 {
		t.Fatal("no log entries")
	}
	if entries[0]["action"] != "dispatch-ready" {
		t.Errorf("expected action='dispatch-ready'; got %v", entries[0]["action"])
	}
}

func TestRetroDispatch_AutoDispatchDisabled_SkipsDispatch(t *testing.T) {
	h, workDir, _ := buildHook(t)
	h.State.AutoDispatch = false
	touchMarker(t, workDir)
	run(t, h)
	// Marker should remain (not consumed when disabled).
	if !markerExists(workDir) {
		t.Error("marker should not be removed when AutoDispatch=false")
	}
}

func TestRetroDispatch_AutoDispatchDisabled_LogsSkip(t *testing.T) {
	h, workDir, _ := buildHook(t)
	h.State.AutoDispatch = false
	touchMarker(t, workDir)
	run(t, h)
	entries := readLog(t, workDir)
	if len(entries) == 0 {
		t.Fatal("expected log entry for skip")
	}
	if entries[0]["action"] != "skipped" {
		t.Errorf("expected action='skipped'; got %v", entries[0]["action"])
	}
}

func TestRetroDispatch_EmptyWorkDir_NoOp(t *testing.T) {
	h := retrodispatch.New("", "")
	out, err := h.Run(context.Background(), hooktype.HookInput{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0 for empty workdir; got %d", out.ExitCode)
	}
}

func TestRetroDispatch_NonexistentWorkDir_NoOp(t *testing.T) {
	h := retrodispatch.New("/nonexistent/99999", "")
	out, err := h.Run(context.Background(), hooktype.HookInput{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected ExitCode=0 for missing workdir; got %d", out.ExitCode)
	}
}

func TestRetroDispatch_LogEntry_ValidJSON(t *testing.T) {
	h, workDir, _ := buildHook(t)
	touchMarker(t, workDir)
	run(t, h)
	entries := readLog(t, workDir)
	if len(entries) == 0 {
		t.Fatal("no log entries")
	}
	// Verify all required fields.
	entry := entries[0]
	for _, field := range []string{"ts", "hook", "severity", "action", "message"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("log entry missing field %q; got %v", field, entry)
		}
	}
}

func TestRetroDispatch_LogEntry_ContainsDispatchID(t *testing.T) {
	h, workDir, _ := buildHook(t)
	touchMarker(t, workDir)
	run(t, h)
	entries := readLog(t, workDir)
	if len(entries) == 0 {
		t.Fatal("no log entries")
	}
	if _, ok := entries[0]["dispatch_id"]; !ok {
		t.Error("log entry missing 'dispatch_id'")
	}
}

func TestRetroDispatch_MultipleRuns_OnlyOneDispatch(t *testing.T) {
	h, workDir, _ := buildHook(t)
	touchMarker(t, workDir)
	// First run: consumes marker.
	run(t, h)
	// Second run: no marker, no-op.
	run(t, h)
	entries := readLog(t, workDir)
	// Only one dispatch-ready entry.
	dispatchReadyCount := 0
	for _, e := range entries {
		if e["action"] == "dispatch-ready" {
			dispatchReadyCount++
		}
	}
	if dispatchReadyCount != 1 {
		t.Errorf("expected 1 dispatch-ready; got %d", dispatchReadyCount)
	}
}

func TestRetroDispatch_StalePIDFile_TreatedAsNotInFlight(t *testing.T) {
	h, workDir, stateDir := buildHook(t)
	touchMarker(t, workDir)
	// Write a PID that is almost certainly not running (PID 99999999).
	pidFile := filepath.Join(stateDir, "retro-dispatch.pid")
	if err := os.WriteFile(pidFile, []byte("99999999\n"), 0644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	run(t, h)
	// Marker should be consumed (stale PID treated as not in-flight).
	if markerExists(workDir) {
		t.Error("marker should be consumed after stale PID")
	}
}

func TestRetroDispatch_MalformedPIDFile_TreatedAsNotInFlight(t *testing.T) {
	h, workDir, stateDir := buildHook(t)
	touchMarker(t, workDir)
	pidFile := filepath.Join(stateDir, "retro-dispatch.pid")
	if err := os.WriteFile(pidFile, []byte("not-a-pid\n"), 0644); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	run(t, h)
	if markerExists(workDir) {
		t.Error("marker should be consumed after malformed PID")
	}
}

func TestRetroDispatch_New_HasAutoDispatchTrue(t *testing.T) {
	h := retrodispatch.New("/tmp", "/tmp/state")
	if !h.State.AutoDispatch {
		t.Error("expected AutoDispatch=true by default")
	}
}

func TestRetroDispatch_LogDir_CreatedIfMissing(t *testing.T) {
	h, workDir, _ := buildHook(t)
	_ = os.RemoveAll(filepath.Join(workDir, "logs"))
	touchMarker(t, workDir)
	run(t, h)
	logFile := filepath.Join(workDir, "logs", "retro-dispatch.ndjson")
	if _, err := os.Stat(logFile); err != nil {
		t.Errorf("log dir/file not created: %v", err)
	}
}

func TestRetroDispatch_ReturnsNilError(t *testing.T) {
	h, _, _ := buildHook(t)
	_, err := h.Run(context.Background(), hooktype.HookInput{})
	if err != nil {
		t.Errorf("expected nil error; got %v", err)
	}
}

func TestRetroDispatch_LogSeverity_Report(t *testing.T) {
	h, workDir, _ := buildHook(t)
	touchMarker(t, workDir)
	run(t, h)
	entries := readLog(t, workDir)
	if len(entries) == 0 {
		t.Fatal("no log entries")
	}
	if entries[0]["severity"] != "REPORT" {
		t.Errorf("expected severity='REPORT'; got %v", entries[0]["severity"])
	}
}
