// Package cost implements the Go port of `yakos cost`.
//
// It reads dispatch-log.ndjson (and rotated archives) and aggregates
// dispatch_finished events by axis (agent | runtime | day | project).
//
// The Event struct is the canonical dispatch-log schema; future ports
// (model-routing, supervise) should import this package for log reading
// rather than rolling their own.
package cost

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Usage holds the optional per-invocation token/cost data reported by the
// runtime adapter (available on events where the runtime returned structured
// telemetry).
//
// Token convention (K-135). InputTokens counts only FRESH prompt tokens, those
// not served from a cache; CacheRead and CacheCreation count the cached
// remainder, so the whole prompt is InputTokens + CacheRead + CacheCreation.
// OutputTokens includes reasoning tokens. TotalCostUSD is whatever the harness
// itself reported, which only claude does; no price is computed anywhere. This
// is the Anthropic convention, and the Go dispatcher normalizes every harness to
// it (runtime.Usage is an alias of this type). codex counts its cached tokens
// INSIDE its input total natively, and the Go parser subtracts them.
//
// The convention is not uniform across the log. For claude the bash and Go
// writers agree. For codex they do not: rows the bash dispatcher wrote copied
// codex's own fields, so input_tokens holds the whole prompt, cached tokens
// included, and cache_read is 0; rows the Go dispatcher writes hold the fresh
// remainder in input_tokens and the cached part in cache_read. The two
// conventions agree on the sum, so every reader that totals tokens (K-136:
// TokenTotals.Total, the budget aggregate, the cost and dashboard views) adds
// all four counts and is unaffected; only a reader that reports the input/cache
// split of a legacy bash codex row sees a different split. The bash writer is
// not changed (docs/runtime-matrix.md, "Usage fields by harness").
//
// Dollars (K-136). TotalCostUSD is the dollar figure the harness reported. It is
// spend only on a row whose billing is "api" (or on a row that predates the
// billing field); under a subscription it is stored as the event's
// api_equivalent_usd instead and TotalCostUSD is 0. Readers must use
// Event.SpendUSD, never sum this field directly.
type Usage struct {
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheRead     int64   `json:"cache_read"`
	CacheCreation int64   `json:"cache_creation"`
	DurationMs    int64   `json:"duration_ms"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
}

// Event is a single line from dispatch-log.ndjson.
//
// Fields marked omitempty are optional in older log entries; callers must
// treat zero values as absent.  The struct mirrors the JSON schema exactly;
// do not add Go-only fields here.
type Event struct {
	Type    string `json:"type"`
	Ts      string `json:"ts"`
	Agent   string `json:"agent"`
	Runtime string `json:"runtime"`
	Project string `json:"project"`

	// Fields present only on dispatch_finished events.
	ExitCode        int     `json:"exit_code"`
	DurationS       float64 `json:"duration_s"`
	OutputBytes     int64   `json:"output_bytes"`
	TaskBytes       int64   `json:"task_bytes"`
	EstInputTokens  int64   `json:"est_input_tokens"`
	EstOutputTokens int64   `json:"est_output_tokens"`

	// Usage is present when the runtime returned structured token telemetry.
	Usage *Usage `json:"usage,omitempty"`

	// Model is the concrete model tier the dispatch ran on (same value as
	// ModelResolved). Present on dispatch_started AND dispatch_finished so a
	// run, eval or not, can be attributed to a tier (K-110).
	Model string `json:"model,omitempty"`

	// Routing metadata (newer events).
	ModelResolved string `json:"model_resolved,omitempty"`
	ModelChosenBy string `json:"model_chosen_by,omitempty"`
	EvalRunID     string `json:"eval_run_id,omitempty"`

	// TaskPreview is present on dispatch_started events only.
	TaskPreview string `json:"task_preview,omitempty"`

	// Phase 2 identity fields — additive-optional; absent on legacy/bash-written
	// lines. Readers must tolerate their absence (zero value = unset).
	OperatorID     string `json:"operator_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`

	// K-136 ledger fields on dispatch_finished, written only by the Go
	// dispatcher's dispatch.Account. All are additive-optional: absent on
	// bash-written lines and on Go lines from before K-136, where a reader sees
	// the zero value. Dispatcher code must tolerate their absence; it must also
	// ignore unknown keys, as the bash readers do.
	//
	// Provider is the vendor behind the runtime (anthropic, openai, google).
	// ModelID is the concrete model id the harness reported, else the resolved
	// model name.
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	// Billing is how the run was paid for: subscription, api or local. Tokens are
	// recorded for every run; dollars count as spend only when it is api (see
	// CountsAsSpend and Event.SpendUSD).
	Billing string `json:"billing,omitempty"`
	// CostSource says where a dollar figure came from: "harness" when the
	// harness reported it (claude's total_cost_usd). Empty when the event has no
	// dollar figure.
	CostSource string `json:"cost_source,omitempty"`
	// APIEquivalentUSD is what a non-api run would have cost at API rates, as
	// the harness reported it. It is informational and is never spend.
	APIEquivalentUSD float64 `json:"api_equivalent_usd,omitempty"`
	// Routing fields. Empty until the router lands (plan phase P1).
	RouteRule    string `json:"route_rule,omitempty"`
	RouteReason  string `json:"route_reason,omitempty"`
	FallbackFrom string `json:"fallback_from,omitempty"`
	RouteClass   string `json:"route_class,omitempty"`
	PolicySHA    string `json:"policy_sha,omitempty"`
	// Surface is the entry point that made the dispatch: cli, console-chat,
	// mcp, jsonrpc, rest, grpc, flows.
	Surface string `json:"surface,omitempty"`
	// NativeSessionID is the harness's own session id (claude session_id, codex
	// thread_id, agy conversation_id), usable to resume the conversation.
	NativeSessionID string `json:"native_session_id,omitempty"`
}

// Billing values of Event.Billing.
const (
	BillingSubscription = "subscription"
	BillingAPI          = "api"
	BillingLocal        = "local"
)

// CountsAsSpend reports whether dollars on an event with this billing value are
// real spend. Tokens are the primary unit (K-136): dollars count only for runs
// paid per API call, never for a subscription harness or a local model. An
// event with no billing field was written before K-136, when the dollar figure
// was the only cost there was; it keeps counting, so a budget does not reset
// itself on upgrade. A billing value this version does not know counts too,
// the safe direction for a cost guard.
func CountsAsSpend(billing string) bool {
	switch billing {
	case BillingSubscription, BillingLocal:
		return false
	}
	return true
}

// SpendUSD is the dollars of this event that count as spend: the usage cost on
// an api-billed or pre-K-136 event, 0 otherwise or when the figure is not a
// finite non-negative number.
func (e Event) SpendUSD() float64 {
	if e.Usage == nil || !CountsAsSpend(e.Billing) {
		return 0
	}
	c := e.Usage.TotalCostUSD
	if math.IsNaN(c) || c < 0 || c > 1e12 { // NaN, negative or absurd
		return 0
	}
	return c
}

// TokenTotals is a sum of reported token counts, kept apart by kind.
type TokenTotals struct {
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	CacheRead     int64 `json:"cache_read"`
	CacheCreation int64 `json:"cache_creation"`
}

// Total is every token: fresh input, output, and both cache kinds.
func (t TokenTotals) Total() int64 { return t.Input + t.Output + t.CacheRead + t.CacheCreation }

// Add returns t plus o.
func (t TokenTotals) Add(o TokenTotals) TokenTotals {
	return TokenTotals{
		Input:         t.Input + o.Input,
		Output:        t.Output + o.Output,
		CacheRead:     t.CacheRead + o.CacheRead,
		CacheCreation: t.CacheCreation + o.CacheCreation,
	}
}

// IsZero reports whether no tokens were reported.
func (t TokenTotals) IsZero() bool { return t == TokenTotals{} }

// Tokens returns the event's reported token counts. An event with no usage
// object reports none: a size estimate (est_input_tokens) is not a count and is
// never mixed in. Negative or absurd counts, which a corrupt line could carry,
// are treated as 0 so one bad row cannot move a total.
func (e Event) Tokens() TokenTotals {
	if e.Usage == nil {
		return TokenTotals{}
	}
	return TokenTotals{
		Input:         sane(e.Usage.InputTokens),
		Output:        sane(e.Usage.OutputTokens),
		CacheRead:     sane(e.Usage.CacheRead),
		CacheCreation: sane(e.Usage.CacheCreation),
	}
}

// maxSaneTokens bounds one count of one event; larger is a corrupt line.
const maxSaneTokens = int64(1) << 40

func sane(n int64) int64 {
	if n < 0 || n > maxSaneTokens {
		return 0
	}
	return n
}

// LogFiles returns the sorted list of dispatch-log NDJSON files in dir.
// It matches dispatch-log*.ndjson (current + rotated archives).
// Returns an empty slice (and no error) when no files match.
func LogFiles(dir string) ([]string, error) {
	pattern := filepath.Join(dir, "dispatch-log*.ndjson")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("cost: globbing log files: %w", err)
	}
	sort.Strings(matches) // stable, deterministic order
	return matches, nil
}

// StreamFinished reads Event records from r, streaming line-by-line without
// loading the full file into memory.  Only events with type=="dispatch_finished"
// are emitted.  Lines that fail JSON parsing are silently skipped (matching
// bash's jq behavior when given malformed input).
func StreamFinished(r io.Reader, since string) <-chan Event {
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		scanner := bufio.NewScanner(r)
		// Allow lines up to 1 MiB (a dispatch log line is typically < 1 KiB,
		// but generous headroom avoids surprises).
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var ev Event
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				continue // skip malformed lines
			}
			if ev.Type != "dispatch_finished" {
				continue
			}
			if since != "" && ev.Ts < since {
				continue
			}
			ch <- ev
		}
	}()
	return ch
}

// StreamFiles opens each file in paths and calls StreamFinished, yielding all
// matching events across all files.  Events from each file are yielded in
// order; files are processed in the order given.
func StreamFiles(paths []string, since string) <-chan Event {
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		for _, p := range paths {
			f, err := os.Open(p) //nolint:gosec
			if err != nil {
				continue // skip unreadable files
			}
			for ev := range StreamFinished(f, since) {
				ch <- ev
			}
			_ = f.Close()
		}
	}()
	return ch
}
