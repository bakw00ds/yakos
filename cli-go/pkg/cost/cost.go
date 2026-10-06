// Package cost is the public library API for yakOS dispatch cost aggregation.
//
// This package re-exports the stable types and functions from
// internal/cost, presenting a clean public surface for use by IDE
// extensions, CI integrations, and alternative CLIs. The internal package
// retains its implementation details and is not importable outside this module.
//
// # Overview
//
// Cost reads dispatch-log.ndjson (and rotated archives) and aggregates
// dispatch_finished events by a chosen axis (agent | runtime | day | project).
// The Event struct is the canonical dispatch-log schema; callers that need
// to read the log themselves should import this package rather than rolling
// their own parser.
//
// # Stability: experimental
//
// This package follows semantic versioning (same module: github.com/bakw00ds/yakos).
// It is pre-1.0; breaking changes are allowed at minor version bumps and
// documented in pkg/CHANGELOG.md. Target for stable v1.0.0: two minor
// releases after Phase 2 GA.
package cost

import (
	"io"

	internalcost "github.com/bakw00ds/yakos/internal/cost"
)

// Axis is the aggregation dimension for cost reports.
type Axis = internalcost.Axis

// Aggregation axes.
const (
	AxisRuntime = internalcost.AxisRuntime // group by runtime (claude/codex/gemini/agy)
	AxisAgent   = internalcost.AxisAgent   // group by agent name
	AxisDay     = internalcost.AxisDay     // group by calendar date (ISO-8601)
	AxisProject = internalcost.AxisProject // group by project path
)

// Usage holds the optional per-invocation token/cost data reported by the
// runtime adapter. Fields are zero when the runtime did not return structured
// telemetry. TotalCostUSD is whatever dollar figure the harness reported; it is
// spend only on an api-billed (or pre-K-136) event, so total dollars with
// Event.SpendUSD, never by summing this field.
type Usage = internalcost.Usage

// Event is a single line from dispatch-log.ndjson.
//
// Fields marked omitempty are optional in older log entries; callers must
// treat zero values as absent. The struct mirrors the JSON schema exactly.
//
// Tokens are the primary accounting unit: Event.Tokens returns the real counts
// the harness reported, and Event.SpendUSD returns the dollars that count as
// spend (api-billed runs only; 0 for subscription and local runs). These two
// methods are the only supported way to total tokens and dollars. The
// est_input_tokens and est_output_tokens fields are size estimates and are not
// token counts.
type Event = internalcost.Event

// TokenTotals is a sum of reported token counts, kept apart by kind.
type TokenTotals = internalcost.TokenTotals

// How a run was paid for (Event.Billing). An event with no billing field was
// written before K-136 and keeps counting its dollar figure as spend.
const (
	BillingSubscription = internalcost.BillingSubscription // Claude Code, Codex ChatGPT login, Antigravity: no per-call charge
	BillingAPI          = internalcost.BillingAPI          // paid per API call: dollars are spend
	BillingLocal        = internalcost.BillingLocal        // a local model: no charge
)

// CountsAsSpend reports whether dollars on an event with this billing value are
// real spend.
func CountsAsSpend(billing string) bool {
	return internalcost.CountsAsSpend(billing)
}

// Row is one aggregated row in a Report.
//
// TotalInTokens and TotalOutTokens are the est_* size estimates. Tokens,
// SpendUSD and APIEquivUSD total the real token counts, the api-billed dollars
// and the informational api-equivalent dollars (what subscription runs would
// have cost at API rates; never spend). The three are not part of a Row's JSON
// encoding.
type Row = internalcost.Row

// Report is the output of Aggregate and AggregateLedger. Ledger is true when at
// least one aggregated event carries a billing field; it is not part of a
// Report's JSON encoding.
type Report = internalcost.Report

// ParseAxis converts a string flag value to an Axis.
//
// Accepted values: "runtime", "agent", "day", "project".
//
// Errors:
//   - returns error on unknown values (mirrors bash's validation)
//
// Example:
//
//	axis, err := cost.ParseAxis("agent")
//	if err != nil {
//	    log.Fatal(err)
//	}
func ParseAxis(s string) (Axis, error) {
	return internalcost.ParseAxis(s)
}

// LogFiles returns the sorted list of dispatch-log NDJSON files in dir.
//
// It matches dispatch-log*.ndjson (current + rotated archives). Returns
// an empty slice (and no error) when no files match.
//
// Errors:
//   - returns error on filepath.Glob failure
//
// Example:
//
//	files, err := cost.LogFiles("/home/user/agent-control/myproject/work/current")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("found %d log file(s)\n", len(files))
func LogFiles(dir string) ([]string, error) {
	return internalcost.LogFiles(dir)
}

// StreamFinished reads Event records from r, streaming line-by-line.
//
// Only events with type=="dispatch_finished" are emitted. Lines that fail
// JSON parsing are silently skipped (matching bash's jq behavior on
// malformed input). The returned channel is closed when r is exhausted.
//
// since is an ISO-8601 timestamp filter; events with ts < since are skipped.
// Pass "" to include all events.
//
// Errors: none returned; scan failures are silently dropped.
func StreamFinished(r io.Reader, since string) <-chan Event {
	return internalcost.StreamFinished(r, since)
}

// StreamFiles opens each file in paths and calls StreamFinished, yielding
// all matching events across all files. Files are processed in the order
// given; events from each file are yielded in order.
//
// Unreadable files are silently skipped.
func StreamFiles(paths []string, since string) <-chan Event {
	return internalcost.StreamFiles(paths, since)
}

// Aggregate streams events from ch and rolls them up by axis.
//
// The returned Report has Rows sorted descending by total tokens, matching
// bash's sort_by(.total_in_tokens + .total_out_tokens) | reverse. These are the
// est_* size estimates, and the order never depends on what the log holds.
//
// limit <= 0 means no limit (all rows returned).
//
// Example:
//
//	dir := filepath.Join(os.Getenv("HOME"), "agent-control/myproject/work/current")
//	files, _ := cost.LogFiles(dir)
//	ch := cost.StreamFiles(files, "")
//	report := cost.Aggregate(ch, cost.AxisAgent, 10)
//	for _, row := range report.Rows {
//	    fmt.Printf("%s: %d runs\n", row.Key, row.Count)
//	}
func Aggregate(ch <-chan Event, axis Axis, limit int) Report {
	return internalcost.Aggregate(ch, axis, limit)
}

// AggregateLedger is Aggregate for logs written by the K-136 dispatcher: when at
// least one aggregated event is a ledger event (it carries a billing field), the
// rows are ranked by real tokens (Row.Tokens, descending) instead of the est_*
// estimates, and limit keeps the top rows by that rank. Without a ledger event it
// returns exactly what Aggregate returns.
//
// Example:
//
//	report := cost.AggregateLedger(cost.StreamFiles(files, ""), cost.AxisRuntime, 0)
//	for _, row := range report.Rows {
//	    fmt.Printf("%s: %d tokens, $%.2f spent\n", row.Key, row.Tokens.Total(), row.SpendUSD)
//	}
func AggregateLedger(ch <-chan Event, axis Axis, limit int) Report {
	return internalcost.AggregateLedger(ch, axis, limit)
}
