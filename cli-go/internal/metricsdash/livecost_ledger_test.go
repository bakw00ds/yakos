package metricsdash_test

// K-136: the Cost tab reports tokens first and counts dollars only for runs
// billed per API call. These tests pin GET /api/metrics/live_cost (and the
// snapshot overlay that reuses its dollar figure):
//
//   - total_cost_usd is Event.SpendUSD summed: api-billed and pre-K-136 rows
//     count; a subscription or local row never does, whatever its usage cost;
//   - tokens come from the usage object (all four kinds), never from the est_*
//     size estimates, which every line below carries in large amounts so that
//     any mixing is visible;
//   - api_equivalent_usd is informational and never added to spend;
//   - the per-runtime breakdown is deterministic and always an array.
//
// The log is written through statepath (YAKOS_DISPATCH_LOG points it at a temp
// dir), so nothing touches the operator's real state.

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/metrics"
	"github.com/bakw00ds/yakos/internal/metricsdash"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// logLines writes lines as the active log of an isolated state dir and returns
// that dir (the server's DispatchLogDir).
func logLines(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(statepath.DispatchLog(), []byte(body), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return dir
}

// fin builds one dispatch_finished line. Every line carries huge est_* size
// estimates: a reader that mixed them into tokens would be off by millions.
// extra is raw JSON members spliced in after the fixed ones (may be empty).
func fin(runtime, extra string) string {
	s := `{"type":"dispatch_finished","ts":"2026-06-01T00:00:00Z","agent":"worker","runtime":` + strconv.Quote(runtime) +
		`,"exit_code":0,"duration_s":1,"output_bytes":100,"task_bytes":50,"est_input_tokens":7000000,"est_output_tokens":3000000`
	if extra != "" {
		s += "," + extra
	}
	return s + "}"
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// usageJSON is the usage member of a finished line.
func usageJSON(in, out, cacheRead, cacheCreate int64, costUSD float64) string {
	return `"usage":{"input_tokens":` + strconv.FormatInt(in, 10) +
		`,"output_tokens":` + strconv.FormatInt(out, 10) +
		`,"cache_read":` + strconv.FormatInt(cacheRead, 10) +
		`,"cache_creation":` + strconv.FormatInt(cacheCreate, 10) +
		`,"duration_ms":1000,"total_cost_usd":` + num(costUSD) + `}`
}

// liveBody is the decoded GET /api/metrics/live_cost response.
type liveBody struct {
	TotalCostUSD float64 `json:"total_cost_usd"`
	EventCount   int     `json:"event_count"`
	Tokens       struct {
		Input         int64 `json:"input"`
		Output        int64 `json:"output"`
		CacheRead     int64 `json:"cache_read"`
		CacheCreation int64 `json:"cache_creation"`
	} `json:"tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	Runtimes         []struct {
		Runtime          string  `json:"runtime"`
		Dispatches       int64   `json:"dispatches"`
		Tokens           int64   `json:"tokens"`
		USD              float64 `json:"usd"`
		APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	} `json:"runtimes"`
}

// getLive fetches live_cost and returns the decoded body plus the raw JSON.
func getLive(t *testing.T, ts *httptest.Server, tok string) (liveBody, string) {
	t.Helper()
	resp := get(t, ts.URL+"/api/metrics/live_cost", tok)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live_cost status=%d; want 200", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read live_cost: %v", err)
	}
	var b liveBody
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("decode live_cost: %v\n%s", err, raw)
	}
	return b, string(raw)
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestLiveCost_DollarsAreAPISpendOnly(t *testing.T) {
	dir := logLines(t,
		// api-billed run: counts.
		fin("claude", `"billing":"api","cost_source":"harness",`+usageJSON(100, 50, 0, 0, 0.30)),
		// subscription run carrying a stray nonzero usage cost plus its
		// api-equivalent figure: tokens yes, spend no.
		fin("claude", `"billing":"subscription","cost_source":"harness","api_equivalent_usd":0.5,`+usageJSON(200, 80, 0, 0, 0.50)),
		fin("codex", `"billing":"subscription",`+usageJSON(300, 90, 0, 0, 0)),
		// local run: not spend, whatever the figure.
		fin("agy", `"billing":"local",`+usageJSON(5, 5, 0, 0, 9.99)),
		// legacy row (no billing field): its dollar figure was the only cost
		// there was, so it still counts.
		fin("claude", usageJSON(10, 5, 0, 0, 0.20)),
		// a billing value this version does not know counts too (the safe
		// direction for a cost guard).
		fin("claude", `"billing":"surprise",`+usageJSON(1, 1, 0, 0, 0.10)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, _ := getLive(t, ts, tok)
	if b.EventCount != 6 {
		t.Errorf("event_count=%d; want 6", b.EventCount)
	}
	// api 0.30 + legacy 0.20 + unknown 0.10. A reader summing usage.total_cost_usd
	// would report 11.09 here.
	if !near(b.TotalCostUSD, 0.60) {
		t.Errorf("total_cost_usd=%v; want 0.60 (api + legacy + unknown billing only)", b.TotalCostUSD)
	}
}

func TestLiveCost_APIEquivalentIsInformationalNeverSpend(t *testing.T) {
	dir := logLines(t,
		// Go writer shape for a claude subscription run: usage cost 0, the
		// harness figure stored as api_equivalent_usd.
		fin("claude", `"billing":"subscription","cost_source":"harness","api_equivalent_usd":0.42,`+usageJSON(1000, 200, 5000, 100, 0)),
		fin("claude", `"billing":"subscription","cost_source":"harness","api_equivalent_usd":0.58,`+usageJSON(900, 100, 4000, 0, 0)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, _ := getLive(t, ts, tok)
	if b.TotalCostUSD != 0 {
		t.Errorf("total_cost_usd=%v; want 0: api_equivalent_usd must never be added to spend", b.TotalCostUSD)
	}
	if !near(b.APIEquivalentUSD, 1.00) {
		t.Errorf("api_equivalent_usd=%v; want 1.00 (0.42 + 0.58)", b.APIEquivalentUSD)
	}
	if len(b.Runtimes) != 1 || b.Runtimes[0].USD != 0 || !near(b.Runtimes[0].APIEquivalentUSD, 1.00) {
		t.Errorf("runtimes=%+v; want one claude row with usd 0 and api_equivalent_usd 1.00", b.Runtimes)
	}
}

func TestLiveCost_TokensByKindAndTotal(t *testing.T) {
	dir := logLines(t,
		// claude, anthropic convention: fresh input, output, both cache kinds.
		fin("claude", `"billing":"subscription",`+usageJSON(100, 50, 1000, 200, 0)),
		// codex written by the Go dispatcher: cached tokens split into cache_read.
		fin("codex", `"billing":"subscription",`+usageJSON(7707, 421, 7424, 0, 0)),
		// codex written by the bash dispatcher: the whole prompt (15131, of which
		// 7424 cached) sits in input_tokens and cache_read is 0.
		fin("codex", usageJSON(15131, 400, 0, 0, 0)),
		// a bash row with no usage object at all: only estimates, no tokens.
		fin("claude", ""),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, _ := getLive(t, ts, tok)
	if b.EventCount != 4 {
		t.Errorf("event_count=%d; want 4", b.EventCount)
	}
	if b.Tokens.Input != 100+7707+15131 {
		t.Errorf("tokens.input=%d; want %d", b.Tokens.Input, 100+7707+15131)
	}
	if b.Tokens.Output != 50+421+400 {
		t.Errorf("tokens.output=%d; want %d", b.Tokens.Output, 50+421+400)
	}
	if b.Tokens.CacheRead != 1000+7424 {
		t.Errorf("tokens.cache_read=%d; want %d", b.Tokens.CacheRead, 1000+7424)
	}
	if b.Tokens.CacheCreation != 200 {
		t.Errorf("tokens.cache_creation=%d; want 200", b.Tokens.CacheCreation)
	}
	// The total adds all four kinds and nothing else: each line carries 10M of
	// est_* estimates, which must not appear.
	const want = (100 + 7707 + 15131) + (50 + 421 + 400) + (1000 + 7424) + 200
	if b.TotalTokens != want {
		t.Errorf("total_tokens=%d; want %d (usage counts only, no est_* estimates)", b.TotalTokens, want)
	}
}

func TestLiveCost_EstimatesAreNeverTokens(t *testing.T) {
	dir := logLines(t,
		fin("claude", ""),
		fin("codex", `"billing":"subscription"`), // ledger row that reported no usage
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, _ := getLive(t, ts, tok)
	if b.TotalTokens != 0 || b.Tokens.Input != 0 || b.Tokens.Output != 0 {
		t.Errorf("tokens=%+v total=%d; want zero when no event reported usage (est_* are size estimates, not counts)", b.Tokens, b.TotalTokens)
	}
	if b.EventCount != 2 {
		t.Errorf("event_count=%d; want 2", b.EventCount)
	}
	// Both runtimes are listed (they dispatched) but carry no tokens.
	if len(b.Runtimes) != 2 {
		t.Fatalf("runtimes=%+v; want a claude and a codex row", b.Runtimes)
	}
	for _, r := range b.Runtimes {
		if r.Dispatches != 1 || r.Tokens != 0 {
			t.Errorf("runtime %q: dispatches=%d tokens=%d; want 1 and 0", r.Runtime, r.Dispatches, r.Tokens)
		}
	}
}

func TestLiveCost_PerRuntimeRows(t *testing.T) {
	dir := logLines(t,
		// claude: one api run and one subscription run.
		fin("claude", `"billing":"api",`+usageJSON(1000, 500, 0, 0, 0.30)),
		fin("claude", `"billing":"subscription","api_equivalent_usd":0.40,`+usageJSON(1500, 500, 0, 0, 0)),
		// codex: subscription tokens, no dollars at all.
		fin("codex", `"billing":"subscription",`+usageJSON(500, 100, 7400, 0, 0)),
		// agy: two runs.
		fin("agy", `"billing":"subscription",`+usageJSON(60, 40, 0, 0, 0)),
		fin("agy", `"billing":"subscription",`+usageJSON(60, 40, 0, 0, 0)),
		// an event whose runtime is empty is labelled.
		fin("", usageJSON(30, 20, 0, 0, 0.01)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, raw := getLive(t, ts, tok)
	type row struct {
		runtime    string
		dispatches int64
		tokens     int64
		usd        float64
		apiEquiv   float64
	}
	want := []row{
		{"codex", 1, 8000, 0, 0},
		{"claude", 2, 3500, 0.30, 0.40},
		{"agy", 2, 200, 0, 0},
		{"(unknown)", 1, 50, 0.01, 0},
	}
	if len(b.Runtimes) != len(want) {
		t.Fatalf("runtimes=%+v; want %d rows\n%s", b.Runtimes, len(want), raw)
	}
	for i, w := range want {
		g := b.Runtimes[i]
		if g.Runtime != w.runtime || g.Dispatches != w.dispatches || g.Tokens != w.tokens ||
			!near(g.USD, w.usd) || !near(g.APIEquivalentUSD, w.apiEquiv) {
			t.Errorf("runtimes[%d]=%+v; want %+v", i, g, w)
		}
	}
	// codex shows tokens and no dollars.
	if b.Runtimes[0].USD != 0 {
		t.Errorf("codex usd=%v; want 0", b.Runtimes[0].USD)
	}
	// The rows add up to the totals.
	var sumTokens int64
	var sumUSD float64
	for _, r := range b.Runtimes {
		sumTokens += r.Tokens
		sumUSD += r.USD
	}
	if sumTokens != b.TotalTokens || !near(sumUSD, b.TotalCostUSD) {
		t.Errorf("rows sum to %d tokens / %v usd; totals are %d / %v", sumTokens, sumUSD, b.TotalTokens, b.TotalCostUSD)
	}
}

func TestLiveCost_RuntimeOrderIsDeterministic(t *testing.T) {
	// Tokens first; equal tokens: more dispatches first; equal again: runtime
	// name ascending. Map iteration order must never leak into the response.
	dir := logLines(t,
		fin("beta", usageJSON(10, 0, 0, 0, 0)),
		fin("alpha", usageJSON(10, 0, 0, 0, 0)),
		fin("zeta", usageJSON(5, 0, 0, 0, 0)),
		fin("zeta", usageJSON(5, 0, 0, 0, 0)),
		fin("big", usageJSON(99, 0, 0, 0, 0)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	for i := 0; i < 5; i++ { // repeated reads, same order every time
		b, _ := getLive(t, ts, tok)
		var got []string
		for _, r := range b.Runtimes {
			got = append(got, r.Runtime)
		}
		if strings.Join(got, ",") != "big,zeta,alpha,beta" {
			t.Fatalf("runtime order=%v; want [big zeta alpha beta] (tokens desc, dispatches desc, name asc)", got)
		}
	}
}

func TestLiveCost_EmptyLogShape(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"empty file": func(t *testing.T) string { return logLines(t) },
		"no log file": func(t *testing.T) string {
			dir := t.TempDir()
			t.Setenv("YAKOS_DISPATCH_LOG", dir)
			return dir
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			ts, tok := newTestServerWithDispatchLog(t, "", mk(t))
			b, raw := getLive(t, ts, tok)
			if b.EventCount != 0 || b.TotalCostUSD != 0 || b.TotalTokens != 0 || b.APIEquivalentUSD != 0 {
				t.Errorf("zero log: %+v", b)
			}
			// The new keys exist and the runtimes list is an array, never null,
			// so a client can iterate it without a guard.
			for _, frag := range []string{
				`"runtimes":[]`,
				`"tokens":{"input":0,"output":0,"cache_read":0,"cache_creation":0}`,
				`"total_tokens":0`,
				`"api_equivalent_usd":0`,
				`"total_cost_usd":0`,
				`"event_count":0`,
			} {
				if !strings.Contains(raw, frag) {
					t.Errorf("response lacks %s: %s", frag, raw)
				}
			}
		})
	}
}

func TestLiveCost_ExistingKeysKeepNameAndMeaning(t *testing.T) {
	// A pre-K-136 client reads only total_cost_usd and event_count; both must
	// still be there with the same meaning (legacy rows' cost is spend).
	dir := logLines(t,
		fin("claude", usageJSON(200, 50, 0, 0, 0.10)),
		fin("claude", usageJSON(200, 50, 0, 0, 0.25)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	resp := get(t, ts.URL+"/api/metrics/live_cost", tok)
	var m map[string]json.RawMessage
	decodeJSON(t, resp, &m)
	for _, k := range []string{"total_cost_usd", "event_count", "tokens", "total_tokens", "api_equivalent_usd", "runtimes"} {
		if _, ok := m[k]; !ok {
			t.Errorf("response lacks key %q", k)
		}
	}
	var cost float64
	var n int
	_ = json.Unmarshal(m["total_cost_usd"], &cost)
	_ = json.Unmarshal(m["event_count"], &n)
	if !near(cost, 0.35) || n != 2 {
		t.Errorf("total_cost_usd=%v event_count=%d; want 0.35 and 2", cost, n)
	}
}

func TestLiveCost_MixedLegacyAndLedgerLog(t *testing.T) {
	dir := logLines(t,
		// bash-written legacy rows: no usage, only estimates.
		fin("claude", ""),
		fin("codex", ""),
		// legacy Go row (usage, no billing): tokens count, dollars count.
		fin("claude", usageJSON(210, 110, 1000, 500, 0.5)),
		// legacy Go codex row (no billing): tokens, no dollars reported.
		fin("codex", usageJSON(7707, 421, 7424, 0, 0)),
		// ledger rows.
		fin("claude", `"billing":"subscription","api_equivalent_usd":0.4231,`+usageJSON(1200, 340, 50000, 2000, 0)),
		fin("claude", `"billing":"api","cost_source":"harness",`+usageJSON(800, 120, 0, 0, 0.3012)),
		fin("codex", `"billing":"subscription",`+usageJSON(300, 90, 0, 0, 0)),
		fin("agy", `"billing":"subscription",`+usageJSON(12863, 1, 0, 0, 0)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, _ := getLive(t, ts, tok)
	if b.EventCount != 8 {
		t.Errorf("event_count=%d; want 8", b.EventCount)
	}
	// Dollars: legacy 0.5 + api 0.3012; the subscription figure is not added.
	if !near(b.TotalCostUSD, 0.8012) {
		t.Errorf("total_cost_usd=%v; want 0.8012", b.TotalCostUSD)
	}
	if !near(b.APIEquivalentUSD, 0.4231) {
		t.Errorf("api_equivalent_usd=%v; want 0.4231", b.APIEquivalentUSD)
	}
	// Tokens: every usage object, the bash rows contribute none.
	want := int64(210+110+1000+500) + int64(7707+421+7424) + int64(1200+340+50000+2000) +
		int64(800+120) + int64(300+90) + int64(12863+1)
	if b.TotalTokens != want {
		t.Errorf("total_tokens=%d; want %d", b.TotalTokens, want)
	}
}

func TestLiveCost_CorruptFiguresCannotMoveTotals(t *testing.T) {
	dir := logLines(t,
		fin("claude", usageJSON(10, 10, 0, 0, 0.25)), // the one sane row
		// negative and absurd dollars, negative and absurd token counts, and a
		// negative / absurd api-equivalent figure.
		fin("claude", usageJSON(1, 1, 0, 0, -3)),
		fin("claude", usageJSON(1, 1, 0, 0, 1e18)),
		fin("claude", `"billing":"subscription","api_equivalent_usd":-7,`+usageJSON(-50, -60, -70, -80, 0)),
		fin("claude", `"billing":"subscription","api_equivalent_usd":1e18,`+usageJSON(9000000000000000000, 9000000000000000000, 0, 0, 0)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, _ := getLive(t, ts, tok)
	if !near(b.TotalCostUSD, 0.25) {
		t.Errorf("total_cost_usd=%v; want 0.25: negative and absurd costs count as 0", b.TotalCostUSD)
	}
	if b.APIEquivalentUSD != 0 {
		t.Errorf("api_equivalent_usd=%v; want 0: negative and absurd figures count as 0", b.APIEquivalentUSD)
	}
	// 10+10 from the sane row and 1+1 from each of the first two corrupt-cost
	// rows (their token counts are sane); the rows with corrupt counts add 0.
	if b.TotalTokens != 10+10+1+1+1+1 {
		t.Errorf("total_tokens=%d; want 24: corrupt token counts count as 0", b.TotalTokens)
	}
}

func TestLiveCost_CacheRefreshesWhenLogGrows(t *testing.T) {
	dir := logLines(t, fin("claude", `"billing":"api",`+usageJSON(100, 0, 0, 0, 0.10)))
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	b, _ := getLive(t, ts, tok)
	if b.TotalTokens != 100 || !near(b.TotalCostUSD, 0.10) {
		t.Fatalf("first read: %+v", b)
	}

	f, err := os.OpenFile(statepath.DispatchLog(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	if _, err := f.WriteString(fin("codex", `"billing":"subscription",`+usageJSON(900, 0, 0, 0, 0)) + "\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = f.Close()

	b, _ = getLive(t, ts, tok)
	if b.TotalTokens != 1000 || b.EventCount != 2 || len(b.Runtimes) != 2 {
		t.Errorf("after append: %+v; want 1000 tokens, 2 events, 2 runtimes (the cache must key on the log changing)", b)
	}
}

func TestLiveCost_ConcurrentReadsAgree(t *testing.T) {
	// The cached result is shared between requests; concurrent readers (and the
	// race detector) must see one consistent answer.
	dir := logLines(t,
		fin("claude", `"billing":"api",`+usageJSON(100, 50, 10, 5, 0.25)),
		fin("codex", `"billing":"subscription",`+usageJSON(900, 10, 0, 0, 0)),
	)
	ts, tok := newTestServerWithDispatchLog(t, "", dir)

	const readers = 16
	errs := make(chan string, readers)
	for i := 0; i < readers; i++ {
		go func() {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/metrics/live_cost", nil)
			req.Header.Set("Authorization", "Bearer "+tok)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errs <- err.Error()
				return
			}
			defer func() { _ = resp.Body.Close() }()
			var b liveBody
			if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
				errs <- err.Error()
				return
			}
			if b.TotalTokens != 1075 || !near(b.TotalCostUSD, 0.25) || len(b.Runtimes) != 2 {
				errs <- "inconsistent answer"
				return
			}
			errs <- ""
		}()
	}
	for i := 0; i < readers; i++ {
		if msg := <-errs; msg != "" {
			t.Error(msg)
		}
	}
}

// ---- snapshot overlay uses the api-only figure -------------------------------

type overlaySnap struct {
	Trigger string `json:"trigger"`
	Metrics struct {
		Efficiency struct {
			TotalCostUSD *float64 `json:"total_cost_usd"`
		} `json:"efficiency"`
	} `json:"metrics"`
}

func TestLiveCost_SnapshotOverlayIsAPISpendOnly(t *testing.T) {
	// A subscription-only log: the overlay must show 0 spend, not the harness's
	// api-equivalent figure and not a stray usage cost.
	lines := []string{
		fin("claude", `"billing":"subscription","api_equivalent_usd":0.42,`+usageJSON(1000, 200, 0, 0, 0.50)),
		fin("codex", `"billing":"subscription",`+usageJSON(300, 90, 0, 0, 0)),
	}

	t.Run("no history", func(t *testing.T) {
		dir := logLines(t, lines...)
		ts, tok := newTestServerWithDispatchLog(t, "", dir)
		var snap overlaySnap
		decodeJSON(t, get(t, ts.URL+"/api/metrics/snapshot", tok), &snap)
		if snap.Trigger != "dispatch" {
			t.Errorf("trigger=%q; want the synthetic dispatch snapshot", snap.Trigger)
		}
		if snap.Metrics.Efficiency.TotalCostUSD == nil {
			t.Fatal("total_cost_usd is nil; the overlay should carry the live figure (0)")
		}
		if got := *snap.Metrics.Efficiency.TotalCostUSD; got != 0 {
			t.Errorf("overlay total_cost_usd=%v; want 0 (subscription runs are not spend)", got)
		}
	})

	t.Run("history without cost", func(t *testing.T) {
		dir := logLines(t, lines...)
		projectDir := writeFixtureHistory(t, []metrics.Snapshot{
			makeSnap("abc123", "main", mustNow(), nil),
		})
		ts, tok := newTestServerWithDispatchLog(t, projectDir, dir)
		var snap overlaySnap
		decodeJSON(t, get(t, ts.URL+"/api/metrics/snapshot", tok), &snap)
		if snap.Metrics.Efficiency.TotalCostUSD == nil {
			t.Fatal("total_cost_usd is nil; the overlay should carry the live figure (0)")
		}
		if got := *snap.Metrics.Efficiency.TotalCostUSD; got != 0 {
			t.Errorf("overlay total_cost_usd=%v; want 0 (subscription runs are not spend)", got)
		}
	})
}

// ---- auth and the console mount ----------------------------------------------

func TestLiveCost_RequiresTokenOnHandler(t *testing.T) {
	dir := logLines(t, fin("claude", usageJSON(1, 1, 0, 0, 0.01)))
	ts, _ := newTestServerWithDispatchLog(t, "", dir)

	resp := get(t, ts.URL+"/api/metrics/live_cost", "")
	defer drainClose(resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status=%d; want 401", resp.StatusCode)
	}
	resp2 := get(t, ts.URL+"/api/metrics/live_cost", strings.Repeat("a", 64))
	defer drainClose(resp2)
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token: status=%d; want 403", resp2.StatusCode)
	}
}

func TestLiveCost_HandlerNoTokenServesSameShape(t *testing.T) {
	// The console mounts HandlerNoToken (auth is enforced at its edge); the Cost
	// tab iframe calls live_cost without a bearer header.
	dir := logLines(t,
		fin("claude", `"billing":"api",`+usageJSON(100, 50, 0, 0, 0.30)),
		fin("codex", `"billing":"subscription",`+usageJSON(900, 10, 0, 0, 0)),
	)
	stateDir := t.TempDir()
	tok, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateMetricsToken: %v", err)
	}
	srv := metricsdash.New(metricsdash.Config{Token: tok, DispatchLogDir: dir})
	ts := httptest.NewServer(srv.HandlerNoToken())
	t.Cleanup(ts.Close)

	b, _ := getLive(t, ts, "") // no Authorization header
	if !near(b.TotalCostUSD, 0.30) || b.TotalTokens != 1060 || len(b.Runtimes) != 2 {
		t.Errorf("HandlerNoToken live_cost: %+v; want 0.30 usd, 1060 tokens, 2 runtimes", b)
	}
	if b.Runtimes[0].Runtime != "codex" || b.Runtimes[0].USD != 0 {
		t.Errorf("first row=%+v; want codex with tokens and no dollars", b.Runtimes[0])
	}
}

// mustNow is a fixed-resolution timestamp for fixture snapshots.
func mustNow() time.Time { return time.Now().UTC().Truncate(time.Second) }
