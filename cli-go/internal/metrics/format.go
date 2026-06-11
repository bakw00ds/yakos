package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// fmtFloat formats a *float64 as "%.2f" or "—" for nil.
func fmtFloat(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f", *p)
}

// fmtInt formats a *int as "%d" or "—" for nil.
func fmtInt(p *int) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *p)
}

// fmtPct formats a *float64 as a percentage or "—" for nil.
func fmtPct(p *float64) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", *p*100)
}

// deltaStr formats a delta between current and previous pointer values.
// Returns "" when either is nil (can't compute delta).
func deltaStr(cur, prev *float64) string {
	if cur == nil || prev == nil {
		return ""
	}
	d := *cur - *prev
	if d == 0 {
		return ""
	}
	if d > 0 {
		return fmt.Sprintf(" (+%.2f)", d)
	}
	return fmt.Sprintf(" (%.2f)", d)
}

// deltaIntStr is like deltaStr but for *int.
func deltaIntStr(cur, prev *int) string {
	if cur == nil || prev == nil {
		return ""
	}
	d := *cur - *prev
	if d == 0 {
		return ""
	}
	if d > 0 {
		return fmt.Sprintf(" (+%d)", d)
	}
	return fmt.Sprintf(" (%d)", d)
}

// PrintReport writes a human-readable report of the latest snapshot with
// delta vs previous to w. If snapshots is empty, prints a "no data" message.
func PrintReport(w io.Writer, snapshots []Snapshot, emitJSON bool) error {
	if len(snapshots) == 0 {
		_, _ = fmt.Fprintln(w, "No metrics snapshots found. Run: yakos metrics collect")
		return nil
	}

	latest := snapshots[len(snapshots)-1]
	var prev *Snapshot
	if len(snapshots) >= 2 {
		p := snapshots[len(snapshots)-2]
		prev = &p
	}

	if emitJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(latest)
	}

	_, _ = fmt.Fprintf(w, "yakos metrics report — %s\n", latest.Ts.Format("2006-01-02T15:04:05Z"))
	_, _ = fmt.Fprintf(w, "commit: %s  branch: %s  trigger: %s\n", short(latest.Commit), latest.Branch, latest.Trigger)
	_, _ = fmt.Fprintf(w, "profiles: %s\n\n", strings.Join(latest.Profiles, ", "))

	m := latest.Metrics
	var pm *Metrics
	if prev != nil {
		pm = &prev.Metrics
	}

	// prevInt / prevFloat64 safely dereference a field from the previous
	// Metrics snapshot.  They return nil when pm is nil (no prior snapshot),
	// giving "—" deltas rather than a panic.
	prevInt := func(f func(*Metrics) *int) *int {
		if pm == nil {
			return nil
		}
		return f(pm)
	}
	prevFloat64 := func(f func(*Metrics) *float64) *float64 {
		if pm == nil {
			return nil
		}
		return f(pm)
	}

	_, _ = fmt.Fprintln(w, "=== Efficiency ===")
	printRow(w, "  task_count", fmtInt(m.Efficiency.TaskCount), deltaIntStr(m.Efficiency.TaskCount, prevInt(func(p *Metrics) *int { return p.Efficiency.TaskCount })))
	printRow(w, "  total_cost_usd", fmtFloat(m.Efficiency.TotalCostUSD), deltaStr(m.Efficiency.TotalCostUSD, prevFloat64(func(p *Metrics) *float64 { return p.Efficiency.TotalCostUSD })))
	printRow(w, "  median_cost_per_task", fmtFloat(m.Efficiency.MedianCostPerTaskUSD), deltaStr(m.Efficiency.MedianCostPerTaskUSD, prevFloat64(func(p *Metrics) *float64 { return p.Efficiency.MedianCostPerTaskUSD })))
	printRow(w, "  mean_cost_per_task", fmtFloat(m.Efficiency.MeanCostPerTaskUSD), deltaStr(m.Efficiency.MeanCostPerTaskUSD, prevFloat64(func(p *Metrics) *float64 { return p.Efficiency.MeanCostPerTaskUSD })))
	printRow(w, "  median_tokens_per_task", fmtFloat(m.Efficiency.MedianTokensPerTask), deltaStr(m.Efficiency.MedianTokensPerTask, prevFloat64(func(p *Metrics) *float64 { return p.Efficiency.MedianTokensPerTask })))

	_, _ = fmt.Fprintln(w, "\n=== Dispatch ===")
	printRow(w, "  first_try_success_rate", fmtPct(m.Dispatch.FirstTrySuccessRate), "")
	printRow(w, "  hook_bypass_count", fmtInt(m.Dispatch.HookBypassCount), "")

	_, _ = fmt.Fprintln(w, "\n=== Model Routing ===")
	printRow(w, "  total_suggested_monthly_savings_usd", fmtFloat(m.ModelRouting.TotalSuggestedMonthlySavingsUSD), "")
	printRow(w, "  right_sized_pct", fmtPct(m.ModelRouting.RightSizedPct), "")

	_, _ = fmt.Fprintln(w, "\n=== DORA ===")
	printRow(w, "  deploy_frequency", fmtInt(m.DORA.DeployFrequency), "")
	printRow(w, "  lead_time_median_hours", fmtFloat(m.DORA.LeadTimeMedianHours), "")

	_, _ = fmt.Fprintln(w, "\n=== Size / Churn ===")
	printRow(w, "  total_loc", fmtInt(m.SizeChurn.TotalLOC), "")
	printRow(w, "  file_count", fmtInt(m.SizeChurn.FileCount), "")
	printRow(w, "  churn_lines_last_30", fmtInt(m.SizeChurn.ChurnLinesLast30), "")

	_, _ = fmt.Fprintln(w, "\n=== Code Quality ===")
	printRow(w, "  vet_findings", fmtInt(m.CodeQuality.VetFindings), "")
	printRow(w, "  lint_findings", fmtInt(m.CodeQuality.LintFindings), "")
	printRow(w, "  staticcheck_findings", fmtInt(m.CodeQuality.StaticcheckFindings), "")
	printRow(w, "  cyclomatic_max", fmtInt(m.CodeQuality.CyclomaticMax), "")

	_, _ = fmt.Fprintln(w, "\n=== Dead Code ===")
	printRow(w, "  dead_code_symbols", fmtInt(m.DeadCode.DeadCodeSymbols), "")

	_, _ = fmt.Fprintln(w, "\n=== Security ===")
	printRow(w, "  secret_scan_hits", fmtInt(m.Security.SecretScanHits), "")
	printRow(w, "  vuln_count", fmtInt(m.Security.VulnCount), "")
	if m.Security.SASTFindingsBySeverity != nil {
		for sev, n := range m.Security.SASTFindingsBySeverity {
			printRow(w, fmt.Sprintf("  sast[%s]", sev), fmt.Sprintf("%d", n), "")
		}
	} else {
		printRow(w, "  sast_findings_by_severity", "—", "")
	}

	_, _ = fmt.Fprintln(w, "\n=== Test ===")
	printRow(w, "  coverage_pct", fmtFloat(m.Test.CoveragePct), "")
	printRow(w, "  test_pass_rate", fmtPct(m.Test.TestPassRate), "")
	printRow(w, "  race_conditions_detected", fmtInt(m.Test.RaceConditionsDetected), "")

	if len(latest.ToolStatus) > 0 {
		_, _ = fmt.Fprintln(w, "\n=== Tool Status ===")
		for name, status := range latest.ToolStatus {
			printRow(w, "  "+name, status, "")
		}
	}

	return nil
}

func printRow(w io.Writer, label, value, delta string) {
	_, _ = fmt.Fprintf(w, "%-40s %s%s\n", label, value, delta)
}

func short(sha string) string {
	if len(sha) >= 8 {
		return sha[:8]
	}
	return sha
}

// sparkline returns a Unicode sparkline string for a series of float64 values.
func sparkline(vals []float64) string {
	if len(vals) == 0 {
		return "(no data)"
	}
	bars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	minV, maxV := vals[0], vals[0]
	for _, v := range vals {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	var sb strings.Builder
	for _, v := range vals {
		var idx int
		if maxV > minV {
			idx = int((v - minV) / (maxV - minV) * float64(len(bars)-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(bars) {
			idx = len(bars) - 1
		}
		sb.WriteRune(bars[idx])
	}
	return sb.String()
}

// PrintTrend writes a trend view of a specific metric path over the last N
// snapshots to w.
func PrintTrend(w io.Writer, snapshots []Snapshot, metricPath string, lastN int, sinceTS string) error {
	if len(snapshots) == 0 {
		_, _ = fmt.Fprintln(w, "No metrics snapshots found.")
		return nil
	}

	// Filter by since.
	var filtered []Snapshot
	for _, s := range snapshots {
		if sinceTS != "" {
			t, err := parseISO(sinceTS)
			if err == nil && s.Ts.Before(t) {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	if lastN > 0 && len(filtered) > lastN {
		filtered = filtered[len(filtered)-lastN:]
	}

	if len(filtered) == 0 {
		_, _ = fmt.Fprintln(w, "No snapshots match the filter.")
		return nil
	}

	vals, labels := extractMetricPath(filtered, metricPath)

	_, _ = fmt.Fprintf(w, "Trend: %s  (last %d snapshots)\n", metricPath, len(filtered))
	_, _ = fmt.Fprintf(w, "%s\n", sparkline(vals))
	_, _ = fmt.Fprintln(w, "")
	for i, s := range filtered {
		label := s.Ts.Format("2006-01-02")
		if i < len(labels) && labels[i] != "" {
			_, _ = fmt.Fprintf(w, "  %s  %s  commit:%s\n", label, labels[i], short(s.Commit))
		} else {
			_, _ = fmt.Fprintf(w, "  %s  —  commit:%s\n", label, short(s.Commit))
		}
	}
	return nil
}

// extractMetricPath extracts the named metric from each snapshot. Returns
// float values (for sparkline) and string labels (for table). Only the
// predefined known paths are supported in Phase-1.
func extractMetricPath(snaps []Snapshot, path string) (vals []float64, labels []string) {
	for _, s := range snaps {
		v, label := getMetricByPath(&s.Metrics, path)
		vals = append(vals, v)
		labels = append(labels, label)
	}
	return
}

// getMetricByPath extracts a float64 approximation and display label for the
// given dot-path metric. Returns 0.0, "" for unknown paths.
func getMetricByPath(m *Metrics, path string) (float64, string) {
	switch path {
	case "efficiency.median_cost_per_task_usd":
		if m.Efficiency.MedianCostPerTaskUSD != nil {
			return *m.Efficiency.MedianCostPerTaskUSD, fmt.Sprintf("%.4f", *m.Efficiency.MedianCostPerTaskUSD)
		}
	case "efficiency.total_cost_usd":
		if m.Efficiency.TotalCostUSD != nil {
			return *m.Efficiency.TotalCostUSD, fmt.Sprintf("%.2f", *m.Efficiency.TotalCostUSD)
		}
	case "dispatch.first_try_success_rate":
		if m.Dispatch.FirstTrySuccessRate != nil {
			return *m.Dispatch.FirstTrySuccessRate, fmt.Sprintf("%.2f%%", *m.Dispatch.FirstTrySuccessRate*100)
		}
	case "test.coverage_pct":
		if m.Test.CoveragePct != nil {
			return *m.Test.CoveragePct, fmt.Sprintf("%.1f%%", *m.Test.CoveragePct)
		}
	case "security.secret_scan_hits":
		if m.Security.SecretScanHits != nil {
			return float64(*m.Security.SecretScanHits), fmt.Sprintf("%d", *m.Security.SecretScanHits)
		}
	}
	return 0, ""
}

// PrintCompare writes a side-by-side comparison of two snapshots to w.
func PrintCompare(w io.Writer, snapshots []Snapshot, shaA, shaB string, emitJSON bool) error {
	var snapA, snapB *Snapshot
	for i := range snapshots {
		s := &snapshots[i]
		if strings.HasPrefix(s.Commit, shaA) {
			snapA = s
		}
		if strings.HasPrefix(s.Commit, shaB) {
			snapB = s
		}
	}
	if snapA == nil {
		return fmt.Errorf("snapshot for commit %q not found", shaA)
	}
	if snapB == nil {
		return fmt.Errorf("snapshot for commit %q not found", shaB)
	}

	if emitJSON {
		result := map[string]interface{}{
			"a": snapA,
			"b": snapB,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	_, _ = fmt.Fprintf(w, "compare  A=%s  B=%s\n\n", short(snapA.Commit), short(snapB.Commit))
	_, _ = fmt.Fprintf(w, "%-42s %-14s %-14s\n", "metric", short(snapA.Commit), short(snapB.Commit))
	_, _ = fmt.Fprintln(w, strings.Repeat("-", 72))

	type row struct {
		label string
		a, b  string
	}
	rows := []row{
		{"efficiency.task_count", fmtInt(snapA.Metrics.Efficiency.TaskCount), fmtInt(snapB.Metrics.Efficiency.TaskCount)},
		{"efficiency.total_cost_usd", fmtFloat(snapA.Metrics.Efficiency.TotalCostUSD), fmtFloat(snapB.Metrics.Efficiency.TotalCostUSD)},
		{"efficiency.median_cost_per_task", fmtFloat(snapA.Metrics.Efficiency.MedianCostPerTaskUSD), fmtFloat(snapB.Metrics.Efficiency.MedianCostPerTaskUSD)},
		{"dispatch.first_try_success_rate", fmtPct(snapA.Metrics.Dispatch.FirstTrySuccessRate), fmtPct(snapB.Metrics.Dispatch.FirstTrySuccessRate)},
		{"dispatch.hook_bypass_count", fmtInt(snapA.Metrics.Dispatch.HookBypassCount), fmtInt(snapB.Metrics.Dispatch.HookBypassCount)},
		{"model_routing.right_sized_pct", fmtPct(snapA.Metrics.ModelRouting.RightSizedPct), fmtPct(snapB.Metrics.ModelRouting.RightSizedPct)},
		{"dora.deploy_frequency", fmtInt(snapA.Metrics.DORA.DeployFrequency), fmtInt(snapB.Metrics.DORA.DeployFrequency)},
		{"size.total_loc", fmtInt(snapA.Metrics.SizeChurn.TotalLOC), fmtInt(snapB.Metrics.SizeChurn.TotalLOC)},
		{"code_quality.lint_findings", fmtInt(snapA.Metrics.CodeQuality.LintFindings), fmtInt(snapB.Metrics.CodeQuality.LintFindings)},
		{"security.secret_scan_hits", fmtInt(snapA.Metrics.Security.SecretScanHits), fmtInt(snapB.Metrics.Security.SecretScanHits)},
		{"test.coverage_pct", fmtFloat(snapA.Metrics.Test.CoveragePct), fmtFloat(snapB.Metrics.Test.CoveragePct)},
		{"test.test_pass_rate", fmtPct(snapA.Metrics.Test.TestPassRate), fmtPct(snapB.Metrics.Test.TestPassRate)},
	}
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%-42s %-14s %-14s\n", r.label, r.a, r.b)
	}
	return nil
}
