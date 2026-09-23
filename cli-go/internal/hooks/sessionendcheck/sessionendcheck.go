// Package sessionendcheck is the Go-native Tier-0 port of
// lib/hooks/session-end-check.sh.
//
// SessionEnd hook — TELEMETRY HOOK, never blocks. Runs audit checks and
// writes a session-end summary if team-lifecycle.sh (or its Go port) hasn't
// already done so.
//
//  1. Audit: decisions.md staleness (>2h mtime → WARN).
//  2. Audit: expired bypass entries in hook-bypass.md.
//  3. Audit: per-hook BLOCK/WARN/REPORT counts from logs/*.ndjson.
//  4. Idempotent session summary: if .session-summarized marker exists,
//     remove it and skip (team_deleted already wrote the summary). Otherwise
//     append a session-end record with exit_kind="session_end_without_team_delete".
package sessionendcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/dirsize"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const (
	hookName            = "session-end-check"
	decisionsStaleLimit = 2 * time.Hour
)

// Hook implements runner.Hook for session end audit.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/.
	WorkCurrentDir string

	// NowFn is injected for tests.
	NowFn func() time.Time

	// TeamsDir overrides $HOME/.claude/teams for the peer-inbox-snapshot
	// feature. Empty (the production default) resolves $HOME itself —
	// see teamsDir(). Tests must always set this to a t.TempDir()-rooted
	// path, never touch the operator's real $HOME/.claude/teams.
	TeamsDir string
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

// Run executes the session-end audit. Always returns ExitCode 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	if h.WorkCurrentDir == "" {
		return out, nil
	}
	if err := os.MkdirAll(h.WorkCurrentDir, 0755); err != nil { //nolint:gosec
		return out, nil
	}

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	ts := now.UTC().Format(time.RFC3339)
	// session_id comes from the stdin payload (hi_session_id), not an env
	// var — session-end-check.sh never reads CLAUDE_SESSION_ID.
	sessionID := hookio.PayloadString(in, "session_id")
	callerRole := senderRole(in)
	logsDir := filepath.Join(h.WorkCurrentDir, "logs")

	_ = os.MkdirAll(logsDir, 0755) //nolint:gosec

	// ---- audit: decisions.md staleness ----
	decisionsStale := false
	var decisionsAgeS int64
	decisionsFile := filepath.Join(h.WorkCurrentDir, "decisions.md")
	if info, err := os.Stat(decisionsFile); err == nil {
		decisionsAgeS = int64(now.Sub(info.ModTime()).Seconds())
		if decisionsAgeS > int64(decisionsStaleLimit.Seconds()) {
			decisionsStale = true
		}
	}

	// ---- audit: expired bypass entries ----
	expiredCount, expiredIDs := h.auditBypassExpiry(now)

	// ---- audit: per-hook outcome counts ----
	blockCount, warnCount, reportCount, hookSummary := h.auditHookLogs(logsDir)

	severity := "REPORT"
	if decisionsStale || expiredCount > 0 {
		severity = "WARN"
	}

	logErr := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  severity,
		Decision:  "pass",
		Reason:    "session terminal state recorded",
		Agent:     callerRole,
		SessionID: sessionID,
		Event:     in.Event,
		Extra: map[string]any{
			// bash builds decisions_stale with --arg (always a JSON
			// STRING "true"/"false"), not --argjson — matching exactly.
			"decisions_stale":      strconv.FormatBool(decisionsStale),
			"decisions_age_s":      decisionsAgeS,
			"expired_bypass_count": expiredCount,
			"expired_bypass_ids":   strings.Join(expiredIDs, " "),
			"hook_blocks":          blockCount,
			"hook_warns":           warnCount,
			"hook_reports":         reportCount,
			"hooks":                hookSummary,
		},
	}, now)
	if logErr != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, logErr)
	}

	// ---- session summary (idempotent) ----
	markerFile := filepath.Join(h.WorkCurrentDir, ".session-summarized")
	if _, err := os.Stat(markerFile); err == nil {
		// team-lifecycle already wrote the summary.
		_ = os.Remove(markerFile)
		return out, nil
	}

	sessionsLog := filepath.Join(filepath.Dir(h.WorkCurrentDir), "sessions.ndjson")
	_ = os.MkdirAll(filepath.Dir(sessionsLog), 0755) //nolint:gosec

	exitKind := "session_end_without_team_delete"
	if h.sessionAlreadyLogged(sessionsLog, sessionID, exitKind) {
		return out, nil
	}

	tsStart := h.lookupStartTs(sessionID)
	var durationSec any = nil
	if tsStart != "" {
		startT, err1 := time.Parse(time.RFC3339, tsStart)
		endT, err2 := time.Parse(time.RFC3339, ts)
		if err1 == nil && err2 == nil {
			durationSec = int64(endT.Sub(startT).Seconds())
		}
	}

	teamName := h.lookupTeamName(sessionID)
	scratchpadBytes := dirsize.Bytes(h.WorkCurrentDir)

	// Snapshot team mailbox inbox files into the session audit log. Per
	// Phase 0.5 (2026-04-29), ~/.claude/teams/<team>/inboxes/<recipient>.json
	// is the durable on-disk record of every peer DM — including
	// peer-to-peer DMs that never transit the lead's hook context. The
	// PreToolUse mailbox-mirror hook captures live SendMessage calls, but
	// it can miss messages routed teammate-to-teammate. Snapshotting at
	// session end ensures the full peer-DM history lands in the audit
	// trail. Per the no-block telemetry policy, every step is guarded.
	h.snapshotTeamInboxes(&out, now, teamName, callerRole, sessionID, in.Event)

	summaryEntry := map[string]any{
		"ts_start":              tsStart,
		"ts_end":                ts,
		"duration_seconds":      durationSec,
		"team_name":             teamName,
		"session_id":            sessionID,
		"scratchpad_size_bytes": scratchpadBytes,
		"exit_kind":             exitKind,
	}
	if err := appendNDJSON(sessionsLog, summaryEntry); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: sessions log: %v\n", hookName, err)
	}

	return out, nil
}

// teamsDir resolves $HOME/.claude/teams, matching bash's
// teams_dir="$HOME/.claude/teams" (an OS env var read, not a stdin
// payload field — same convention every other HOME-reading Go hook in
// this package uses). h.TeamsDir overrides it for tests, which must
// never touch the operator's real $HOME/.claude/teams.
func (h *Hook) teamsDir() string {
	if h.TeamsDir != "" {
		return h.TeamsDir
	}
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "teams")
}

// snapshotTeamInboxes copies every *.json file from
// <teamsDir>/<teamName>/inboxes/ into <WorkCurrentDir>/team-inboxes/, and
// — matching bash exactly — emits a SECOND REPORT-severity hooklog entry
// (in addition to the audit entry Run already wrote) when at least one
// file was copied. Never blocks; every step is best-effort.
func (h *Hook) snapshotTeamInboxes(out *hooktype.HookOutput, now time.Time, teamName, callerRole, sessionID, event string) {
	if teamName == "" {
		return
	}
	teamsDir := h.teamsDir()
	if teamsDir == "" {
		return
	}
	if info, err := os.Stat(teamsDir); err != nil || !info.IsDir() {
		return
	}

	inboxSrc := filepath.Join(teamsDir, teamName, "inboxes")
	info, err := os.Stat(inboxSrc)
	if err != nil || !info.IsDir() {
		return
	}

	entries, err := os.ReadDir(inboxSrc)
	if err != nil {
		return
	}

	snapshotDst := filepath.Join(h.WorkCurrentDir, "team-inboxes")
	if err := os.MkdirAll(snapshotDst, 0755); err != nil { //nolint:gosec
		return
	}

	snapCount := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		src := filepath.Join(inboxSrc, e.Name())
		data, readErr := os.ReadFile(src) //nolint:gosec
		if readErr != nil {
			continue
		}
		if writeErr := os.WriteFile(filepath.Join(snapshotDst, e.Name()), data, 0644); writeErr != nil { //nolint:gosec
			continue
		}
		snapCount++
	}

	if snapCount == 0 {
		return
	}

	logErr := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  "REPORT",
		Decision:  "pass",
		Reason:    fmt.Sprintf("snapshotted %d inbox file(s) from team %s", snapCount, teamName),
		Agent:     callerRole,
		SessionID: sessionID,
		Event:     event,
		Extra: map[string]any{
			"team":        teamName,
			"inbox_files": snapCount,
		},
	}, now)
	if logErr != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: inbox snapshot log: %v\n", hookName, logErr)
	}
}

// ---- audit helpers -----------------------------------------------------------

// auditBypassExpiry scans hook-bypass.md for expired entries.
// Very simplified: looks for "Expires:" lines and compares to now.
func (h *Hook) auditBypassExpiry(now time.Time) (int, []string) {
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return 0, nil
	}
	var expiredIDs []string
	var currentID string
	for _, line := range splitLines(string(data)) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## bypass:") {
			currentID = strings.TrimSpace(strings.TrimPrefix(trimmed, "## bypass:"))
		} else if strings.HasPrefix(trimmed, "**Expires:**") {
			expStr := strings.TrimSpace(strings.TrimPrefix(trimmed, "**Expires:**"))
			// Strip any trailing content after the first token (e.g. extra annotations).
			fields := strings.Fields(expStr)
			if len(fields) == 0 {
				continue
			}
			expStr = fields[0]
			expT, err := time.Parse(time.RFC3339, expStr)
			if err == nil && expT.Before(now) && currentID != "" {
				expiredIDs = append(expiredIDs, currentID)
			}
		}
	}
	return len(expiredIDs), expiredIDs
}

// hookOutcomeSummary tracks per-hook outcome counts.
type hookOutcomeSummary struct {
	Block  int `json:"block"`
	Warn   int `json:"warn"`
	Report int `json:"report"`
}

// auditHookLogs scans logs/*.ndjson and tallies BLOCK/WARN/REPORT counts.
func (h *Hook) auditHookLogs(logsDir string) (blockTotal, warnTotal, reportTotal int, summary map[string]hookOutcomeSummary) {
	summary = make(map[string]hookOutcomeSummary)
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".ndjson") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".ndjson")
		if name == hookName {
			continue // skip self
		}
		logPath := filepath.Join(logsDir, e.Name())
		data, err := os.ReadFile(logPath) //nolint:gosec
		if err != nil {
			continue
		}
		var hs hookOutcomeSummary
		for _, line := range splitLines(string(data)) {
			if line == "" {
				continue
			}
			var rec map[string]any
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			sev, _ := rec["severity"].(string)
			switch strings.ToUpper(sev) {
			case "BLOCK":
				hs.Block++
				blockTotal++
			case "WARN":
				hs.Warn++
				warnTotal++
			case "REPORT", "PASS":
				hs.Report++
				reportTotal++
			}
		}
		summary[name] = hs
	}
	return
}

// sessionAlreadyLogged checks if (session_id, exit_kind) is in sessions.ndjson.
func (h *Hook) sessionAlreadyLogged(sessionsLog, sessionID, exitKind string) bool {
	data, err := os.ReadFile(sessionsLog) //nolint:gosec
	if err != nil {
		return false
	}
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

// lookupStartTs finds the most recent "team_created" ts for sessionID.
func (h *Hook) lookupStartTs(sessionID string) string {
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
	// Fallback.
	startedFile := filepath.Join(h.WorkCurrentDir, ".session-started")
	if data, err := os.ReadFile(startedFile); err == nil { //nolint:gosec
		line := strings.TrimSpace(string(data))
		if line != "" {
			return line
		}
	}
	return ""
}

// lookupTeamName finds the most recent "team_created" .team_name for
// sessionID from .session-started-history.ndjson, matching bash exactly:
//
//	team_name="$(jq -rc --arg sid "$session_id" \
//	    'select(.session_id == $sid and .event == "team_created") | .team_name' \
//	    "$(yakos_session_history_file)" | tail -n 1)"
//
// Unlike lookupStartTs, bash has NO fallback to .session-started here
// (that file only ever holds a timestamp, never a team name) — a session
// with no matching history record resolves to "".
func (h *Hook) lookupTeamName(sessionID string) string {
	histFile := filepath.Join(h.WorkCurrentDir, ".session-started-history.ndjson")
	data, err := os.ReadFile(histFile) //nolint:gosec
	if err != nil {
		return ""
	}
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
			if name, ok := rec["team_name"].(string); ok {
				last = name
			}
		}
	}
	return last
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

// ---- generic helpers ---------------------------------------------------------

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
