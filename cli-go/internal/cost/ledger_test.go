package cost

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---- event builders ----------------------------------------------------------

// legacyEvent is a pre-K-136 row: no billing field. usage may be nil (bash rows).
func legacyEvent(agent, runtime string, est [2]int64, usage *Usage) Event {
	return Event{
		Type: "dispatch_finished", Ts: "2026-01-01T00:00:00Z", Agent: agent, Runtime: runtime,
		DurationS: 10, EstInputTokens: est[0], EstOutputTokens: est[1], Usage: usage,
	}
}

// ledgerEvent is a K-136 row: billing set, plus the optional api-equivalent.
func ledgerEvent(agent, runtime, billing string, est [2]int64, usage *Usage, apiEquiv float64) Event {
	ev := legacyEvent(agent, runtime, est, usage)
	ev.Billing = billing
	ev.APIEquivalentUSD = apiEquiv
	return ev
}

func chanOf(evs ...Event) <-chan Event {
	ch := make(chan Event, len(evs))
	for _, e := range evs {
		ch <- e
	}
	close(ch)
	return ch
}

func rowByKey(t *testing.T, rpt Report, key string) Row {
	t.Helper()
	for _, r := range rpt.Rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("no row %q in %+v", key, rpt.Rows)
	return Row{}
}

// mixedEvents is a log with every kind of row the reader meets: legacy bash and
// legacy Go rows, and ledger rows for claude (subscription and api), codex and
// agy. It is the in-code twin of the ledger-mixed fixture in cmd/yakos.
func mixedEvents() []Event {
	return []Event{
		legacyEvent("architect", "claude", [2]int64{25, 125}, nil), // bash row: no usage
		legacyEvent("backend", "claude", [2]int64{200, 100},
			&Usage{InputTokens: 210, OutputTokens: 110, CacheRead: 1000, CacheCreation: 500, TotalCostUSD: 0.5}), // Go row before K-136: spend
		ledgerEvent("architect", "claude", BillingSubscription, [2]int64{100, 450},
			&Usage{InputTokens: 1200, OutputTokens: 340, CacheRead: 50000, CacheCreation: 2000}, 0.4231),
		ledgerEvent("backend", "claude", BillingSubscription, [2]int64{90, 60},
			&Usage{InputTokens: 800, OutputTokens: 150, CacheRead: 20000}, 0.1875),
		ledgerEvent("backend", "claude", BillingAPI, [2]int64{70, 80},
			&Usage{InputTokens: 5000, OutputTokens: 900, TotalCostUSD: 0.3012}, 0),
		ledgerEvent("frontend", "codex", BillingSubscription, [2]int64{300, 700},
			&Usage{InputTokens: 7707, OutputTokens: 421, CacheRead: 7424}, 0),
		ledgerEvent("general-agy", "agy", BillingSubscription, [2]int64{30, 60},
			&Usage{InputTokens: 12863, OutputTokens: 1}, 0),
		ledgerEvent("general-agy", "agy", BillingSubscription, [2]int64{30, 60}, nil, 0), // plain-text run: no usage
	}
}

// ---- helpers -----------------------------------------------------------------

func TestIsLedger(t *testing.T) {
	if IsLedger(Event{}) {
		t.Error("an event with no billing field is a legacy row, not a ledger event")
	}
	if IsLedger(Event{Usage: &Usage{InputTokens: 5, TotalCostUSD: 1}}) {
		t.Error("usage alone does not make a ledger event: pre-K-136 Go rows carry usage")
	}
	for _, b := range []string{BillingSubscription, BillingAPI, BillingLocal, "something-new"} {
		if !IsLedger(Event{Billing: b}) {
			t.Errorf("billing %q should make a ledger event", b)
		}
	}
}

func TestAPIEquivalentUSD_IgnoresCorruptFigures(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{0.42, 0.42}, {0, 0}, {-1, 0}, {math.NaN(), 0}, {math.Inf(1), 0}, {1e13, 0},
	}
	for _, tc := range cases {
		if got := APIEquivalentUSD(Event{APIEquivalentUSD: tc.in}); got != tc.want {
			t.Errorf("APIEquivalentUSD(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ---- aggregation -------------------------------------------------------------

func TestAggregate_LedgerTotalsAreRealTokensAndAPISpendOnly(t *testing.T) {
	rpt := AggregateLedger(chanOf(mixedEvents()...), AxisRuntime, 0)
	if !rpt.Ledger {
		t.Fatal("a log holding ledger events must report Ledger")
	}

	claude := rowByKey(t, rpt, "claude")
	// Legacy bash row (no usage) adds none; the legacy Go row, both subscription
	// rows and the api row add their usage.
	want := TokenTotals{Input: 210 + 1200 + 800 + 5000, Output: 110 + 340 + 150 + 900, CacheRead: 1000 + 50000 + 20000, CacheCreation: 500 + 2000}
	if claude.Tokens != want {
		t.Errorf("claude tokens = %+v, want %+v", claude.Tokens, want)
	}
	// Dollars: the legacy Go row (no billing) and the api row. Neither
	// subscription row contributes, and the api-equivalent is never spend.
	if math.Abs(claude.SpendUSD-0.8012) > 1e-9 {
		t.Errorf("claude spend = %v, want 0.8012", claude.SpendUSD)
	}
	if math.Abs(claude.APIEquivUSD-0.6106) > 1e-9 {
		t.Errorf("claude api-equivalent = %v, want 0.6106", claude.APIEquivUSD)
	}

	codex := rowByKey(t, rpt, "codex")
	if codex.Tokens.Total() != 7707+421+7424 {
		t.Errorf("codex tokens total = %d, want 15552", codex.Tokens.Total())
	}
	if codex.SpendUSD != 0 || codex.APIEquivUSD != 0 {
		t.Errorf("codex must carry no dollars, got spend %v api-equivalent %v", codex.SpendUSD, codex.APIEquivUSD)
	}

	agy := rowByKey(t, rpt, "agy")
	if agy.Tokens.Total() != 12864 || agy.SpendUSD != 0 {
		t.Errorf("agy = %+v, want 12864 tokens and no spend", agy)
	}
	if agy.Count != 2 {
		t.Errorf("agy count = %d, want 2 (a run with no usage still counts)", agy.Count)
	}
}

// A subscription or local row must carry no spend even when a stray usage cost is
// logged on it, and a row with an unknown billing value counts like api.
func TestAggregate_SubscriptionAndLocalNeverSpendDollars(t *testing.T) {
	rpt := AggregateLedger(chanOf(
		ledgerEvent("a", "codex", BillingSubscription, [2]int64{}, &Usage{InputTokens: 10, TotalCostUSD: 9.99}, 0),
		ledgerEvent("b", "local", BillingLocal, [2]int64{}, &Usage{InputTokens: 10, TotalCostUSD: 9.99}, 0),
		ledgerEvent("c", "claude", "reseller", [2]int64{}, &Usage{InputTokens: 10, TotalCostUSD: 0.25}, 0),
	), AxisAgent, 0)
	if r := rowByKey(t, rpt, "a"); r.SpendUSD != 0 {
		t.Errorf("subscription spend = %v, want 0", r.SpendUSD)
	}
	if r := rowByKey(t, rpt, "b"); r.SpendUSD != 0 {
		t.Errorf("local spend = %v, want 0", r.SpendUSD)
	}
	if r := rowByKey(t, rpt, "c"); r.SpendUSD != 0.25 {
		t.Errorf("unknown-billing spend = %v, want 0.25 (counts, the safe direction)", r.SpendUSD)
	}
}

// est_* are byte-count estimates, not usage: they never enter the real token
// columns, with or without a usage object.
func TestAggregate_EstimatesNeverEnterRealTokens(t *testing.T) {
	rpt := AggregateLedger(chanOf(
		ledgerEvent("x", "agy", BillingSubscription, [2]int64{1_000_000, 2_000_000}, nil, 0),
		ledgerEvent("x", "agy", BillingSubscription, [2]int64{500, 600}, &Usage{InputTokens: 7, OutputTokens: 3}, 0),
	), AxisRuntime, 0)
	r := rowByKey(t, rpt, "agy")
	if r.Tokens != (TokenTotals{Input: 7, Output: 3}) {
		t.Errorf("tokens = %+v, want only the reported 7 in / 3 out", r.Tokens)
	}
	// The estimate columns still hold the estimates, untouched.
	if r.TotalInTokens != 1_000_500 || r.TotalOutTokens != 2_000_600 {
		t.Errorf("est columns = %d/%d, want 1000500/2000600", r.TotalInTokens, r.TotalOutTokens)
	}
}

// Dollar sums are rounded to a micro-dollar, so float noise (0.1 + 0.2 is
// 0.30000000000000004) never reaches a report.
func TestAggregate_DollarsAreRoundedToMicroDollars(t *testing.T) {
	rpt := AggregateLedger(chanOf(
		ledgerEvent("a", "claude", BillingAPI, [2]int64{}, &Usage{InputTokens: 1, TotalCostUSD: 0.1}, 0.1),
		ledgerEvent("a", "claude", BillingAPI, [2]int64{}, &Usage{InputTokens: 1, TotalCostUSD: 0.2}, 0.2),
	), AxisRuntime, 0)
	r := rowByKey(t, rpt, "claude")
	if r.SpendUSD != 0.3 || r.APIEquivUSD != 0.3 {
		t.Errorf("spend = %v, api-equivalent = %v, want exactly 0.3 for both", r.SpendUSD, r.APIEquivUSD)
	}
	if got := renderJSON(t, rpt); !strings.Contains(got, `"usd":0.3,"api_equivalent_usd":0.3`) {
		t.Errorf("json carries float noise: %s", got)
	}
}

// One corrupt line (a negative or absurd figure, which a hand-edited or damaged
// log can carry) must not move a total: the readers drop such figures.
func TestAggregate_CorruptFiguresMoveNoTotal(t *testing.T) {
	rpt := AggregateLedger(chanOf(
		ledgerEvent("a", "claude", BillingAPI, [2]int64{}, &Usage{InputTokens: 100, OutputTokens: 10, TotalCostUSD: 0.25}, 0.5),
		ledgerEvent("a", "claude", BillingAPI, [2]int64{}, &Usage{InputTokens: -50, OutputTokens: 1 << 50, CacheRead: -1, TotalCostUSD: -3}, -2),
		ledgerEvent("a", "claude", BillingAPI, [2]int64{}, &Usage{InputTokens: 1, TotalCostUSD: 5e12}, 7e12),
	), AxisRuntime, 0)
	r := rowByKey(t, rpt, "claude")
	if r.SpendUSD != 0.25 {
		t.Errorf("spend = %v, want 0.25 (negative and absurd costs count as 0)", r.SpendUSD)
	}
	if r.APIEquivUSD != 0.5 {
		t.Errorf("api-equivalent = %v, want 0.5 (negative and absurd figures count as 0)", r.APIEquivUSD)
	}
	if r.Tokens != (TokenTotals{Input: 101, Output: 10}) {
		t.Errorf("tokens = %+v, want 101 in / 10 out (negative and absurd counts count as 0)", r.Tokens)
	}
}

func TestAggregateLedger_RanksByRealTokens_AggregateKeepsEstimateOrder(t *testing.T) {
	// "big-est" has huge estimates and few real tokens; "big-real" the opposite.
	evs := []Event{
		ledgerEvent("big-est", "claude", BillingSubscription, [2]int64{900, 900}, &Usage{InputTokens: 10}, 0),
		ledgerEvent("big-real", "claude", BillingSubscription, [2]int64{1, 1}, &Usage{InputTokens: 5000}, 0),
	}
	legacy := Aggregate(chanOf(evs...), AxisAgent, 0)
	if legacy.Rows[0].Key != "big-est" {
		t.Errorf("Aggregate must keep ranking by the est_* estimates (every API surface relies on it); got %q first", legacy.Rows[0].Key)
	}
	ledger := AggregateLedger(chanOf(evs...), AxisAgent, 0)
	if ledger.Rows[0].Key != "big-real" {
		t.Errorf("AggregateLedger must rank by real tokens; got %q first", ledger.Rows[0].Key)
	}
	// limit keeps the top rows by the active ranking.
	top := AggregateLedger(chanOf(evs...), AxisAgent, 1)
	if len(top.Rows) != 1 || top.Rows[0].Key != "big-real" {
		t.Errorf("limit 1 = %+v, want big-real", top.Rows)
	}
}

func TestAggregateLedger_TieBreaksByKeyDescending(t *testing.T) {
	rpt := AggregateLedger(chanOf(
		ledgerEvent("alpha", "r", BillingAPI, [2]int64{}, &Usage{InputTokens: 10}, 0),
		ledgerEvent("zulu", "r", BillingAPI, [2]int64{}, &Usage{InputTokens: 10}, 0),
	), AxisAgent, 0)
	if rpt.Rows[0].Key != "zulu" {
		t.Errorf("tie on tokens: got %q first, want zulu (key descending, as before)", rpt.Rows[0].Key)
	}
}

// A log with no ledger event aggregates identically whichever entry point is
// used, and is not a ledger report even when its rows carry usage and dollars.
func TestAggregateLedger_LegacyOnlyIsExactlyAggregate(t *testing.T) {
	evs := []Event{
		legacyEvent("a", "claude", [2]int64{5, 20}, nil),
		legacyEvent("b", "claude", [2]int64{100, 50}, &Usage{InputTokens: 210, OutputTokens: 110, CacheRead: 1000, TotalCostUSD: 0.5}),
		legacyEvent("c", "codex", [2]int64{9, 9}, &Usage{InputTokens: 99999}),
	}
	plain := Aggregate(chanOf(evs...), AxisAgent, 0)
	ledger := AggregateLedger(chanOf(evs...), AxisAgent, 0)
	if plain.Ledger || ledger.Ledger {
		t.Fatal("pre-K-136 rows (usage, no billing) must not switch a report to the ledger layout")
	}
	if !reflect.DeepEqual(plain, ledger) {
		t.Errorf("AggregateLedger differs from Aggregate on a legacy-only log:\n%+v\n%+v", plain, ledger)
	}
	// Ranked by the estimates: b (150) before a (25) before c (18).
	var keys []string
	for _, r := range ledger.Rows {
		keys = append(keys, r.Key)
	}
	if strings.Join(keys, ",") != "b,a,c" {
		t.Errorf("legacy order = %v, want b,a,c", keys)
	}
}

// ---- table -------------------------------------------------------------------

func renderTable(t *testing.T, rpt Report, since, by string) string {
	t.Helper()
	var sb strings.Builder
	if err := PrintTable(&sb, rpt, since, by); err != nil {
		t.Fatalf("PrintTable: %v", err)
	}
	return sb.String()
}

// The exact table of a legacy-only log, including one whose rows carry usage and
// a dollar figure: the K-136 columns must not appear.
func TestPrintTable_LegacyOnlyIsUnchanged(t *testing.T) {
	rpt := Aggregate(chanOf(
		legacyEvent("claude", "claude", [2]int64{25, 125}, nil),
		legacyEvent("claude", "claude", [2]int64{200, 100}, &Usage{InputTokens: 210, OutputTokens: 110, CacheRead: 1000, CacheCreation: 500, TotalCostUSD: 0.5}),
		legacyEvent("codex", "codex", [2]int64{75, 500}, &Usage{InputTokens: 15131, OutputTokens: 421}),
	), AxisRuntime, 0)
	want := "yakos cost — 3 dispatch event(s) by runtime\n" +
		"\n" +
		"  key                       count    ok  fail   dur(s) est_in_tok est_out_tok\n" +
		"  ------------------------ ------ ----- ----- -------- ---------- ----------\n" +
		"  codex                         1     1     0       10         75        500\n" +
		"  claude                        2     2     0       20        225        225\n" +
		"\n" +
		"  TOTAL                         3                   30        300        725\n"
	if got := renderTable(t, rpt, "", "runtime"); got != want {
		t.Errorf("legacy table changed:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintTable_LedgerLayout(t *testing.T) {
	rpt := AggregateLedger(chanOf(mixedEvents()...), AxisRuntime, 0)
	got := renderTable(t, rpt, "", "runtime")
	want := "yakos cost — 8 dispatch event(s) by runtime\n" +
		"\n" +
		"  key                       count    ok  fail   dur(s) est_in_tok est_out_tok         in        out      cache     tokens        usd  api_equiv\n" +
		"  ------------------------ ------ ----- ----- -------- ---------- ---------- ---------- ---------- ---------- ---------- ---------- ----------\n" +
		"  claude                        5     5     0       50        485        815       7210       1500      73500      82210     0.8012     0.6106\n" +
		"  codex                         1     1     0       10        300        700       7707        421       7424      15552          -          -\n" +
		"  agy                           2     2     0       20         60        120      12863          1          0      12864          -          -\n" +
		"\n" +
		"  TOTAL                         8                   80        845       1635      27780       1922      80924     110626     0.8012     0.6106\n" +
		"\n" +
		"  tokens = in + out + cache, as logged; est_in_tok and est_out_tok are size estimates and are not counted.\n" +
		"  api_equiv = what subscription runs would have cost at API rates; it is not spend and is not part of usd.\n"
	if got != want {
		t.Errorf("ledger table:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// Subscription-only logs show tokens and no dollar column at all, and no
// api_equiv column when the harness reported no figure.
func TestPrintTable_NoDollarColumnsWithoutDollars(t *testing.T) {
	rpt := AggregateLedger(chanOf(
		ledgerEvent("frontend", "codex", BillingSubscription, [2]int64{1, 1}, &Usage{InputTokens: 7707, OutputTokens: 421, CacheRead: 7424, TotalCostUSD: 3}, 0),
		ledgerEvent("g", "agy", BillingSubscription, [2]int64{1, 1}, &Usage{InputTokens: 12863, OutputTokens: 1}, 0),
	), AxisRuntime, 0)
	got := renderTable(t, rpt, "", "runtime")
	for _, banned := range []string{"usd", "api_equiv", "$", "3.0000"} {
		if strings.Contains(got, banned) {
			t.Errorf("a subscription-only log must show no %q:\n%s", banned, got)
		}
	}
	for _, need := range []string{" in ", " out ", " cache ", " tokens", "15552", "12864"} {
		if !strings.Contains(got, need) {
			t.Errorf("missing %q:\n%s", need, got)
		}
	}
}

// headerOf returns the column names of the table's header line.
func headerOf(t *testing.T, table string) []string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "key" {
			return f
		}
	}
	t.Fatalf("no header line in:\n%s", table)
	return nil
}

func hasCol(cols []string, name string) bool {
	for _, c := range cols {
		if c == name {
			return true
		}
	}
	return false
}

// usd appears with api spend alone; api_equiv appears with an api-equivalent
// alone. Each is independent of the other.
func TestPrintTable_DollarColumnsAreIndependent(t *testing.T) {
	onlyAPI := renderTable(t, AggregateLedger(chanOf(
		ledgerEvent("a", "claude", BillingAPI, [2]int64{}, &Usage{InputTokens: 1, TotalCostUSD: 0.01}, 0),
	), AxisRuntime, 0), "", "runtime")
	cols := headerOf(t, onlyAPI)
	if !hasCol(cols, "usd") || hasCol(cols, "api_equiv") {
		t.Errorf("api spend alone: want usd and no api_equiv, header %v:\n%s", cols, onlyAPI)
	}
	if strings.Contains(onlyAPI, "not spend") {
		t.Errorf("the not-spend footnote belongs to the api_equiv column only:\n%s", onlyAPI)
	}

	onlyEquiv := renderTable(t, AggregateLedger(chanOf(
		ledgerEvent("a", "claude", BillingSubscription, [2]int64{}, &Usage{InputTokens: 1}, 0.2),
	), AxisRuntime, 0), "", "runtime")
	cols = headerOf(t, onlyEquiv)
	if hasCol(cols, "usd") || !hasCol(cols, "api_equiv") {
		t.Errorf("api-equivalent alone: want api_equiv and no usd, header %v:\n%s", cols, onlyEquiv)
	}
	if !strings.Contains(onlyEquiv, "not spend") {
		t.Errorf("the api_equiv column needs its not-spend footnote:\n%s", onlyEquiv)
	}
}

func TestUSDCell(t *testing.T) {
	cases := map[float64]string{0: "-", -1: "-", 0.00004: "<0.0001", 0.0001: "0.0001", 0.3012: "0.3012", 12.5: "12.5000"}
	for in, want := range cases {
		if got := usdCell(in); got != want {
			t.Errorf("usdCell(%v) = %q, want %q", in, got, want)
		}
	}
}

// ---- JSON --------------------------------------------------------------------

func renderJSON(t *testing.T, rpt Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := PrintJSON(&buf, rpt); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	return buf.String()
}

func TestPrintJSON_LegacyOnlyIsUnchanged(t *testing.T) {
	rpt := Aggregate(chanOf(
		legacyEvent("b", "claude", [2]int64{200, 100}, &Usage{InputTokens: 210, CacheRead: 1000, TotalCostUSD: 0.5}),
	), AxisAgent, 0)
	want := `{"events":1,"rows":[{"key":"b","count":1,"ok":1,"fail":0,"total_duration_s":10,"total_in_tokens":200,"total_out_tokens":100}]}` + "\n"
	if got := renderJSON(t, rpt); got != want {
		t.Errorf("legacy JSON changed:\n got: %s\nwant: %s", got, want)
	}
}

func TestPrintJSON_LedgerRowsAddTokensAndDollars(t *testing.T) {
	rpt := AggregateLedger(chanOf(mixedEvents()...), AxisRuntime, 0)
	got := renderJSON(t, rpt)

	var doc struct {
		Events int64 `json:"events"`
		Rows   []map[string]json.RawMessage
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("decode %q: %v", got, err)
	}
	if doc.Events != 8 || len(doc.Rows) != 3 {
		t.Fatalf("events=%d rows=%d, want 8 and 3", doc.Events, len(doc.Rows))
	}
	// Every legacy key survives, in front, in the original order.
	if !strings.Contains(got, `{"key":"claude","count":5,"ok":5,"fail":0,"total_duration_s":50,"total_in_tokens":485,"total_out_tokens":815,"tokens":{"input":7210,"output":1500,"cache_read":71000,"cache_creation":2500,"total":82210},"usd":0.8012,"api_equivalent_usd":0.6106}`) {
		t.Errorf("claude row JSON not as specified:\n%s", got)
	}
	// codex: tokens, and neither dollar key.
	if !strings.Contains(got, `{"key":"codex","count":1,"ok":1,"fail":0,"total_duration_s":10,"total_in_tokens":300,"total_out_tokens":700,"tokens":{"input":7707,"output":421,"cache_read":7424,"cache_creation":0,"total":15552}}`) {
		t.Errorf("codex row must carry tokens and no usd/api_equivalent_usd:\n%s", got)
	}
	if strings.Count(got, `"usd"`) != 1 || strings.Count(got, `"api_equivalent_usd"`) != 1 {
		t.Errorf("usd and api_equivalent_usd must appear only on the claude row:\n%s", got)
	}
}

// The other surfaces marshal Report and Row directly (REST /v1/cost, JSON-RPC
// yakos.cost.aggregate, the MCP cost tool). The K-136 fields must not leak into
// them: their output is part of those APIs' contracts.
func TestRowAndReportJSON_OtherSurfacesAreUnchanged(t *testing.T) {
	rpt := Aggregate(chanOf(mixedEvents()...), AxisRuntime, 0)
	if !rpt.Ledger {
		t.Fatal("fixture should be a ledger report")
	}
	b, err := json.Marshal(rpt)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top["Events"] == nil || top["Rows"] == nil {
		t.Errorf("Report marshals with keys %v, want exactly Events and Rows", keysOf(top))
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(top["Rows"], &rows); err != nil {
		t.Fatal(err)
	}
	want := []string{"Count", "Fail", "Key", "OK", "TotalDurationS", "TotalInTokens", "TotalOutTokens"}
	for _, r := range rows {
		if got := keysOf(r); !reflect.DeepEqual(got, want) {
			t.Errorf("Row marshals with keys %v, want %v", got, want)
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
