package perfdash

import (
	"fmt"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
)

// ---- parseWindow ------------------------------------------------------------

func TestParseWindow_Standard(t *testing.T) {
	cases := []struct {
		input string
		want  time.Duration
	}{
		{"1h", time.Hour},
		{"6h", 6 * time.Hour},
		{"12h", 12 * time.Hour},
		{"24h", 24 * time.Hour},
		{"48h", 48 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"", 24 * time.Hour},         // default
		{"bogus", 24 * time.Hour},    // unknown → default
	}
	for _, tc := range cases {
		got := parseWindow(tc.input)
		if got != tc.want {
			t.Errorf("parseWindow(%q)=%v; want %v", tc.input, got, tc.want)
		}
	}
}

// ---- eventCostUSD -----------------------------------------------------------

func TestEventCostUSD_UsageField(t *testing.T) {
	ev := cost.Event{
		Usage: &cost.Usage{TotalCostUSD: 0.123},
	}
	got := eventCostUSD(ev)
	if got != 0.123 {
		t.Errorf("got %f; want 0.123", got)
	}
}

func TestEventCostUSD_FallbackTokens(t *testing.T) {
	ev := cost.Event{
		EstInputTokens:  1_000_000, // $3.00
		EstOutputTokens: 0,
	}
	got := eventCostUSD(ev)
	if got < 2.9 || got > 3.1 {
		t.Errorf("got %f; want ~3.0 (1M input tokens)", got)
	}
}

func TestEventCostUSD_ZeroWhenNoData(t *testing.T) {
	ev := cost.Event{}
	got := eventCostUSD(ev)
	if got != 0 {
		t.Errorf("got %f; want 0", got)
	}
}

// ---- eventLatencyMs ---------------------------------------------------------

func TestEventLatencyMs_UsageField(t *testing.T) {
	ev := cost.Event{Usage: &cost.Usage{DurationMs: 5432}}
	got := eventLatencyMs(ev)
	if got != 5432 {
		t.Errorf("got %d; want 5432", got)
	}
}

func TestEventLatencyMs_FallbackDurationS(t *testing.T) {
	ev := cost.Event{DurationS: 12.5}
	got := eventLatencyMs(ev)
	if got != 12500 {
		t.Errorf("got %d; want 12500", got)
	}
}

// ---- percentile -------------------------------------------------------------

func TestPercentile_Empty(t *testing.T) {
	got := percentile(nil, 50)
	if got != 0 {
		t.Errorf("got %d; want 0 for empty slice", got)
	}
}

func TestPercentile_P50(t *testing.T) {
	s := []int64{1, 2, 3, 4, 5}
	got := percentile(s, 50)
	if got < 2 || got > 3 {
		t.Errorf("p50=%d; want ~2-3 for [1,2,3,4,5]", got)
	}
}

func TestPercentile_P100(t *testing.T) {
	s := []int64{10, 20, 30}
	got := percentile(s, 100)
	if got != 30 {
		t.Errorf("p100=%d; want 30", got)
	}
}

func TestPercentile_P0(t *testing.T) {
	s := []int64{10, 20, 30}
	got := percentile(s, 0)
	if got != 10 {
		t.Errorf("p0=%d; want 10", got)
	}
}

// ---- ComputeSummary ---------------------------------------------------------

func makeEvent(ts, agent, runtime string, durationS, costUSD float64) cost.Event {
	return cost.Event{
		Type:      "dispatch_finished",
		Ts:        ts,
		Agent:     agent,
		Runtime:   runtime,
		Project:   "proj",
		DurationS: durationS,
		Usage:     &cost.Usage{TotalCostUSD: costUSD, DurationMs: int64(durationS * 1000)},
	}
}

func nowRFC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func TestComputeSummary_Empty(t *testing.T) {
	s := ComputeSummary(nil, 5)
	if s.TotalDispatches != 0 {
		t.Errorf("TotalDispatches=%d; want 0", s.TotalDispatches)
	}
	if s.TopAgents == nil {
		t.Error("TopAgents should not be nil")
	}
}

func TestComputeSummary_Counts(t *testing.T) {
	evs := []cost.Event{
		makeEvent(nowRFC(), "a", "claude", 60, 0.05),
		makeEvent(nowRFC(), "b", "gemini", 30, 0.02),
		makeEvent(nowRFC(), "a", "claude", 90, 0.07),
	}
	s := ComputeSummary(evs, 5)
	if s.TotalDispatches != 3 {
		t.Errorf("TotalDispatches=%d; want 3", s.TotalDispatches)
	}
	if s.TotalCostUSD == 0 {
		t.Error("TotalCostUSD should be > 0")
	}
	if s.AvgLatencyMs == 0 {
		t.Error("AvgLatencyMs should be > 0")
	}
}

func TestComputeSummary_TopAgentsLimit(t *testing.T) {
	evs := make([]cost.Event, 10)
	for i := range evs {
		evs[i] = makeEvent(nowRFC(), fmt.Sprintf("agent%d", i), "claude", 10, 0.01)
	}
	s := ComputeSummary(evs, 5)
	if len(s.TopAgents) > 5 {
		t.Errorf("len(TopAgents)=%d; want ≤5 with topN=5", len(s.TopAgents))
	}
}

func TestComputeSummary_TopRuntimesLimit(t *testing.T) {
	evs := make([]cost.Event, 6)
	runtimes := []string{"a", "b", "c", "d", "e", "f"}
	for i, r := range runtimes {
		evs[i] = makeEvent(nowRFC(), "agent", r, 10, 0.01)
	}
	s := ComputeSummary(evs, 5)
	if len(s.TopRuntimes) > 3 {
		t.Errorf("len(TopRuntimes)=%d; want ≤3", len(s.TopRuntimes))
	}
}

// ---- ComputeTimeseries ------------------------------------------------------

func TestComputeTimeseries_Empty(t *testing.T) {
	points := ComputeTimeseries(nil, 24*time.Hour, time.Hour, "dispatches")
	if points == nil {
		t.Error("ComputeTimeseries should return [] not nil for empty events")
	}
}

func TestComputeTimeseries_HourlyBuckets(t *testing.T) {
	evs := []cost.Event{
		makeEvent(nowRFC(), "a", "claude", 10, 0.01),
	}
	points := ComputeTimeseries(evs, 6*time.Hour, time.Hour, "dispatches")
	if len(points) < 6 {
		t.Errorf("len(points)=%d; want ≥6 for 6h window hourly", len(points))
	}
}

func TestComputeTimeseries_CostMetric(t *testing.T) {
	evs := []cost.Event{
		makeEvent(nowRFC(), "a", "claude", 10, 1.5),
	}
	points := ComputeTimeseries(evs, 24*time.Hour, time.Hour, "cost")
	var total float64
	for _, p := range points {
		total += p.Value
	}
	// The 1.5 cost should appear in one bucket.
	if total < 1.4 || total > 1.6 {
		t.Errorf("total cost across buckets=%f; want ~1.5", total)
	}
}

func TestComputeTimeseries_LatencyMetric(t *testing.T) {
	evs := []cost.Event{
		makeEvent(nowRFC(), "a", "claude", 60, 0.01), // 60000ms
	}
	points := ComputeTimeseries(evs, 24*time.Hour, time.Hour, "latency")
	var hasNonZero bool
	for _, p := range points {
		if p.Value > 0 {
			hasNonZero = true
			break
		}
	}
	if !hasNonZero {
		t.Error("latency timeseries should have at least one non-zero bucket")
	}
}

func TestComputeTimeseries_PointsHaveTs(t *testing.T) {
	evs := []cost.Event{makeEvent(nowRFC(), "a", "claude", 10, 0.01)}
	points := ComputeTimeseries(evs, 2*time.Hour, time.Hour, "dispatches")
	for _, p := range points {
		if p.Ts == "" {
			t.Error("timeseries point should have non-empty ts")
		}
	}
}

// ---- ComputeByAxis ----------------------------------------------------------

func TestComputeByAxis_ByAgent(t *testing.T) {
	evs := []cost.Event{
		makeEvent(nowRFC(), "backend", "claude", 60, 0.05),
		makeEvent(nowRFC(), "backend", "claude", 30, 0.02),
		makeEvent(nowRFC(), "frontend", "gemini", 10, 0.01),
	}
	rows := ComputeByAxis(evs, "agent")
	if len(rows) != 2 {
		t.Fatalf("rows=%d; want 2 agents", len(rows))
	}
	if rows[0].Key != "backend" {
		t.Errorf("rows[0].Key=%q; want backend (most dispatches)", rows[0].Key)
	}
	if rows[0].Dispatches != 2 {
		t.Errorf("rows[0].Dispatches=%d; want 2", rows[0].Dispatches)
	}
}

func TestComputeByAxis_ByRuntime(t *testing.T) {
	evs := []cost.Event{
		makeEvent(nowRFC(), "a", "claude", 10, 0.01),
		makeEvent(nowRFC(), "b", "claude", 10, 0.01),
		makeEvent(nowRFC(), "c", "gemini", 10, 0.01),
	}
	rows := ComputeByAxis(evs, "runtime")
	if len(rows) != 2 {
		t.Fatalf("rows=%d; want 2 runtimes", len(rows))
	}
	if rows[0].Key != "claude" {
		t.Errorf("rows[0].Key=%q; want claude", rows[0].Key)
	}
}

func TestComputeByAxis_ByProject(t *testing.T) {
	evs := []cost.Event{
		{Type: "dispatch_finished", Ts: nowRFC(), Agent: "a", Runtime: "claude", Project: "proj-x", DurationS: 10},
		{Type: "dispatch_finished", Ts: nowRFC(), Agent: "b", Runtime: "claude", Project: "proj-y", DurationS: 10},
	}
	rows := ComputeByAxis(evs, "project")
	if len(rows) != 2 {
		t.Fatalf("rows=%d; want 2 projects", len(rows))
	}
}

func TestComputeByAxis_ByDay(t *testing.T) {
	evs := []cost.Event{
		makeEvent("2026-06-01T10:00:00Z", "a", "claude", 10, 0.01),
		makeEvent("2026-06-01T15:00:00Z", "b", "claude", 10, 0.01),
		makeEvent("2026-06-02T10:00:00Z", "c", "claude", 10, 0.01),
	}
	rows := ComputeByAxis(evs, "day")
	if len(rows) != 2 {
		t.Fatalf("rows=%d; want 2 days", len(rows))
	}
}

func TestComputeByAxis_Empty(t *testing.T) {
	rows := ComputeByAxis(nil, "agent")
	if rows == nil {
		t.Error("ComputeByAxis should return [] not nil for empty events")
	}
}

func TestComputeByAxis_UnknownKey(t *testing.T) {
	evs := []cost.Event{
		{Type: "dispatch_finished", Ts: nowRFC(), Agent: "", Runtime: ""},
	}
	rows := ComputeByAxis(evs, "agent")
	if len(rows) != 1 || rows[0].Key != "(unknown)" {
		t.Errorf("expected one (unknown) row; got %v", rows)
	}
}

// ---- RecentDispatches -------------------------------------------------------

func TestRecentDispatches_Limit(t *testing.T) {
	evs := make([]cost.Event, 20)
	for i := range evs {
		evs[i] = makeEvent(nowRFC(), "a", "claude", 10, 0.01)
	}
	rows := RecentDispatches(evs, 5)
	if len(rows) != 5 {
		t.Errorf("rows=%d; want 5", len(rows))
	}
}

func TestRecentDispatches_TakesFromEnd(t *testing.T) {
	evs := []cost.Event{
		makeEvent("2026-06-01T00:00:00Z", "old-agent", "claude", 10, 0.01),
		makeEvent("2026-06-02T00:00:00Z", "new-agent", "claude", 10, 0.01),
	}
	rows := RecentDispatches(evs, 1)
	if len(rows) != 1 {
		t.Fatalf("rows=%d; want 1", len(rows))
	}
	if rows[0].Agent != "new-agent" {
		t.Errorf("agent=%q; want new-agent (most recent)", rows[0].Agent)
	}
}

func TestRecentDispatches_Empty(t *testing.T) {
	rows := RecentDispatches(nil, 50)
	if rows == nil {
		t.Error("RecentDispatches should return [] not nil for empty events")
	}
}

func TestRecentDispatches_DefaultLimit(t *testing.T) {
	evs := make([]cost.Event, 100)
	for i := range evs {
		evs[i] = makeEvent(nowRFC(), "a", "claude", 10, 0.01)
	}
	rows := RecentDispatches(evs, 0) // 0 → default 50
	if len(rows) != 50 {
		t.Errorf("rows=%d; want 50 (default limit)", len(rows))
	}
}
