// Package teamlifecycle is the Go-native Tier-0 port of
// lib/hooks/team-lifecycle.sh.
//
// PreToolUse hook on TeamCreate, Agent, and TeamDelete. TELEMETRY HOOK — never
// blocks, always exits 0.
//
// TeamCreate:
//   - Appends "team_created" event to work/current/logs/team-lifecycle.ndjson
//   - Overwrites work/current/.session-started with current ISO ts
//   - Appends a "team_created" NDJSON line to
//     work/current/.session-started-history.ndjson
//   - Moves the first TODO task to IN PROGRESS in work/current/kanban.md
//
// Agent (subagent spawn):
//   - Appends "agent_spawned" event. Does NOT touch session-started files.
//
// TeamDelete:
//   - Appends "team_deleted" event.
//   - Moves the first IN PROGRESS task to DONE in work/current/kanban.md.
//   - Appends a session summary to sessions.ndjson (idempotent check on
//     session_id + exit_kind).
//   - Creates .session-summarized marker for session-end-check.
//
// Reuses internal/kanban for the kanban mutation (Move).
package teamlifecycle

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
	"github.com/bakw00ds/yakos/internal/kanban"
)

const hookName = "team-lifecycle"

// Hook implements runner.Hook for team lifecycle events.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/.
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

// Run executes the team lifecycle logic. Always returns ExitCode 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	switch in.Tool {
	case "TeamCreate", "Agent", "TeamDelete":
	default:
		return out, nil
	}

	if h.WorkCurrentDir == "" {
		return out, nil
	}
	if err := os.MkdirAll(h.WorkCurrentDir, 0755); err != nil { //nolint:gosec
		// Best-effort; telemetry hook.
		return out, nil
	}

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	ts := now.UTC().Format(time.RFC3339)
	// Field derivation matches bash exactly: session_id/agent come from the
	// stdin payload (hi_session_id/hi_sender_role), never env vars; tool_input
	// is the NESTED .tool_input object (jq -c '.tool_input // {}'), not the
	// whole payload — the previous appendLifecycleEvent logged the entire
	// Payload (including session_id/tool_name/etc.) under the "tool_input"
	// key, and stringField(in.Payload, "name") always returned "" since
	// "name" only ever exists under .tool_input, never top-level.
	sessionID := hookio.PayloadString(in, "session_id")
	callerRole := senderRole(in)
	toolInput := hookio.ToolInput(in)
	if toolInput == nil {
		toolInput = map[string]any{}
	}

	switch in.Tool {
	case "TeamCreate":
		teamName := stringField(toolInput, "name")

		h.appendLifecycleEvent(&out, now, "team_created", callerRole, in.Tool, in.Event, sessionID, toolInput)

		// .session-started — overwrite with current ts.
		startedFile := filepath.Join(h.WorkCurrentDir, ".session-started")
		_ = atomicWrite(startedFile, []byte(ts+"\n"))

		// .session-started-history.ndjson — append.
		histFile := filepath.Join(h.WorkCurrentDir, ".session-started-history.ndjson")
		histEntry := map[string]any{
			"ts": ts, "event": "team_created",
			"team_name": teamName, "session_id": sessionID,
		}
		_ = appendNDJSON(histFile, histEntry)

		// Kanban: first TODO → IN PROGRESS.
		h.kanbanMoveFirst(&out, kanban.ColTODO, kanban.ColInProgress)

	case "Agent":
		h.appendLifecycleEvent(&out, now, "agent_spawned", callerRole, in.Tool, in.Event, sessionID, toolInput)
		// Intentionally does NOT touch .session-started or history.

	case "TeamDelete":
		teamName := stringField(toolInput, "name")

		h.appendLifecycleEvent(&out, now, "team_deleted", callerRole, in.Tool, in.Event, sessionID, toolInput)

		// Kanban: first IN PROGRESS → DONE.
		h.kanbanMoveFirst(&out, kanban.ColInProgress, kanban.ColDone)

		// Write session summary (idempotent).
		sessionsLog := filepath.Join(filepath.Dir(h.WorkCurrentDir), "sessions.ndjson")
		exitKind := "team_deleted"

		if !h.sessionAlreadyLogged(sessionsLog, sessionID, exitKind) {
			tsStart := h.lookupTeamCreatedTs(sessionID)
			var durationSec *int64
			if tsStart != "" {
				startT, err1 := time.Parse(time.RFC3339, tsStart)
				endT, err2 := time.Parse(time.RFC3339, ts)
				if err1 == nil && err2 == nil {
					d := int64(endT.Sub(startT).Seconds())
					durationSec = &d
				}
			}

			summaryEntry := map[string]any{
				"ts_start":              tsStart,
				"ts_end":                ts,
				"duration_seconds":      maybeInt64(durationSec),
				"team_name":             teamName,
				"session_id":            sessionID,
				"scratchpad_size_bytes": 0,
				"exit_kind":             exitKind,
			}
			_ = appendNDJSON(sessionsLog, summaryEntry)
		} else {
			h.appendLifecycleEvent(&out, now, "duplicate_summary_suppressed", callerRole, in.Tool, in.Event, sessionID, toolInput)
		}

		// Marker for session-end-check.
		markerFile := filepath.Join(h.WorkCurrentDir, ".session-summarized")
		_ = atomicTouch(markerFile)
	}

	return out, nil
}

// kanbanMoveFirst moves the first task in srcCol to dstCol in kanban.md.
// No-op if kanban.md doesn't exist. Never panics — telemetry hook.
func (h *Hook) kanbanMoveFirst(out *hooktype.HookOutput, srcCol, dstCol string) {
	kanbanPath := filepath.Join(h.WorkCurrentDir, "kanban.md")
	f, err := os.Open(kanbanPath) //nolint:gosec
	if err != nil {
		return // file absent is not an error for a telemetry hook
	}
	b, err := kanban.Parse(f)
	f.Close() //nolint:errcheck
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: kanban parse: %v\n", hookName, err)
		return
	}

	// Find first task ID in srcCol.
	var srcItems []string
	switch srcCol {
	case kanban.ColTODO:
		srcItems = b.TODOItems
	case kanban.ColInProgress:
		srcItems = b.InProgressItems
	}
	if len(srcItems) == 0 {
		return // nothing to move
	}

	// Extract the task ID from the first item (format: "K-N — title").
	taskID := extractTaskID(srcItems[0])
	if taskID == "" {
		return
	}

	if err := b.Move(taskID, dstCol); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: kanban move %s → %s: %v\n", hookName, taskID, dstCol, err)
		return
	}
	if err := b.Save(kanbanPath); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: kanban save: %v\n", hookName, err)
	}
}

// appendLifecycleEvent writes one NDJSON line to the lifecycle log via the
// shared hooklog writer, matching bash's emit_event exactly:
//
//	extra="$(jq -nc --arg event "$event" --arg caller "$caller" --arg tool "$tool" --argjson input "$tool_input" \
//	    '{event: $event, caller: $caller, tool: $tool, tool_input: $input}')"
//	ho_log "team-lifecycle" "REPORT" "pass" "team-lifecycle: $event" "$extra"
//
// Note the extra object's own "event" key (the lifecycle sub-event, e.g.
// "team_created") deliberately overrides ho_log's base "event" field (the
// Claude Code hook_event_name, e.g. "PreToolUse") per jq's `{...} + $extra`
// merge — hooklog.Append's Extra map has the identical override semantics.
func (h *Hook) appendLifecycleEvent(out *hooktype.HookOutput, now time.Time, event, caller, tool, hookEvent, sessionID string, toolInput map[string]any) {
	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  "REPORT",
		Decision:  "pass",
		Reason:    "team-lifecycle: " + event,
		Agent:     caller,
		SessionID: sessionID,
		Event:     hookEvent,
		Extra: map[string]any{
			"event":      event,
			"caller":     caller,
			"tool":       tool,
			"tool_input": toolInput,
		},
	}, now)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, err)
	}
}

// sessionAlreadyLogged checks if (session_id, exit_kind) already appears in
// sessions.ndjson.
func (h *Hook) sessionAlreadyLogged(sessionsLog, sessionID, exitKind string) bool {
	data, err := os.ReadFile(sessionsLog) //nolint:gosec
	if err != nil {
		return false
	}
	// Scan NDJSON lines.
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["session_id"] == sessionID && rec["exit_kind"] == exitKind {
			return true
		}
	}
	return false
}

// lookupTeamCreatedTs finds the most recent "team_created" ts for sessionID
// from .session-started-history.ndjson. Falls back to .session-started.
func (h *Hook) lookupTeamCreatedTs(sessionID string) string {
	histFile := filepath.Join(h.WorkCurrentDir, ".session-started-history.ndjson")
	if data, err := os.ReadFile(histFile); err == nil { //nolint:gosec
		var last string
		for _, line := range splitLines(string(data)) {
			if line == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec["event"] == "team_created" && rec["session_id"] == sessionID {
				if ts, ok := rec["ts"].(string); ok {
					last = ts
				}
			}
		}
		if last != "" {
			return last
		}
	}
	// Fallback: .session-started.
	startedFile := filepath.Join(h.WorkCurrentDir, ".session-started")
	if data, err := os.ReadFile(startedFile); err == nil { //nolint:gosec
		line := strings.TrimSpace(string(data))
		if line != "" {
			return line
		}
	}
	return ""
}

// ---- helpers -----------------------------------------------------------------

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

func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func appendNDJSON(path string, entry map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	_, err = f.Write(data)
	return err
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func atomicTouch(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".yakos-tl-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// extractTaskID extracts the K-N id from a task item string.
//
// Board.TODOItems/InProgressItems strip the leading "- " bullet but preserve
// the checkbox: "[ ] K-3 — title" or "[-] K-3 — title".
// This function scans all space-separated tokens for the first "K-<digits>" token.
func extractTaskID(item string) string {
	// Scan tokens separated by spaces.
	start := 0
	for i := 0; i <= len(item); i++ {
		if i == len(item) || item[i] == ' ' || item[i] == '\t' {
			token := item[start:i]
			if len(token) > 2 && token[:2] == "K-" {
				valid := true
				for _, c := range token[2:] {
					if c < '0' || c > '9' {
						valid = false
						break
					}
				}
				if valid && len(token) > 2 {
					return token
				}
			}
			start = i + 1
		}
	}
	return ""
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func maybeInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
