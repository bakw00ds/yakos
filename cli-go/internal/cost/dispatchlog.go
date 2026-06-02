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
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Usage holds the optional per-invocation token/cost data reported by the
// runtime adapter (available on events where the runtime returned structured
// telemetry).
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

	// Routing metadata (newer events).
	ModelResolved  string `json:"model_resolved,omitempty"`
	ModelChosenBy  string `json:"model_chosen_by,omitempty"`
	EvalRunID      string `json:"eval_run_id,omitempty"`

	// TaskPreview is present on dispatch_started events only.
	TaskPreview string `json:"task_preview,omitempty"`
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
