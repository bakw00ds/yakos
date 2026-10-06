// cost_ledger_test.go — `yakos cost` after K-136 (tokens are the primary unit).
//
// The bash twin of `yakos cost` is not changed, so there is nothing to compare
// the new layout with; these tests pin it with golden files instead, and pin the
// original layout with goldens captured from the binary as it was before K-136.
//
//   - A log with no ledger event (bash rows, Go rows written before K-136, even
//     ones that carry usage and a dollar figure) prints byte for byte what it
//     always printed. cost_parity_test.go compares that against bash; the legacy-*
//     goldens here repeat it for the fixtures that matter and need no bash.
//   - A log with ledger events keeps the seven original columns and appends real
//     token columns, then usd and api_equiv only when there is something to show.
//
// Every case runs twice: in process (internal/cost, always) and through
// bin/yakos (skipped when it is not built: run `make build` first). Both must
// match the same golden file.
//
// Regenerate the goldens from the binary with:
//
//	make build && cd cli-go && go test ./cmd/yakos -run TestCostGolden_Binary -update-cost-golden
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/statepath"
)

var updateCostGolden = flag.Bool("update-cost-golden", false,
	"rewrite the cost golden files from the Go binary's output instead of comparing")

// costGoldenCases maps a golden file to the fixture and arguments that produce it.
var costGoldenCases = []struct {
	golden  string
	fixture string
	args    []string
}{
	// Legacy-only logs: identical to before K-136.
	{"legacy-clean.by-runtime.table.golden", "clean-log.ndjson", []string{"--by", "runtime"}},
	{"legacy-clean.by-agent.json.golden", "clean-log.ndjson", []string{"--by", "agent", "--json"}},
	// mixed-models-log has a Go row with usage and a dollar figure but no billing
	// field: it is still a legacy row and must not switch the layout.
	{"legacy-mixed-models.by-agent.table.golden", "mixed-models-log.ndjson", []string{"--by", "agent"}},
	{"legacy-mixed-models.by-runtime.json.golden", "mixed-models-log.ndjson", []string{"--by", "runtime", "--json"}},

	// Ledger logs.
	{"ledger-mixed.by-runtime.table.golden", "ledger-mixed-log.ndjson", []string{"--by", "runtime"}},
	{"ledger-mixed.by-runtime.json.golden", "ledger-mixed-log.ndjson", []string{"--by", "runtime", "--json"}},
	{"ledger-mixed.by-agent.table.golden", "ledger-mixed-log.ndjson", []string{"--by", "agent"}},
	{"ledger-mixed.by-agent.json.golden", "ledger-mixed-log.ndjson", []string{"--by", "agent", "--json"}},
	// --since cuts away every ledger event: the aggregated events decide the
	// layout, so this is the legacy layout again.
	{"ledger-mixed.since-march.table.golden", "ledger-mixed-log.ndjson", []string{"--by", "agent", "--since", "2026-03-01"}},
	{"ledger-subscription.by-runtime.table.golden", "ledger-subscription-log.ndjson", []string{"--by", "runtime"}},
	{"ledger-subscription.by-runtime.json.golden", "ledger-subscription-log.ndjson", []string{"--by", "runtime", "--json"}},
}

// installCostFixture copies a fixture into a fresh state directory under the
// name the reader expects, points YAKOS_DISPATCH_LOG at it and returns the dir.
func installCostFixture(t *testing.T, fixture string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(costFixtureDir(t), fixture))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", fixture, err)
	}
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	if err := os.WriteFile(statepath.DispatchLog(), src, 0o644); err != nil {
		t.Fatalf("installing fixture %s: %v", fixture, err)
	}
	return statepath.Dir()
}

// costRenderer renders `yakos cost <args>` over a fixture.
type costRenderer func(t *testing.T, fixture string, args []string) string

// renderCostInProcess runs the same pipeline runCost runs, without the process.
func renderCostInProcess(t *testing.T, fixture string, args []string) string {
	t.Helper()
	dir := installCostFixture(t, fixture)
	by, since, asJSON := "runtime", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--by":
			i++
			by = args[i]
		case "--since":
			i++
			since = args[i]
		case "--json":
			asJSON = true
		default:
			t.Fatalf("renderCostInProcess: unsupported argument %q", args[i])
		}
	}
	axis, err := cost.ParseAxis(by)
	if err != nil {
		t.Fatal(err)
	}
	files, err := cost.LogFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	rpt := cost.AggregateLedger(cost.StreamFiles(files, since), axis, 0)
	var buf bytes.Buffer
	if asJSON {
		err = cost.PrintJSON(&buf, rpt)
	} else {
		err = cost.PrintTable(&buf, rpt, since, by)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// renderCostViaBinary runs the built binary; the test is skipped without one.
func renderCostViaBinary(t *testing.T, fixture string, args []string) string {
	t.Helper()
	bin := resolveGoBinary()
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("Go yakos binary not found at %q (build with `make build`): %v", bin, err)
	}
	dir := installCostFixture(t, fixture)
	// HOME is a scratch dir too: nothing the binary does may reach the real one.
	out, code := runGoCost(t, bin, append([]string{"cost"}, args...),
		map[string]string{"YAKOS_DISPATCH_LOG": dir, "HOME": t.TempDir()})
	if code != 0 {
		t.Fatalf("yakos cost %v exited %d:\n%s", args, code, out)
	}
	return out
}

// checkCostGolden compares got with the golden file; with -update-cost-golden it
// rewrites the file instead. Golden files are read with CRLF folded to LF so a
// Windows checkout with autocrlf compares equal.
func checkCostGolden(t *testing.T, golden, got string) {
	t.Helper()
	path := filepath.Join(costFixtureDir(t), golden)
	if *updateCostGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", golden, err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s (regenerate with -update-cost-golden): %v", golden, err)
	}
	if want := strings.ReplaceAll(string(raw), "\r\n", "\n"); got != want {
		t.Errorf("output differs from %s\n--- got ---\n%s--- want ---\n%s", golden, got, want)
	}
}

func TestCostGolden_InProcess(t *testing.T) {
	if *updateCostGolden {
		t.Skip("-update-cost-golden rewrites the goldens from the binary (TestCostGolden_Binary)")
	}
	for _, tc := range costGoldenCases {
		t.Run(tc.golden, func(t *testing.T) {
			checkCostGolden(t, tc.golden, renderCostInProcess(t, tc.fixture, tc.args))
		})
	}
}

func TestCostGolden_Binary(t *testing.T) {
	for _, tc := range costGoldenCases {
		t.Run(tc.golden, func(t *testing.T) {
			checkCostGolden(t, tc.golden, renderCostViaBinary(t, tc.fixture, tc.args))
		})
	}
}

// ---- what the ledger layout must say, whatever the formatting -----------------

type costTokensJSON struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
	Total         int64 `json:"total"`
}

type costRowJSON struct {
	Key            string          `json:"key"`
	Count          int64           `json:"count"`
	TotalInTokens  int64           `json:"total_in_tokens"`
	TotalOutTokens int64           `json:"total_out_tokens"`
	Tokens         *costTokensJSON `json:"tokens"`
	USD            *float64        `json:"usd"`
	APIEquivalent  *float64        `json:"api_equivalent_usd"`
}

func costRows(t *testing.T, render costRenderer, fixture string, args ...string) map[string]costRowJSON {
	t.Helper()
	out := render(t, fixture, append(append([]string{}, args...), "--json"))
	var doc struct {
		Events int64         `json:"events"`
		Rows   []costRowJSON `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decoding %q: %v", out, err)
	}
	rows := map[string]costRowJSON{}
	for _, r := range doc.Rows {
		rows[r.Key] = r
	}
	return rows
}

func forEachCostRenderer(t *testing.T, fn func(t *testing.T, render costRenderer)) {
	t.Helper()
	for name, render := range map[string]costRenderer{"in-process": renderCostInProcess, "binary": renderCostViaBinary} {
		t.Run(name, func(t *testing.T) { fn(t, render) })
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// Tokens first: every runtime shows real tokens; only the api-billed claude run
// (and the pre-K-136 Go row that logged a dollar figure) shows usd; codex and agy
// show no dollar figure; the claude subscription rows show their api-equivalent.
func TestCostLedger_TokensFirstDollarsOnlyForAPI(t *testing.T) {
	forEachCostRenderer(t, func(t *testing.T, render costRenderer) {
		rows := costRows(t, render, "ledger-mixed-log.ndjson", "--by", "runtime")

		claude := rows["claude"]
		if claude.Tokens == nil || claude.Tokens.Total != 82210 || claude.Tokens.Input != 7210 || claude.Tokens.Output != 1500 ||
			claude.Tokens.CacheRead != 71000 || claude.Tokens.CacheCreation != 2500 {
			t.Errorf("claude tokens = %+v, want 7210 in / 1500 out / 71000 cache read / 2500 cache creation = 82210", claude.Tokens)
		}
		// 0.5 (a Go row from before K-136) + 0.3012 (the api-billed run). Neither
		// subscription row adds to it.
		if claude.USD == nil || !near(*claude.USD, 0.8012) {
			t.Errorf("claude usd = %v, want 0.8012", claude.USD)
		}
		if claude.APIEquivalent == nil || !near(*claude.APIEquivalent, 0.6106) {
			t.Errorf("claude api_equivalent_usd = %v, want 0.6106 (0.4231 + 0.1875)", claude.APIEquivalent)
		}

		for _, rt := range []struct {
			key   string
			total int64
		}{{"codex", 52704}, {"agy", 12864}} {
			r := rows[rt.key]
			if r.Tokens == nil || r.Tokens.Total != rt.total {
				t.Errorf("%s tokens = %+v, want total %d", rt.key, r.Tokens, rt.total)
			}
			if r.USD != nil || r.APIEquivalent != nil {
				t.Errorf("%s must carry no dollar figure, got usd=%v api_equivalent_usd=%v", rt.key, r.USD, r.APIEquivalent)
			}
		}
	})
}

// The est_* estimates of a row stay in their own columns; the real token columns
// are the reported usage only.
func TestCostLedger_EstimatesNeverMixedIntoTokens(t *testing.T) {
	forEachCostRenderer(t, func(t *testing.T, render costRenderer) {
		agy := costRows(t, render, "ledger-mixed-log.ndjson", "--by", "runtime")["agy"]
		// Two agy runs estimate 30+25 in and 60+100 out; one reported 12863+1 and the
		// other reported nothing.
		if agy.TotalInTokens != 55 || agy.TotalOutTokens != 160 {
			t.Errorf("agy est columns = %d/%d, want 55/160", agy.TotalInTokens, agy.TotalOutTokens)
		}
		if agy.Tokens == nil || agy.Tokens.Total != 12864 {
			t.Errorf("agy tokens = %+v, want 12864 (the estimates must not add to it)", agy.Tokens)
		}
		// A legacy bash row with no usage object reports no real tokens either:
		// architect has two of them and one ledger run, whose est_* is 100/450.
		architect := costRows(t, render, "ledger-mixed-log.ndjson", "--by", "agent")["architect"]
		if architect.Tokens == nil || architect.Tokens.Total != 53540 {
			t.Errorf("architect tokens = %+v, want 53540 from the one ledger run alone", architect.Tokens)
		}
		if architect.TotalInTokens != 150 || architect.TotalOutTokens != 700 {
			t.Errorf("architect est columns = %d/%d, want 150/700", architect.TotalInTokens, architect.TotalOutTokens)
		}
	})
}

// A log of subscription runs only shows tokens: no usd column, no api_equiv
// column, no dollar figure anywhere in either output.
func TestCostLedger_SubscriptionOnlyLogShowsNoDollars(t *testing.T) {
	forEachCostRenderer(t, func(t *testing.T, render costRenderer) {
		table := render(t, "ledger-subscription-log.ndjson", []string{"--by", "runtime"})
		var header []string
		for _, line := range strings.Split(table, "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == "key" {
				header = f
			}
		}
		want := []string{"key", "count", "ok", "fail", "dur(s)", "est_in_tok", "est_out_tok", "in", "out", "cache", "tokens"}
		if strings.Join(header, " ") != strings.Join(want, " ") {
			t.Errorf("header = %v, want %v (tokens, no usd, no api_equiv)", header, want)
		}
		for _, banned := range []string{"usd", "api_equiv", "$", "not spend"} {
			if strings.Contains(table, banned) {
				t.Errorf("table must not mention %q:\n%s", banned, table)
			}
		}
		js := render(t, "ledger-subscription-log.ndjson", []string{"--by", "runtime", "--json"})
		for _, banned := range []string{`"usd"`, `"api_equivalent_usd"`} {
			if strings.Contains(js, banned) {
				t.Errorf("json must not carry %s:\n%s", banned, js)
			}
		}
		rows := costRows(t, render, "ledger-subscription-log.ndjson", "--by", "runtime")
		if rows["codex"].Tokens == nil || rows["codex"].Tokens.Total != 28750 || rows["agy"].Tokens == nil || rows["agy"].Tokens.Total != 12864 {
			t.Errorf("tokens = %+v / %+v, want codex 28750 and agy 12864", rows["codex"].Tokens, rows["agy"].Tokens)
		}
	})
}

// A log with ledger events ranks by real tokens, ties by key descending as before.
func TestCostLedger_RowsRankedByRealTokens(t *testing.T) {
	forEachCostRenderer(t, func(t *testing.T, render costRenderer) {
		out := render(t, "ledger-mixed-log.ndjson", []string{"--by", "agent", "--json"})
		var doc struct {
			Rows []struct {
				Key string `json:"key"`
			} `json:"rows"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, r := range doc.Rows {
			keys = append(keys, r.Key)
		}
		// 53540, 52704, 28670, 12864 real tokens. By the estimates it would be
		// frontend first (2012 in+out vs 850 for architect).
		if got := strings.Join(keys, ","); got != "architect,frontend,backend,general-agy" {
			t.Errorf("order = %s, want architect,frontend,backend,general-agy", got)
		}
	})
}

// The layout is decided by the aggregated events, after --since.
func TestCostLedger_SinceDecidesTheLayout(t *testing.T) {
	forEachCostRenderer(t, func(t *testing.T, render costRenderer) {
		legacyOnly := render(t, "ledger-mixed-log.ndjson", []string{"--by", "agent", "--since", "2026-03-01", "--json"})
		if strings.Contains(legacyOnly, `"tokens"`) {
			t.Errorf("only a pre-K-136 row is left after --since: the legacy JSON layout is expected:\n%s", legacyOnly)
		}
		all := render(t, "ledger-mixed-log.ndjson", []string{"--by", "agent", "--since", "2026-02-01", "--json"})
		if !strings.Contains(all, `"tokens"`) {
			t.Errorf("ledger events are left after --since 2026-02-01: the ledger JSON layout is expected:\n%s", all)
		}
	})
}
