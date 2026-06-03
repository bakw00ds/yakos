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
package supervisorgate

import (
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

const hookName = "supervisor-gate"

type yakosYMLSupervisor struct {
	Supervisor *supervisorConfig `yaml:"supervisor"`
}

type supervisorConfig struct {
	Enabled         *bool `yaml:"enabled"`
	BlockOnCritical *bool `yaml:"block_on_critical"`
}

// supervisorFinding is the minimal parse of the most-recent finding.
type supervisorFinding struct {
	Ts                string `json:"ts"`
	Overall           string `json:"overall"`
	Rationale         string `json:"rationale"`
	RecommendedAction string `json:"recommended_action"`
}

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

// Run executes the supervisor-gate logic.
// Returns ExitCode=2 (block) only for CRITICAL findings when block_on_critical
// is true and no bypass is present.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Emergency bypass.
	if in.Env["YAKOS_SUPERVISOR_DISABLE"] == "1" {
		return out, nil
	}

	// Config-level disable.
	projectDir := h.resolveProjectDir(in)
	cfg := h.loadConfig(projectDir)
	if cfg != nil && cfg.Enabled != nil && !*cfg.Enabled {
		return out, nil
	}

	if h.WorkCurrentDir == "" {
		return out, nil
	}

	findingsFile := filepath.Join(h.WorkCurrentDir, "supervisor-findings.ndjson")
	if _, err := os.Stat(findingsFile); os.IsNotExist(err) {
		return out, nil
	}

	// Read the most-recent finding.
	last, err := lastFinding(findingsFile)
	if err != nil || last == nil {
		return out, nil
	}
	if !isValidJSON(findingsFile, last) {
		h.appendLog(&out, "WARN", "pass",
			"most-recent finding is not valid JSON; ignoring", map[string]any{})
		return out, nil
	}

	overall := last.Overall
	if overall == "" {
		overall = "PASS"
	}
	rationale := last.Rationale
	if rationale == "" {
		rationale = "(no rationale)"
	}
	recommended := last.RecommendedAction
	if recommended == "" {
		recommended = "continue"
	}
	findingTS := last.Ts
	if findingTS == "" {
		findingTS = "unknown"
	}

	// Idempotency marker.
	markerFile := filepath.Join(h.WorkCurrentDir, ".supervisor-gate-last-surfaced")
	lastSurfaced := h.readMarker(markerFile)

	switch overall {
	case "PASS":
		h.appendLog(&out, "REPORT", "pass", "supervisor finding: PASS",
			map[string]any{"finding_ts": findingTS, "overall": "PASS"})
		return out, nil

	case "WARN":
		if findingTS != lastSurfaced {
			h.appendLog(&out, "WARN", "pass",
				"supervisor WARN: "+rationale,
				map[string]any{"finding_ts": findingTS, "overall": "WARN", "rationale": rationale})
			out.Stderr = fmt.Appendf(out.Stderr,
				"supervisor-gate: WARN from supervisor (finding %s):\n  %s\n  Recommended action: %s\n  (passing through; this is informational only)\n",
				findingTS, rationale, recommended)
			h.writeMarker(markerFile, findingTS)
		}
		return out, nil

	case "CRITICAL":
		// Check bypass.
		bypassScope := "finding=" + findingTS
		if h.isBypassed(bypassScope) {
			h.appendLog(&out, "WARN", "pass",
				"supervisor CRITICAL but bypass active for "+bypassScope,
				map[string]any{"finding_ts": findingTS, "overall": "CRITICAL", "rationale": rationale, "bypass": true})
			return out, nil
		}

		// Check block_on_critical.
		blockOnCritical := true
		if cfg != nil && cfg.BlockOnCritical != nil {
			blockOnCritical = *cfg.BlockOnCritical
		}

		if !blockOnCritical {
			// Passive mode: surface but don't block.
			if findingTS != lastSurfaced {
				out.Stderr = fmt.Appendf(out.Stderr,
					"supervisor-gate: CRITICAL from supervisor (finding %s):\n  %s\n  Recommended action: %s\n  (passive mode: supervisor.block_on_critical=false; passing through)\n",
					findingTS, rationale, recommended)
				h.writeMarker(markerFile, findingTS)
			}
			h.appendLog(&out, "WARN", "pass",
				"supervisor CRITICAL but block_on_critical=false; surfaced only",
				map[string]any{"finding_ts": findingTS, "overall": "CRITICAL", "rationale": rationale, "blocked": false})
			return out, nil
		}

		// Active mode: block.
		h.appendLog(&out, "BLOCK", "block", "supervisor CRITICAL; blocking",
			map[string]any{"finding_ts": findingTS, "overall": "CRITICAL", "rationale": rationale, "blocked": true})
		msg := fmt.Sprintf(
			"supervisor flagged CRITICAL on finding %s:\n"+
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
				"            export YAKOS_SUPERVISOR_DISABLE=1",
			findingTS, rationale, recommended, findingTS, findingTS)
		out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
		out.ExitCode = 2
		return out, nil

	default:
		h.appendLog(&out, "WARN", "pass",
			fmt.Sprintf("unknown supervisor overall: '%s'", overall), map[string]any{})
		return out, nil
	}
}

// ---- finding reader ----------------------------------------------------------

// lastFinding returns the last non-empty finding from the findings file.
func lastFinding(path string) (*supervisorFinding, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	// Walk backwards to find the last non-empty line.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var f supervisorFinding
		if err := json.Unmarshal([]byte(line), &f); err != nil {
			// Return something with an empty Overall so the caller marks invalid JSON.
			return &supervisorFinding{}, nil
		}
		return &f, nil
	}
	return nil, nil
}

// isValidJSON checks whether the last line of the file is valid JSON.
// Called after lastFinding to decide whether to emit the "invalid JSON" log.
func isValidJSON(path string, f *supervisorFinding) bool {
	// If Overall and Ts are both empty, it was an unmarshal failure.
	return f.Overall != "" || f.Ts != ""
}

// ---- bypass check -----------------------------------------------------------

func (h *Hook) isBypassed(scope string) bool {
	if h.WorkCurrentDir == "" {
		return false
	}
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, "supervisor") && strings.Contains(content, scope)
}

// ---- idempotency marker -----------------------------------------------------

func (h *Hook) readMarker(markerFile string) string {
	data, err := os.ReadFile(markerFile) //nolint:gosec
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (h *Hook) writeMarker(markerFile, ts string) {
	_ = os.MkdirAll(filepath.Dir(markerFile), 0755) //nolint:gosec
	tmp := markerFile + ".tmp"
	_ = os.WriteFile(tmp, []byte(ts+"\n"), 0644) //nolint:gosec
	_ = os.Rename(tmp, markerFile)
}

// ---- config helpers ---------------------------------------------------------

func (h *Hook) loadConfig(projectDir string) *supervisorConfig {
	if projectDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(projectDir, ".yakos.yml")) //nolint:gosec
	if err != nil {
		return nil
	}
	var doc yakosYMLSupervisor
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc.Supervisor
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
