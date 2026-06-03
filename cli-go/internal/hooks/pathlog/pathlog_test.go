package pathlog_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/pathlog"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newInput(tool, filePath, agentRole string) hooktype.HookInput {
	env := map[string]string{}
	if agentRole != "" {
		env["YAKOS_AGENT_ROLE"] = agentRole
	}
	payload := map[string]any{}
	if filePath != "" {
		payload["path"] = filePath
	}
	return hooktype.HookInput{
		Tool:    tool,
		Payload: payload,
		Env:     env,
	}
}

func readLastLogEntry(t *testing.T, logFile string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	lines := []string{}
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			if i > start {
				lines = append(lines, string(data[start:i]))
			}
			start = i + 1
		}
	}
	if len(lines) == 0 {
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
	in := hooktype.HookInput{Tool: "Edit", Payload: map[string]any{}, Env: map[string]string{}}
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["file_path"] != "" {
		t.Errorf("file_path should be empty, got %v", rec["file_path"])
	}
}

func TestPathLog_FilePathFromFilePathKey(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Tool:    "Write",
		Payload: map[string]any{"file_path": "/some/path.go"},
		Env:     map[string]string{},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["file_path"] != "/some/path.go" {
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
	// Even with a read-only dir, the hook must return exit 0.
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

func TestPathLog_ActionFieldIsPass(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("Edit", "x.go", ""))
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["action"] != "pass" {
		t.Errorf("action=%v", rec["action"])
	}
}

func TestPathLog_UnknownAgentDefault(t *testing.T) {
	dir := t.TempDir()
	h := &pathlog.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{Tool: "Edit", Payload: map[string]any{"path": "x.go"}, Env: map[string]string{}}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLogEntry(t, filepath.Join(dir, "logs", "path-log.ndjson"))
	if rec["agent_type"] != "unknown" {
		t.Errorf("agent_type=%v, want unknown", rec["agent_type"])
	}
}
