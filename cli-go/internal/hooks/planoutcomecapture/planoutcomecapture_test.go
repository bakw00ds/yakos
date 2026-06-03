package planoutcomecapture_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/planoutcomecapture"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(logPath string) *planoutcomecapture.Hook {
	return &planoutcomecapture.Hook{
		PlanQualityLog: logPath,
		NowFn:          fixedNow,
		GitRunner:      noopGit,
	}
}

// noopGit returns no diff stats (simulates no git activity).
func noopGit(dir string) (int, int, error) {
	return 0, 0, nil
}

// fakeGit returns fixed file/line counts.
func fakeGitWith(files, lines int) func(string) (int, int, error) {
	return func(dir string) (int, int, error) {
		return files, lines, nil
	}
}

func makeInput(env map[string]string) hooktype.HookInput {
	if env == nil {
		env = map[string]string{}
	}
	return hooktype.HookInput{
		Event:   "SessionEnd",
		Payload: map[string]any{},
		Env:     env,
	}
}

func writeLine(t *testing.T, logPath string, rec map[string]any) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readLastRecord(t *testing.T, logPath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var last map[string]any
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			last = rec
		}
	}
	return last
}

// TestAlwaysExitZero confirms the hook never blocks.
func TestAlwaysExitZero(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	h := newHook(logPath)
	out, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
}

// TestNoLogFile passes gracefully when log doesn't exist.
func TestNoLogFile(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "nonexistent.ndjson")
	h := newHook(logPath)
	out, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
}

// TestAllPlansHaveOutcomes passes when every scored plan has an outcome.
func TestAllPlansHaveOutcomes(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-001"})
	writeLine(t, logPath, map[string]any{"type": "plan_outcome", "plan_id": "plan-001"})
	h := newHook(logPath)
	out, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
}

// TestCapturesUnmatchedPlan appends a plan_outcome for an unmatched plan.
func TestCapturesUnmatchedPlan(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-abc"})
	h := newHook(logPath)
	out, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	rec := readLastRecord(t, logPath)
	if rec["type"] != "plan_outcome" {
		t.Fatalf("expected type=plan_outcome, got %v", rec["type"])
	}
	if rec["plan_id"] != "plan-abc" {
		t.Fatalf("expected plan_id=plan-abc, got %v", rec["plan_id"])
	}
	if rec["capture_method"] != "session_end_fallback" {
		t.Fatalf("expected capture_method=session_end_fallback, got %v", rec["capture_method"])
	}
}

// TestLastUnmatchedPlanChosen picks the last scored plan without an outcome.
func TestLastUnmatchedPlanChosen(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-001"})
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-002"})
	writeLine(t, logPath, map[string]any{"type": "plan_outcome", "plan_id": "plan-001"})
	// plan-002 has no outcome.
	h := newHook(logPath)
	_, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := readLastRecord(t, logPath)
	if rec["plan_id"] != "plan-002" {
		t.Fatalf("expected plan_id=plan-002, got %v", rec["plan_id"])
	}
}

// TestGitStatsCaptured includes files_touched and lines_changed when git works.
func TestGitStatsCaptured(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-xyz"})
	h := newHook(logPath)
	h.GitRunner = fakeGitWith(7, 150)
	_, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := readLastRecord(t, logPath)
	if ft, ok := rec["files_touched"].(float64); !ok || int(ft) != 7 {
		t.Fatalf("expected files_touched=7, got %v", rec["files_touched"])
	}
	if lc, ok := rec["lines_changed"].(float64); !ok || int(lc) != 150 {
		t.Fatalf("expected lines_changed=150, got %v", rec["lines_changed"])
	}
}

// TestGitFailsGracefully passes when git fails.
func TestGitFailsGracefully(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-xyz"})
	h := newHook(logPath)
	h.GitRunner = func(dir string) (int, int, error) {
		return -1, -1, os.ErrNotExist
	}
	out, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
}

// TestDispatchLogStats reads token and cost sums from dispatch log.
func TestDispatchLogStats(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	dispatchPath := filepath.Join(tmp, "dispatch-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-d1"})

	// Write 2 dispatch entries.
	for _, rec := range []map[string]any{
		{"tokens_total": 1000, "cost_usd": 0.05},
		{"tokens_total": 2000, "cost_usd": 0.10},
	} {
		writeLine(t, dispatchPath, rec)
	}

	h := &planoutcomecapture.Hook{
		PlanQualityLog: logPath,
		DispatchLog:    dispatchPath,
		NowFn:          fixedNow,
		GitRunner:      noopGit,
	}
	_, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := readLastRecord(t, logPath)
	if tt, ok := rec["tokens_spent_total"].(float64); !ok || int(tt) != 3000 {
		t.Fatalf("expected tokens_spent_total=3000, got %v", rec["tokens_spent_total"])
	}
}

// TestHumanSignalNull confirms human_signal is always null.
func TestHumanSignalNull(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-hs"})
	h := newHook(logPath)
	_, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := readLastRecord(t, logPath)
	if _, ok := rec["human_signal"]; !ok {
		t.Fatalf("expected human_signal key in record")
	}
	if rec["human_signal"] != nil {
		t.Fatalf("expected human_signal=null, got %v", rec["human_signal"])
	}
}

// TestReworkFailureModesEmpty confirms rework_failure_modes is always [].
func TestReworkFailureModesEmpty(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-rf"})
	h := newHook(logPath)
	_, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := readLastRecord(t, logPath)
	modes, ok := rec["rework_failure_modes"].([]interface{})
	if !ok {
		t.Fatalf("expected rework_failure_modes array, got %T: %v", rec["rework_failure_modes"], rec["rework_failure_modes"])
	}
	if len(modes) != 0 {
		t.Fatalf("expected empty rework_failure_modes, got %v", modes)
	}
}

// TestLogPathFromEnv uses YAKOS_PLAN_QUALITY_LOG env var.
func TestLogPathFromEnv(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "env-plan-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-env"})
	h := &planoutcomecapture.Hook{
		NowFn:     fixedNow,
		GitRunner: noopGit,
	}
	in := makeInput(map[string]string{
		"YAKOS_PLAN_QUALITY_LOG": logPath,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d", out.ExitCode)
	}
	rec := readLastRecord(t, logPath)
	if rec["type"] != "plan_outcome" {
		t.Fatalf("expected plan_outcome record, got %v", rec["type"])
	}
}

// TestTimestampFormatted confirms ts uses the expected format.
func TestTimestampFormatted(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": "plan-ts"})
	h := newHook(logPath)
	_, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec := readLastRecord(t, logPath)
	ts, ok := rec["ts"].(string)
	if !ok {
		t.Fatalf("expected ts string, got %T", rec["ts"])
	}
	if ts != "2026-01-15T10:00:00Z" {
		t.Fatalf("expected fixed timestamp, got %s", ts)
	}
}

// TestMultipleScoredAllMatched skips capture when all have outcomes.
func TestMultipleScoredAllMatched(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "plan-quality-log.ndjson")
	for _, id := range []string{"p1", "p2", "p3"} {
		writeLine(t, logPath, map[string]any{"type": "plan_scored", "plan_id": id})
		writeLine(t, logPath, map[string]any{"type": "plan_outcome", "plan_id": id})
	}
	linesBefore := countLines(t, logPath)
	h := newHook(logPath)
	_, err := h.Run(context.Background(), makeInput(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	linesAfter := countLines(t, logPath)
	if linesAfter != linesBefore {
		t.Fatalf("expected no new lines (all matched), got %d extra", linesAfter-linesBefore)
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}
