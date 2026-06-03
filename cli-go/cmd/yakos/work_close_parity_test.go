// work_close_parity_test.go — Phase 1 parity tests for `yakos work close`.
//
// Design notes:
//
//  1. All tests call workclose.Run directly with injected deps; no bash runtime
//     or real ~/.yakos-state directory is required.
//
//  2. The bash work-close.sh captures outcome telemetry (git diff, dispatch-log
//     stats, rework) and appends a plan_outcome record to the quality log.
//
//  3. Parity is verified behaviourally (output shape, filesystem effects, error
//     conditions) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) no plan_id → error + advisory
//	(b) plan_id from log → resolved automatically
//	(c) plan_id from --plan-id override
//	(d) appends plan_outcome record
//	(e) record contains correct type/ts/plan_id fields
//	(f) git stats injected via GitFn
//	(g) git failure → null fields (non-blocking)
//	(h) scope_creep_ratio from plan.md estimate
//	(i) dispatch log summed correctly
//	(j) session filter applied to dispatch log
//	(k) rework cycles detected from repeated agents
//	(l) first_try_pass=true when 0 rework + no failure modes
//	(m) first_try_pass=false when rework present
//	(n) human_signal captured via PromptFn
//	(o) human_signal=null when --no-prompt
//	(p) rework_failure_modes is always an array
//	(q) summary printed to stdout
//	(r) work close entry in portedCommands
//	(s) help text has required fields
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/workclose"
)

// ---- helpers ----------------------------------------------------------------

func newWorkCloseCfg(t *testing.T) workclose.Config {
	t.Helper()
	home := t.TempDir()
	return workclose.Config{
		HomeDir:   home,
		NoPrompt:  true,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		GitFn:     func(dir string, args ...string) (string, error) { return "", nil },
	}
}

func wcOut(cfg workclose.Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func wcErrOut(cfg workclose.Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

func wcLogPath(cfg workclose.Config) string {
	if cfg.PlanQualityLog != "" {
		return cfg.PlanQualityLog
	}
	return filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
}

func writePlanScoredForWC(t *testing.T, path, planID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	rec := map[string]interface{}{
		"type":            "plan_scored",
		"ts":              "2026-06-01T10:00:00Z",
		"plan_id":         planID,
		"verdict":         "PASS",
		"aggregate_score": 0.82,
		"dissent":         false,
		"cost_usd":        0.01,
	}
	line, _ := json.Marshal(rec)
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

func readAllOutcomes(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var results []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.Contains(line, `"plan_outcome"`) {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		results = append(results, m)
	}
	return results
}

// ---- (a) no plan_id → error -------------------------------------------------

func TestWorkClose_NoPlanID(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	_, err := workclose.Run(cfg)
	if err == nil {
		t.Error("expected error when no plan_id available")
	}
	if !strings.Contains(wcErrOut(cfg), "no plan_scored record") {
		t.Errorf("expected advisory; got %q", wcErrOut(cfg))
	}
}

// ---- (b) plan_id from log ---------------------------------------------------

func TestWorkClose_PlanIDFromLog(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "log-plan")

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PlanID != "log-plan" {
		t.Errorf("expected log-plan; got %q", res.PlanID)
	}
}

// ---- (c) plan_id from override ----------------------------------------------

func TestWorkClose_PlanIDOverride(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "explicit-id"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "other-id")

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PlanID != "explicit-id" {
		t.Errorf("expected explicit-id; got %q", res.PlanID)
	}
}

// ---- (d) appends plan_outcome record ----------------------------------------

func TestWorkClose_AppendsRecord(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-append"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-append")

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RecordPath != lp {
		t.Errorf("expected RecordPath=%q; got %q", lp, res.RecordPath)
	}
	outcomes := readAllOutcomes(t, lp)
	if len(outcomes) == 0 {
		t.Fatal("no plan_outcome records found")
	}
}

// ---- (e) record fields ------------------------------------------------------

func TestWorkClose_RecordFields(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-fields"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-fields")

	_, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	outcomes := readAllOutcomes(t, lp)
	if len(outcomes) == 0 {
		t.Fatal("no records")
	}
	rec := outcomes[0]
	if rec["type"] != "plan_outcome" {
		t.Errorf("expected type=plan_outcome; got %v", rec["type"])
	}
	if rec["plan_id"] != "plan-fields" {
		t.Errorf("expected plan_id=plan-fields; got %v", rec["plan_id"])
	}
	ts, ok := rec["ts"].(string)
	if !ok || !strings.Contains(ts, "2026-06-02") {
		t.Errorf("expected injected ts; got %v", rec["ts"])
	}
}

// ---- (f) git stats via GitFn ------------------------------------------------

func TestWorkClose_GitStats(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-git"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-git")

	cfg.GitFn = func(dir string, args ...string) (string, error) {
		return "5\t2\ta.go\n3\t1\tb.go\n", nil
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FilesTouched == nil || *res.FilesTouched != 2 {
		t.Errorf("expected 2 files; got %v", res.FilesTouched)
	}
	if res.LinesChanged == nil || *res.LinesChanged != 11 {
		t.Errorf("expected 11 lines (5+2+3+1); got %v", res.LinesChanged)
	}
}

// ---- (g) git failure → null (non-blocking) ----------------------------------

func TestWorkClose_GitFailure_NullFields(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-nogit"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-nogit")

	cfg.GitFn = func(dir string, args ...string) (string, error) {
		return "", fmt.Errorf("not a git repo")
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err) // must not fail
	}
	if res.FilesTouched != nil {
		t.Errorf("expected nil files_touched; got %v", *res.FilesTouched)
	}
}

// ---- (h) scope_creep_ratio from plan.md -------------------------------------

func TestWorkClose_ScopeCreep(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-scope"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-scope")

	projDir := t.TempDir()
	cfg.ProjectDir = projDir
	planMD := filepath.Join(projDir, "work", "current", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planMD), 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(planMD, []byte("## Tasks\nfiles_touched_estimate: 4\n## Risks\n"), 0644)

	cfg.GitFn = func(dir string, args ...string) (string, error) {
		return "1\t0\tf1\n1\t0\tf2\n1\t0\tf3\n1\t0\tf4\n1\t0\tf5\n1\t0\tf6\n1\t0\tf7\n1\t0\tf8\n", nil
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ScopeCreepRatio == nil {
		t.Fatal("expected non-nil scope_creep_ratio")
	}
	// 8 actual / 4 estimated = 2.0
	if *res.ScopeCreepRatio != 2.0 {
		t.Errorf("expected 2.0; got %.2f", *res.ScopeCreepRatio)
	}
}

// ---- (i) dispatch log summed ------------------------------------------------

func TestWorkClose_DispatchStats(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-disp"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-disp")

	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []map[string]interface{}{
		{"ts": "2026-06-02T10:00:00Z", "agent": "a", "tokens_total": 1000, "cost_usd": 0.01},
		{"ts": "2026-06-02T10:30:00Z", "agent": "b", "tokens_total": 2000, "cost_usd": 0.02},
	} {
		line, _ := json.Marshal(entry)
		f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CostUSD == nil {
		t.Fatal("expected cost_usd")
	}
	if *res.CostUSD < 0.029 || *res.CostUSD > 0.031 {
		t.Errorf("expected ~0.03; got %.4f", *res.CostUSD)
	}
}

// ---- (j) session filter on dispatch log ------------------------------------

func TestWorkClose_DispatchSessionFilter(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-sess"
	cfg.SessionStartTS = "2026-06-02T10:00:00Z"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-sess")

	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []map[string]interface{}{
		// Before session start — must be excluded.
		{"ts": "2026-06-01T09:00:00Z", "agent": "x", "tokens_total": 9999, "cost_usd": 9.99},
		// After session start — included.
		{"ts": "2026-06-02T11:00:00Z", "agent": "y", "tokens_total": 100, "cost_usd": 0.001},
	} {
		line, _ := json.Marshal(entry)
		f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CostUSD != nil && *res.CostUSD > 0.01 {
		t.Errorf("expected filtered cost (≤0.01); got %.4f", *res.CostUSD)
	}
}

// ---- (k) rework cycles ------------------------------------------------------

func TestWorkClose_ReworkCycles(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-rew"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-rew")

	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	// backend appears 3× → 2 rework cycles.
	for _, entry := range []map[string]interface{}{
		{"agent": "backend"},
		{"agent": "backend"},
		{"agent": "backend"},
		{"agent": "frontend"},
	} {
		line, _ := json.Marshal(entry)
		f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ReworkCycles == nil {
		t.Fatal("expected non-nil rework_cycles")
	}
	if *res.ReworkCycles != 2 {
		t.Errorf("expected 2; got %d", *res.ReworkCycles)
	}
}

// ---- (l) first_try_pass=true ------------------------------------------------

func TestWorkClose_FirstTryPass_True(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-ftp"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-ftp")

	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	// Distinct agents, no repeats.
	for _, entry := range []map[string]interface{}{
		{"agent": "backend"},
		{"agent": "frontend"},
	} {
		line, _ := json.Marshal(entry)
		f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FirstTryPass == nil || !*res.FirstTryPass {
		t.Errorf("expected first_try_pass=true; got %v", res.FirstTryPass)
	}
}

// ---- (m) first_try_pass=false when rework -----------------------------------

func TestWorkClose_FirstTryPass_FalseOnRework(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-rework"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-rework")

	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []map[string]interface{}{
		{"agent": "backend"},
		{"agent": "backend"},
	} {
		line, _ := json.Marshal(entry)
		f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	res, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FirstTryPass == nil || *res.FirstTryPass {
		t.Errorf("expected first_try_pass=false; got %v", res.FirstTryPass)
	}
}

// ---- (n) human_signal via PromptFn -----------------------------------------

func TestWorkClose_HumanSignal_Captured(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-hs"
	cfg.NoPrompt = false
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-hs")

	idx := 0
	answers := []string{"y", "great job"}
	cfg.PromptFn = func(prompt string) (string, error) {
		a := answers[idx]
		idx++
		return a, nil
	}

	_, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	outcomes := readAllOutcomes(t, lp)
	if len(outcomes) == 0 {
		t.Fatal("no outcomes")
	}
	hs, ok := outcomes[0]["human_signal"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected human_signal object; got %T", outcomes[0]["human_signal"])
	}
	if hs["satisfied"] != true {
		t.Errorf("expected satisfied=true; got %v", hs["satisfied"])
	}
	if hs["note"] != "great job" {
		t.Errorf("expected note='great job'; got %v", hs["note"])
	}
}

// ---- (o) human_signal=null with --no-prompt ---------------------------------

func TestWorkClose_HumanSignal_Null_NoPrompt(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-np"
	cfg.NoPrompt = true
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-np")

	_, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	outcomes := readAllOutcomes(t, lp)
	if len(outcomes) == 0 {
		t.Fatal("no outcomes")
	}
	if outcomes[0]["human_signal"] != nil {
		t.Errorf("expected null human_signal; got %v", outcomes[0]["human_signal"])
	}
}

// ---- (p) rework_failure_modes always an array ------------------------------

func TestWorkClose_ReworkFailureModes_AlwaysArray(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-modes"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-modes")

	_, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	outcomes := readAllOutcomes(t, lp)
	if len(outcomes) == 0 {
		t.Fatal("no outcomes")
	}
	_, ok := outcomes[0]["rework_failure_modes"].([]interface{})
	if !ok {
		t.Errorf("expected []interface{}; got %T", outcomes[0]["rework_failure_modes"])
	}
}

// ---- (q) summary printed to stdout ------------------------------------------

func TestWorkClose_SummaryPrinted(t *testing.T) {
	cfg := newWorkCloseCfg(t)
	cfg.PlanID = "plan-summary"
	lp := wcLogPath(cfg)
	writePlanScoredForWC(t, lp, "plan-summary")

	_, err := workclose.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := wcOut(cfg)
	for _, want := range []string{"Work closed for plan_id", "plan-summary", "files_touched", "plan_outcome appended"} {
		if !strings.Contains(o, want) {
			t.Errorf("expected %q in output; got %q", want, o)
		}
	}
}

// ---- (r) portedCommands entry -----------------------------------------------

func TestWorkClose_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "work close" {
			return
		}
	}
	t.Error("expected 'work close' in portedCommands")
}

// ---- (s) help text ----------------------------------------------------------

func TestWorkClose_HelpText(t *testing.T) {
	var buf bytes.Buffer
	workclose.PrintHelp(&buf)
	o := buf.String()
	for _, want := range []string{"--plan-id", "--no-prompt", "--project", "plan_outcome", "plan-quality-log"} {
		if !strings.Contains(o, want) {
			t.Errorf("help missing %q", want)
		}
	}
}
