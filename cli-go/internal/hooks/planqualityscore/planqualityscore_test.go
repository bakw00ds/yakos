package planqualityscore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/planqualityscore"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(workDir, projectDir, logPath string) *planqualityscore.Hook {
	return &planqualityscore.Hook{
		WorkCurrentDir: workDir,
		ProjectDir:     projectDir,
		PlanQualityLog: logPath,
		NowFn:          fixedNow,
	}
}

func makeInput(event, tool, filePath string, env map[string]string) hooktype.HookInput {
	if env == nil {
		env = map[string]string{}
	}
	payload := map[string]any{}
	if filePath != "" {
		payload["path"] = filePath
	}
	return hooktype.HookInput{
		Event:   event,
		Tool:    tool,
		Payload: payload,
		Env:     env,
	}
}

func writeYAML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
}

func writeScoredRecord(t *testing.T, logPath string, planID string, aggregate float64, dissent bool) {
	t.Helper()
	rec := map[string]any{
		"type":            "plan_scored",
		"plan_id":         planID,
		"aggregate_score": aggregate,
		"threshold":       0.75,
		"verdict":         "pass",
		"dissent":         dissent,
		"panel_size":      3,
	}
	data, _ := json.Marshal(rec)
	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(data, '\n'))
}

// TestPostToolUseNonPlanFileIgnored confirms non-plan.md writes are ignored.
func TestPostToolUseNonPlanFileIgnored(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp, "")
	in := makeInput("PostToolUse", "Edit", "/some/other/file.md", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("non-plan file should pass, got exit %d", out.ExitCode)
	}
}

// TestPostToolUseAboveThresholdPasses confirms pass when aggregate >= threshold.
func TestPostToolUseAboveThresholdPasses(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-pass", 0.85, false)
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Edit",
		filepath.Join(tmp, "work/current/plan.md"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected pass, got exit %d", out.ExitCode)
	}
	// No .plan-blocked marker.
	if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err == nil {
		t.Fatalf("should not write .plan-blocked when passing")
	}
}

// TestPostToolUseBelowThresholdSurfaceMode writes notes but no block marker.
func TestPostToolUseBelowThresholdSurfaceMode(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-low", 0.60, false)
	writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: surface\n  threshold: 0.75\n")
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Write",
		filepath.Join(tmp, "work/current/plan.md"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("surface mode should not block, got exit %d", out.ExitCode)
	}
	// No .plan-blocked.
	if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err == nil {
		t.Fatalf("surface mode should not write .plan-blocked")
	}
	// Notes file should exist.
	entries, _ := os.ReadDir(filepath.Join(tmp, "notes"))
	if len(entries) == 0 {
		t.Fatalf("expected notes file in surface mode")
	}
}

// TestPostToolUseBelowThresholdBlockMode writes .plan-blocked.
func TestPostToolUseBelowThresholdBlockMode(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-block", 0.55, false)
	writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Write",
		filepath.Join(tmp, "work/current/plan.md"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("PostToolUse never blocks; got exit %d", out.ExitCode)
	}
	// .plan-blocked should exist.
	if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err != nil {
		t.Fatalf("expected .plan-blocked to be written in block mode")
	}
}

// TestPostToolUseDissent surfaces but doesn't block regardless of mode.
func TestPostToolUseDissent(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-dissent", 0.80, true) // dissent=true
	writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Write",
		filepath.Join(tmp, "work/current/plan.md"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("dissent path should not block, got exit %d", out.ExitCode)
	}
	// No .plan-blocked on dissent (dissent always surfaces).
	if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err == nil {
		t.Fatalf("should not write .plan-blocked on dissent")
	}
}

// TestPostToolUseNoLogRecordPasses passes conservatively when no scored record.
func TestPostToolUseNoLogRecordPasses(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "empty.ndjson")
	_ = os.WriteFile(logPath, []byte{}, 0644)
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Write",
		filepath.Join(tmp, "work/current/plan.md"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected conservative pass, got exit %d", out.ExitCode)
	}
}

// TestPlanBlockedMarkerContainsPlanID confirms marker JSON has plan_id.
func TestPlanBlockedMarkerContainsPlanID(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-mId", 0.40, false)
	writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Write",
		fmt.Sprintf("%s/work/current/plan.md", tmp), nil)
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	markerPath := filepath.Join(tmp, ".plan-blocked")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}
	var marker map[string]any
	if err := json.Unmarshal(data, &marker); err != nil {
		// trim the trailing newline from atomic write.
		if err2 := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &marker); err2 != nil {
			t.Fatalf("unmarshal marker: %v (raw: %s)", err2, data)
		}
	}
	if marker["plan_id"] != "plan-mId" {
		t.Fatalf("expected plan_id=plan-mId in marker, got %v", marker["plan_id"])
	}
}

// TestWriteToolAlsoCovered confirms Write tool is handled in PostToolUse.
func TestWriteToolAlsoCovered(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-w", 0.90, false)
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Write",
		filepath.Join(tmp, "work/current/plan.md"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected pass, got %d", out.ExitCode)
	}
}

// TestUnknownEventNoOp confirms unknown events are ignored.
func TestUnknownEventNoOp(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp, "")
	in := makeInput("UserPromptSubmit", "", "", nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("unknown event should pass, got %d", out.ExitCode)
	}
}

// TestMultipleLogRecordsLastWins confirms the last plan_scored record is used.
func TestMultipleLogRecordsLastWins(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "pq.ndjson")
	writeScoredRecord(t, logPath, "plan-first", 0.90, false) // passes threshold
	writeScoredRecord(t, logPath, "plan-last", 0.40, false)  // fails threshold
	writeYAML(t, tmp, "plan_quality:\n  enabled: true\n  mode: block\n  threshold: 0.75\n")
	h := newHook(tmp, tmp, logPath)
	in := makeInput("PostToolUse", "Edit",
		filepath.Join(tmp, "work/current/plan.md"), nil)
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// .plan-blocked should be written because last record is below threshold.
	if _, err := os.Stat(filepath.Join(tmp, ".plan-blocked")); err != nil {
		t.Fatalf("expected .plan-blocked for last below-threshold record")
	}
}

// TestPreToolUseNeverGates confirms the scorer ignores PreToolUse dispatches
// even with a marker present: gating is planqualitygate's job (K-81 split).
func TestPreToolUseNeverGates(t *testing.T) {
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, ".plan-blocked"), []byte(`{"plan_id":"p","reason":"bad"}`), 0644)
	h := newHook(tmp, tmp, "")
	for _, tool := range []string{"Agent", "TeamCreate"} {
		out, err := h.Run(context.Background(), makeInput("PreToolUse", tool, "", nil))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.ExitCode != 0 {
			t.Fatalf("scorer must never block PreToolUse %s, got exit %d", tool, out.ExitCode)
		}
	}
}

// TestInfraErrorIsConservativePass pins the conservative posture: an
// unreadable/absent log path is a WARN + pass, never a block.
func TestInfraErrorIsConservativePass(t *testing.T) {
	tmp := t.TempDir()
	h := newHook(tmp, tmp, filepath.Join(tmp, "no-such-dir", "pq.ndjson"))
	in := makeInput("PostToolUse", "Write", filepath.Join(tmp, "work/current/plan.md"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("infra error must pass, got exit %d", out.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(tmp, "logs", "plan-quality-score.ndjson")); err != nil {
		t.Fatalf("expected WARN record in logs/plan-quality-score.ndjson: %v", err)
	}
}

// TestName pins the registry-facing name.
func TestName(t *testing.T) {
	if got := newHook(t.TempDir(), "", "").Name(); got != "plan-quality-score" {
		t.Fatalf("Name()=%q", got)
	}
}
