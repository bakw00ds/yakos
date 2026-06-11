package metricsdash

import (
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/metrics"
)

// SnapshotHeader is the minimal header returned in GET /api/metrics/history.
type SnapshotHeader struct {
	Commit string    `json:"commit"`
	Ts     time.Time `json:"ts"`
	Branch string    `json:"branch"`
}

// TrendPoint is one data point in a trend series.
type TrendPoint struct {
	Ts     time.Time `json:"ts"`
	Commit string    `json:"commit"`
	Value  *float64  `json:"value"` // nil when metric not measured at that snapshot
}

// CompareDiff is the shape for GET /api/metrics/compare.
type CompareDiff struct {
	A *metrics.Snapshot `json:"a"`
	B *metrics.Snapshot `json:"b"`
}

// ProjectSummary is one entry in the cross-project rollup.
type ProjectSummary struct {
	Project       string            `json:"project"`
	HistoryPath   string            `json:"history_path"`
	SnapshotCount int               `json:"snapshot_count"`
	Latest        *metrics.Snapshot `json:"latest"` // nil if no snapshots
}

// HistoryHeaders extracts commit+ts headers from a snapshot slice.
func HistoryHeaders(snaps []metrics.Snapshot) []SnapshotHeader {
	if len(snaps) == 0 {
		return []SnapshotHeader{}
	}
	out := make([]SnapshotHeader, len(snaps))
	for i, s := range snaps {
		out[i] = SnapshotHeader{
			Commit: s.Commit,
			Ts:     s.Ts,
			Branch: s.Branch,
		}
	}
	return out
}

// LatestSnapshot returns the last snapshot or nil if empty.
func LatestSnapshot(snaps []metrics.Snapshot) *metrics.Snapshot {
	if len(snaps) == 0 {
		return nil
	}
	s := snaps[len(snaps)-1]
	return &s
}

// TrendSeries extracts a metric trend over the last lastN snapshots since
// sinceTS. metricPath is a dot-path like "efficiency.total_cost_usd".
func TrendSeries(snaps []metrics.Snapshot, metricPath string, lastN int, sinceTS string) []TrendPoint {
	// Filter by since.
	var filtered []metrics.Snapshot
	for _, s := range snaps {
		if sinceTS != "" {
			t, err := parseISO(sinceTS)
			if err == nil && s.Ts.Before(t) {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	// Apply lastN limit.
	if lastN > 0 && len(filtered) > lastN {
		filtered = filtered[len(filtered)-lastN:]
	}

	if len(filtered) == 0 {
		return []TrendPoint{}
	}

	out := make([]TrendPoint, len(filtered))
	for i, s := range filtered {
		val := getMetricByPath(&s.Metrics, metricPath)
		out[i] = TrendPoint{
			Ts:     s.Ts,
			Commit: s.Commit,
			Value:  val,
		}
	}
	return out
}

// CompareSnapshots finds two snapshots by commit SHA prefix and returns a diff.
// Returns nil, nil if both are not found (callers should 404).
func CompareSnapshots(snaps []metrics.Snapshot, shaA, shaB string) (*metrics.Snapshot, *metrics.Snapshot) {
	var snapA, snapB *metrics.Snapshot
	for i := range snaps {
		s := &snaps[i]
		if strings.HasPrefix(s.Commit, shaA) {
			snapA = s
		}
		if strings.HasPrefix(s.Commit, shaB) {
			snapB = s
		}
	}
	return snapA, snapB
}

// getMetricByPath extracts a *float64 for the given dot-path.
// Returns nil when the metric was not measured.
func getMetricByPath(m *metrics.Metrics, path string) *float64 {
	switch path {
	case "efficiency.total_cost_usd":
		return m.Efficiency.TotalCostUSD
	case "efficiency.median_cost_per_task_usd":
		return m.Efficiency.MedianCostPerTaskUSD
	case "efficiency.mean_cost_per_task_usd":
		return m.Efficiency.MeanCostPerTaskUSD
	case "efficiency.median_tokens_per_task":
		return m.Efficiency.MedianTokensPerTask
	case "dispatch.first_try_success_rate":
		return m.Dispatch.FirstTrySuccessRate
	case "model_routing.right_sized_pct":
		return m.ModelRouting.RightSizedPct
	case "model_routing.total_suggested_monthly_savings_usd":
		return m.ModelRouting.TotalSuggestedMonthlySavingsUSD
	case "dora.lead_time_median_hours":
		return m.DORA.LeadTimeMedianHours
	case "test.coverage_pct":
		return m.Test.CoveragePct
	case "test.test_pass_rate":
		return m.Test.TestPassRate
	}
	return nil
}

// parseISO is a local copy matching metrics.parseISO to avoid cross-package
// unexported access.
func parseISO(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		time.RFC3339Nano,
		"2006-01-02T15:04:05-0700",
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errBadTime("cannot parse time: " + s)
}

type errBadTime string

func (e errBadTime) Error() string { return string(e) }
