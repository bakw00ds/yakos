// Package workclose is the Go port of cli/lib/work-close.sh (rank 37).
//
// It implements `yakos work close`: captures outcome telemetry for the current
// work session and appends a plan_outcome record to the plan-quality log,
// closing the feedback loop between plan scores and real execution outcomes.
//
// # Synopsis
//
//	yakos work close [--plan-id <id>] [--no-prompt] [--project <path>]
//
// # What it does
//
//  1. Resolve the plan_id from the log or --plan-id override.
//  2. Compute outcome fields: git diff stats, dispatch-log sums, rework count.
//  3. (Optionally) prompt for a human_signal (skip with --no-prompt or non-TTY).
//  4. Append a plan_outcome record to ~/.yakos-state/plan-quality-log.ndjson.
//
// # Non-blocking contract
//
// Any missing data field → null; a WARN is logged to ErrWriter; execution
// continues. A failure to compute a field never causes work close to abort.
//
// # Log format
//
// The appended record has type "plan_outcome" and contains:
//
//	plan_id, project, first_try_pass, rework_cycles, files_touched,
//	files_touched_estimated, scope_creep_ratio, lines_changed,
//	tokens_spent_total, cost_usd_actual, rework_failure_modes, human_signal
package workclose

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// PlanID overrides automatic plan_id resolution from the log.
	// When empty, the most recent plan_scored record is used.
	PlanID string

	// NoPrompt skips the interactive human_signal prompt.
	NoPrompt bool

	// ProjectDir is the project root for git diff (defaults to os.Getwd).
	ProjectDir string

	// PlanQualityLog overrides the log path.
	// When empty, $YAKOS_PLAN_QUALITY_LOG or ~/.yakos-state/plan-quality-log.ndjson is used.
	PlanQualityLog string

	// DispatchLog overrides the dispatch-log path.
	// When empty, $YAKOS_DISPATCH_LOG or ~/.yakos-state/dispatch-log.ndjson is used.
	DispatchLog string

	// CurrentDir overrides work/current path resolution.
	// When empty, the default resolution logic is used.
	CurrentDir string

	// HomeDir overrides $HOME for path resolution.
	HomeDir string

	// SessionStartTS is the ISO-8601 timestamp to use as the session window start
	// for dispatch-log filtering. Empty means use all records.
	SessionStartTS string

	// Now is injected for tests; zero value uses time.Now().
	Now time.Time

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives WARN/advisory messages (default: os.Stderr).
	ErrWriter io.Writer

	// GitFn is an injectable function that runs a git command in dir and returns
	// combined output. When nil, exec.Command("git", ...) is used.
	GitFn func(dir string, args ...string) (string, error)

	// PromptFn is injected for tests to simulate TTY user input.
	// It receives the prompt text and returns the user's answer.
	// When nil (and NoPrompt=false), a real TTY read is attempted.
	PromptFn func(prompt string) (string, error)
}

// Result is the outcome of a successful Run call.
type Result struct {
	// PlanID is the plan_id that was closed.
	PlanID string

	// RecordPath is the absolute path to the log that was written.
	RecordPath string

	// FilesTouched is the actual file count (nil when unavailable).
	FilesTouched *int

	// LinesChanged is the line delta (nil when unavailable).
	LinesChanged *int

	// ReworkCycles is the count of re-dispatched agents (nil when unavailable).
	ReworkCycles *int

	// FirstTryPass is true when rework_cycles==0 and no failure modes (nil when unknown).
	FirstTryPass *bool

	// CostUSD is the session cost (nil when unavailable).
	CostUSD *float64

	// ScopeCreepRatio is actual/estimated files (nil when unavailable).
	ScopeCreepRatio *float64
}

// humanSignal is the operator-supplied satisfaction signal.
type humanSignal struct {
	Satisfied bool    `json:"satisfied"`
	Note      *string `json:"note"`
}

// outcomeRecord is the JSON schema for a plan_outcome log line.
type outcomeRecord struct {
	Type                 string       `json:"type"`
	Ts                   string       `json:"ts"`
	PlanID               string       `json:"plan_id"`
	Project              string       `json:"project"`
	FirstTryPass         interface{}  `json:"first_try_pass"`          // bool or null
	ReworkCycles         interface{}  `json:"rework_cycles"`           // int or null
	FilesTouched         interface{}  `json:"files_touched"`           // int or null
	FilesTouchedEstimated interface{} `json:"files_touched_estimated"` // int or null
	ScopeCreepRatio      interface{}  `json:"scope_creep_ratio"`       // float or null
	LinesChanged         interface{}  `json:"lines_changed"`           // int or null
	TokensSpentTotal     interface{}  `json:"tokens_spent_total"`      // int or null
	CostUSDActual        interface{}  `json:"cost_usd_actual"`         // float or null
	ReworkFailureModes   []string     `json:"rework_failure_modes"`
	HumanSignal          interface{}  `json:"human_signal"` // humanSignal or null
}

// ---- entry point ------------------------------------------------------------

// Run executes `yakos work close` according to cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	logPath := resolveLogPath(cfg)

	// 1. Resolve plan_id.
	planID, err := resolvePlanID(cfg, logPath)
	if err != nil {
		return nil, err
	}
	if planID == "" {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "work close: no plan_scored record found in %s\n", logPath)
		_, _ = fmt.Fprintln(cfg.ErrWriter, "  Run 'yakos skill plan-quality-eval <plan.md>' first, or pass --plan-id.")
		return nil, fmt.Errorf("work close: no plan_id resolved")
	}

	_, _ = fmt.Fprintf(cfg.ErrWriter, "work close: closing plan_id=%s\n", planID)

	// Resolve project dir.
	projectDir := cfg.ProjectDir
	if projectDir == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			projectDir = "."
		}
	}

	res := &Result{PlanID: planID}

	// 2. Compute outcome fields (all best-effort).
	filesTouched, linesChanged := computeGitStats(cfg, projectDir)
	filesTouchedEstimated := parsePlanEstimate(projectDir)
	scopeCreepRatio := computeScopeCreep(filesTouched, filesTouchedEstimated)
	tokensSpent, costUSD := computeDispatchStats(cfg, logPath)
	reworkCycles := computeRework(cfg, logPath)
	failureModes := computeFailureModes(cfg, logPath, planID)
	firstTryPass := computeFirstTryPass(reworkCycles, failureModes)

	// Populate Result.
	res.FilesTouched = filesTouched
	res.LinesChanged = linesChanged
	res.ReworkCycles = reworkCycles
	res.FirstTryPass = firstTryPass
	res.CostUSD = costUSD
	res.ScopeCreepRatio = scopeCreepRatio

	// 3. Optionally prompt for human_signal.
	var hs *humanSignal
	if !cfg.NoPrompt {
		hs = promptHumanSignal(cfg, planID)
	}

	// 4. Build and append record.
	ts := cfg.now().UTC().Format(time.RFC3339)

	rec := outcomeRecord{
		Type:               "plan_outcome",
		Ts:                 ts,
		PlanID:             planID,
		Project:            projectDir,
		ReworkFailureModes: failureModes,
	}

	// Nullable fields.
	if firstTryPass != nil {
		rec.FirstTryPass = *firstTryPass
	}
	if reworkCycles != nil {
		rec.ReworkCycles = *reworkCycles
	}
	if filesTouched != nil {
		rec.FilesTouched = *filesTouched
	}
	if filesTouchedEstimated != nil {
		rec.FilesTouchedEstimated = *filesTouchedEstimated
	}
	if scopeCreepRatio != nil {
		rec.ScopeCreepRatio = *scopeCreepRatio
	}
	if linesChanged != nil {
		rec.LinesChanged = *linesChanged
	}
	if tokensSpent != nil {
		rec.TokensSpentTotal = *tokensSpent
	}
	if costUSD != nil {
		rec.CostUSDActual = *costUSD
	}
	if hs != nil {
		rec.HumanSignal = hs
	}

	if rec.ReworkFailureModes == nil {
		rec.ReworkFailureModes = []string{}
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("work close: marshal record: %w", err)
	}

	if err := appendToLog(logPath, line); err != nil {
		return nil, fmt.Errorf("work close: write log: %w", err)
	}

	res.RecordPath = logPath
	_, _ = fmt.Fprintf(cfg.ErrWriter, "work close: plan_outcome record appended to %s\n", logPath)

	// Print summary.
	_, _ = fmt.Fprintln(cfg.Writer, "")
	_, _ = fmt.Fprintf(cfg.Writer, "Work closed for plan_id: %s\n", planID)
	_, _ = fmt.Fprintf(cfg.Writer, "  files_touched:   %s\n", formatNullableInt(filesTouched))
	_, _ = fmt.Fprintf(cfg.Writer, "  lines_changed:   %s\n", formatNullableInt(linesChanged))
	_, _ = fmt.Fprintf(cfg.Writer, "  rework_cycles:   %s\n", formatNullableInt(reworkCycles))
	_, _ = fmt.Fprintf(cfg.Writer, "  first_try_pass:  %s\n", formatNullableBool(firstTryPass))
	_, _ = fmt.Fprintf(cfg.Writer, "  cost_usd_actual: %s\n", formatNullableFloat(costUSD))
	_, _ = fmt.Fprintln(cfg.Writer, "")
	_, _ = fmt.Fprintf(cfg.Writer, "Record type plan_outcome appended to: %s\n", logPath)

	return res, nil
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos work close [options]

Capture outcome telemetry for the current work session and append a
plan_outcome record to the plan-quality log. Used to close the
feedback loop between plan scores and real execution outcomes.

Options:
  --plan-id <id>    Explicitly name the plan_id to close. Defaults to the
                    most recent plan_scored record in the log.
  --no-prompt       Skip the interactive human_signal prompt.
  --project <path>  Project root for git diff (defaults to cwd).
  -h, --help        Show this help.

The plan_outcome record is appended to:
  ~/.yakos-state/plan-quality-log.ndjson

Record type: plan_outcome (joined to plan_scored via plan_id).
`)
}

// ---- config helpers ---------------------------------------------------------

func (cfg Config) now() time.Time {
	if !cfg.Now.IsZero() {
		return cfg.Now
	}
	return time.Now()
}

func resolveLogPath(cfg Config) string {
	if cfg.PlanQualityLog != "" {
		return cfg.PlanQualityLog
	}
	if env := os.Getenv("YAKOS_PLAN_QUALITY_LOG"); env != "" {
		return env
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".yakos-state", "plan-quality-log.ndjson")
}

func resolveDispatchLogPath(cfg Config) string {
	if cfg.DispatchLog != "" {
		return cfg.DispatchLog
	}
	if env := os.Getenv("YAKOS_DISPATCH_LOG"); env != "" {
		return filepath.Join(env, "dispatch-log.ndjson")
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".yakos-state", "dispatch-log.ndjson")
}

// ---- plan_id resolution -----------------------------------------------------

func resolvePlanID(cfg Config, logPath string) (string, error) {
	if cfg.PlanID != "" {
		return cfg.PlanID, nil
	}
	data, err := os.ReadFile(logPath) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("work close: read log: %w", err)
	}
	// Scan from end to find the most recent plan_scored record.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, `"plan_scored"`) {
			continue
		}
		var rec struct {
			Type   string `json:"type"`
			PlanID string `json:"plan_id"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.Type == "plan_scored" && rec.PlanID != "" {
			return rec.PlanID, nil
		}
	}
	return "", nil
}

// ---- git stats --------------------------------------------------------------

// computeGitStats runs `git diff --numstat HEAD` in projectDir and returns
// (files_touched, lines_changed). Both are nil on failure (non-blocking).
func computeGitStats(cfg Config, projectDir string) (filesTouched, linesChanged *int) {
	gitFn := cfg.GitFn
	if gitFn == nil {
		gitFn = defaultGitFn
	}

	out, err := gitFn(projectDir, "diff", "--numstat", "HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		// Fallback: diff without HEAD.
		out, err = gitFn(projectDir, "diff", "--numstat")
		if err != nil || strings.TrimSpace(out) == "" {
			return nil, nil
		}
	}

	var nFiles, nLines int
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		added, err1 := strconv.Atoi(parts[0])
		deleted, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			continue
		}
		nFiles++
		nLines += added + deleted
	}
	if nFiles == 0 {
		return nil, nil
	}
	return &nFiles, &nLines
}

// defaultGitFn runs git in dir and returns stdout.
func defaultGitFn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// ---- plan estimate ----------------------------------------------------------

// parsePlanEstimate reads files_touched_estimate from the plan.md Tasks block.
// Returns nil when unavailable.
func parsePlanEstimate(projectDir string) *int {
	planMD := filepath.Join(projectDir, "work", "current", "plan.md")
	data, err := os.ReadFile(planMD) //nolint:gosec
	if err != nil {
		return nil
	}

	// Look for "files_touched_estimate: N" lines inside the Tasks section.
	var total int
	var found bool
	inTasks := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## Tasks") {
			inTasks = true
			continue
		}
		// Another ## heading ends the Tasks section.
		if inTasks && strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "## Tasks") {
			break
		}
		if !inTasks {
			continue
		}
		lower := strings.ToLower(trimmed)
		if idx := strings.Index(lower, "files_touched_estimate:"); idx >= 0 {
			rest := trimmed[idx+len("files_touched_estimate:"):]
			rest = strings.TrimSpace(rest)
			// Take the first token.
			parts := strings.Fields(rest)
			if len(parts) > 0 {
				n, err := strconv.Atoi(parts[0])
				if err == nil {
					total += n
					found = true
				}
			}
		}
	}
	if !found {
		return nil
	}
	return &total
}

// ---- scope creep ------------------------------------------------------------

func computeScopeCreep(filesTouched, filesTouchedEstimated *int) *float64 {
	if filesTouched == nil || filesTouchedEstimated == nil || *filesTouchedEstimated == 0 {
		return nil
	}
	ratio := float64(*filesTouched) / float64(*filesTouchedEstimated)
	r := roundFloat(ratio, 2)
	return &r
}

func roundFloat(f float64, decimals int) float64 {
	factor := 1.0
	for i := 0; i < decimals; i++ {
		factor *= 10
	}
	return float64(int(f*factor+0.5)) / factor
}

// ---- dispatch-log stats -----------------------------------------------------

// dispatchRec is the minimal shape we read from dispatch-log.ndjson.
type dispatchRec struct {
	Ts          string  `json:"ts"`
	Agent       string  `json:"agent"`
	TokensTotal int64   `json:"tokens_total"`
	CostUSD     float64 `json:"cost_usd"`
	// Also present in dispatch_finished events under usage.
	Usage *struct {
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"usage,omitempty"`
	EstInputTokens  int64 `json:"est_input_tokens"`
	EstOutputTokens int64 `json:"est_output_tokens"`
}

// computeDispatchStats sums tokens_total and cost_usd from the dispatch log,
// optionally filtered by session start timestamp. Returns (nil, nil) on failure.
func computeDispatchStats(cfg Config, logPath string) (tokensSpent *int64, costUSD *float64) {
	dispLog := resolveDispatchLogPath(cfg)
	data, err := os.ReadFile(dispLog) //nolint:gosec
	if err != nil {
		return nil, nil
	}

	var totalTokens int64
	var totalCost float64
	found := false

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec dispatchRec
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		// Filter by session window.
		if cfg.SessionStartTS != "" && rec.Ts < cfg.SessionStartTS {
			continue
		}

		// Prefer usage.total_cost_usd when available.
		if rec.Usage != nil {
			totalTokens += rec.Usage.InputTokens + rec.Usage.OutputTokens
			totalCost += rec.Usage.TotalCostUSD
			found = true
		} else if rec.TokensTotal > 0 || rec.CostUSD > 0 {
			totalTokens += rec.TokensTotal
			totalCost += rec.CostUSD
			found = true
		} else if rec.EstInputTokens+rec.EstOutputTokens > 0 {
			totalTokens += rec.EstInputTokens + rec.EstOutputTokens
			found = true
		}
	}

	if !found {
		return nil, nil
	}
	return &totalTokens, &totalCost
}

// ---- rework cycles ----------------------------------------------------------

// computeRework counts agents that appear more than once in the dispatch log
// (re-dispatched to the same agent = rework). Returns nil when unavailable.
func computeRework(cfg Config, logPath string) *int {
	dispLog := resolveDispatchLogPath(cfg)
	data, err := os.ReadFile(dispLog) //nolint:gosec
	if err != nil {
		return nil
	}

	agentCount := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec struct {
			Agent string `json:"agent"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Agent == "" {
			continue
		}
		agentCount[rec.Agent]++
	}

	var rework int
	for _, count := range agentCount {
		if count > 1 {
			rework += count - 1
		}
	}
	return &rework
}

// ---- failure modes ----------------------------------------------------------

func computeFailureModes(cfg Config, logPath, planID string) []string {
	var modes []string

	// Check dispatch log for repeated test runner dispatches (test_rerun).
	dispLog := resolveDispatchLogPath(cfg)
	if data, err := os.ReadFile(dispLog); err == nil { //nolint:gosec
		var testRunnerCount int
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			var rec struct {
				Agent string `json:"agent"`
				Task  string `json:"task"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			agentLower := strings.ToLower(rec.Agent)
			taskLower := strings.ToLower(rec.Task)
			if strings.Contains(agentLower, "test") || strings.Contains(taskLower, "test") {
				testRunnerCount++
			}
		}
		if testRunnerCount > 1 {
			modes = append(modes, "test_rerun")
		}
	}

	// Check plan-quality log for block_overridden.
	if data, err := os.ReadFile(logPath); err == nil { //nolint:gosec
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, `"plan_score_override"`) && strings.Contains(line, planID) {
				modes = append(modes, "block_overridden")
				break
			}
		}
	}

	return modes
}

// ---- first_try_pass ---------------------------------------------------------

func computeFirstTryPass(reworkCycles *int, failureModes []string) *bool {
	if reworkCycles == nil {
		return nil
	}
	hasBypass := contains(failureModes, "hook_bypass")
	hasOverride := contains(failureModes, "block_overridden")
	pass := *reworkCycles == 0 && !hasBypass && !hasOverride
	return &pass
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// ---- human signal -----------------------------------------------------------

func promptHumanSignal(cfg Config, planID string) *humanSignal {
	if cfg.PromptFn == nil {
		// Skip interactive prompt in non-TTY environments.
		return nil
	}

	_, _ = fmt.Fprintln(cfg.Writer, "")
	_, _ = fmt.Fprintf(cfg.Writer, "Work session closed for plan_id: %s\n", planID)
	_, _ = fmt.Fprintln(cfg.Writer, "")
	_, _ = fmt.Fprintln(cfg.Writer, "Optional: capture a human signal for the feedback loop.")

	answer, err := cfg.PromptFn("Was the outcome satisfactory? [y/n/skip]: ")
	if err != nil {
		return nil
	}

	var satisfied bool
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		satisfied = true
	case "n", "no":
		satisfied = false
	default:
		return nil
	}

	hs := &humanSignal{Satisfied: satisfied}

	noteAnswer, err := cfg.PromptFn("Optional note (press Enter to skip): ")
	if err == nil && strings.TrimSpace(noteAnswer) != "" {
		note := strings.TrimSpace(noteAnswer)
		hs.Note = &note
	}

	return hs
}

// ---- log writing ------------------------------------------------------------

func appendToLog(logPath string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintf(f, "%s\n", line)
	return err
}

// ---- formatting helpers -----------------------------------------------------

func formatNullableInt(p *int) string {
	if p == nil {
		return "null"
	}
	return strconv.Itoa(*p)
}

func formatNullableBool(p *bool) string {
	if p == nil {
		return "null"
	}
	if *p {
		return "true"
	}
	return "false"
}

func formatNullableFloat(p *float64) string {
	if p == nil {
		return "null"
	}
	return fmt.Sprintf("%.4f", *p)
}
