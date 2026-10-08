package perfdash

import (
	"encoding/json"
	"fmt"
	"math"
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
		{"", 24 * time.Hour},      // default
		{"bogus", 24 * time.Hour}, // unknown → default
	}
	for _, tc := range cases {
		got := parseWindow(tc.input)
		if got != tc.want {
			t.Errorf("parseWindow(%q)=%v; want %v", tc.input, got, tc.want)
		}
	}
}

// ---- dollars and tokens (K-136) ---------------------------------------------
//
// Tokens are the primary unit and dollars are API spend only. Nothing is
// estimated: before K-136 an event with no reported cost was priced from its
// est_* size estimates at a hard-coded Sonnet rate.

// ledgerEvent builds a dispatch_finished event the way the Go dispatcher writes
// it after K-136: a usage object and a billing field.
func ledgerEvent(agent, runtime, billing string, u cost.Usage, apiEquiv float64) cost.Event {
	return cost.Event{
		Type:             "dispatch_finished",
		Ts:               nowRFC(),
		Agent:            agent,
		Runtime:          runtime,
		Project:          "proj",
		DurationS:        1,
		Billing:          billing,
		APIEquivalentUSD: apiEquiv,
		Usage:            &u,
	}
}

// estOnlyEvent is a row with size estimates and no usage object: every
// bash-written row, and a Go row from a harness that reported no usage.
func estOnlyEvent(agent, runtime string) cost.Event {
	return cost.Event{
		Type:            "dispatch_finished",
		Ts:              nowRFC(),
		Agent:           agent,
		Runtime:         runtime,
		DurationS:       1,
		EstInputTokens:  1_000_000,
		EstOutputTokens: 1_000_000,
	}
}

const usdEpsilon = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) < usdEpsilon }

// TestEstimatesAreNeverDollars pins the deleted estimator: an event that only
// carries est_* fields costs $0 in every view. At the Sonnet rate that was
// $3 per million estimated input tokens plus $15 per million output, so this log
// used to be priced at $36 per event.
func TestEstimatesAreNeverDollars(t *testing.T) {
	evs := []cost.Event{estOnlyEvent("a", "claude"), estOnlyEvent("b", "codex")}

	s := ComputeSummary(evs, 5)
	if s.TotalCostUSD != 0 {
		t.Errorf("summary total_cost_usd=%v; want 0 (estimates are not dollars)", s.TotalCostUSD)
	}
	for _, it := range append(append([]TopAxisItem{}, s.TopAgents...), s.TopRuntimes...) {
		if it.CostUSD != 0 {
			t.Errorf("top item %q cost_usd=%v; want 0", it.Key, it.CostUSD)
		}
	}
	for _, axis := range []string{"agent", "runtime", "project", "day"} {
		for _, r := range ComputeByAxis(evs, axis) {
			if r.CostUSD != 0 {
				t.Errorf("by_axis %s row %q cost_usd=%v; want 0", axis, r.Key, r.CostUSD)
			}
		}
	}
	for _, r := range RecentDispatches(evs, 10) {
		if r.CostUSD != 0 {
			t.Errorf("recent row %q cost_usd=%v; want 0", r.Agent, r.CostUSD)
		}
	}
	for _, p := range ComputeTimeseries(evs, 24*time.Hour, time.Hour, "cost") {
		if p.Value != 0 {
			t.Errorf("cost timeseries point %s=%v; want 0", p.Ts, p.Value)
		}
	}
}

// TestEstimatesAreNeverTokens: est_* is a size estimate from byte counts, not a
// count of tokens, so it is in no token total. An event with no usage object
// reports none.
func TestEstimatesAreNeverTokens(t *testing.T) {
	evs := []cost.Event{
		estOnlyEvent("a", "claude"),
		// Real usage next to big estimates: only the usage counts.
		func() cost.Event {
			e := ledgerEvent("b", "claude", cost.BillingAPI, cost.Usage{InputTokens: 10, OutputTokens: 5}, 0)
			e.EstInputTokens, e.EstOutputTokens = 1_000_000, 1_000_000
			return e
		}(),
	}

	s := ComputeSummary(evs, 5)
	if s.TotalTokens != 15 {
		t.Errorf("summary total_tokens=%d; want 15 (usage only, no estimates)", s.TotalTokens)
	}
	if s.TokenDetail != (cost.TokenTotals{Input: 10, Output: 5}) {
		t.Errorf("summary token_detail=%+v", s.TokenDetail)
	}
	for _, it := range s.TopAgents {
		want := int64(0)
		if it.Key == "b" {
			want = 15
		}
		if it.Tokens != want {
			t.Errorf("top agent %q tokens=%d; want %d", it.Key, it.Tokens, want)
		}
	}
	for _, r := range ComputeByAxis(evs, "agent") {
		want := int64(0)
		if r.Key == "b" {
			want = 15
		}
		if r.Tokens != want {
			t.Errorf("by_axis agent %q tokens=%d; want %d", r.Key, r.Tokens, want)
		}
	}
	var sum float64
	for _, p := range ComputeTimeseries(evs, 24*time.Hour, time.Hour, "tokens") {
		sum += p.Value
	}
	if sum != 15 {
		t.Errorf("tokens timeseries sums to %v; want 15", sum)
	}
}

// TestDollarsAreAPISpendOnly: the same $0.30 usage cost is spend only when the
// run was paid per API call. A subscription or local row that carries a usage
// cost (it should not, but a corrupt or hand-edited line might) adds nothing.
// A row written before the billing field existed, or with a billing value this
// version does not know, keeps counting, the safe direction for a cost view.
func TestDollarsAreAPISpendOnly(t *testing.T) {
	cases := []struct {
		name    string
		billing string
		want    float64
	}{
		{"api", cost.BillingAPI, 0.30},
		{"subscription", cost.BillingSubscription, 0},
		{"local", cost.BillingLocal, 0},
		{"legacy-no-billing", "", 0.30},
		{"unknown-billing", "mystery", 0.30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evs := []cost.Event{ledgerEvent("a", "claude", tc.billing, cost.Usage{InputTokens: 1, TotalCostUSD: 0.30}, 0)}

			if got := ComputeSummary(evs, 5).TotalCostUSD; !approx(got, tc.want) {
				t.Errorf("summary total_cost_usd=%v; want %v", got, tc.want)
			}
			if got := ComputeSummary(evs, 5).TopAgents[0].CostUSD; !approx(got, tc.want) {
				t.Errorf("top agent cost_usd=%v; want %v", got, tc.want)
			}
			if got := ComputeByAxis(evs, "agent")[0].CostUSD; !approx(got, tc.want) {
				t.Errorf("by_axis cost_usd=%v; want %v", got, tc.want)
			}
			if got := RecentDispatches(evs, 5)[0].CostUSD; !approx(got, tc.want) {
				t.Errorf("recent cost_usd=%v; want %v", got, tc.want)
			}
			var sum float64
			for _, p := range ComputeTimeseries(evs, 24*time.Hour, time.Hour, "cost") {
				sum += p.Value
			}
			if !approx(sum, tc.want) {
				t.Errorf("cost timeseries sums to %v; want %v", sum, tc.want)
			}
		})
	}
}

// TestTokensCountEveryKind: the total is input + output + cache read + cache
// creation, and the per-kind detail carries each as logged, in every view.
func TestTokensCountEveryKind(t *testing.T) {
	u := cost.Usage{InputTokens: 1000, OutputTokens: 200, CacheRead: 5000, CacheCreation: 300}
	want := cost.TokenTotals{Input: 1000, Output: 200, CacheRead: 5000, CacheCreation: 300}
	evs := []cost.Event{
		ledgerEvent("a", "claude", cost.BillingSubscription, u, 0),
		ledgerEvent("a", "claude", cost.BillingSubscription, u, 0),
	}
	doubled := cost.TokenTotals{Input: 2000, Output: 400, CacheRead: 10000, CacheCreation: 600}

	s := ComputeSummary(evs, 5)
	if s.TotalTokens != 13000 || s.TokenDetail != doubled {
		t.Errorf("summary total_tokens=%d detail=%+v; want 13000 %+v", s.TotalTokens, s.TokenDetail, doubled)
	}
	if got := s.TopAgents[0]; got.Tokens != 13000 || got.TokenDetail != doubled {
		t.Errorf("top agent tokens=%d detail=%+v", got.Tokens, got.TokenDetail)
	}
	if got := s.TopRuntimes[0]; got.Tokens != 13000 || got.TokenDetail != doubled {
		t.Errorf("top runtime tokens=%d detail=%+v", got.Tokens, got.TokenDetail)
	}
	if got := ComputeByAxis(evs, "runtime")[0]; got.Tokens != 13000 || got.TokenDetail != doubled {
		t.Errorf("by_axis tokens=%d detail=%+v", got.Tokens, got.TokenDetail)
	}
	for _, r := range RecentDispatches(evs, 5) {
		if r.Tokens != 6500 || r.TokenDetail != want {
			t.Errorf("recent row tokens=%d detail=%+v; want 6500 %+v", r.Tokens, r.TokenDetail, want)
		}
	}
	var sum float64
	for _, p := range ComputeTimeseries(evs, 24*time.Hour, time.Hour, "tokens") {
		sum += p.Value
	}
	if sum != 13000 {
		t.Errorf("tokens timeseries sums to %v; want 13000", sum)
	}
}

// TestSubscriptionRowsReportTokensAndNoDollars is the headline rule: a codex or
// agy row, or a subscription-billed claude row, shows its tokens and $0.00; only
// the api-billed claude row and the pre-K-136 row carry dollars.
func TestSubscriptionRowsReportTokensAndNoDollars(t *testing.T) {
	evs := []cost.Event{
		ledgerEvent("w1", "claude", cost.BillingAPI, cost.Usage{InputTokens: 900, OutputTokens: 100, TotalCostUSD: 0.30}, 0),
		ledgerEvent("w2", "claude", cost.BillingSubscription, cost.Usage{InputTokens: 1200, OutputTokens: 340, CacheRead: 50000, CacheCreation: 2000}, 0.42),
		ledgerEvent("w3", "codex", cost.BillingSubscription, cost.Usage{InputTokens: 7707, OutputTokens: 421, CacheRead: 7424}, 0),
		ledgerEvent("w4", "agy", cost.BillingSubscription, cost.Usage{InputTokens: 12863, OutputTokens: 1}, 0),
		estOnlyEvent("w5", "gemini"),
		makeEvent(nowRFC(), "w6", "claude", 1, 0.20), // legacy: usage cost, no billing field
	}

	byRuntime := map[string]AxisRow{}
	for _, r := range ComputeByAxis(evs, "runtime") {
		byRuntime[r.Key] = r
	}
	if r := byRuntime["codex"]; r.CostUSD != 0 || r.Tokens != 7707+421+7424 {
		t.Errorf("codex row: cost_usd=%v tokens=%d; want 0 and 15552", r.CostUSD, r.Tokens)
	}
	if r := byRuntime["agy"]; r.CostUSD != 0 || r.Tokens != 12864 {
		t.Errorf("agy row: cost_usd=%v tokens=%d; want 0 and 12864", r.CostUSD, r.Tokens)
	}
	if r := byRuntime["gemini"]; r.CostUSD != 0 || r.Tokens != 0 {
		t.Errorf("est-only gemini row: cost_usd=%v tokens=%d; want 0 and 0", r.CostUSD, r.Tokens)
	}
	// claude: api $0.30 + legacy $0.20; the subscription row adds tokens only.
	if r := byRuntime["claude"]; !approx(r.CostUSD, 0.50) || r.Tokens != 1000+1200+340+50000+2000 {
		t.Errorf("claude row: cost_usd=%v tokens=%d; want 0.50 and 54540", r.CostUSD, r.Tokens)
	}

	s := ComputeSummary(evs, 5)
	if !approx(s.TotalCostUSD, 0.50) {
		t.Errorf("total_cost_usd=%v; want 0.50", s.TotalCostUSD)
	}
	if want := int64(1000 + 53540 + 15552 + 12864); s.TotalTokens != want {
		t.Errorf("total_tokens=%d; want %d", s.TotalTokens, want)
	}
}

// TestAPIEquivalentIsInformationalOnly: api_equivalent_usd is summed on its own
// and never reaches a dollar figure.
func TestAPIEquivalentIsInformationalOnly(t *testing.T) {
	evs := []cost.Event{
		ledgerEvent("a", "claude", cost.BillingSubscription, cost.Usage{InputTokens: 1}, 0.42),
		ledgerEvent("a", "claude", cost.BillingSubscription, cost.Usage{InputTokens: 1}, 0.08),
		ledgerEvent("b", "claude", cost.BillingAPI, cost.Usage{InputTokens: 1, TotalCostUSD: 0.30}, 0),
	}

	s := ComputeSummary(evs, 5)
	if !approx(s.APIEquivalentUSD, 0.50) {
		t.Errorf("summary api_equivalent_usd=%v; want 0.50", s.APIEquivalentUSD)
	}
	if !approx(s.TotalCostUSD, 0.30) {
		t.Errorf("total_cost_usd=%v; want 0.30 (api-equivalent is not spend)", s.TotalCostUSD)
	}
	rows := ComputeByAxis(evs, "agent")
	for _, r := range rows {
		switch r.Key {
		case "a":
			if !approx(r.APIEquivalentUSD, 0.50) || r.CostUSD != 0 {
				t.Errorf("agent a: api_equivalent_usd=%v cost_usd=%v; want 0.50 and 0", r.APIEquivalentUSD, r.CostUSD)
			}
		case "b":
			if r.APIEquivalentUSD != 0 || !approx(r.CostUSD, 0.30) {
				t.Errorf("agent b: api_equivalent_usd=%v cost_usd=%v; want 0 and 0.30", r.APIEquivalentUSD, r.CostUSD)
			}
		}
	}
	recent := RecentDispatches(evs, 5)
	if !approx(recent[0].APIEquivalentUSD, 0.42) || recent[0].CostUSD != 0 {
		t.Errorf("recent[0]: api_equivalent_usd=%v cost_usd=%v", recent[0].APIEquivalentUSD, recent[0].CostUSD)
	}
	var sum float64
	for _, p := range ComputeTimeseries(evs, 24*time.Hour, time.Hour, "cost") {
		sum += p.Value
	}
	if !approx(sum, 0.30) {
		t.Errorf("cost timeseries sums to %v; want 0.30", sum)
	}
}

// TestCorruptFiguresMoveNoTotal: a NaN, negative or absurd cost, token count or
// api-equivalent is read as 0, so one bad line cannot move a total or make the
// summary unmarshalable (JSON has no NaN).
func TestCorruptFiguresMoveNoTotal(t *testing.T) {
	bad := []cost.Usage{
		{InputTokens: -5, OutputTokens: -1, CacheRead: -9, CacheCreation: -2, TotalCostUSD: -1},
		{InputTokens: 1 << 41, OutputTokens: 1 << 50, TotalCostUSD: 1e13},
		{TotalCostUSD: math.NaN()},
	}
	var evs []cost.Event
	for i, u := range bad {
		evs = append(evs, ledgerEvent(fmt.Sprintf("a%d", i), "claude", cost.BillingAPI, u, math.NaN()))
		evs = append(evs, ledgerEvent(fmt.Sprintf("a%d", i), "claude", cost.BillingAPI, u, -3))
		evs = append(evs, ledgerEvent(fmt.Sprintf("a%d", i), "claude", cost.BillingAPI, u, 1e13))
	}

	s := ComputeSummary(evs, 5)
	if s.TotalCostUSD != 0 || s.TotalTokens != 0 || s.APIEquivalentUSD != 0 || !s.TokenDetail.IsZero() {
		t.Errorf("corrupt rows moved a total: %+v", s)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Errorf("summary does not marshal: %v", err)
	}
	for _, r := range ComputeByAxis(evs, "agent") {
		if r.CostUSD != 0 || r.Tokens != 0 || r.APIEquivalentUSD != 0 {
			t.Errorf("corrupt rows moved by_axis row %q: %+v", r.Key, r)
		}
	}
	for _, r := range RecentDispatches(evs, 20) {
		if r.CostUSD != 0 || r.Tokens != 0 || r.APIEquivalentUSD != 0 {
			t.Errorf("corrupt row moved a recent row: %+v", r)
		}
	}
	for _, metric := range []string{"cost", "tokens"} {
		for _, p := range ComputeTimeseries(evs, 24*time.Hour, time.Hour, metric) {
			if p.Value != 0 {
				t.Errorf("%s timeseries point %s=%v; want 0", metric, p.Ts, p.Value)
			}
		}
	}
}

// TestRecentDispatches_BillingAndTokens: the billing field passes through, and a
// row written before K-136 has none.
func TestRecentDispatches_BillingAndTokens(t *testing.T) {
	evs := []cost.Event{
		makeEvent(nowRFC(), "old", "claude", 1, 0.01),
		ledgerEvent("new", "codex", cost.BillingSubscription, cost.Usage{InputTokens: 7, OutputTokens: 3}, 0),
	}
	rows := RecentDispatches(evs, 5)
	if rows[0].Billing != "" {
		t.Errorf("legacy row billing=%q; want empty", rows[0].Billing)
	}
	if rows[1].Billing != cost.BillingSubscription || rows[1].Tokens != 10 {
		t.Errorf("ledger row billing=%q tokens=%d; want subscription and 10", rows[1].Billing, rows[1].Tokens)
	}
}

// TestTimeseriesTokensMetric: the tokens series sums real tokens per bucket.
func TestTimeseriesTokensMetric(t *testing.T) {
	b0 := time.Now().UTC().Truncate(time.Hour)
	at := func(d time.Duration) string { return b0.Add(d).Format(time.RFC3339) }
	mk := func(ts string, in, out int64) cost.Event {
		e := ledgerEvent("a", "codex", cost.BillingSubscription, cost.Usage{InputTokens: in, OutputTokens: out}, 0)
		e.Ts = ts
		return e
	}
	evs := []cost.Event{
		mk(at(5*time.Second), 100, 10),       // this hour
		mk(at(6*time.Second), 200, 20),       // this hour
		mk(at(-time.Hour+time.Second), 7, 3), // previous hour
		mk(at(-26*time.Hour), 999, 999),      // outside the 24h window
	}

	got := map[string]float64{}
	for _, p := range ComputeTimeseries(evs, 24*time.Hour, time.Hour, "tokens") {
		got[p.Ts] = p.Value
	}
	if v := got[b0.Format(time.RFC3339)]; v != 330 {
		t.Errorf("this hour's tokens=%v; want 330", v)
	}
	if v := got[b0.Add(-time.Hour).Format(time.RFC3339)]; v != 10 {
		t.Errorf("previous hour's tokens=%v; want 10", v)
	}
	var sum float64
	for _, v := range got {
		sum += v
	}
	if sum != 340 {
		t.Errorf("tokens timeseries sums to %v; want 340 (the 26h-old event is outside the window)", sum)
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

// ---- by-model axis (K-153) --------------------------------------------------

func TestComputeByAxis_Model(t *testing.T) {
	in := func(n int64) *cost.Usage { return &cost.Usage{InputTokens: n} }
	evs := []cost.Event{
		{Type: "dispatch_finished", ModelID: "gpt-5.5", ModelResolved: "ignored", Model: "ignored", Usage: in(100)},
		{Type: "dispatch_finished", ModelID: "gpt-5.5", Usage: in(50)},
		{Type: "dispatch_finished", ModelResolved: "sonnet", Model: "ignored", Usage: in(7)},
		{Type: "dispatch_finished", Model: "haiku", Usage: in(3)},
		{Type: "dispatch_finished", Usage: in(1)},
	}
	got := map[string]int64{}
	for _, r := range ComputeByAxis(evs, "model") {
		got[r.Key] = r.Tokens
	}
	want := map[string]int64{"gpt-5.5": 150, "sonnet": 7, "haiku": 3, "(unknown)": 1}
	if len(got) != len(want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d tokens, want %d", k, got[k], v)
		}
	}
}
