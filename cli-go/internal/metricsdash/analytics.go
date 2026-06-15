package metricsdash

import (
	"fmt"
	"os"
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

// LiveCostResult is returned by LiveTotalCostUSD.
type LiveCostResult struct {
	TotalCostUSD float64 `json:"total_cost_usd"`
	EventCount   int     `json:"event_count"`
}

// liveCostCache is an mtime+size-keyed cache for the aggregated dispatch-log
// cost total.  It mirrors the eventsCache pattern in perfdash/server.go:
// re-reads only when a log file changes.
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
	ch := cost.StreamFiles(paths, "")
	var total float64
	var n int
	for ev := range ch {
		if ev.Usage != nil {
			total += ev.Usage.TotalCostUSD
		}
		n++
	}

	r := &LiveCostResult{TotalCostUSD: total, EventCount: n}
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
