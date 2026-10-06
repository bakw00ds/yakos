package metrics

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// K-136: efficiency.total_cost_usd is API spend only. Tokens are the primary
// accounting unit; a dollar figure on a subscription or local run is not spend
// (a subscription harness reports an API-equivalent figure, kept apart as
// api_equivalent_usd), so collectEfficiency must total dollars with
// Event.SpendUSD, never by summing usage.total_cost_usd directly.

// writeSpendLog writes the lines as the active dispatch log of an isolated
// state dir and returns that dir. The path comes from statepath, which reads
// YAKOS_DISPATCH_LOG, so nothing here touches the operator's real state.
func writeSpendLog(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)
	if err := os.WriteFile(statepath.DispatchLog(), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return dir
}

// finishedLine builds one dispatch_finished line. extra is spliced in after the
// fixed keys (billing, api_equivalent_usd, ...); usage is the full usage object.
func finishedLine(runtime, extra, usage string) string {
	s := `{"type":"dispatch_finished","ts":"2026-06-01T00:00:00Z","agent":"worker","runtime":"` + runtime + `","exit_code":0,"duration_s":1`
	if extra != "" {
		s += "," + extra
	}
	if usage != "" {
		s += `,"usage":` + usage
	}
	return s + "}"
}

func usageObj(in, out int64, cost string) string {
	return `{"input_tokens":` + strconv.FormatInt(in, 10) + `,"output_tokens":` + strconv.FormatInt(out, 10) +
		`,"cache_read":0,"cache_creation":0,"duration_ms":1000,"total_cost_usd":` + cost + `}`
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// show renders an optional metric for a failure message.
func show(p *float64) string {
	if p == nil {
		return "nil"
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

func TestCollectEfficiency_SpendIsAPIOnly(t *testing.T) {
	dir := writeSpendLog(t,
		// api-billed claude run: counts, $0.30.
		finishedLine("claude", `"billing":"api","cost_source":"harness"`, usageObj(100, 50, "0.30")),
		// subscription claude run carrying a stray nonzero usage cost plus its
		// API-equivalent figure: tokens yes, spend NO.
		finishedLine("claude", `"billing":"subscription","cost_source":"harness","api_equivalent_usd":0.5`, usageObj(200, 80, "0.50")),
		// codex run under a ChatGPT login: tokens only.
		finishedLine("codex", `"billing":"subscription"`, usageObj(300, 90, "0")),
		// legacy row (no billing field): its dollar figure was the only cost
		// there was, so it still counts as spend.
		finishedLine("claude", "", usageObj(10, 5, "0.20")),
	)

	var m Metrics
	collectEfficiency(dir, "", "", &m)

	if m.Efficiency.TaskCount == nil || *m.Efficiency.TaskCount != 4 {
		t.Fatalf("task_count = %v, want 4", m.Efficiency.TaskCount)
	}
	if m.Efficiency.TotalCostUSD == nil {
		t.Fatal("total_cost_usd is nil, want 0.50 (api 0.30 + legacy 0.20)")
	}
	if got := *m.Efficiency.TotalCostUSD; !approx(got, 0.50) {
		t.Errorf("total_cost_usd = %v, want 0.50: only api-billed and legacy rows are spend (a subscription row's 0.50 must not be added)", got)
	}
	// Median and mean cost per task are over the tasks that carried spend.
	if m.Efficiency.MedianCostPerTaskUSD == nil || !approx(*m.Efficiency.MedianCostPerTaskUSD, 0.25) {
		t.Errorf("median_cost_per_task_usd = %s, want 0.25 over {0.30, 0.20}", show(m.Efficiency.MedianCostPerTaskUSD))
	}
	if m.Efficiency.MeanCostPerTaskUSD == nil || !approx(*m.Efficiency.MeanCostPerTaskUSD, 0.25) {
		t.Errorf("mean_cost_per_task_usd = %s, want 0.25 over {0.30, 0.20}", show(m.Efficiency.MeanCostPerTaskUSD))
	}
}

func TestCollectEfficiency_SubscriptionOnlyLeavesCostNil(t *testing.T) {
	dir := writeSpendLog(t,
		finishedLine("claude", `"billing":"subscription","cost_source":"harness","api_equivalent_usd":0.42`, usageObj(100, 50, "0.42")),
		finishedLine("codex", `"billing":"subscription"`, usageObj(300, 90, "1.5")),
		finishedLine("agy", `"billing":"local"`, usageObj(5, 5, "9.99")),
	)

	var m Metrics
	collectEfficiency(dir, "", "", &m)

	if m.Efficiency.TaskCount == nil || *m.Efficiency.TaskCount != 3 {
		t.Fatalf("task_count = %v, want 3", m.Efficiency.TaskCount)
	}
	if m.Efficiency.TotalCostUSD != nil {
		t.Errorf("total_cost_usd = %s, want nil: subscription and local runs are not spend", show(m.Efficiency.TotalCostUSD))
	}
	if m.Efficiency.MedianCostPerTaskUSD != nil || m.Efficiency.MeanCostPerTaskUSD != nil {
		t.Errorf("per-task cost = %s / %s, want nil when no task carried spend",
			show(m.Efficiency.MedianCostPerTaskUSD), show(m.Efficiency.MeanCostPerTaskUSD))
	}
}

func TestCollectEfficiency_BillingValuesCountAsSpend(t *testing.T) {
	cases := []struct {
		billing string
		want    float64
	}{
		{"", 0.1},           // legacy: counts
		{"api", 0.1},        // api: counts
		{"surprise", 0.1},   // unknown value: counts (the safe direction for a cost guard)
		{"subscription", 0}, // not spend
		{"local", 0},        // not spend
	}
	for _, tc := range cases {
		t.Run("billing="+tc.billing, func(t *testing.T) {
			extra := ""
			if tc.billing != "" {
				extra = `"billing":"` + tc.billing + `"`
			}
			dir := writeSpendLog(t, finishedLine("claude", extra, usageObj(1, 1, "0.1")))
			var m Metrics
			collectEfficiency(dir, "", "", &m)
			got := 0.0
			if m.Efficiency.TotalCostUSD != nil {
				got = *m.Efficiency.TotalCostUSD
			}
			if !approx(got, tc.want) {
				t.Errorf("billing %q: total_cost_usd = %v, want %v", tc.billing, got, tc.want)
			}
		})
	}
}

func TestCollectEfficiency_AbsurdCostCannotMoveTotal(t *testing.T) {
	// One corrupt line carrying an absurd dollar figure must not become the
	// total; Event.SpendUSD treats it as 0 (as it does NaN and negatives).
	dir := writeSpendLog(t,
		finishedLine("claude", "", usageObj(1, 1, "0.25")),
		finishedLine("claude", "", usageObj(1, 1, "1e18")),
	)
	var m Metrics
	collectEfficiency(dir, "", "", &m)
	if m.Efficiency.TotalCostUSD == nil || !approx(*m.Efficiency.TotalCostUSD, 0.25) {
		t.Errorf("total_cost_usd = %s, want 0.25 (the absurd row ignored)", show(m.Efficiency.TotalCostUSD))
	}
}
