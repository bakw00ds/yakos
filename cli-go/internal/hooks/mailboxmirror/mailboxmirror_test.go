package mailboxmirror_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/mailboxmirror"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(workDir string) *mailboxmirror.Hook {
	return &mailboxmirror.Hook{
		WorkCurrentDir: workDir,
		NowFn:          fixedNow,
	}
}

func makeInput(tool string, payload map[string]any, env map[string]string) hooktype.HookInput {
	if payload == nil {
		payload = map[string]any{}
	}
	if env == nil {
		env = map[string]string{}
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    tool,
		Payload: payload,
		Env:     env,
	}
}

func readMessages(t *testing.T, workDir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workDir, "messages.ndjson"))
	if err != nil {
		t.Fatalf("read messages: %v", err)
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal message: %v", err)
		}
		records = append(records, rec)
	}
	return records
}

// TestOnlySendMessage confirms the hook no-ops for non-SendMessage tools.
func TestOnlySendMessage(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	for _, tool := range []string{"Edit", "Write", "Bash", "Read"} {
		in := makeInput(tool, nil, nil)
		out, err := h.Run(context.Background(), in)
		if err != nil {
			t.Fatalf("tool=%s: unexpected error: %v", tool, err)
		}
		if out.ExitCode != 0 {
			t.Fatalf("tool=%s: expected exit 0", tool)
		}
		// No messages.ndjson should exist.
		if _, statErr := os.Stat(filepath.Join(tmp, "messages.ndjson")); statErr == nil {
			t.Fatalf("tool=%s: messages.ndjson should not be created", tool)
		}
	}
}

// TestMessageLogged confirms a SendMessage call writes a record.
func TestMessageLogged(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeInput("SendMessage", map[string]any{
		"to":      "researcher",
		"summary": "start on task 1",
		"message": "begin investigating the hook pattern",
	}, map[string]string{
		"CLAUDE_SESSION_ID":      "sess-abc",
		"CLAUDE_TRANSCRIPT_PATH": "/tmp/tx.json",
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	records := readMessages(t, tmp)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec["to"] != "researcher" {
		t.Fatalf("expected to=researcher, got %v", rec["to"])
	}
	if rec["summary"] != "start on task 1" {
		t.Fatalf("expected summary, got %v", rec["summary"])
	}
	if rec["body"] != "begin investigating the hook pattern" {
		t.Fatalf("expected body, got %v", rec["body"])
	}
	if rec["session_id"] != "sess-abc" {
		t.Fatalf("expected session_id, got %v", rec["session_id"])
	}
	if rec["ts"] != "2026-01-15T10:00:00Z" {
		t.Fatalf("expected ts, got %v", rec["ts"])
	}
}

// TestSenderFromEnv confirms YAKOS_AGENT_ROLE is used for the from field.
func TestSenderFromEnv(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeInput("SendMessage", map[string]any{
		"to":      "lead",
		"summary": "done",
		"message": "task complete",
	}, map[string]string{
		"YAKOS_AGENT_ROLE": "researcher",
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	records := readMessages(t, tmp)
	if records[0]["from"] != "researcher" {
		t.Fatalf("expected from=researcher, got %v", records[0]["from"])
	}
}

// TestSenderDefaultsToLead confirms the from field defaults to "lead".
func TestSenderDefaultsToLead(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeInput("SendMessage", map[string]any{
		"to":      "frontend",
		"summary": "contracts ready",
		"message": "see api-contracts.md",
	}, nil)
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	records := readMessages(t, tmp)
	if records[0]["from"] != "lead" {
		t.Fatalf("expected from=lead, got %v", records[0]["from"])
	}
}

// TestMultipleMessagesAppended confirms subsequent calls append records.
func TestMultipleMessagesAppended(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	for i := 0; i < 3; i++ {
		in := makeInput("SendMessage", map[string]any{
			"to":      "researcher",
			"summary": "msg",
			"message": "body",
		}, nil)
		if _, err := h.Run(context.Background(), in); err != nil {
			t.Fatalf("msg %d: unexpected error: %v", i, err)
		}
	}
	records := readMessages(t, tmp)
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
}

// TestAuditLogWritten confirms the hook log entry is created.
func TestAuditLogWritten(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp)
	in := makeInput("SendMessage", map[string]any{
		"to":      "researcher",
		"summary": "hello",
		"message": "world",
	}, nil)
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	logFile := filepath.Join(tmp, "logs", "mailbox-mirror.ndjson")
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "mailbox-mirror") {
		t.Fatalf("expected hook name in log, got: %s", data)
	}
}

// TestCoordActivityEmitted confirms the coord activity log is written when enabled.
func TestCoordActivityEmitted(t *testing.T) {
	tmp := t.TempDir()
	coordDir := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coordDir, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp)
	in := makeInput("SendMessage", map[string]any{
		"to":      "frontend",
		"summary": "contracts done",
		"message": "private body not shared",
	}, map[string]string{
		"YAKOS_COORD_ENABLED": "1",
		"YAKOS_COORD_DIR":     coordDir,
	})
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	activityLog := filepath.Join(coordDir, "activity.ndjson")
	data, err := os.ReadFile(activityLog)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	if !strings.Contains(string(data), "send_message") {
		t.Fatalf("expected send_message in activity log, got: %s", data)
	}
	// Body must not appear in the coord log.
	if strings.Contains(string(data), "private body not shared") {
		t.Fatalf("body should not appear in coord activity log")
	}
}

// TestCoordSkippedWhenDisabled confirms no coord write when YAKOS_COORD_ENABLED is unset.
func TestCoordSkippedWhenDisabled(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeInput("SendMessage", map[string]any{
		"to":      "lead",
		"summary": "done",
		"message": "body",
	}, nil) // No YAKOS_COORD_ENABLED
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No coord dir should be created.
	if _, err := os.Stat(filepath.Join(tmp, "coord")); err == nil {
		t.Fatalf("coord dir should not exist when coord disabled")
	}
}

// TestAlwaysExitZero confirms the hook never blocks.
func TestAlwaysExitZero(t *testing.T) {
	tmp := t.TempDir()
	// Make messages log parent unwritable to simulate I/O error.
	unwritable := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(unwritable, 0444); err != nil {
		t.Skip("cannot set up unwritable dir on this platform")
	}
	h := &mailboxmirror.Hook{
		WorkCurrentDir: filepath.Join(unwritable, "work"),
		NowFn:          fixedNow,
	}
	in := makeInput("SendMessage", map[string]any{
		"to":      "researcher",
		"summary": "hello",
		"message": "body",
	}, nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("hook must always exit 0, got %d", out.ExitCode)
	}
}

// TestTranscriptPathRecorded confirms transcript_path is saved.
func TestTranscriptPathRecorded(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeInput("SendMessage", map[string]any{
		"to":      "researcher",
		"summary": "s",
		"message": "m",
	}, map[string]string{
		"CLAUDE_TRANSCRIPT_PATH": "/transcripts/tx-123.json",
	})
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	records := readMessages(t, tmp)
	if records[0]["transcript_path"] != "/transcripts/tx-123.json" {
		t.Fatalf("expected transcript_path, got %v", records[0]["transcript_path"])
	}
}
