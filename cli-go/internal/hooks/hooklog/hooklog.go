// Package hooklog is the single, shared NDJSON log writer for Go-native
// (Tier-0) hooks. It reproduces the field set, field order, and file
// location of bash's ho_log helper (lib/hooks/lib/hook-output.sh) so a Go
// hook's log record is structurally identical to its bash counterpart's:
//
//	{ts, hook, severity, decision, reason, agent, session_id, event} + extra
//
// written to <work/current>/logs/<hook>.ndjson.
//
// Before this package existed, every Go hook rolled its own appendNDJSON
// helper with its own field names (commonly "action"/"message" instead of
// "decision"/"reason", and missing "agent"/"session_id"/"event" entirely —
// see the S-6 structural plan §1.2 cycle-counter/secret-scan spot checks).
// Converting a hook to hooklog.Append is the fix for that class of
// divergence; internal/hooks/pathlog is the first hook converted, as the
// reference pattern for the rest (S-6 A-2).
package hooklog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// baseFieldOrder is ho_log's fixed field order, reproduced exactly:
//
//	{ts: ..., hook: ..., severity: ..., decision: ..., reason: ...,
//	 agent: ..., session_id: ..., event: ...} + $extra
var baseFieldOrder = []string{"ts", "hook", "severity", "decision", "reason", "agent", "session_id", "event"}

// Entry is one hooklog record.
type Entry struct {
	// Hook is the canonical hook name (e.g. "path-log"). Also selects the
	// log file: <work/current>/logs/<Hook>.ndjson.
	Hook string

	// Severity is one of BLOCK / WARN / PASS / REPORT (see hook-output.sh's
	// severity tiers). Not validated here — hooks own their own severity
	// vocabulary, this package only writes what it's given.
	Severity string

	// Decision is bash ho_log's "decision" field (e.g. "pass", "block").
	Decision string

	// Reason is bash ho_log's "reason" field — a short human-readable
	// explanation.
	Reason string

	// Agent is the sender role (hi_sender_role() on the bash side):
	// "lead" when the invocation carries no agent_type, else the
	// agent_type with any "yakos:" namespace prefix stripped.
	Agent string

	// SessionID is the Claude Code session_id, or "" when unknown.
	SessionID string

	// Event is the Claude hook event name (hook_event_name), or "" when
	// unknown.
	Event string

	// Extra holds hook-specific fields merged into the record, exactly as
	// bash's `{...} + $extra` jq merge: a key in Extra overrides the base
	// field of the same name.
	Extra map[string]any
}

// Append writes one NDJSON record to <workDir>/logs/<hook>.ndjson. It is a
// no-op (returns nil) when workDir is empty, matching every Go hook's
// existing "no active session" behavior.
func Append(workDir string, e Entry, now time.Time) error {
	if workDir == "" {
		return nil
	}
	if e.Hook == "" {
		return fmt.Errorf("hooklog: Entry.Hook is required")
	}

	logDir := filepath.Join(workDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil { //nolint:gosec
		return fmt.Errorf("hooklog: mkdir %s: %w", logDir, err)
	}
	logFile := filepath.Join(logDir, e.Hook+".ndjson")

	line, err := marshalEntry(e, now)
	if err != nil {
		return fmt.Errorf("hooklog: marshal: %w", err)
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("hooklog: open %s: %w", logFile, err)
	}
	defer f.Close() //nolint:errcheck

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("hooklog: write %s: %w", logFile, err)
	}
	return nil
}

// marshalEntry renders e as one NDJSON line (including the trailing
// newline), with the base fields first in ho_log's fixed order, followed
// by any Extra keys not already a base field name, sorted for determinism
// (rule:cache-stability — no map-iteration-order output).
func marshalEntry(e Entry, now time.Time) ([]byte, error) {
	fields := map[string]any{
		"ts":         now.UTC().Format(time.RFC3339),
		"hook":       e.Hook,
		"severity":   e.Severity,
		"decision":   e.Decision,
		"reason":     e.Reason,
		"agent":      e.Agent,
		"session_id": e.SessionID,
		"event":      e.Event,
	}

	isBase := make(map[string]bool, len(baseFieldOrder))
	for _, k := range baseFieldOrder {
		isBase[k] = true
	}

	var extraKeys []string
	for k, v := range e.Extra {
		fields[k] = v
		if !isBase[k] {
			extraKeys = append(extraKeys, k)
		}
	}
	sort.Strings(extraKeys)

	order := make([]string, 0, len(baseFieldOrder)+len(extraKeys))
	order = append(order, baseFieldOrder...)
	order = append(order, extraKeys...)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range order {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(fields[k])
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
