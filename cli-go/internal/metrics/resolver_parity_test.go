package metrics

// resolver_parity_test.go — verifies that a metric path resolves identically
// in every consumer: gate (resolveMetricPath), format (getMetricByPath), and
// metricsdash (via the exported ResolveMetricPath).
//
// Fix for release-audit finding #4: the three diverging metric-path resolvers
// previously produced silent mismatches for paths like code_quality.duplication_pct.

import (
	"os/exec"
	"testing"
	"time"
)

// testPaths covers all metric categories and a representative sample of
// fields — including previously-missing ones like duplication_pct,
// cyclomatic_complexity_p90, and the security map paths.
var testPaths = []struct {
	path string
	// builderFn populates m with a non-nil value for the path so we can
	// verify a non-zero resolve result.
	builderFn func() *Metrics
	wantOK    bool
}{
	{
		path: "efficiency.total_cost_usd",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.Efficiency.TotalCostUSD = floatPtr(1.23)
			return m
		},
		wantOK: true,
	},
	{
		path: "efficiency.median_cost_per_task_usd",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.Efficiency.MedianCostPerTaskUSD = floatPtr(0.5)
			return m
		},
		wantOK: true,
	},
	{
		path: "code_quality.duplication_pct",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.CodeQuality.DuplicationPct = floatPtr(7.5)
			return m
		},
		wantOK: true,
	},
	{
		path: "code_quality.cyclomatic_complexity_p90",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.CodeQuality.CyclomaticComplexityP90 = floatPtr(12.0)
			return m
		},
		wantOK: true,
	},
	{
		path: "code_quality.lint_findings",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.CodeQuality.LintFindings = intPtr(5)
			return m
		},
		wantOK: true,
	},
	{
		path: "dead_code.dead_code_symbols",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.DeadCode.DeadCodeSymbols = intPtr(3)
			return m
		},
		wantOK: true,
	},
	{
		path: "dead_code.unused_exported_symbols", // schema alias
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.DeadCode.DeadCodeSymbols = intPtr(2)
			return m
		},
		wantOK: true,
	},
	{
		path: "security.secret_scan_hits",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.Security.SecretScanHits = intPtr(1)
			return m
		},
		wantOK: true,
	},
	{
		path: "security.cve_count_by_severity.critical",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.Security.CVECountBySeverity = map[string]int{"critical": 2}
			return m
		},
		wantOK: true,
	},
	{
		path: "test.coverage_pct",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.Test.CoveragePct = floatPtr(85.0)
			return m
		},
		wantOK: true,
	},
	{
		path: "test.test_pass_rate",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.Test.TestPassRate = floatPtr(0.99)
			return m
		},
		wantOK: true,
	},
	{
		path: "dispatch.first_try_success_rate",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.Dispatch.FirstTrySuccessRate = floatPtr(0.92)
			return m
		},
		wantOK: true,
	},
	{
		path: "model_routing.right_sized_pct",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.ModelRouting.RightSizedPct = floatPtr(0.75)
			return m
		},
		wantOK: true,
	},
	{
		path: "dora.deploy_frequency",
		builderFn: func() *Metrics {
			m := &Metrics{}
			m.DORA.DeployFrequency = intPtr(12)
			return m
		},
		wantOK: true,
	},
	{
		path:      "bogus.nonexistent",
		builderFn: func() *Metrics { return &Metrics{} },
		wantOK:    false,
	},
}

// TestResolverParity verifies that resolveMetricPath (gate, internal),
// getMetricByPath (format.go), and ResolveMetricPath (exported for
// metricsdash) all agree on whether a path is found and on the resolved value.
func TestResolverParity(t *testing.T) {
	for _, tc := range testPaths {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			m := tc.builderFn()

			// --- gate.go resolver (internal) ---
			gateVal, gateOK := resolveMetricPath(m, tc.path)
			if gateOK != tc.wantOK {
				t.Errorf("resolveMetricPath(%q): ok=%v; want %v", tc.path, gateOK, tc.wantOK)
			}

			// --- exported resolver (metricsdash consumer) ---
			exportedVal, exportedOK := ResolveMetricPath(m, tc.path)
			if exportedOK != tc.wantOK {
				t.Errorf("ResolveMetricPath(%q): ok=%v; want %v", tc.path, exportedOK, tc.wantOK)
			}
			if exportedOK && exportedVal != gateVal {
				t.Errorf("ResolveMetricPath(%q) = %v; resolveMetricPath = %v; mismatch",
					tc.path, exportedVal, gateVal)
			}

			// --- format.go resolver ---
			formatVal, formatLabel := getMetricByPath(m, tc.path)
			if tc.wantOK {
				if formatVal != gateVal {
					t.Errorf("getMetricByPath(%q) value=%v; resolveMetricPath=%v; mismatch",
						tc.path, formatVal, gateVal)
				}
				if formatLabel == "" {
					t.Errorf("getMetricByPath(%q) label is empty for a resolved path", tc.path)
				}
			} else {
				if formatVal != 0 || formatLabel != "" {
					t.Errorf("getMetricByPath(%q) should return 0/\"\" for unknown path; got %v/%q",
						tc.path, formatVal, formatLabel)
				}
			}
		})
	}
}

// TestResolverParity_NilNotMeasured verifies that a nil pointer field
// (not measured) is correctly reported as ok=false in all resolvers.
func TestResolverParity_NilNotMeasured(t *testing.T) {
	m := &Metrics{} // all fields nil

	paths := []string{
		"efficiency.total_cost_usd",
		"code_quality.duplication_pct",
		"test.coverage_pct",
		"security.secret_scan_hits",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			_, gateOK := resolveMetricPath(m, path)
			_, expOK := ResolveMetricPath(m, path)
			fmtVal, fmtLabel := getMetricByPath(m, path)

			if gateOK {
				t.Errorf("resolveMetricPath(%q) ok=true for nil field; want false", path)
			}
			if expOK {
				t.Errorf("ResolveMetricPath(%q) ok=true for nil field; want false", path)
			}
			if fmtVal != 0 || fmtLabel != "" {
				t.Errorf("getMetricByPath(%q) = %v/%q for nil field; want 0/\"\"",
					path, fmtVal, fmtLabel)
			}
		})
	}
}

// testTimeout is a short timeout for the TestAnalyzerTimeout test.
// Must be much less than the sleep duration (60s) but long enough to start.
const testTimeout = 50 * time.Millisecond

// TestAnalyzerTimeout verifies that a tool that takes longer than the timeout
// is killed and its status is set to "timeout" rather than "ok" or "error".
func TestAnalyzerTimeout(t *testing.T) {
	// Use a very short timeout and a tool that sleeps. If "sleep" is not
	// available, skip gracefully.
	sleepPath, err := findExec("sleep")
	if err != nil {
		t.Skipf("sleep not available: %v", err)
	}

	// Call runTool directly with a minimal timeout to verify the timeout path.
	_, toolErr := runTool(sleepPath, []string{"60"}, t.TempDir(), testTimeout)
	if !isDeadlineErr(toolErr) {
		t.Errorf("runTool with %v timeout: expected deadline error; got %v", testTimeout, toolErr)
	}
}

// findExec looks for an executable by name using exec.LookPath, then checks
// known absolute paths if LookPath is unsuccessful.
func findExec(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	// Try well-known absolute paths as a fallback (useful in restricted envs).
	for _, candidate := range []string{
		"/bin/" + name,
		"/usr/bin/" + name,
	} {
		if _, statErr := exec.LookPath(candidate); statErr == nil {
			return candidate, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}
