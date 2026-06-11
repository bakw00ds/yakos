package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- helpers ---

func writeBudgetsYAML(t *testing.T, dir, content string) string {
	t.Helper()
	metricsDir := filepath.Join(dir, ".yakos", "metrics")
	if err := os.MkdirAll(metricsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(metricsDir, "budgets.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write budgets.yaml: %v", err)
	}
	return path
}

func writeFixtureHistory(t *testing.T, dir string, snaps ...Snapshot) {
	t.Helper()
	for _, s := range snaps {
		if err := AppendSnapshot(dir, s); err != nil {
			t.Fatalf("AppendSnapshot: %v", err)
		}
	}
}

// baseSnap returns a minimal snapshot with all-nil metrics (not measured).
func baseSnap(commit string) Snapshot {
	return Snapshot{
		Schema:     schemaVersion,
		Ts:         time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC),
		Commit:     commit,
		Branch:     "main",
		Trigger:    "manual",
		Profiles:   []string{"go-backend"},
		ToolStatus: map[string]string{},
	}
}

// --- TestGate_NoBudgetsFile ---

func TestGate_NoBudgetsFile(t *testing.T) {
	dir := t.TempDir()
	var buf strings.Builder
	cfg := GateConfig{
		ProjectDir: dir,
		Writer:     &buf,
		ErrWriter:  &buf,
	}
	result, err := RunGate(cfg)
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0 for missing budgets.yaml; got %d", result.ExitCode)
	}
	if !strings.Contains(buf.String(), "no budgets.yaml found") {
		t.Errorf("expected 'no budgets.yaml found' message; got:\n%s", buf.String())
	}
}

// --- TestGate_AbsoluteCeilingBreach ---

func TestGate_AbsoluteCeilingBreach(t *testing.T) {
	dir := t.TempDir()

	// Budget: secret_scan_hits ceiling = 0.
	// Snapshot: secret_scan_hits = 3 → breach.
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  security.secret_scan_hits: 0
`)
	snap := baseSnap("aaa1")
	snap.Metrics.Security.SecretScanHits = intPtr(3)
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 1 {
		t.Fatalf("expected 1 breach; got %d\n%s", len(result.Breaches), buf.String())
	}
	b := result.Breaches[0]
	if b.MetricPath != "security.secret_scan_hits" {
		t.Errorf("breach metric: got %q; want security.secret_scan_hits", b.MetricPath)
	}
	if b.Kind != BreachAbsolute {
		t.Errorf("breach kind: got %q; want absolute", b.Kind)
	}
	if b.Direction != "ceiling" {
		t.Errorf("breach direction: got %q; want ceiling", b.Direction)
	}
	if result.ExitCode != 1 {
		t.Errorf("enforce mode with breach should exit 1; got %d", result.ExitCode)
	}
}

// --- TestGate_AbsoluteFloorBreach ---

func TestGate_AbsoluteFloorBreach(t *testing.T) {
	dir := t.TempDir()

	// Budget: test.coverage_pct floor = 80.
	// Snapshot: coverage_pct = 65 → breach (below floor).
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  test.coverage_pct: 80
`)
	snap := baseSnap("bbb1")
	snap.Metrics.Test.CoveragePct = floatPtr(65.0)
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 1 {
		t.Fatalf("expected 1 breach; got %d\n%s", len(result.Breaches), buf.String())
	}
	b := result.Breaches[0]
	if b.Direction != "floor" {
		t.Errorf("breach direction: got %q; want floor", b.Direction)
	}
	if b.Kind != BreachAbsolute {
		t.Errorf("breach kind: got %q; want absolute", b.Kind)
	}
	if result.ExitCode != 1 {
		t.Errorf("enforce mode with floor breach should exit 1; got %d", result.ExitCode)
	}
}

// --- TestGate_FloorPass ---

func TestGate_FloorPass(t *testing.T) {
	dir := t.TempDir()

	// Budget: test.coverage_pct floor = 70.
	// Snapshot: coverage_pct = 85 → PASS (above floor).
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  test.coverage_pct: 70
`)
	snap := baseSnap("ccc1")
	snap.Metrics.Test.CoveragePct = floatPtr(85.0)
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 0 {
		t.Errorf("expected 0 breaches; got %d\n%s", len(result.Breaches), buf.String())
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit 0 for pass; got %d", result.ExitCode)
	}
}

// --- TestGate_NullMetricSkipped ---

func TestGate_NullMetricSkipped(t *testing.T) {
	dir := t.TempDir()

	// Budget: test.coverage_pct floor = 80.
	// Snapshot: coverage_pct = nil (not measured) → skipped, NOT a breach.
	writeBudgetsYAML(t, dir, `
mode: enforce
regression_pct: 5
budgets:
  test.coverage_pct: 80
  security.secret_scan_hits: 0
`)
	snap := baseSnap("nnn1")
	// Both metrics nil.
	// security.secret_scan_hits is nil → skipped.
	// test.coverage_pct is nil → skipped.
	writeFixtureHistory(t, dir, snap)

	var errBuf strings.Builder
	var outBuf strings.Builder
	result, err := RunGate(GateConfig{
		ProjectDir: dir,
		Writer:     &outBuf,
		ErrWriter:  &errBuf,
	})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 0 {
		t.Errorf("nil metric must not breach; got %d breaches: %+v", len(result.Breaches), result.Breaches)
	}
	if result.ExitCode != 0 {
		t.Errorf("no breach → exit 0; got %d", result.ExitCode)
	}
	// Skipped paths should be recorded.
	if len(result.Skipped) != 2 {
		t.Errorf("expected 2 skipped; got %d: %v", len(result.Skipped), result.Skipped)
	}
	// INFO lines should appear in stderr.
	if !strings.Contains(errBuf.String(), "not measured") {
		t.Errorf("expected 'not measured' INFO line; got:\n%s", errBuf.String())
	}
}

// --- TestGate_RegressionBreach ---

func TestGate_RegressionBreach(t *testing.T) {
	dir := t.TempDir()

	// Budget: code_quality.lint_findings ceiling = 100, regression_pct = 5.
	// Previous snapshot: lint_findings = 10.
	// Current snapshot: lint_findings = 20 → 100% regression (well above 5%).
	writeBudgetsYAML(t, dir, `
mode: enforce
regression_pct: 5
budgets:
  code_quality.lint_findings: 100
`)
	prev := baseSnap("ppp1")
	prev.Metrics.CodeQuality.LintFindings = intPtr(10)

	curr := baseSnap("ppp2")
	curr.Ts = time.Date(2026, 6, 11, 1, 0, 0, 0, time.UTC)
	curr.Metrics.CodeQuality.LintFindings = intPtr(20)

	writeFixtureHistory(t, dir, prev, curr)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	// Should have 1 regression breach (lint went 10→20, which is 100% worse than budget 5%).
	// The absolute budget (100) is not breached.
	hasRegression := false
	for _, b := range result.Breaches {
		if b.Kind == BreachRegression && b.MetricPath == "code_quality.lint_findings" {
			hasRegression = true
		}
	}
	if !hasRegression {
		t.Errorf("expected regression breach for lint_findings; breaches: %+v\n%s", result.Breaches, buf.String())
	}
	if result.ExitCode != 1 {
		t.Errorf("enforce + regression breach should exit 1; got %d", result.ExitCode)
	}
}

// --- TestGate_NoRegressionWhenImproved ---

func TestGate_NoRegressionWhenImproved(t *testing.T) {
	dir := t.TempDir()

	writeBudgetsYAML(t, dir, `
mode: enforce
regression_pct: 5
budgets:
  code_quality.lint_findings: 100
`)
	prev := baseSnap("qqq1")
	prev.Metrics.CodeQuality.LintFindings = intPtr(20)

	curr := baseSnap("qqq2")
	curr.Ts = time.Date(2026, 6, 11, 1, 0, 0, 0, time.UTC)
	curr.Metrics.CodeQuality.LintFindings = intPtr(10) // improved

	writeFixtureHistory(t, dir, prev, curr)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 0 {
		t.Errorf("improvement should not breach; got %d breaches: %+v", len(result.Breaches), result.Breaches)
	}
}

// --- TestGate_AdvisoryExitsZeroOnBreach ---

func TestGate_AdvisoryExitsZeroOnBreach(t *testing.T) {
	dir := t.TempDir()

	writeBudgetsYAML(t, dir, `
mode: advisory
budgets:
  security.secret_scan_hits: 0
`)
	snap := baseSnap("adv1")
	snap.Metrics.Security.SecretScanHits = intPtr(5) // breaches ceiling
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 1 {
		t.Errorf("expected 1 breach; got %d", len(result.Breaches))
	}
	if result.ExitCode != 0 {
		t.Errorf("advisory mode must exit 0; got %d", result.ExitCode)
	}
}

// --- TestGate_ForceEnforceOverridesAdvisory ---

func TestGate_ForceEnforceOverridesAdvisory(t *testing.T) {
	dir := t.TempDir()

	writeBudgetsYAML(t, dir, `
mode: advisory
budgets:
  security.secret_scan_hits: 0
`)
	snap := baseSnap("fe1")
	snap.Metrics.Security.SecretScanHits = intPtr(1)
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{
		ProjectDir:   dir,
		ForceEnforce: true,
		Writer:       &buf,
		ErrWriter:    &buf,
	})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if result.Mode != "enforce" {
		t.Errorf("expected mode enforce; got %q", result.Mode)
	}
	if result.ExitCode != 1 {
		t.Errorf("force enforce with breach must exit 1; got %d", result.ExitCode)
	}
}

// --- TestGate_ForceAdvisoryOverridesEnforce ---

func TestGate_ForceAdvisoryOverridesEnforce(t *testing.T) {
	dir := t.TempDir()

	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  security.secret_scan_hits: 0
`)
	snap := baseSnap("fa1")
	snap.Metrics.Security.SecretScanHits = intPtr(1)
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{
		ProjectDir:    dir,
		ForceAdvisory: true,
		Writer:        &buf,
		ErrWriter:     &buf,
	})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if result.Mode != "advisory" {
		t.Errorf("expected mode advisory; got %q", result.Mode)
	}
	if result.ExitCode != 0 {
		t.Errorf("force advisory must exit 0 even with breach; got %d", result.ExitCode)
	}
}

// --- TestGate_MapPathResolution ---

func TestGate_MapPathResolution(t *testing.T) {
	dir := t.TempDir()

	// Budget: security.cve_count_by_severity.critical ceiling = 0.
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  security.cve_count_by_severity.critical: 0
`)
	snap := baseSnap("map1")
	snap.Metrics.Security.CVECountBySeverity = map[string]int{
		"critical": 2,
		"high":     5,
	}
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 1 {
		t.Fatalf("expected 1 breach for cve critical; got %d\n%s", len(result.Breaches), buf.String())
	}
	if result.Breaches[0].MetricPath != "security.cve_count_by_severity.critical" {
		t.Errorf("breach metric: got %q; want security.cve_count_by_severity.critical", result.Breaches[0].MetricPath)
	}
}

// --- TestGate_MapPathResolution_NilMap ---

func TestGate_MapPathResolution_NilMap(t *testing.T) {
	dir := t.TempDir()

	// Budget: security.cve_count_by_severity.critical = 0.
	// Snapshot: CVECountBySeverity is nil (npm audit not run) → skipped.
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  security.cve_count_by_severity.critical: 0
`)
	snap := baseSnap("map2")
	snap.Metrics.Security.CVECountBySeverity = nil
	writeFixtureHistory(t, dir, snap)

	var errBuf strings.Builder
	var outBuf strings.Builder
	result, err := RunGate(GateConfig{
		ProjectDir: dir,
		Writer:     &outBuf,
		ErrWriter:  &errBuf,
	})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 0 {
		t.Errorf("nil map metric must not breach; got %d breaches", len(result.Breaches))
	}
	if len(result.Skipped) != 1 {
		t.Errorf("expected 1 skipped; got %d", len(result.Skipped))
	}
}

// --- TestGate_AllBreachesCollected ---

func TestGate_AllBreachesCollected(t *testing.T) {
	dir := t.TempDir()

	// Three budgets; two breach, one passes.
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  security.secret_scan_hits: 0
  test.coverage_pct: 80
  code_quality.lint_findings: 100
`)
	snap := baseSnap("all1")
	snap.Metrics.Security.SecretScanHits = intPtr(2)   // ceiling breach (> 0)
	snap.Metrics.Test.CoveragePct = floatPtr(55.0)     // floor breach (< 80)
	snap.Metrics.CodeQuality.LintFindings = intPtr(10) // PASS (< 100)
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 2 {
		t.Errorf("expected 2 breaches; got %d\n%s", len(result.Breaches), buf.String())
	}
	// Verify both breaching paths appear.
	paths := map[string]bool{}
	for _, b := range result.Breaches {
		paths[b.MetricPath] = true
	}
	if !paths["security.secret_scan_hits"] {
		t.Error("expected breach for security.secret_scan_hits")
	}
	if !paths["test.coverage_pct"] {
		t.Error("expected breach for test.coverage_pct")
	}
	if result.ExitCode != 1 {
		t.Errorf("enforce with breaches → exit 1; got %d", result.ExitCode)
	}
}

// --- TestGate_JSONOutput ---

func TestGate_JSONOutput(t *testing.T) {
	dir := t.TempDir()

	writeBudgetsYAML(t, dir, `
mode: advisory
budgets:
  security.secret_scan_hits: 0
`)
	snap := baseSnap("json1")
	snap.Metrics.Security.SecretScanHits = intPtr(1)
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{
		ProjectDir: dir,
		EmitJSON:   true,
		Writer:     &buf,
		ErrWriter:  &buf,
	})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("advisory mode should exit 0; got %d", result.ExitCode)
	}

	var parsed GateResult
	if err := json.Unmarshal([]byte(buf.String()), &parsed); err != nil {
		t.Fatalf("JSON parse failed: %v\n%s", err, buf.String())
	}
	if parsed.Mode != "advisory" {
		t.Errorf("JSON mode: got %q; want advisory", parsed.Mode)
	}
	if len(parsed.Breaches) != 1 {
		t.Errorf("JSON breaches: expected 1; got %d", len(parsed.Breaches))
	}
}

// --- TestGate_NoSnapshotsInHistory ---

func TestGate_NoSnapshotsInHistory(t *testing.T) {
	dir := t.TempDir()
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  security.secret_scan_hits: 0
`)
	// No history written.

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("no snapshots → exit 0; got %d", result.ExitCode)
	}
	if !strings.Contains(buf.String(), "no snapshots found") {
		t.Errorf("expected 'no snapshots found' message; got:\n%s", buf.String())
	}
}

// --- TestGate_DeadCodeAliasPath ---

func TestGate_DeadCodeAliasPath(t *testing.T) {
	dir := t.TempDir()

	// The task spec example uses dead_code.unused_exported_symbols which
	// aliases to dead_code_symbols in the schema.
	writeBudgetsYAML(t, dir, `
mode: enforce
budgets:
  dead_code.unused_exported_symbols: 10
`)
	snap := baseSnap("dc1")
	snap.Metrics.DeadCode.DeadCodeSymbols = intPtr(25) // breach ceiling
	writeFixtureHistory(t, dir, snap)

	var buf strings.Builder
	result, err := RunGate(GateConfig{ProjectDir: dir, Writer: &buf, ErrWriter: &buf})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if len(result.Breaches) != 1 {
		t.Fatalf("expected 1 breach; got %d\n%s", len(result.Breaches), buf.String())
	}
}

// --- TestResolveMetricPath_AllCategories ---

// Spot-checks every top-level category so we catch typos.
func TestResolveMetricPath_AllCategories(t *testing.T) {
	m := &Metrics{}
	m.Efficiency.TotalCostUSD = floatPtr(42.5)
	m.Dispatch.HookBypassCount = intPtr(3)
	m.ModelRouting.RightSizedPct = floatPtr(0.9)
	m.DORA.DeployFrequency = intPtr(7)
	m.SizeChurn.TotalLOC = intPtr(10000)
	m.CodeQuality.LintFindings = intPtr(5)
	m.DeadCode.DeadCodeSymbols = intPtr(12)
	m.Security.SecretScanHits = intPtr(0)
	m.Test.CoveragePct = floatPtr(85.0)

	cases := []struct {
		path string
		want float64
	}{
		{"efficiency.total_cost_usd", 42.5},
		{"dispatch.hook_bypass_count", 3},
		{"model_routing.right_sized_pct", 0.9},
		{"dora.deploy_frequency", 7},
		{"size_churn.total_loc", 10000},
		{"code_quality.lint_findings", 5},
		{"dead_code.dead_code_symbols", 12},
		{"security.secret_scan_hits", 0},
		{"test.coverage_pct", 85.0},
	}

	for _, tc := range cases {
		v, ok := resolveMetricPath(m, tc.path)
		if !ok {
			t.Errorf("resolveMetricPath(%q): got ok=false; want true", tc.path)
			continue
		}
		if v != tc.want {
			t.Errorf("resolveMetricPath(%q): got %v; want %v", tc.path, v, tc.want)
		}
	}
}

// --- TestResolveMetricPath_NilIsNotMeasured ---

func TestResolveMetricPath_NilIsNotMeasured(t *testing.T) {
	m := &Metrics{} // all fields nil

	paths := []string{
		"efficiency.total_cost_usd",
		"dispatch.first_try_success_rate",
		"model_routing.right_sized_pct",
		"dora.deploy_frequency",
		"size_churn.total_loc",
		"code_quality.lint_findings",
		"dead_code.dead_code_symbols",
		"security.secret_scan_hits",
		"test.coverage_pct",
	}

	for _, path := range paths {
		_, ok := resolveMetricPath(m, path)
		if ok {
			t.Errorf("resolveMetricPath(%q) on nil metric: got ok=true; want false", path)
		}
	}
}

// --- TestGate_ParseArgs_Gate ---

func TestGate_ParseArgs_Gate(t *testing.T) {
	cfg, err := ParseArgs([]string{
		"gate",
		"--enforce",
		"--json",
		"--budgets", "/tmp/custom.yaml",
	}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs gate: %v", err)
	}
	if cfg.Subcommand != "gate" {
		t.Errorf("Subcommand: got %q; want gate", cfg.Subcommand)
	}
	if !cfg.GateEnforce {
		t.Error("expected GateEnforce=true")
	}
	if !cfg.EmitJSON {
		t.Error("expected EmitJSON=true")
	}
	if cfg.GateBudgetsPath != "/tmp/custom.yaml" {
		t.Errorf("GateBudgetsPath: got %q; want /tmp/custom.yaml", cfg.GateBudgetsPath)
	}
}

// --- TestGate_ParseArgs_GateAdvisory ---

func TestGate_ParseArgs_GateAdvisory(t *testing.T) {
	cfg, err := ParseArgs([]string{"gate", "--advisory"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs gate advisory: %v", err)
	}
	if !cfg.GateAdvisory {
		t.Error("expected GateAdvisory=true")
	}
}

// --- TestGate_ParseArgs_GateCollect ---

func TestGate_ParseArgs_GateCollect(t *testing.T) {
	cfg, err := ParseArgs([]string{"gate", "--collect"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs gate collect: %v", err)
	}
	if !cfg.GateCollect {
		t.Error("expected GateCollect=true")
	}
}

// --- TestBreachesAbsolute ---

func TestBreachesAbsolute(t *testing.T) {
	cases := []struct {
		value, threshold float64
		floor            bool
		want             bool
	}{
		// ceiling: breach when value > threshold
		{5, 0, false, true},
		{0, 0, false, false},
		{-1, 0, false, false},
		// floor: breach when value < threshold
		{65, 80, true, true},
		{80, 80, true, false},
		{90, 80, true, false},
	}
	for _, tc := range cases {
		got := breachesAbsolute(tc.value, tc.threshold, tc.floor)
		if got != tc.want {
			t.Errorf("breachesAbsolute(%v, %v, floor=%v) = %v; want %v",
				tc.value, tc.threshold, tc.floor, got, tc.want)
		}
	}
}

// --- TestRegressionPct ---

func TestRegressionPct(t *testing.T) {
	cases := []struct {
		current, previous float64
		floor             bool
		wantRegress       bool
		wantPctAbove      float64 // minimum expected pct (for regressions)
	}{
		// ceiling: regression when current > previous
		{20, 10, false, true, 90}, // 100% worse
		{10, 20, false, false, 0}, // improved
		{10, 10, false, false, 0}, // no change
		// floor: regression when current < previous
		{65, 85, true, true, 20}, // 23.5% worse (dropped)
		{85, 65, true, false, 0}, // improved (rose)
		{85, 85, true, false, 0}, // no change
	}
	for _, tc := range cases {
		pct, regressed := regressionPct(tc.current, tc.previous, tc.floor)
		if regressed != tc.wantRegress {
			t.Errorf("regressionPct(%v, %v, floor=%v): regressed=%v; want %v",
				tc.current, tc.previous, tc.floor, regressed, tc.wantRegress)
		}
		if tc.wantRegress && pct < tc.wantPctAbove {
			t.Errorf("regressionPct(%v, %v, floor=%v): pct=%.2f; want > %.2f",
				tc.current, tc.previous, tc.floor, pct, tc.wantPctAbove)
		}
	}
}
