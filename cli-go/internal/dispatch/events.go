package dispatch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// stateDirName is the base name of the yakOS-owned state directory
// (statepath.Dir's default). Kept in sync by TestStateDirName_MatchesStatepath.
const stateDirName = ".yakos-state"

// dispatchLogPath returns the path to the active dispatch-log file.
// Delegates to statepath.DispatchLog() — the single canonical resolver
// shared with perfdash and metricsdash so reader and writer always agree.
func dispatchLogPath() string {
	return statepath.DispatchLog()
}

// appendEvent appends a single JSON line to the dispatch-log using O_APPEND +
// flock for cross-process safety (matching the bash flock usage in dispatch.sh).
// Errors are non-fatal: if the log can't be written, dispatch still proceeds.
func appendEvent(path string, line []byte) error {
	// SECURITY (M5, security-review-2026-09-14.md): the dispatch-log holds
	// TaskPreview (the first 200 bytes of every dispatched task), operator
	// IDs, and conversation/session IDs. 0755/0644 let any local user read
	// it; 0700/0600 restrict it to the owner, matching every other
	// yakOS-written credential/state file (console token, REST tokens,
	// setup token — all 0600/0700).
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil { //nolint:gosec
		return fmt.Errorf("events: mkdir %s: %w", dir, err)
	}
	// S-2 R12 / N5: MkdirAll and O_CREATE apply their modes only when they
	// create the path, so an existing 0755 directory (bash-created install)
	// or an attacker-pre-created file keeps its mode. Verify and tighten the
	// yakOS-owned state directory; an operator-chosen override directory
	// (YAKOS_DISPATCH_LOG) is left alone, since it is not ours to chmod.
	if filepath.Base(dir) == stateDirName {
		if err := statepath.SecureDir(dir); err != nil {
			return fmt.Errorf("events: %w", err)
		}
	}

	// Open with O_APPEND for atomic multi-process appends. noFollowFlag
	// (round-2 review R4) refuses to follow a symlink planted at path —
	// see openflags_unix.go.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|noFollowFlag, 0600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("events: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Tighten via the open descriptor (fchmod, no TOCTOU) and refuse a file
	// owned by another user — see statepath.SecureFile.
	if err := statepath.SecureFile(f); err != nil {
		return fmt.Errorf("events: %w", err)
	}

	// flock for cross-process append safety (mirrors bash flock usage).
	// Per-platform impl in lock_unix.go / lock_windows.go.
	lockFile(f)
	defer unlockFile(f)

	line = append(line, '\n')
	_, err = f.Write(line)
	return err
}

// writeStarted writes a dispatch_started event to the dispatch-log.
// Schema matches PR #40: includes the project field.
// Identity fields (operator_id, conversation_id, session_id) are emitted
// when non-empty; they are omitted on legacy dispatches so legacy readers
// continue to parse the line without error.
func writeStarted(req Request, ts time.Time, logPath string) {
	// Use the canonical Event struct from internal/cost to ensure schema parity.
	ev := cost.Event{
		Type:           "dispatch_started",
		Ts:             ts.UTC().Format(time.RFC3339),
		Agent:          req.AgentName,
		Runtime:        req.Runtime,
		Project:        req.Project,
		TaskPreview:    truncate(req.Task, 200),
		Model:          req.ModelResolved,
		OperatorID:     req.OperatorID,
		ConversationID: req.ConversationID,
		SessionID:      req.SessionID,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return // not fatal
	}
	_ = appendEvent(logPath, line)
}

// Result is the outcome of a dispatch Run, used to build the dispatch_finished event.
//
// The fields from Runtime down are the typed output of the run (K-135): what
// the agent said, what it cost in tokens, and the handle to resume it. They are
// filled by Run and by the streaming path from the runtime's own stdout format
// (claude stream-json, codex JSONL, agy stream-json, or plain text) so every
// transport receives text instead of raw NDJSON. They are NOT written to the
// dispatch-log by writeFinished; the log keeps its schema.
type Result struct {
	ExitCode    int
	DurationS   float64
	OutputBytes int64
	TaskBytes   int64
	StderrTail  string // empty → null in JSON
	StderrTrunc bool
	// Usage is the token usage of this run (input, output, cache read, cache
	// creation, and the dollar cost for the one harness that reports it). Nil when
	// the run reported none. Counts follow runtime.Usage's convention across every
	// harness. It is what gets logged and summed, so adding runs up counts every
	// token once. For agy, whose result frame totals the whole conversation, the
	// parser decides from the frame's own turn count: the frame's counts on a
	// first turn, the sum of the run's DONE steps after it, with DurationMs zero
	// (see runtime.ParseResult.Usage). DurationS is the measured duration.
	Usage *cost.Usage

	// CumulativeUsage is the running total of the whole native conversation up to
	// and including this run, for the harness that reports one (agy), else nil.
	// It is for reference and cross-checking. Do not add it up across runs: it
	// counts the earlier turns again. Not written to the dispatch records.
	CumulativeUsage *cost.Usage

	ModelChosenBy string
	ModelResolved string
	EvalRunID     string

	// Runtime is the runtime that ran the dispatch ("claude", "codex", "agy").
	Runtime string

	// Provider is the model provider behind Runtime (anthropic, openai,
	// google); "" for a runtime with no known provider. Derived from the
	// runtime name for now; the registry will refine it.
	Provider string

	// Text is the agent's answer, parsed out of the runtime's stdout: for claude
	// the result frame's final text (never sub-agent narration), for codex and
	// agy every assistant message, for a stream that is not a recognised JSON
	// format the stdout text itself. Trailing newlines are trimmed. This is what
	// the transports and Flows hand on. See Parsed and runtime.ParseResult.Text.
	Text string

	// TextAll is everything the agent said, sub-agent narration included, the way
	// the bash dispatcher printed it. It contains Text and is for a person at a
	// terminal (yakos dispatch prints it); transports that hand a result to
	// another agent return Text only. Equal to Text for runtimes that do not
	// tell the two apart.
	TextAll string

	// Parsed is true when Text came from the runtime's LineParser. A consumer
	// must then prefer Text over raw stdout even when Text is empty (an agent
	// that answered nothing); a Result built by a fake or a legacy caller leaves
	// it false and the raw stdout stands (see OutputText).
	Parsed bool

	// SessionID is the harness-native session id (claude session_id, codex
	// thread_id, agy conversation_id), usable to resume the conversation. It is
	// NOT Request.SessionID, which is the console UI session. "" when the
	// stream carried none.
	SessionID string

	// ModelID is the concrete model id the stream reported, "" when none.
	ModelID string

	// Truncated is true when Text is incomplete. TextCapped and LinesDropped say
	// why; a streamed turn can also be truncated by the 32 MB input ceiling,
	// which sets neither.
	Truncated bool

	// TextCapped is true when Text reached the parser's 1 MiB cap and the rest
	// was dropped; TextAllCapped says the same of TextAll.
	TextCapped    bool
	TextAllCapped bool

	// LinesDropped counts output lines skipped for exceeding the per-line cap
	// (runtime.MaxStreamLineBytes). A dropped line contributes nothing to Text.
	LinesDropped int

	// Error is the failure message the harness itself reported, "" for a run
	// that did not report one. Diagnostic only; never part of Text.
	Error string
}

// finishedEvent is the full dispatch_finished schema (PR #40 + #31 + #34 + #32 + Phase 2).
// Named fields are serialized exactly — downstream tools (cost, supervise,
// model-routing, finops-review) parse this; no drift allowed.
//
// Identity fields (operator_id, conversation_id, session_id) are
// additive-optional: omitempty means they are absent from legacy/bash-written
// lines. All readers of this schema MUST tolerate their absence.
type finishedEvent struct {
	Type            string      `json:"type"`
	Ts              string      `json:"ts"`
	Agent           string      `json:"agent"`
	Runtime         string      `json:"runtime"`
	Project         string      `json:"project"` // PR #40
	ExitCode        int         `json:"exit_code"`
	DurationS       float64     `json:"duration_s"`
	OutputBytes     int64       `json:"output_bytes"`
	TaskBytes       int64       `json:"task_bytes"`
	EstInputTokens  int64       `json:"est_input_tokens"`
	EstOutputTokens int64       `json:"est_output_tokens"`
	Model           string      `json:"model,omitempty"`  // K-110: same value as model_resolved
	ModelChosenBy   string      `json:"model_chosen_by"`  // PR #32
	ModelResolved   string      `json:"model_resolved"`   // PR #32
	EvalRunID       interface{} `json:"eval_run_id"`      // string | null
	StderrTail      interface{} `json:"stderr_tail"`      // string | null — PR #34
	StderrTruncated bool        `json:"stderr_truncated"` // PR #34
	Usage           *cost.Usage `json:"usage,omitempty"`  // PR #31
	// Phase 2 identity fields — additive-optional; absent on legacy lines.
	OperatorID     string `json:"operator_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
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
		Model:           res.ModelResolved,
		ModelChosenBy:   res.ModelChosenBy,
		ModelResolved:   res.ModelResolved,
		StderrTruncated: res.StderrTrunc,
		Usage:           res.Usage,
		// Identity fields: omitempty so legacy readers see no new keys.
		OperatorID:     req.OperatorID,
		ConversationID: req.ConversationID,
		SessionID:      req.SessionID,
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
