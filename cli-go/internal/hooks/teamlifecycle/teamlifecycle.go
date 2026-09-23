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
// The kanban mutation (kanbanMoveFirst) is a from-scratch, line-based port
// of bash's awk state machine rather than a caller of internal/kanban's
// ID-based Move API — see kanbanMoveFirst's doc comment for why.
package teamlifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/dirsize"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
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
		h.kanbanMoveFirst(&out, colTODO, colInProgress, "-")

	case "Agent":
		h.appendLifecycleEvent(&out, now, "agent_spawned", callerRole, in.Tool, in.Event, sessionID, toolInput)
		// Intentionally does NOT touch .session-started or history.

	case "TeamDelete":
		teamName := stringField(toolInput, "name")

		h.appendLifecycleEvent(&out, now, "team_deleted", callerRole, in.Tool, in.Event, sessionID, toolInput)

		// Kanban: first IN PROGRESS → DONE.
		h.kanbanMoveFirst(&out, colInProgress, colDone, "x")

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
				"scratchpad_size_bytes": dirsize.Bytes(h.WorkCurrentDir),
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

// Column header text, matching the "## <col>" headings kanban.md uses —
// kept as plain strings (not internal/kanban's Board/ID-based Move API)
// because kanbanMoveFirst is a from-scratch, line-based port of bash's
// kanban_move_first awk script below, not a caller of internal/kanban.
const (
	colTODO       = "TODO"
	colInProgress = "IN PROGRESS"
	colDone       = "DONE"
)

var (
	reBulletStart = regexp.MustCompile(`^- \[`)
	reContinued   = regexp.MustCompile(`^  `)
	reAnyHeader   = regexp.MustCompile(`^## `)
	reCheckbox    = regexp.MustCompile(`^- \[.\]`)
)

// srcHeaderRe / dstHeaderRe build the "^## <col>[[:space:]]*$" pattern
// bash's awk uses, anchored to one exact column name.
func colHeaderRe(col string) *regexp.Regexp {
	return regexp.MustCompile(`^## ` + regexp.QuoteMeta(col) + `[[:space:]]*$`)
}

// kanbanMoveFirst moves the first task bullet under "## srcCol" to "## dstCol"
// in kanban.md, replacing its checkbox character with cbox. No-op if
// kanban.md doesn't exist. Never panics — telemetry hook.
//
// This is a faithful, from-scratch port of bash's kanban_move_first
// (lib/hooks/team-lifecycle.sh), a pure line-based awk state machine that
// moves the FIRST bullet under the source section REGARDLESS of its text
// content — no task-ID format (e.g. "K-<digits>") is required or assumed.
// A prior Go port instead extracted a "K-<digits>" token and called
// internal/kanban's ID-based Move, which silently no-ops (no move, no
// warning) for any board that doesn't use that ID convention — S-6 A-2a
// round 2 review finding 2 reproduced this with a free-form task title
// and confirmed bash moves it while the ID-based Go port left the board
// untouched. rule:kanban-discipline documents "TeamCreate → most recent
// matching TODO task → IN PROGRESS" without mandating any ID format, so
// this port restores that behavior exactly rather than the narrower one.
func (h *Hook) kanbanMoveFirst(out *hooktype.HookOutput, srcCol, dstCol, cbox string) {
	kanbanPath := filepath.Join(h.WorkCurrentDir, "kanban.md")
	data, err := os.ReadFile(kanbanPath) //nolint:gosec
	if err != nil {
		return // file absent is not an error for a telemetry hook
	}

	lines := splitLines(string(data))
	// awk always treats a file with N newline-terminated records as N
	// lines and never emits a trailing blank record for the final "\n"
	// itself; splitLines (used elsewhere in this package) already drops
	// that trailing empty element the same way strings.Split would add
	// one, so no extra adjustment is needed here.

	srcHeaderRe := colHeaderRe(srcCol)
	dstHeaderRe := colHeaderRe(dstCol)

	var outLines []string
	state := "scan" // "scan" | "in_src" | "capturing_cont"
	var taskFirst string
	var taskRest []string
	moved := false
	movedEmitted := false

	for _, line := range lines {
		if srcHeaderRe.MatchString(line) {
			outLines = append(outLines, line)
			state = "in_src"
			continue
		}

		if state == "in_src" && !moved && reBulletStart.MatchString(line) {
			taskFirst = line
			state = "capturing_cont"
			continue
		}

		if state == "capturing_cont" && reContinued.MatchString(line) {
			taskRest = append(taskRest, line)
			continue
		}

		if state == "capturing_cont" {
			moved = true
			state = "in_src"
			// Fall through — this line still needs the checks below
			// (it may itself be an "## " header, or the dst header).
		}

		if reAnyHeader.MatchString(line) && state == "in_src" {
			state = "scan"
			// Fall through, same as the awk source.
		}

		if dstHeaderRe.MatchString(line) {
			outLines = append(outLines, line)
			if moved {
				outLines = append(outLines, reCheckbox.ReplaceAllString(taskFirst, "- ["+cbox+"]"))
				outLines = append(outLines, taskRest...)
				movedEmitted = true
			}
			continue
		}

		outLines = append(outLines, line)
	}

	// END block: captured but never re-emitted (src found, dst section
	// missing) — don't drop the task, emit a warning marker instead.
	if moved && !movedEmitted {
		outLines = append(outLines, "# WARN: yakos kanban auto-update found src but no dst section")
		outLines = append(outLines, taskFirst)
		outLines = append(outLines, taskRest...)
	}

	newContent := strings.Join(outLines, "\n")
	if len(outLines) > 0 {
		newContent += "\n"
	}
	if err := atomicWrite(kanbanPath, []byte(newContent)); err != nil {
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
