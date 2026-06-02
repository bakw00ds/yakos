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
type Row struct {
	Key            string
	Count          int64
	OK             int64
	Fail           int64
	TotalDurationS float64
	TotalInTokens  int64
	TotalOutTokens int64
}

// Report is the output of Aggregate.
type Report struct {
	Events int64
	Rows   []Row
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
//
// limit <= 0 means no limit (all rows returned).
func Aggregate(ch <-chan Event, axis Axis, limit int) Report {
	type acc struct {
		count    int64
		ok       int64
		fail     int64
		durS     float64
		inTok    int64
		outTok   int64
	}

	keys := make([]string, 0, 32)
	byKey := make(map[string]*acc, 32)
	var n int64

	for ev := range ch {
		n++
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
		})
	}

	// Sort descending by (in_tokens + out_tokens) — mirrors bash's jq sort.
	// For ties, sort by key ascending for stable output.
	sortRows(rows)

	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	return Report{Events: n, Rows: rows}
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
