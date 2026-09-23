package hooklog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestAppend_NoopOnEmptyWorkDir(t *testing.T) {
	if err := hooklog.Append("", hooklog.Entry{Hook: "x"}, fixedTime); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAppend_RequiresHookName(t *testing.T) {
	dir := t.TempDir()
	if err := hooklog.Append(dir, hooklog.Entry{}, fixedTime); err == nil {
		t.Fatal("expected error for empty Hook name")
	}
}

func TestAppend_FieldSetAndOrder(t *testing.T) {
	dir := t.TempDir()
	err := hooklog.Append(dir, hooklog.Entry{
		Hook:      "path-log",
		Severity:  "REPORT",
		Decision:  "pass",
		Reason:    "logged file-write attempt",
		Agent:     "lead",
		SessionID: "sess-1",
		Event:     "PreToolUse",
		Extra: map[string]any{
			"agent_type": "lead",
			"file_path":  "x.go",
			"tool":       "Edit",
		},
	}, fixedTime)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	logFile := filepath.Join(dir, "logs", "path-log.ndjson")
	data, err := os.ReadFile(logFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := strings.TrimRight(string(data), "\n")

	// The base 8 fields must appear first, in ho_log's fixed order, before
	// any extra key — verified by key position in the raw JSON text (a
	// stronger assertion than parsed-map comparison, which would not catch
	// a field-order regression).
	wantOrder := []string{`"ts"`, `"hook"`, `"severity"`, `"decision"`, `"reason"`, `"agent"`, `"session_id"`, `"event"`}
	lastIdx := -1
	for _, key := range wantOrder {
		idx := strings.Index(line, key)
		if idx == -1 {
			t.Fatalf("key %s not found in %s", key, line)
		}
		if idx < lastIdx {
			t.Fatalf("key %s out of order in %s", key, line)
		}
		lastIdx = idx
	}

	recs := readLines(t, logFile)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	rec := recs[0]
	want := map[string]any{
		"ts":         "2026-01-15T10:00:00Z",
		"hook":       "path-log",
		"severity":   "REPORT",
		"decision":   "pass",
		"reason":     "logged file-write attempt",
		"agent":      "lead",
		"session_id": "sess-1",
		"event":      "PreToolUse",
		"agent_type": "lead",
		"file_path":  "x.go",
		"tool":       "Edit",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("field %s=%v, want %v", k, rec[k], v)
		}
	}
	if len(rec) != len(want) {
		t.Errorf("record has %d fields, want %d: %v", len(rec), len(want), rec)
	}
}

func TestAppend_ExtraOverridesBaseField(t *testing.T) {
	dir := t.TempDir()
	err := hooklog.Append(dir, hooklog.Entry{
		Hook:     "x",
		Decision: "pass",
		Extra:    map[string]any{"decision": "override"},
	}, fixedTime)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	recs := readLines(t, filepath.Join(dir, "logs", "x.ndjson"))
	if recs[0]["decision"] != "override" {
		t.Errorf("decision=%v, want override (extra should win)", recs[0]["decision"])
	}
}

func TestAppend_MultipleCallsAppend(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := hooklog.Append(dir, hooklog.Entry{Hook: "x"}, fixedTime); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	recs := readLines(t, filepath.Join(dir, "logs", "x.ndjson"))
	if len(recs) != 3 {
		t.Errorf("expected 3 records, got %d", len(recs))
	}
}

func TestAppend_ExtraKeysDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	err := hooklog.Append(dir, hooklog.Entry{
		Hook:  "x",
		Extra: map[string]any{"zeta": 1, "alpha": 2, "mid": 3},
	}, fixedTime)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "logs", "x.ndjson")) //nolint:gosec
	line := string(data)
	ia := strings.Index(line, `"alpha"`)
	im := strings.Index(line, `"mid"`)
	iz := strings.Index(line, `"zeta"`)
	if !(ia < im && im < iz) {
		t.Errorf("extra keys not sorted: alpha=%d mid=%d zeta=%d in %s", ia, im, iz, line)
	}
}

func TestAppend_CreatesLogsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := hooklog.Append(dir, hooklog.Entry{Hook: "x"}, fixedTime); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "logs")); err != nil {
		t.Errorf("logs dir should exist: %v", err)
	}
}
