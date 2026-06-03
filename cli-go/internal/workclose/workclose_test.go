package workclose

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

func newCfg(t *testing.T) Config {
	t.Helper()
	home := t.TempDir()
	return Config{
		HomeDir:   home,
		NoPrompt:  true,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		GitFn:     func(dir string, args ...string) (string, error) { return "", nil },
	}
}

func out(cfg Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func errOut(cfg Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

// logPath returns the resolved log path for cfg.
func logPath(cfg Config) string { return resolveLogPath(cfg) }

// writeScoredRecord appends a plan_scored record.
func writeScoredRecord(t *testing.T, path, planID string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
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
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// readOutcomeRecords reads plan_outcome records from path.
func readOutcomeRecords(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var records []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.Contains(line, `"plan_outcome"`) {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		records = append(records, m)
	}
	return records
}

// ---- plan_id resolution -----------------------------------------------------

func TestResolvePlanID_FromOverride(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "explicit-plan"
	id, err := resolvePlanID(cfg, "/nonexistent")
	if err != nil {
		t.Fatalf("resolvePlanID: %v", err)
	}
	if id != "explicit-plan" {
		t.Errorf("expected explicit-plan; got %q", id)
	}
}

func TestResolvePlanID_FromLog(t *testing.T) {
	cfg := newCfg(t)
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-from-log")
	id, err := resolvePlanID(cfg, lp)
	if err != nil {
		t.Fatalf("resolvePlanID: %v", err)
	}
	if id != "plan-from-log" {
		t.Errorf("expected plan-from-log; got %q", id)
	}
}

func TestResolvePlanID_LatestWins(t *testing.T) {
	cfg := newCfg(t)
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-first")
	writeScoredRecord(t, lp, "plan-latest")
	id, err := resolvePlanID(cfg, lp)
	if err != nil {
		t.Fatalf("resolvePlanID: %v", err)
	}
	if id != "plan-latest" {
		t.Errorf("expected plan-latest; got %q", id)
	}
}

func TestResolvePlanID_NoLog(t *testing.T) {
	cfg := newCfg(t)
	id, err := resolvePlanID(cfg, "/nonexistent/log.ndjson")
	if err != nil {
		t.Fatalf("resolvePlanID: %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id; got %q", id)
	}
}

// ---- Run: no plan_id --------------------------------------------------------

func TestRun_NoPlanID(t *testing.T) {
	cfg := newCfg(t)
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when no plan_id available")
	}
	if !strings.Contains(errOut(cfg), "no plan_scored record") {
		t.Errorf("expected advisory in stderr; got %q", errOut(cfg))
	}
}

// ---- Run: basic success -----------------------------------------------------

func TestRun_AppendsOutcomeRecord(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-001"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-001")

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.PlanID != "plan-001" {
		t.Errorf("expected plan-001; got %q", res.PlanID)
	}
	if res.RecordPath != lp {
		t.Errorf("expected record path %q; got %q", lp, res.RecordPath)
	}

	records := readOutcomeRecords(t, lp)
	if len(records) == 0 {
		t.Fatal("expected at least one plan_outcome record")
	}
	last := records[len(records)-1]
	if last["plan_id"] != "plan-001" {
		t.Errorf("expected plan_id=plan-001; got %v", last["plan_id"])
	}
	if last["type"] != "plan_outcome" {
		t.Errorf("expected type=plan_outcome; got %v", last["type"])
	}
}

func TestRun_RecordHasTimestamp(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-ts"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-ts")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	records := readOutcomeRecords(t, lp)
	if len(records) == 0 {
		t.Fatal("no records")
	}
	ts, ok := records[0]["ts"].(string)
	if !ok || ts == "" {
		t.Errorf("expected non-empty ts; got %v", records[0]["ts"])
	}
	if !strings.Contains(ts, "2026-06-02") {
		t.Errorf("expected injected timestamp; got %q", ts)
	}
}

func TestRun_PrintsWorkClosedSummary(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-summary"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-summary")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "Work closed for plan_id") {
		t.Errorf("expected summary header; got %q", o)
	}
	if !strings.Contains(o, "plan-summary") {
		t.Errorf("expected plan_id in summary; got %q", o)
	}
	if !strings.Contains(o, "files_touched") {
		t.Errorf("expected files_touched field; got %q", o)
	}
	if !strings.Contains(o, "plan_outcome appended") {
		t.Errorf("expected appended message; got %q", o)
	}
}

func TestRun_RecordHasReworkFailureModes(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-modes"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-modes")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	records := readOutcomeRecords(t, lp)
	if len(records) == 0 {
		t.Fatal("no records")
	}
	modes, ok := records[0]["rework_failure_modes"].([]interface{})
	if !ok {
		t.Fatalf("expected rework_failure_modes array; got %T", records[0]["rework_failure_modes"])
	}
	_ = modes // may be empty; just assert it's an array
}

// ---- Run: git stats injection -----------------------------------------------

func TestRun_GitStatsMocked(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-git"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-git")

	// Mock git to return a diff with 3 files.
	cfg.GitFn = func(dir string, args ...string) (string, error) {
		return "10\t2\tfile1.go\n5\t1\tfile2.go\n3\t0\tfile3.go\n", nil
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FilesTouched == nil || *res.FilesTouched != 3 {
		t.Errorf("expected 3 files_touched; got %v", res.FilesTouched)
	}
	if res.LinesChanged == nil || *res.LinesChanged != 21 {
		t.Errorf("expected 21 lines_changed (10+2+5+1+3+0); got %v", res.LinesChanged)
	}
}

func TestRun_GitStatsNullOnFailure(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-nogit"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-nogit")

	// Git fails.
	cfg.GitFn = func(dir string, args ...string) (string, error) {
		return "", fmt.Errorf("no git repo")
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FilesTouched != nil {
		t.Errorf("expected nil files_touched on git failure; got %v", *res.FilesTouched)
	}
}

// ---- Run: scope creep -------------------------------------------------------

func TestRun_ScopeCreepRatio(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-scope"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-scope")

	// Write plan.md with estimate.
	projDir := t.TempDir()
	cfg.ProjectDir = projDir
	planMD := filepath.Join(projDir, "work", "current", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planMD), 0755); err != nil {
		t.Fatal(err)
	}
	content := "## Overview\n\n## Tasks\nfiles_touched_estimate: 5\n\n## Notes\n"
	if err := os.WriteFile(planMD, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Git returns 10 files.
	cfg.GitFn = func(dir string, args ...string) (string, error) {
		lines := make([]string, 10)
		for i := range lines {
			lines[i] = "1\t0\tfile.go"
		}
		return strings.Join(lines, "\n") + "\n", nil
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ScopeCreepRatio == nil {
		t.Fatal("expected non-nil scope_creep_ratio")
	}
	// 10/5 = 2.0
	if *res.ScopeCreepRatio != 2.0 {
		t.Errorf("expected scope_creep_ratio=2.0; got %.2f", *res.ScopeCreepRatio)
	}
}

func TestRun_ScopeCreepNullWhenNoEstimate(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-noest"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-noest")
	cfg.GitFn = func(dir string, args ...string) (string, error) {
		return "1\t0\tfile.go\n", nil
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ScopeCreepRatio != nil {
		t.Errorf("expected nil scope_creep_ratio; got %v", *res.ScopeCreepRatio)
	}
}

// ---- Run: dispatch stats ----------------------------------------------------

func TestRun_DispatchStatsSummed(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-dispatch"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-dispatch")

	// Write a mock dispatch log.
	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	lines := []map[string]interface{}{
		{"ts": "2026-06-02T11:00:00Z", "agent": "backend", "tokens_total": 1000, "cost_usd": 0.01},
		{"ts": "2026-06-02T11:30:00Z", "agent": "frontend", "tokens_total": 2000, "cost_usd": 0.02},
	}
	f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	for _, l := range lines {
		b, _ := json.Marshal(l)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CostUSD == nil {
		t.Fatal("expected non-nil cost_usd")
	}
	// Should be approximately 0.01 + 0.02 = 0.03.
	if *res.CostUSD < 0.029 || *res.CostUSD > 0.031 {
		t.Errorf("expected cost≈0.03; got %.4f", *res.CostUSD)
	}
}

func TestRun_DispatchStatsNullWhenNoLog(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-nodisp"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-nodisp")

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CostUSD != nil {
		t.Errorf("expected nil cost_usd when dispatch log absent; got %v", *res.CostUSD)
	}
}

func TestRun_DispatchSessionFilter(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-sess"
	cfg.SessionStartTS = "2026-06-02T11:00:00Z"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-sess")

	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	lines := []map[string]interface{}{
		// Before session start → should be excluded.
		{"ts": "2026-06-01T09:00:00Z", "agent": "old", "tokens_total": 9999, "cost_usd": 9.99},
		// After session start → included.
		{"ts": "2026-06-02T11:30:00Z", "agent": "new", "tokens_total": 500, "cost_usd": 0.005},
	}
	f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	for _, l := range lines {
		b, _ := json.Marshal(l)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CostUSD == nil {
		t.Fatal("expected cost_usd")
	}
	// Only the session record should be counted (cost≈0.005).
	if *res.CostUSD > 0.01 {
		t.Errorf("expected cost≈0.005 (session-filtered); got %.4f", *res.CostUSD)
	}
}

// ---- Run: rework cycles -----------------------------------------------------

func TestRun_ReworkCyclesNoDispatchLog(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-rework"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-rework")

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No dispatch log → rework is nil.
	if res.ReworkCycles != nil {
		t.Errorf("expected nil rework_cycles; got %v", *res.ReworkCycles)
	}
}

func TestRun_ReworkCyclesDetected(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-rework2"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-rework2")

	// Write dispatch log with repeated agent.
	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	lines := []map[string]interface{}{
		{"agent": "backend"},
		{"agent": "backend"},
		{"agent": "frontend"},
	}
	f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	for _, l := range lines {
		b, _ := json.Marshal(l)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ReworkCycles == nil {
		t.Fatal("expected non-nil rework_cycles")
	}
	// backend appeared twice → 1 rework cycle.
	if *res.ReworkCycles != 1 {
		t.Errorf("expected 1 rework cycle; got %d", *res.ReworkCycles)
	}
}

// ---- Run: first_try_pass ----------------------------------------------------

func TestRun_FirstTryPass_True(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-ftp"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-ftp")

	// Dispatch log with no repeated agents.
	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	lines := []map[string]interface{}{
		{"agent": "backend"},
		{"agent": "frontend"},
	}
	f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	for _, l := range lines {
		b, _ := json.Marshal(l)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FirstTryPass == nil {
		t.Fatal("expected non-nil first_try_pass")
	}
	if !*res.FirstTryPass {
		t.Errorf("expected first_try_pass=true; got false")
	}
}

func TestRun_FirstTryPass_False_Rework(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-ftp-false"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-ftp-false")

	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	lines := []map[string]interface{}{
		{"agent": "backend"},
		{"agent": "backend"}, // re-dispatch
	}
	f, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	for _, l := range lines {
		b, _ := json.Marshal(l)
		_, _ = f.Write(append(b, '\n'))
	}
	_ = f.Close()

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FirstTryPass == nil {
		t.Fatal("expected non-nil first_try_pass")
	}
	if *res.FirstTryPass {
		t.Errorf("expected first_try_pass=false (rework); got true")
	}
}

func TestRun_FirstTryPass_False_BlockOverridden(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-override"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-override")

	// Append a plan_score_override record for this plan_id.
	overrideRec := map[string]interface{}{
		"type":    "plan_score_override",
		"ts":      "2026-06-01T11:00:00Z",
		"plan_id": "plan-override",
		"reason":  "operator override",
		"user":    "test",
	}
	line, _ := json.Marshal(overrideRec)
	f, _ := os.OpenFile(lp, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()

	// Dispatch log with no rework.
	dispLog := filepath.Join(cfg.HomeDir, ".yakos-state", "dispatch-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(dispLog), 0755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]interface{}{"agent": "backend"})
	fDisp, _ := os.OpenFile(dispLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	_, _ = fDisp.Write(append(b, '\n'))
	_ = fDisp.Close()

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FirstTryPass == nil {
		t.Fatal("expected non-nil first_try_pass")
	}
	if *res.FirstTryPass {
		t.Errorf("expected first_try_pass=false (block_overridden); got true")
	}
}

// ---- Run: human_signal injection -------------------------------------------

func TestRun_HumanSignal_Satisfied(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-hs"
	cfg.NoPrompt = false
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-hs")

	answers := []string{"y", "looks great"}
	idx := 0
	cfg.PromptFn = func(prompt string) (string, error) {
		a := answers[idx]
		idx++
		return a, nil
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	records := readOutcomeRecords(t, lp)
	if len(records) == 0 {
		t.Fatal("no records")
	}
	hs, ok := records[0]["human_signal"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected human_signal object; got %T", records[0]["human_signal"])
	}
	if hs["satisfied"] != true {
		t.Errorf("expected satisfied=true; got %v", hs["satisfied"])
	}
	if hs["note"] != "looks great" {
		t.Errorf("expected note='looks great'; got %v", hs["note"])
	}
}

func TestRun_HumanSignal_Skipped(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-noprompt"
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-noprompt")

	answers := []string{"skip", ""}
	idx := 0
	cfg.NoPrompt = false
	cfg.PromptFn = func(prompt string) (string, error) {
		a := answers[idx]
		idx++
		return a, nil
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	records := readOutcomeRecords(t, lp)
	if len(records) == 0 {
		t.Fatal("no records")
	}
	if records[0]["human_signal"] != nil {
		t.Errorf("expected null human_signal for 'skip'; got %v", records[0]["human_signal"])
	}
}

func TestRun_NoPrompt_HumanSignalNull(t *testing.T) {
	cfg := newCfg(t)
	cfg.PlanID = "plan-np"
	cfg.NoPrompt = true
	lp := logPath(cfg)
	writeScoredRecord(t, lp, "plan-np")

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	records := readOutcomeRecords(t, lp)
	if len(records) == 0 {
		t.Fatal("no records")
	}
	if records[0]["human_signal"] != nil {
		t.Errorf("expected null human_signal with --no-prompt; got %v", records[0]["human_signal"])
	}
}

// ---- Plan estimate parsing --------------------------------------------------

func TestParsePlanEstimate_Found(t *testing.T) {
	dir := t.TempDir()
	planMD := filepath.Join(dir, "work", "current", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planMD), 0755); err != nil {
		t.Fatal(err)
	}
	content := "## Overview\n\n## Tasks\nfiles_touched_estimate: 7\n\n## Risks\n"
	if err := os.WriteFile(planMD, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got := parsePlanEstimate(dir)
	if got == nil || *got != 7 {
		t.Errorf("expected 7; got %v", got)
	}
}

func TestParsePlanEstimate_Multiple(t *testing.T) {
	dir := t.TempDir()
	planMD := filepath.Join(dir, "work", "current", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planMD), 0755); err != nil {
		t.Fatal(err)
	}
	content := "## Tasks\nfiles_touched_estimate: 3\nfiles_touched_estimate: 4\n## Risks\n"
	if err := os.WriteFile(planMD, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got := parsePlanEstimate(dir)
	if got == nil || *got != 7 {
		t.Errorf("expected sum=7; got %v", got)
	}
}

func TestParsePlanEstimate_NoPlanFile(t *testing.T) {
	dir := t.TempDir()
	got := parsePlanEstimate(dir)
	if got != nil {
		t.Errorf("expected nil; got %v", *got)
	}
}

func TestParsePlanEstimate_NoEstimate(t *testing.T) {
	dir := t.TempDir()
	planMD := filepath.Join(dir, "work", "current", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planMD), 0755); err != nil {
		t.Fatal(err)
	}
	content := "## Tasks\nDo something\n## Risks\n"
	if err := os.WriteFile(planMD, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got := parsePlanEstimate(dir)
	if got != nil {
		t.Errorf("expected nil; got %v", *got)
	}
}

func TestParsePlanEstimate_OutsideTasks(t *testing.T) {
	dir := t.TempDir()
	planMD := filepath.Join(dir, "work", "current", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planMD), 0755); err != nil {
		t.Fatal(err)
	}
	// Estimate is in Risks section, not Tasks.
	content := "## Risks\nfiles_touched_estimate: 99\n## Tasks\nDo something\n"
	if err := os.WriteFile(planMD, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got := parsePlanEstimate(dir)
	if got != nil {
		t.Errorf("expected nil (outside Tasks); got %v", *got)
	}
}

// ---- scope creep ------------------------------------------------------------

func TestComputeScopeCreep_Normal(t *testing.T) {
	f := 10
	e := 5
	r := computeScopeCreep(&f, &e)
	if r == nil || *r != 2.0 {
		t.Errorf("expected 2.0; got %v", r)
	}
}

func TestComputeScopeCreep_NilTouched(t *testing.T) {
	e := 5
	if computeScopeCreep(nil, &e) != nil {
		t.Error("expected nil when filesTouched is nil")
	}
}

func TestComputeScopeCreep_ZeroEstimate(t *testing.T) {
	f := 10
	e := 0
	if computeScopeCreep(&f, &e) != nil {
		t.Error("expected nil when estimate is 0")
	}
}

// ---- formatters -------------------------------------------------------------

func TestFormatNullableInt_Nil(t *testing.T) {
	if formatNullableInt(nil) != "null" {
		t.Error("expected null for nil int")
	}
}

func TestFormatNullableInt_Value(t *testing.T) {
	v := 42
	if formatNullableInt(&v) != "42" {
		t.Errorf("expected 42; got %q", formatNullableInt(&v))
	}
}

func TestFormatNullableBool_Nil(t *testing.T) {
	if formatNullableBool(nil) != "null" {
		t.Error("expected null for nil bool")
	}
}

func TestFormatNullableBool_True(t *testing.T) {
	v := true
	if formatNullableBool(&v) != "true" {
		t.Error("expected true")
	}
}

func TestFormatNullableFloat_Nil(t *testing.T) {
	if formatNullableFloat(nil) != "null" {
		t.Error("expected null for nil float")
	}
}

func TestFormatNullableFloat_Value(t *testing.T) {
	v := 0.0123
	if formatNullableFloat(&v) != "0.0123" {
		t.Errorf("expected 0.0123; got %q", formatNullableFloat(&v))
	}
}

// ---- PrintHelp --------------------------------------------------------------

func TestPrintHelp(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	o := buf.String()
	for _, want := range []string{"--plan-id", "--no-prompt", "--project", "plan_outcome", "plan-quality-log"} {
		if !strings.Contains(o, want) {
			t.Errorf("help missing %q; got:\n%s", want, o)
		}
	}
}

// ---- resolveDispatchLogPath -------------------------------------------------

func TestResolveDispatchLogPath_CfgOverride(t *testing.T) {
	cfg := Config{DispatchLog: "/custom/dispatch.ndjson"}
	got := resolveDispatchLogPath(cfg)
	if got != "/custom/dispatch.ndjson" {
		t.Errorf("expected custom path; got %q", got)
	}
}

func TestResolveDispatchLogPath_Default(t *testing.T) {
	cfg := Config{HomeDir: "/home/test"}
	got := resolveDispatchLogPath(cfg)
	if !strings.Contains(got, "dispatch-log.ndjson") {
		t.Errorf("expected dispatch-log.ndjson in path; got %q", got)
	}
}
