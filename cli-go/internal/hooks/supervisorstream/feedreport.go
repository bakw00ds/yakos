package supervisorstream

// feedreport.go is the entry the dispatch layer uses to feed the supervisor
// from a runtime's normalized event stream (K-146). It reuses this package's
// built-in risk patterns, trusted policy reader and pending/findings file
// conventions, so the detect-and-report feed and the PostToolUse hook agree.
//
// Nothing here echoes event content: a finding carries static labels only.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// feedFindingMaxLabels bounds the labels one finding records.
const feedFindingMaxLabels = 8

var feedNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// FeedFinding is one detect-and-report result over a normalized event.
type FeedFinding struct {
	Runtime  string   // runtime name, e.g. "codex"
	Kind     string   // "tool_result" or "text"
	Severity string   // "critical" or "warn"
	Labels   []string // static scanner labels, never event content
	Session  string   // console/session id, used only as a sanitized file-name key
}

// RiskLabel runs the supervisor pre-filter's built-in risk patterns over text
// (bounded to its head and tail like the hook does) and returns
// "risk-regex:<label>" for the first match, or "". It uses the defaults only: a
// project's extra patterns are not applied to model output.
func RiskLabel(text string) string {
	t := boundRisk(text)
	t = strings.ReplaceAll(strings.ReplaceAll(t, "\n", " "), "\\ ", "  ")
	for i, re := range defaultRiskPatterns {
		if re.MatchString(t) {
			return "risk-regex:" + defaultRiskLabels[i]
		}
	}
	return ""
}

// KillOnCritical reports the trusted user policy's kill_on_critical switch
// (default off). It is read through loadPolicy, the same trusted-file reader as
// the launch-gate limits; a project .yakos.yml can never set it.
func KillOnCritical() bool {
	pf := loadPolicy()
	return pf.KillOnCritical != nil && *pf.KillOnCritical
}

// ReportFeedFinding appends one finding to <workCurrent>/supervisor-findings.ndjson
// and one preview record to the session's pending file. The finding is written
// with overall WARN on purpose: the findings file also drives the PostToolUse
// CRITICAL block gate, and a hostile tool result must not be able to block the
// lead by planting a pattern. The finding is surfaced to the operator through
// recommended_action (yakos supervise pending) instead. workCurrent must be an
// existing directory; nothing is created. Errors are returned, never logged with
// a path.
func ReportFeedFinding(workCurrent string, f FeedFinding, now time.Time) error {
	if workCurrent == "" || !feedNameRE.MatchString(f.Runtime) || (f.Kind != "tool_result" && f.Kind != "text") {
		return os.ErrInvalid
	}
	if fi, err := os.Stat(workCurrent); err != nil || !fi.IsDir() {
		return os.ErrNotExist
	}
	labels := f.Labels
	if len(labels) > feedFindingMaxLabels {
		labels = labels[:feedFindingMaxLabels]
	}
	sev := "warn"
	action := "continue"
	if f.Severity == "critical" {
		sev = "critical"
		action = "surface_to_operator"
	}
	ts := now.UTC().Format(time.RFC3339)
	rec := map[string]any{
		"ts": ts, "batch_size": 0, "scores": map[string]any{},
		"overall": "WARN", "synthetic": true, "source": "event-scan",
		"severity": sev, "runtime": f.Runtime, "event_kind": f.Kind, "labels": labels,
		"rationale": "detect-only output scan (" + f.Runtime + " " + f.Kind + "): " +
			strings.Join(labels, "; ") + ". Review the run; the output was not blocked.",
		"recommended_action": action,
	}
	if err := appendNoFollow(filepath.Join(workCurrent, "supervisor-findings.ndjson"), rec); err != nil {
		return err
	}
	appendPending(filepath.Join(workCurrent, ".supervisor-pending."+sessionKey(f.Session)), map[string]any{
		"ts": ts, "agent": f.Runtime, "tool": "event-scan:" + f.Kind, "session_id": sessionKey(f.Session),
		"input": map[string]any{"labels": labels, "severity": sev},
	})
	return nil
}

func appendNoFollow(path string, rec map[string]any) error {
	if fi, err := os.Lstat(path); err == nil && !fi.Mode().IsRegular() {
		return os.ErrInvalid
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
