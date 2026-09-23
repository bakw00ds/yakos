package pathlog_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/pathlog"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

// newInput builds a HookInput the way hookio.Decode would from a Claude
// Code hook JSON payload: agent_type and session_id live at the top level
// of Payload, and the file path lives under tool_input.
func newInput(tool, filePath, agentType string) hooktype.HookInput {
	payload := map[string]any{}
	if agentType != "" {
		payload["agent_type"] = agentType
	}
	if filePath != "" {
		payload["tool_input"] = map[string]any{"file_path": filePath}
	}
	return hooktype.HookInput{
		Tool:    tool,
		Payload: payload,
	}
}

func readLastLogEntry(t *testing.T, logFile string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("no log lines written")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("unmarshal last log line: %v", err)
	}
	return rec
}

func TestPathLog_EditLogged(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("Edit", "cli-go/main.go", "backend"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v, want REPORT", rec["severity"])
	}
	if rec["tool"] != "Edit" {
		t.Errorf("tool=%v, want Edit", rec["tool"])
	}
	if rec["file_path"] != "cli-go/main.go" {
		t.Errorf("file_path=%v, want cli-go/main.go", rec["file_path"])
	}
}

func TestPathLog_WriteLogged(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("Write", "foo.go", ""))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("unexpected err=%v code=%d", err, out.ExitCode)
	}
	readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
}

func TestPathLog_MultiEditLogged(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("MultiEdit", "a/b.go", "lead"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("unexpected err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["tool"] != "MultiEdit" {
		t.Errorf("tool=%v, want MultiEdit", rec["tool"])
	}
}

func TestPathLog_BashSkipped(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, err := h.Run(context.Background(), newInput("Bash", "script.sh", ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No log file should have been created.
	if _, statErr := os.Stat(filepath.Join(dir, "logs", "path-log.ndjson")); !os.IsNotExist(statErr) {
		t.Error("expected no log file for Bash tool")
	}
}

func TestPathLog_ReadSkipped(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, _ := h.Run(context.Background(), newInput("Read", "main.go", ""))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d", out.ExitCode)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "logs", "path-log.ndjson")); !os.IsNotExist(statErr) {
		t.Error("expected no log file for Read tool")
	}
}

func TestPathLog_NoWorkCurrentDir(t *testing.T) {
	h := &pathlog.Hook{WorkCurrentDir: "", NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("Edit", "x.go", ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d", out.ExitCode)
	}
}

func TestPathLog_AgentRoleRecorded(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("Write", "schema.sql", "db-migrations"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("unexpected err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["agent_type"] != "db-migrations" {
		t.Errorf("agent_type=%v, want db-migrations", rec["agent_type"])
	}
	// hooklog's top-level "agent" field must carry the same value bash's
	// ho_log computes via a second hi_sender_role() call.
	if rec["agent"] != "db-migrations" {
		t.Errorf("agent=%v, want db-migrations", rec["agent"])
	}
}

func TestPathLog_AgentNamespacePrefixStripped(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("Edit", "x.go", "yakos:go-api"))
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["agent_type"] != "go-api" {
		t.Errorf("agent_type=%v, want go-api (yakos: prefix stripped)", rec["agent_type"])
	}
}

func TestPathLog_TimestampPresent(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("Edit", "x.go", ""))
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["ts"] != "2026-01-15T10:00:00Z" {
		t.Errorf("ts=%v, want 2026-01-15T10:00:00Z", rec["ts"])
	}
}

func TestPathLog_MultipleCallsAppend(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	for i := 0; i < 3; i++ {
		_, _ = h.Run(context.Background(), newInput("Edit", "file.go", ""))
	}
	data, _ := os.ReadFile(filepath.Join(dir, "logs", "path-log.ndjson"))
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	if count != 3 {
		t.Errorf("expected 3 log lines, got %d", count)
	}
}

func TestPathLog_EmptyFilePathLogged(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{Tool: "Edit", Payload: map[string]any{}}
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["file_path"] != "" {
		t.Errorf("file_path should be empty, got %v", rec["file_path"])
	}
}

func TestPathLog_FilePathFromNotebookPathFallback(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Tool: "Edit",
		Payload: map[string]any{
			"tool_input": map[string]any{"notebook_path": "/some/notebook.ipynb"},
		},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["file_path"] != "/some/notebook.ipynb" {
		t.Errorf("file_path=%v", rec["file_path"])
	}
}

func TestPathLog_HookNameInLog(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("Edit", "x.go", ""))
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["hook"] != "path-log" {
		t.Errorf("hook=%v", rec["hook"])
	}
}

func TestPathLog_LogCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("Write", "x.go", ""))
	if _, err := os.Stat(filepath.Join(dir, "logs")); err != nil {
		t.Errorf("logs dir should exist: %v", err)
	}
}

func TestPathLog_AlwaysExitZero(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("Edit", "x.go", ""))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", out.ExitCode)
	}
}

func TestPathLog_Name(t *testing.T) {
	h := pathlog.New("/tmp/work")
	if h.Name() != "path-log" {
		t.Errorf("Name()=%q", h.Name())
	}
}

// TestPathLog_DecisionFieldIsPass replaces the pre-hookio
// TestPathLog_ActionFieldIsPass: hooklog writes bash's "decision" field
// name, not the old ad-hoc "action" name.
func TestPathLog_DecisionFieldIsPass(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("Edit", "x.go", ""))
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["decision"] != "pass" {
		t.Errorf("decision=%v", rec["decision"])
	}
	if rec["reason"] != "logged file-write attempt" {
		t.Errorf("reason=%v", rec["reason"])
	}
}

// TestPathLog_DefaultAgentIsLead replaces the pre-hookio
// TestPathLog_UnknownAgentDefault: hi_sender_role's fallback (and
// therefore this hook's) is "lead", not "unknown".
func TestPathLog_DefaultAgentIsLead(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Tool:    "Edit",
		Payload: map[string]any{"tool_input": map[string]any{"file_path": "x.go"}},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["agent_type"] != "lead" {
		t.Errorf("agent_type=%v, want lead", rec["agent_type"])
	}
	if rec["agent"] != "lead" {
		t.Errorf("agent=%v, want lead", rec["agent"])
	}
}

// TestPathLog_SessionIDAndEventRecorded covers the two fields the
// pre-hookio implementation dropped entirely (S-6 structural plan §1.2).
func TestPathLog_SessionIDAndEventRecorded(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Event: "PreToolUse",
		Tool:  "Edit",
		Payload: map[string]any{
			"session_id": "sess-123",
			"tool_input": map[string]any{"file_path": "x.go"},
		},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["session_id"] != "sess-123" {
		t.Errorf("session_id=%v, want sess-123", rec["session_id"])
	}
	if rec["event"] != "PreToolUse" {
		t.Errorf("event=%v, want PreToolUse", rec["event"])
	}
}

// TestPathLog_DecodedFromClaudeJSON exercises the full hookio.Decode ->
// pathlog.Run path against the real fixture shape (tests/fixtures/hooks
// convention), not a hand-built HookInput — this is the parity-relevant
// path `yakos hook run path-log` actually takes.
func TestPathLog_DecodedFromClaudeJSON(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{
		"session_id": "fixture-edit-api-0001",
		"cwd": "/tmp/fake-project",
		"agent_type": "go-api",
		"hook_event_name": "PreToolUse",
		"tool_name": "Edit",
		"tool_input": {
			"file_path": "api/main.go",
			"old_string": "package main",
			"new_string": "package main\n// edited"
		}
	}`)
	in, err := hookio.DecodeBytes(raw)
	if err != nil {
		t.Fatalf("hookio.DecodeBytes: %v", err)
	}
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("unexpected err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["file_path"] != "api/main.go" {
		t.Errorf("file_path=%v, want api/main.go", rec["file_path"])
	}
	if rec["agent_type"] != "go-api" {
		t.Errorf("agent_type=%v, want go-api", rec["agent_type"])
	}
	if rec["session_id"] != "fixture-edit-api-0001" {
		t.Errorf("session_id=%v", rec["session_id"])
	}
	if rec["event"] != "PreToolUse" {
		t.Errorf("event=%v", rec["event"])
	}
}
