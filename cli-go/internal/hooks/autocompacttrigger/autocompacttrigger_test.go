package autocompacttrigger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/autocompacttrigger"
	"github.com/bakw00ds/yakos/internal/hooks/contextthreshold"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

// writeMarker creates the .compact-pending marker in workDir.
func writeMarker(t *testing.T, workDir string) {
	t.Helper()
	_ = os.MkdirAll(workDir, 0755)
	markerPath := filepath.Join(workDir, contextthreshold.CompactPendingMarker)
	_ = os.WriteFile(markerPath, []byte("2026-01-15T10:00:00Z\n"), 0644)
}

// captureHook runs the hook and captures stdout JSON via WriteStdout injection.
func captureHook(t *testing.T, h *autocompacttrigger.Hook, in hooktype.HookInput) (map[string]any, hooktype.HookOutput) {
	t.Helper()
	var buf bytes.Buffer
	h.WriteStdout = buf.Write
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if buf.Len() == 0 {
		return nil, out
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &m); err != nil {
		t.Fatalf("parse stdout JSON: %v (raw: %q)", err, buf.String())
	}
	return m, out
}

// ---- core behaviour ---------------------------------------------------------

func TestAutoCompact_NoMarker_NoOp(t *testing.T) {
	work := t.TempDir()
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	var buf bytes.Buffer
	h.WriteStdout = buf.Write
	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no stdout when marker absent; got %q", buf.String())
	}
}

func TestAutoCompact_WithMarker_EmitsBlockJSON(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow

	m, out := captureHook(t, h, hooktype.HookInput{Env: map[string]string{}})
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", out.ExitCode)
	}
	if m == nil {
		t.Fatal("expected JSON on stdout; got nothing")
	}
	if m["decision"] != "block" {
		t.Errorf("decision=%v, want 'block'", m["decision"])
	}
	if m["reason"] != "/compact" {
		t.Errorf("reason=%v, want '/compact'", m["reason"])
	}
}

func TestAutoCompact_WithMarker_SystemMessagePresent(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow

	m, _ := captureHook(t, h, hooktype.HookInput{Env: map[string]string{}})
	if m == nil {
		t.Fatal("expected JSON on stdout")
	}
	msg, _ := m["systemMessage"].(string)
	if !strings.Contains(msg, "yakos auto-compact") {
		t.Errorf("systemMessage missing 'yakos auto-compact'; got %q", msg)
	}
	if !strings.Contains(msg, "/compact") {
		t.Errorf("systemMessage missing '/compact'; got %q", msg)
	}
}

func TestAutoCompact_WithMarker_MarkerRemovedAfterTrigger(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	var buf bytes.Buffer
	h.WriteStdout = buf.Write

	_, _ = h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})

	markerPath := filepath.Join(work, contextthreshold.CompactPendingMarker)
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Error("expected marker to be removed after trigger")
	}
}

func TestAutoCompact_HookName(t *testing.T) {
	h := autocompacttrigger.New("/tmp/work")
	if h.Name() != "auto-compact-trigger" {
		t.Errorf("Name()=%q, want 'auto-compact-trigger'", h.Name())
	}
}

func TestAutoCompact_AlwaysExitZero(t *testing.T) {
	work := t.TempDir()
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	h.WriteStdout = (&bytes.Buffer{}).Write
	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", out.ExitCode)
	}
}

func TestAutoCompact_NoWorkDir_NoOp(t *testing.T) {
	h := autocompacttrigger.New("")
	h.NowFn = fixedNow
	var buf bytes.Buffer
	h.WriteStdout = buf.Write
	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output with empty WorkCurrentDir; got %q", buf.String())
	}
}

func TestAutoCompact_IdempotentAfterTrigger(t *testing.T) {
	// Second run with no marker must be a no-op.
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow

	var buf1, buf2 bytes.Buffer
	h.WriteStdout = buf1.Write
	_, _ = h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})

	h.WriteStdout = buf2.Write
	_, _ = h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})

	if buf1.Len() == 0 {
		t.Error("first run: expected JSON output")
	}
	if buf2.Len() != 0 {
		t.Errorf("second run: expected no output (marker already removed); got %q", buf2.String())
	}
}

func TestAutoCompact_WithMarker_LogWritten(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	h.WriteStdout = (&bytes.Buffer{}).Write

	in := hooktype.HookInput{
		Env: map[string]string{"CLAUDE_SESSION_ID": "sess-log-1"},
	}
	_, _ = h.Run(context.Background(), in)

	logPath := filepath.Join(work, "logs", "auto-compact-trigger.ndjson")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "compact_triggered") {
		t.Errorf("log missing 'compact_triggered'; got: %s", string(data))
	}
	if !strings.Contains(string(data), "sess-log-1") {
		t.Errorf("log missing session_id; got: %s", string(data))
	}
}

func TestAutoCompact_WithMarker_LogTimestamp(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	h.WriteStdout = (&bytes.Buffer{}).Write

	_, _ = h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})

	logPath := filepath.Join(work, "logs", "auto-compact-trigger.ndjson")
	data, _ := os.ReadFile(logPath)
	if !strings.Contains(string(data), "2026-01-15T10:00:00Z") {
		t.Errorf("log missing expected timestamp; got: %s", string(data))
	}
}

// ---- degradation / error paths ----------------------------------------------

func TestAutoCompact_StatError_DegradesSilently(t *testing.T) {
	work := t.TempDir()
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	// Inject a Stat that returns a non-NotExist error.
	h.Stat = func(name string) (os.FileInfo, error) {
		return nil, errors.New("permission denied")
	}
	var buf bytes.Buffer
	h.WriteStdout = buf.Write

	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d (should degrade silently)", err, out.ExitCode)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no JSON output on stat error; got %q", buf.String())
	}
}

func TestAutoCompact_RemoveError_ContinuesToEmitBlock(t *testing.T) {
	// If Remove fails, we still emit the block JSON (marker will be stale but
	// the operator is not silently dropped into infinite loop — next trigger
	// will try again).
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	h.Remove = func(name string) error {
		return errors.New("disk full")
	}
	var buf bytes.Buffer
	h.WriteStdout = buf.Write

	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	if buf.Len() == 0 {
		t.Error("expected JSON output even when marker removal fails")
	}
	// stderr should contain the remove error
	if !strings.Contains(string(out.Stderr), "remove marker") {
		t.Errorf("expected remove-marker error in stderr; got %q", string(out.Stderr))
	}
}

func TestAutoCompact_StdoutWriteError_DegradesSilently(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	h.WriteStdout = func([]byte) (int, error) {
		return 0, errors.New("broken pipe")
	}

	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d (should degrade silently)", err, out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "write stdout") {
		t.Errorf("expected write-stdout error in stderr; got %q", string(out.Stderr))
	}
}

// ---- JSON contract verification ---------------------------------------------

func TestAutoCompact_JSON_DecisionField(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	m, _ := captureHook(t, h, hooktype.HookInput{Env: map[string]string{}})
	if m == nil {
		t.Fatal("expected JSON")
	}
	if _, ok := m["decision"]; !ok {
		t.Error("JSON missing 'decision' field")
	}
}

func TestAutoCompact_JSON_ReasonIsCompactSlashCommand(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	m, _ := captureHook(t, h, hooktype.HookInput{Env: map[string]string{}})
	if m == nil {
		t.Fatal("expected JSON")
	}
	if m["reason"] != "/compact" {
		t.Errorf("reason=%q; want '/compact'", m["reason"])
	}
}

func TestAutoCompact_JSON_SystemMessageField(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	m, _ := captureHook(t, h, hooktype.HookInput{Env: map[string]string{}})
	if m == nil {
		t.Fatal("expected JSON")
	}
	if _, ok := m["systemMessage"]; !ok {
		t.Error("JSON missing 'systemMessage' field")
	}
}

func TestAutoCompact_JSON_ValidJSON(t *testing.T) {
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow

	var buf bytes.Buffer
	h.WriteStdout = buf.Write
	_, _ = h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})

	if !json.Valid(bytes.TrimSpace(buf.Bytes())) {
		t.Errorf("output is not valid JSON: %q", buf.String())
	}
}

func TestAutoCompact_JSON_NoExtraTopLevelKeys(t *testing.T) {
	// The Stop hook contract only needs decision, reason, systemMessage.
	// Guard against accidentally emitting extra keys that might confuse older
	// Claude Code versions.
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow
	m, _ := captureHook(t, h, hooktype.HookInput{Env: map[string]string{}})
	if m == nil {
		t.Fatal("expected JSON")
	}
	allowed := map[string]bool{"decision": true, "reason": true, "systemMessage": true}
	for k := range m {
		if !allowed[k] {
			t.Errorf("unexpected key %q in Stop hook JSON output", k)
		}
	}
}

// ---- marker / threshold boundary tests -------------------------------------

func TestAutoCompact_MarkerFileName(t *testing.T) {
	// Verify the marker file name matches what context-threshold writes.
	if contextthreshold.CompactPendingMarker != ".compact-pending" {
		t.Errorf("CompactPendingMarker=%q; expected '.compact-pending'", contextthreshold.CompactPendingMarker)
	}
}

func TestAutoCompact_MultipleMarkersDoNotDouble(t *testing.T) {
	// Even if the marker is written multiple times (e.g. across multiple
	// threshold crossings in one turn), the trigger fires exactly once per
	// Stop event because the marker is removed before the block is emitted.
	work := t.TempDir()
	writeMarker(t, work)
	h := autocompacttrigger.New(work)
	h.NowFn = fixedNow

	callCount := 0
	h.WriteStdout = func(b []byte) (int, error) {
		callCount++
		return len(b), nil
	}

	_, _ = h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if callCount != 1 {
		t.Errorf("WriteStdout called %d times; want 1", callCount)
	}
}
