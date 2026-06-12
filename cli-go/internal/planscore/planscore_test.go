package planscore

import (
	"bytes"
	"encoding/json"
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
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
		Now:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
	}
}

func out(cfg Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func errOut(cfg Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

// writeScoredRecord appends a plan_scored record to the log.
func writeScoredRecord(t *testing.T, logPath string, planID, verdict string, agg float64, dissent bool) {
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
		"dissent":         dissent,
		"cost_usd":        0.012,
		"project":         "proj-a",
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
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// writeOutcomeRecord appends a plan_outcome record to the log.
func writeOutcomeRecord(t *testing.T, logPath, planID string, ftp bool, scopeCreep float64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := map[string]interface{}{
		"type":              "plan_outcome",
		"ts":                "2026-06-02T10:00:00Z",
		"plan_id":           planID,
		"project":           "proj-a",
		"first_try_pass":    ftp,
		"scope_creep_ratio": scopeCreep,
		"rework_cycles":     0,
	}
	line, _ := json.Marshal(rec)
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// ---- show -------------------------------------------------------------------

func TestShow_NoLog(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "show"
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when log missing")
	}
	if !strings.Contains(errOut(cfg), "No plan-quality-log.ndjson") {
		t.Errorf("expected missing-log advisory in stderr; got %q", errOut(cfg))
	}
}

func TestShow_EmptyLog(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "show"
	logPath := filepath.Join(cfg.HomeDir, ".yakos-state", "plan-quality-log.ndjson")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(logPath, []byte{}, 0644)
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when log empty")
	}
}

func TestShow_LatestRecord(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "show"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)
	writeScoredRecord(t, logPath, "plan-002", "PASS", 0.91, false)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RecordsFound != 1 {
		t.Errorf("expected 1 record; got %d", res.RecordsFound)
	}
	// Should show plan-002 (latest).
	o := out(cfg)
	if !strings.Contains(o, "plan-002") {
		t.Errorf("expected plan-002 in output; got %q", o)
	}
	if !strings.Contains(o, "0.91") {
		t.Errorf("expected aggregate 0.91 in output; got %q", o)
	}
}

func TestShow_SpecificPlanID(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "show"
	cfg.PlanID = "plan-001"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)
	writeScoredRecord(t, logPath, "plan-002", "PASS", 0.91, false)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RecordsFound != 1 {
		t.Errorf("expected 1 record; got %d", res.RecordsFound)
	}
	o := out(cfg)
	if !strings.Contains(o, "plan-001") {
		t.Errorf("expected plan-001 in output; got %q", o)
	}
	if strings.Contains(o, "plan-002") {
		t.Errorf("expected plan-002 NOT in output; got %q", o)
	}
}

func TestShow_UnknownPlanID(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "show"
	cfg.PlanID = "nonexistent"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for nonexistent plan_id")
	}
}

func TestShow_DimensionMedians(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "show"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "Per-dimension medians") {
		t.Errorf("expected dimension section in output; got %q", o)
	}
	if !strings.Contains(o, "acceptance_criteria_specificity") {
		t.Errorf("expected dimension name in output; got %q", o)
	}
}

// ---- history ----------------------------------------------------------------

func TestHistory_NoLog(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "history"
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "No records found") {
		t.Errorf("expected no-records advisory; got %q", o)
	}
}

func TestHistory_Header(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "history"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "ts") || !strings.Contains(o, "plan_id") || !strings.Contains(o, "verdict") {
		t.Errorf("expected header row in output; got %q", o)
	}
}

func TestHistory_NewestFirst(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "history"
	lp := cfg.resolveLogPath()

	// Write three records with distinct timestamps by manipulating the JSON directly.
	recs := []struct {
		id string
		ts string
	}{
		{"plan-old", "2026-01-01T00:00:00Z"},
		{"plan-mid", "2026-03-01T00:00:00Z"},
		{"plan-new", "2026-06-01T00:00:00Z"},
	}
	if err := os.MkdirAll(filepath.Dir(lp), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, r := range recs {
		rec := map[string]interface{}{
			"type":            "plan_scored",
			"ts":              r.ts,
			"plan_id":         r.id,
			"verdict":         "PASS",
			"aggregate_score": 0.8,
			"dissent":         false,
			"cost_usd":        0.01,
		}
		line, _ := json.Marshal(rec)
		f, err := os.OpenFile(lp, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			t.Fatalf("open lp: %v", err)
		}
		_, _ = f.Write(append(line, '\n'))
		_ = f.Close()
	}

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	newIdx := strings.Index(o, "plan-new")
	midIdx := strings.Index(o, "plan-mid")
	oldIdx := strings.Index(o, "plan-old")
	if newIdx == -1 || midIdx == -1 || oldIdx == -1 {
		t.Fatalf("all plan IDs should appear; got %q", o)
	}
	if newIdx >= midIdx || midIdx >= oldIdx {
		t.Errorf("expected newest-first order; new=%d mid=%d old=%d", newIdx, midIdx, oldIdx)
	}
}

func TestHistory_Limit(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "history"
	cfg.Limit = 2
	logPath := cfg.resolveLogPath()
	for i := 0; i < 5; i++ {
		writeScoredRecord(t, logPath, "plan-"+string(rune('0'+i)), "PASS", 0.8, false)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RecordsFound != 2 {
		t.Errorf("expected 2 records (limit); got %d", res.RecordsFound)
	}
}

func TestHistory_ProjectFilter(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "history"
	cfg.Project = "proj-a"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-a", "PASS", 0.8, false)

	// Write a record for a different project.
	rec := map[string]interface{}{
		"type":            "plan_scored",
		"ts":              "2026-06-01T10:00:00Z",
		"plan_id":         "plan-b",
		"verdict":         "FAIL",
		"aggregate_score": 0.4,
		"dissent":         false,
		"cost_usd":        0.01,
		"project":         "proj-b",
	}
	line, _ := json.Marshal(rec)
	f, _ := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if strings.Contains(o, "plan-b") {
		t.Errorf("plan-b should be filtered out; got %q", o)
	}
	if !strings.Contains(o, "plan-a") {
		t.Errorf("expected plan-a in output; got %q", o)
	}
}

func TestHistory_MalformedLinesSkipped(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "history"
	logPath := cfg.resolveLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatal(err)
	}
	// Write a mix of valid and malformed lines.
	content := `{"type":"plan_scored","ts":"2026-06-01T00:00:00Z","plan_id":"p1","verdict":"PASS","aggregate_score":0.8,"dissent":false,"cost_usd":0.01}
{this is not json}
{"type":"plan_scored","ts":"2026-06-01T01:00:00Z","plan_id":"p2","verdict":"PASS","aggregate_score":0.9,"dissent":false,"cost_usd":0.02}
`
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RecordsFound != 2 {
		t.Errorf("expected 2 records; got %d", res.RecordsFound)
	}
}

// ---- override ---------------------------------------------------------------

func TestOverride_MissingPlanID(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.Reason = "looks good"
	_, err := Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "plan_id") {
		t.Errorf("expected plan_id required error; got %v", err)
	}
}

func TestOverride_MissingReason(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	_, err := Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Errorf("expected reason required error; got %v", err)
	}
}

func TestOverride_NoExistingRecord(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-ghost"
	cfg.Reason = "manual override"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for nonexistent plan_id")
	}
	if !strings.Contains(errOut(cfg), "no existing record") {
		t.Errorf("expected no-existing-record message; got %q", errOut(cfg))
	}
}

func TestOverride_AppendsRecord(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	cfg.Reason = "manual review approved"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "FAIL", 0.4, false)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OverrideWritten {
		t.Error("expected OverrideWritten=true")
	}

	// Verify the record is in the log.
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "plan_score_override") {
		t.Errorf("expected override record in log; got %q", string(data))
	}
	if !strings.Contains(string(data), "manual review approved") {
		t.Errorf("expected reason in log; got %q", string(data))
	}
}

func TestOverride_OverrideOutput(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	cfg.Reason = "approved"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)

	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "Override recorded") {
		t.Errorf("expected confirmation message; got %q", o)
	}
	if !strings.Contains(o, "plan-001") {
		t.Errorf("expected plan_id in output; got %q", o)
	}
}

func TestOverride_RemovesPlanBlockedMarker_MatchingPlanID(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	cfg.Reason = "approved"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "FAIL", 0.4, false)

	// Create a .plan-blocked marker with matching plan_id.
	curDir := t.TempDir()
	cfg.CurrentDir = curDir
	marker := filepath.Join(curDir, ".plan-blocked")
	markerContent := `{"plan_id":"plan-001"}`
	if err := os.WriteFile(marker, []byte(markerContent), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.BlockedMarkerRemoved {
		t.Error("expected BlockedMarkerRemoved=true")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("expected marker to be removed; stat err: %v", err)
	}
	if !strings.Contains(out(cfg), ".plan-blocked marker removed") {
		t.Errorf("expected removal message; got %q", out(cfg))
	}
}

func TestOverride_SkipsPlanBlockedMarker_DifferentPlanID(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	cfg.Reason = "approved"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "FAIL", 0.4, false)

	// Create a .plan-blocked marker with a different plan_id.
	curDir := t.TempDir()
	cfg.CurrentDir = curDir
	marker := filepath.Join(curDir, ".plan-blocked")
	markerContent := `{"plan_id":"plan-999"}`
	if err := os.WriteFile(marker, []byte(markerContent), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.BlockedMarkerRemoved {
		t.Error("expected BlockedMarkerRemoved=false (different plan_id)")
	}
	// Marker should still exist.
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		t.Error("expected marker to remain; it was removed")
	}
}

func TestOverride_RemovesPlanBlockedMarker_NoJSON(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	cfg.Reason = "approved"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "FAIL", 0.4, false)

	// Create a .plan-blocked marker with no JSON (plain text).
	curDir := t.TempDir()
	cfg.CurrentDir = curDir
	marker := filepath.Join(curDir, ".plan-blocked")
	if err := os.WriteFile(marker, []byte("blocked"), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.BlockedMarkerRemoved {
		t.Error("expected BlockedMarkerRemoved=true for non-JSON marker")
	}
}

func TestOverride_NoCurrentDirOK(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "override"
	cfg.PlanID = "plan-001"
	cfg.Reason = "approved"
	logPath := cfg.resolveLogPath()
	writeScoredRecord(t, logPath, "plan-001", "PASS", 0.82, false)
	// No CurrentDir set and no env vars → resolveCurrentDir fails → that's OK.
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OverrideWritten {
		t.Error("expected OverrideWritten=true even without current dir")
	}
}

// ---- correlate --------------------------------------------------------------

func TestCorrelate_InsufficientData(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "correlate"
	cfg.MinN = 5
	logPath := cfg.resolveLogPath()
	// Write 3 joined pairs — fewer than MinN=5.
	for i := 0; i < 3; i++ {
		id := "plan-" + string(rune('a'+i))
		writeScoredRecord(t, logPath, id, "PASS", 0.8, false)
		writeOutcomeRecord(t, logPath, id, true, 1.0)
	}

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for insufficient data")
	}
	if !strings.Contains(errOut(cfg), "joined records found") {
		t.Errorf("expected insufficiency message; got %q", errOut(cfg))
	}
}

func TestCorrelate_SufficientData(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "correlate"
	cfg.MinN = 3
	logPath := cfg.resolveLogPath()

	for i := 0; i < 5; i++ {
		id := "plan-" + string(rune('a'+i))
		agg := 0.5 + float64(i)*0.1
		ftp := i%2 == 0
		writeScoredRecord(t, logPath, id, "PASS", agg, false)
		writeOutcomeRecord(t, logPath, id, ftp, float64(i)/4.0)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.CorrelateN != 5 {
		t.Errorf("expected 5 joined rows; got %d", res.CorrelateN)
	}
	o := out(cfg)
	if !strings.Contains(o, "Per-dimension Pearson r") {
		t.Errorf("expected Pearson r section; got %q", o)
	}
	if !strings.Contains(o, "first-try-pass rate") {
		t.Errorf("expected threshold section; got %q", o)
	}
}

func TestCorrelate_NoOutcomes(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "correlate"
	cfg.MinN = 2
	logPath := cfg.resolveLogPath()
	// Scored but no outcomes → 0 joined.
	writeScoredRecord(t, logPath, "plan-a", "PASS", 0.8, false)

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error with 0 joined records")
	}
}

func TestCorrelate_SinceFilter(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "correlate"
	cfg.MinN = 2
	cfg.Since = "2026-06-01T00:00:00Z"
	logPath := cfg.resolveLogPath()

	// Old scored record (before since) and new one (after).
	oldRec := map[string]interface{}{
		"type":            "plan_scored",
		"ts":              "2025-01-01T00:00:00Z",
		"plan_id":         "plan-old",
		"verdict":         "PASS",
		"aggregate_score": 0.8,
		"dissent":         false,
		"cost_usd":        0.01,
		"per_dimension_median": map[string]float64{
			"acceptance_criteria_specificity": 0.8,
			"assumption_surfacing":            0.7,
			"decomposition_granularity":       0.9,
			"dependency_clarity":              0.75,
			"domain_boundaries_respected":     0.85,
			"risk_rollback_honesty":           0.6,
		},
	}
	line, _ := json.Marshal(oldRec)
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	f, _ := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
	writeOutcomeRecord(t, logPath, "plan-old", false, 2.0)

	// Write 3 new records after since cutoff.
	for i := 0; i < 3; i++ {
		writeScoredRecord(t, logPath, "plan-new-"+string(rune('0'+i)), "PASS", 0.85, false)
		writeOutcomeRecord(t, logPath, "plan-new-"+string(rune('0'+i)), true, 1.0)
	}

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// plan-old scored before since → excluded from scored map → not joined.
	if res.CorrelateN != 3 {
		t.Errorf("expected 3 new joined records; got %d", res.CorrelateN)
	}
}

func TestCorrelate_DefaultMinN(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "correlate"
	// MinN=0 → defaults to 20.
	logPath := cfg.resolveLogPath()
	for i := 0; i < 5; i++ {
		id := "plan-" + string(rune('a'+i))
		writeScoredRecord(t, logPath, id, "PASS", 0.8, false)
		writeOutcomeRecord(t, logPath, id, true, 1.0)
	}

	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error: 5 < defaultMinN(20)")
	}
}

// ---- unknown subcommand -----------------------------------------------------

func TestUnknownSubcommand(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "frobnicate"
	_, err := Run(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("expected unknown subcommand error; got %v", err)
	}
}

// ---- PrintHelp --------------------------------------------------------------

func TestPrintHelp(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	out := buf.String()
	for _, want := range []string{"show", "history", "override", "correlate", "--limit", "--reason"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q; got:\n%s", want, out)
		}
	}
}

// ---- resolveLogPath ---------------------------------------------------------

func TestResolveLogPath_EnvOverride(t *testing.T) {
	t.Setenv("YAKOS_PLAN_QUALITY_LOG", "/tmp/test-pql.ndjson")
	cfg := Config{}
	got := cfg.resolveLogPath()
	if got != "/tmp/test-pql.ndjson" {
		t.Errorf("expected env-override path; got %q", got)
	}
}

func TestResolveLogPath_CfgOverride(t *testing.T) {
	cfg := Config{PlanQualityLog: "/custom/path.ndjson"}
	got := cfg.resolveLogPath()
	if got != "/custom/path.ndjson" {
		t.Errorf("expected cfg override; got %q", got)
	}
}

func TestResolveLogPath_Default(t *testing.T) {
	cfg := Config{HomeDir: "/home/testuser"}
	got := cfg.resolveLogPath()
	want := filepath.FromSlash("/home/testuser/.yakos-state/plan-quality-log.ndjson")
	if got != want {
		t.Errorf("expected %q; got %q", want, got)
	}
}

// ---- appendToLog / atomicity ------------------------------------------------

func TestAppendToLog_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "sub", "dir", "log.ndjson")
	line := []byte(`{"test":true}`)
	if err := appendToLog(logPath, line); err != nil {
		t.Fatalf("appendToLog: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test") {
		t.Errorf("expected content in log; got %q", string(data))
	}
}

func TestAppendToLog_MultipleAppends(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.ndjson")
	for i := 0; i < 3; i++ {
		line := []byte(`{"n":` + string(rune('0'+i)) + `}`)
		if err := appendToLog(logPath, line); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines; got %d: %q", len(lines), string(data))
	}
}

// ---- fileHasContent ---------------------------------------------------------

func TestFileHasContent_Missing(t *testing.T) {
	if fileHasContent("/nonexistent/path/file.ndjson") {
		t.Error("expected false for missing file")
	}
}

func TestFileHasContent_Empty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.ndjson")
	_ = os.WriteFile(p, []byte{}, 0644)
	if fileHasContent(p) {
		t.Error("expected false for empty file")
	}
}

func TestFileHasContent_HasData(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.ndjson")
	_ = os.WriteFile(p, []byte(`{"x":1}`+"\n"), 0644)
	if !fileHasContent(p) {
		t.Error("expected true for non-empty file")
	}
}

// ---- pearsonR ---------------------------------------------------------------

func TestPearsonR_PerfectPositive(t *testing.T) {
	rows := []joinedRow{
		{dims: [6]float64{0.1}, firstTryPass: 0},
		{dims: [6]float64{0.5}, firstTryPass: 0},
		{dims: [6]float64{0.9}, firstTryPass: 1},
		{dims: [6]float64{1.0}, firstTryPass: 1},
	}
	r := pearsonR(rows, 0)
	if r < 0.8 {
		t.Errorf("expected high positive r; got %.3f", r)
	}
}

func TestPearsonR_InsufficientData(t *testing.T) {
	rows := []joinedRow{
		{dims: [6]float64{0.5}, firstTryPass: -1},
		{dims: [6]float64{0.8}, firstTryPass: -1},
	}
	r := pearsonR(rows, 0)
	if r != 0 {
		t.Errorf("expected 0 for all-absent ftp; got %.3f", r)
	}
}
