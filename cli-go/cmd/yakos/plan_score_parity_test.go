// plan_score_parity_test.go — Phase 1 parity tests for `yakos plan score`.
//
// Design notes:
//
//  1. All tests call planscore.Run directly with temp-dir paths; no bash runtime
//     or real ~/.yakos-state directory is required.
//
//  2. The bash plan-score.sh manages plan quality records: show (pretty-print),
//     history (tabular), override (append + marker removal), correlate (stats).
//
//  3. Parity is verified behaviourally (output shape, filesystem effects, error
//     conditions) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) show: missing log → error advisory
//	(b) show: renders latest record
//	(c) show: plan_id filter
//	(d) history: empty log → advisory
//	(e) history: newest-first ordering
//	(f) history: --limit respected
//	(g) history: --project filter
//	(h) override: missing plan_id → error
//	(i) override: missing --reason → error
//	(j) override: no existing record → error
//	(k) override: appends record + output
//	(l) override: removes .plan-blocked marker (matching)
//	(m) override: leaves .plan-blocked marker alone (different plan_id)
//	(n) correlate: insufficient data → error
//	(o) correlate: sufficient data → report sections present
//	(p) unknown subcommand → EX_USAGE (64)
//	(q) plan score entry in portedCommands
//	(r) help text contains key phrases
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/planscore"
)

// ---- helpers ----------------------------------------------------------------

func newPlanScoreConfig(t *testing.T) planscore.Config {
	t.Helper()
	home := t.TempDir()
	return planscore.Config{
		HomeDir:   home,
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
	}
}

func psOut(cfg planscore.Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func psErrOut(cfg planscore.Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

func writePlanScoredRecord(t *testing.T, logPath, planID, verdict string, agg float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := map[string]interface{}{
		"type":            "plan_scored",
		"ts":              "2026-06-01T10:00:00Z",
		"plan_id":         planID,
		"verdict":         verdict,
		"aggregate_score": agg,
		"dissent":         false,
		"cost_usd":        0.012,
		"project":         "proj-x",
		"per_dimension_median": map[string]float64{
			"acceptance_criteria_specificity": 0.8,
			"assumption_surfacing":            0.7,
			"decomposition_granularity":       0.9,
			"dependency_clarity":              0.75,
			"domain_boundaries_respected":     0.85,
			"risk_rollback_honesty":           0.6,
		},
	}
	line, _ := json.Marshal(rec)
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

func writePlanOutcomeRecord(t *testing.T, logPath, planID string, ftp bool, sc float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := map[string]interface{}{
		"type":              "plan_outcome",
		"ts":                "2026-06-02T10:00:00Z",
		"plan_id":           planID,
		"project":           "proj-x",
		"first_try_pass":    ftp,
		"scope_creep_ratio": sc,
		"rework_cycles":     0,
	}
	line, _ := json.Marshal(rec)
	f, _ := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// ---- (a) show: missing log --------------------------------------------------

func TestPlanScore_Show_MissingLog(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "show"
	_, err := planscore.Run(cfg)
	if err == nil {
		t.Error("expected error for missing log")
	}
	if !strings.Contains(psErrOut(cfg), "No plan-quality-log") {
		t.Errorf("expected advisory; got %q", psErrOut(cfg))
	}
}

// ---- (b) show: renders latest -----------------------------------------------

func TestPlanScore_Show_RendersLatest(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "show"
	lp := cfg.PlanQualityLog
	if lp == "" {
		lp = filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	}
	writePlanScoredRecord(t, lp, "old-plan", "FAIL", 0.4)
	writePlanScoredRecord(t, lp, "new-plan", "PASS", 0.92)

	_, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := psOut(cfg)
	if !strings.Contains(o, "new-plan") {
		t.Errorf("expected newest record; got %q", o)
	}
	if strings.Contains(o, "old-plan") && !strings.Contains(o, "new-plan") {
		t.Errorf("expected new-plan to appear; got %q", o)
	}
}

// ---- (c) show: plan_id filter -----------------------------------------------

func TestPlanScore_Show_PlanIDFilter(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "show"
	cfg.PlanID = "specific-plan"
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	writePlanScoredRecord(t, lp, "specific-plan", "PASS", 0.85)
	writePlanScoredRecord(t, lp, "other-plan", "PASS", 0.90)

	_, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := psOut(cfg)
	if !strings.Contains(o, "specific-plan") {
		t.Errorf("expected specific-plan; got %q", o)
	}
}

// ---- (d) history: empty log advisory ----------------------------------------

func TestPlanScore_History_EmptyLog(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "history"
	_, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(psOut(cfg), "No records found") {
		t.Errorf("expected no-records advisory; got %q", psOut(cfg))
	}
}

// ---- (e) history: newest-first ordering ------------------------------------

func TestPlanScore_History_NewestFirst(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "history"
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")

	if err := os.MkdirAll(filepath.Dir(lp), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write records with timestamps in file order (old → new).
	for _, pair := range []struct{ id, ts string }{
		{"plan-alpha", "2026-01-01T00:00:00Z"},
		{"plan-beta", "2026-03-01T00:00:00Z"},
		{"plan-gamma", "2026-06-01T00:00:00Z"},
	} {
		rec := map[string]interface{}{
			"type": "plan_scored", "ts": pair.ts, "plan_id": pair.id,
			"verdict": "PASS", "aggregate_score": 0.8, "dissent": false, "cost_usd": 0.01,
		}
		line, _ := json.Marshal(rec)
		f, err := os.OpenFile(lp, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			t.Fatalf("open lp: %v", err)
		}
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	_, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := psOut(cfg)
	gIdx := strings.Index(o, "plan-gamma")
	bIdx := strings.Index(o, "plan-beta")
	aIdx := strings.Index(o, "plan-alpha")
	if gIdx == -1 || bIdx == -1 || aIdx == -1 {
		t.Fatalf("expected all plan IDs in output; got %q", o)
	}
	if gIdx >= bIdx || bIdx >= aIdx {
		t.Errorf("expected newest-first; gamma=%d beta=%d alpha=%d", gIdx, bIdx, aIdx)
	}
}

// ---- (f) history: --limit respected ----------------------------------------

func TestPlanScore_History_Limit(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "history"
	cfg.Limit = 2
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	for i := 0; i < 5; i++ {
		writePlanScoredRecord(t, lp, "plan-"+string(rune('0'+i)), "PASS", 0.8)
	}

	res, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RecordsFound != 2 {
		t.Errorf("expected 2 (limit); got %d", res.RecordsFound)
	}
}

// ---- (g) history: --project filter -----------------------------------------

func TestPlanScore_History_ProjectFilter(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "history"
	cfg.Project = "proj-x"
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	writePlanScoredRecord(t, lp, "plan-in", "PASS", 0.8)

	// Write record for a different project.
	rec := map[string]interface{}{
		"type": "plan_scored", "ts": "2026-06-01T10:00:00Z",
		"plan_id": "plan-out", "verdict": "PASS", "aggregate_score": 0.8,
		"dissent": false, "cost_usd": 0.01, "project": "proj-y",
	}
	line, _ := json.Marshal(rec)
	f, _ := os.OpenFile(lp, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()

	_, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := psOut(cfg)
	if strings.Contains(o, "plan-out") {
		t.Errorf("plan-out should be filtered; got %q", o)
	}
}

// ---- (h) override: missing plan_id ------------------------------------------

func TestPlanScore_Override_MissingPlanID(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "override"
	cfg.Reason = "ok"
	_, err := planscore.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "plan_id") {
		t.Errorf("expected plan_id error; got %v", err)
	}
}

// ---- (i) override: missing --reason -----------------------------------------

func TestPlanScore_Override_MissingReason(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-x"
	_, err := planscore.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Errorf("expected reason error; got %v", err)
	}
}

// ---- (j) override: no existing record ---------------------------------------

func TestPlanScore_Override_NoExistingRecord(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "ghost"
	cfg.Reason = "trust me"
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	writePlanScoredRecord(t, lp, "real-plan", "PASS", 0.8)

	_, err := planscore.Run(cfg)
	if err == nil {
		t.Error("expected error for missing record")
	}
	if !strings.Contains(psErrOut(cfg), "no existing record") {
		t.Errorf("expected no-existing-record message; got %q", psErrOut(cfg))
	}
}

// ---- (k) override: appends record -------------------------------------------

func TestPlanScore_Override_AppendsRecord(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	cfg.Reason = "operator reviewed and approved"
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	writePlanScoredRecord(t, lp, "plan-001", "FAIL", 0.3)

	res, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OverrideWritten {
		t.Error("expected OverrideWritten=true")
	}

	data, _ := os.ReadFile(lp)
	if !strings.Contains(string(data), "plan_score_override") {
		t.Errorf("expected override record in log; got %q", string(data))
	}
	o := psOut(cfg)
	if !strings.Contains(o, "Override recorded") {
		t.Errorf("expected confirmation; got %q", o)
	}
}

// ---- (l) override: removes .plan-blocked marker (matching) ------------------

func TestPlanScore_Override_RemovesBlockedMarker(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-002"
	cfg.Reason = "approved"
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	writePlanScoredRecord(t, lp, "plan-002", "FAIL", 0.3)

	curDir := t.TempDir()
	cfg.CurrentDir = curDir
	marker := filepath.Join(curDir, ".plan-blocked")
	_ = os.WriteFile(marker, []byte(`{"plan_id":"plan-002"}`), 0644)

	res, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.BlockedMarkerRemoved {
		t.Error("expected BlockedMarkerRemoved=true")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("expected marker to be removed")
	}
}

// ---- (m) override: different plan_id leaves marker --------------------------

func TestPlanScore_Override_LeavesDifferentMarker(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-003"
	cfg.Reason = "approved"
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	writePlanScoredRecord(t, lp, "plan-003", "FAIL", 0.3)

	curDir := t.TempDir()
	cfg.CurrentDir = curDir
	marker := filepath.Join(curDir, ".plan-blocked")
	_ = os.WriteFile(marker, []byte(`{"plan_id":"plan-999"}`), 0644)

	res, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BlockedMarkerRemoved {
		t.Error("expected BlockedMarkerRemoved=false (different plan_id)")
	}
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("marker should remain for different plan_id")
	}
}

// ---- (n) correlate: insufficient data ---------------------------------------

func TestPlanScore_Correlate_InsufficientData(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "correlate"
	cfg.MinN = 5
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	// 2 joined pairs < MinN=5.
	writePlanScoredRecord(t, lp, "p1", "PASS", 0.8)
	writePlanOutcomeRecord(t, lp, "p1", true, 1.0)

	_, err := planscore.Run(cfg)
	if err == nil {
		t.Error("expected error for insufficient data")
	}
	if !strings.Contains(psErrOut(cfg), "joined records found") {
		t.Errorf("expected insufficiency message; got %q", psErrOut(cfg))
	}
}

// ---- (o) correlate: sufficient data → report sections ----------------------

func TestPlanScore_Correlate_SufficientData(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "correlate"
	cfg.MinN = 3
	lp := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")

	for i := 0; i < 5; i++ {
		id := "plan-" + string(rune('a'+i))
		writePlanScoredRecord(t, lp, id, "PASS", 0.6+float64(i)*0.08)
		writePlanOutcomeRecord(t, lp, id, i%2 == 0, float64(i)*0.3)
	}

	res, err := planscore.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CorrelateN != 5 {
		t.Errorf("expected 5 joined; got %d", res.CorrelateN)
	}
	o := psOut(cfg)
	for _, want := range []string{"Pearson r", "first_try_pass", "first-try-pass rate"} {
		if !strings.Contains(o, want) {
			t.Errorf("expected %q in correlate output; got %q", want, o)
		}
	}
}

// ---- (p) unknown subcommand -------------------------------------------------

func TestPlanScore_UnknownSubcommand(t *testing.T) {
	cfg := newPlanScoreConfig(t)
	cfg.Subcommand = "frobnicate"
	_, err := planscore.Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error; got %v", err)
	}
}

// ---- (q) portedCommands entry -----------------------------------------------

func TestPlanScore_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "plan score" {
			return
		}
	}
	t.Error("expected 'plan score' in portedCommands")
}

// ---- (r) help text ----------------------------------------------------------

func TestPlanScore_HelpText(t *testing.T) {
	var buf bytes.Buffer
	planscore.PrintHelp(&buf)
	o := buf.String()
	for _, want := range []string{"show", "history", "override", "correlate", "--limit", "--reason", "--min-n"} {
		if !strings.Contains(o, want) {
			t.Errorf("help missing %q", want)
		}
	}
}
