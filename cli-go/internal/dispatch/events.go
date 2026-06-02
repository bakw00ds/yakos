package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
)

const dispatchLogName = "dispatch-log.ndjson"

// dispatchLogPath returns the path to the active dispatch-log file.
// YAKOS_DISPATCH_LOG env var overrides the directory (matches cost.go).
func dispatchLogPath() string {
	dir := os.Getenv("YAKOS_DISPATCH_LOG")
	if dir == "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/tmp"
		}
		dir = filepath.Join(home, ".yakos-state")
	}
	return filepath.Join(dir, dispatchLogName)
}

// appendEvent appends a single JSON line to the dispatch-log using O_APPEND +
// flock for cross-process safety (matching the bash flock usage in dispatch.sh).
// Errors are non-fatal: if the log can't be written, dispatch still proceeds.
func appendEvent(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("events: mkdir %s: %w", filepath.Dir(path), err)
	}

	// Open with O_APPEND for atomic multi-process appends.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("events: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// flock for cross-process append safety (mirrors bash flock usage).
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		// Best-effort: proceed without lock on filesystems that don't support flock.
		_ = err
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()

	line = append(line, '\n')
	_, err = f.Write(line)
	return err
}

// writeStarted writes a dispatch_started event to the dispatch-log.
// Schema matches PR #40: includes the project field.
func writeStarted(req Request, ts time.Time, logPath string) {
	// Use the canonical Event struct from internal/cost to ensure schema parity.
	ev := cost.Event{
		Type:        "dispatch_started",
		Ts:          ts.UTC().Format(time.RFC3339),
		Agent:       req.AgentName,
		Runtime:     req.Runtime,
		Project:     req.Project,
		TaskPreview: truncate(req.Task, 200),
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return // not fatal
	}
	_ = appendEvent(logPath, line)
}

// Result is the outcome of a dispatch Run, used to build the dispatch_finished event.
type Result struct {
	ExitCode      int
	DurationS     float64
	OutputBytes   int64
	TaskBytes     int64
	StderrTail    string // empty → null in JSON
	StderrTrunc   bool
	Usage         *cost.Usage
	ModelChosenBy string
	ModelResolved string
	EvalRunID     string
}

// finishedEvent is the full dispatch_finished schema (PR #40 + #31 + #34 + #32).
// Named fields are serialized exactly — downstream tools (cost, supervise,
// model-routing, finops-review) parse this; no drift allowed.
type finishedEvent struct {
	Type            string      `json:"type"`
	Ts              string      `json:"ts"`
	Agent           string      `json:"agent"`
	Runtime         string      `json:"runtime"`
	Project         string      `json:"project"`           // PR #40
	ExitCode        int         `json:"exit_code"`
	DurationS       float64     `json:"duration_s"`
	OutputBytes     int64       `json:"output_bytes"`
	TaskBytes       int64       `json:"task_bytes"`
	EstInputTokens  int64       `json:"est_input_tokens"`
	EstOutputTokens int64       `json:"est_output_tokens"`
	ModelChosenBy   string      `json:"model_chosen_by"`   // PR #32
	ModelResolved   string      `json:"model_resolved"`    // PR #32
	EvalRunID       interface{} `json:"eval_run_id"`       // string | null
	StderrTail      interface{} `json:"stderr_tail"`       // string | null — PR #34
	StderrTruncated bool        `json:"stderr_truncated"`  // PR #34
	Usage           *cost.Usage `json:"usage,omitempty"`   // PR #31
}

// writeFinished writes a dispatch_finished event to the dispatch-log.
func writeFinished(req Request, res Result, ts time.Time, logPath string) {
	ev := finishedEvent{
		Type:            "dispatch_finished",
		Ts:              ts.UTC().Format(time.RFC3339),
		Agent:           req.AgentName,
		Runtime:         req.Runtime,
		Project:         req.Project,
		ExitCode:        res.ExitCode,
		DurationS:       res.DurationS,
		OutputBytes:     res.OutputBytes,
		TaskBytes:       res.TaskBytes,
		EstInputTokens:  res.TaskBytes / 4,
		EstOutputTokens: res.OutputBytes / 4,
		ModelChosenBy:   res.ModelChosenBy,
		ModelResolved:   res.ModelResolved,
		StderrTruncated: res.StderrTrunc,
		Usage:           res.Usage,
	}

	// eval_run_id: null when empty, string when set.
	if res.EvalRunID != "" {
		ev.EvalRunID = res.EvalRunID
	} else {
		ev.EvalRunID = nil
	}

	// stderr_tail: null when empty (success case), string when set.
	if res.StderrTail != "" {
		ev.StderrTail = res.StderrTail
	} else {
		ev.StderrTail = nil
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return // not fatal
	}
	_ = appendEvent(logPath, line)
}

// WriteBudgetViolation writes a budget_violation event when a dispatch exceeded
// the agent's max-cost-per-task. Phase 1 logs the violation but does not abort
// (the call already happened). Mirrors dispatch.sh:violation_event.
// Exported so the CLI layer (main.go) can call it after inspecting Usage.TotalCostUSD.
func WriteBudgetViolation(req Request, actualCost, maxCost float64, logPath string) {
	type budgetViolationEvent struct {
		Type          string  `json:"type"`
		Ts            string  `json:"ts"`
		Agent         string  `json:"agent"`
		Runtime       string  `json:"runtime"`
		ActualCostUSD float64 `json:"actual_cost_usd"`
		MaxCostUSD    float64 `json:"max_cost_usd"`
	}
	ev := budgetViolationEvent{
		Type:          "budget_violation",
		Ts:            time.Now().UTC().Format(time.RFC3339),
		Agent:         req.AgentName,
		Runtime:       req.Runtime,
		ActualCostUSD: actualCost,
		MaxCostUSD:    maxCost,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = appendEvent(logPath, line)
}

// truncate returns s truncated to n bytes (byte-level, not rune-level).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
