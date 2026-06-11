package metrics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// historyFile is the relative path within a project for the metrics history.
const historyFile = ".yakos/metrics/history.ndjson"

// HistoryPath returns the absolute path to the history.ndjson file for
// the given projectDir.
func HistoryPath(projectDir string) string {
	return filepath.Join(projectDir, historyFile)
}

// AppendSnapshot appends snap as a single JSON line to the history file.
// The directory is created if it does not exist. The write is O_APPEND|O_CREATE
// so concurrent appends (one line < PIPE_BUF) are atomic on POSIX.
func AppendSnapshot(projectDir string, snap Snapshot) error {
	path := HistoryPath(projectDir)
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return fmt.Errorf("metrics: mkdir %s: %w", filepath.Dir(path), err)
	}
	line, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("metrics: marshal snapshot: %w", err)
	}
	line = append(line, '\n')

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("metrics: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("metrics: write %s: %w", path, err)
	}
	return nil
}

// ReadHistory reads all snapshots from the history file at projectDir.
// Returns an empty slice (and no error) if the file does not exist.
// Malformed lines are skipped.
func ReadHistory(projectDir string) ([]Snapshot, error) {
	path := HistoryPath(projectDir)
	f, err := os.Open(path) //nolint:gosec
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("metrics: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var snaps []Snapshot
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var s Snapshot
		if err := json.Unmarshal(line, &s); err != nil {
			continue // skip malformed lines
		}
		snaps = append(snaps, s)
	}
	return snaps, nil
}
