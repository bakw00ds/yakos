package metrics

import (
	"testing"

	"github.com/bakw00ds/yakos/internal/stackdetect"
)

// --- analyzerListFor profile wiring ---

func TestAnalyzerListFor_GoBackend(t *testing.T) {
	profiles := []stackdetect.Profile{stackdetect.ProfileGoBackend}
	list := analyzerListFor(profiles)
	assertContainsAnalyzer(t, list, "go-test")
	assertContainsAnalyzer(t, list, "golangci-lint")
	assertContainsAnalyzer(t, list, "gitleaks")
	assertNotContainsAnalyzer(t, list, "eslint")
	assertNotContainsAnalyzer(t, list, "ruff")
	assertNotContainsAnalyzer(t, list, "cargo-clippy")
}

func TestAnalyzerListFor_Node(t *testing.T) {
	profiles := []stackdetect.Profile{stackdetect.ProfileNode}
	list := analyzerListFor(profiles)
	assertContainsAnalyzer(t, list, "eslint")
	assertContainsAnalyzer(t, list, "tsc")
	assertContainsAnalyzer(t, list, "jscpd")
	assertContainsAnalyzer(t, list, "knip")
	assertContainsAnalyzer(t, list, "depcheck")
	assertContainsAnalyzer(t, list, "npm-audit")
	assertContainsAnalyzer(t, list, "gitleaks")
	assertNotContainsAnalyzer(t, list, "go-test")
	assertNotContainsAnalyzer(t, list, "ruff")
	assertNotContainsAnalyzer(t, list, "cargo-clippy")
}

func TestAnalyzerListFor_ReactNative(t *testing.T) {
	profiles := []stackdetect.Profile{stackdetect.ProfileReactNative}
	list := analyzerListFor(profiles)
	// react-native maps to nodeAnalyzers
	assertContainsAnalyzer(t, list, "eslint")
	assertContainsAnalyzer(t, list, "npm-audit")
	assertContainsAnalyzer(t, list, "gitleaks")
}

func TestAnalyzerListFor_Python(t *testing.T) {
	profiles := []stackdetect.Profile{stackdetect.ProfilePython}
	list := analyzerListFor(profiles)
	assertContainsAnalyzer(t, list, "ruff")
	assertContainsAnalyzer(t, list, "mypy")
	assertContainsAnalyzer(t, list, "radon")
	assertContainsAnalyzer(t, list, "vulture")
	assertContainsAnalyzer(t, list, "deptry")
	assertContainsAnalyzer(t, list, "bandit")
	assertContainsAnalyzer(t, list, "pip-audit")
	assertContainsAnalyzer(t, list, "gitleaks")
	assertNotContainsAnalyzer(t, list, "eslint")
	assertNotContainsAnalyzer(t, list, "cargo-clippy")
}

func TestAnalyzerListFor_Rust(t *testing.T) {
	profiles := []stackdetect.Profile{stackdetect.ProfileRust}
	list := analyzerListFor(profiles)
	assertContainsAnalyzer(t, list, "cargo-clippy")
	assertContainsAnalyzer(t, list, "cargo-audit")
	assertContainsAnalyzer(t, list, "cargo-deny")
	assertContainsAnalyzer(t, list, "cargo-machete")
	assertContainsAnalyzer(t, list, "gitleaks")
	assertNotContainsAnalyzer(t, list, "eslint")
	assertNotContainsAnalyzer(t, list, "ruff")
}

func TestAnalyzerListFor_MultiProfile(t *testing.T) {
	// A project with both Go and Node (e.g. a monorepo) gets both sets.
	profiles := []stackdetect.Profile{stackdetect.ProfileGoBackend, stackdetect.ProfileNode}
	list := analyzerListFor(profiles)
	assertContainsAnalyzer(t, list, "go-test")
	assertContainsAnalyzer(t, list, "eslint")
	assertContainsAnalyzer(t, list, "gitleaks")
}

func TestAnalyzerListFor_Unknown(t *testing.T) {
	// Unknown profile still gets gitleaks from cross-cutting.
	list := analyzerListFor([]stackdetect.Profile{"dotnet"})
	assertContainsAnalyzer(t, list, "gitleaks")
	assertNotContainsAnalyzer(t, list, "eslint")
	assertNotContainsAnalyzer(t, list, "ruff")
}

// --- missing-tool / null-metric invariant ---

func TestNodeAnalyzers_MissingToolsLeaveMetricsNil(t *testing.T) {
	dir := t.TempDir()
	m := &Metrics{}
	status := make(map[string]string)

	// Replace all node analyzers with bogus tool names so LookPath fails.
	bogus := make([]analyzer, len(nodeAnalyzers))
	for i, a := range nodeAnalyzers {
		b := a
		b.Tool = "bogus-node-tool-does-not-exist-" + a.Name
		bogus[i] = b
	}
	runAnalyzerList(dir, bogus, m, status)

	for _, a := range bogus {
		if s := status[a.Name]; s != "tool-missing" {
			t.Errorf("node analyzer %s: expected tool-missing; got %q", a.Name, s)
		}
	}
	// All metrics must remain nil — Apply was never called.
	if m.CodeQuality.LintFindings != nil {
		t.Error("LintFindings should be nil when tool missing")
	}
	if m.CodeQuality.LintWarningDensity != nil {
		t.Error("LintWarningDensity should be nil when tool missing")
	}
	if m.CodeQuality.VetFindings != nil {
		t.Error("VetFindings should be nil when tool missing")
	}
	if m.CodeQuality.DuplicationPct != nil {
		t.Error("DuplicationPct should be nil when tool missing")
	}
	if m.DeadCode.DeadCodeSymbols != nil {
		t.Error("DeadCodeSymbols should be nil when tool missing")
	}
	if m.DeadCode.UnusedDeps != nil {
		t.Error("UnusedDeps should be nil when tool missing")
	}
	if m.Security.CVECountBySeverity != nil {
		t.Error("CVECountBySeverity should be nil when tool missing")
	}
}

func TestPythonAnalyzers_MissingToolsLeaveMetricsNil(t *testing.T) {
	dir := t.TempDir()
	m := &Metrics{}
	status := make(map[string]string)

	bogus := make([]analyzer, len(pythonAnalyzers))
	for i, a := range pythonAnalyzers {
		b := a
		b.Tool = "bogus-python-tool-does-not-exist-" + a.Name
		bogus[i] = b
	}
	runAnalyzerList(dir, bogus, m, status)

	for _, a := range bogus {
		if s := status[a.Name]; s != "tool-missing" {
			t.Errorf("python analyzer %s: expected tool-missing; got %q", a.Name, s)
		}
	}
	if m.CodeQuality.LintFindings != nil {
		t.Error("LintFindings should be nil")
	}
	if m.CodeQuality.LintWarningDensity != nil {
		t.Error("LintWarningDensity should be nil")
	}
	if m.CodeQuality.CyclomaticComplexityP90 != nil {
		t.Error("CyclomaticComplexityP90 should be nil")
	}
	if m.DeadCode.DeadCodeSymbols != nil {
		t.Error("DeadCodeSymbols should be nil")
	}
	if m.DeadCode.UnusedDeps != nil {
		t.Error("UnusedDeps should be nil")
	}
	if m.Security.SASTFindingsBySeverity != nil {
		t.Error("SASTFindingsBySeverity should be nil")
	}
	if m.Security.VulnCount != nil {
		t.Error("VulnCount should be nil")
	}
}

func TestRustAnalyzers_MissingToolsLeaveMetricsNil(t *testing.T) {
	dir := t.TempDir()
	m := &Metrics{}
	status := make(map[string]string)

	bogus := make([]analyzer, len(rustAnalyzers))
	for i, a := range rustAnalyzers {
		b := a
		b.Tool = "bogus-rust-tool-does-not-exist-" + a.Name
		bogus[i] = b
	}
	runAnalyzerList(dir, bogus, m, status)

	for _, a := range bogus {
		if s := status[a.Name]; s != "tool-missing" {
			t.Errorf("rust analyzer %s: expected tool-missing; got %q", a.Name, s)
		}
	}
	if m.CodeQuality.LintFindings != nil {
		t.Error("LintFindings should be nil")
	}
	if m.Security.VulnCount != nil {
		t.Error("VulnCount should be nil")
	}
	if m.Security.LicenseRiskCount != nil {
		t.Error("LicenseRiskCount should be nil when tool missing")
	}
	if m.DeadCode.UnusedDeps != nil {
		t.Error("UnusedDeps should be nil")
	}
}

// --- Apply parser unit tests (feed canned tool output) ---

func TestParseESLintCompact(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantWarnings int
		wantErrors   int
	}{
		{
			name: "mixed findings",
			input: `/src/app.ts: line 10, col 5, warning - 'x' is defined but never used (no-unused-vars)
/src/app.ts: line 20, col 1, error - Unexpected console statement (no-console)
/src/util.ts: line 5, col 3, warning - Missing semicolon (semi)`,
			wantWarnings: 2,
			wantErrors:   1,
		},
		{
			name:         "empty output",
			input:        "",
			wantWarnings: 0,
			wantErrors:   0,
		},
		{
			name: "errors only",
			input: `/src/bad.ts: line 1, col 1, error - Parsing error (parse-error)
/src/bad2.ts: line 2, col 2, error - Missing return type (return-type)`,
			wantWarnings: 0,
			wantErrors:   2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warnings, errors := parseESLintCompact(tc.input)
			if warnings != tc.wantWarnings {
				t.Errorf("warnings: got %d, want %d", warnings, tc.wantWarnings)
			}
			if errors != tc.wantErrors {
				t.Errorf("errors: got %d, want %d", errors, tc.wantErrors)
			}
		})
	}
}

// TestESLintApply_LintWarningDensity_NilWhenLOCUnknown verifies that
// LintWarningDensity stays nil when TotalLOC is not yet populated — density
// cannot be computed without a denominator.
func TestESLintApply_LintWarningDensity_NilWhenLOCUnknown(t *testing.T) {
	m := &Metrics{}       // SizeChurn.TotalLOC is nil
	a := nodeAnalyzers[0] // eslint is first
	if a.Name != "eslint" {
		t.Fatalf("expected eslint as first node analyzer; got %s", a.Name)
	}
	input := `/src/app.ts: line 1, col 1, warning - unused var (no-unused-vars)`
	if err := a.Apply(input, nil, m); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// LintFindings must be set (the count is known).
	if m.CodeQuality.LintFindings == nil {
		t.Fatal("LintFindings should not be nil")
	}
	// LintWarningDensity must be nil — LOC unknown, denominator missing.
	if m.CodeQuality.LintWarningDensity != nil {
		t.Errorf("LintWarningDensity should be nil when LOC unknown; got %v", *m.CodeQuality.LintWarningDensity)
	}
}

// TestESLintApply_LintWarningDensity_SetWhenLOCKnown verifies that density IS
// populated once TotalLOC is available.
func TestESLintApply_LintWarningDensity_SetWhenLOCKnown(t *testing.T) {
	m := &Metrics{}
	m.SizeChurn.TotalLOC = intPtr(200)
	a := nodeAnalyzers[0] // eslint
	input := `/src/app.ts: line 1, col 1, warning - unused var (no-unused-vars)
/src/app.ts: line 2, col 1, warning - missing semi (semi)`
	if err := a.Apply(input, nil, m); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if m.CodeQuality.LintWarningDensity == nil {
		t.Fatal("LintWarningDensity should not be nil when LOC is known")
	}
	// 2 findings / 200 LOC * 100 = 1.0
	if *m.CodeQuality.LintWarningDensity != 1.0 {
		t.Errorf("LintWarningDensity: got %v, want 1.0", *m.CodeQuality.LintWarningDensity)
	}
}

// TestTSCApply_NilOnEmptyOutput verifies that VetFindings stays nil when tsc
// produces no output (tool startup failure, not a diagnostic result).
func TestTSCApply_NilOnEmptyOutput(t *testing.T) {
	m := &Metrics{}
	// tsc is the second node analyzer.
	var tscApply func(string, error, *Metrics) error
	for _, a := range nodeAnalyzers {
		if a.Name == "tsc" {
			tscApply = a.Apply
			break
		}
	}
	if tscApply == nil {
		t.Fatal("tsc analyzer not found in nodeAnalyzers")
	}
	if err := tscApply("", nil, m); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if m.CodeQuality.VetFindings != nil {
		t.Errorf("VetFindings should be nil for empty tsc output; got %d", *m.CodeQuality.VetFindings)
	}
}

func TestParseJSCPDJSON_WithDuplication(t *testing.T) {
	input := `{
  "statistics": {
    "total": {
      "percentage": 12.5
    }
  }
}`
	m := &Metrics{}
	if err := parseJSCPDJSON(input, m); err != nil {
		t.Fatalf("parseJSCPDJSON: %v", err)
	}
	if m.CodeQuality.DuplicationPct == nil {
		t.Fatal("DuplicationPct should not be nil")
	}
	if *m.CodeQuality.DuplicationPct != 12.5 {
		t.Errorf("DuplicationPct: got %v, want 12.5", *m.CodeQuality.DuplicationPct)
	}
}

func TestParseJSCPDJSON_NoDuplication(t *testing.T) {
	input := `{
  "statistics": {
    "total": {
      "percentage": 0
    }
  }
}`
	m := &Metrics{}
	if err := parseJSCPDJSON(input, m); err != nil {
		t.Fatalf("parseJSCPDJSON: %v", err)
	}
	if m.CodeQuality.DuplicationPct == nil {
		t.Fatal("DuplicationPct should not be nil for valid JSON with 0%")
	}
	if *m.CodeQuality.DuplicationPct != 0 {
		t.Errorf("DuplicationPct: got %v, want 0", *m.CodeQuality.DuplicationPct)
	}
}

// TestParseJSCPDJSON_NoJSON verifies that DuplicationPct stays NIL when jscpd
// produces no JSON (e.g. crashed or produced only progress text).
func TestParseJSCPDJSON_NoJSON(t *testing.T) {
	m := &Metrics{}
	if err := parseJSCPDJSON("", m); err != nil {
		t.Fatalf("parseJSCPDJSON: %v", err)
	}
	if m.CodeQuality.DuplicationPct != nil {
		t.Errorf("DuplicationPct should be nil for no-JSON output; got %v", *m.CodeQuality.DuplicationPct)
	}
}

func TestParseDepcheckJSON_TwoUnusedProdOneUnusedDev(t *testing.T) {
	input := `{
  "dependencies": ["lodash", "moment"],
  "devDependencies": ["jest"],
  "missing": {}
}`
	m := &Metrics{}
	if err := parseDepcheckJSON(input, m); err != nil {
		t.Fatalf("parseDepcheckJSON: %v", err)
	}
	if m.DeadCode.UnusedDeps == nil {
		t.Fatal("UnusedDeps should not be nil")
	}
	if *m.DeadCode.UnusedDeps != 3 {
		t.Errorf("UnusedDeps: got %d, want 3", *m.DeadCode.UnusedDeps)
	}
}

func TestParseDepcheckJSON_NoUnused(t *testing.T) {
	input := `{
  "dependencies": [],
  "devDependencies": [],
  "missing": {}
}`
	m := &Metrics{}
	if err := parseDepcheckJSON(input, m); err != nil {
		t.Fatalf("parseDepcheckJSON: %v", err)
	}
	if m.DeadCode.UnusedDeps == nil {
		t.Fatal("UnusedDeps should not be nil (0 != nil)")
	}
	if *m.DeadCode.UnusedDeps != 0 {
		t.Errorf("UnusedDeps: got %d, want 0", *m.DeadCode.UnusedDeps)
	}
}

// TestParseDepcheckJSON_NoJSON verifies that UnusedDeps stays NIL when
// depcheck produces no JSON output.
func TestParseDepcheckJSON_NoJSON(t *testing.T) {
	m := &Metrics{}
	if err := parseDepcheckJSON("", m); err != nil {
		t.Fatalf("parseDepcheckJSON: %v", err)
	}
	if m.DeadCode.UnusedDeps != nil {
		t.Errorf("UnusedDeps should be nil for no-JSON output; got %d", *m.DeadCode.UnusedDeps)
	}
}

func TestParseNPMAuditJSON(t *testing.T) {
	// Real npm audit v7 JSON structure (abbreviated).
	input := `{
  "auditReportVersion": 2,
  "vulnerabilities": {},
  "metadata": {
    "vulnerabilities": {
      "info": 0,
      "low": 1,
      "moderate": 3,
      "high": 2,
      "critical": 1,
      "total": 7
    },
    "dependencies": {
      "prod": 150,
      "dev": 50,
      "optional": 0,
      "peer": 0,
      "peerOptional": 0,
      "total": 200
    }
  }
}`
	m := &Metrics{}
	if err := parseNPMAuditJSON(input, m); err != nil {
		t.Fatalf("parseNPMAuditJSON: %v", err)
	}
	if m.Security.CVECountBySeverity == nil {
		t.Fatal("CVECountBySeverity should not be nil")
	}
	if m.Security.CVECountBySeverity["low"] != 1 {
		t.Errorf("low: got %d, want 1", m.Security.CVECountBySeverity["low"])
	}
	if m.Security.CVECountBySeverity["moderate"] != 3 {
		t.Errorf("moderate: got %d, want 3", m.Security.CVECountBySeverity["moderate"])
	}
	if m.Security.CVECountBySeverity["high"] != 2 {
		t.Errorf("high: got %d, want 2", m.Security.CVECountBySeverity["high"])
	}
	if m.Security.CVECountBySeverity["critical"] != 1 {
		t.Errorf("critical: got %d, want 1", m.Security.CVECountBySeverity["critical"])
	}
	// info=0 should not appear in the map.
	if _, ok := m.Security.CVECountBySeverity["info"]; ok {
		t.Error("info=0 should not appear in CVECountBySeverity")
	}
}

func TestParseNPMAuditJSON_Clean(t *testing.T) {
	// A clean project: all zeros → empty map (not nil).
	input := `{
  "auditReportVersion": 2,
  "vulnerabilities": {},
  "metadata": {
    "vulnerabilities": {
      "info": 0, "low": 0, "moderate": 0, "high": 0, "critical": 0, "total": 0
    },
    "dependencies": {"prod": 10, "dev": 5, "optional": 0, "peer": 0, "peerOptional": 0, "total": 15}
  }
}`
	m := &Metrics{}
	if err := parseNPMAuditJSON(input, m); err != nil {
		t.Fatalf("parseNPMAuditJSON: %v", err)
	}
	// Empty map (not nil) means "ran and found nothing".
	if m.Security.CVECountBySeverity == nil {
		t.Fatal("CVECountBySeverity should not be nil (empty map != nil)")
	}
	if len(m.Security.CVECountBySeverity) != 0 {
		t.Errorf("expected empty map; got %v", m.Security.CVECountBySeverity)
	}
}

// TestParseNPMAuditJSON_NoJSON verifies that CVECountBySeverity stays NIL
// when npm audit produces no JSON (tool failure, not a clean audit).
func TestParseNPMAuditJSON_NoJSON(t *testing.T) {
	m := &Metrics{}
	if err := parseNPMAuditJSON("", m); err != nil {
		t.Fatalf("parseNPMAuditJSON: %v", err)
	}
	if m.Security.CVECountBySeverity != nil {
		t.Errorf("CVECountBySeverity should be nil for no-JSON output; got %v", m.Security.CVECountBySeverity)
	}
}

// TestParseNPMAuditJSON_Unparseable verifies that CVECountBySeverity stays NIL
// when the JSON is malformed (cannot be parsed).
func TestParseNPMAuditJSON_Unparseable(t *testing.T) {
	m := &Metrics{}
	if err := parseNPMAuditJSON(`{ this is not valid json `, m); err != nil {
		t.Fatalf("parseNPMAuditJSON: %v", err)
	}
	if m.Security.CVECountBySeverity != nil {
		t.Errorf("CVECountBySeverity should be nil for unparseable JSON; got %v", m.Security.CVECountBySeverity)
	}
}

func TestParseRadonCC(t *testing.T) {
	// radon cc -s output sample.
	input := `
mymodule/app.py
    M 10:4 MyClass.method_a - A (2)
    M 20:4 MyClass.method_b - B (8)
    F 40:0 top_level_func - A (3)
mymodule/utils.py
    F 5:0 helper - A (1)
    F 50:0 complex_helper - C (15)
`
	m := &Metrics{}
	if err := parseRadonCC(input, m); err != nil {
		t.Fatalf("parseRadonCC: %v", err)
	}
	if m.CodeQuality.CyclomaticComplexityP90 == nil {
		t.Fatal("CyclomaticComplexityP90 should not be nil")
	}
	// scores: [2, 8, 3, 1, 15] → sorted [1, 2, 3, 8, 15]
	// rank = (90/100)*(5-1) = 3.6 → 8*(0.4) + 15*(0.6) = 3.2 + 9.0 = 12.2
	p90 := *m.CodeQuality.CyclomaticComplexityP90
	if p90 < 12.0 || p90 > 12.5 {
		t.Errorf("CyclomaticComplexityP90: got %v; expected ~12.2 (90th pct of [1,2,3,8,15])", p90)
	}
}

// TestParseRadonCC_Empty verifies that CyclomaticComplexityP90 stays NIL when
// radon produces no function scores — cannot compute a p90 from empty data.
func TestParseRadonCC_Empty(t *testing.T) {
	m := &Metrics{}
	if err := parseRadonCC("", m); err != nil {
		t.Fatalf("parseRadonCC empty: %v", err)
	}
	if m.CodeQuality.CyclomaticComplexityP90 != nil {
		t.Errorf("CyclomaticComplexityP90 should be nil for empty radon output; got %v",
			*m.CodeQuality.CyclomaticComplexityP90)
	}
}

func TestParseBanditJSON(t *testing.T) {
	// Real bandit -f json output structure (abbreviated).
	input := `{
  "errors": [],
  "generated_at": "2026-06-11T00:00:00Z",
  "metrics": {},
  "results": [
    {"issue_severity": "HIGH", "issue_confidence": "HIGH", "test_id": "B101", "issue_text": "Use of assert detected"},
    {"issue_severity": "MEDIUM", "issue_confidence": "MEDIUM", "test_id": "B105", "issue_text": "Hardcoded password"},
    {"issue_severity": "LOW", "issue_confidence": "HIGH", "test_id": "B110", "issue_text": "Try/except pass"}
  ]
}`
	m := &Metrics{}
	if err := parseBanditJSON(input, m); err != nil {
		t.Fatalf("parseBanditJSON: %v", err)
	}
	if m.Security.SASTFindingsBySeverity == nil {
		t.Fatal("SASTFindingsBySeverity should not be nil")
	}
	if m.Security.SASTFindingsBySeverity["high"] != 1 {
		t.Errorf("high: got %d, want 1", m.Security.SASTFindingsBySeverity["high"])
	}
	if m.Security.SASTFindingsBySeverity["medium"] != 1 {
		t.Errorf("medium: got %d, want 1", m.Security.SASTFindingsBySeverity["medium"])
	}
	if m.Security.SASTFindingsBySeverity["low"] != 1 {
		t.Errorf("low: got %d, want 1", m.Security.SASTFindingsBySeverity["low"])
	}
}

// TestParseBanditJSON_NoJSON verifies that SASTFindingsBySeverity stays NIL
// when bandit produces no JSON output.
func TestParseBanditJSON_NoJSON(t *testing.T) {
	m := &Metrics{}
	if err := parseBanditJSON("", m); err != nil {
		t.Fatalf("parseBanditJSON: %v", err)
	}
	if m.Security.SASTFindingsBySeverity != nil {
		t.Errorf("SASTFindingsBySeverity should be nil for no-JSON output; got %v",
			m.Security.SASTFindingsBySeverity)
	}
}

func TestParsePipAuditJSON(t *testing.T) {
	// Real pip-audit --format json output structure.
	input := `[
  {"name": "requests", "version": "2.25.0", "vulns": [{"id": "CVE-2023-32681"}, {"id": "CVE-2022-40897"}]},
  {"name": "flask", "version": "1.0", "vulns": []},
  {"name": "jinja2", "version": "2.11.3", "vulns": [{"id": "CVE-2020-28493"}]}
]`
	m := &Metrics{}
	if err := parsePipAuditJSON(input, m); err != nil {
		t.Fatalf("parsePipAuditJSON: %v", err)
	}
	if m.Security.VulnCount == nil {
		t.Fatal("VulnCount should not be nil")
	}
	if *m.Security.VulnCount != 3 {
		t.Errorf("VulnCount: got %d, want 3", *m.Security.VulnCount)
	}
}

func TestParsePipAuditJSON_Clean(t *testing.T) {
	input := `[
  {"name": "requests", "version": "2.31.0", "vulns": []},
  {"name": "flask", "version": "3.0.0", "vulns": []}
]`
	m := &Metrics{}
	if err := parsePipAuditJSON(input, m); err != nil {
		t.Fatalf("parsePipAuditJSON: %v", err)
	}
	if m.Security.VulnCount == nil {
		t.Fatal("VulnCount should not be nil (0 != nil)")
	}
	if *m.Security.VulnCount != 0 {
		t.Errorf("VulnCount: got %d, want 0", *m.Security.VulnCount)
	}
}

// TestParsePipAuditJSON_NoJSON verifies that VulnCount stays NIL when
// pip-audit produces no JSON output (tool failure or startup error).
func TestParsePipAuditJSON_NoJSON(t *testing.T) {
	m := &Metrics{}
	if err := parsePipAuditJSON("", m); err != nil {
		t.Fatalf("parsePipAuditJSON: %v", err)
	}
	if m.Security.VulnCount != nil {
		t.Errorf("VulnCount should be nil for no-JSON output; got %d", *m.Security.VulnCount)
	}
}

func TestParseCargoClippyJSON(t *testing.T) {
	// Real cargo clippy --message-format=json NDJSON output (abbreviated).
	input := `{"reason":"compiler-artifact","package_id":"foo 0.1.0","executable":null,"features":[],"filenames":[],"fresh":false}
{"reason":"compiler-message","package_id":"foo 0.1.0","message":{"rendered":"warning: unused variable\n","level":"warning","spans":[],"children":[]}}
{"reason":"compiler-message","package_id":"foo 0.1.0","message":{"rendered":"warning: dead code\n","level":"warning","spans":[],"children":[]}}
{"reason":"compiler-message","package_id":"foo 0.1.0","message":{"rendered":"error[E0308]: mismatched types\n","level":"error","spans":[],"children":[]}}
{"reason":"build-finished","success":false}`
	m := &Metrics{}
	if err := parseCargoClippyJSON(input, m); err != nil {
		t.Fatalf("parseCargoClippyJSON: %v", err)
	}
	if m.CodeQuality.LintFindings == nil {
		t.Fatal("LintFindings should not be nil")
	}
	// 2 warnings + 1 error = 3 total
	if *m.CodeQuality.LintFindings != 3 {
		t.Errorf("LintFindings: got %d, want 3", *m.CodeQuality.LintFindings)
	}
}

func TestParseCargoClippyJSON_Clean(t *testing.T) {
	// No compiler-message entries — clean project.
	input := `{"reason":"compiler-artifact","package_id":"foo 0.1.0","executable":null,"features":[],"filenames":[],"fresh":false}
{"reason":"build-finished","success":true}`
	m := &Metrics{}
	if err := parseCargoClippyJSON(input, m); err != nil {
		t.Fatalf("parseCargoClippyJSON: %v", err)
	}
	if m.CodeQuality.LintFindings == nil {
		t.Fatal("LintFindings should not be nil (0 != nil)")
	}
	if *m.CodeQuality.LintFindings != 0 {
		t.Errorf("LintFindings: got %d, want 0", *m.CodeQuality.LintFindings)
	}
}

func TestParseCargoAuditJSON(t *testing.T) {
	// Real cargo audit --json output structure (abbreviated).
	input := `{
  "database": {"advisory-count": 500, "last-commit": "abc"},
  "lockfile": {"dependency-count": 42},
  "settings": {},
  "vulnerabilities": {
    "found": true,
    "count": 2,
    "list": [
      {"advisory": {"id": "RUSTSEC-2021-0001"}},
      {"advisory": {"id": "RUSTSEC-2022-0002"}}
    ]
  }
}`
	m := &Metrics{}
	if err := parseCargoAuditJSON(input, m); err != nil {
		t.Fatalf("parseCargoAuditJSON: %v", err)
	}
	if m.Security.VulnCount == nil {
		t.Fatal("VulnCount should not be nil")
	}
	if *m.Security.VulnCount != 2 {
		t.Errorf("VulnCount: got %d, want 2", *m.Security.VulnCount)
	}
}

func TestParseCargoAuditJSON_Clean(t *testing.T) {
	input := `{
  "database": {"advisory-count": 500, "last-commit": "abc"},
  "lockfile": {"dependency-count": 10},
  "settings": {},
  "vulnerabilities": {"found": false, "count": 0, "list": []}
}`
	m := &Metrics{}
	if err := parseCargoAuditJSON(input, m); err != nil {
		t.Fatalf("parseCargoAuditJSON: %v", err)
	}
	if m.Security.VulnCount == nil {
		t.Fatal("VulnCount should not be nil (0 != nil)")
	}
	if *m.Security.VulnCount != 0 {
		t.Errorf("VulnCount: got %d, want 0", *m.Security.VulnCount)
	}
}

// TestParseCargoAuditJSON_NoJSON verifies that VulnCount stays NIL when
// cargo audit produces no JSON (tool startup error or missing Cargo.lock).
func TestParseCargoAuditJSON_NoJSON(t *testing.T) {
	m := &Metrics{}
	if err := parseCargoAuditJSON("", m); err != nil {
		t.Fatalf("parseCargoAuditJSON: %v", err)
	}
	if m.Security.VulnCount != nil {
		t.Errorf("VulnCount should be nil for no-JSON output; got %d", *m.Security.VulnCount)
	}
}

// TestCargoDeny_NoDuplicateVulnCount verifies that cargo-deny does NOT write
// to VulnCount (which cargo-audit owns), preventing double-counting.
// Instead it writes to LicenseRiskCount.
func TestCargoDeny_NoDuplicateVulnCount(t *testing.T) {
	// Find the cargo-deny Apply.
	var denyApply func(string, error, *Metrics) error
	for _, a := range rustAnalyzers {
		if a.Name == "cargo-deny" {
			denyApply = a.Apply
			break
		}
	}
	if denyApply == nil {
		t.Fatal("cargo-deny analyzer not found in rustAnalyzers")
	}

	// Simulate cargo-audit having already set VulnCount=2 for two advisories.
	m := &Metrics{}
	m.Security.VulnCount = intPtr(2)

	// cargo deny finds 1 license violation.
	cargodenOutput := `error[L001]: license not allowed
  --> Cargo.lock:5:1
   |
 5 | some-crate = "1.0.0"
`
	if err := denyApply(cargodenOutput, nil, m); err != nil {
		t.Fatalf("cargo-deny Apply: %v", err)
	}

	// VulnCount must still be 2 — cargo-deny must NOT touch it.
	if m.Security.VulnCount == nil || *m.Security.VulnCount != 2 {
		t.Errorf("VulnCount should still be 2 after cargo-deny; got %v", m.Security.VulnCount)
	}
	// LicenseRiskCount must be 1.
	if m.Security.LicenseRiskCount == nil {
		t.Fatal("LicenseRiskCount should not be nil after cargo-deny")
	}
	if *m.Security.LicenseRiskCount != 1 {
		t.Errorf("LicenseRiskCount: got %d, want 1", *m.Security.LicenseRiskCount)
	}
}

// TestCargoDeny_Clean verifies that LicenseRiskCount=0 when cargo deny
// reports no violations (parsed successfully, result is zero).
func TestCargoDeny_Clean(t *testing.T) {
	var denyApply func(string, error, *Metrics) error
	for _, a := range rustAnalyzers {
		if a.Name == "cargo-deny" {
			denyApply = a.Apply
			break
		}
	}
	if denyApply == nil {
		t.Fatal("cargo-deny analyzer not found")
	}

	m := &Metrics{}
	// No "error[" lines → zero violations.
	if err := denyApply("Checking advisories\nChecking licenses\n", nil, m); err != nil {
		t.Fatalf("cargo-deny Apply: %v", err)
	}
	if m.Security.LicenseRiskCount == nil {
		t.Fatal("LicenseRiskCount should not be nil (0 != nil)")
	}
	if *m.Security.LicenseRiskCount != 0 {
		t.Errorf("LicenseRiskCount: got %d, want 0", *m.Security.LicenseRiskCount)
	}
	// VulnCount untouched.
	if m.Security.VulnCount != nil {
		t.Errorf("VulnCount should be nil; got %d", *m.Security.VulnCount)
	}
}

// TestRuffApply_LintWarningDensity_NilWhenLOCUnknown mirrors the eslint test.
func TestRuffApply_LintWarningDensity_NilWhenLOCUnknown(t *testing.T) {
	m := &Metrics{} // TotalLOC nil
	var ruffApply func(string, error, *Metrics) error
	for _, a := range pythonAnalyzers {
		if a.Name == "ruff" {
			ruffApply = a.Apply
			break
		}
	}
	if ruffApply == nil {
		t.Fatal("ruff analyzer not found")
	}
	input := "src/app.py:1:1: E501 line too long (120 > 88 characters)"
	if err := ruffApply(input, nil, m); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if m.CodeQuality.LintFindings == nil {
		t.Fatal("LintFindings should not be nil")
	}
	if m.CodeQuality.LintWarningDensity != nil {
		t.Errorf("LintWarningDensity should be nil when LOC unknown; got %v", *m.CodeQuality.LintWarningDensity)
	}
}

// --- schema field null invariant for new fields ---

func TestNewSchemaFields_NullWhenNotMeasured(t *testing.T) {
	// All new fields should remain nil if no analyzer populated them.
	m := &Metrics{}
	if m.CodeQuality.LintWarningDensity != nil {
		t.Error("LintWarningDensity should default to nil")
	}
	if m.CodeQuality.DuplicationPct != nil {
		t.Error("DuplicationPct should default to nil")
	}
	if m.CodeQuality.CyclomaticComplexityP90 != nil {
		t.Error("CyclomaticComplexityP90 should default to nil")
	}
	if m.DeadCode.UnusedDeps != nil {
		t.Error("UnusedDeps should default to nil")
	}
	if m.Security.CVECountBySeverity != nil {
		t.Error("CVECountBySeverity should default to nil")
	}
	if m.Security.LicenseRiskCount != nil {
		t.Error("LicenseRiskCount should default to nil")
	}
}

// --- helpers ---

func assertContainsAnalyzer(t *testing.T, list []analyzer, name string) {
	t.Helper()
	for _, a := range list {
		if a.Name == name {
			return
		}
	}
	t.Errorf("analyzer list missing %q", name)
}

func assertNotContainsAnalyzer(t *testing.T, list []analyzer, name string) {
	t.Helper()
	for _, a := range list {
		if a.Name == name {
			t.Errorf("analyzer list should not contain %q", name)
			return
		}
	}
}
