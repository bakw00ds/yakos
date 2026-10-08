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
	if f.Severity == "critical" {
		sev = "critical"
	}
	// K-146: a detect-only output finding NEVER carries an ack-gate tier. Model
	// and tool output control these records, so surface_to_operator would let a
	// hostile page halt the lead's dispatch. Both ack gates ignore "review";
	// `yakos supervise pending` lists these records in a separate section. When
	// kill_on_critical fires the kill itself is the response and the ledger's
	// cancel_reason records it.
	const action = "review"
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
	pend, err := json.Marshal(map[string]any{
		"ts": ts, "agent": f.Runtime, "tool": "event-scan:" + f.Kind, "session_id": sessionKey(f.Session),
		"input": map[string]any{"labels": labels, "severity": sev},
	})
	if err != nil {
		return err
	}
	return appendPendingLines(filepath.Join(workCurrent, ".supervisor-pending."+sessionKey(f.Session)), []string{string(pend)})
}

// beforeOpenHook, when a test sets it, runs between the Lstat and the open of
// openRegularNoFollow, where a model with write access to the directory can swap
// the entry. It is nil otherwise.
var beforeOpenHook func(path string)

// openRegularNoFollow opens path for writing without ever following a link or
// touching a non-regular file: Lstat refuses an existing non-regular entry, the
// open carries O_NOFOLLOW and O_NONBLOCK where the platform has them, and the opened descriptor
// must be a regular file that is the same file Lstat saw (a swap between the two
// is refused). Permissions are set by the caller on the descriptor, never the path.
func openRegularNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	pre, lerr := os.Lstat(path)
	if lerr == nil && !pre.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	if beforeOpenHook != nil {
		beforeOpenHook(path)
	}
	// O_NONBLOCK: the Lstat above and this open are two steps, so an entry a model
	// swaps for a FIFO in between would otherwise block an O_WRONLY or O_RDWR open
	// for good (no reader on the other end) and hang the stream goroutine. With it
	// the open of a FIFO fails or returns at once, and the fstat below refuses it.
	// It changes nothing for the regular file this function exists to open.
	f, err := os.OpenFile(path, flag|openNoFollow|openNonblock, perm) //nolint:gosec
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || (lerr == nil && !os.SameFile(pre, fi)) {
		_ = f.Close()
		return nil, os.ErrInvalid
	}
	return f, nil
}

func appendNoFollow(path string, rec map[string]any) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := openRegularNoFollow(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(data, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
