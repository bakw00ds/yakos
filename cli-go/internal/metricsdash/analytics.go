package metricsdash

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/metrics"
)

// SnapshotHeader is the minimal header returned in GET /api/metrics/history.
type SnapshotHeader struct {
	Commit string    `json:"commit"`
	Ts     time.Time `json:"ts"`
	Branch string    `json:"branch"`
}

// TrendPoint is one data point in a trend series.
type TrendPoint struct {
	Ts     time.Time `json:"ts"`
	Commit string    `json:"commit"`
	Value  *float64  `json:"value"` // nil when metric not measured at that snapshot
}

// CompareDiff is the shape for GET /api/metrics/compare.
type CompareDiff struct {
	A *metrics.Snapshot `json:"a"`
	B *metrics.Snapshot `json:"b"`
}

// ProjectSummary is one entry in the cross-project rollup.
// HistoryPath is intentionally omitted from JSON: exposing full filesystem
// paths is an unnecessary information leak to browser clients.
type ProjectSummary struct {
	Project       string            `json:"project"`
	SnapshotCount int               `json:"snapshot_count"`
	Latest        *metrics.Snapshot `json:"latest"` // nil if no snapshots
}

// HistoryHeaders extracts commit+ts headers from a snapshot slice.
func HistoryHeaders(snaps []metrics.Snapshot) []SnapshotHeader {
	if len(snaps) == 0 {
		return []SnapshotHeader{}
	}
	out := make([]SnapshotHeader, len(snaps))
	for i, s := range snaps {
		out[i] = SnapshotHeader{
			Commit: s.Commit,
			Ts:     s.Ts,
			Branch: s.Branch,
		}
	}
	return out
}

// LatestSnapshot returns the last snapshot or nil if empty.
func LatestSnapshot(snaps []metrics.Snapshot) *metrics.Snapshot {
	if len(snaps) == 0 {
		return nil
	}
	s := snaps[len(snaps)-1]
	return &s
}

// TrendSeries extracts a metric trend over the last lastN snapshots since
// sinceTS. metricPath is a dot-path like "efficiency.total_cost_usd".
func TrendSeries(snaps []metrics.Snapshot, metricPath string, lastN int, sinceTS string) []TrendPoint {
	// Filter by since.
	var filtered []metrics.Snapshot
	for _, s := range snaps {
		if sinceTS != "" {
			t, err := parseISO(sinceTS)
			if err == nil && s.Ts.Before(t) {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	// Apply lastN limit.
	if lastN > 0 && len(filtered) > lastN {
		filtered = filtered[len(filtered)-lastN:]
	}

	if len(filtered) == 0 {
		return []TrendPoint{}
	}

	out := make([]TrendPoint, len(filtered))
	for i, s := range filtered {
		val := getMetricByPath(&s.Metrics, metricPath)
		out[i] = TrendPoint{
			Ts:     s.Ts,
			Commit: s.Commit,
			Value:  val,
		}
	}
	return out
}

// CompareSnapshots finds two snapshots by commit SHA prefix and returns a diff.
// Returns nil, nil if both are not found (callers should 404).
func CompareSnapshots(snaps []metrics.Snapshot, shaA, shaB string) (*metrics.Snapshot, *metrics.Snapshot) {
	var snapA, snapB *metrics.Snapshot
	for i := range snaps {
		s := &snaps[i]
		if strings.HasPrefix(s.Commit, shaA) {
			snapA = s
		}
		if strings.HasPrefix(s.Commit, shaB) {
			snapB = s
		}
	}
	return snapA, snapB
}

// getMetricByPath extracts a *float64 for the given dot-path.
// Delegates to metrics.ResolveMetricPath (the authoritative resolver from
// gate.go) to ensure consistent path resolution across gate, trend, and
// dashboard endpoints. Returns nil when the metric was not measured.
func getMetricByPath(m *metrics.Metrics, path string) *float64 {
	v, ok := metrics.ResolveMetricPath(m, path)
	if !ok {
		return nil
	}
	return &v
}

// parseISO delegates to metrics.ParseISO for consistent timestamp parsing.
// The local copy is removed — cross-package unexported access is no longer
// needed now that metrics.ParseISO is exported.
func parseISO(s string) (time.Time, error) {
	return metrics.ParseISO(s)
}

// ---- Live cost from dispatch-log --------------------------------------------

// LiveCostResult is returned by GET /api/metrics/live_cost.
//
// Dollars follow the K-136 rule: TotalCostUSD is Event.SpendUSD summed, so only
// runs billed per API call (and rows that predate the billing field) count; a
// subscription or local run never adds to it. Tokens are the primary unit: they
// come from the usage object only (Event.Tokens), and the est_input_tokens and
// est_output_tokens size estimates are never mixed in.
//
// TotalCostUSD and EventCount keep their original names and meaning. Everything
// after them is additive (K-136) and absent from an older server.
type LiveCostResult struct {
	TotalCostUSD float64 `json:"total_cost_usd"`
	EventCount   int     `json:"event_count"`

	// Tokens is the sum of reported token counts by kind, as logged. A legacy
	// bash-written codex row keeps its cached tokens inside input while a Go row
	// splits them into cache_read, so only TotalTokens is comparable across rows.
	Tokens      cost.TokenTotals `json:"tokens"`
	TotalTokens int64            `json:"total_tokens"`
	// APIEquivalentUSD is what subscription runs would have cost at API rates, as
	// the harness reported it. It is informational: never spend, never added to
	// TotalCostUSD.
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
	// Runtimes is the per-runtime breakdown. It is always an array, ordered by
	// tokens (then dispatches, then runtime name) so the output is deterministic.
	Runtimes []LiveRuntimeRow `json:"runtimes"`
}

// LiveRuntimeRow is one runtime's share of a LiveCostResult. USD is API spend
// only, so a subscription runtime such as codex shows tokens and 0 dollars.
type LiveRuntimeRow struct {
	Runtime          string  `json:"runtime"`
	Dispatches       int64   `json:"dispatches"`
	Tokens           int64   `json:"tokens"`
	USD              float64 `json:"usd"`
	APIEquivalentUSD float64 `json:"api_equivalent_usd"`
}

// unknownRuntime labels events whose runtime field is empty.
const unknownRuntime = "(unknown)"

// roundUSD trims float summation noise (0.1 + 0.2) to micro-dollars.
func roundUSD(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// aggregateLive rolls the events up into a LiveCostResult. Dollars come from
// Event.SpendUSD and tokens from Event.Tokens, the only sanctioned readers.
func aggregateLive(events <-chan cost.Event) *LiveCostResult {
	type acc struct {
		dispatches int64
		tokens     int64
		usd        float64
		apiEquiv   float64
	}
	byRuntime := make(map[string]*acc)
	var total cost.TokenTotals
	var spend, apiEquiv float64
	n := 0

	for ev := range events {
		n++
		tok := ev.Tokens()
		usd := ev.SpendUSD()
		eq := cost.APIEquivalentUSD(ev)
		total = total.Add(tok)
		spend += usd
		apiEquiv += eq

		key := ev.Runtime
		if key == "" {
			key = unknownRuntime
		}
		a := byRuntime[key]
		if a == nil {
			a = &acc{}
			byRuntime[key] = a
		}
		a.dispatches++
		a.tokens += tok.Total()
		a.usd += usd
		a.apiEquiv += eq
	}

	rows := make([]LiveRuntimeRow, 0, len(byRuntime))
	for k, a := range byRuntime {
		rows = append(rows, LiveRuntimeRow{
			Runtime:          k,
			Dispatches:       a.dispatches,
			Tokens:           a.tokens,
			USD:              roundUSD(a.usd),
			APIEquivalentUSD: roundUSD(a.apiEquiv),
		})
	}
	// Map order is random; a total order keeps the response deterministic.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Tokens != rows[j].Tokens {
			return rows[i].Tokens > rows[j].Tokens
		}
		if rows[i].Dispatches != rows[j].Dispatches {
			return rows[i].Dispatches > rows[j].Dispatches
		}
		return rows[i].Runtime < rows[j].Runtime
	})

	return &LiveCostResult{
		TotalCostUSD:     roundUSD(spend),
		EventCount:       n,
		Tokens:           total,
		TotalTokens:      total.Total(),
		APIEquivalentUSD: roundUSD(apiEquiv),
		Runtimes:         rows,
	}
}

// liveCostCache is an mtime+size-keyed cache for the aggregated dispatch-log
// spend and token totals.  It mirrors the eventsCache pattern in
// perfdash/server.go: re-reads only when a log file changes.
type liveCostCache struct {
	mu        sync.Mutex
	maxMtime  time.Time
	totalSize int64
	result    *LiveCostResult
}

// load returns the cached result when log files are unchanged; otherwise
// re-aggregates from the dispatch-log directory and updates the cache.
// Returns (nil, nil) when dispatchLogDir is empty (feature disabled).
func (c *liveCostCache) load(dispatchLogDir string) (*LiveCostResult, error) {
	if dispatchLogDir == "" {
		return nil, nil
	}

	paths, err := cost.LogFiles(dispatchLogDir)
	if err != nil {
		return nil, fmt.Errorf("metricsdash: live cost log files: %w", err)
	}

	// Stat all files to compute aggregate mtime + size.
	var maxMtime time.Time
	var totalSize int64
	for _, p := range paths {
		fi, err := statFile(p)
		if err != nil {
			continue // file may have been rotated away
		}
		if fi.mtime.After(maxMtime) {
			maxMtime = fi.mtime
		}
		totalSize += fi.size
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Cache hit: files unchanged.
	if maxMtime.Equal(c.maxMtime) && totalSize == c.totalSize && c.result != nil {
		return c.result, nil
	}

	// Cache miss: re-aggregate.
	r := aggregateLive(cost.StreamFiles(paths, ""))
	c.maxMtime = maxMtime
	c.totalSize = totalSize
	c.result = r
	return r, nil
}

// fileStat holds the mtime and size of a file.
type fileStat struct {
	mtime time.Time
	size  int64
}

// statFile returns mtime+size for path, or an error when the file is
// inaccessible (rotated away, permissions, etc.).
func statFile(path string) (fileStat, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStat{}, err
	}
	return fileStat{mtime: fi.ModTime(), size: fi.Size()}, nil
}
