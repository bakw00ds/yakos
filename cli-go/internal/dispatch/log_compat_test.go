package dispatch

// log_compat_test.go: the dispatch log holds rows from two writers (the bash
// dispatcher and this package) and from before and after K-135, which began
// writing a usage object for one-shot Go runs. The readers must take them all
// together. The Go rows here are written by the real Run against fake runtimes;
// the legacy rows are what the bash dispatcher wrote. Everything happens in a
// temporary directory (isolatedLogDir); no operator state is touched.
//
// It pins what the readers do and the one fact they cannot know from the keys:
// for codex the two writers put different numbers under the same input_tokens
// key (see cost.Usage). K-136 did not change the bash writer; it made every
// reader total tokens as input + output + cache_read + cache_creation, on which
// the two conventions agree (asserted below), and moved a subscription run's
// dollar figure out of usage.total_cost_usd (see applyLedger).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/perfdash"
)

// legacyRow builds a bash-style dispatch_finished line. usage is a raw JSON
// object, or "" for none.
func legacyRow(agent, rt, usage string) string {
	row := `{"type":"dispatch_finished","ts":"2026-09-01T10:00:00Z","agent":"` + agent + `","runtime":"` + rt + `","project":"/p",` +
		`"exit_code":0,"duration_s":12.5,"output_bytes":2048,"task_bytes":400,"est_input_tokens":100,"est_output_tokens":512,` +
		`"model":"sonnet","model_chosen_by":"frontmatter","model_resolved":"sonnet","eval_run_id":null,"stderr_tail":null,"stderr_truncated":false`
	if usage != "" {
		row += `,"usage":` + usage
	}
	return row + "}"
}

func runKeepingTheLog(t *testing.T, runtimeName string) {
	t.Helper()
	if _, _, err := Run(context.Background(), Request{
		AgentName: "unpinned", Task: "t", Project: t.TempDir(), YakosRoot: pinRoot(t), Runtime: runtimeName,
	}); err != nil {
		t.Fatalf("Run(%s): %v", runtimeName, err)
	}
}

func TestDispatchLog_MixedLegacyAndNewRecordsAreReadTogether(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	// What bash wrote before K-135, plus noise any real log accumulates.
	legacy := []string{
		legacyRow("legacy-claude", "claude", `{"input_tokens":120,"output_tokens":45,"cache_read":9000,"cache_creation":3000,"duration_ms":4321,"total_cost_usd":0.0123}`),
		// bash codex: the raw input total with the cached part inside it, cache_read 0,
		// and a key the struct has no field for.
		legacyRow("legacy-codex", "codex", `{"input_tokens":15131,"output_tokens":5,"cache_read":0,"total_tokens":15136}`),
		legacyRow("legacy-agy", "agy", ""),
		`{"type":"dispatch_started","ts":"2026-09-01T09:59:59Z","agent":"legacy-agy","runtime":"agy","project":"/p"}`,
		`this is not json {`,
		``,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(legacy, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// What this package writes now, through the real Run: claude (usage with cache
	// counts and a cost), codex (tokens, fresh input plus cache_read), a plain-text
	// agy (no usage reported) and a stream-json agy.
	fakeRuntimeBin(t, "claude", "claude-stream-json-oneshot-SYNTHETIC.ndjson", "", 0)
	runKeepingTheLog(t, "claude")
	fakeRuntimeBin(t, "codex", "codex-exec-json-0.154.0-ok.ndjson", "", 0)
	runKeepingTheLog(t, "codex")
	fakeRuntimeBin(t, "agy", "", "just prose\n", 0)
	runKeepingTheLog(t, "agy")
	fakeRuntimeBin(t, "agy", "agy-stream-json-1.2.17-ok.ndjson", "", 0)
	runKeepingTheLog(t, "agy")

	// Read the whole log with the real readers.
	files, err := cost.LogFiles(logDir)
	if err != nil {
		t.Fatal(err)
	}
	var evs []cost.Event
	for ev := range cost.StreamFiles(files, "") {
		evs = append(evs, ev)
	}
	if len(evs) != 7 {
		t.Fatalf("read %d dispatch_finished rows, want 7 (3 legacy valid, 4 new; the garbage, the blank and the started line are skipped)", len(evs))
	}

	byAgent := map[string][]cost.Event{}
	for _, ev := range evs {
		byAgent[ev.Agent] = append(byAgent[ev.Agent], ev)
	}
	newRows := map[string]cost.Event{} // by runtime; agy has two, told apart below
	var newAgyPlain, newAgyStream cost.Event
	for _, ev := range byAgent["unpinned"] {
		switch {
		case ev.Runtime == "agy" && ev.Usage == nil:
			newAgyPlain = ev
		case ev.Runtime == "agy":
			newAgyStream = ev
		default:
			newRows[ev.Runtime] = ev
		}
	}

	// A legacy row without usage still reads, with no usage object.
	if got := byAgent["legacy-agy"]; len(got) != 1 || got[0].Usage != nil {
		t.Errorf("legacy agy row: %+v", got)
	}
	// So does a new row from a runtime that reported none.
	if newAgyPlain.Agent == "" || newAgyPlain.Usage != nil {
		t.Errorf("a plain-text run writes no usage object: %+v", newAgyPlain)
	}
	if newAgyStream.Usage == nil || newAgyStream.Usage.InputTokens != 12863 || newAgyStream.Usage.OutputTokens != 1 {
		t.Errorf("a stream-json agy run writes its usage: %+v", newAgyStream.Usage)
	}

	// claude: both writers agree on every token key. They differ on dollars only
	// by design: the bash row (no billing field) keeps the figure as spend, while
	// the Go row, written with no API key in the environment, is a subscription
	// row and holds it as api_equivalent_usd with usage.total_cost_usd 0.
	oldClaude, newClaude := byAgent["legacy-claude"][0].Usage, newRows["claude"].Usage
	if oldClaude == nil || newClaude == nil {
		t.Fatalf("claude rows lack usage: bash %+v, go %+v", oldClaude, newClaude)
	}
	if o, n := *oldClaude, *newClaude; o.TotalCostUSD != 0.0123 || n.TotalCostUSD != 0 {
		t.Errorf("claude spend figures: bash %v, go %v; want 0.0123 and 0", o.TotalCostUSD, n.TotalCostUSD)
	} else {
		o.TotalCostUSD, n.TotalCostUSD = 0, 0
		if o != n {
			t.Errorf("claude token keys disagree: bash %+v, go %+v", o, n)
		}
	}
	if ev := newRows["claude"]; ev.Billing != cost.BillingSubscription || ev.APIEquivalentUSD != 0.0123 || ev.SpendUSD() != 0 {
		t.Errorf("go claude row billing=%q api_equivalent=%v spend=%v; want a subscription row with a 0.0123 API-equivalent and no spend",
			ev.Billing, ev.APIEquivalentUSD, ev.SpendUSD())
	}
	if ev := byAgent["legacy-claude"][0]; ev.SpendUSD() != 0.0123 {
		t.Errorf("a legacy row keeps counting as spend: %v", ev.SpendUSD())
	}

	// codex: the SAME recorded run (15131 input tokens of which 7424 cached) reads
	// differently under the same key depending on the writer. The readers do not
	// normalize; the two agree only on the whole prompt.
	oldCodex, newCodex := byAgent["legacy-codex"][0].Usage, newRows["codex"].Usage
	if oldCodex == nil || newCodex == nil {
		t.Fatalf("codex rows lack usage: bash %+v, go %+v", oldCodex, newCodex)
	}
	if oldCodex.InputTokens != 15131 || oldCodex.CacheRead != 0 {
		t.Errorf("bash codex row read as %+v", oldCodex)
	}
	if newCodex.InputTokens != 7707 || newCodex.CacheRead != 7424 {
		t.Errorf("go codex row = %+v, want fresh 7707 and cache_read 7424", newCodex)
	}
	if oldCodex.InputTokens == newCodex.InputTokens {
		t.Error("the writers' input_tokens for codex differ by convention (bash keeps cached tokens inside it)")
	}
	if whole := newCodex.InputTokens + newCodex.CacheRead + newCodex.CacheCreation; whole != oldCodex.InputTokens {
		t.Errorf("whole prompt: go %d, bash %d", whole, oldCodex.InputTokens)
	}
	// What makes the split harmless to the readers that add all four counts (the
	// budget aggregate and the cost views; the metrics collector and `work close`
	// add input plus output only, and do see it): the two conventions agree on
	// that sum for the SAME run.
	if o, n := byAgent["legacy-codex"][0].Tokens(), newRows["codex"].Tokens(); o.Input+o.CacheRead+o.CacheCreation != n.Input+n.CacheRead+n.CacheCreation || o.Output != n.Output {
		t.Errorf("codex token totals disagree: bash %+v, go %+v", o, n)
	}

	// The aggregating readers take the mixed log without error. cost.Aggregate
	// rolls up by the est_* fields; perfdash prefers a usage cost when present.
	axis, err := cost.ParseAxis("runtime")
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan cost.Event, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	report := cost.Aggregate(ch, axis, 0)
	counts := map[string]int64{}
	for _, row := range report.Rows {
		counts[row.Key] = row.Count
	}
	if report.Events != 7 || counts["claude"] != 2 || counts["codex"] != 2 || counts["agy"] != 3 {
		t.Errorf("Aggregate: events=%d counts=%v", report.Events, counts)
	}
	summary := perfdash.ComputeSummary(evs, 5)
	if summary.TotalDispatches != 7 {
		t.Errorf("perfdash summary counts %d dispatches, want 7", summary.TotalDispatches)
	}
	// Dollars are spend only (K-136): the legacy claude row counts, the Go claude
	// row was a subscription run and does not, and nothing is estimated.
	if summary.TotalCostUSD != 0.0123 {
		t.Errorf("perfdash total cost %.4f, want the legacy claude row's 0.0123 and nothing else", summary.TotalCostUSD)
	}
}
