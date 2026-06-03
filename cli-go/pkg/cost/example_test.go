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
