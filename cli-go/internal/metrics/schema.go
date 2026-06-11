// Package metrics implements the `yakos metrics` command.
//
// It records a per-project, commit-keyed time series of code-quality,
// effectiveness/efficiency, dead-code, and security signals.
//
// # Architecture
//
// Two collector classes:
//
//   - [E] collectors read existing yakOS artifacts (dispatch log, git, state
//     files) with no additional tool invocations. These run on every collect.
//   - [T] analyzer collectors invoke external tools (go test, golangci-lint,
//     etc.). They are skipped when --skip-analyzers is set, and gracefully
//     degrade when a tool is absent (metric → null, status → tool-missing).
//
// # Storage
//
// Snapshots are appended to <project>/.yakos/metrics/history.ndjson, one
// JSON line per snapshot. This is an append-only NDJSON file committed to
// the project repo (see ADR-0001).
//
// # Null vs 0
//
// Metric fields are pointer types (*float64, *int, *bool). A nil pointer
// serialises as JSON null (not measured). A non-nil pointer with value 0
// serialises as 0 (measured, result is zero). Never coerce nil→0.
package metrics

import (
	"time"

	"github.com/bakw00ds/yakos/internal/stackdetect"
)

const schemaVersion = "yakos.metrics/v1"

// Snapshot is a single point-in-time metrics observation for a project.
// All metric fields use pointer types so null (not measured) is distinct
// from 0 (measured, result is zero).
type Snapshot struct {
	Schema   string    `json:"schema"`
	Ts       time.Time `json:"ts"`
	Commit   string    `json:"commit"`
	Branch   string    `json:"branch"`
	Trigger  string    `json:"trigger"` // manual | git-hook | ci | release
	Deep     bool      `json:"deep"`    // Phase-2 LLM collectors flag
	Profiles []string  `json:"profiles"`
	Metrics  Metrics   `json:"metrics"`

	// ToolStatus maps tool name → status string.
	// Values: "ok" | "tool-missing" | "error".
	ToolStatus map[string]string `json:"tool_status,omitempty"`
}

// Metrics holds all measured signals grouped by category.
type Metrics struct {
	Efficiency   EfficiencyMetrics   `json:"efficiency"`
	Dispatch     DispatchMetrics     `json:"dispatch"`
	ModelRouting ModelRoutingMetrics `json:"model_routing"`
	DORA         DORAMetrics         `json:"dora"`
	Drift        DriftMetrics        `json:"drift"`
	SizeChurn    SizeChurnMetrics    `json:"size_churn"`
	CodeQuality  CodeQualityMetrics  `json:"code_quality"`
	DeadCode     DeadCodeMetrics     `json:"dead_code"`
	Security     SecurityMetrics     `json:"security"`
	Test         TestMetrics         `json:"test"`
}

// EfficiencyMetrics captures AI agent cost and token efficiency.
type EfficiencyMetrics struct {
	MedianCostPerTaskUSD *float64 `json:"median_cost_per_task_usd"`
	MeanCostPerTaskUSD   *float64 `json:"mean_cost_per_task_usd"`
	TotalCostUSD         *float64 `json:"total_cost_usd"`
	TaskCount            *int     `json:"task_count"`
	MedianTokensPerTask  *float64 `json:"median_tokens_per_task"`
	MeanTokensPerTask    *float64 `json:"mean_tokens_per_task"`
}

// DispatchMetrics captures dispatching patterns.
type DispatchMetrics struct {
	// FirstTrySuccessRate is the fraction of dispatch_finished events with
	// exit_code==0. This is a lossy proxy for "first-try success" — a true
	// grouping would require a per-task correlation id which does not exist
	// in the current dispatch log schema. null if no events in window.
	FirstTrySuccessRate *float64 `json:"first_try_success_rate"`

	// HookBypassCount is the count of non-expired bypass entries in
	// work/current/hook-bypass.md. null if the file is absent.
	HookBypassCount *int `json:"hook_bypass_count"`
}

// ModelRoutingMetrics captures model routing efficiency signals.
type ModelRoutingMetrics struct {
	// TotalSuggestedMonthlySavingsUSD is the sum of
	// estimated_monthly_savings_usd across all open downgrade candidates.
	// null if the candidates file is absent.
	TotalSuggestedMonthlySavingsUSD *float64 `json:"total_suggested_monthly_savings_usd"`

	// RightSizedPct is the fraction of agents WITHOUT an open downgrade
	// candidate (i.e. agents already on the recommended model).
	// An agent with a suggested cheaper model is NOT right-sized.
	// null if the candidates file is absent.
	RightSizedPct *float64 `json:"right_sized_pct"`
}

// DORAMetrics captures DORA-style delivery metrics.
type DORAMetrics struct {
	// DeployFrequency is the number of semver git tags (^v\d+\.\d+\.\d+\.\d+$)
	// in the measurement window. null if no tags are present.
	DeployFrequency *int `json:"deploy_frequency"`

	// LeadTimeMedianHours is the median time from commit to first containing
	// tag, in hours. null if tags are absent or ambiguous.
	LeadTimeMedianHours *float64 `json:"lead_time_median_hours"`
}

// DriftMetrics captures config-drift and mistake-recurrence signals.
// Both fields are null in Phase-1 MVP — no machine-readable artifact
// exists for these. Fields are preserved in the schema for Phase-2.
type DriftMetrics struct {
	// DriftIncidents is always null in Phase-1 (prose-only drift-report.md).
	DriftIncidents *int `json:"drift_incidents"`

	// MistakeRecurrence is always null in Phase-1 (retro-dispatch.ndjson is
	// a hook-exec log, not a structured mistake catalog).
	MistakeRecurrence *int `json:"mistake_recurrence"`
}

// SizeChurnMetrics captures codebase size and churn.
type SizeChurnMetrics struct {
	TotalLOC  *int `json:"total_loc"`
	FileCount *int `json:"file_count"`
	// ChurnLinesLast30 is the sum of added+deleted lines from git numstat
	// over the last 30 commits.
	ChurnLinesLast30 *int `json:"churn_lines_last_30"`
}

// CodeQualityMetrics captures linter / vet findings.
type CodeQualityMetrics struct {
	VetFindings         *int `json:"vet_findings"`
	LintFindings        *int `json:"lint_findings"`
	CyclomaticMax       *int `json:"cyclomatic_max"`
	StaticcheckFindings *int `json:"staticcheck_findings"`

	// LintWarningDensity is the number of lint warnings per 100 lines of source
	// code. Populated by profile analyzers that distinguish warning-level findings
	// (eslint, ruff). null when not measured.
	LintWarningDensity *float64 `json:"lint_warning_density"`

	// DuplicationPct is the percentage of cloned / duplicated code as reported
	// by jscpd (node profile). null when not measured.
	DuplicationPct *float64 `json:"duplication_pct"`

	// CyclomaticComplexityP90 is the 90th-percentile cyclomatic complexity score
	// across all functions as reported by radon cc (python profile). null when
	// not measured.
	CyclomaticComplexityP90 *float64 `json:"cyclomatic_complexity_p90"`
}

// DeadCodeMetrics captures dead-code signals.
type DeadCodeMetrics struct {
	DeadCodeSymbols *int `json:"dead_code_symbols"`

	// UnusedDeps is the count of declared dependencies that are not imported /
	// referenced by any source file. Populated by depcheck (node), deptry
	// (python), cargo-machete / cargo-udeps (rust). null when not measured.
	UnusedDeps *int `json:"unused_deps"`
}

// SecurityMetrics captures security scan results.
type SecurityMetrics struct {
	// SecretScanHits is the count of secrets found by gitleaks.
	// A measured 0 (empty scan) is distinct from null (tool absent).
	SecretScanHits *int `json:"secret_scan_hits"`

	// SASTFindingsBySeverity maps severity label → count from gosec.
	// nil means gosec was not run (tool absent); an empty map means
	// gosec ran and found nothing.
	SASTFindingsBySeverity map[string]int `json:"sast_findings_by_severity,omitempty"`

	// VulnCount is the count of known vulnerabilities from govulncheck (go),
	// pip-audit (python), or cargo audit (rust). cargo-audit is the sole owner
	// of this field for the rust profile; cargo-deny violations are routed to
	// LicenseRiskCount to avoid double-counting the same advisory.
	VulnCount *int `json:"vuln_count"`

	// CVECountBySeverity maps severity label → count of CVEs, as reported by
	// npm audit --json (node profile). Severity labels are the npm audit
	// severity strings: "critical", "high", "moderate", "low", "info".
	// nil means npm audit was not run (tool absent); empty map means it ran
	// and found no vulnerabilities.
	CVECountBySeverity map[string]int `json:"cve_count_by_severity,omitempty"`

	// LicenseRiskCount is the count of license-policy and advisory-ban
	// violations reported by cargo deny check (rust profile). Kept separate
	// from VulnCount (which cargo-audit owns) to avoid double-counting the same
	// RUSTSEC advisory. null when cargo deny was not run or could not be parsed.
	LicenseRiskCount *int `json:"license_risk_count"`
}

// TestMetrics captures test coverage and results.
type TestMetrics struct {
	// CoveragePct is the overall test coverage percentage from go test -cover.
	// null if go test was not run (tool absent).
	CoveragePct *float64 `json:"coverage_pct"`

	// TestPassRate is the fraction of tests that passed (1.0 = all pass).
	TestPassRate *float64 `json:"test_pass_rate"`

	// RaceConditionsDetected is non-null when go test -race was run.
	RaceConditionsDetected *int `json:"race_conditions_detected"`
}

// newSnapshot returns a Snapshot with the schema field pre-populated and
// ToolStatus initialised to an empty map.
func newSnapshot(ts time.Time, commit, branch, trigger string, profiles []stackdetect.Profile) Snapshot {
	profileStrs := make([]string, len(profiles))
	for i, p := range profiles {
		profileStrs[i] = string(p)
	}
	return Snapshot{
		Schema:     schemaVersion,
		Ts:         ts,
		Commit:     commit,
		Branch:     branch,
		Trigger:    trigger,
		Deep:       false,
		Profiles:   profileStrs,
		ToolStatus: make(map[string]string),
	}
}
