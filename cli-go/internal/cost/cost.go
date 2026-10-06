package cost

import (
	"fmt"
	"sort"
	"strings"
)

// Axis is the aggregation dimension for cost reports.
type Axis int

const (
	AxisRuntime Axis = iota // default
	AxisAgent
	AxisDay
	AxisProject
)

// ParseAxis converts a string flag value to an Axis.
// Returns an error on unknown values (mirrors bash's validation).
func ParseAxis(s string) (Axis, error) {
	switch s {
	case "runtime":
		return AxisRuntime, nil
	case "agent":
		return AxisAgent, nil
	case "day":
		return AxisDay, nil
	case "project":
		return AxisProject, nil
	default:
		return 0, fmt.Errorf("cost: --by must be agent | runtime | day | project, got %q", s)
	}
}

// Row is one aggregated row in the report.
//
// TotalInTokens and TotalOutTokens are the est_* SIZE ESTIMATES (chars/4 of the
// prompt and output bytes), the only token figures the bash writer ever
// produced. They are not counts and are never mixed into Tokens.
//
// Tokens, SpendUSD and APIEquivUSD (K-136) total the real usage counts and the
// dollars of the row's events, and are what the ledger layout of `yakos cost`
// prints. They carry json:"-" so every other surface that marshals a Row
// directly (REST, JSON-RPC, MCP) stays byte-identical; `yakos cost --json`
// builds its own shape from them (see PrintJSON).
type Row struct {
	Key            string
	Count          int64
	OK             int64
	Fail           int64
	TotalDurationS float64
	TotalInTokens  int64
	TotalOutTokens int64

	// Tokens sums Event.Tokens: the real counts the harness reported. An event
	// with no usage object adds none.
	Tokens TokenTotals `json:"-"`
	// SpendUSD sums Event.SpendUSD: dollars of api-billed (and pre-K-136)
	// events only, rounded to a micro-dollar.
	SpendUSD float64 `json:"-"`
	// APIEquivUSD sums APIEquivalentUSD: what the subscription runs would have
	// cost at API rates. It is informational, never spend, and never part of
	// SpendUSD.
	APIEquivUSD float64 `json:"-"`
}

// Report is the output of Aggregate and AggregateLedger.
type Report struct {
	Events int64
	Rows   []Row

	// Ledger is true when at least one aggregated event is a ledger event (it
	// carries a billing field, see IsLedger). PrintTable and PrintJSON switch to
	// the ledger layout, which adds real token columns and dollars, only when it
	// is set; a report without a ledger event prints exactly as it did before
	// K-136.
	Ledger bool `json:"-"`
}

// axisKey derives the grouping key for ev under axis.
func axisKey(ev Event, axis Axis) string {
	switch axis {
	case AxisAgent:
		return ev.Agent
	case AxisRuntime:
		return ev.Runtime
	case AxisDay:
		// ts is an ISO-8601 string; split at "T" to get the date part.
		parts := strings.SplitN(ev.Ts, "T", 2)
		if len(parts) == 2 {
			return parts[0]
		}
		return ev.Ts
	case AxisProject:
		if ev.Project == "" {
			return "(unknown)"
		}
		return ev.Project
	}
	return ev.Runtime
}

// Aggregate streams events from ch and rolls them up by axis.
// The returned Report has Rows sorted descending by total tokens
// (matching bash's sort_by(.total_in_tokens + .total_out_tokens) | reverse).
// That is the est_* size estimate, whatever the events carry: every surface
// that marshals the rows directly (REST, JSON-RPC, MCP, gRPC) relies on this
// order, so it never changes with the log's contents. Rows also carry the real
// token and dollar totals (Row.Tokens, Row.SpendUSD, Row.APIEquivUSD) and the
// Report says whether a ledger event was seen (Report.Ledger).
//
// limit <= 0 means no limit (all rows returned).
func Aggregate(ch <-chan Event, axis Axis, limit int) Report {
	return aggregate(ch, axis, limit, false)
}

// AggregateLedger is Aggregate for the K-136 report (`yakos cost`): the same
// rows and totals, but when at least one aggregated event is a ledger event the
// rows are ranked by real tokens (Row.Tokens.Total, descending, ties by key
// descending as in Aggregate) instead of the est_* size estimates, and limit
// keeps the top rows by that rank. With no ledger event it is exactly Aggregate.
func AggregateLedger(ch <-chan Event, axis Axis, limit int) Report {
	return aggregate(ch, axis, limit, true)
}

// aggregate is the shared body. rankByTokens lets a ledger log be ranked by real
// tokens; it has no effect on a log without ledger events.
func aggregate(ch <-chan Event, axis Axis, limit int, rankByTokens bool) Report {
	type acc struct {
		count   int64
		ok      int64
		fail    int64
		durS    float64
		inTok   int64
		outTok  int64
		tokens  TokenTotals
		spend   float64
		apiEquv float64
	}

	keys := make([]string, 0, 32)
	byKey := make(map[string]*acc, 32)
	var n int64
	ledger := false

	for ev := range ch {
		n++
		if IsLedger(ev) {
			ledger = true
		}
		k := axisKey(ev, axis)
		a, exists := byKey[k]
		if !exists {
			a = &acc{}
			byKey[k] = a
			keys = append(keys, k)
		}
		a.count++
		if ev.ExitCode == 0 {
			a.ok++
		} else {
			a.fail++
		}
		a.durS += ev.DurationS
		a.inTok += ev.EstInputTokens
		a.outTok += ev.EstOutputTokens
		// K-136: real counts and dollars, apart from the estimates above.
		a.tokens = a.tokens.Add(ev.Tokens())
		a.spend += ev.SpendUSD()
		a.apiEquv += APIEquivalentUSD(ev)
	}

	rows := make([]Row, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		rows = append(rows, Row{
			Key:            k,
			Count:          a.count,
			OK:             a.ok,
			Fail:           a.fail,
			TotalDurationS: a.durS,
			TotalInTokens:  a.inTok,
			TotalOutTokens: a.outTok,
			Tokens:         a.tokens,
			SpendUSD:       roundUSD(a.spend),
			APIEquivUSD:    roundUSD(a.apiEquv),
		})
	}

	// Sort descending by (in_tokens + out_tokens) — mirrors bash's jq sort. A
	// ledger report that asked for it ranks by real tokens instead.
	// For ties, sort by key descending for stable output.
	if rankByTokens && ledger {
		sortRowsByTokens(rows)
	} else {
		sortRows(rows)
	}

	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	return Report{Events: n, Rows: rows, Ledger: ledger}
}

// sortRows sorts rows descending by (TotalInTokens + TotalOutTokens).
//
// Tie-breaking matches jq's behaviour in bash cost.sh:
//
//	group_by(<key>) produces groups in ascending alphabetical order.
//	sort_by(total_tokens) is stable — ties keep the ascending key order.
//	reverse flips everything, so ties appear in descending key order.
//
// Net: primary descending token sum; tie-break descending key (Z before A).
// Keys are unique per group, so the comparator is a total order and
// sort.Slice produces deterministic output regardless of input order.
func sortRows(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		iSum := rows[i].TotalInTokens + rows[i].TotalOutTokens
		jSum := rows[j].TotalInTokens + rows[j].TotalOutTokens
		if iSum != jSum {
			return iSum > jSum // descending token sum
		}
		return rows[i].Key > rows[j].Key // descending key for ties
	})
}

// sortRowsByTokens sorts rows descending by real tokens (Tokens.Total), ties by
// key descending, the same tie-break sortRows uses. It ranks the ledger layout.
func sortRowsByTokens(rows []Row) {
	sort.Slice(rows, func(i, j int) bool {
		iSum, jSum := rows[i].Tokens.Total(), rows[j].Tokens.Total()
		if iSum != jSum {
			return iSum > jSum
		}
		return rows[i].Key > rows[j].Key
	})
}
