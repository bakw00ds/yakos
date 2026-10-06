package cost_test

import (
	"fmt"
	"strings"

	"github.com/bakw00ds/yakos/pkg/cost"
)

func ExampleAggregate() {
	// Simulate a dispatch-log.ndjson reader with two finished events.
	src := strings.NewReader(`{"type":"dispatch_finished","ts":"2026-06-01T10:00:00Z","agent":"backend","runtime":"claude","project":"/home/user/myproject","exit_code":0,"duration_s":12.5,"est_input_tokens":4000,"est_output_tokens":1200}
{"type":"dispatch_finished","ts":"2026-06-01T11:00:00Z","agent":"security","runtime":"claude","project":"/home/user/myproject","exit_code":0,"duration_s":8.1,"est_input_tokens":2000,"est_output_tokens":800}
{"type":"dispatch_started","ts":"2026-06-01T10:00:01Z","agent":"backend","runtime":"claude"}
`)
	ch := cost.StreamFinished(src, "")
	report := cost.Aggregate(ch, cost.AxisAgent, 0)
	fmt.Printf("events=%d rows=%d\n", report.Events, len(report.Rows))
	// Output: events=2 rows=2
}

func ExampleParseAxis() {
	axis, err := cost.ParseAxis("day")
	if err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}
	fmt.Println(axis == cost.AxisDay)
	// Output: true
}

func ExampleStreamFinished() {
	src := strings.NewReader(`{"type":"dispatch_finished","ts":"2026-06-01T10:00:00Z","agent":"backend","runtime":"claude","exit_code":0,"duration_s":5.0,"est_input_tokens":1000,"est_output_tokens":400}
{"type":"dispatch_started","ts":"2026-06-01T10:00:01Z","agent":"backend","runtime":"claude"}
`)
	count := 0
	for range cost.StreamFinished(src, "") {
		count++
	}
	fmt.Println(count)
	// Output: 1
}

func ExampleAggregateLedger() {
	// A claude run on a subscription and a codex run, both accounted by the
	// dispatcher (they carry a billing field), plus an api-billed claude run.
	// Tokens are always reported; dollars count only for the api-billed run.
	src := strings.NewReader(`{"type":"dispatch_finished","ts":"2026-06-01T10:00:00Z","agent":"backend","runtime":"claude","exit_code":0,"duration_s":12,"billing":"subscription","api_equivalent_usd":0.42,"usage":{"input_tokens":1200,"output_tokens":340,"cache_read":50000,"cache_creation":2000,"total_cost_usd":0}}
{"type":"dispatch_finished","ts":"2026-06-01T11:00:00Z","agent":"backend","runtime":"codex","exit_code":0,"duration_s":8,"billing":"subscription","usage":{"input_tokens":7707,"output_tokens":421,"cache_read":7424,"cache_creation":0,"total_cost_usd":0}}
{"type":"dispatch_finished","ts":"2026-06-01T12:00:00Z","agent":"api-run","runtime":"claude","exit_code":0,"duration_s":5,"billing":"api","usage":{"input_tokens":5000,"output_tokens":900,"cache_read":0,"cache_creation":0,"total_cost_usd":0.3012}}
`)
	report := cost.AggregateLedger(cost.StreamFinished(src, ""), cost.AxisRuntime, 0)
	fmt.Println("ledger:", report.Ledger)
	for _, row := range report.Rows {
		fmt.Printf("%s: %d tokens, $%.4f spent, $%.4f api-equivalent\n", row.Key, row.Tokens.Total(), row.SpendUSD, row.APIEquivUSD)
	}
	// Output:
	// ledger: true
	// claude: 59440 tokens, $0.3012 spent, $0.4200 api-equivalent
	// codex: 15552 tokens, $0.0000 spent, $0.0000 api-equivalent
}
