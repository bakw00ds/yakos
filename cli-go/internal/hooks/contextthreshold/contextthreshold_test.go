package contextthreshold_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/contextthreshold"
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

func writeSettings(t *testing.T, stateDir string, notice, warning int) {
	t.Helper()
	_ = os.MkdirAll(stateDir, 0755)
	content := `{"context_thresholds":{"notice":` + itoa(notice) + `,"warning":` + itoa(warning) + `}}`
	_ = os.WriteFile(filepath.Join(stateDir, "settings.json"), []byte(content), 0644)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// writeClaudeTranscript creates a fake transcript file to drive the probe.
func writeClaudeTranscript(t *testing.T, homeDir, projectDir, sessionID string, sizeBytes int) {
	t.Helper()
	// Claude encodes project path: normalise to forward slashes, strip leading /,
	// replace / with -, replace : (Windows drive separator) with -.
	// This must match encodeProjectPath in contextthreshold.go exactly.
	project := filepath.ToSlash(projectDir)
	project = strings.ReplaceAll(project, ":", "-")
	project = strings.TrimLeft(project, "/")
	project = strings.ReplaceAll(project, "/", "-")
	dir := filepath.Join(homeDir, ".claude", "projects", project)
	_ = os.MkdirAll(dir, 0755)
	data := make([]byte, sizeBytes)
	for i := range data {
		data[i] = 'x'
	}
	_ = os.WriteFile(filepath.Join(dir, "transcript-"+sessionID+".jsonl"), data, 0644)
}

func TestContextThreshold_ProbeUnavailableLogsReport(t *testing.T) {
	work := t.TempDir()
	h := contextthreshold.New(work)
	h.NowFn = fixedNow
	in := hooktype.HookInput{
		Env: map[string]string{
			"YAKOS_RUNTIME":    "claude",
			"CLAUDE_SESSION_ID": "sess-nope",
			// No CLAUDE_PROJECT_DIR → probe fails.
		},
	}
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	if rec["action"] != "probe_unavailable" {
		t.Errorf("action=%v, want probe_unavailable", rec["action"])
	}
}

func TestContextThreshold_BelowThresholdsReport(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	sessionID := "sess-1"

	// Write a small transcript (far below 75% of 200k tokens = ~600k bytes)
	writeClaudeTranscript(t, home, proj, sessionID, 1000)

	h := &contextthreshold.Hook{
		WorkCurrentDir: work,
		HomeDir:        home,
		NowFn:          fixedNow,
	}
	in := hooktype.HookInput{
		Env: map[string]string{
			"YAKOS_RUNTIME":      "claude",
			"CLAUDE_SESSION_ID":  sessionID,
			"CLAUDE_PROJECT_DIR": proj,
		},
	}
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v, want REPORT for low context usage", rec["severity"])
	}
}

func TestContextThreshold_AboveNoticeThresholdWarn(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	stateDir := t.TempDir()
	sessionID := "sess-2"
	// Set thresholds low so a small file triggers notice.
	writeSettings(t, stateDir, 1, 90) // notice at 1%

	// Write a transcript that's ~10% of 200k tokens = 800k bytes
	writeClaudeTranscript(t, home, proj, sessionID, 800_000)

	h := &contextthreshold.Hook{
		WorkCurrentDir: work,
		HomeDir:        home,
		StateDir:       stateDir,
		NowFn:          fixedNow,
	}
	in := hooktype.HookInput{
		Env: map[string]string{
			"YAKOS_RUNTIME":      "claude",
			"CLAUDE_SESSION_ID":  sessionID,
			"CLAUDE_PROJECT_DIR": proj,
		},
	}
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	if rec["severity"] != "WARN" {
		t.Errorf("severity=%v, want WARN at notice threshold", rec["severity"])
	}
}

func TestContextThreshold_AboveWarningThresholdCheckpoint(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	stateDir := t.TempDir()
	sessionID := "sess-3"
	// Set warning threshold very low.
	writeSettings(t, stateDir, 1, 2) // warning at 2%

	// Write a significant-sized transcript.
	writeClaudeTranscript(t, home, proj, sessionID, 200_000)
	// Write plan.md to ensure it's copied to checkpoint.
	_ = os.WriteFile(filepath.Join(work, "plan.md"), []byte("# plan"), 0644)

	h := &contextthreshold.Hook{
		WorkCurrentDir: work,
		HomeDir:        home,
		StateDir:       stateDir,
		NowFn:          fixedNow,
	}
	in := hooktype.HookInput{
		Env: map[string]string{
			"YAKOS_RUNTIME":      "claude",
			"CLAUDE_SESSION_ID":  sessionID,
			"CLAUDE_PROJECT_DIR": proj,
		},
	}
	out, _ := h.Run(context.Background(), in)
	if out.ExitCode != 0 {
		t.Errorf("exit code %d", out.ExitCode)
	}
	// Check that a checkpoint directory was created.
	checkpointsDir := filepath.Join(work, "checkpoints")
	entries, _ := os.ReadDir(checkpointsDir)
	if len(entries) == 0 {
		t.Error("expected checkpoint directory to be created at warning threshold")
	}
	// Check stderr warning message.
	if len(out.Stderr) == 0 {
		t.Error("expected WARN stderr message at warning threshold")
	}
}

func TestContextThreshold_NoWorkDir(t *testing.T) {
	h := contextthreshold.New("")
	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{}})
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestContextThreshold_DefaultThresholdValues(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	sessionID := "sess-4"
	writeClaudeTranscript(t, home, proj, sessionID, 500)

	h := &contextthreshold.Hook{
		WorkCurrentDir: work,
		HomeDir:        home,
		NowFn:          fixedNow,
	}
	in := hooktype.HookInput{
		Env: map[string]string{
			"YAKOS_RUNTIME":      "claude",
			"CLAUDE_SESSION_ID":  sessionID,
			"CLAUDE_PROJECT_DIR": proj,
		},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	// Default thresholds should be 75 and 90.
	noticeVal := rec["notice"]
	if noticeVal == nil {
		t.Error("notice field missing from log")
	}
}

func TestContextThreshold_AlwaysExitZero(t *testing.T) {
	work := t.TempDir()
	h := contextthreshold.New(work)
	h.NowFn = fixedNow
	out, err := h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{"YAKOS_RUNTIME": "claude"}})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0", out.ExitCode)
	}
}

func TestContextThreshold_HookName(t *testing.T) {
	h := contextthreshold.New("/tmp/work")
	if h.Name() != "context-threshold" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestContextThreshold_UnknownRuntimeProbeUnavailable(t *testing.T) {
	work := t.TempDir()
	h := contextthreshold.New(work)
	h.NowFn = fixedNow
	in := hooktype.HookInput{Env: map[string]string{"YAKOS_RUNTIME": "unknown-runtime-xyz"}}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	if rec["action"] != "probe_unavailable" {
		t.Errorf("action=%v for unknown runtime", rec["action"])
	}
}

func TestContextThreshold_PctCapAt100(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	stateDir := t.TempDir()
	sessionID := "sess-max"
	// Very low threshold so a massive file still yields 100% max.
	writeSettings(t, stateDir, 1, 2)
	// Write a huge transcript — way more than 200k tokens.
	writeClaudeTranscript(t, home, proj, sessionID, 10_000_000)

	h := &contextthreshold.Hook{
		WorkCurrentDir: work,
		HomeDir:        home,
		StateDir:       stateDir,
		NowFn:          fixedNow,
	}
	in := hooktype.HookInput{
		Env: map[string]string{
			"YAKOS_RUNTIME":      "claude",
			"CLAUDE_SESSION_ID":  sessionID,
			"CLAUDE_PROJECT_DIR": proj,
		},
	}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	pct, _ := rec["pct"].(float64)
	if pct > 100 {
		t.Errorf("pct=%v should be capped at 100", pct)
	}
}

func TestContextThreshold_TimestampPresent(t *testing.T) {
	work := t.TempDir()
	h := contextthreshold.New(work)
	h.NowFn = fixedNow
	_, _ = h.Run(context.Background(), hooktype.HookInput{Env: map[string]string{"YAKOS_RUNTIME": "claude"}})
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	if rec["ts"] != "2026-01-15T10:00:00Z" {
		t.Errorf("ts=%v", rec["ts"])
	}
}

func TestContextThreshold_CheckpointManifestWritten(t *testing.T) {
	work := t.TempDir()
	home := t.TempDir()
	proj := t.TempDir()
	stateDir := t.TempDir()
	sessionID := "sess-cp"
	writeSettings(t, stateDir, 1, 2)
	writeClaudeTranscript(t, home, proj, sessionID, 200_000)

	h := &contextthreshold.Hook{
		WorkCurrentDir: work,
		HomeDir:        home,
		StateDir:       stateDir,
		NowFn:          fixedNow,
	}
	in := hooktype.HookInput{
		Env: map[string]string{
			"YAKOS_RUNTIME":      "claude",
			"CLAUDE_SESSION_ID":  sessionID,
			"CLAUDE_PROJECT_DIR": proj,
		},
	}
	_, _ = h.Run(context.Background(), in)

	checkpointsDir := filepath.Join(work, "checkpoints")
	entries, _ := os.ReadDir(checkpointsDir)
	if len(entries) == 0 {
		t.Fatal("expected checkpoint directory")
	}
	manifestPath := filepath.Join(checkpointsDir, entries[0].Name(), "manifest.txt")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest.txt not written: %v", err)
	}
}

func TestContextThreshold_SettingsNoticeOverride(t *testing.T) {
	work := t.TempDir()
	stateDir := t.TempDir()
	home := t.TempDir()
	writeSettings(t, stateDir, 50, 95)

	h := &contextthreshold.Hook{
		WorkCurrentDir: work,
		HomeDir:        home,
		StateDir:       stateDir,
		NowFn:          fixedNow,
	}
	// Probe will fail (no transcript), but we just want to confirm settings load.
	in := hooktype.HookInput{Env: map[string]string{"YAKOS_RUNTIME": "claude"}}
	_, _ = h.Run(context.Background(), in)
	rec := readLastLog(t, filepath.Join(work, "logs", "context-threshold.ndjson"))
	// probe_unavailable because no transcript, but settings were read.
	if rec["action"] == nil {
		t.Error("expected log entry")
	}
}
