// Package supervisorackgate is the Go-native Tier-0 port of
// lib/hooks/supervisor-ack-gate.sh.
//
// PreToolUse hook that blocks TeamCreate and Agent dispatches when there are
// unacknowledged supervisor findings at or above the surface_to_operator tier.
//
// Finding severity ladder (ordered):
//
//	continue < surface_to_operator < block_next_tool < halt
//
// The gate fires when ANY finding has recommended_action in:
//
//	surface_to_operator | block_next_tool | halt
//
// AND has not been acknowledged in ~/.yakos-state/supervisor-acks.ndjson
// for this project.
//
// Finding ID scheme: "f-<ts>-<project>-<linenum>" where:
//   - <ts>      = finding's .ts field (ISO-8601, colons replaced with -)
//   - <project> = YAKOS_PROJECT_NAME (or basename of CLAUDE_PROJECT_DIR)
//   - <linenum> = 1-based line index in supervisor-findings.ndjson
//
// Conservative failure mode: any infra issue → WARN + pass.
// Emergency bypass: YAKOS_SUPERVISOR_DISABLE=1.
// Per-project opt-out: supervise.gate_on_escalation: false in .yakos.yml.
package supervisorackgate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "supervisor-ack-gate"

type yakosYMLSupervise struct {
	Supervise *superviseConfig `yaml:"supervise"`
}

type superviseConfig struct {
	GateOnEscalation *bool `yaml:"gate_on_escalation"`
}

// finding is a partial parse of supervisor-findings.ndjson.
type finding struct {
	Ts                string `json:"ts"`
	RecommendedAction string `json:"recommended_action"`
	Rationale         string `json:"rationale"`
}

// ackRecord is a partial parse of supervisor-acks.ndjson.
type ackRecord struct {
	Project   string `json:"project"`
	FindingID string `json:"finding_id"`
}

// Hook implements runner.Hook for supervisor ack gating.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the session.
	WorkCurrentDir string

	// ProjectDir is the project root for .yakos.yml.
	ProjectDir string

	// AcksFile overrides the default supervisor-acks.ndjson path.
	// When empty, ~/.yakos-state/supervisor-acks.ndjson is used.
	AcksFile string

	// HomeDir overrides $HOME for path resolution.
	HomeDir string

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

// Run executes the supervisor-ack-gate logic.
// Returns ExitCode=2 (block) when unacknowledged escalating findings exist.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Only gate on dispatch tools.
	switch in.Tool {
	case "TeamCreate", "Agent":
	default:
		return out, nil
	}

	// Emergency bypass.
	if in.Env["YAKOS_SUPERVISOR_DISABLE"] == "1" {
		return out, nil
	}

	// Per-project opt-out.
	projectDir := h.resolveProjectDir(in)
	if h.isGateDisabledByYAML(projectDir) {
		return out, nil
	}

	// Hard dependency: WorkCurrentDir.
	if h.WorkCurrentDir == "" {
		h.appendLog(&out, "WARN", "pass",
			"WorkCurrentDir not set; skipping supervisor-ack-gate", map[string]any{})
		return out, nil
	}

	findingsFile := filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson")
	if _, err := os.Stat(findingsFile); os.IsNotExist(err) {
		h.appendLog(&out, "REPORT", "pass",
			"no supervisor-findings.ndjson; nothing to gate on", map[string]any{})
		return out, nil
	}

	projectName := h.resolveProjectName(in)
	acksFile := h.resolveAcksFile(in)
	acksSet := h.loadAcks(acksFile, projectName)

	// Scan findings.
	pendingIDs, pendingSummaries, warningLines := h.scanFindings(findingsFile, projectName, acksSet)

	// Log any per-line warnings.
	for _, w := range warningLines {
		h.appendLog(&out, "WARN", "pass", w, map[string]any{})
	}

	if len(pendingIDs) == 0 {
		h.appendLog(&out, "REPORT", "pass",
			"no unacknowledged supervisor escalations",
			map[string]any{"project": projectName})
		return out, nil
	}

	firstID := pendingIDs[0]
	h.appendLog(&out, "BLOCK", "block",
		fmt.Sprintf("blocking %d unacknowledged supervisor escalation(s)", len(pendingIDs)),
		map[string]any{"project": projectName, "pending_count": len(pendingIDs),
			"finding_ids": strings.Join(pendingIDs, "\n")})

	msg := fmt.Sprintf(
		"Supervisor escalation pending — acknowledge before next dispatch:\n"+
			"%s\n"+
			"Acknowledge with:\n"+
			"  yakos supervise ack %s [--note \"rationale\"]\n"+
			"Or list pending:  yakos supervise pending\n"+
			"Or ack all:       yakos supervise ack-all [--note \"...\"]\n"+
			"Per-project disable: set supervise.gate_on_escalation: false in .yakos.yml\n"+
			"Emergency (session only): export YAKOS_SUPERVISOR_DISABLE=1",
		strings.Join(pendingSummaries, "\n"),
		firstID)
	out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
	out.ExitCode = 2
	return out, nil
}

// ---- findings scanner -------------------------------------------------------

// needsAck returns true for recommended_action values that require acknowledgement.
func needsAck(action string) bool {
	switch action {
	case "surface_to_operator", "block_next_tool", "halt":
		return true
	}
	return false
}

// deriveFindingID builds a stable finding ID from ts, project, and 1-based
// line number — identical to the bash hook's scheme.
func deriveFindingID(ts, project string, linenum int) string {
	safeTS := strings.ReplaceAll(ts, ":", "-")
	return fmt.Sprintf("f-%s-%s-%d", safeTS, project, linenum)
}

func (h *Hook) scanFindings(findingsFile, projectName string, acksSet map[string]bool) (
	pendingIDs []string, pendingSummaries []string, warnings []string) {

	f, err := os.Open(findingsFile) //nolint:gosec
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("cannot open %s: %v", findingsFile, err)}
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	linenum := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		linenum++

		var rec finding
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			warnings = append(warnings,
				fmt.Sprintf("skipping malformed JSON on line %d of supervisor-findings.ndjson", linenum))
			continue
		}

		if !needsAck(rec.RecommendedAction) {
			continue
		}

		findingID := deriveFindingID(rec.Ts, projectName, linenum)
		if acksSet[findingID] {
			continue
		}

		rationale := rec.Rationale
		if len(rationale) > 120 {
			rationale = rationale[:120] + "..."
		}
		pendingIDs = append(pendingIDs, findingID)
		pendingSummaries = append(pendingSummaries,
			fmt.Sprintf("  [%s] %s: %s", findingID, rec.RecommendedAction, rationale))
	}
	return pendingIDs, pendingSummaries, warnings
}

// ---- ack loading ------------------------------------------------------------

func (h *Hook) loadAcks(acksFile, projectName string) map[string]bool {
	result := map[string]bool{}
	if acksFile == "" {
		return result
	}
	data, err := os.ReadFile(acksFile) //nolint:gosec
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec ackRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Project == projectName && rec.FindingID != "" {
			result[rec.FindingID] = true
		}
	}
	return result
}

// ---- config helpers ---------------------------------------------------------

func (h *Hook) isGateDisabledByYAML(projectDir string) bool {
	if projectDir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(projectDir, ".yakos.yml")) //nolint:gosec
	if err != nil {
		return false
	}
	var doc yakosYMLSupervise
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false
	}
	return doc.Supervise != nil &&
		doc.Supervise.GateOnEscalation != nil &&
		!*doc.Supervise.GateOnEscalation
}

// ---- resolution helpers -----------------------------------------------------

func (h *Hook) resolveProjectDir(in hooktype.HookInput) string {
	if h.ProjectDir != "" {
		return h.ProjectDir
	}
	if d := in.Env["CLAUDE_PROJECT_DIR"]; d != "" {
		return d
	}
	return in.WorkDir
}

func (h *Hook) resolveProjectName(in hooktype.HookInput) string {
	if n := in.Env["YAKOS_PROJECT_NAME"]; n != "" {
		return n
	}
	if d := in.Env["CLAUDE_PROJECT_DIR"]; d != "" {
		return filepath.Base(d)
	}
	return "unknown"
}

func (h *Hook) resolveAcksFile(in hooktype.HookInput) string {
	if h.AcksFile != "" {
		return h.AcksFile
	}
	home := h.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".yakos-state", "supervisor-acks.ndjson")
}

// ---- log helper -------------------------------------------------------------

func (h *Hook) appendLog(out *hooktype.HookOutput, severity, action, message string, extra map[string]any) {
	if h.WorkCurrentDir == "" {
		return
	}
	logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
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
