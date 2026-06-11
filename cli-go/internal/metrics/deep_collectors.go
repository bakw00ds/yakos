package metrics

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// dispatchRunner is an interface so yakos dispatch calls can be replaced
// in tests without invoking a real LLM. Mirrors the gitRunner seam.
type dispatchRunner interface {
	// Dispatch runs `yakos dispatch <agent> "<task>"` and returns its combined
	// stdout output. An error is returned on exec failure or non-zero exit.
	Dispatch(agent, task string) (string, error)
}

// realDispatchRunner shells out to `yakos dispatch` with YAKOS_IMPL=go.
type realDispatchRunner struct{}

func (r realDispatchRunner) Dispatch(agent, task string) (string, error) {
	// Use the yakos binary already on PATH; honour YAKOS_IMPL=go if set.
	cmd := exec.Command("yakos", "dispatch", agent, task) //nolint:gosec
	cmd.Env = append(cmd.Environ(), "YAKOS_IMPL=go")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("dispatch %s: %w", agent, err)
	}
	return string(out), nil
}

// extractJSON finds the first '{' ... '}' or '[' ... ']' balanced JSON
// object/array in raw and returns it. Returns "" if none found.
// This is intentionally lenient: agents may wrap JSON in prose.
func extractJSON(raw string) string {
	// Try object first, then array.
	for _, pair := range [][2]byte{
		{'{', '}'},
		{'[', ']'},
	} {
		open, close := pair[0], pair[1]
		start := strings.IndexByte(raw, open)
		if start < 0 {
			continue
		}
		depth := 0
		inStr := false
		escape := false
		for i := start; i < len(raw); i++ {
			ch := raw[i]
			if escape {
				escape = false
				continue
			}
			if ch == '\\' && inStr {
				escape = true
				continue
			}
			if ch == '"' {
				inStr = !inStr
				continue
			}
			if inStr {
				continue
			}
			switch ch {
			case open:
				depth++
			case close:
				depth--
				if depth == 0 {
					return raw[start : i+1]
				}
			}
		}
	}
	return ""
}

// runDeepCollectors runs all [S] collectors and marks snap.Deep=true.
// Each collector is best-effort: on any failure it emits nil metrics and
// records a status entry. Never blocks; never returns an error.
// If runner is nil, all [S] collectors record "dispatch-failed" status and
// leave their metric fields nil (safe degradation).
func runDeepCollectors(runner dispatchRunner, snap *Snapshot) {
	snap.Deep = true
	if runner == nil {
		snap.ToolStatus["code-review"] = "dispatch-failed"
		snap.ToolStatus["security-review"] = "dispatch-failed"
		return
	}
	collectCodeReviewFindings(runner, snap)
	collectSecurityReviewFindings(runner, snap)
}

// codeReviewTally is the strict JSON shape demanded from the code-reviewer
// agent.  Only this shape is accepted; anything else → nil + unparseable.
type codeReviewTally struct {
	FindingsBySeverity map[string]int `json:"findings_by_severity"`
}

// collectCodeReviewFindings dispatches the code-reviewer agent and parses
// a structured severity tally into CodeQuality.ReviewFindingsBySeverity.
//
// Prompt demands:
//
//	{"findings_by_severity":{"P0":<n>,"P1":<n>,"P2":<n>,"P3":<n>}}
//
// Status keys: "code-review" → "ok" | "dispatch-failed" | "unparseable".
func collectCodeReviewFindings(runner dispatchRunner, snap *Snapshot) {
	const statusKey = "code-review"
	task := `Review the working tree for code quality issues. ` +
		`Respond ONLY with a single JSON object, no prose before or after: ` +
		`{"findings_by_severity":{"P0":<critical bugs>,"P1":<major issues>,"P2":<minor issues>,"P3":<nits>}} ` +
		`where each value is an integer count. P0=data-loss/crash, P1=correctness, P2=maintainability, P3=style.`

	out, err := runner.Dispatch("code-reviewer", task)
	if err != nil {
		snap.ToolStatus[statusKey] = "dispatch-failed"
		return
	}

	extracted := extractJSON(out)
	if extracted == "" {
		snap.ToolStatus[statusKey] = "unparseable"
		return
	}

	var tally codeReviewTally
	if err := json.Unmarshal([]byte(extracted), &tally); err != nil {
		snap.ToolStatus[statusKey] = "unparseable"
		return
	}
	if tally.FindingsBySeverity == nil {
		// JSON parsed but key absent — treat as unparseable to keep nil invariant.
		snap.ToolStatus[statusKey] = "unparseable"
		return
	}

	snap.Metrics.CodeQuality.ReviewFindingsBySeverity = tally.FindingsBySeverity
	snap.ToolStatus[statusKey] = "ok"
}

// securityReviewTally is the strict JSON shape demanded from the
// security-reviewer agent.
type securityReviewTally struct {
	FindingsBySeverity map[string]int `json:"findings_by_severity"`
}

// collectSecurityReviewFindings dispatches the security-reviewer agent and
// parses a structured severity tally into
// Security.SecurityReviewFindingsBySeverity.
//
// Prompt demands:
//
//	{"findings_by_severity":{"P0":<n>,"P1":<n>,"P2":<n>,"P3":<n>}}
//
// Status keys: "security-review" → "ok" | "dispatch-failed" | "unparseable".
func collectSecurityReviewFindings(runner dispatchRunner, snap *Snapshot) {
	const statusKey = "security-review"
	task := `Review the working tree for security vulnerabilities and weaknesses. ` +
		`Respond ONLY with a single JSON object, no prose before or after: ` +
		`{"findings_by_severity":{"P0":<critical/RCE/auth-bypass>,"P1":<high/data-exposure>,"P2":<medium/defense-in-depth>,"P3":<low/hardening>}} ` +
		`where each value is an integer count. ` +
		`P0=remote code execution or authentication bypass, ` +
		`P1=sensitive data exposure or privilege escalation, ` +
		`P2=defense-in-depth weaknesses, P3=low-severity hardening opportunities.`

	out, err := runner.Dispatch("security-reviewer", task)
	if err != nil {
		snap.ToolStatus[statusKey] = "dispatch-failed"
		return
	}

	extracted := extractJSON(out)
	if extracted == "" {
		snap.ToolStatus[statusKey] = "unparseable"
		return
	}

	var tally securityReviewTally
	if err := json.Unmarshal([]byte(extracted), &tally); err != nil {
		snap.ToolStatus[statusKey] = "unparseable"
		return
	}
	if tally.FindingsBySeverity == nil {
		snap.ToolStatus[statusKey] = "unparseable"
		return
	}

	snap.Metrics.Security.SecurityReviewFindingsBySeverity = tally.FindingsBySeverity
	snap.ToolStatus[statusKey] = "ok"
}
