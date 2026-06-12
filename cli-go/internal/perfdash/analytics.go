package perfdash

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
)

// ---- window parsing ---------------------------------------------------------

// parseWindow converts a window string ("1h", "24h", "7d", "30d") to a
// time.Duration.  Returns 24 hours for unrecognised values.
func parseWindow(s string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1h":
		return time.Hour
	case "6h":
		return 6 * time.Hour
	case "12h":
		return 12 * time.Hour
	case "48h":
		return 48 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	case "24h", "":
		return 24 * time.Hour
	}
	// Parse numeric prefix + unit suffix.
	for i, c := range s {
		if c == 'h' || c == 'd' {
			numPart := s[:i]
			var n int
			if _, err := parseInt(numPart, &n); err == nil {
				if c == 'h' {
					return time.Duration(n) * time.Hour
				}
				return time.Duration(n) * 24 * time.Hour
			}
			break
		}
	}
	return 24 * time.Hour
}

// parseInt is a minimal strconv.Atoi-equivalent to avoid importing strconv
// in pure-domain analytics (no I/O).
func parseInt(s string, out *int) (int, error) {
	v := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadInt
		}
		v = v*10 + int(c-'0')
	}
	*out = v
	return v, nil
}

var errBadInt = errSimple("bad int")

type errSimple string

func (e errSimple) Error() string { return string(e) }

// sinceISO returns an ISO-8601 UTC timestamp string for now minus window.
func sinceISO(window time.Duration) string {
	return time.Now().UTC().Add(-window).Format(time.RFC3339)
}

// ---- data types -------------------------------------------------------------

// DispatchRow represents a single dispatch_finished event for the recent list.
type DispatchRow struct {
	Ts        string  `json:"ts"`
	Agent     string  `json:"agent"`
	Runtime   string  `json:"runtime"`
	Project   string  `json:"project"`
	ExitCode  int     `json:"exit_code"`
	DurationS float64 `json:"duration_s"`
	CostUSD   float64 `json:"cost_usd"`
	LatencyMs int64   `json:"latency_ms"`
}

// SummaryResponse is the response shape for GET /api/perf/summary.
type SummaryResponse struct {
	TotalDispatches int64         `json:"total_dispatches"`
	TotalCostUSD    float64       `json:"total_cost_usd"`
	AvgLatencyMs    int64         `json:"avg_latency_ms"`
	P50LatencyMs    int64         `json:"p50_latency_ms"`
	P95LatencyMs    int64         `json:"p95_latency_ms"`
	TopAgents       []TopAxisItem `json:"top_agents"`
	TopRuntimes     []TopAxisItem `json:"top_runtimes"`
}

// TopAxisItem is an entry in top-N lists.
type TopAxisItem struct {
	Key        string  `json:"key"`
	Dispatches int64   `json:"dispatches"`
	CostUSD    float64 `json:"cost_usd"`
}

// TimeseriesPoint is one bucket in a timeseries response.
type TimeseriesPoint struct {
	Ts    string  `json:"ts"` // bucket start, ISO-8601
	Value float64 `json:"value"`
}

// AxisRow is one entry in a by-axis breakdown.
type AxisRow struct {
	Key          string  `json:"key"`
	Dispatches   int64   `json:"dispatches"`
	CostUSD      float64 `json:"cost_usd"`
	AvgLatencyMs int64   `json:"avg_latency_ms"`
	P95LatencyMs int64   `json:"p95_latency_ms"`
}

// ---- event collection -------------------------------------------------------

// collectEvents drains ch into a slice.
func collectEvents(ch <-chan cost.Event) []cost.Event {
	var evs []cost.Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

// eventCostUSD returns the cost in USD for a single event.
// Prefers the structured Usage.TotalCostUSD field; falls back to estimating
// from token counts at the Sonnet-class rate ($3/M input, $15/M output).
func eventCostUSD(ev cost.Event) float64 {
	if ev.Usage != nil && ev.Usage.TotalCostUSD > 0 {
		return ev.Usage.TotalCostUSD
	}
	// Rough estimate from estimated token counts.
	inputCost := float64(ev.EstInputTokens) / 1_000_000 * 3.0
	outputCost := float64(ev.EstOutputTokens) / 1_000_000 * 15.0
	return inputCost + outputCost
}

// eventLatencyMs returns the dispatch latency in milliseconds.
// Uses Usage.DurationMs if present, otherwise converts DurationS.
func eventLatencyMs(ev cost.Event) int64 {
	if ev.Usage != nil && ev.Usage.DurationMs > 0 {
		return ev.Usage.DurationMs
	}
	return int64(math.Round(ev.DurationS * 1000))
}

// ---- Summary ----------------------------------------------------------------

// ComputeSummary aggregates events into a SummaryResponse.
func ComputeSummary(events []cost.Event, topN int) SummaryResponse {
	if topN <= 0 {
		topN = 5
	}

	var totalCost float64
	latencies := make([]int64, 0, len(events))
	agentCounts := make(map[string]int64)
	agentCost := make(map[string]float64)
	runtimeCounts := make(map[string]int64)
	runtimeCost := make(map[string]float64)

	for _, ev := range events {
		c := eventCostUSD(ev)
		l := eventLatencyMs(ev)
		totalCost += c
		latencies = append(latencies, l)

		agentCounts[ev.Agent]++
		agentCost[ev.Agent] += c
		runtimeCounts[ev.Runtime]++
		runtimeCost[ev.Runtime] += c
	}

	n := int64(len(events))
	var avgLatency, p50, p95 int64
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		var sum int64
		for _, l := range latencies {
			sum += l
		}
		avgLatency = sum / int64(len(latencies))
		p50 = percentile(latencies, 50)
		p95 = percentile(latencies, 95)
	}

	return SummaryResponse{
		TotalDispatches: n,
		TotalCostUSD:    math.Round(totalCost*10000) / 10000,
		AvgLatencyMs:    avgLatency,
		P50LatencyMs:    p50,
		P95LatencyMs:    p95,
		TopAgents:       topAxisItems(agentCounts, agentCost, topN),
		TopRuntimes:     topAxisItems(runtimeCounts, runtimeCost, 3),
	}
}

// topAxisItems builds a top-N list from counts and costs maps, sorted
// descending by dispatch count then ascending by key for ties.
func topAxisItems(counts map[string]int64, costs map[string]float64, n int) []TopAxisItem {
	items := make([]TopAxisItem, 0, len(counts))
	for k, c := range counts {
		items = append(items, TopAxisItem{
			Key:        k,
			Dispatches: c,
			CostUSD:    math.Round(costs[k]*10000) / 10000,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dispatches != items[j].Dispatches {
			return items[i].Dispatches > items[j].Dispatches
		}
		return items[i].Key < items[j].Key
	})
	if n > 0 && len(items) > n {
		items = items[:n]
	}
	return items
}

// percentile returns the p-th percentile (0..100) of a sorted slice.
// Panics if slice is empty (caller must guard).
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(float64(p)/100.0*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ---- Timeseries -------------------------------------------------------------

// bucketDuration converts a bucket string ("hour", "day", "6h") to a duration.
func bucketDuration(s string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hour", "1h":
		return time.Hour
	case "day", "24h":
		return 24 * time.Hour
	case "6h":
		return 6 * time.Hour
	case "12h":
		return 12 * time.Hour
	default:
		return time.Hour
	}
}

// ComputeTimeseries aggregates events into a time-bucketed series.
// metric is one of "cost", "latency", "dispatches".
// Always returns the full sequence of bucket start times for the window
// even when there are no events (all values will be 0).
func ComputeTimeseries(events []cost.Event, window, bucket time.Duration, metric string) []TimeseriesPoint {
	now := time.Now().UTC()
	start := now.Add(-window).Truncate(bucket)

	if len(events) == 0 {
		// Return the full bucket grid with zero values.
		var points []TimeseriesPoint
		for t := start; !t.After(now); t = t.Add(bucket) {
			points = append(points, TimeseriesPoint{Ts: t.Format(time.RFC3339), Value: 0})
		}
		if points == nil {
			points = []TimeseriesPoint{}
		}
		return points
	}

	// Build bucket map: bucket-start → accumulated value.
	buckets := make(map[int64]float64)    // unix seconds of bucket start → value
	bucketCounts := make(map[int64]int64) // for latency averaging

	for _, ev := range events {
		ts, err := time.Parse(time.RFC3339, ev.Ts)
		if err != nil {
			// Try without nanoseconds.
			ts, err = time.Parse("2006-01-02T15:04:05Z", ev.Ts)
			if err != nil {
				continue
			}
		}
		ts = ts.UTC()
		if ts.Before(start) {
			continue
		}
		bk := ts.Truncate(bucket).Unix()
		switch metric {
		case "cost":
			buckets[bk] += eventCostUSD(ev)
		case "latency":
			buckets[bk] += float64(eventLatencyMs(ev))
			bucketCounts[bk]++
		case "dispatches":
			buckets[bk]++
		}
	}

	// Build the full sequence of bucket start times in the window.
	var points []TimeseriesPoint
	for t := start; !t.After(now); t = t.Add(bucket) {
		bk := t.Unix()
		val := buckets[bk]
		if metric == "latency" {
			if cnt := bucketCounts[bk]; cnt > 0 {
				val = val / float64(cnt)
			}
		}
		points = append(points, TimeseriesPoint{
			Ts:    t.Format(time.RFC3339),
			Value: math.Round(val*100) / 100,
		})
	}
	return points
}

// ---- By-axis breakdown ------------------------------------------------------

// ComputeByAxis aggregates events by a given axis dimension.
// axis is one of "agent", "runtime", "project", "day".
func ComputeByAxis(events []cost.Event, axis string) []AxisRow {
	type acc struct {
		dispatches int64
		totalCost  float64
		latencies  []int64
	}
	keys := make([]string, 0, 32)
	byKey := make(map[string]*acc, 32)

	for _, ev := range events {
		k := axisKeyFor(ev, axis)
		a, ok := byKey[k]
		if !ok {
			a = &acc{}
			byKey[k] = a
			keys = append(keys, k)
		}
		a.dispatches++
		a.totalCost += eventCostUSD(ev)
		a.latencies = append(a.latencies, eventLatencyMs(ev))
	}

	rows := make([]AxisRow, 0, len(keys))
	for _, k := range keys {
		a := byKey[k]
		sort.Slice(a.latencies, func(i, j int) bool { return a.latencies[i] < a.latencies[j] })
		var avg, p95 int64
		if len(a.latencies) > 0 {
			var sum int64
			for _, l := range a.latencies {
				sum += l
			}
			avg = sum / int64(len(a.latencies))
			p95 = percentile(a.latencies, 95)
		}
		rows = append(rows, AxisRow{
			Key:          k,
			Dispatches:   a.dispatches,
			CostUSD:      math.Round(a.totalCost*10000) / 10000,
			AvgLatencyMs: avg,
			P95LatencyMs: p95,
		})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Dispatches != rows[j].Dispatches {
			return rows[i].Dispatches > rows[j].Dispatches
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

// axisKeyFor derives the grouping key for ev under axis.
func axisKeyFor(ev cost.Event, axis string) string {
	switch axis {
	case "agent":
		if ev.Agent == "" {
			return "(unknown)"
		}
		return ev.Agent
	case "runtime":
		if ev.Runtime == "" {
			return "(unknown)"
		}
		return ev.Runtime
	case "project":
		if ev.Project == "" {
			return "(unknown)"
		}
		return ev.Project
	case "day":
		parts := strings.SplitN(ev.Ts, "T", 2)
		if len(parts) == 2 {
			return parts[0]
		}
		return ev.Ts
	}
	return ev.Runtime
}

// ---- Recent dispatches ------------------------------------------------------

// RecentDispatches returns the last n events from the slice (most recent last
// in the dispatch log, so we take from the end).
func RecentDispatches(events []cost.Event, limit int) []DispatchRow {
	if limit <= 0 {
		limit = 50
	}
	start := 0
	if len(events) > limit {
		start = len(events) - limit
	}
	rows := make([]DispatchRow, 0, len(events)-start)
	for _, ev := range events[start:] {
		rows = append(rows, DispatchRow{
			Ts:        ev.Ts,
			Agent:     ev.Agent,
			Runtime:   ev.Runtime,
			Project:   ev.Project,
			ExitCode:  ev.ExitCode,
			DurationS: ev.DurationS,
			CostUSD:   math.Round(eventCostUSD(ev)*10000) / 10000,
			LatencyMs: eventLatencyMs(ev),
		})
	}
	return rows
}
