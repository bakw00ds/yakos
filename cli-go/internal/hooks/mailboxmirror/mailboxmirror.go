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
	"time"

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
	to := stringField(in.Payload, "to")
	summary := stringField(in.Payload, "summary")
	body := stringField(in.Payload, "message")
	sessionID := in.Env["CLAUDE_SESSION_ID"]
	transcriptPath := in.Env["CLAUDE_TRANSCRIPT_PATH"]

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

	logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
	h.appendLog(&out, logFile, "REPORT", "pass", "logged peer message",
		map[string]any{"from": sender, "to": to, "summary": summary})

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

func senderRole(in hooktype.HookInput) string {
	if r := in.Env["YAKOS_AGENT_ROLE"]; r != "" {
		return r
	}
	if r := stringField(in.Payload, "agent_type"); r != "" {
		return r
	}
	return "lead"
}

func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (h *Hook) appendLog(out *hooktype.HookOutput, logFile, severity, action, message string, extra map[string]any) {
	ts := h.NowFn().UTC().Format(time.RFC3339)
	entry := map[string]any{
		"ts":       ts,
		"hook":     hookName,
		"severity": severity,
		"action":   action,
		"message":  message,
	}
	for k, v := range extra {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil { //nolint:gosec
		return
	}
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: open log: %v\n", hookName, err)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(data)
}
