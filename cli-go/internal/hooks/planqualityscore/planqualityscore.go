// Package planqualityscore is the Go-native Tier-0 port of
// lib/hooks/plan-quality-score.sh (the PostToolUse half of the old
// plan-quality-gate hook, split out under K-81).
//
// PostToolUse (Edit|Write|MultiEdit): when a write targets
// work/current/plan.md, reads the latest plan_scored record. If mode=block
// and aggregate is below threshold, writes the .plan-blocked marker that the
// fail-closed planqualitygate hook enforces. If mode=surface, writes a notes
// file. The actual scoring subprocess (score-plan.sh) is NOT run from Go — Go
// reads a pre-existing log record that the scoring subprocess wrote,
// matching the Tier-0 design (Q8; no subprocess fork).
//
// Emergency bypass: YAKOS_PLAN_QUALITY_DISABLE=1.
//
// Conservative failure mode: any infra error → pass (exit 0). The hook NEVER
// prevents the operator from saving a plan because scoring infrastructure
// broke. This is deliberately the opposite of planqualitygate, which fails
// closed.
package planqualityscore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "plan-quality-score"

// planQualityConfig holds the parsed plan_quality block from .yakos.yml.
type planQualityConfig struct {
	Enabled   *bool    `yaml:"enabled"`
	Mode      string   `yaml:"mode"`
	Threshold *float64 `yaml:"threshold"`
}

type yakosYMLPlanQuality struct {
	PlanQuality *planQualityConfig `yaml:"plan_quality"`
}

// planBlockedMarker is the JSON written to .plan-blocked.
type planBlockedMarker struct {
	PlanID         string  `json:"plan_id"`
	AggregateScore float64 `json:"aggregate_score"`
	Threshold      float64 `json:"threshold"`
	Ts             string  `json:"ts"`
	Reason         string  `json:"reason"`
}

// logRecord is the minimal shape of a plan_scored NDJSON line.
type logRecord struct {
	Type           string  `json:"type"`
	PlanID         string  `json:"plan_id"`
	AggregateScore float64 `json:"aggregate_score"`
	Threshold      float64 `json:"threshold"`
	Verdict        string  `json:"verdict"`
	Dissent        bool    `json:"dissent"`
	PanelSize      int     `json:"panel_size"`
}

const defaultThreshold = 0.75

// Hook implements runner.Hook for plan quality scoring.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active session.
	WorkCurrentDir string

	// ProjectDir is the project root where .yakos.yml is located.
	ProjectDir string

	// PlanQualityLog overrides the default log path.
	// When empty, YAKOS_PLAN_QUALITY_LOG env or ~/.yakos-state/plan-quality-log.ndjson.
	PlanQualityLog string

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

// Run executes the plan-quality-score logic. It never blocks: ExitCode is
// always 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Emergency bypass.
	if in.Env["YAKOS_PLAN_QUALITY_DISABLE"] == "1" {
		return out, nil
	}

	if in.Event == "PostToolUse" {
		return h.runPostToolUse(out, in)
	}
	return out, nil
}

// ---- PostToolUse path -------------------------------------------------------

func (h *Hook) runPostToolUse(out hooktype.HookOutput, in hooktype.HookInput) (hooktype.HookOutput, error) {
	switch in.Tool {
	case "Edit", "Write", "MultiEdit":
	default:
		return out, nil
	}

	// Only act when the write targets plan.md.
	filePath := hookio.ToolFilePath(in)
	// Normalise to forward slashes before the suffix check so that Windows
	// paths (which use "\") and mixed-separator paths both match the
	// canonical "/work/current/plan.md" suffix.
	if !strings.HasSuffix(filepath.ToSlash(filePath), "/work/current/plan.md") {
		return out, nil
	}

	projectDir := h.resolveProjectDir(in)

	// Load plan_quality config.
	cfg := h.loadConfig(projectDir)
	if cfg != nil && cfg.Enabled != nil && !*cfg.Enabled {
		h.appendLog(&out, "REPORT", "pass", "plan_quality.enabled=false; skipping",
			map[string]any{"file_path": filePath})
		return out, nil
	}

	// Read the most-recent plan_scored record from the log.
	logPath := h.resolveLogPath(in)
	if logPath == "" {
		return out, nil
	}

	record, err := lastScoredRecord(logPath)
	if err != nil || record == nil {
		// No scored record: pass conservatively.
		h.appendLog(&out, "WARN", "pass",
			"no plan_scored record found in quality log; passing",
			map[string]any{"log_path": logPath})
		return out, nil
	}

	threshold := defaultThreshold
	if cfg != nil && cfg.Threshold != nil {
		threshold = *cfg.Threshold
	}

	currentDir := h.WorkCurrentDir
	planID := record.PlanID
	aggregate := record.AggregateScore
	dissent := record.Dissent
	mode := "surface"
	if cfg != nil && cfg.Mode != "" {
		mode = cfg.Mode
	}

	ts := h.NowFn().UTC().Format("2006-01-02T15:04:05Z")

	// Dissent path (takes priority).
	if dissent {
		if currentDir != "" {
			notesFile := filepath.Join(currentDir, "notes", fmt.Sprintf("plan-quality-%s.md", planID))
			_ = os.MkdirAll(filepath.Dir(notesFile), 0755) //nolint:gosec
			content := fmt.Sprintf("# Plan Quality Surface — %s\n\n"+
				"**Reason:** Judge panel dissent\n\n"+
				"## Score summary\n\nplan_id: %s\naggregate_score: %.4f\n",
				planID, planID, aggregate)
			_ = os.WriteFile(notesFile, []byte(content), 0644) //nolint:gosec
		}
		h.appendLog(&out, "WARN", "surface_to_operator",
			"plan-quality-score: dissent detected; surfaced to operator",
			map[string]any{"plan_id": planID, "aggregate_score": aggregate,
				"decision": "surface_to_operator"})
		return out, nil
	}

	// Threshold pass path.
	if aggregate >= threshold {
		h.appendLog(&out, "REPORT", "pass",
			fmt.Sprintf("plan-quality-score: aggregate=%.4f >= threshold=%.4f; PASS", aggregate, threshold),
			map[string]any{"plan_id": planID, "aggregate_score": aggregate,
				"threshold": threshold, "decision": "pass"})
		return out, nil
	}

	// Below threshold path.
	if currentDir != "" {
		notesFile := filepath.Join(currentDir, "notes", fmt.Sprintf("plan-quality-%s.md", planID))
		_ = os.MkdirAll(filepath.Dir(notesFile), 0755) //nolint:gosec
		content := fmt.Sprintf("# Plan Quality Surface — %s\n\n"+
			"**Reason:** Aggregate score %.4f is below threshold %.4f.\n\n"+
			"## Score summary\n\nplan_id: %s\naggregate_score: %.4f\n",
			planID, aggregate, threshold, planID, aggregate)
		_ = os.WriteFile(notesFile, []byte(content), 0644) //nolint:gosec
	}

	if mode == "block" && currentDir != "" {
		marker := planBlockedMarker{
			PlanID:         planID,
			AggregateScore: aggregate,
			Threshold:      threshold,
			Ts:             ts,
			Reason:         fmt.Sprintf("aggregate %.4f < threshold %.4f; plan quality below bar", aggregate, threshold),
		}
		markerData, _ := json.Marshal(marker)
		blockedMarkerFile := filepath.Join(currentDir, ".plan-blocked")
		_ = atomicWrite(blockedMarkerFile, markerData)
		h.appendLog(&out, "WARN", "block_next_tool",
			fmt.Sprintf("plan-quality-score: aggregate=%.4f < threshold=%.4f; mode=block; .plan-blocked written",
				aggregate, threshold),
			map[string]any{"plan_id": planID, "aggregate_score": aggregate,
				"threshold": threshold, "decision": "block_next_tool",
				"marker": filepath.Join(currentDir, ".plan-blocked")})
	} else {
		h.appendLog(&out, "WARN", "surface_to_operator",
			fmt.Sprintf("plan-quality-score: aggregate=%.4f < threshold=%.4f; mode=surface; surfaced",
				aggregate, threshold),
			map[string]any{"plan_id": planID, "aggregate_score": aggregate,
				"threshold": threshold, "decision": "surface_to_operator"})
	}
	return out, nil
}

// ---- config helpers ---------------------------------------------------------

func (h *Hook) loadConfig(projectDir string) *planQualityConfig {
	if projectDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(projectDir, ".yakos.yml")) //nolint:gosec
	if err != nil {
		return nil
	}
	var doc yakosYMLPlanQuality
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return doc.PlanQuality
}

// ---- log path resolution ----------------------------------------------------

func (h *Hook) resolveLogPath(in hooktype.HookInput) string {
	if h.PlanQualityLog != "" {
		return h.PlanQualityLog
	}
	if env := in.Env["YAKOS_PLAN_QUALITY_LOG"]; env != "" {
		return env
	}
	home := h.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".yakos-state", "plan-quality-log.ndjson")
}

// ---- log reader -------------------------------------------------------------

func lastScoredRecord(logPath string) (*logRecord, error) {
	data, err := os.ReadFile(logPath) //nolint:gosec
	if err != nil {
		return nil, err
	}
	var last *logRecord
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, `"plan_scored"`) {
			continue
		}
		var rec logRecord
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			continue
		}
		if rec.Type == "plan_scored" {
			r := rec
			last = &r
		}
	}
	return last, nil
}

// ---- atomic write -----------------------------------------------------------

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil { //nolint:gosec
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------

func (h *Hook) resolveProjectDir(in hooktype.HookInput) string {
	if h.ProjectDir != "" {
		return h.ProjectDir
	}
	if d := in.Env["CLAUDE_PROJECT_DIR"]; d != "" {
		return d
	}
	return in.WorkDir
}

func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

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
