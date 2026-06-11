// model_routing_parity_test.go — Phase 1 parity tests for `yakos model-routing`.
//
// Design notes:
//
//  1. All tests call routing.Run directly with temp-dir paths; no bash runtime
//     or real ~/.yakos-state directory is required.
//
//  2. The bash model-routing.sh manages the eval harness, candidates, promote,
//     reject and history subcommands. This file verifies the Go port's behaviour
//     is structurally identical across the documented subcommand surface.
//
//  3. DispatchFn and JudgeFn are injected so no real dispatch.sh is invoked.
//
// Critical scenarios:
//
//	(a) eval: missing agent-id → error
//	(b) eval: agent not found → error
//	(c) eval: self-judge forbidden → error
//	(d) eval: too few cases → error + log candidate_refused
//	(e) eval: all cases pass with CI gate → candidate emitted
//	(f) eval: budget cap exceeded → budget_exceeded logged + warn
//	(g) eval: eval_run_started + eval_run_finished written
//	(h) eval: eval_case records × 3 tiers written
//	(i) list: no candidates file → advisory
//	(j) list: latest-per-agent semantics
//	(k) show: missing agent-id → error
//	(l) show: no candidate → advisory
//	(m) show: renders candidate JSON + eval history
//	(n) promote: framework guard (no --global) → error
//	(o) promote: rewrites frontmatter + backup + history
//	(p) promote: validation failure → restore backup
//	(q) reject: repeat-rejection guard (3 times) → error
//	(r) reject: --force bypasses guard
//	(s) reject: writes graveyard entry + strips candidate
//	(t) history: descending sort, agent filter
//	(u) unknown subcommand → error
//	(v) portedCommands entry present
//	(w) help text contains key phrases
//	(x) WilsonLower parity with bash awk (20 cases)
package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/routing"
)

// ---- helpers ----------------------------------------------------------------

var mrTestNow = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

func newMRConfig(t *testing.T) routing.Config {
	t.Helper()
	tmp := t.TempDir()
	return routing.Config{
		YakosRoot:      tmp,
		StateDir:       tmp,
		EvalLog:        filepath.Join(tmp, "eval-log.ndjson"),
		CandidatesFile: filepath.Join(tmp, "candidates.ndjson"),
		HistoryFile:    filepath.Join(tmp, "history.ndjson"),
		GraveyardFile:  filepath.Join(tmp, "graveyard.ndjson"),
		BackupsDir:     filepath.Join(tmp, "backups"),
		HomeDir:        tmp,
		Now:            mrTestNow,
		Writer:         &bytes.Buffer{},
		ErrWriter:      &bytes.Buffer{},
	}
}

func mrOut(cfg routing.Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func mrErrOut(cfg routing.Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

func mrWriteAgent(t *testing.T, dir, name, model string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name+".md")
	content := "---\nmodel: " + model + "\ndomain: backend\n---\n\n# " + name + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write agent %s: %v", path, err)
	}
	return path
}

func mrWriteEvalCase(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir eval: %v", err)
	}
	rec := map[string]interface{}{
		"case_id":          id,
		"task":             "solve task " + id,
		"expected_outcomes": []string{"correct"},
		"rubric":           map[string]interface{}{"criteria": []interface{}{}},
	}
	data, _ := json.Marshal(rec)
	_ = os.WriteFile(filepath.Join(dir, "case-"+id+".json"), data, 0644)
}

func mrSetupAgent(t *testing.T, cfg routing.Config, name, model string, nCases int) {
	t.Helper()
	agentsDir := filepath.Join(cfg.YakosRoot, "lib", "agents")
	mrWriteAgent(t, agentsDir, name, model)
	evalDir := filepath.Join(agentsDir, name, "eval")
	for i := 0; i < nCases; i++ {
		mrWriteEvalCase(t, evalDir, string(rune('a'+i)))
	}
}

func mrWriteCandidate(t *testing.T, cfg routing.Config, agentID, current, suggested, ts string) {
	t.Helper()
	rec := map[string]interface{}{
		"agent":           agentID,
		"current_model":   current,
		"suggested_model": suggested,
		"estimated_monthly_savings_usd": 1.0,
		"generated_at":    ts,
		"evidence": map[string]interface{}{
			"n_cases":      10,
			"eval_run_id":  "run-test",
			"epsilon_used": 0.05,
			"judge":        "code-reviewer",
			"ci_lower":     map[string]float64{"haiku": 0.8, "sonnet": 0.85, "opus": 0.9},
			"mean_costs":   map[string]float64{"haiku": 0.001, "sonnet": 0.003, "opus": 0.010},
			"pass_rates":   map[string]float64{"haiku": 0.85, "sonnet": 0.90, "opus": 0.92},
		},
	}
	data, _ := json.Marshal(rec)
	f, err := os.OpenFile(cfg.CandidatesFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open candidates: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(data, '\n'))
}

func alwaysPassJudge(judgeID, inputJSON, project string) (routing.JudgeResult, error) {
	return routing.JudgeResult{Pass: true, Notes: "mock pass"}, nil
}

func alwaysPassDispatch(agentID, task, tier, runID, project string) (routing.DispatchResult, error) {
	return routing.DispatchResult{Stdout: "ok", Cost: 0.001, DurationS: 1}, nil
}

// ---- (a) eval: missing agent-id ---------------------------------------------

func TestMR_Eval_MissingAgentID(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "missing <agent-id>") {
		t.Errorf("expected missing agent-id error; got %v", err)
	}
}

// ---- (b) eval: agent not found ----------------------------------------------

func TestMR_Eval_AgentNotFound(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "no-such-agent"
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error; got %v", err)
	}
}

// ---- (c) eval: self-judge forbidden -----------------------------------------

func TestMR_Eval_SelfJudgeForbidden(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "backend"
	mrSetupAgent(t, cfg, "backend", "sonnet", 6)
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "self-evaluation is forbidden") {
		t.Errorf("expected self-eval error; got %v", err)
	}
}

// ---- (d) eval: too few cases ------------------------------------------------

func TestMR_Eval_TooFewCases(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	mrSetupAgent(t, cfg, "backend", "sonnet", 3)
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "minimum is") {
		t.Errorf("expected minimum-cases error; got %v", err)
	}
	data, _ := os.ReadFile(cfg.EvalLog)
	if !strings.Contains(string(data), "candidate_refused") {
		t.Errorf("expected candidate_refused in log; got %q", string(data))
	}
}

// ---- (e) eval: CI gate candidate emitted ------------------------------------

func TestMR_Eval_CIGate_CandidateEmitted(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	// 15 cases; opus agent; opus fails 3/15 (rate=0.8); haiku CI ≈ 0.796 >= 0.8-0.05=0.75 → emitted.
	mrSetupAgent(t, cfg, "backend", "opus", 15)
	lastTier := ""
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (routing.DispatchResult, error) {
		lastTier = tier
		return routing.DispatchResult{Stdout: "ok", Cost: 0.001, DurationS: 1}, nil
	}
	opusFails := 0
	cfg.JudgeFn = func(judgeID, inputJSON, project string) (routing.JudgeResult, error) {
		if lastTier == "opus" && opusFails < 3 {
			opusFails++
			return routing.JudgeResult{Pass: false}, nil
		}
		return routing.JudgeResult{Pass: true}, nil
	}

	res, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.CandidateEmitted {
		t.Errorf("expected candidate emitted; reason=%q", res.CandidateReason)
	}
}

// ---- (f) eval: budget cap ---------------------------------------------------

func TestMR_Eval_BudgetCap(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	cfg.MaxCostUSD = 0.004
	mrSetupAgent(t, cfg, "backend", "opus", 5)
	cfg.DispatchFn = func(agentID, task, tier, runID, project string) (routing.DispatchResult, error) {
		return routing.DispatchResult{Stdout: "ok", Cost: 0.003, DurationS: 1}, nil
	}
	cfg.JudgeFn = alwaysPassJudge

	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := os.ReadFile(cfg.EvalLog)
	if !strings.Contains(string(data), "budget_exceeded") {
		t.Errorf("expected budget_exceeded in log")
	}
	if !strings.Contains(mrOut(cfg), "WARN: budget cap") {
		t.Errorf("expected budget warning in output; got %q", mrOut(cfg))
	}
}

// ---- (g) eval: run started + finished written --------------------------------

func TestMR_Eval_LogStartedAndFinished(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	mrSetupAgent(t, cfg, "backend", "opus", 5)
	cfg.DispatchFn = alwaysPassDispatch
	cfg.JudgeFn = alwaysPassJudge

	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := os.ReadFile(cfg.EvalLog)
	for _, want := range []string{"eval_run_started", "eval_run_finished"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("expected %q in eval log", want)
		}
	}
}

// ---- (h) eval: eval_case records × 3 tiers ----------------------------------

func TestMR_Eval_CaseRecordsPerTier(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "eval"
	cfg.AgentID = "backend"
	cfg.Judge = "code-reviewer"
	mrSetupAgent(t, cfg, "backend", "opus", 5) // 5 cases × 4 tiers = 20 eval_case records
	cfg.DispatchFn = alwaysPassDispatch
	cfg.JudgeFn = alwaysPassJudge

	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, _ := os.ReadFile(cfg.EvalLog)
	count := strings.Count(string(data), `"type":"eval_case"`)
	if count != 20 {
		t.Errorf("expected 20 eval_case records (5 cases × 4 tiers); got %d", count)
	}
}

// ---- (i) list: no candidates file ------------------------------------------

func TestMR_List_NoCandidatesFile(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "list"
	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(mrOut(cfg), "no candidates yet") {
		t.Errorf("expected no-candidates advisory; got %q", mrOut(cfg))
	}
}

// ---- (j) list: latest-per-agent semantics -----------------------------------

func TestMR_List_LatestPerAgent(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "list"
	mrWriteCandidate(t, cfg, "backend", "opus", "sonnet", "2026-01-01T00:00:00Z")
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")

	res, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ListCount != 1 {
		t.Errorf("ListCount = %d; want 1", res.ListCount)
	}
	if !strings.Contains(mrOut(cfg), "haiku") {
		t.Errorf("expected latest (haiku) in output; got %q", mrOut(cfg))
	}
}

// ---- (k) show: missing agent-id ---------------------------------------------

func TestMR_Show_MissingAgentID(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "show"
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "missing <agent-id>") {
		t.Errorf("expected missing agent-id; got %v", err)
	}
}

// ---- (l) show: no candidate advisory ----------------------------------------

func TestMR_Show_NoCandidate(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "show"
	cfg.AgentID = "ghost"
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")
	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(mrOut(cfg), "no candidate for agent") {
		t.Errorf("expected no-candidate advisory; got %q", mrOut(cfg))
	}
}

// ---- (m) show: renders candidate + history header ---------------------------

func TestMR_Show_RendersCandidate(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "show"
	cfg.AgentID = "backend"
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")
	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := mrOut(cfg)
	if !strings.Contains(out, "=== candidate: backend ===") {
		t.Errorf("expected candidate header; got %q", out)
	}
	if !strings.Contains(out, "=== last 3 eval runs for backend ===") {
		t.Errorf("expected eval-runs header; got %q", out)
	}
}

// ---- (n) promote: framework guard -------------------------------------------

func TestMR_Promote_FrameworkGuard(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "promote"
	cfg.AgentID = "backend"
	cfg.Global = false
	mrWriteAgent(t, filepath.Join(cfg.YakosRoot, "lib", "agents"), "backend", "opus")
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "--global") {
		t.Errorf("expected --global required error; got %v", err)
	}
}

// ---- (o) promote: rewrites frontmatter + backup + history -------------------

func TestMR_Promote_WritesAll(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "promote"
	cfg.AgentID = "backend"
	cfg.Global = true
	agentPath := mrWriteAgent(t, filepath.Join(cfg.YakosRoot, "lib", "agents"), "backend", "opus")
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")

	res, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PromoteFrom != "opus" || res.PromoteTo != "haiku" {
		t.Errorf("PromoteFrom=%q PromoteTo=%q; want opus→haiku", res.PromoteFrom, res.PromoteTo)
	}

	// Frontmatter rewritten.
	data, _ := os.ReadFile(agentPath)
	if !strings.Contains(string(data), "model: haiku") {
		t.Errorf("expected model: haiku; got %q", string(data))
	}

	// Backup exists.
	entries, _ := os.ReadDir(cfg.BackupsDir)
	if len(entries) == 0 {
		t.Error("expected backup file")
	}

	// History written.
	hist, _ := os.ReadFile(cfg.HistoryFile)
	if !strings.Contains(string(hist), "promoted") {
		t.Errorf("expected promoted in history; got %q", string(hist))
	}
}

// ---- (p) promote: validation failure restores backup -------------------------

func TestMR_Promote_ValidationFailureRestores(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "promote"
	cfg.AgentID = "backend"
	cfg.Global = true
	agentPath := mrWriteAgent(t, filepath.Join(cfg.YakosRoot, "lib", "agents"), "backend", "opus")
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")
	cfg.ValidateFn = func(target string) error {
		return os.ErrInvalid
	}

	_, err := routing.Run(cfg)
	if err == nil {
		t.Fatal("expected validation failure")
	}
	// Original model should be restored.
	data, _ := os.ReadFile(agentPath)
	if !strings.Contains(string(data), "model: opus") {
		t.Errorf("expected opus restored; got %q", string(data))
	}
}

// ---- (q) reject: repeat-rejection guard -------------------------------------

func TestMR_Reject_RepeatGuard(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "reject"
	cfg.AgentID = "backend"
	for i := 0; i < 3; i++ {
		_ = appendToFile(cfg.GraveyardFile, `{"agent":"backend","suggested_model":"haiku","eval_run_id":"r","reason":"","by_user":"u"}`)
	}
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "rejected 3 times") {
		t.Errorf("expected repeat-rejection guard; got %v", err)
	}
}

// ---- (r) reject: --force bypasses guard -------------------------------------

func TestMR_Reject_ForceBypasses(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "reject"
	cfg.AgentID = "backend"
	cfg.Force = true
	for i := 0; i < 3; i++ {
		_ = appendToFile(cfg.GraveyardFile, `{"agent":"backend","suggested_model":"haiku","eval_run_id":"r","reason":"","by_user":"u"}`)
	}
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")
	_, err := routing.Run(cfg)
	if err != nil {
		t.Errorf("--force should bypass guard; got %v", err)
	}
}

// ---- (s) reject: graveyard + strip candidate --------------------------------

func TestMR_Reject_GraveyardAndStrip(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "reject"
	cfg.AgentID = "backend"
	cfg.Note = "not ready"
	mrWriteCandidate(t, cfg, "backend", "opus", "haiku", "2026-06-01T00:00:00Z")

	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	grave, _ := os.ReadFile(cfg.GraveyardFile)
	if !strings.Contains(string(grave), "backend") || !strings.Contains(string(grave), "not ready") {
		t.Errorf("expected graveyard entry; got %q", string(grave))
	}

	cands, _ := os.ReadFile(cfg.CandidatesFile)
	if strings.Contains(string(cands), "backend") {
		t.Errorf("expected candidate stripped; got %q", string(cands))
	}
}

// ---- (t) history: descending sort + agent filter ----------------------------

func TestMR_History_SortAndFilter(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "history"
	cfg.FilterAgent = "backend"
	// Older record: opus → sonnet (2026-01)
	_ = appendToFile(cfg.HistoryFile, `{"ts":"2026-01-01T00:00:00Z","action":"promoted","agent":"backend","from_model":"opus","to_model":"sonnet","eval_run_id":"r1","by_user":"u"}`)
	// Newer record: sonnet → haiku (2026-06)
	_ = appendToFile(cfg.HistoryFile, `{"ts":"2026-06-01T00:00:00Z","action":"promoted","agent":"backend","from_model":"sonnet","to_model":"haiku","eval_run_id":"r2","by_user":"u"}`)
	// Different agent — should be filtered out
	_ = appendToFile(cfg.HistoryFile, `{"ts":"2026-06-01T00:00:00Z","action":"promoted","agent":"frontend","from_model":"opus","to_model":"haiku","eval_run_id":"r3","by_user":"u"}`)

	_, err := routing.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := mrOut(cfg)
	if strings.Contains(out, "frontend") {
		t.Errorf("frontend should be filtered; got %q", out)
	}
	// Newest first: 2026-06 (sonnet→haiku) before 2026-01 (opus→sonnet).
	// The 2026-06 row contains "sonnet -> haiku"; the 2026-01 row contains "opus -> sonnet".
	// Check that the 2026-06 timestamp appears before the 2026-01 timestamp.
	idx2026_06 := strings.Index(out, "2026-06-01")
	idx2026_01 := strings.Index(out, "2026-01-01")
	if idx2026_06 == -1 || idx2026_01 == -1 {
		t.Fatalf("expected both timestamps in output; got:\n%s", out)
	}
	if idx2026_06 >= idx2026_01 {
		t.Errorf("expected 2026-06 (newer) before 2026-01 (older); got:\n%s", out)
	}
}

// ---- (u) unknown subcommand -------------------------------------------------

func TestMR_UnknownSubcommand(t *testing.T) {
	cfg := newMRConfig(t)
	cfg.Subcommand = "quantum-leap"
	_, err := routing.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error; got %v", err)
	}
}

// ---- (v) portedCommands entry -----------------------------------------------

func TestMR_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "model-routing" {
			return
		}
	}
	t.Error("expected 'model-routing' in portedCommands")
}

// ---- (w) help text ----------------------------------------------------------

func TestMR_HelpText(t *testing.T) {
	var buf bytes.Buffer
	routing.PrintHelp(&buf)
	out := buf.String()
	for _, want := range []string{
		"eval", "list", "show", "promote", "reject", "history",
		"--judge", "--max-cost-usd", "--cases", "--global", "--force", "--note",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

// ---- (x) WilsonLower parity with bash awk (20 cases) -----------------------
//
// Expected values computed with the bash awk formula from model-routing.sh:
//
//	awk -v k=K -v n=N 'BEGIN{if(n==0){printf "0";exit};phat=k/n;z=1.96;denom=1+z*z/n;
//	  center=(phat+z*z/(2*n))/denom;var=phat*(1-phat)/n+z*z/(4*n*n);if(var<0)var=0;
//	  margin=z*sqrt(var)/denom;lower=center-margin;if(lower<0)lower=0;printf "%.6f",lower}'

func TestMR_WilsonLower_Parity20Cases(t *testing.T) {
	// All expected values verified against the bash awk formula in model-routing.sh:
	// awk -v k=K -v n=N 'BEGIN{if(n==0){printf "0";exit};phat=k/n;z=1.96;
	//   denom=1+z*z/n;center=(phat+z*z/(2*n))/denom;var=phat*(1-phat)/n+z*z/(4*n*n);
	//   if(var<0)var=0;margin=z*sqrt(var)/denom;lower=center-margin;
	//   if(lower<0)lower=0;printf "%.6f",lower}'
	cases := []struct {
		k, n int
		want float64
	}{
		{0, 0, 0.000000},
		{0, 5, 0.000000},
		{0, 10, 0.000000},
		{0, 20, 0.000000},
		{1, 5, 0.036223},
		{2, 5, 0.117618},
		{3, 5, 0.230720},
		{4, 5, 0.375528},
		{5, 5, 0.565509},
		{3, 10, 0.107789},
		{5, 10, 0.236590},
		{7, 10, 0.396773},
		{8, 10, 0.490157},
		{10, 10, 0.722460},
		{6, 15, 0.198242},
		{10, 15, 0.417131},
		{12, 15, 0.548141},
		{15, 15, 0.796111},
		{3, 20, 0.052368},
		{15, 20, 0.531295},
	}
	for _, tc := range cases {
		got := routing.WilsonLower(tc.k, tc.n)
		if math.Abs(got-tc.want) > 0.0001 {
			t.Errorf("WilsonLower(%d,%d) = %.6f; want %.6f", tc.k, tc.n, got, tc.want)
		}
	}
}

// ---- helper: appendToFile ---------------------------------------------------

func appendToFile(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(line + "\n")
	return err
}
