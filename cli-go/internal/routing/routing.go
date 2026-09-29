// Package routing is the Go port of cli/lib/model-routing.sh (rank 36).
//
// It implements the model-routing eval harness: evaluating an agent across
// haiku/sonnet/opus tiers using eval case fixtures, scoring responses with a
// judge agent, computing Wilson 95% CI lower bounds, and emitting promotion
// candidates when a cheaper tier is statistically equivalent.
//
// # Synopsis
//
//	yakos model-routing eval <agent-id> [--judge <agent>] [--max-cost-usd <n>]
//	                                     [--cases <glob>] [--project <path>]
//	yakos model-routing list
//	yakos model-routing show <agent-id>
//	yakos model-routing promote <agent-id> [--global]
//	yakos model-routing reject <agent-id> [--note "<text>"] [--force]
//	yakos model-routing history [<agent-id>]
//
// # State files
//
//	~/.yakos-state/model-routing-eval-log.ndjson       — per-case eval records
//	~/.yakos-state/model-routing-candidates.ndjson     — promotion candidates
//	~/.yakos-state/model-routing-history.ndjson        — promote/reject history
//	~/.yakos-state/model-routing-graveyard.ndjson      — rejected candidates
//	~/.yakos-state/model-routing-backups/<agent>-<ts>.md — pre-promote backups
//
// # Settings
//
// Settings live in ~/.yakos-state/settings.json under the "model_routing" key:
//
//	epsilon_pass_rate        — acceptable pass-rate gap (default 0.05)
//	min_cases_for_eval       — minimum eval cases required (default 5)
//	min_cases_for_confidence — cases for CI-only gate (default 12)
//	max_eval_run_cost_usd    — per-run cost cap (default 5.00)
//	weekly_max_cost_usd      — weekly spend cap (default 50.00)
//
// # Promotion decision algorithm
//
// After running eval cases across all three tiers, the algorithm checks
// cheaper tiers (haiku < sonnet) against the agent's current model:
//
//  1. If n_run >= min_cases_for_confidence: use CI-only gate.
//     Pass iff wilson_lower(cand) >= pass_rate(current) - epsilon.
//
//  2. Otherwise: strict floor gate.
//     Pass iff cost_saving >= 2x AND pass_rate_margin >= 0.10.
//
// # Dispatch injection
//
// DispatchFn and JudgeFn in Config are injectable for tests. In production,
// callers set YakosRoot and the package invokes the real dispatch.sh via shell.
//
// # Wilson 95% CI
//
// The WilsonLower function computes the lower bound of the Wilson score
// confidence interval with z=1.96 (95%). The formula:
//
//	phat   = k/n
//	denom  = 1 + z^2/n
//	center = (phat + z^2/(2n)) / denom
//	margin = z * sqrt(phat*(1-phat)/n + z^2/(4*n*n)) / denom
//	lower  = max(0, center - margin)
package routing

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrDispatchUnavailable is returned by realDispatch/realJudge when
// dispatch.sh cannot be located or invoked at all (as opposed to being
// invoked successfully and exiting non-zero, which is a normal outcome
// per the dispatch contract). Callers use errors.Is to distinguish a
// broken harness from a legitimately failing eval case.
var ErrDispatchUnavailable = errors.New("model-routing: dispatch.sh unavailable")

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: eval, list, show, promote, reject, history.
	Subcommand string

	// AgentID is the target agent for eval/show/promote/reject.
	AgentID string

	// Judge overrides the default judge for eval.
	Judge string

	// MaxCostUSD overrides the per-run cost cap for eval.
	MaxCostUSD float64

	// CasesGlob selects eval case files. Empty means every "case-*.json".
	// A value is a comma-separated list of filename globs (any of * ? [) and
	// case ids (a case_id, or a file name with or without the "case-" prefix
	// and ".json" suffix). Every entry must match at least one case.
	CasesGlob string

	// Tiers lists the model tiers to dispatch. Empty means haiku, sonnet
	// and opus (plus fable when IncludeFable is set).
	Tiers []string

	// IncludeFable adds the fable tier to the default tier list, or to an
	// explicit Tiers list.
	IncludeFable bool

	// Project is the optional project path for agent resolution.
	Project string

	// Note is the reason text for reject.
	Note string

	// Force bypasses the repeat-rejection guard in reject.
	Force bool

	// Global promotes framework-shipped agents when set in promote.
	Global bool

	// FilterAgent filters history by agent ID (optional, for history subcommand).
	FilterAgent string

	// YakosRoot is the yakos framework root (lib/agents/ lives here).
	YakosRoot string

	// StateDir overrides ~/.yakos-state for test injection.
	StateDir string

	// EvalLog overrides the eval log path.
	EvalLog string

	// CandidatesFile overrides the candidates file path.
	CandidatesFile string

	// HistoryFile overrides the history file path.
	HistoryFile string

	// GraveyardFile overrides the graveyard file path.
	GraveyardFile string

	// BackupsDir overrides the backups directory path.
	BackupsDir string

	// SettingsFile overrides settings.json path.
	SettingsFile string

	// HomeDir overrides $HOME for path resolution.
	HomeDir string

	// Now is injected for tests; zero value uses time.Now().
	Now time.Time

	// DispatchFn is injected for tests to replace real dispatch.sh calls.
	// Signature: (agentID, task, tier, runID, project string) -> DispatchResult, error
	DispatchFn func(agentID, task, tier, runID, project string) (DispatchResult, error)

	// JudgeFn is injected for tests to replace real judge dispatch.
	// Signature: (judgeID, inputJSON, project string) -> JudgeResult, error
	JudgeFn func(judgeID, inputJSON, project string) (JudgeResult, error)

	// ValidateFn is injected for tests (promote validation gate).
	// Returns nil for success, non-nil for failure.
	ValidateFn func(target string) error

	// Writer receives normal output.
	Writer io.Writer

	// ErrWriter receives advisory/warning messages.
	ErrWriter io.Writer
}

// DispatchResult holds the output from a single agent dispatch.
type DispatchResult struct {
	Stdout       string
	Cost         float64
	DurationS    float64
	InputTokens  int64
	OutputTokens int64

	// CostUnknown is true when the dispatch produced no cost telemetry
	// (no matching dispatch-log record, or a runtime that reports no
	// total_cost_usd). The per-run cost cap cannot be enforced against an
	// unknown cost, so runEval fails closed when it sees this.
	CostUnknown bool
}

// JudgeResult holds the parsed scoring output from a judge dispatch.
type JudgeResult struct {
	Pass           bool
	CriteriaScores []json.RawMessage
	Notes          string

	// ParseErr is non-empty when the judge output could not be turned into
	// a verdict (no valid JSON object, or the judge dispatch itself failed).
	// Such a case is left unscored, not counted as a failure.
	ParseErr string

	// Raw is the judge's raw output, kept when ParseErr is set.
	Raw string
}

// Unscored reports whether the judge produced no usable verdict.
func (j JudgeResult) Unscored() bool { return j.ParseErr != "" }

// Result summarises what Run did.
type Result struct {
	// Subcommand echoes cfg.Subcommand.
	Subcommand string

	// EvalRunID is the run ID generated for eval.
	EvalRunID string

	// CandidateEmitted is true when eval produced a promotion candidate.
	CandidateEmitted bool

	// CandidateTier is the suggested cheaper tier (empty if no candidate).
	CandidateTier string

	// CandidateReason is the human-readable reason for the candidate.
	CandidateReason string

	// ListCount is the number of candidates shown in list.
	ListCount int

	// PromoteFrom / PromoteTo are the model transitions for promote.
	PromoteFrom string
	PromoteTo   string

	// RejectedAgent / RejectedSuggestedModel for reject.
	RejectedAgent          string
	RejectedSuggestedModel string

	// HistoryCount is the number of records shown in history.
	HistoryCount int

	// TierCILower is the Wilson 95% lower bound per tier that was run (eval).
	TierCILower map[string]float64

	// Gate is the promotion-gate evaluation for eval.
	Gate Gate
}

// GateRow is the gate verdict for one candidate tier.
type GateRow struct {
	Tier string `json:"tier"`
	// NScored is the number of scored cases for the tier.
	NScored int `json:"n_scored"`
	// CILower is the Wilson 95% lower bound of the tier's pass rate.
	CILower float64 `json:"ci_lower"`
	// Mode is "ci" (n >= min_cases_for_confidence) or "strict_floor".
	Mode string `json:"mode"`
	// Threshold is the bar CILower must clear in "ci" mode
	// (baseline pass rate minus epsilon); 0 in strict_floor mode.
	Threshold float64 `json:"threshold"`
	Pass      bool    `json:"pass"`
	Detail    string  `json:"detail"`
}

// Gate summarises the promotion-gate evaluation for a run.
type Gate struct {
	Baseline     string    `json:"baseline"`
	BaselineN    int       `json:"baseline_n"`
	BaselineRate float64   `json:"baseline_pass_rate"`
	MinCasesConf int       `json:"min_cases_for_confidence"`
	Epsilon      float64   `json:"epsilon"`
	Decision     string    `json:"decision"` // "candidate", "refused" or "partial"
	Reason       string    `json:"reason"`
	Rows         []GateRow `json:"tiers"`
}

// ---- per-tier accumulator ---------------------------------------------------

type tierStats struct {
	pass     int
	total    int
	unscored int
	cost     float64
}

func (s *tierStats) rate() float64 {
	if s.total == 0 {
		return 0
	}
	return float64(s.pass) / float64(s.total)
}

func (s *tierStats) meanCost() float64 {
	if s.total == 0 {
		return 0
	}
	return s.cost / float64(s.total)
}

// ---- Run is the package entrypoint ------------------------------------------

// Run executes the model-routing subcommand described by cfg.
func Run(cfg Config) (Result, error) {
	// Apply defaults.
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.HomeDir == "" {
		cfg.HomeDir = os.Getenv("HOME")
		if cfg.HomeDir == "" {
			cfg.HomeDir = "/tmp"
		}
	}
	if cfg.StateDir == "" {
		if v := os.Getenv("YAKOS_MR_STATE_DIR"); v != "" {
			cfg.StateDir = v
		} else {
			cfg.StateDir = filepath.Join(cfg.HomeDir, ".yakos-state")
		}
	}
	if cfg.EvalLog == "" {
		if v := os.Getenv("YAKOS_MR_EVAL_LOG"); v != "" {
			cfg.EvalLog = v
		} else {
			cfg.EvalLog = filepath.Join(cfg.StateDir, "model-routing-eval-log.ndjson")
		}
	}
	if cfg.CandidatesFile == "" {
		if v := os.Getenv("YAKOS_MR_CANDIDATES"); v != "" {
			cfg.CandidatesFile = v
		} else {
			cfg.CandidatesFile = filepath.Join(cfg.StateDir, "model-routing-candidates.ndjson")
		}
	}
	if cfg.HistoryFile == "" {
		if v := os.Getenv("YAKOS_MR_HISTORY"); v != "" {
			cfg.HistoryFile = v
		} else {
			cfg.HistoryFile = filepath.Join(cfg.StateDir, "model-routing-history.ndjson")
		}
	}
	if cfg.GraveyardFile == "" {
		if v := os.Getenv("YAKOS_MR_GRAVEYARD"); v != "" {
			cfg.GraveyardFile = v
		} else {
			cfg.GraveyardFile = filepath.Join(cfg.StateDir, "model-routing-graveyard.ndjson")
		}
	}
	if cfg.BackupsDir == "" {
		if v := os.Getenv("YAKOS_MR_BACKUPS_DIR"); v != "" {
			cfg.BackupsDir = v
		} else {
			cfg.BackupsDir = filepath.Join(cfg.StateDir, "model-routing-backups")
		}
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now().UTC()
	}

	switch cfg.Subcommand {
	case "eval":
		return runEval(cfg)
	case "list":
		return runList(cfg)
	case "show":
		return runShow(cfg)
	case "promote":
		return runPromote(cfg)
	case "reject":
		return runReject(cfg)
	case "history":
		return runHistory(cfg)
	default:
		return Result{}, fmt.Errorf("model-routing: unknown subcommand %q", cfg.Subcommand)
	}
}

// PrintHelp writes the usage text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos model-routing <subcommand> [args...]

Subcommands:
  eval <agent-id> [--judge <agent>] [--max-cost-usd <n>]
                  [--tiers <t1,t2,...>] [--include-fable]
                  [--cases <glob|ids>]  [--project <path>]
      Run a model-routing eval for <agent-id>.  Dispatches each eval/
      case at each tier, scores with a judge, computes Wilson 95% CI
      lower bounds, and emits a candidate (or a refused-reason).
      Hard-refuses if judge == subject.

      --tiers <list>     Comma-separated tiers to run (haiku, sonnet,
                         opus, fable).  Default: haiku,sonnet,opus.
                         The agent's current tier must be included or
                         no candidate can be emitted.
      --include-fable    Add the fable tier to the default (or listed) tiers.
      --cases <spec>     Comma-separated filename globs and/or case ids
                         (e.g. "01,02" or "case-0*.json").  Default:
                         every case-*.json.  Each entry must match.
      --judge <agent>    Default: code-reviewer, or architect for
                         cross-cutting/design agents; never the subject
                         (falls back to another judge and logs it).
      --max-cost-usd <n> Per-run cap.  Cost is read from the dispatch-log;
                         the run stops (partial results, no candidate)
                         if a dispatch reports no cost.

  list
      Show the latest candidate per agent (from
      ~/.yakos-state/model-routing-candidates.ndjson).

  show <agent-id>
      Full evidence for one agent's latest candidate + last 3 run records.

  promote <agent-id> [--global]
      Operator-gated: rewrite the agent's model: frontmatter to the
      suggested tier. Backs up the prior file; validates; records history.
      For framework-shipped agents (lib/agents/), --global is required.
      No --force option; promotion is always deliberate.

  reject <agent-id> [--note "<text>"] [--force]
      Discard the pending candidate.  Records reason in the graveyard.
      Repeat-rejection guard: exits 1 if (agent, model) rejected >= 3
      times already; --force bypasses the guard.

  history [<agent-id>]
      Show promotion + rejection history, sorted by ts descending.
      Optional <agent-id> filters to one agent.

Logs written to:
  ~/.yakos-state/model-routing-eval-log.ndjson
  ~/.yakos-state/model-routing-candidates.ndjson
  ~/.yakos-state/model-routing-history.ndjson
  ~/.yakos-state/model-routing-graveyard.ndjson
  ~/.yakos-state/model-routing-backups/<agent-id>-<ts>.md
`)
}

// ---- settings ---------------------------------------------------------------

type mrSettings struct {
	EpsilonPassRate   float64
	MinCasesForEval   int
	MinCasesForConf   int
	MaxEvalRunCostUSD float64
	WeeklyMaxCostUSD  float64
}

func loadSettings(cfg Config) mrSettings {
	s := mrSettings{
		EpsilonPassRate:   0.05,
		MinCasesForEval:   5,
		MinCasesForConf:   12,
		MaxEvalRunCostUSD: 5.00,
		WeeklyMaxCostUSD:  50.00,
	}

	settingsPath := cfg.SettingsFile
	if settingsPath == "" {
		settingsPath = filepath.Join(cfg.StateDir, "settings.json")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return s
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return s
	}
	mrRaw, ok := root["model_routing"]
	if !ok {
		return s
	}
	var mr map[string]json.RawMessage
	if err := json.Unmarshal(mrRaw, &mr); err != nil {
		return s
	}

	readFloat := func(key string, dst *float64) {
		if raw, ok := mr[key]; ok {
			var v float64
			if json.Unmarshal(raw, &v) == nil {
				*dst = v
			}
		}
	}
	readInt := func(key string, dst *int) {
		if raw, ok := mr[key]; ok {
			var v int
			if json.Unmarshal(raw, &v) == nil {
				*dst = v
			}
		}
	}

	readFloat("epsilon_pass_rate", &s.EpsilonPassRate)
	readInt("min_cases_for_eval", &s.MinCasesForEval)
	readInt("min_cases_for_confidence", &s.MinCasesForConf)
	readFloat("max_eval_run_cost_usd", &s.MaxEvalRunCostUSD)
	readFloat("weekly_max_cost_usd", &s.WeeklyMaxCostUSD)

	return s
}

// ---- Wilson 95% CI lower bound ----------------------------------------------

// WilsonLower computes the lower bound of the Wilson score 95% confidence
// interval. k is the number of successes, n is the total count.
// Returns 0 when n == 0.
//
// This is a pure function exported for testing and parity verification.
func WilsonLower(k, n int) float64 {
	if n == 0 {
		return 0
	}
	phat := float64(k) / float64(n)
	z := 1.96
	denom := 1 + z*z/float64(n)
	center := (phat + z*z/(2*float64(n))) / denom
	variance := phat*(1-phat)/float64(n) + z*z/(4*float64(n)*float64(n))
	if variance < 0 {
		variance = 0
	}
	margin := z * math.Sqrt(variance) / denom
	lower := center - margin
	if lower < 0 {
		lower = 0
	}
	return lower
}

// ---- tier ordering ----------------------------------------------------------

func tierValue(tier string) int {
	switch tier {
	case "haiku":
		return 1
	case "sonnet":
		return 2
	case "opus":
		return 3
	case "fable":
		return 4
	}
	return 0
}

func tierCheaperThan(a, b string) bool {
	va := tierValue(a)
	vb := tierValue(b)
	if va == 0 || vb == 0 {
		return false
	}
	return va < vb
}

// ---- agent file helpers -----------------------------------------------------

// findAgent searches project override then framework lib/agents for <id>.md.
func findAgent(yakosRoot, id, project string) (string, error) {
	var dirs []string
	if project != "" {
		dirs = append(dirs, filepath.Join(project, ".claude", "agents"))
	}
	if yakosRoot != "" {
		dirs = append(dirs, filepath.Join(yakosRoot, "lib", "agents"))
	}

	for _, d := range dirs {
		if stat, err := os.Stat(d); err != nil || !stat.IsDir() {
			continue
		}
		f := filepath.Join(d, id+".md")
		if _, err := os.Stat(f); err == nil {
			return f, nil
		}
	}
	return "", fmt.Errorf("agent %q not found (searched: %v)", id, dirs)
}

// readFrontmatterField reads a specific key from a YAML frontmatter block.
// Returns the trimmed value, or "" if not found.
func readFrontmatterField(path, field string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	inFM := false
	prefix := field + ":"

	for scanner.Scan() {
		line := scanner.Text()
		lineNo++
		if lineNo == 1 {
			if strings.TrimRight(line, " \t") == "---" {
				inFM = true
			}
			continue
		}
		if !inFM {
			break
		}
		if strings.TrimRight(line, " \t") == "---" {
			break
		}
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimPrefix(line, prefix)
			val = strings.TrimSpace(val)
			return val, nil
		}
	}
	return "", scanner.Err()
}

// agentCurrentModel returns the agent's effective model: model-policy (if set)
// → model → "sonnet" default. Mirrors bash:
//
//	current_model="$(_mr_agent_model_policy ...)" || "$(_mr_agent_model ...)" || "sonnet"
func agentCurrentModel(agentFile string) (string, error) {
	policy, err := readFrontmatterField(agentFile, "model-policy")
	if err != nil {
		return "", err
	}
	if policy != "" {
		return policy, nil
	}
	model, err := readFrontmatterField(agentFile, "model")
	if err != nil {
		return "", err
	}
	if model != "" {
		return model, nil
	}
	return "sonnet", nil
}

// agentDomain returns the domain: frontmatter field, or "" if not set.
func agentDomain(agentFile string) string {
	v, _ := readFrontmatterField(agentFile, "domain")
	return v
}

// defaultJudge picks the default judge agent for a given domain.
func defaultJudge(domain string) string {
	switch domain {
	case "backend", "frontend", "mobile", "database", "testing":
		return "code-reviewer"
	case "cross-cutting", "design":
		return "architect"
	default:
		return "code-reviewer"
	}
}

// fallbackJudges is tried in order when the domain default equals the subject.
var fallbackJudges = []string{"code-reviewer", "architect"}

// resolveJudge picks the judge for subject. An explicit override is returned
// as-is (the caller enforces judge != subject). Otherwise the domain default
// is used; when it equals the subject, the first differing fallback wins.
// note describes a fallback so the caller can log the choice; it is empty
// when the plain domain default was used.
func resolveJudge(override, domain, subject string) (judge, note string) {
	if override != "" {
		return override, ""
	}
	def := defaultJudge(domain)
	if def != subject {
		return def, ""
	}
	for _, fb := range fallbackJudges {
		if fb != subject {
			return fb, fmt.Sprintf("default judge %q for domain %q is the subject; fell back to %q", def, domain, fb)
		}
	}
	return def, ""
}

// ---- NDJSON log helpers -----------------------------------------------------

func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = fmt.Fprintln(f, line)
	return err
}

func logWrite(path, line string) {
	// Non-fatal; mirrors bash's || true pattern.
	_ = appendLine(path, line)
}

// isoNow formats the current time as RFC3339Z (matches ct_iso_now_z).
func isoNow(now time.Time) string {
	return now.UTC().Format("2006-01-02T15:04:05Z")
}

// jsonMarshal returns a JSON string or "null" on error. For embedding in
// manually constructed JSON we need the raw string without extra quoting.
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

// buildEvalRunStarted constructs the eval_run_started NDJSON record.
func buildEvalRunStarted(now time.Time, runID, agentID string, nCases int, tiers []string, judge string, epsilon, maxCost float64) string {
	return mustJSON(map[string]interface{}{
		"type":         "eval_run_started",
		"ts":           isoNow(now),
		"run_id":       runID,
		"agent":        agentID,
		"n_cases":      nCases,
		"tiers":        tiers,
		"judge":        judge,
		"epsilon":      epsilon,
		"max_cost_usd": maxCost,
	})
}

// ---- case hash (sha256) -----------------------------------------------------

func caseHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// ---- run ID generator -------------------------------------------------------

func genRunID(now time.Time) string {
	return fmt.Sprintf("yakos-mr-%d-%x", now.Unix(), now.UnixNano()&0xFFFFFFFF)
}

// ---- weekly budget check ----------------------------------------------------

func checkWeeklyBudget(cfg Config, settings mrSettings) error {
	if _, err := os.Stat(cfg.EvalLog); os.IsNotExist(err) {
		return nil
	}

	cutoff := cfg.Now.Add(-7 * 24 * time.Hour).Format("2006-01-02T15:04:05Z")

	f, err := os.Open(cfg.EvalLog)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var spent float64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var rec map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		typeRaw, ok := rec["type"]
		if !ok {
			continue
		}
		var recType string
		if err := json.Unmarshal(typeRaw, &recType); err != nil || recType != "eval_case" {
			continue
		}
		tsRaw, ok := rec["ts"]
		if !ok {
			continue
		}
		var ts string
		if err := json.Unmarshal(tsRaw, &ts); err != nil || ts < cutoff {
			continue
		}
		var cost float64
		if costRaw, ok := rec["total_cost_usd"]; ok {
			_ = json.Unmarshal(costRaw, &cost)
		}
		spent += cost
	}

	if spent >= settings.WeeklyMaxCostUSD {
		return fmt.Errorf("model-routing: weekly eval spend $%.4f >= cap $%.4f; cannot start run", spent, settings.WeeklyMaxCostUSD)
	}
	return nil
}

// ---- eval case struct -------------------------------------------------------

type evalCase struct {
	CaseID           string          `json:"case_id"`
	Task             string          `json:"task"`
	ExpectedOutcomes json.RawMessage `json:"expected_outcomes"`
	Rubric           json.RawMessage `json:"rubric"`
}

func loadEvalCase(path string) (evalCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return evalCase{}, err
	}
	var c evalCase
	if err := json.Unmarshal(data, &c); err != nil {
		return evalCase{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// ---- tier selection ---------------------------------------------------------

// allTiers is the canonical tier order, cheapest to most expensive.
var allTiers = []string{"haiku", "sonnet", "opus", "fable"}

// resolveTiers turns cfg.Tiers/cfg.IncludeFable into the ordered list of
// tiers to run. Unknown tier names are an error so a typo cannot silently
// run a different (possibly more expensive) set.
func resolveTiers(tiers []string, includeFable bool) ([]string, error) {
	want := map[string]bool{}
	if len(tiers) == 0 {
		for _, t := range allTiers[:3] {
			want[t] = true
		}
	}
	for _, t := range tiers {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			return nil, fmt.Errorf("--tiers has an empty entry")
		}
		known := false
		for _, k := range allTiers {
			if k == t {
				known = true
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown tier %q (valid: %s)", t, strings.Join(allTiers, ", "))
		}
		want[t] = true
	}
	if includeFable {
		want["fable"] = true
	}
	var out []string
	for _, t := range allTiers {
		if want[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tiers selected")
	}
	return out, nil
}

// ---- find eval case files ---------------------------------------------------

// selectCaseFiles applies a --cases spec (see Config.CasesGlob) to evalDir.
// An empty spec selects every "case-*.json". Each comma-separated entry must
// match at least one file, so a mistyped id fails loudly rather than
// silently shrinking the run.
func selectCaseFiles(evalDir, spec string) ([]string, error) {
	if strings.TrimSpace(spec) == "" {
		return findCaseFiles(evalDir, "case-*.json")
	}
	entries, err := os.ReadDir(evalDir)
	if err != nil {
		return nil, err
	}
	var all []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			all = append(all, filepath.Join(evalDir, e.Name()))
		}
	}
	sort.Strings(all)

	picked := map[string]bool{}
	for _, tok := range strings.Split(spec, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return nil, fmt.Errorf("--cases has an empty entry")
		}
		matched := 0
		for _, f := range all {
			ok, err := caseMatches(f, tok)
			if err != nil {
				return nil, err
			}
			if ok {
				picked[f] = true
				matched++
			}
		}
		if matched == 0 {
			return nil, fmt.Errorf("--cases entry %q matches no case in %s", tok, evalDir)
		}
	}
	var out []string
	for _, f := range all {
		if picked[f] {
			out = append(out, f)
		}
	}
	return out, nil
}

func caseMatches(path, tok string) (bool, error) {
	base := filepath.Base(path)
	if strings.ContainsAny(tok, "*?[") {
		return filepath.Match(tok, base)
	}
	stem := strings.TrimSuffix(base, ".json")
	if tok == base || tok == stem || tok == strings.TrimPrefix(stem, "case-") {
		return true, nil
	}
	if ec, err := loadEvalCase(path); err == nil && ec.CaseID == tok {
		return true, nil
	}
	return false, nil
}

func findCaseFiles(evalDir, glob string) ([]string, error) {
	if glob == "" {
		glob = "case-*.json"
	}
	entries, err := os.ReadDir(evalDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		matched, err := filepath.Match(glob, e.Name())
		if err != nil {
			return nil, err
		}
		if matched {
			files = append(files, filepath.Join(evalDir, e.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// ---- locate eval directory --------------------------------------------------

func locateEvalDir(agentFile string) (string, error) {
	dir := filepath.Dir(agentFile)
	base := strings.TrimSuffix(filepath.Base(agentFile), ".md")

	// Prefer <agentsDir>/<agentID>/eval/.
	candidate1 := filepath.Join(dir, base, "eval")
	if st, err := os.Stat(candidate1); err == nil && st.IsDir() {
		return candidate1, nil
	}
	// Fallback: <agentsDir>/eval/.
	candidate2 := filepath.Join(dir, "eval")
	if st, err := os.Stat(candidate2); err == nil && st.IsDir() {
		return candidate2, nil
	}
	return "", fmt.Errorf("no eval/ directory found for agent %q", base)
}

// ---- real dispatch shim -----------------------------------------------------

// realDispatch invokes dispatch.sh via shell (production path only).
//
// dispatch.sh lives at <yakosRoot>/cli/lib/dispatch.sh — the same
// lib-root-plus-"cli/lib" resolution used by sibling callers such as
// internal/start/start.go (agents-compose.sh) and internal/team/archive.go
// (archive.sh). yakosRoot itself is expected to already be resolved via
// resolveLibRoot (see cmd/yakos/main.go), so "lib/agents" and "cli/lib"
// both hang directly off it.
func realDispatch(yakosRoot, agentID, task, tier, runID, project string) (DispatchResult, error) {
	yakosLib := filepath.Join(yakosRoot, "cli", "lib")
	dispatchSh := filepath.Join(yakosLib, "dispatch.sh")
	if _, statErr := os.Stat(dispatchSh); statErr != nil {
		return DispatchResult{}, fmt.Errorf("%w: %s: %v", ErrDispatchUnavailable, dispatchSh, statErr)
	}

	args := []string{
		dispatchSh,
		agentID, task,
		"--model", tier,
		"--eval-run-id", runID,
	}
	if project != "" {
		args = append(args, "--project", project)
	}

	logPath := dispatchLogPath()
	offset := fileSize(logPath)

	cmd := exec.Command("bash", args...)
	cmd.Env = append(os.Environ(),
		"YAKOS_ROOT="+yakosRoot,
		"YAKOS_LIB="+yakosLib,
	)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	start := time.Now()
	_ = cmd.Run() // dispatch.sh's own non-zero exit is OK per dispatch contract
	wall := time.Since(start).Seconds()

	dr := DispatchResult{Stdout: outBuf.String()}
	tel, ok := readDispatchTelemetry(logPath, offset, runID, agentID)
	if !ok {
		dr.DurationS = wall
		dr.CostUnknown = true
		return dr, nil
	}
	dr.DurationS = tel.DurationS
	if dr.DurationS == 0 {
		dr.DurationS = wall
	}
	dr.InputTokens = tel.InputTokens
	dr.OutputTokens = tel.OutputTokens
	if tel.Cost == nil {
		dr.CostUnknown = true
	} else {
		dr.Cost = *tel.Cost
	}
	return dr, nil
}

// dispatchLogPath mirrors dispatch.sh, which appends to
// $HOME/.yakos-state/dispatch-log.ndjson.
func dispatchLogPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".yakos-state", "dispatch-log.ndjson")
}

func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// dispatchTelemetry is the slice of a dispatch_finished record eval needs.
type dispatchTelemetry struct {
	DurationS    float64
	Cost         *float64 // nil when the runtime reported no total_cost_usd
	InputTokens  int64
	OutputTokens int64
}

// readDispatchTelemetry finds the last dispatch_finished record for
// (runID, agentID) appended after byte offset. ok is false when there is
// none. A shrunken file (log rotation) is read from the start.
func readDispatchTelemetry(logPath string, offset int64, runID, agentID string) (dispatchTelemetry, bool) {
	if logPath == "" {
		return dispatchTelemetry{}, false
	}
	f, err := os.Open(logPath)
	if err != nil {
		return dispatchTelemetry{}, false
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return dispatchTelemetry{}, false
	}
	var (
		found bool
		tel   dispatchTelemetry
	)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var rec struct {
			Type         string  `json:"type"`
			Agent        string  `json:"agent"`
			EvalRunID    *string `json:"eval_run_id"`
			DurationS    float64 `json:"duration_s"`
			EstInTokens  int64   `json:"est_input_tokens"`
			EstOutTokens int64   `json:"est_output_tokens"`
			Usage        *struct {
				InputTokens  *int64   `json:"input_tokens"`
				OutputTokens *int64   `json:"output_tokens"`
				Cost         *float64 `json:"total_cost_usd"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.Type != "dispatch_finished" || rec.Agent != agentID || rec.EvalRunID == nil || *rec.EvalRunID != runID {
			continue
		}
		found = true
		tel = dispatchTelemetry{
			DurationS:    rec.DurationS,
			InputTokens:  rec.EstInTokens,
			OutputTokens: rec.EstOutTokens,
		}
		if rec.Usage != nil {
			if rec.Usage.InputTokens != nil {
				tel.InputTokens = *rec.Usage.InputTokens
			}
			if rec.Usage.OutputTokens != nil {
				tel.OutputTokens = *rec.Usage.OutputTokens
			}
			tel.Cost = rec.Usage.Cost
		}
	}
	return tel, found
}

// realJudge invokes dispatch.sh for the judge agent. See realDispatch for
// the path-resolution rationale.
func realJudge(yakosRoot, judgeID, inputJSON, project string) (JudgeResult, error) {
	yakosLib := filepath.Join(yakosRoot, "cli", "lib")
	dispatchSh := filepath.Join(yakosLib, "dispatch.sh")
	if _, statErr := os.Stat(dispatchSh); statErr != nil {
		return JudgeResult{}, fmt.Errorf("%w: %s: %v", ErrDispatchUnavailable, dispatchSh, statErr)
	}

	args := []string{
		dispatchSh,
		judgeID, inputJSON,
	}
	if project != "" {
		args = append(args, "--project", project)
	}

	cmd := exec.Command("bash", args...)
	cmd.Env = append(os.Environ(),
		"YAKOS_ROOT="+yakosRoot,
		"YAKOS_LIB="+yakosLib,
	)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	_ = cmd.Run()

	raw := outBuf.String()
	return parseJudgeOutput(raw), nil
}

// parseJudgeOutput extracts pass/criteria_scores/notes from judge output.
//
// Judges frequently wrap their verdict JSON in prose or a fenced code block,
// so the whole stdout is not required to be a bare object. Candidates are
// tried in order: fenced ```json blocks, then every balanced top-level {...}
// object found by scanning the text. The first candidate that is a JSON
// object with a boolean "pass" (and, when present, an array
// "criteria_scores") wins. When none validates, the result carries
// ParseErr and the raw output so the caller can record it and leave the
// case unscored rather than counting a formatting problem as a failure.
func parseJudgeOutput(raw string) JudgeResult {
	for _, cand := range judgeJSONCandidates(raw) {
		var rec map[string]json.RawMessage
		if err := json.Unmarshal([]byte(cand), &rec); err != nil {
			continue
		}
		passRaw, ok := rec["pass"]
		if !ok {
			continue
		}
		var pass bool
		if err := json.Unmarshal(passRaw, &pass); err != nil {
			continue
		}
		result := JudgeResult{Pass: pass}
		if scoresRaw, ok := rec["criteria_scores"]; ok {
			var scores []json.RawMessage
			if err := json.Unmarshal(scoresRaw, &scores); err != nil {
				continue
			}
			result.CriteriaScores = scores
		}
		if notesRaw, ok := rec["notes"]; ok {
			_ = json.Unmarshal(notesRaw, &result.Notes)
		}
		return result
	}
	return JudgeResult{
		ParseErr: "judge output contains no JSON object with a boolean \"pass\" field",
		Raw:      raw,
	}
}

// judgeJSONCandidates lists candidate JSON object texts in priority order.
func judgeJSONCandidates(raw string) []string {
	var out []string
	trimmed := strings.TrimSpace(raw)
	// Fenced blocks: ```json ... ``` (or bare ```).
	rest := raw
	for {
		i := strings.Index(rest, "```")
		if i < 0 {
			break
		}
		after := rest[i+3:]
		j := strings.Index(after, "```")
		if j < 0 {
			break
		}
		body := after[:j]
		// Drop an optional language tag on the first line.
		if nl := strings.IndexByte(body, '\n'); nl >= 0 {
			tag := strings.TrimSpace(body[:nl])
			if tag == "" || strings.EqualFold(tag, "json") {
				body = body[nl+1:]
			}
		}
		body = strings.TrimSpace(body)
		if strings.HasPrefix(body, "{") {
			out = append(out, body)
		}
		rest = after[j+3:]
	}
	if strings.HasPrefix(trimmed, "{") {
		out = append(out, trimmed)
	}
	out = append(out, balancedObjects(raw)...)
	return out
}

// balancedObjects returns every top-level balanced {...} span in s. Braces
// inside JSON strings are ignored. An unbalanced opener is skipped so a
// stray "{" in prose does not hide a later valid object.
func balancedObjects(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		if s[i] != '{' {
			i++
			continue
		}
		end := matchBrace(s, i)
		if end < 0 {
			i++
			continue
		}
		out = append(out, s[i:end+1])
		i = end + 1
	}
	return out
}

// matchBrace returns the index of the brace closing the one at s[start], or
// -1 when the object never closes.
func matchBrace(s string, start int) int {
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// ---- subcommand: eval -------------------------------------------------------

func runEval(cfg Config) (Result, error) {
	if cfg.AgentID == "" {
		return Result{}, fmt.Errorf("model-routing eval: missing <agent-id>")
	}

	settings := loadSettings(cfg)

	// Resolve agent file.
	agentFile, err := findAgent(cfg.YakosRoot, cfg.AgentID, cfg.Project)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing eval: %w", err)
	}

	currentModel, err := agentCurrentModel(agentFile)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing eval: read model from %s: %w", agentFile, err)
	}
	domain := agentDomain(agentFile)

	// Resolve judge.
	judge, judgeNote := resolveJudge(cfg.Judge, domain, cfg.AgentID)

	// Anti-self-congratulation guard.
	if judge == cfg.AgentID {
		return Result{}, fmt.Errorf(
			"model-routing eval: judge and subject are the same agent (%q); self-evaluation is forbidden. Use --judge <different-agent>.",
			cfg.AgentID,
		)
	}

	tiers, err := resolveTiers(cfg.Tiers, cfg.IncludeFable)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing eval: %w", err)
	}

	maxCost := cfg.MaxCostUSD
	if maxCost <= 0 {
		maxCost = settings.MaxEvalRunCostUSD
	}

	// Weekly budget guard.
	if err := checkWeeklyBudget(cfg, settings); err != nil {
		return Result{}, err
	}

	// Locate eval dir and case files.
	evalDir, err := locateEvalDir(agentFile)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing eval: %w", err)
	}

	caseFiles, err := selectCaseFiles(evalDir, cfg.CasesGlob)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing eval: list cases: %w", err)
	}
	nCases := len(caseFiles)

	if nCases < settings.MinCasesForEval {
		refused := mustJSON(map[string]interface{}{
			"type":         "candidate_refused",
			"ts":           isoNow(cfg.Now),
			"agent":        cfg.AgentID,
			"reason":       "min_cases",
			"n_cases":      nCases,
			"min_required": settings.MinCasesForEval,
		})
		logWrite(cfg.EvalLog, refused)
		return Result{}, fmt.Errorf(
			"model-routing eval: agent %q has %d case(s); minimum is %d",
			cfg.AgentID, nCases, settings.MinCasesForEval,
		)
	}

	runID := genRunID(cfg.Now)

	// Emit eval_run_started.
	started := buildEvalRunStarted(cfg.Now, runID, cfg.AgentID, nCases, tiers, judge, settings.EpsilonPassRate, maxCost)
	logWrite(cfg.EvalLog, started)

	fmt.Fprintf(cfg.Writer, "model-routing eval: %s  run=%s\n", cfg.AgentID, runID)
	fmt.Fprintf(cfg.Writer, "  cases=%d  judge=%s  tiers=%s  budget=$0/$%.2f\n", nCases, judge, strings.Join(tiers, ","), maxCost)
	if judgeNote != "" {
		fmt.Fprintf(cfg.Writer, "  judge: %s\n", judgeNote)
		logWrite(cfg.EvalLog, mustJSON(map[string]interface{}{
			"type":   "judge_fallback",
			"ts":     isoNow(cfg.Now),
			"run_id": runID,
			"agent":  cfg.AgentID,
			"judge":  judge,
			"reason": judgeNote,
		}))
	}
	fmt.Fprintln(cfg.Writer)

	// Resolve dispatch/judge functions.
	dispatchFn := cfg.DispatchFn
	if dispatchFn == nil {
		dispatchFn = func(agentID, task, tier, runID2, project string) (DispatchResult, error) {
			return realDispatch(cfg.YakosRoot, agentID, task, tier, runID2, project)
		}
	}
	judgeFn := cfg.JudgeFn
	if judgeFn == nil {
		judgeFn = func(judgeID, inputJSON, project string) (JudgeResult, error) {
			return realJudge(cfg.YakosRoot, judgeID, inputJSON, project)
		}
	}

	// Per-tier accumulators.
	stats := map[string]*tierStats{
		"haiku":  {},
		"sonnet": {},
		"opus":   {},
		"fable":  {},
	}
	var totalSpent float64
	budgetHit := false
	costUnverifiable := false

outerLoop:
	for _, caseFile := range caseFiles {
		ec, err := loadEvalCase(caseFile)
		if err != nil {
			// Log and skip bad case files (matches bash || continue).
			continue
		}
		hash := caseHash(caseFile)

		for _, tier := range tiers {
			if budgetHit {
				break outerLoop
			}

			// Dispatch subject.
			dr, err := dispatchFn(cfg.AgentID, ec.Task, tier, runID, cfg.Project)
			if err != nil {
				if errors.Is(err, ErrDispatchUnavailable) {
					// The harness itself is broken (dispatch.sh missing or
					// unreachable) — abort loudly instead of silently
					// scoring every remaining case as a failure.
					return Result{}, fmt.Errorf("model-routing eval: %w", err)
				}
				// Non-fatal: treat as failed case.
				dr = DispatchResult{}
			}

			// Fail closed: with no cost telemetry the per-run cap cannot be
			// enforced, so stop instead of spending blind.
			if dr.CostUnknown {
				costUnverifiable = true
				logWrite(cfg.EvalLog, mustJSON(map[string]interface{}{
					"type":      "budget_unverifiable",
					"ts":        isoNow(cfg.Now),
					"run_id":    runID,
					"agent":     cfg.AgentID,
					"case_id":   ec.CaseID,
					"tier":      tier,
					"spent_usd": totalSpent,
					"cap_usd":   maxCost,
					"reason":    "dispatch returned no cost telemetry (no dispatch-log record or no total_cost_usd)",
				}))
				fmt.Fprintf(cfg.Writer, "  ERROR: no cost telemetry for %s/%s; cost cap cannot be enforced; stopping run with partial results\n", ec.CaseID, tier)
				break outerLoop
			}

			// Build judge input.
			judgeInput := mustJSON(map[string]interface{}{
				"case_id":           ec.CaseID,
				"task":              ec.Task,
				"expected_outcomes": ec.ExpectedOutcomes,
				"rubric":            ec.Rubric,
				"agent_response":    dr.Stdout,
				"duration_s":        dr.DurationS,
				"actual_cost_usd":   dr.Cost,
			})

			// Dispatch judge.
			jr, err := judgeFn(judge, judgeInput, cfg.Project)
			if err != nil {
				if errors.Is(err, ErrDispatchUnavailable) {
					return Result{}, fmt.Errorf("model-routing eval: %w", err)
				}
				jr = JudgeResult{ParseErr: "judge dispatch failed: " + err.Error()}
			}

			// A judge verdict that could not be parsed leaves the case
			// unscored: record the raw output, do not count a pass or a fail.
			if jr.Unscored() {
				logWrite(cfg.EvalLog, mustJSON(map[string]interface{}{
					"type":           "eval_case",
					"ts":             isoNow(cfg.Now),
					"run_id":         runID,
					"agent":          cfg.AgentID,
					"case_id":        ec.CaseID,
					"case_hash":      hash,
					"tier":           tier,
					"pass":           nil,
					"scored":         false,
					"judge_error":    jr.ParseErr,
					"judge_raw":      jr.Raw,
					"rubric_scores":  []interface{}{},
					"total_cost_usd": dr.Cost,
					"duration_s":     dr.DurationS,
					"usage": map[string]interface{}{
						"input_tokens":  dr.InputTokens,
						"output_tokens": dr.OutputTokens,
					},
					"judge": judge,
				}))
				stats[tier].unscored++
				stats[tier].cost += dr.Cost
				totalSpent += dr.Cost
				if totalSpent > maxCost {
					budgetHit = tripBudget(cfg, runID, totalSpent, maxCost)
					break
				}
				continue
			}

			// Build criteria_scores JSON for logging.
			var criteriaJSON json.RawMessage
			if len(jr.CriteriaScores) > 0 {
				b, _ := json.Marshal(jr.CriteriaScores)
				criteriaJSON = b
			} else {
				criteriaJSON = json.RawMessage("[]")
			}

			// Emit eval_case record.
			caseRec := mustJSON(map[string]interface{}{
				"type":           "eval_case",
				"ts":             isoNow(cfg.Now),
				"run_id":         runID,
				"agent":          cfg.AgentID,
				"case_id":        ec.CaseID,
				"case_hash":      hash,
				"tier":           tier,
				"pass":           jr.Pass,
				"rubric_scores":  criteriaJSON,
				"total_cost_usd": dr.Cost,
				"duration_s":     dr.DurationS,
				"usage": map[string]interface{}{
					"input_tokens":  dr.InputTokens,
					"output_tokens": dr.OutputTokens,
				},
				"judge":       judge,
				"judge_notes": jr.Notes,
			})
			logWrite(cfg.EvalLog, caseRec)

			// Accumulate.
			st := stats[tier]
			st.total++
			if jr.Pass {
				st.pass++
			}
			st.cost += dr.Cost
			totalSpent += dr.Cost

			// Cost cap check.
			if totalSpent > maxCost {
				budgetHit = tripBudget(cfg, runID, totalSpent, maxCost)
				break
			}
		}
	}

	if costUnverifiable {
		return finishPartial(cfg, runID, stats, totalSpent), fmt.Errorf(
			"model-routing eval: run %s stopped: dispatch returned no cost telemetry, so the $%.2f cap cannot be enforced (partial results logged; no candidate emitted)",
			runID, maxCost)
	}

	// Compute rates and CI.
	haiku := stats["haiku"]
	sonnet := stats["sonnet"]
	opus := stats["opus"]
	fable := stats["fable"]

	haikuRate := haiku.rate()
	sonnetRate := sonnet.rate()
	opusRate := opus.rate()
	fableRate := fable.rate()

	haikuCI := WilsonLower(haiku.pass, haiku.total)
	sonnetCI := WilsonLower(sonnet.pass, sonnet.total)
	opusCI := WilsonLower(opus.pass, opus.total)
	fableCI := WilsonLower(fable.pass, fable.total)

	haikuMeanCost := haiku.meanCost()
	sonnetMeanCost := sonnet.meanCost()
	opusMeanCost := opus.meanCost()
	fableMeanCost := fable.meanCost()

	// Promotion decision.
	var curRate, curMeanCost float64
	switch currentModel {
	case "haiku":
		curRate = haikuRate
		curMeanCost = haikuMeanCost
	case "sonnet":
		curRate = sonnetRate
		curMeanCost = sonnetMeanCost
	case "opus":
		curRate = opusRate
		curMeanCost = opusMeanCost
	case "fable":
		curRate = fableRate
		curMeanCost = fableMeanCost
	default:
		curRate = sonnetRate
		curMeanCost = sonnetMeanCost
	}

	var candidateTier, candidateReason string

	// The current tier is the baseline every candidate is measured against.
	// If it was not run (or nothing was scored), curRate would read 0 and any
	// candidate would clear the gate, so refuse instead.
	curKey := currentModel
	if _, ok := stats[curKey]; !ok {
		curKey = "sonnet"
	}
	baselineMissing := stats[curKey].total == 0
	if baselineMissing {
		logWrite(cfg.EvalLog, mustJSON(map[string]interface{}{
			"type":   "candidate_refused",
			"ts":     isoNow(cfg.Now),
			"run_id": runID,
			"agent":  cfg.AgentID,
			"reason": "current_tier_not_run",
			"detail": fmt.Sprintf("baseline tier %q has no scored cases; include it in --tiers (or --include-fable)", curKey),
		}))
	}

	gate := Gate{
		Baseline:     curKey,
		BaselineN:    stats[curKey].total,
		BaselineRate: stats[curKey].rate(),
		MinCasesConf: settings.MinCasesForConf,
		Epsilon:      settings.EpsilonPassRate,
	}

	for _, candTier := range []string{"haiku", "sonnet", "opus"} {
		if baselineMissing {
			break
		}
		if stats[candTier].total == 0 {
			continue // not run (or nothing scored): nothing to evaluate
		}
		if !tierCheaperThan(candTier, currentModel) {
			continue
		}
		var candRate, candCI, candCost float64
		var nRun int
		switch candTier {
		case "haiku":
			candRate = haikuRate
			candCI = haikuCI
			candCost = haikuMeanCost
			nRun = haiku.total
		case "sonnet":
			candRate = sonnetRate
			candCI = sonnetCI
			candCost = sonnetMeanCost
			nRun = sonnet.total
		case "opus":
			candRate = opusRate
			candCI = opusCI
			candCost = opusMeanCost
			nRun = opus.total
		}

		if nRun >= settings.MinCasesForConf {
			// CI-only gate.
			threshold := curRate - settings.EpsilonPassRate
			gate.Rows = append(gate.Rows, GateRow{
				Tier: candTier, NScored: nRun, CILower: candCI, Mode: "ci",
				Threshold: threshold, Pass: candCI >= threshold,
				Detail: fmt.Sprintf("ci_lower %.1f%% vs baseline %.1f%% - epsilon %.1f%% = %.1f%%",
					candCI*100, curRate*100, settings.EpsilonPassRate*100, threshold*100),
			})
			if candCI >= threshold {
				candidateTier = candTier
				candidateReason = fmt.Sprintf(
					"ci_lower[%s]=%.6f >= pass_rate[%s]=%.6f - epsilon=%.6f",
					candTier, candCI, currentModel, curRate, settings.EpsilonPassRate,
				)
				break
			}
			// Refused.
			refusedRec := mustJSON(map[string]interface{}{
				"type":   "candidate_refused",
				"ts":     isoNow(cfg.Now),
				"run_id": runID,
				"agent":  cfg.AgentID,
				"reason": "low_confidence",
				"detail": fmt.Sprintf(
					"ci_lower[%s]=%.6f < pass_rate[%s]=%.6f - epsilon=%.6f",
					candTier, candCI, currentModel, curRate, settings.EpsilonPassRate,
				),
			})
			logWrite(cfg.EvalLog, refusedRec)
		} else {
			// Strict floor gate.
			costOK := curMeanCost > 0 && candCost <= curMeanCost/2
			marginOK := candRate >= curRate+0.10
			gate.Rows = append(gate.Rows, GateRow{
				Tier: candTier, NScored: nRun, CILower: candCI, Mode: "strict_floor",
				Pass: costOK && marginOK,
				Detail: fmt.Sprintf("n=%d < %d: need >=2x cost saving (%v) and >=+0.10 pass-rate margin (%v)",
					nRun, settings.MinCasesForConf, costOK, marginOK),
			})

			if costOK && marginOK {
				candidateTier = candTier
				candidateReason = fmt.Sprintf(
					"strict_floor: cost_saving=%.6f<=%.6f/2 AND margin=%.6f>=%.6f+0.10",
					candCost, curMeanCost, candRate, curRate,
				)
				break
			}
			var refusedReason string
			if costOK {
				refusedReason = "cost_only_no_quality"
			} else {
				refusedReason = "insufficient_cost_saving"
			}
			refusedRec := mustJSON(map[string]interface{}{
				"type":   "candidate_refused",
				"ts":     isoNow(cfg.Now),
				"run_id": runID,
				"agent":  cfg.AgentID,
				"reason": refusedReason,
				"detail": fmt.Sprintf("cost_ok=%v margin_ok=%v n_run=%d", costOK, marginOK, nRun),
			})
			logWrite(cfg.EvalLog, refusedRec)
		}
	}

	candidateEmitted := candidateTier != ""
	switch {
	case candidateEmitted:
		gate.Decision, gate.Reason = "candidate", candidateReason
	case baselineMissing:
		gate.Decision = "refused"
		gate.Reason = fmt.Sprintf("current_tier_not_run: baseline tier %q has no scored cases", curKey)
	case len(gate.Rows) == 0:
		gate.Decision = "refused"
		gate.Reason = "no cheaper tier was run and scored"
	default:
		gate.Decision = "refused"
		gate.Reason = "no cheaper tier cleared the gate"
	}
	tierN := map[string]int{}
	tierUnscored := map[string]int{}
	tierCI := map[string]float64{}
	for _, tier := range tiers {
		tierN[tier] = stats[tier].total
		tierUnscored[tier] = stats[tier].unscored
		tierCI[tier] = WilsonLower(stats[tier].pass, stats[tier].total)
	}

	// Emit eval_run_finished.
	finishedRec := mustJSON(map[string]interface{}{
		"type":   "eval_run_finished",
		"ts":     isoNow(cfg.Now),
		"run_id": runID,
		"agent":  cfg.AgentID,
		"tier_pass_rates": map[string]float64{
			"haiku":  haikuRate,
			"sonnet": sonnetRate,
			"opus":   opusRate,
			"fable":  fableRate,
		},
		"tier_mean_costs": map[string]float64{
			"haiku":  haikuMeanCost,
			"sonnet": sonnetMeanCost,
			"opus":   opusMeanCost,
			"fable":  fableMeanCost,
		},
		"tier_ci_lower": map[string]float64{
			"haiku":  haikuCI,
			"sonnet": sonnetCI,
			"opus":   opusCI,
			"fable":  fableCI,
		},
		"tiers_run":         tiers,
		"tier_n_scored":     tierN,
		"tier_n_unscored":   tierUnscored,
		"gate":              gate,
		"candidate_emitted": candidateEmitted,
		"candidate_tier":    nilOrString(candidateTier),
		"candidate_reason":  candidateReason,
	})
	logWrite(cfg.EvalLog, finishedRec)

	// Emit candidate record.
	if candidateEmitted {
		var ctCost float64
		switch candidateTier {
		case "haiku":
			ctCost = haikuMeanCost
		case "sonnet":
			ctCost = sonnetMeanCost
		case "opus":
			ctCost = opusMeanCost
		}
		savings := curMeanCost - ctCost
		if savings < 0 {
			savings = 0
		}
		savingsPerMo := savings * 1000 // matches bash: diff*1000

		candRec := mustJSON(map[string]interface{}{
			"agent":           cfg.AgentID,
			"current_model":   currentModel,
			"suggested_model": candidateTier,
			"evidence": map[string]interface{}{
				"pass_rates":   map[string]float64{"haiku": haikuRate, "sonnet": sonnetRate, "opus": opusRate, "fable": fableRate},
				"ci_lower":     map[string]float64{"haiku": haikuCI, "sonnet": sonnetCI, "opus": opusCI, "fable": fableCI},
				"mean_costs":   map[string]float64{"haiku": haikuMeanCost, "sonnet": sonnetMeanCost, "opus": opusMeanCost, "fable": fableMeanCost},
				"n_cases":      nCases,
				"eval_run_id":  runID,
				"epsilon_used": settings.EpsilonPassRate,
				"judge":        judge,
			},
			"estimated_monthly_savings_usd": savingsPerMo,
			"generated_at":                  isoNow(cfg.Now),
		})
		logWrite(cfg.CandidatesFile, candRec)
	}

	// Human summary.
	fmt.Fprintf(cfg.Writer, "  tier     pass-rate  ci-lower  mean-cost/case  n\n")
	for _, tier := range tiers {
		st := stats[tier]
		note := ""
		if st.unscored > 0 {
			note = fmt.Sprintf("  (%d unscored: judge output unparseable)", st.unscored)
		}
		fmt.Fprintf(cfg.Writer, "  %-8s   %5.1f%%    %5.1f%%    $%.4f          %d%s\n",
			tier, st.rate()*100, WilsonLower(st.pass, st.total)*100, st.meanCost(), st.total, note)
	}
	printGate(cfg.Writer, gate)
	fmt.Fprintln(cfg.Writer)

	if candidateEmitted {
		var savingsPerMo float64
		var ctCost float64
		switch candidateTier {
		case "haiku":
			ctCost = haikuMeanCost
		case "sonnet":
			ctCost = sonnetMeanCost
		case "opus":
			ctCost = opusMeanCost
		}
		savingsPerMo = (curMeanCost - ctCost) * 1000
		if savingsPerMo < 0 {
			savingsPerMo = 0
		}
		fmt.Fprintf(cfg.Writer, "  candidate: %s  (savings ~$%.2f/mo)\n", candidateTier, savingsPerMo)
		fmt.Fprintf(cfg.Writer, "    reason: %s\n", candidateReason)
	} else {
		fmt.Fprintf(cfg.Writer, "  candidate: refused\n")
		fmt.Fprintf(cfg.Writer, "    (see eval-log for details)\n")
	}
	fmt.Fprintf(cfg.Writer, "  total spent: $%.6f\n", totalSpent)

	return Result{
		Subcommand:       "eval",
		EvalRunID:        runID,
		CandidateEmitted: candidateEmitted,
		CandidateTier:    candidateTier,
		CandidateReason:  candidateReason,
		TierCILower:      tierCI,
		Gate:             gate,
	}, nil
}

// printGate writes the promotion-gate evaluation: the Wilson lower bound per
// candidate tier against the gate that applies at its case count, and the
// decision. Format is stable so callers can grep the "gate decision" line.
func printGate(w io.Writer, g Gate) {
	fmt.Fprintf(w, "  gate: baseline=%s n=%d pass=%.1f%%  min_cases_for_confidence=%d  epsilon=%.2f\n",
		g.Baseline, g.BaselineN, g.BaselineRate*100, g.MinCasesConf, g.Epsilon)
	for _, r := range g.Rows {
		verdict := "FAIL"
		if r.Pass {
			verdict = "PASS"
		}
		fmt.Fprintf(w, "    %-7s n=%-3d ci-lower=%5.1f%%  mode=%-12s %s  %s\n",
			r.Tier, r.NScored, r.CILower*100, r.Mode, verdict, r.Detail)
	}
	fmt.Fprintf(w, "  gate decision: %s (%s)\n", strings.ToUpper(g.Decision), g.Reason)
}

// finishPartial logs an eval_run_finished record for a run that stopped
// early (no candidate is ever emitted from a partial run), prints the partial
// per-tier results, and returns the Result.
func finishPartial(cfg Config, runID string, stats map[string]*tierStats, spent float64) Result {
	rates := map[string]float64{}
	cis := map[string]float64{}
	costs := map[string]float64{}
	ns := map[string]int{}
	for _, tier := range []string{"haiku", "sonnet", "opus", "fable"} {
		st := stats[tier]
		rates[tier] = st.rate()
		cis[tier] = WilsonLower(st.pass, st.total)
		costs[tier] = st.meanCost()
		ns[tier] = st.total
	}
	logWrite(cfg.EvalLog, mustJSON(map[string]interface{}{
		"type":              "eval_run_finished",
		"ts":                isoNow(cfg.Now),
		"run_id":            runID,
		"agent":             cfg.AgentID,
		"partial":           true,
		"tier_pass_rates":   rates,
		"tier_mean_costs":   costs,
		"tier_ci_lower":     cis,
		"tier_n_scored":     ns,
		"candidate_emitted": false,
		"candidate_tier":    nil,
		"candidate_reason":  "partial run: stopped before completion",
	}))
	fmt.Fprintf(cfg.Writer, "  partial results (run stopped early):\n")
	fmt.Fprintf(cfg.Writer, "  tier     n  pass-rate  ci-lower  mean-cost/case\n")
	for _, tier := range []string{"haiku", "sonnet", "opus", "fable"} {
		if stats[tier].total == 0 && stats[tier].unscored == 0 {
			continue
		}
		fmt.Fprintf(cfg.Writer, "  %-8s %2d  %5.1f%%    %5.1f%%    $%.4f\n",
			tier, ns[tier], rates[tier]*100, cis[tier]*100, costs[tier])
	}
	fmt.Fprintf(cfg.Writer, "  candidate: none (partial run)\n")
	fmt.Fprintf(cfg.Writer, "  total spent (known): $%.6f\n", spent)
	return Result{Subcommand: "eval", EvalRunID: runID}
}

// tripBudget records the budget_exceeded event and warns. It returns true so
// callers can assign it straight to the budget flag.
func tripBudget(cfg Config, runID string, spent, maxCost float64) bool {
	logWrite(cfg.EvalLog, mustJSON(map[string]interface{}{
		"type":      "budget_exceeded",
		"ts":        isoNow(cfg.Now),
		"run_id":    runID,
		"agent":     cfg.AgentID,
		"spent_usd": spent,
		"cap_usd":   maxCost,
	}))
	fmt.Fprintf(cfg.Writer, "  WARN: budget cap $%.2f exceeded after $%.6f spent; aborting run\n", maxCost, spent)
	return true
}

// nilOrString returns the string as a Go interface (nil when empty) for JSON null.
func nilOrString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// ---- subcommand: list -------------------------------------------------------

// candidateRecord is the top-level shape of a candidates.ndjson line.
type candidateRecord struct {
	Agent                      string          `json:"agent"`
	CurrentModel               string          `json:"current_model"`
	SuggestedModel             string          `json:"suggested_model"`
	EstimatedMonthlySavingsUSD float64         `json:"estimated_monthly_savings_usd"`
	GeneratedAt                string          `json:"generated_at"`
	Evidence                   json.RawMessage `json:"evidence"`
}

func (c candidateRecord) nCases() int {
	var ev map[string]json.RawMessage
	if err := json.Unmarshal(c.Evidence, &ev); err != nil {
		return 0
	}
	raw, ok := ev["n_cases"]
	if !ok {
		return 0
	}
	var n int
	_ = json.Unmarshal(raw, &n)
	return n
}

func (c candidateRecord) ciLower() map[string]float64 {
	var ev map[string]json.RawMessage
	if err := json.Unmarshal(c.Evidence, &ev); err != nil {
		return nil
	}
	raw, ok := ev["ci_lower"]
	if !ok {
		return nil
	}
	var m map[string]float64
	_ = json.Unmarshal(raw, &m)
	return m
}

func readAllCandidates(path string) ([]candidateRecord, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var recs []candidateRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var r candidateRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		recs = append(recs, r)
	}
	return recs, scanner.Err()
}

// latestPerAgent returns only the most recent candidateRecord per agent.
func latestPerAgent(recs []candidateRecord) []candidateRecord {
	latest := make(map[string]candidateRecord)
	for _, r := range recs {
		if prev, ok := latest[r.Agent]; !ok || r.GeneratedAt > prev.GeneratedAt {
			latest[r.Agent] = r
		}
	}
	var out []candidateRecord
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Agent < out[j].Agent
	})
	return out
}

func runList(cfg Config) (Result, error) {
	if _, err := os.Stat(cfg.CandidatesFile); os.IsNotExist(err) {
		fmt.Fprintln(cfg.Writer, "no candidates yet (run: yakos model-routing eval <agent>)")
		return Result{Subcommand: "list", ListCount: 0}, nil
	}

	recs, err := readAllCandidates(cfg.CandidatesFile)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing list: %w", err)
	}
	latest := latestPerAgent(recs)

	if len(latest) == 0 {
		fmt.Fprintln(cfg.Writer, "(no records)")
		return Result{Subcommand: "list", ListCount: 0}, nil
	}

	for _, r := range latest {
		n := r.nCases()
		ci := r.ciLower()
		var ciParts []string
		for _, tier := range []string{"haiku", "sonnet", "opus", "fable"} {
			if v, ok := ci[tier]; ok {
				ciParts = append(ciParts, fmt.Sprintf("%s=%.6f", tier, v))
			}
		}
		ciStr := strings.Join(ciParts, ",")
		fmt.Fprintf(cfg.Writer,
			"%-24s current=%-8s -> suggested=%-8s savings=~$%.2f/mo  n=%d  ci=[%s]\n",
			r.Agent, r.CurrentModel, r.SuggestedModel,
			r.EstimatedMonthlySavingsUSD, n, ciStr,
		)
	}

	return Result{Subcommand: "list", ListCount: len(latest)}, nil
}

// ---- subcommand: show -------------------------------------------------------

func runShow(cfg Config) (Result, error) {
	if cfg.AgentID == "" {
		return Result{}, fmt.Errorf("model-routing show: missing <agent-id>")
	}

	if _, err := os.Stat(cfg.CandidatesFile); os.IsNotExist(err) {
		fmt.Fprintln(cfg.Writer, "no candidates on file")
		return Result{Subcommand: "show"}, nil
	}

	recs, err := readAllCandidates(cfg.CandidatesFile)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing show: %w", err)
	}

	// Latest candidate for this agent.
	var latest *candidateRecord
	for i := range recs {
		if recs[i].Agent == cfg.AgentID {
			if latest == nil || recs[i].GeneratedAt > latest.GeneratedAt {
				cp := recs[i]
				latest = &cp
			}
		}
	}

	if latest == nil {
		fmt.Fprintf(cfg.Writer, "no candidate for agent %q\n", cfg.AgentID)
		return Result{Subcommand: "show"}, nil
	}

	fmt.Fprintf(cfg.Writer, "=== candidate: %s ===\n", cfg.AgentID)
	b, _ := json.MarshalIndent(latest, "", "  ")
	fmt.Fprintf(cfg.Writer, "%s\n\n", b)

	// Last 3 eval run finished records.
	fmt.Fprintf(cfg.Writer, "=== last 3 eval runs for %s ===\n", cfg.AgentID)
	if _, err := os.Stat(cfg.EvalLog); os.IsNotExist(err) {
		fmt.Fprintln(cfg.Writer, "(eval log not found)")
		return Result{Subcommand: "show"}, nil
	}

	runFinished, err := readEvalRunFinished(cfg.EvalLog, cfg.AgentID)
	if err != nil {
		fmt.Fprintln(cfg.Writer, "(error reading eval log)")
		return Result{Subcommand: "show"}, nil
	}
	if len(runFinished) == 0 {
		fmt.Fprintln(cfg.Writer, "(no eval_run_finished records found)")
		return Result{Subcommand: "show"}, nil
	}

	// Sort by ts, take last 3.
	sort.Slice(runFinished, func(i, j int) bool {
		return runFinished[i]["ts"].(string) < runFinished[j]["ts"].(string)
	})
	start := len(runFinished) - 3
	if start < 0 {
		start = 0
	}
	for _, rec := range runFinished[start:] {
		b, _ := json.Marshal(rec)
		fmt.Fprintln(cfg.Writer, string(b))
	}

	return Result{Subcommand: "show"}, nil
}

func readEvalRunFinished(logPath, agentID string) ([]map[string]interface{}, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []map[string]interface{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["type"] != "eval_run_finished" {
			continue
		}
		if agentID != "" && rec["agent"] != agentID {
			continue
		}
		out = append(out, rec)
	}
	return out, scanner.Err()
}

// ---- subcommand: promote ----------------------------------------------------

func runPromote(cfg Config) (Result, error) {
	if cfg.AgentID == "" {
		return Result{}, fmt.Errorf("model-routing promote: missing <agent-id>")
	}

	// 1. Locate agent file.
	agentFile, isFramework, err := findAgentForPromote(cfg.YakosRoot, cfg.AgentID, cfg.Project)
	if err != nil {
		return Result{}, fmt.Errorf("model-routing promote: agent %q not found in project or framework", cfg.AgentID)
	}

	// 2. Framework guard.
	if isFramework && !cfg.Global {
		return Result{}, fmt.Errorf(
			"model-routing promote: %q is a framework-shipped agent.\nPass --global to promote a framework-shipped agent (rewrites lib/agents/%s.md under YAKOS_ROOT).",
			cfg.AgentID, cfg.AgentID,
		)
	}

	// 3. Find latest candidate.
	cand, err := latestCandidateFor(cfg.CandidatesFile, cfg.AgentID)
	if err != nil || cand == nil {
		return Result{}, fmt.Errorf(
			"model-routing promote: no candidate on file for %q (run: yakos model-routing eval %s)",
			cfg.AgentID, cfg.AgentID,
		)
	}

	currentModel := cand.CurrentModel
	suggestedModel := cand.SuggestedModel

	var evalRunID string
	var ev map[string]json.RawMessage
	if err := json.Unmarshal(cand.Evidence, &ev); err == nil {
		if raw, ok := ev["eval_run_id"]; ok {
			_ = json.Unmarshal(raw, &evalRunID)
		}
	}
	if evalRunID == "" {
		evalRunID = "unknown"
	}

	// 4. Backup.
	if err := os.MkdirAll(cfg.BackupsDir, 0755); err != nil { //nolint:gosec
		return Result{}, fmt.Errorf("model-routing promote: mkdir backups: %w", err)
	}
	tsSafe := cfg.Now.UTC().Format("20060102T150405Z")
	backupFile := filepath.Join(cfg.BackupsDir, cfg.AgentID+"-"+tsSafe+".md")
	if err := copyFile(agentFile, backupFile); err != nil {
		return Result{}, fmt.Errorf("model-routing promote: backup: %w", err)
	}

	// 5. Atomic frontmatter rewrite.
	if err := rewriteModelFrontmatter(agentFile, suggestedModel); err != nil {
		return Result{}, fmt.Errorf("model-routing promote: rewrite frontmatter: %w", err)
	}

	// 6. Validate.
	if cfg.ValidateFn != nil {
		var validateTarget string
		if isFramework {
			validateTarget = cfg.YakosRoot
		} else {
			validateTarget = cfg.Project
			if validateTarget == "" {
				cwd, _ := os.Getwd()
				validateTarget = cwd
			}
		}
		if err := cfg.ValidateFn(validateTarget); err != nil {
			// Restore from backup.
			_ = copyFile(backupFile, agentFile)
			return Result{}, fmt.Errorf(
				"model-routing promote: validation failed after rewrite; original restored from %s",
				backupFile,
			)
		}
	}

	// 7. Append history entry.
	byUser := currentUser()
	histRec := mustJSON(map[string]interface{}{
		"ts":          isoNow(cfg.Now),
		"action":      "promoted",
		"agent":       cfg.AgentID,
		"from_model":  currentModel,
		"to_model":    suggestedModel,
		"eval_run_id": evalRunID,
		"by_user":     byUser,
	})
	logWrite(cfg.HistoryFile, histRec)

	// 8. Strip candidate.
	if err := stripCandidate(cfg.CandidatesFile, cfg.AgentID); err != nil {
		// Non-fatal.
		fmt.Fprintf(cfg.ErrWriter, "model-routing promote: strip candidate: %v\n", err)
	}

	fmt.Fprintf(cfg.Writer, "model-routing promote: %s\n", cfg.AgentID)
	fmt.Fprintf(cfg.Writer, "  %s -> %s\n", currentModel, suggestedModel)
	fmt.Fprintf(cfg.Writer, "  agent file: %s\n", agentFile)
	fmt.Fprintf(cfg.Writer, "  backup:     %s\n", backupFile)

	return Result{
		Subcommand:  "promote",
		PromoteFrom: currentModel,
		PromoteTo:   suggestedModel,
	}, nil
}

// findAgentForPromote searches project then framework. Returns (path, isFramework, error).
func findAgentForPromote(yakosRoot, id, project string) (string, bool, error) {
	// Project override first.
	if project == "" {
		if pd := os.Getenv("YAKOS_PROJECT_DIR"); pd != "" {
			project = pd
		} else {
			var err error
			project, err = os.Getwd()
			if err != nil {
				project = "."
			}
		}
	}
	if project != "" {
		f := filepath.Join(project, ".claude", "agents", id+".md")
		if _, err := os.Stat(f); err == nil {
			return f, false, nil
		}
	}
	if yakosRoot != "" {
		f := filepath.Join(yakosRoot, "lib", "agents", id+".md")
		if _, err := os.Stat(f); err == nil {
			return f, true, nil
		}
	}
	return "", false, fmt.Errorf("not found")
}

// latestCandidateFor returns the most recent candidateRecord for agentID, or nil.
func latestCandidateFor(path, agentID string) (*candidateRecord, error) {
	recs, err := readAllCandidates(path)
	if err != nil {
		return nil, err
	}
	var latest *candidateRecord
	for i := range recs {
		if recs[i].Agent == agentID {
			if latest == nil || recs[i].GeneratedAt > latest.GeneratedAt {
				cp := recs[i]
				latest = &cp
			}
		}
	}
	return latest, nil
}

// rewriteModelFrontmatter atomically rewrites the model: field in YAML frontmatter.
func rewriteModelFrontmatter(path, newModel string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	inFM := false
	done := false
	for i, line := range lines {
		if i == 0 {
			if strings.TrimRight(line, " \t") == "---" {
				inFM = true
			}
			continue
		}
		if inFM && !done {
			if strings.TrimRight(line, " \t") == "---" {
				inFM = false
				done = true
				continue
			}
			if strings.HasPrefix(line, "model:") {
				lines[i] = "model: " + newModel
			}
		}
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0644); err != nil { //nolint:gosec
		return err
	}
	return os.Rename(tmp, path)
}

// stripCandidate removes all rows for agentID from the candidates file.
func stripCandidate(path, agentID string) error {
	recs, err := readAllCandidates(path)
	if err != nil || len(recs) == 0 {
		return err
	}

	var buf bytes.Buffer
	for _, r := range recs {
		if r.Agent == agentID {
			continue
		}
		b, _ := json.Marshal(r)
		buf.Write(b)
		buf.WriteByte('\n')
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0644); err != nil { //nolint:gosec
		return err
	}
	return os.Rename(tmp, path)
}

// copyFile copies src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644) //nolint:gosec
}

// currentUser returns the operating system username (or "unknown").
func currentUser() string {
	u := os.Getenv("USER")
	if u != "" {
		return u
	}
	u = os.Getenv("LOGNAME")
	if u != "" {
		return u
	}
	out, err := exec.Command("id", "-un").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return "unknown"
}

// ---- subcommand: reject -----------------------------------------------------

func runReject(cfg Config) (Result, error) {
	if cfg.AgentID == "" {
		return Result{}, fmt.Errorf("model-routing reject: missing <agent-id>")
	}

	cand, err := latestCandidateFor(cfg.CandidatesFile, cfg.AgentID)
	if err != nil || cand == nil {
		return Result{}, fmt.Errorf("model-routing reject: no candidate on file for %q", cfg.AgentID)
	}

	suggestedModel := cand.SuggestedModel
	var evalRunID string
	var ev map[string]json.RawMessage
	if err := json.Unmarshal(cand.Evidence, &ev); err == nil {
		if raw, ok := ev["eval_run_id"]; ok {
			_ = json.Unmarshal(raw, &evalRunID)
		}
	}
	if evalRunID == "" {
		evalRunID = "unknown"
	}

	// Repeat-rejection guard.
	rejectCount := graveyardCount(cfg.GraveyardFile, cfg.AgentID, suggestedModel)
	if rejectCount >= 3 && !cfg.Force {
		return Result{}, fmt.Errorf(
			"model-routing reject: (%s, %s) has been rejected %d times already.\nPass --force to reject again despite the repeat-rejection guard.",
			cfg.AgentID, suggestedModel, rejectCount,
		)
	}

	// Strip candidate.
	if err := stripCandidate(cfg.CandidatesFile, cfg.AgentID); err != nil {
		fmt.Fprintf(cfg.ErrWriter, "model-routing reject: strip candidate: %v\n", err)
	}

	// Append to graveyard.
	byUser := currentUser()
	graveRec := mustJSON(map[string]interface{}{
		"ts":              isoNow(cfg.Now),
		"agent":           cfg.AgentID,
		"suggested_model": suggestedModel,
		"eval_run_id":     evalRunID,
		"reason":          cfg.Note,
		"by_user":         byUser,
	})
	logWrite(cfg.GraveyardFile, graveRec)

	fmt.Fprintf(cfg.Writer, "model-routing reject: %s (suggested: %s)\n", cfg.AgentID, suggestedModel)
	if cfg.Note != "" {
		fmt.Fprintf(cfg.Writer, "  note: %s\n", cfg.Note)
	}

	return Result{
		Subcommand:             "reject",
		RejectedAgent:          cfg.AgentID,
		RejectedSuggestedModel: suggestedModel,
	}, nil
}

// graveyardCount returns how many times (agent, suggestedModel) appears in the graveyard.
func graveyardCount(graveyardFile, agentID, suggestedModel string) int {
	f, err := os.Open(graveyardFile)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var rec map[string]string
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec["agent"] == agentID && rec["suggested_model"] == suggestedModel {
			count++
		}
	}
	return count
}

// ---- subcommand: history ----------------------------------------------------

type historyRecord struct {
	TS        string `json:"ts"`
	Action    string `json:"action"`
	Agent     string `json:"agent"`
	FromModel string `json:"from_model"`
	ToModel   string `json:"to_model"`
	Reason    string `json:"reason"`
	ByUser    string `json:"by_user"`
	EvalRunID string `json:"eval_run_id"`
}

func runHistory(cfg Config) (Result, error) {
	var combined []historyRecord

	// Read history (promoted).
	if _, err := os.Stat(cfg.HistoryFile); err == nil {
		recs, err := readHistoryFile(cfg.HistoryFile, cfg.FilterAgent)
		if err == nil {
			combined = append(combined, recs...)
		}
	}

	// Read graveyard (rejected) — normalize to history shape.
	if _, err := os.Stat(cfg.GraveyardFile); err == nil {
		recs, err := readGraveyardAsHistory(cfg.GraveyardFile, cfg.FilterAgent)
		if err == nil {
			combined = append(combined, recs...)
		}
	}

	if len(combined) == 0 {
		fmt.Fprintln(cfg.Writer, "(no history records found)")
		return Result{Subcommand: "history", HistoryCount: 0}, nil
	}

	// Sort descending by ts.
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].TS > combined[j].TS
	})

	// Print table.
	fmt.Fprintf(cfg.Writer, "%-26s %-10s %-20s %-20s %-30s %s\n",
		"ts", "action", "agent", "from_model -> to_model", "reason", "by_user")
	fmt.Fprintln(cfg.Writer, strings.Repeat("-", 110))

	for _, r := range combined {
		fromTo := r.FromModel + " -> " + r.ToModel
		reason := r.Reason
		if len(reason) > 29 {
			reason = reason[:29]
		}
		fmt.Fprintf(cfg.Writer, "%-26s %-10s %-20s %-20s %-30s %s\n",
			r.TS, r.Action, r.Agent, fromTo, reason, r.ByUser)
	}

	return Result{Subcommand: "history", HistoryCount: len(combined)}, nil
}

func readHistoryFile(path, filterAgent string) ([]historyRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var recs []historyRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var r historyRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		if filterAgent != "" && r.Agent != filterAgent {
			continue
		}
		recs = append(recs, r)
	}
	return recs, scanner.Err()
}

func readGraveyardAsHistory(path, filterAgent string) ([]historyRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var recs []historyRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var raw map[string]string
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		agent := raw["agent"]
		if filterAgent != "" && agent != filterAgent {
			continue
		}
		recs = append(recs, historyRecord{
			TS:        raw["ts"],
			Action:    "rejected",
			Agent:     agent,
			FromModel: raw["suggested_model"],
			ToModel:   "-",
			Reason:    raw["reason"],
			ByUser:    raw["by_user"],
			EvalRunID: raw["eval_run_id"],
		})
	}
	return recs, scanner.Err()
}
