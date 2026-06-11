package metrics

// gate.go implements `yakos metrics gate`.
//
// # Budget file shape
//
//   mode: advisory          # advisory | enforce  (default advisory)
//   regression_pct: 5       # any budgeted metric this-% worse than previous is a breach
//   budgets:
//     security.secret_scan_hits: 0
//     security.cve_count_by_severity.critical: 0
//     test.coverage_pct: 70        # floor metric (breach if BELOW)
//     dead_code.unused_exported_symbols: 20
//     code_quality.duplication_pct: 5
//
// # Direction registry
//
// Metrics whose natural "good" direction is upward (floors — breach when
// value falls BELOW threshold) are listed in floorMetrics.  Every other
// budgeted metric is treated as a ceiling (breach when value rises ABOVE
// threshold).
//
// Floor metrics: test.coverage_pct, model_routing.right_sized_pct,
//                dispatch.first_try_success_rate, test.test_pass_rate
//                efficiency.first_try_success_rate (alias).
//
// Callers may also add an explicit `direction: floor` or
// `direction: ceiling` per-entry to override the registry.
//
// # Null handling
//
// A nil metric value means "not measured in this snapshot."  A nil value
// CANNOT breach any budget — it is silently skipped with an INFO line.
// Nil is never coerced to 0.
//
// # Exit codes
//
//   advisory (default): always 0, even if breaches exist.
//   enforce:            non-zero if any breach exists.
//   --advisory flag:    forces advisory regardless of file mode.
//   --enforce flag:     forces enforce regardless of file mode.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// budgetsFile is the relative path within a project for budgets.yaml.
const budgetsFile = ".yakos/metrics/budgets.yaml"

// GateExitError is returned by Run (via the gate subcommand) when enforce mode
// requires a non-zero exit.  Callers check for this type with errors.As and
// exit with the embedded code rather than printing an error message.
type GateExitError struct {
	Code int
}

func (e *GateExitError) Error() string {
	return fmt.Sprintf("metrics gate: enforce mode — %d budget breach(es) (exit %d)", e.Code, e.Code)
}

// gateExitError is the package-internal alias used inside Run.
type gateExitError = GateExitError

// BudgetsPath returns the absolute path to budgets.yaml for projectDir.
func BudgetsPath(projectDir string) string {
	return filepath.Join(projectDir, budgetsFile)
}

// BudgetEntry is a single metric budget parsed from the YAML.
type BudgetEntry struct {
	// Threshold is the numeric limit.
	Threshold float64
	// Direction is "ceiling" or "floor"; empty = inferred from registry.
	Direction string
}

// BudgetFile is the parsed shape of budgets.yaml.
type BudgetFile struct {
	Mode          string             `yaml:"mode"`           // advisory | enforce
	RegressionPct float64            `yaml:"regression_pct"` // percent, e.g. 5
	Budgets       map[string]float64 `yaml:"budgets"`        // metric-path -> threshold
	// Directions optionally overrides the inferred direction per path.
	// Key matches a key in Budgets; value is "ceiling" or "floor".
	Directions map[string]string `yaml:"directions"`
}

// GateConfig carries all inputs for RunGate.
type GateConfig struct {
	// BudgetsPath overrides the default budget file location.
	BudgetsPath string

	// ProjectDir overrides project directory resolution.
	ProjectDir string

	// HomeDir overrides os.Getenv("HOME").
	HomeDir string

	// StateDir overrides the yakos state directory.
	StateDir string

	// ForceAdvisory makes the gate always exit 0 (overrides file mode).
	ForceAdvisory bool

	// ForceEnforce makes the gate exit non-zero on breach (overrides file mode).
	ForceEnforce bool

	// EmitJSON emits machine-readable JSON instead of a table.
	EmitJSON bool

	// Collect triggers a fresh collect before gating (no --deep, no --skip-analyzers suppressed).
	Collect bool

	// Writer receives normal output.
	Writer io.Writer

	// ErrWriter receives advisory/info messages.
	ErrWriter io.Writer

	// GitRunner overrides git execution (for tests).
	GitRunner gitRunner
}

// BreachKind indicates whether a breach is absolute or regression-based.
type BreachKind string

const (
	BreachAbsolute   BreachKind = "absolute"
	BreachRegression BreachKind = "regression"
)

// GateBreach is a single budget breach.
type GateBreach struct {
	// MetricPath is the dot-path of the breaching metric.
	MetricPath string `json:"metric_path"`
	// Value is the current snapshot's value.
	Value float64 `json:"value"`
	// Threshold is the budget threshold (for absolute breaches).
	Threshold float64 `json:"threshold,omitempty"`
	// PreviousValue is the prior snapshot's value (for regression breaches).
	PreviousValue float64 `json:"previous_value,omitempty"`
	// RegressionPct is the regression percentage (for regression breaches).
	RegressionPct float64 `json:"regression_pct,omitempty"`
	// Kind is "absolute" or "regression".
	Kind BreachKind `json:"kind"`
	// Direction is "ceiling" or "floor".
	Direction string `json:"direction"`
}

// GateResult is the output of RunGate.
type GateResult struct {
	// Mode is "advisory" or "enforce" as resolved (after flag overrides).
	Mode string `json:"mode"`
	// Breaches lists all detected budget breaches.
	Breaches []GateBreach `json:"breaches"`
	// Skipped lists metric paths that were not measured (nil) and therefore skipped.
	Skipped []string `json:"skipped,omitempty"`
	// ExitCode is the recommended process exit code.
	ExitCode int `json:"exit_code"`
}

// floorMetrics lists metric paths that are floors: breach when value drops
// BELOW the threshold.  All other metrics are treated as ceilings.
var floorMetrics = map[string]bool{
	"test.coverage_pct":                 true,
	"test.test_pass_rate":               true,
	"model_routing.right_sized_pct":     true,
	"dispatch.first_try_success_rate":   true,
	"efficiency.first_try_success_rate": true, // alias
}

// isFloor returns true if the metric path is a floor metric.
func isFloor(path, directionOverride string) bool {
	if directionOverride != "" {
		return directionOverride == "floor"
	}
	return floorMetrics[path]
}

// RunGate implements `yakos metrics gate`.
//
// It returns a GateResult and the recommended exit code (0 or 1).
// It writes human-readable (or JSON) output to cfg.Writer.
// It never calls os.Exit itself.
func RunGate(cfg GateConfig) (GateResult, error) {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = os.Stderr
	}

	// Resolve project dir.
	projectDir := ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return GateResult{}, fmt.Errorf("metrics gate: cannot determine project dir")
		}
		projectDir = cwd
	}

	// Resolve budgets file path.
	budgetsPath := cfg.BudgetsPath
	if budgetsPath == "" {
		budgetsPath = BudgetsPath(projectDir)
	}

	// Load the budget file.
	bf, err := loadBudgetFile(budgetsPath)
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintln(w, "metrics gate: no budgets.yaml found — nothing to gate.")
		_, _ = fmt.Fprintf(w, "  Create one at: %s\n", budgetsPath)
		_, _ = fmt.Fprintf(w, "  See docs/metrics-budgets-example.yaml for reference.\n")
		return GateResult{Mode: "advisory", ExitCode: 0}, nil
	}
	if err != nil {
		return GateResult{}, fmt.Errorf("metrics gate: load budgets: %w", err)
	}

	// Optionally run a fresh collect.
	if cfg.Collect {
		stateDir := cfg.StateDir
		if stateDir == "" {
			home := cfg.HomeDir
			if home == "" {
				home = os.Getenv("HOME")
			}
			stateDir = ResolveStateDir(home)
		}
		runner := cfg.GitRunner
		if runner == nil {
			runner = realGitRunner{}
		}
		collectCfg := Config{
			Subcommand: "collect",
			Trigger:    "gate",
			ProjectDir: projectDir,
			StateDir:   stateDir,
			HomeDir:    cfg.HomeDir,
		}
		if _, err := runCollect(collectCfg, runner, nil, time.Now().UTC(), cfg.HomeDir, ew, ew); err != nil {
			return GateResult{}, fmt.Errorf("metrics gate: collect: %w", err)
		}
	}

	// Load history to find latest and previous snapshots.
	snaps, err := ReadHistory(projectDir)
	if err != nil {
		return GateResult{}, fmt.Errorf("metrics gate: read history: %w", err)
	}

	if len(snaps) == 0 {
		_, _ = fmt.Fprintln(w, "metrics gate: no snapshots found — run 'yakos metrics collect' first.")
		return GateResult{Mode: "advisory", ExitCode: 0}, nil
	}

	latest := &snaps[len(snaps)-1]
	var prev *Snapshot
	if len(snaps) >= 2 {
		p := snaps[len(snaps)-2]
		prev = &p
	}

	// Resolve effective mode.
	mode := bf.Mode
	if mode == "" {
		mode = "advisory"
	}
	if cfg.ForceAdvisory {
		mode = "advisory"
	}
	if cfg.ForceEnforce {
		mode = "enforce"
	}

	// Evaluate all budgets.
	var breaches []GateBreach
	var skipped []string

	for path, threshold := range bf.Budgets {
		dirOverride := ""
		if bf.Directions != nil {
			dirOverride = bf.Directions[path]
		}
		floor := isFloor(path, dirOverride)
		direction := "ceiling"
		if floor {
			direction = "floor"
		}

		// Resolve current value.
		curVal, ok := resolveMetricPath(&latest.Metrics, path)
		if !ok {
			// Metric not measured — skip, never breach.
			_, _ = fmt.Fprintf(ew, "metrics gate: INFO: metric %q not measured in latest snapshot — skipped\n", path)
			skipped = append(skipped, path)
			continue
		}

		// Check absolute threshold.
		if breachesAbsolute(curVal, threshold, floor) {
			breaches = append(breaches, GateBreach{
				MetricPath: path,
				Value:      curVal,
				Threshold:  threshold,
				Kind:       BreachAbsolute,
				Direction:  direction,
			})
		}

		// Check regression vs previous snapshot (when available and configured).
		if prev != nil && bf.RegressionPct > 0 {
			prevVal, prevOK := resolveMetricPath(&prev.Metrics, path)
			if prevOK {
				if pct, regressed := regressionPct(curVal, prevVal, floor); regressed && pct > bf.RegressionPct {
					breaches = append(breaches, GateBreach{
						MetricPath:    path,
						Value:         curVal,
						PreviousValue: prevVal,
						RegressionPct: pct,
						Kind:          BreachRegression,
						Direction:     direction,
					})
				}
			}
		}
	}

	exitCode := 0
	if mode == "enforce" && len(breaches) > 0 {
		exitCode = 1
	}

	result := GateResult{
		Mode:     mode,
		Breaches: breaches,
		Skipped:  skipped,
		ExitCode: exitCode,
	}

	// Emit output.
	if cfg.EmitJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return result, fmt.Errorf("metrics gate: encode JSON: %w", err)
		}
	} else {
		printGateResult(w, result, latest, bf)
	}

	return result, nil
}

// breachesAbsolute returns true if value breaches the threshold.
//
//	ceiling: breach when value > threshold
//	floor:   breach when value < threshold
func breachesAbsolute(value, threshold float64, floor bool) bool {
	if floor {
		return value < threshold
	}
	return value > threshold
}

// regressionPct returns the percentage worsening and whether it is a regression.
//
// For ceiling metrics, regression = current > previous (higher is worse).
// For floor metrics, regression = current < previous (lower is worse).
// Returns (0, false) when there is no regression.
func regressionPct(current, previous float64, floor bool) (float64, bool) {
	if previous == 0 {
		// Avoid division by zero; only flag regression if meaningful.
		if floor && current < previous {
			return 100, true
		}
		if !floor && current > previous {
			return 100, true
		}
		return 0, false
	}

	var delta float64
	if floor {
		// Regression when current dropped below previous.
		delta = previous - current // positive when regressed
	} else {
		// Regression when current rose above previous.
		delta = current - previous // positive when regressed
	}

	if delta <= 0 {
		return 0, false
	}

	pct := math.Abs(delta) / math.Abs(previous) * 100
	return pct, true
}

// loadBudgetFile parses a budgets.yaml file.
// Returns os.ErrNotExist-wrapped error when the file is absent.
func loadBudgetFile(path string) (BudgetFile, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return BudgetFile{}, err // preserve os.IsNotExist
	}
	var bf BudgetFile
	if err := yaml.Unmarshal(data, &bf); err != nil {
		return BudgetFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return bf, nil
}

// printGateResult writes the human-readable gate report to w.
func printGateResult(w io.Writer, r GateResult, latest *Snapshot, bf BudgetFile) {
	status := "PASS"
	if len(r.Breaches) > 0 {
		status = "FAIL"
	}

	_, _ = fmt.Fprintf(w, "yakos metrics gate — commit:%s  mode:%s\n", short(latest.Commit), r.Mode)
	_, _ = fmt.Fprintf(w, "result: %s  (%d breach(es), %d skipped)\n\n",
		status, len(r.Breaches), len(r.Skipped))

	if len(r.Breaches) == 0 {
		_, _ = fmt.Fprintln(w, "All budgets satisfied.")
		if r.Mode == "advisory" {
			_, _ = fmt.Fprintln(w, "(advisory mode — exit 0 regardless)")
		}
		return
	}

	// Column widths.
	const (
		colPath  = 46
		colVal   = 12
		colLimit = 12
		colKind  = 12
	)
	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %s",
		colPath, "metric",
		colVal, "value",
		colLimit, "limit/prev",
		colKind, "kind",
		"direction",
	)
	_, _ = fmt.Fprintln(w, header)
	_, _ = fmt.Fprintln(w, strings.Repeat("-", len(header)))

	for _, b := range r.Breaches {
		var limitStr string
		switch b.Kind {
		case BreachAbsolute:
			limitStr = formatGateFloat(b.Threshold)
		case BreachRegression:
			limitStr = fmt.Sprintf("prev:%.2f", b.PreviousValue)
		}
		_, _ = fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %s\n",
			colPath, b.MetricPath,
			colVal, formatGateFloat(b.Value),
			colLimit, limitStr,
			colKind, string(b.Kind),
			b.Direction,
		)
		if b.Kind == BreachRegression {
			_, _ = fmt.Fprintf(w, "%-*s  %.1f%% worse (budget: regression_pct=%.0f%%)\n",
				colPath, "",
				b.RegressionPct,
				bf.RegressionPct,
			)
		}
	}

	_, _ = fmt.Fprintln(w, "")
	if r.Mode == "advisory" {
		_, _ = fmt.Fprintln(w, "advisory mode — exit 0 (set mode: enforce in budgets.yaml to gate CI)")
	} else {
		_, _ = fmt.Fprintln(w, "enforce mode — exit non-zero")
	}
}

// formatGateFloat formats a float64 for gate table display.
func formatGateFloat(f float64) string {
	// If the number is an integer, omit the decimal.
	if f == math.Trunc(f) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.4g", f)
}

// resolveMetricPath resolves a dot-path into the Metrics struct.
//
// Supported paths:
//
//	efficiency.*            EfficiencyMetrics fields
//	dispatch.*              DispatchMetrics fields
//	model_routing.*         ModelRoutingMetrics fields
//	dora.*                  DORAMetrics fields
//	size_churn.*            SizeChurnMetrics fields
//	code_quality.*          CodeQualityMetrics fields
//	dead_code.*             DeadCodeMetrics fields
//	security.*              SecurityMetrics fields (incl. map paths)
//	test.*                  TestMetrics fields
//
// Map-valued fields (security.cve_count_by_severity.*, security.sast_findings_by_severity.*):
//
//	security.cve_count_by_severity.<severity>
//	security.sast_findings_by_severity.<severity>
//
// Returns (value, true) when measured; (0, false) when nil (not measured).
func resolveMetricPath(m *Metrics, path string) (float64, bool) {
	parts := splitDotPath(path)
	if len(parts) < 2 {
		return 0, false
	}

	switch parts[0] {
	case "efficiency":
		return resolveEfficiency(&m.Efficiency, parts[1:])
	case "dispatch":
		return resolveDispatch(&m.Dispatch, parts[1:])
	case "model_routing":
		return resolveModelRouting(&m.ModelRouting, parts[1:])
	case "dora":
		return resolveDORA(&m.DORA, parts[1:])
	case "size_churn":
		return resolveSizeChurn(&m.SizeChurn, parts[1:])
	case "code_quality":
		return resolveCodeQuality(&m.CodeQuality, parts[1:])
	case "dead_code":
		return resolveDeadCode(&m.DeadCode, parts[1:])
	case "security":
		return resolveSecurity(&m.Security, parts[1:])
	case "test":
		return resolveTest(&m.Test, parts[1:])
	}
	return 0, false
}

func splitDotPath(path string) []string {
	return strings.SplitN(path, ".", 2) // split into [category, rest]
}

func resolveEfficiency(e *EfficiencyMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "median_cost_per_task_usd":
		return derefFloat(e.MedianCostPerTaskUSD)
	case "mean_cost_per_task_usd":
		return derefFloat(e.MeanCostPerTaskUSD)
	case "total_cost_usd":
		return derefFloat(e.TotalCostUSD)
	case "task_count":
		return derefInt(e.TaskCount)
	case "median_tokens_per_task":
		return derefFloat(e.MedianTokensPerTask)
	case "mean_tokens_per_task":
		return derefFloat(e.MeanTokensPerTask)
	}
	return 0, false
}

func resolveDispatch(d *DispatchMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "first_try_success_rate":
		return derefFloat(d.FirstTrySuccessRate)
	case "hook_bypass_count":
		return derefInt(d.HookBypassCount)
	}
	return 0, false
}

func resolveModelRouting(mr *ModelRoutingMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "total_suggested_monthly_savings_usd":
		return derefFloat(mr.TotalSuggestedMonthlySavingsUSD)
	case "right_sized_pct":
		return derefFloat(mr.RightSizedPct)
	}
	return 0, false
}

func resolveDORA(d *DORAMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "deploy_frequency":
		return derefInt(d.DeployFrequency)
	case "lead_time_median_hours":
		return derefFloat(d.LeadTimeMedianHours)
	}
	return 0, false
}

func resolveSizeChurn(s *SizeChurnMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "total_loc":
		return derefInt(s.TotalLOC)
	case "file_count":
		return derefInt(s.FileCount)
	case "churn_lines_last_30":
		return derefInt(s.ChurnLinesLast30)
	}
	return 0, false
}

func resolveCodeQuality(c *CodeQualityMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "vet_findings":
		return derefInt(c.VetFindings)
	case "lint_findings":
		return derefInt(c.LintFindings)
	case "cyclomatic_max":
		return derefInt(c.CyclomaticMax)
	case "staticcheck_findings":
		return derefInt(c.StaticcheckFindings)
	case "lint_warning_density":
		return derefFloat(c.LintWarningDensity)
	case "duplication_pct":
		return derefFloat(c.DuplicationPct)
	case "cyclomatic_complexity_p90":
		return derefFloat(c.CyclomaticComplexityP90)
	}
	return 0, false
}

func resolveDeadCode(d *DeadCodeMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "dead_code_symbols":
		return derefInt(d.DeadCodeSymbols)
	case "unused_exported_symbols": // schema alias used in examples
		return derefInt(d.DeadCodeSymbols)
	case "unused_deps":
		return derefInt(d.UnusedDeps)
	}
	return 0, false
}

func resolveSecurity(s *SecurityMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	// Handle map paths: security.cve_count_by_severity.<severity>
	// The rest-of-path after the category split is a single string like
	// "cve_count_by_severity.critical".  We need to split it further.
	sub := parts[0]
	dotIdx := strings.Index(sub, ".")
	if dotIdx >= 0 {
		mapKey := sub[:dotIdx]
		severity := sub[dotIdx+1:]
		switch mapKey {
		case "cve_count_by_severity":
			if s.CVECountBySeverity == nil {
				return 0, false
			}
			v, ok := s.CVECountBySeverity[severity]
			if !ok {
				return 0, false
			}
			return float64(v), true
		case "sast_findings_by_severity":
			if s.SASTFindingsBySeverity == nil {
				return 0, false
			}
			v, ok := s.SASTFindingsBySeverity[severity]
			if !ok {
				return 0, false
			}
			return float64(v), true
		}
		return 0, false
	}

	switch sub {
	case "secret_scan_hits":
		return derefInt(s.SecretScanHits)
	case "vuln_count":
		return derefInt(s.VulnCount)
	case "license_risk_count":
		return derefInt(s.LicenseRiskCount)
	}
	return 0, false
}

func resolveTest(t *TestMetrics, parts []string) (float64, bool) {
	if len(parts) == 0 {
		return 0, false
	}
	switch parts[0] {
	case "coverage_pct":
		return derefFloat(t.CoveragePct)
	case "test_pass_rate":
		return derefFloat(t.TestPassRate)
	case "race_conditions_detected":
		return derefInt(t.RaceConditionsDetected)
	}
	return 0, false
}

// derefFloat safely dereferences a *float64.
// Returns (0, false) for nil (not measured).
func derefFloat(p *float64) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

// derefInt safely dereferences a *int as float64.
// Returns (0, false) for nil (not measured).
func derefInt(p *int) (float64, bool) {
	if p == nil {
		return 0, false
	}
	return float64(*p), true
}
