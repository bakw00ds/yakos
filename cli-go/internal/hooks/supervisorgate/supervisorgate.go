// Package supervisorgate is the Go-native Tier-0 port of
// lib/hooks/supervisor-gate.sh.
//
// PreToolUse hook that consults supervisor findings and blocks on critical
// drift (v0.33+).
//
// Reads the most recent finding from supervisor-findings.ndjson and:
//   - PASS     → exit 0 (allow)
//   - WARN     → exit 0 (allow), surface rationale via Stderr to the lead
//   - CRITICAL → check block_on_critical; if true, block (exit 2).
//     Otherwise surface via Stderr.
//
// Idempotency: tracks the last-surfaced finding_ts in a marker file
// (.supervisor-gate-last-surfaced) to avoid re-surfacing the same finding.
//
// Bypass: hook-bypass.md entry with Hook: supervisor, Scope: finding=<ts>.
//
// Disabled when:
//   - YAKOS_SUPERVISOR_DISABLE=1
//   - .yakos.yml supervisor.enabled: false
//   - .yakos.yml supervisor.block_on_critical: false (passive mode)
//
// Both .yakos.yml settings are read the way the bash script reads them — a
// grep -A 10 window after a "supervisor:" line, not a YAML parse — so
// (for example) "block_on_critical: false # note" is NOT passive mode, on
// either side. Log records go through internal/hooks/hooklog and the stderr
// text is byte-identical to lib/hooks/supervisor-gate.sh.
package supervisorgate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookbypass"
	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "supervisor-gate"

// ws is grep's [[:space:]] within one line.
const ws = `[ \t\v\f\r]`

var (
	reSupervisorStart = regexp.MustCompile(`^` + ws + `*supervisor:`)
	reEnabledFalse    = regexp.MustCompile(`^` + ws + `*enabled:` + ws + `*false` + ws + `*$`)
	reBlockOnCritical = regexp.MustCompile(`^` + ws + `*block_on_critical:` + ws + `*(true|false)`)
)

// Hook implements runner.Hook for the supervisor gate.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the session.
	WorkCurrentDir string

	// ProjectDir is the project root for .yakos.yml.
	ProjectDir string

	// NowFn is injected for tests.
	NowFn func() time.Time
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir, projectDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		ProjectDir:     projectDir,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the supervisor-gate logic. ExitCode 2 only for a CRITICAL
// finding when block_on_critical is not "false" and no bypass matches.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	if in.Env["YAKOS_SUPERVISOR_DISABLE"] == "1" {
		return out, nil
	}

	yml := h.readYML(in)
	if windowHasLine(yml, reEnabledFalse, 10) {
		return out, nil
	}

	if h.WorkCurrentDir == "" {
		return out, nil
	}

	findingsFile := filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson")
	if fi, err := os.Stat(findingsFile); err != nil || !fi.Mode().IsRegular() {
		return out, nil
	}
	last := tailLine(findingsFile)
	if last == "" {
		return out, nil
	}

	// `jq empty` accepts any JSON value; the field reads below only make
	// sense on an object. Both sides (bash since K-107) treat a valid-JSON
	// non-object such as [] or 5 as the same "not a usable finding" case as
	// invalid JSON and pass with a WARN.
	var parsed any
	obj, isObj := map[string]any(nil), false
	if err := json.Unmarshal([]byte(last), &parsed); err == nil {
		obj, isObj = parsed.(map[string]any)
	}
	if !isObj {
		h.log(&out, in, "WARN", "pass", "most-recent finding is not valid JSON; ignoring", map[string]any{})
		return out, nil
	}

	field := func(key, def string) string {
		v := hookio.JQAlt(obj[key])
		if v == nil {
			return def
		}
		return strings.TrimRight(hookio.JQRawOrJSON(v), "\n")
	}
	overall := field("overall", "PASS")
	rationale := field("rationale", "(no rationale)")
	findingTS := field("ts", "unknown")
	recommended := field("recommended_action", "continue")

	markerFile := filepath.Join(h.WorkCurrentDir, ".supervisor-gate-last-surfaced")
	lastSurfaced := ""
	if data, err := os.ReadFile(markerFile); err == nil { //nolint:gosec
		lastSurfaced = strings.TrimRight(string(data), "\n")
	}

	switch overall {
	case "PASS":
		h.log(&out, in, "REPORT", "pass", "supervisor finding: PASS",
			map[string]any{"finding_ts": findingTS, "overall": "PASS"})
		return out, nil

	case "WARN":
		if findingTS != lastSurfaced {
			h.log(&out, in, "WARN", "pass", "supervisor WARN: "+rationale,
				map[string]any{"finding_ts": findingTS, "overall": "WARN", "rationale": rationale})
			out.Stderr = fmt.Appendf(out.Stderr,
				"supervisor-gate: WARN from supervisor (finding %s):\n  %s\n  Recommended action: %s\n  (passing through; this is informational only)\n",
				findingTS, rationale, recommended)
			writeMarker(markerFile, findingTS)
		}
		return out, nil

	case "CRITICAL":
		blockOnCritical := blockOnCriticalFromYML(yml) != "false"

		if h.bypassed("finding=" + findingTS) {
			h.log(&out, in, "WARN", "pass", "supervisor CRITICAL but bypass active for finding="+findingTS,
				map[string]any{"finding_ts": findingTS, "overall": "CRITICAL", "rationale": rationale, "bypass": true})
			return out, nil
		}

		if !blockOnCritical {
			if findingTS != lastSurfaced {
				out.Stderr = fmt.Appendf(out.Stderr,
					"supervisor-gate: CRITICAL from supervisor (finding %s):\n  %s\n  Recommended action: %s\n  (passive mode: supervisor.block_on_critical=false; passing through)\n",
					findingTS, rationale, recommended)
				writeMarker(markerFile, findingTS)
			}
			h.log(&out, in, "WARN", "pass", "supervisor CRITICAL but block_on_critical=false; surfaced only",
				map[string]any{"finding_ts": findingTS, "overall": "CRITICAL", "rationale": rationale, "blocked": false})
			return out, nil
		}

		h.log(&out, in, "BLOCK", "block", "supervisor CRITICAL; blocking",
			map[string]any{"finding_ts": findingTS, "overall": "CRITICAL", "rationale": rationale, "blocked": true})
		out.Stderr = fmt.Appendf(out.Stderr,
			"supervisor-gate: supervisor flagged CRITICAL on finding %s:\n"+
				"       %s\n"+
				"       Recommended action: %s\n"+
				"       To proceed:\n"+
				"         1. Review the finding in work/current/supervisor-findings.ndjson\n"+
				"         2. If the supervisor is wrong, add a bypass entry:\n"+
				"            ## bypass:supervisor-override-%s\n"+
				"            **Hook:** supervisor\n"+
				"            **Scope:** finding=%s\n"+
				"            (plus the standard Hook/Reason/Approved/Created/Expires fields)\n"+
				"         3. Or set supervisor.block_on_critical: false in .yakos.yml\n"+
				"            for passive-mode warnings only.\n"+
				"         4. Emergency bypass for this session only:\n"+
				"            export YAKOS_SUPERVISOR_DISABLE=1\n",
			findingTS, rationale, recommended, findingTS, findingTS)
		out.ExitCode = 2
		return out, nil

	default:
		h.log(&out, in, "WARN", "pass", fmt.Sprintf("unknown supervisor overall: '%s'", overall), map[string]any{})
		return out, nil
	}
}

// ---- bash-faithful readers ----------------------------------------------------

// tailLine mirrors `tail -n 1 file` inside a command substitution: the last
// physical line (an empty last line means "nothing to decide"), trailing
// newlines stripped, NUL bytes dropped.
func tailLine(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if n := len(lines); n > 1 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return strings.ReplaceAll(lines[len(lines)-1], "\x00", "")
}

// windowLines returns, in file order and without repeats, the lines
// `grep -A after <start>` would print.
func windowLines(content string, after int) []string {
	lines := strings.Split(content, "\n")
	emit := make([]bool, len(lines))
	for i, l := range lines {
		if reSupervisorStart.MatchString(l) {
			for j := i; j <= i+after && j < len(lines); j++ {
				emit[j] = true
			}
		}
	}
	var out []string
	for i, l := range lines {
		if emit[i] {
			out = append(out, l)
		}
	}
	return out
}

// windowHasLine mirrors `grep -A n '^[[:space:]]*supervisor:' | grep -q re`.
func windowHasLine(content string, re *regexp.Regexp, after int) bool {
	for _, l := range windowLines(content, after) {
		if re.MatchString(l) {
			return true
		}
	}
	return false
}

// blockOnCriticalFromYML mirrors
//
//	grep -A 10 supervisor: | grep -E '^ws*block_on_critical:ws*(true|false)' \
//	  | head -1 | awk -F: '{print $2}' | tr -d '[:space:]'
//
// including its quirks: the value is field 2 of the first matching line
// split on ':' with ALL whitespace removed, so "false # note" yields
// "false#note" (not "false"). Returns "" when there is no match.
func blockOnCriticalFromYML(content string) string {
	for _, l := range windowLines(content, 10) {
		if !reBlockOnCritical.MatchString(l) {
			continue
		}
		fields := strings.Split(l, ":")
		if len(fields) < 2 {
			return ""
		}
		return strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\n', '\v', '\f', '\r':
				return -1
			}
			return r
		}, fields[1])
	}
	return ""
}

func (h *Hook) readYML(in hooktype.HookInput) string {
	projectDir := h.ProjectDir
	if projectDir == "" {
		projectDir = in.Env["CLAUDE_PROJECT_DIR"]
	}
	if projectDir == "" {
		projectDir = in.WorkDir
	}
	if projectDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(projectDir, ".yakos.yml")) //nolint:gosec
	if err != nil {
		return ""
	}
	return string(data)
}

// bypassed mirrors ho_check_bypass "supervisor" <scope> — note the probe
// hook name is the literal "supervisor", not "supervisor-gate".
func (h *Hook) bypassed(scope string) bool {
	if h.WorkCurrentDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(h.WorkCurrentDir, "hook-bypass.md")) //nolint:gosec
	if err != nil {
		return false
	}
	return hookbypass.Check(string(data), "supervisor", scope)
}

func writeMarker(markerFile, ts string) {
	// bash: printf '%s\n' "$ts" > "$marker" 2>/dev/null || true
	_ = os.WriteFile(markerFile, []byte(ts+"\n"), 0o644) //nolint:gosec
}

func (h *Hook) log(out *hooktype.HookOutput, in hooktype.HookInput, severity, decision, reason string, extra map[string]any) {
	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  severity,
		Decision:  decision,
		Reason:    reason,
		Agent:     senderRole(in),
		SessionID: hookio.JQRawOrJSON(hookio.JQAlt(in.Payload["session_id"])),
		Event:     in.Event,
		Extra:     extra,
	}, now)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, err)
	}
}

// senderRole mirrors hi_sender_role.
func senderRole(in hooktype.HookInput) string {
	return hookio.SenderRole(in)
}
