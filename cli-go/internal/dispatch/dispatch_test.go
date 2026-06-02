package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/cost"
)

// fixedTime is a stable reference time for test assertions.
var fixedTime = time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

// ---- test fixtures -----------------------------------------------------------

// isolatedLogDir creates a temp dir for the dispatch-log and sets
// YAKOS_DISPATCH_LOG so events are written there rather than ~/.yakos-state.
func isolatedLogDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)
	return dir
}

// readDispatchLog reads all NDJSON events from the dispatch-log in dir.
func readDispatchLog(t *testing.T, dir string) []map[string]interface{} {
	t.Helper()
	path := filepath.Join(dir, "dispatch-log.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readDispatchLog: %v", err)
	}
	var events []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev map[string]interface{}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("readDispatchLog: parse line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

// assertField checks that a JSON event map has the expected string value for key.
func assertField(t *testing.T, m map[string]interface{}, key, want string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("field %q missing from event %v", key, m)
		return
	}
	if v != want {
		t.Errorf("field %q: got %v, want %q", key, v, want)
	}
}

// ---- stderr.go tests ---------------------------------------------------------

func TestProcessStderr_ANSIStrip(t *testing.T) {
	input := []byte("\x1b[31mERROR:\x1b[0m something failed\n")
	tail, trunc := processStderr(input)
	if strings.Contains(tail, "\x1b") {
		t.Errorf("ANSI escape not stripped; tail=%q", tail)
	}
	if trunc {
		t.Error("expected no truncation for small input")
	}
	if !strings.Contains(tail, "ERROR: something failed") {
		t.Errorf("expected error text preserved; tail=%q", tail)
	}
}

func TestProcessStderr_Last30Lines(t *testing.T) {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, strings.Repeat("x", 50))
	}
	input := []byte(strings.Join(lines, "\n"))
	tail, trunc := processStderr(input)
	if !trunc {
		t.Error("expected truncated=true for 50 lines")
	}
	tailLines := strings.Split(strings.TrimSpace(tail), "\n")
	if len(tailLines) > 30 {
		t.Errorf("tail has %d lines, want ≤30", len(tailLines))
	}
}

func TestProcessStderr_4KBLimit(t *testing.T) {
	// 5KB of data in a single line.
	input := []byte(strings.Repeat("a", 5000) + "\n")
	tail, trunc := processStderr(input)
	if !trunc {
		t.Error("expected truncated=true for 5KB input")
	}
	if len(tail) > 4096 {
		t.Errorf("tail is %d bytes, want ≤4096", len(tail))
	}
}

func TestProcessStderr_Empty(t *testing.T) {
	tail, trunc := processStderr([]byte{})
	if tail != "" {
		t.Errorf("empty input: got tail=%q, want empty", tail)
	}
	if trunc {
		t.Error("empty input: expected trunc=false")
	}
}

func TestProcessStderr_OnlyWhitespace(t *testing.T) {
	tail, _ := processStderr([]byte("   \n\n   "))
	if tail != "" {
		t.Errorf("whitespace-only: expected empty tail, got %q", tail)
	}
}

// ---- events.go: truncate ----------------------------------------------------

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"", 10, ""},
		{"abc", 3, "abc"},
	}
	for _, tc := range cases {
		got := truncate(tc.in, tc.n)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

// ---- events.go: writeStarted schema (PR #40) --------------------------------

func TestWriteStarted_Schema(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{
		AgentName: "backend",
		Runtime:   "claude",
		Project:   "/abs/project",
		Task:      "implement GET /v1/users",
		YakosRoot: "/abs/yakos",
	}

	writeStarted(req, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	assertField(t, ev, "type", "dispatch_started")
	assertField(t, ev, "agent", "backend")
	assertField(t, ev, "runtime", "claude")
	assertField(t, ev, "project", "/abs/project") // PR #40
	if ev["task_preview"] == nil {
		t.Error("task_preview should be present in dispatch_started")
	}
}

func TestWriteStarted_TaskPreviewTruncated(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{
		AgentName: "agent",
		Runtime:   "claude",
		Project:   "/p",
		Task:      strings.Repeat("x", 300), // 300 bytes
	}
	writeStarted(req, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	preview, _ := events[0]["task_preview"].(string)
	if len(preview) > 200 {
		t.Errorf("task_preview should be truncated to 200 bytes; got %d", len(preview))
	}
}

// ---- events.go: writeFinished schema (PR #40 + #34 + #32 + #31) ------------

func TestWriteFinished_HappyPath(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{
		AgentName:     "backend",
		Runtime:       "claude",
		Project:       "/abs/project",
		Task:          "implement something",
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
	}
	res := Result{
		ExitCode:      0,
		DurationS:     1.5,
		OutputBytes:   200,
		TaskBytes:     100,
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
	}

	writeFinished(req, res, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	assertField(t, ev, "type", "dispatch_finished")
	assertField(t, ev, "agent", "backend")
	assertField(t, ev, "runtime", "claude")
	assertField(t, ev, "project", "/abs/project")        // PR #40
	assertField(t, ev, "model_resolved", "sonnet")       // PR #32
	assertField(t, ev, "model_chosen_by", "frontmatter") // PR #32

	// PR #34: stderr_tail and stderr_truncated always present.
	if _, ok := ev["stderr_tail"]; !ok {
		t.Error("stderr_tail field must always be present (PR #34)")
	}
	if _, ok := ev["stderr_truncated"]; !ok {
		t.Error("stderr_truncated field must always be present (PR #34)")
	}
	// On exit_code==0, stderr_tail must be null.
	if ev["stderr_tail"] != nil {
		t.Errorf("stderr_tail must be null on exit_code=0 (PR #34); got %v", ev["stderr_tail"])
	}

	// eval_run_id must be present as null.
	if _, ok := ev["eval_run_id"]; !ok {
		t.Error("eval_run_id field must always be present")
	}
	if ev["eval_run_id"] != nil {
		t.Errorf("eval_run_id must be null when not set; got %v", ev["eval_run_id"])
	}
}

func TestWriteFinished_StderrTailOnFailure(t *testing.T) {
	// PR #34: stderr_tail populated on non-zero exit.
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{AgentName: "backend", Runtime: "claude", Project: "/p"}
	res := Result{
		ExitCode:      1,
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
		StderrTail:    "error: tool not found",
		StderrTrunc:   false,
	}

	writeFinished(req, res, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	ev := events[0]
	if ev["stderr_tail"] != "error: tool not found" {
		t.Errorf("stderr_tail mismatch on failure: %v", ev["stderr_tail"])
	}
	if ev["stderr_truncated"] != false {
		t.Errorf("stderr_truncated: got %v, want false", ev["stderr_truncated"])
	}
}

func TestWriteFinished_EvalRunID(t *testing.T) {
	// PR #32 / model-routing eval: eval_run_id populated, model_chosen_by="eval".
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{AgentName: "backend", Runtime: "claude", Project: "/p"}
	res := Result{
		ExitCode:      0,
		ModelChosenBy: "eval",
		ModelResolved: "sonnet",
		EvalRunID:     "e-abc123",
	}

	writeFinished(req, res, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	ev := events[0]
	if ev["eval_run_id"] != "e-abc123" {
		t.Errorf("eval_run_id: got %v, want %q", ev["eval_run_id"], "e-abc123")
	}
	assertField(t, ev, "model_chosen_by", "eval")
}

func TestWriteFinished_UsageField(t *testing.T) {
	// PR #31: usage.cache_read populated from runtime telemetry.
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{AgentName: "backend", Runtime: "claude", Project: "/p"}
	res := Result{
		ExitCode:      0,
		ModelChosenBy: "frontmatter",
		ModelResolved: "haiku",
		Usage: &cost.Usage{
			InputTokens:   100,
			OutputTokens:  50,
			CacheRead:     30,
			CacheCreation: 10,
			DurationMs:    1500,
			TotalCostUSD:  0.0025,
		},
	}

	writeFinished(req, res, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	ev := events[0]
	usageRaw, ok := ev["usage"]
	if !ok {
		t.Fatal("usage field missing from dispatch_finished")
	}
	usageMap, ok := usageRaw.(map[string]interface{})
	if !ok {
		t.Fatalf("usage is not a map: %T", usageRaw)
	}
	if usageMap["input_tokens"] != float64(100) {
		t.Errorf("usage.input_tokens: got %v, want 100", usageMap["input_tokens"])
	}
	if usageMap["cache_read"] != float64(30) {
		t.Errorf("usage.cache_read: got %v, want 30 (PR #31)", usageMap["cache_read"])
	}
}

func TestWriteFinished_EstTokensComputed(t *testing.T) {
	// Verify est_input_tokens and est_output_tokens are task_bytes/4 and output_bytes/4.
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{AgentName: "agent", Runtime: "claude", Project: "/p"}
	res := Result{
		ExitCode:      0,
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
		TaskBytes:     400,
		OutputBytes:   200,
	}

	writeFinished(req, res, fixedTime, logPath)

	events := readDispatchLog(t, logDir)
	ev := events[0]
	if ev["est_input_tokens"] != float64(100) {
		t.Errorf("est_input_tokens: got %v, want 100", ev["est_input_tokens"])
	}
	if ev["est_output_tokens"] != float64(50) {
		t.Errorf("est_output_tokens: got %v, want 50", ev["est_output_tokens"])
	}
}

func TestAppendEvent_MultipleWrites(t *testing.T) {
	// Verify that multiple calls to appendEvent all land cleanly (no corruption).
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	for i := 0; i < 10; i++ {
		line, _ := json.Marshal(map[string]interface{}{
			"type":  "dispatch_started",
			"agent": "backend",
			"i":     i,
		})
		if err := appendEvent(logPath, line); err != nil {
			t.Fatalf("appendEvent[%d]: %v", i, err)
		}
	}

	events := readDispatchLog(t, logDir)
	if len(events) != 10 {
		t.Errorf("expected 10 events, got %d", len(events))
	}
}

// TestDispatchLog_BothEventTypes verifies that writeStarted + writeFinished
// produce two events in sequence with correct types — the core dispatch-log
// parity contract.
func TestDispatchLog_BothEventTypes(t *testing.T) {
	logDir := isolatedLogDir(t)
	logPath := filepath.Join(logDir, "dispatch-log.ndjson")

	req := Request{
		AgentName:     "backend",
		Runtime:       "claude",
		Project:       "/abs/project",
		Task:          "implement /v1/users GET",
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
	}
	res := Result{
		ExitCode:      0,
		DurationS:     2.3,
		OutputBytes:   512,
		TaskBytes:     64,
		ModelChosenBy: "frontmatter",
		ModelResolved: "sonnet",
	}

	writeStarted(req, fixedTime, logPath)
	writeFinished(req, res, fixedTime.Add(2*time.Second), logPath)

	events := readDispatchLog(t, logDir)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (started + finished), got %d", len(events))
	}
	assertField(t, events[0], "type", "dispatch_started")
	assertField(t, events[1], "type", "dispatch_finished")

	// Both events must carry project field (PR #40).
	assertField(t, events[0], "project", "/abs/project")
	assertField(t, events[1], "project", "/abs/project")
}
