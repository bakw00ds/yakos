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
	"strings"
	"time"

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

	now := h.NowFn()
	ts := now.UTC().Format(time.RFC3339)
	sessionID := in.Env["CLAUDE_SESSION_ID"]
	logsDir := filepath.Join(h.WorkCurrentDir, "logs")
	logFile := filepath.Join(logsDir, hookName+".ndjson")

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

	auditEntry := map[string]any{
		"ts":                   ts,
		"hook":                 hookName,
		"severity":             severity,
		"action":               "pass",
		"message":              "session terminal state recorded",
		"decisions_stale":      decisionsStale,
		"decisions_age_s":      decisionsAgeS,
		"expired_bypass_count": expiredCount,
		"expired_bypass_ids":   strings.Join(expiredIDs, " "),
		"hook_blocks":          blockCount,
		"hook_warns":           warnCount,
		"hook_reports":         reportCount,
		"hooks":                hookSummary,
	}
	_ = appendNDJSON(logFile, auditEntry)

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

	summaryEntry := map[string]any{
		"ts_start":              tsStart,
		"ts_end":                ts,
		"duration_seconds":      durationSec,
		"team_name":             "",
		"session_id":            sessionID,
		"scratchpad_size_bytes": 0,
		"exit_kind":             exitKind,
	}
	if err := appendNDJSON(sessionsLog, summaryEntry); err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: sessions log: %v\n", hookName, err)
	}

	return out, nil
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
