package telemetry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// logPath returns the path to the main telemetry NDJSON log.
func logPath(home string) string {
	return filepath.Join(home, ".yakos-state", "telemetry.ndjson")
}

// shippedLogPath returns the path to the shipped-records sidecar.
// Records appended here have already been POSTed to the configured endpoint.
func shippedLogPath(home string) string {
	return filepath.Join(home, ".yakos-state", "telemetry-shipped.ndjson")
}

// Record appends ev (after redaction) to the local telemetry NDJSON log when
// telemetry is enabled.  It is fail-silent: any error is silently discarded so
// the CLI never blocks or aborts on a telemetry failure.
//
// Record is safe to call even when telemetry is disabled — it short-circuits
// immediately.  The recommended call site is a defer in main():
//
//	defer telemetry.Record(home, ev)
func Record(home string, ev Event) {
	cfg, err := LoadConfig(home)
	if err != nil || !cfg.Enabled {
		return
	}

	ev = RedactEvent(ev)

	path := logPath(home)
	if err := appendNDJSON(path, ev); err != nil {
		// Fail-silent.
		return
	}
}

// appendNDJSON marshals v and appends it as a single NDJSON line to path.
// The file is created if it does not exist (mode 0600).
// O_APPEND is atomic on POSIX for writes <= PIPE_BUF (~4 KiB); our records
// are well under that limit, so this is safe for single-host single-process
// use.  Cross-process locking is deferred to Phase 2 daemon (Q8 decision).
func appendNDJSON(path string, v interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}

	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := fmt.Fprintf(f, "%s\n", b); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// CountRecords returns the number of NDJSON records in the local telemetry log.
// Returns 0 without error when the file does not exist.
func CountRecords(home string) (int, error) {
	return countLines(logPath(home))
}

// countLines counts non-empty lines in path.
func countLines(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read: %w", err)
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n, nil
}

// ReadRecentRecords returns the last limit records from the local telemetry
// log, parsed as Event values.  When limit <= 0 all records are returned.
func ReadRecentRecords(home string, limit int) ([]Event, error) {
	data, err := os.ReadFile(logPath(home)) //nolint:gosec
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: read log: %w", err)
	}

	// Split into non-empty lines.
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := string(data[start:i])
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	// Remaining content after last newline.
	if start < len(data) {
		if line := string(data[start:]); len(line) > 0 {
			lines = append(lines, line)
		}
	}

	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}

	out := make([]Event, 0, len(lines))
	for _, l := range lines {
		var ev Event
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			continue // skip corrupt lines
		}
		out = append(out, ev)
	}
	return out, nil
}

// PurgeLog removes the local telemetry NDJSON log file.
// Returns nil when the file did not exist.
func PurgeLog(home string) error {
	path := logPath(home)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
