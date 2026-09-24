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

// makeSendMessageInput builds a HookInput matching bash's actual SendMessage
// shape: to/summary/message live under .tool_input (hi_msg_to/hi_msg_summary/
// hi_msg_body), session_id/transcript_path/agent_type are top-level Payload
// fields (hi_session_id/hi_transcript/hi_sender_role) — not env vars, which
// mailbox-mirror.sh never reads for any of these.
func makeSendMessageInput(to, summary, message string, extra map[string]any) hooktype.HookInput {
	payload := map[string]any{
		"tool_input": map[string]any{
			"to":      to,
			"summary": summary,
			"message": message,
		},
	}
	for k, v := range extra {
		payload[k] = v
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "SendMessage",
		Payload: payload,
		Env:     map[string]string{},
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
//
// S-6 A-2a: session_id/transcript_path now come from the stdin Payload
// (hi_session_id/hi_transcript), matching bash exactly — the hook never
// reads CLAUDE_SESSION_ID/CLAUDE_TRANSCRIPT_PATH from the environment.
func TestMessageLogged(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeSendMessageInput("researcher", "start on task 1", "begin investigating the hook pattern", map[string]any{
		"session_id":      "sess-abc",
		"transcript_path": "/tmp/tx.json",
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

// TestMessageBody_HTMLCharsNotEscaped confirms messages.ndjson — the
// durable peer-message audit trail — round-trips `<`, `>`, and `&` as
// literal bytes, matching bash's `jq -nc` (which never HTML-escapes).
// encoding/json.Marshal's default behavior DOES escape these to
// </>/&, which is semantically inert to a JSON-parsing
// reader but breaks raw grep/byte-diff tooling over the audit log (S-6
// A-2a round 2 review finding 5) — this asserts the RAW file bytes, not
// the round-tripped-through-json.Unmarshal value, since Unmarshal would
// silently undo the very escaping under test.
func TestMessageBody_HTMLCharsNotEscaped(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeSendMessageInput("researcher", "A & B <compare>", "see <b>bold</b> & \"quoted\"", nil)
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmp, "messages.ndjson"))
	if err != nil {
		t.Fatalf("read messages.ndjson: %v", err)
	}
	raw := string(data)
	for _, want := range []string{"A & B <compare>", "see <b>bold</b> & "} {
		if !strings.Contains(raw, want) {
			t.Errorf("expected raw messages.ndjson to contain literal %q, got: %s", want, raw)
		}
	}
	// Built from byte slices rather than string literals containing a
	// backslash-u escape, so this source file itself never carries a
	// literal "<"-shaped token for any tool (or this very test) to
	// trip over.
	for _, unwanted := range []string{
		string([]byte{'\\', 'u', '0', '0', '3', 'c'}), // <
		string([]byte{'\\', 'u', '0', '0', '3', 'e'}), // >
		string([]byte{'\\', 'u', '0', '0', '2', '6'}), // &
	} {
		if strings.Contains(raw, unwanted) {
			t.Errorf("messages.ndjson should not HTML-escape (%s found), got: %s", unwanted, raw)
		}
	}
}

// TestSenderFromPayload confirms the top-level .agent_type Payload field is
// used for the from field, matching hi_sender_role.
//
// S-6 A-2a: previously pinned YAKOS_AGENT_ROLE (an env var bash's
// mailbox-mirror.sh never reads — hi_sender_role reads .agent_type from
// stdin JSON only, per lib/hooks/lib/hook-input.sh) as the sender source;
// renamed and switched to the Payload field to match bash.
func TestSenderFromPayload(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeSendMessageInput("lead", "done", "task complete", map[string]any{
		"agent_type": "researcher",
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
	in := makeSendMessageInput("frontend", "contracts ready", "see api-contracts.md", nil)
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
		in := makeSendMessageInput("researcher", "msg", "body", nil)
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
	in := makeSendMessageInput("researcher", "hello", "world", nil)
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
	in := makeSendMessageInput("frontend", "contracts done", "private body not shared", nil)
	in.Env = map[string]string{
		"YAKOS_COORD_ENABLED": "1",
		"YAKOS_COORD_DIR":     coordDir,
	}
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
	in := makeSendMessageInput("lead", "done", "body", nil) // No YAKOS_COORD_ENABLED
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
	in := makeSendMessageInput("researcher", "hello", "body", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("hook must always exit 0, got %d", out.ExitCode)
	}
}

// TestTranscriptPathRecorded confirms transcript_path is saved.
//
// S-6 A-2a: transcript_path now comes from the stdin Payload
// (hi_transcript), matching bash; previously pinned the CLAUDE_TRANSCRIPT_PATH
// env var, which bash never reads.
func TestTranscriptPathRecorded(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp)
	in := makeSendMessageInput("researcher", "s", "m", map[string]any{
		"transcript_path": "/transcripts/tx-123.json",
	})
	if _, err := h.Run(context.Background(), in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	records := readMessages(t, tmp)
	if records[0]["transcript_path"] != "/transcripts/tx-123.json" {
		t.Fatalf("expected transcript_path, got %v", records[0]["transcript_path"])
	}
}
