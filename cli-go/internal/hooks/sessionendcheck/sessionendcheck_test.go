package sessionendcheck_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/sessionendcheck"
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

func makeInput(sessionID string) hooktype.HookInput {
	return hooktype.HookInput{
		Payload: map[string]any{},
		Env:     map[string]string{"CLAUDE_SESSION_ID": sessionID},
	}
}

// workCurrentDir sets up a work/current structure and returns it.
// The sessions.ndjson lives in the parent dir (work/).
func setupWork(t *testing.T) (workDir, sessionsLog string) {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work", "current")
	_ = os.MkdirAll(work, 0755)
	_ = os.MkdirAll(filepath.Join(work, "logs"), 0755)
	sessions := filepath.Join(base, "work", "sessions.ndjson")
	return work, sessions
}

func TestSessionEndCheck_AlwaysExitZero(t *testing.T) {
	work, _ := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	out, err := h.Run(context.Background(), makeInput("sess-1"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestSessionEndCheck_WritesAuditLog(t *testing.T) {
	work, _ := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-1"))
	if _, err := os.Stat(filepath.Join(work, "logs", "session-end-check.ndjson")); err != nil {
		t.Errorf("audit log not written: %v", err)
	}
}

func TestSessionEndCheck_AuditSeverityReport(t *testing.T) {
	work, _ := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-1"))
	rec := readLastLog(t, filepath.Join(work, "logs", "session-end-check.ndjson"))
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v, want REPORT (no issues)", rec["severity"])
	}
}

func TestSessionEndCheck_WritesSummaryToSessionsLog(t *testing.T) {
	work, sessLog := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-2"))
	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("sessions.ndjson not written: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &rec); err != nil {
		t.Fatalf("unmarshal sessions.ndjson: %v", err)
	}
	if rec["exit_kind"] != "session_end_without_team_delete" {
		t.Errorf("exit_kind=%v", rec["exit_kind"])
	}
}

func TestSessionEndCheck_SummaryIdempotent(t *testing.T) {
	work, sessLog := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-3"))
	_, _ = h.Run(context.Background(), makeInput("sess-3")) // second call
	data, _ := os.ReadFile(sessLog)
	// Count lines.
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 session summary, got %d", count)
	}
}

func TestSessionEndCheck_SummarizedMarkerSkipsSummary(t *testing.T) {
	work, sessLog := setupWork(t)
	// Pre-create the marker (team-lifecycle would have done this).
	_ = os.WriteFile(filepath.Join(work, ".session-summarized"), []byte{}, 0644)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-4"))
	if _, err := os.Stat(sessLog); !os.IsNotExist(err) {
		t.Error("sessions.ndjson should NOT be written when marker exists")
	}
	// Marker should be removed.
	if _, err := os.Stat(filepath.Join(work, ".session-summarized")); !os.IsNotExist(err) {
		t.Error("marker should have been removed")
	}
}

func TestSessionEndCheck_StaleDecisionsWarn(t *testing.T) {
	work, _ := setupWork(t)
	decisionsFile := filepath.Join(work, "decisions.md")
	_ = os.WriteFile(decisionsFile, []byte("# decisions"), 0644)
	// Set mtime to fixedTime - 3 hours, so the hook (using fixedNow) sees it as 3h stale.
	past := fixedTime.Add(-3 * time.Hour)
	_ = os.Chtimes(decisionsFile, past, past)

	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-5"))
	rec := readLastLog(t, filepath.Join(work, "logs", "session-end-check.ndjson"))
	if rec["severity"] != "WARN" {
		t.Errorf("severity=%v, want WARN for stale decisions.md", rec["severity"])
	}
	if rec["decisions_stale"] != true {
		t.Errorf("decisions_stale=%v, want true", rec["decisions_stale"])
	}
}

func TestSessionEndCheck_NoDecisionsFileNoStale(t *testing.T) {
	work, _ := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-6"))
	rec := readLastLog(t, filepath.Join(work, "logs", "session-end-check.ndjson"))
	if rec["decisions_stale"] != false {
		t.Errorf("decisions_stale=%v, want false when no decisions.md", rec["decisions_stale"])
	}
}

func TestSessionEndCheck_HookName(t *testing.T) {
	h := sessionendcheck.New("/tmp/work")
	if h.Name() != "session-end-check" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestSessionEndCheck_NoWorkDir(t *testing.T) {
	h := &sessionendcheck.Hook{WorkCurrentDir: "", NowFn: fixedNow}
	out, err := h.Run(context.Background(), makeInput("sess-7"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestSessionEndCheck_HookCountsInAudit(t *testing.T) {
	work, _ := setupWork(t)
	logsDir := filepath.Join(work, "logs")
	// Write a fake hook log with known BLOCK/WARN/REPORT counts.
	entry1 := map[string]any{"severity": "BLOCK", "ts": "2026-01-15T10:00:00Z"}
	entry2 := map[string]any{"severity": "WARN", "ts": "2026-01-15T10:00:01Z"}
	entry3 := map[string]any{"severity": "REPORT", "ts": "2026-01-15T10:00:02Z"}
	var logData []byte
	for _, e := range []map[string]any{entry1, entry2, entry3} {
		b, _ := json.Marshal(e)
		logData = append(logData, b...)
		logData = append(logData, '\n')
	}
	_ = os.WriteFile(filepath.Join(logsDir, "path-allowlist.ndjson"), logData, 0644)

	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-8"))

	// Find the session-end-check audit entry (not the summary appended by the audit).
	data, _ := os.ReadFile(filepath.Join(logsDir, "session-end-check.ndjson"))
	var first map[string]any
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				var rec map[string]any
				if json.Unmarshal(data[start:i], &rec) == nil {
					first = rec
					break
				}
			}
			start = i + 1
		}
	}
	if first == nil {
		t.Fatal("no audit log entry")
	}
	if first["hook_blocks"] != float64(1) {
		t.Errorf("hook_blocks=%v, want 1", first["hook_blocks"])
	}
}

func TestSessionEndCheck_AuditTimestamp(t *testing.T) {
	work, _ := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-9"))
	rec := readLastLog(t, filepath.Join(work, "logs", "session-end-check.ndjson"))
	if rec["ts"] != "2026-01-15T10:00:00Z" {
		t.Errorf("ts=%v", rec["ts"])
	}
}

func TestSessionEndCheck_SessionIDInSummary(t *testing.T) {
	work, sessLog := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-target"))
	data, _ := os.ReadFile(sessLog)
	var rec map[string]any
	_ = json.Unmarshal(data[:len(data)-1], &rec)
	if rec["session_id"] != "sess-target" {
		t.Errorf("session_id=%v", rec["session_id"])
	}
}

func TestSessionEndCheck_DifferentSessionsDontCollide(t *testing.T) {
	work, sessLog := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-A"))
	_, _ = h.Run(context.Background(), makeInput("sess-B"))
	data, _ := os.ReadFile(sessLog)
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 session summaries for distinct sessions, got %d", count)
	}
}
