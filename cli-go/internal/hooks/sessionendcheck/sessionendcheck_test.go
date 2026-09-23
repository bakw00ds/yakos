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

// makeInput builds a HookInput with sessionID as the top-level .session_id
// Payload field, matching hi_session_id.
//
// S-6 A-2a: previously set the CLAUDE_SESSION_ID env var, which bash's
// session-end-check.sh never reads.
func makeInput(sessionID string) hooktype.HookInput {
	payload := map[string]any{}
	if sessionID != "" {
		payload["session_id"] = sessionID
	}
	return hooktype.HookInput{
		Payload: payload,
		Env:     map[string]string{},
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
	// S-6 A-2a: bash builds decisions_stale via jq --arg (always a JSON
	// string "true"/"false"), not --argjson (a boolean).
	if rec["decisions_stale"] != "true" {
		t.Errorf("decisions_stale=%v, want \"true\"", rec["decisions_stale"])
	}
}

func TestSessionEndCheck_NoDecisionsFileNoStale(t *testing.T) {
	work, _ := setupWork(t)
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-6"))
	rec := readLastLog(t, filepath.Join(work, "logs", "session-end-check.ndjson"))
	if rec["decisions_stale"] != "false" {
		t.Errorf("decisions_stale=%v, want \"false\" when no decisions.md", rec["decisions_stale"])
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

// ---- team_name / scratchpad_size_bytes / team-inbox snapshot -----------------
// S-6 A-2a round 2 review finding 1 (audit-data-loss, blocking): these three
// fields/behaviors were previously hardcoded ("", 0) or entirely
// unimplemented. Fixtures below build a session with a team_created
// history record, live scratchpad content, and a live team inbox file —
// exactly the setup the review noted the original two fixtures never
// exercised.

func writeHistoryTeamCreated(t *testing.T, work, sessionID, teamName string) {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"ts": "2026-01-15T09:00:00Z", "event": "team_created",
		"team_name": teamName, "session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("marshal history line: %v", err)
	}
	histFile := filepath.Join(work, ".session-started-history.ndjson")
	if err := os.WriteFile(histFile, append(line, '\n'), 0644); err != nil {
		t.Fatalf("write history: %v", err)
	}
}

func TestSessionEndCheck_TeamNameFromHistory_NotHardcodedEmpty(t *testing.T) {
	work, sessLog := setupWork(t)
	writeHistoryTeamCreated(t, work, "sess-team", "my-team")

	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-team"))

	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("sessions.ndjson not written: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec["team_name"] != "my-team" {
		t.Errorf("team_name=%v, want my-team", rec["team_name"])
	}
}

func TestSessionEndCheck_TeamName_LastMatchWins(t *testing.T) {
	// Mirrors bash's `tail -n 1` over the jq-filtered matches: multiple
	// team_created records for the same session_id resolve to the LAST
	// one's team_name.
	work, sessLog := setupWork(t)
	histFile := filepath.Join(work, ".session-started-history.ndjson")
	lines := ""
	for _, name := range []string{"team-alpha", "team-beta", "team-gamma"} {
		b, _ := json.Marshal(map[string]any{
			"ts": "2026-01-15T09:00:00Z", "event": "team_created",
			"team_name": name, "session_id": "sess-multi",
		})
		lines += string(b) + "\n"
	}
	if err := os.WriteFile(histFile, []byte(lines), 0644); err != nil {
		t.Fatalf("write history: %v", err)
	}

	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-multi"))

	data, _ := os.ReadFile(sessLog)
	var rec map[string]any
	_ = json.Unmarshal(data[:len(data)-1], &rec)
	if rec["team_name"] != "team-gamma" {
		t.Errorf("team_name=%v, want team-gamma (last match)", rec["team_name"])
	}
}

func TestSessionEndCheck_TeamName_NoMatchingHistory_Empty(t *testing.T) {
	work, sessLog := setupWork(t)
	// No history file at all — team_name must stay "" (no crash, no
	// fallback to any other file; bash has none here).
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-none"))

	data, _ := os.ReadFile(sessLog)
	var rec map[string]any
	_ = json.Unmarshal(data[:len(data)-1], &rec)
	if rec["team_name"] != "" {
		t.Errorf("team_name=%v, want empty string", rec["team_name"])
	}
}

func TestSessionEndCheck_ScratchpadSizeBytes_NotHardcodedZero(t *testing.T) {
	work, sessLog := setupWork(t)
	if err := os.WriteFile(filepath.Join(work, "notes.md"), make([]byte, 16384), 0644); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}

	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("sess-sz"))

	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("sessions.ndjson not written: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	size, ok := rec["scratchpad_size_bytes"].(float64)
	if !ok || size <= 0 {
		t.Errorf("scratchpad_size_bytes=%v, want > 0", rec["scratchpad_size_bytes"])
	}
}

func TestSessionEndCheck_TeamInboxSnapshot_CopiesFiles(t *testing.T) {
	work, _ := setupWork(t)
	writeHistoryTeamCreated(t, work, "sess-inbox", "snap-team")

	teamsDir := t.TempDir()
	inboxSrc := filepath.Join(teamsDir, "snap-team", "inboxes")
	if err := os.MkdirAll(inboxSrc, 0755); err != nil {
		t.Fatalf("mkdir inboxSrc: %v", err)
	}
	leadInbox := []byte(`{"messages":[{"from":"researcher","summary":"hi"}]}`)
	if err := os.WriteFile(filepath.Join(inboxSrc, "lead.json"), leadInbox, 0644); err != nil {
		t.Fatalf("write lead.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxSrc, "researcher.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("write researcher.json: %v", err)
	}
	// A non-.json file must be ignored.
	if err := os.WriteFile(filepath.Join(inboxSrc, "README"), []byte("ignore me"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow, TeamsDir: teamsDir}
	_, _ = h.Run(context.Background(), makeInput("sess-inbox"))

	dst := filepath.Join(work, "team-inboxes")
	got, err := os.ReadFile(filepath.Join(dst, "lead.json"))
	if err != nil {
		t.Fatalf("team-inboxes/lead.json not written: %v", err)
	}
	if string(got) != string(leadInbox) {
		t.Errorf("lead.json content mismatch: got %s", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "researcher.json")); err != nil {
		t.Errorf("team-inboxes/researcher.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "README")); err == nil {
		t.Error("non-.json file should not be snapshotted")
	}
}

func TestSessionEndCheck_TeamInboxSnapshot_EmitsReportLog(t *testing.T) {
	// Matches bash's ho_log call for the snapshot: this becomes a SECOND
	// record in session-end-check.ndjson (after the audit-log entry Run
	// always writes first), so it's also the LAST record — the shape the
	// bash-vs-Go parity harness's "tail -n 1" log comparison checks.
	work, _ := setupWork(t)
	writeHistoryTeamCreated(t, work, "sess-inbox2", "snap-team-2")

	teamsDir := t.TempDir()
	inboxSrc := filepath.Join(teamsDir, "snap-team-2", "inboxes")
	_ = os.MkdirAll(inboxSrc, 0755)
	_ = os.WriteFile(filepath.Join(inboxSrc, "lead.json"), []byte(`{}`), 0644)

	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow, TeamsDir: teamsDir}
	_, _ = h.Run(context.Background(), makeInput("sess-inbox2"))

	rec := readLastLog(t, filepath.Join(work, "logs", "session-end-check.ndjson"))
	if rec["reason"] != "snapshotted 1 inbox file(s) from team snap-team-2" {
		t.Errorf("reason=%v", rec["reason"])
	}
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v, want REPORT", rec["severity"])
	}
	if rec["session_id"] != "sess-inbox2" {
		t.Errorf("session_id=%v, want sess-inbox2 (ho_log always carries the caller's session_id)", rec["session_id"])
	}
}

func TestSessionEndCheck_TeamInboxSnapshot_NoTeamName_NoOp(t *testing.T) {
	work, _ := setupWork(t)
	// No history — team_name resolves to "" — snapshot must no-op even if
	// TeamsDir happens to be set.
	teamsDir := t.TempDir()
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow, TeamsDir: teamsDir}
	_, _ = h.Run(context.Background(), makeInput("sess-no-team"))

	if _, err := os.Stat(filepath.Join(work, "team-inboxes")); err == nil {
		t.Error("team-inboxes/ should not be created when team_name is empty")
	}
}

func TestSessionEndCheck_TeamInboxSnapshot_NoInboxDir_NoOp(t *testing.T) {
	work, _ := setupWork(t)
	writeHistoryTeamCreated(t, work, "sess-noinbox", "ghost-team")
	teamsDir := t.TempDir() // ghost-team/inboxes never created
	h := &sessionendcheck.Hook{WorkCurrentDir: work, NowFn: fixedNow, TeamsDir: teamsDir}
	_, _ = h.Run(context.Background(), makeInput("sess-noinbox"))

	if _, err := os.Stat(filepath.Join(work, "team-inboxes")); err == nil {
		t.Error("team-inboxes/ should not be created when the source inbox dir doesn't exist")
	}
}
