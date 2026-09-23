// Package mailboxmirror is the Go-native Tier-0 port of
// lib/hooks/mailbox-mirror.sh.
//
// PreToolUse hook on SendMessage. Mirrors every team-internal SendMessage
// call (peer DM, lead → teammate, teammate → lead) to
// work/current/messages.ndjson. Always exits 0; this hook is audit, never
// policy.
//
// When YAKOS_COORD_ENABLED is set, a summary event (without body) is also
// emitted to the coord activity log so multi-dev tooling can surface
// cross-session message activity.
package mailboxmirror

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/mailbox"
)

const hookName = "mailbox-mirror"

// Hook implements runner.Hook for mailbox mirroring.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the session.
	WorkCurrentDir string

	// NowFn is injected for tests.
	NowFn func() time.Time
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the mailbox-mirror logic. Always returns ExitCode 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Only act on SendMessage tool calls.
	if in.Tool != "SendMessage" {
		return out, nil
	}

	ts := mailbox.FormatTS(h.NowFn())
	sender := senderRole(in)
	// Field derivation mirrors hi_msg_to/hi_msg_summary/hi_msg_body exactly:
	// .tool_input.to / .tool_input.summary / .tool_input.message — NOT
	// top-level Payload fields (SendMessage's own arguments live under
	// tool_input like every other tool call).
	to := hookio.ToolInputString(in, "to")
	summary := hookio.ToolInputString(in, "summary")
	body := hookio.ToolInputString(in, "message")
	// session_id/transcript_path come from the stdin payload (hi_session_id /
	// hi_transcript), not env vars — bash never reads CLAUDE_SESSION_ID or
	// CLAUDE_TRANSCRIPT_PATH from the environment for these.
	sessionID := hookio.PayloadString(in, "session_id")
	transcriptPath := hookio.PayloadString(in, "transcript_path")

	// Resolve messages log path.
	messagesLog := h.resolveMessagesLog(in)
	if err := os.MkdirAll(filepath.Dir(messagesLog), 0755); err != nil { //nolint:gosec
		out.Stderr = fmt.Appendf(out.Stderr, "%s: mkdir messages log dir: %v\n", hookName, err)
		return out, nil
	}

	// Build message record.
	record := map[string]any{
		"ts":              ts,
		"from":            sender,
		"to":              to,
		"summary":         summary,
		"body":            body,
		"session_id":      sessionID,
		"transcript_path": transcriptPath,
	}
	line, err := json.Marshal(record)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: marshal record: %v\n", hookName, err)
		return out, nil
	}

	// Append to messages.ndjson using O_APPEND for atomic multi-process writes.
	if appendErr := mailbox.AppendLine(messagesLog, line); appendErr != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: append messages log: %v\n", hookName, appendErr)
	}

	// Coord activity mirror (summary only; body excluded for privacy).
	if in.Env["YAKOS_COORD_ENABLED"] == "1" {
		h.emitCoordActivity(in, ts, sender, to, summary)
	}

	now := h.NowFn()
	logErr := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  "REPORT",
		Decision:  "pass",
		Reason:    "logged peer message",
		Agent:     sender,
		SessionID: sessionID,
		Event:     in.Event,
		Extra: map[string]any{
			"from":    sender,
			"to":      to,
			"summary": summary,
		},
	}, now)
	if logErr != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, logErr)
	}

	return out, nil
}

// ---- coord mirror -----------------------------------------------------------

// emitCoordActivity appends a send_message event to the coord activity log.
// Body is excluded (peer DMs are private by default).
func (h *Hook) emitCoordActivity(in hooktype.HookInput, ts, sender, to, summary string) {
	activityLog := resolveCoordActivityLog(in)
	if activityLog == "" {
		return
	}
	event := mailbox.Event{
		Ts:   ts,
		Kind: "send_message",
		Actor: mailbox.Actor{
			User:      in.Env["USER"],
			Host:      in.Env["HOSTNAME"],
			Agent:     sender,
			SessionID: in.Env["CLAUDE_SESSION_ID"],
		},
	}
	detail := map[string]any{
		"from":    sender,
		"to":      to,
		"summary": summary,
	}
	detailBytes, err := json.Marshal(detail)
	if err == nil {
		event.Detail = detailBytes
	}
	// Best-effort; ignore error (coord is best-effort per design).
	_ = mailbox.AppendEvent(activityLog, event)
}

// resolveCoordActivityLog returns the coord activity.ndjson path or "".
func resolveCoordActivityLog(in hooktype.HookInput) string {
	if d := in.Env["YAKOS_COORD_DIR"]; d != "" {
		return filepath.Join(d, "activity.ndjson")
	}
	proj := in.Env["YAKOS_PROJECT_NAME"]
	if proj == "" {
		return ""
	}
	return filepath.Join("/var/lib/yakos", proj, "coord", "activity.ndjson")
}

// ---- helpers ----------------------------------------------------------------

func (h *Hook) resolveMessagesLog(in hooktype.HookInput) string {
	if h.WorkCurrentDir != "" {
		return filepath.Join(h.WorkCurrentDir, "messages.ndjson")
	}
	if d := in.Env["CLAUDE_PROJECT_DIR"]; d != "" {
		return filepath.Join(d, "work", "current", "messages.ndjson")
	}
	return filepath.Join(".", "work", "current", "messages.ndjson")
}

// senderRole extracts the agent/role, matching hi_sender_role exactly:
// hi_field_or '.agent_type' 'lead' (top-level, fallback "lead" when
// absent/empty), trimmed, then the "yakos:" namespace prefix stripped.
func senderRole(in hooktype.HookInput) string {
	raw := hookio.PayloadString(in, "agent_type")
	if raw == "" {
		raw = "lead"
	}
	raw = strings.TrimSpace(raw)
	return strings.TrimPrefix(raw, "yakos:")
}
