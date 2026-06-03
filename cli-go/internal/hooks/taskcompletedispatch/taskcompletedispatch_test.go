package taskcompletedispatch_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/taskcompletedispatch"
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
		t.Fatal("no log entries found")
	}
	return last
}

func newInput(agentRole string) hooktype.HookInput {
	env := map[string]string{}
	if agentRole != "" {
		env["YAKOS_AGENT_ROLE"] = agentRole
	}
	return hooktype.HookInput{Payload: map[string]any{}, Env: env}
}

func TestTaskCompleteDispatch_ReportOnly(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("backend"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", out.ExitCode)
	}
}

func TestTaskCompleteDispatch_ModeReportOnly(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("backend"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["mode"] != "report-only" {
		t.Errorf("mode=%v", rec["mode"])
	}
}

func TestTaskCompleteDispatch_BackendRoute(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("backend"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["routed_domain"] != "backend" {
		t.Errorf("routed_domain=%v", rec["routed_domain"])
	}
}

func TestTaskCompleteDispatch_FrontendRoute(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("nextjs"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["routed_domain"] != "frontend" {
		t.Errorf("routed_domain=%v, want frontend", rec["routed_domain"])
	}
}

func TestTaskCompleteDispatch_MobileRoute(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("flutter-ui"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["routed_domain"] != "mobile" {
		t.Errorf("routed_domain=%v, want mobile", rec["routed_domain"])
	}
}

func TestTaskCompleteDispatch_DBMigrationsRoute(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("db-migrations"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["routed_domain"] != "db-migration" {
		t.Errorf("routed_domain=%v, want db-migration", rec["routed_domain"])
	}
}

func TestTaskCompleteDispatch_UnknownAgentEmptyDomain(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("foobar"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["routed_domain"] != "" {
		t.Errorf("routed_domain=%v, want empty", rec["routed_domain"])
	}
}

func TestTaskCompleteDispatch_WouldRunPath(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow, HooksDirHint: "lib/hooks/per-domain"}
	_, _ = h.Run(context.Background(), newInput("backend"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	wouldRun, _ := rec["would_run"].(string)
	if wouldRun == "" {
		t.Error("expected would_run to be set for known domain")
	}
}

func TestTaskCompleteDispatch_BypassInactive(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("backend"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["bypass_active"] != false {
		t.Errorf("bypass_active=%v, want false", rec["bypass_active"])
	}
}

func TestTaskCompleteDispatch_BypassActive(t *testing.T) {
	dir := t.TempDir()
	// Write a bypass entry.
	bypassContent := "## bypass: abc\nHook: task-complete-dispatch\nScope: backend\n"
	_ = os.WriteFile(filepath.Join(dir, "hook-bypass.md"), []byte(bypassContent), 0644)

	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("backend"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["bypass_active"] != true {
		t.Errorf("bypass_active=%v, want true", rec["bypass_active"])
	}
}

func TestTaskCompleteDispatch_NoWorkDir(t *testing.T) {
	h := &taskcompletedispatch.Hook{WorkCurrentDir: "", NowFn: fixedNow}
	out, err := h.Run(context.Background(), newInput("backend"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d", out.ExitCode)
	}
}

func TestTaskCompleteDispatch_APIAlias(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("api"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["routed_domain"] != "backend" {
		t.Errorf("routed_domain=%v", rec["routed_domain"])
	}
}

func TestTaskCompleteDispatch_GoAPIAlias(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("go-api"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["routed_domain"] != "backend" {
		t.Errorf("routed_domain=%v", rec["routed_domain"])
	}
}

func TestTaskCompleteDispatch_HookName(t *testing.T) {
	h := taskcompletedispatch.New("/tmp")
	if h.Name() != "task-complete-dispatch" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestTaskCompleteDispatch_TimestampPresent(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("backend"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["ts"] != "2026-01-15T10:00:00Z" {
		t.Errorf("ts=%v", rec["ts"])
	}
}

func TestTaskCompleteDispatch_SeverityReport(t *testing.T) {
	dir := t.TempDir()
	h := &taskcompletedispatch.Hook{WorkCurrentDir: dir, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), newInput("backend"))
	rec := readLastLog(t, filepath.Join(dir, "logs", "task-complete-dispatch.ndjson"))
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v", rec["severity"])
	}
}
