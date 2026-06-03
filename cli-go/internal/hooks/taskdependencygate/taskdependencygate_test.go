package taskdependencygate_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/taskdependencygate"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func readLastLog(t *testing.T, logFile string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var last map[string]any
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				var rec map[string]any
				if err := json.Unmarshal(data[start:i], &rec); err == nil {
					last = rec
				}
			}
			start = i + 1
		}
	}
	if last == nil {
		t.Fatal("no log entries")
	}
	return last
}

func TestTaskDependencyGate_ReportOnly(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}}
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", out.ExitCode)
	}
}

func TestTaskDependencyGate_ModeIsReportOnly(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}})
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["mode"] != "report-only" {
		t.Errorf("mode=%v, want report-only", rec["mode"])
	}
}

func TestTaskDependencyGate_SeverityReport(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}})
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v", rec["severity"])
	}
}

func TestTaskDependencyGate_TaskIDFromPayload(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Payload: map[string]any{"task_id": "T-42"},
		Env:     map[string]string{},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["task_id"] != "T-42" {
		t.Errorf("task_id=%v", rec["task_id"])
	}
}

func TestTaskDependencyGate_NestedTaskID(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Payload: map[string]any{"task": map[string]any{"id": "T-99"}},
		Env:     map[string]string{},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["task_id"] != "T-99" {
		t.Errorf("task_id=%v", rec["task_id"])
	}
}

func TestTaskDependencyGate_BlockedBySuspect(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Payload: map[string]any{"blockedBy": []any{"T-5", "T-6"}},
		Env:     map[string]string{},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	suspect, _ := rec["suspect_block_reason"].(string)
	if suspect == "" {
		t.Error("expected suspect_block_reason to be set")
	}
}

func TestTaskDependencyGate_WouldBlockUnknown(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}})
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["would_block"] != "unknown" {
		t.Errorf("would_block=%v", rec["would_block"])
	}
}

func TestTaskDependencyGate_AgentTypeLogged(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Payload: map[string]any{},
		Env:     map[string]string{"YAKOS_AGENT_ROLE": "backend"},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["agent_type"] != "backend" {
		t.Errorf("agent_type=%v", rec["agent_type"])
	}
}

func TestTaskDependencyGate_TimestampPresent(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}})
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["ts"] != "2026-01-15T10:00:00Z" {
		t.Errorf("ts=%v", rec["ts"])
	}
}

func TestTaskDependencyGate_NoWorkDir(t *testing.T) {
	h := &taskdependencygate.Hook{WorkCurrentDir: "", NowFn: fixedNow}
	out, err := h.Run(context.Background(), hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d", out.ExitCode)
	}
}

func TestTaskDependencyGate_NullBlockedByNoSuspect(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Payload: map[string]any{"blockedBy": nil},
		Env:     map[string]string{},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if suspect, _ := rec["suspect_block_reason"].(string); suspect != "" {
		t.Errorf("expected no suspect reason for null blockedBy, got: %s", suspect)
	}
}

func TestTaskDependencyGate_EmptyBlockedByNoSuspect(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	in := hooktype.HookInput{
		Payload: map[string]any{"blockedBy": []any{}},
		Env:     map[string]string{},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if suspect, _ := rec["suspect_block_reason"].(string); suspect != "" {
		t.Errorf("expected no suspect reason for empty blockedBy, got: %s", suspect)
	}
}

func TestTaskDependencyGate_HookName(t *testing.T) {
	h := taskdependencygate.New("/tmp")
	if h.Name() != "task-dependency-gate" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestTaskDependencyGate_MultipleCallsAppend(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	for i := 0; i < 4; i++ {
		_, _ = h.Run(context.Background(), hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}})
	}
	data, _ := os.ReadFile(filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	if count != 4 {
		t.Errorf("expected 4 log lines, got %d", count)
	}
}

func TestTaskDependencyGate_HookFieldInLog(t *testing.T) {
	dir := t.TempDir()
	h := &taskdependencygate.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), hooktype.HookInput{Payload: map[string]any{}, Env: map[string]string{}})
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-dependency-gate.ndjson"))
	if rec["hook"] != "task-dependency-gate" {
		t.Errorf("hook=%v", rec["hook"])
	}
}
