// Package planoutcomecapture is the Go-native Tier-0 port of
// lib/hooks/plan-outcome-capture.sh.
//
// SessionEnd hook (fallback) for outcome capture when the operator does not
// run 'yakos work close'. This is a TELEMETRY HOOK. It NEVER blocks.
// Always exits 0.
//
// What it does:
//  1. Looks for the most recent plan_scored record that does NOT yet have a
//     corresponding plan_outcome record (same plan_id).
//  2. If found, computes best-effort outcome fields (git diff stats via
//     git diff --numstat HEAD, dispatch-log sums) and appends a
//     plan_outcome record to the log.
//  3. human_signal is always null (no interactive prompt in SessionEnd path).
//
// State file: YAKOS_PLAN_QUALITY_LOG or ~/.yakos-state/plan-quality-log.ndjson.
package planoutcomecapture

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "plan-outcome-capture"

// planScoredRecord is the minimal shape of a plan_scored NDJSON line.
type planScoredRecord struct {
	Type    string `json:"type"`
	PlanID  string `json:"plan_id"`
	Project string `json:"project"`
}

// planOutcomeRecord is the shape written by this hook.
type planOutcomeRecord struct {
	Type                   string   `json:"type"`
	Ts                     string   `json:"ts"`
	PlanID                 string   `json:"plan_id"`
	Project                string   `json:"project"`
	FirstTryPass           *bool    `json:"first_try_pass"`
	ReworkCycles           *int     `json:"rework_cycles"`
	FilesTouched           *int     `json:"files_touched"`
	FilesTouchedEstimated  *bool    `json:"files_touched_estimated"`
	ScopeCreepRatio        *float64 `json:"scope_creep_ratio"`
	LinesChanged           *int     `json:"lines_changed"`
	TokensSpentTotal       *int     `json:"tokens_spent_total"`
	CostUSDActual          *float64 `json:"cost_usd_actual"`
	ReworkFailureModes     []string `json:"rework_failure_modes"`
	HumanSignal            *string  `json:"human_signal"`
	CaptureMethod          string   `json:"capture_method"`
}

// Hook implements runner.Hook for plan outcome capture.
type Hook struct {
	// PlanQualityLog overrides the default log path.
	// When empty, YAKOS_PLAN_QUALITY_LOG env or ~/.yakos-state/plan-quality-log.ndjson.
	PlanQualityLog string

	// DispatchLog overrides the dispatch log path.
	DispatchLog string

	// HomeDir overrides $HOME for path resolution.
	HomeDir string

	// NowFn is injected for tests.
	NowFn func() time.Time

	// GitRunner is injected for tests to replace the real git subprocess.
	// When nil, the real exec.Command is used.
	GitRunner func(dir string) (files, lines int, err error)
}

// New returns a Hook with sensible defaults.
func New() *Hook {
	return &Hook{NowFn: time.Now}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the plan-outcome-capture logic. Always returns ExitCode 0.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	logPath := h.resolveLogPath(in)
	if logPath == "" {
		return out, nil
	}

	// Find most-recent plan_scored without a plan_outcome.
	planID, projectDir := h.findUnmatchedPlan(logPath, in)
	if planID == "" {
		h.appendLog(&out, logPath, "REPORT", "pass",
			"all scored plans already have outcome records", map[string]any{})
		return out, nil
	}

	if projectDir == "" {
		projectDir = in.Env["CLAUDE_PROJECT_DIR"]
		if projectDir == "" {
			projectDir = in.WorkDir
		}
	}

	// Git diff stats.
	var filesPtr *int
	var linesPtr *int
	gitRunner := h.GitRunner
	if gitRunner == nil {
		gitRunner = realGitDiffStats
	}
	if files, lines, err := gitRunner(projectDir); err == nil && files >= 0 {
		filesPtr = &files
		linesPtr = &lines
	}

	// Dispatch log stats.
	var tokensPtr *int
	var costPtr *float64
	dispatchLog := h.resolveDispatchLog(in)
	if dispatchLog != "" {
		if tokens, cost, err := readDispatchLogStats(dispatchLog); err == nil {
			tokensPtr = &tokens
			costPtr = &cost
		}
	}

	ts := h.NowFn().UTC().Format("2006-01-02T15:04:05Z")
	record := planOutcomeRecord{
		Type:               "plan_outcome",
		Ts:                 ts,
		PlanID:             planID,
		Project:            projectDir,
		FilesTouched:       filesPtr,
		LinesChanged:       linesPtr,
		TokensSpentTotal:   tokensPtr,
		CostUSDActual:      costPtr,
		ReworkFailureModes: []string{},
		CaptureMethod:      "session_end_fallback",
	}

	line, err := json.Marshal(record)
	if err != nil {
		h.appendLog(&out, logPath, "REPORT", "pass",
			"failed to build record JSON; skipping", map[string]any{"plan_id": planID})
		return out, nil
	}

	if writeErr := appendToLog(logPath, line); writeErr != nil {
		h.appendLog(&out, logPath, "REPORT", "pass",
			"failed to write record; skipping",
			map[string]any{"plan_id": planID, "error": writeErr.Error()})
		return out, nil
	}

	extra := map[string]any{"plan_id": planID}
	if filesPtr != nil {
		extra["files_touched"] = *filesPtr
	}
	h.appendLog(&out, logPath, "REPORT", "pass",
		"best-effort outcome captured for plan_id="+planID, extra)

	return out, nil
}

// ---- log path resolution -----------------------------------------------------

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

func (h *Hook) resolveDispatchLog(in hooktype.HookInput) string {
	if h.DispatchLog != "" {
		return h.DispatchLog
	}
	if env := in.Env["YAKOS_DISPATCH_LOG"]; env != "" {
		return env
	}
	home := h.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".yakos-state", "dispatch-log.ndjson")
}

// ---- scan for unmatched plan -------------------------------------------------

// findUnmatchedPlan returns the most-recent plan_scored plan_id that has no
// plan_outcome record in the log. Returns ("", "") when none found.
func (h *Hook) findUnmatchedPlan(logPath string, in hooktype.HookInput) (planID, projectDir string) {
	f, err := os.Open(logPath) //nolint:gosec
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()

	type scored struct {
		planID     string
		projectDir string
	}
	var scoredList []scored
	outcomeIDs := map[string]bool{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			PlanID  string `json:"plan_id"`
			Project string `json:"project"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "plan_scored":
			if rec.PlanID != "" {
				scoredList = append(scoredList, scored{rec.PlanID, rec.Project})
			}
		case "plan_outcome":
			if rec.PlanID != "" {
				outcomeIDs[rec.PlanID] = true
			}
		}
	}

	// Find the last scored plan_id without a matching outcome.
	for i := len(scoredList) - 1; i >= 0; i-- {
		s := scoredList[i]
		if !outcomeIDs[s.planID] {
			return s.planID, s.projectDir
		}
	}
	return "", ""
}

// ---- git diff stats ----------------------------------------------------------

// realGitDiffStats runs git diff --numstat HEAD in dir and returns
// (files, lines, error).
func realGitDiffStats(dir string) (files, lines int, err error) {
	cmd := exec.Command("git", "-C", dir, "diff", "--numstat", "HEAD") //nolint:gosec
	out, err := cmd.Output()
	if err != nil {
		return -1, -1, err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return 0, 0, nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])
		files++
		lines += added + deleted
	}
	return files, lines, nil
}

// ---- dispatch log stats ------------------------------------------------------

type dispatchLine struct {
	TokensTotal *int     `json:"tokens_total"`
	CostUSD     *float64 `json:"cost_usd"`
}

func readDispatchLogStats(logPath string) (tokens int, cost float64, err error) {
	data, err := os.ReadFile(logPath) //nolint:gosec
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec dispatchLine
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			continue
		}
		if rec.TokensTotal != nil {
			tokens += *rec.TokensTotal
		}
		if rec.CostUSD != nil {
			cost += *rec.CostUSD
		}
	}
	return tokens, cost, nil
}

// ---- append to log -----------------------------------------------------------

func appendToLog(logPath string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// ---- log helper --------------------------------------------------------------

func (h *Hook) appendLog(out *hooktype.HookOutput, logPath, severity, action, message string, extra map[string]any) {
	// Derive log file path from the state log directory.
	logDir := filepath.Dir(logPath)
	logFile := filepath.Join(logDir, "hooks", hookName+".ndjson")

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
