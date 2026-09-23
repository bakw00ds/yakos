package teamlifecycle_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/teamlifecycle"
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

// makeInput builds a HookInput matching bash's actual shape: team name
// lives under .tool_input.name (tool_input="$(... | jq -c '.tool_input //
// {}')"; team_name="$(... | jq -r '.name // empty')" reads FROM that nested
// object), and session_id is the top-level .session_id Payload field
// (hi_session_id), not an env var — team-lifecycle.sh never reads
// CLAUDE_SESSION_ID from the environment.
func makeInput(tool, teamName, sessionID string) hooktype.HookInput {
	payload := map[string]any{}
	if teamName != "" {
		payload["tool_input"] = map[string]any{"name": teamName}
	}
	if sessionID != "" {
		payload["session_id"] = sessionID
	}
	return hooktype.HookInput{Tool: tool, Payload: payload, Env: map[string]string{}}
}

func TestTeamLifecycle_NonLifecycleToolSkipped(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	out, err := h.Run(context.Background(), makeInput("Edit", "", ""))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(work, "logs")); !os.IsNotExist(err) {
		t.Error("no logs should be created for non-lifecycle tool")
	}
}

func TestTeamLifecycle_TeamCreateLogsEvent(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, err := h.Run(context.Background(), makeInput("TeamCreate", "my-team", "sess-1"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	rec := readLastLog(t, filepath.Join(work, "logs", "team-lifecycle.ndjson"))
	if rec["event"] != "team_created" {
		t.Errorf("event=%v, want team_created", rec["event"])
	}
}

func TestTeamLifecycle_TeamCreateWritesSessionStarted(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamCreate", "team-1", "sess-1"))
	data, err := os.ReadFile(filepath.Join(work, ".session-started"))
	if err != nil {
		t.Fatalf(".session-started not written: %v", err)
	}
	ts := string(data)
	if len(ts) < 10 {
		t.Errorf(".session-started content too short: %q", ts)
	}
}

func TestTeamLifecycle_TeamCreateWritesHistory(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamCreate", "team-hist", "sess-2"))
	data, err := os.ReadFile(filepath.Join(work, ".session-started-history.ndjson"))
	if err != nil {
		t.Fatalf("history file not written: %v", err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &rec); err != nil {
		t.Fatalf("history parse: %v", err)
	}
	if rec["event"] != "team_created" {
		t.Errorf("event=%v", rec["event"])
	}
	if rec["team_name"] != "team-hist" {
		t.Errorf("team_name=%v", rec["team_name"])
	}
}

func TestTeamLifecycle_AgentLogsSpawn(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), hooktype.HookInput{
		Tool: "Agent",
		Payload: map[string]any{
			"tool_input": map[string]any{"subagent_type": "backend"},
			"session_id": "sess-3",
		},
		Env: map[string]string{},
	})
	rec := readLastLog(t, filepath.Join(work, "logs", "team-lifecycle.ndjson"))
	if rec["event"] != "agent_spawned" {
		t.Errorf("event=%v, want agent_spawned", rec["event"])
	}
}

func TestTeamLifecycle_AgentDoesNotTouchSessionStarted(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), hooktype.HookInput{
		Tool: "Agent",
		Payload: map[string]any{
			"tool_input": map[string]any{"subagent_type": "backend"},
			"session_id": "sess-4",
		},
		Env: map[string]string{},
	})
	if _, err := os.Stat(filepath.Join(work, ".session-started")); !os.IsNotExist(err) {
		t.Error("Agent should not write .session-started")
	}
}

func TestTeamLifecycle_TeamDeleteLogsEvent(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "my-team", "sess-5"))
	rec := readLastLog(t, filepath.Join(work, "logs", "team-lifecycle.ndjson"))
	if rec["event"] != "team_deleted" {
		t.Errorf("event=%v, want team_deleted", rec["event"])
	}
}

func TestTeamLifecycle_TeamDeleteWritesSummarizedMarker(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "team-del", "sess-6"))
	if _, err := os.Stat(filepath.Join(work, ".session-summarized")); err != nil {
		t.Errorf(".session-summarized marker not created: %v", err)
	}
}

func TestTeamLifecycle_TeamDeleteWritesSessionSummary(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "work", "current")
	_ = os.MkdirAll(work, 0755)
	sessLog := filepath.Join(base, "work", "sessions.ndjson")

	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "my-team", "sess-7"))

	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("sessions.ndjson not written: %v", err)
	}
	var rec map[string]any
	_ = json.Unmarshal(data[:len(data)-1], &rec)
	if rec["exit_kind"] != "team_deleted" {
		t.Errorf("exit_kind=%v", rec["exit_kind"])
	}
}

func TestTeamLifecycle_TeamDeleteIdempotent(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "work", "current")
	_ = os.MkdirAll(work, 0755)
	sessLog := filepath.Join(base, "work", "sessions.ndjson")

	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "team-idem", "sess-8"))
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "team-idem", "sess-8"))

	data, _ := os.ReadFile(sessLog)
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 session summary (idempotent), got %d", count)
	}
}

func TestTeamLifecycle_AlwaysExitZero(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	out, err := h.Run(context.Background(), makeInput("TeamCreate", "t", "s"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestTeamLifecycle_HookName(t *testing.T) {
	h := teamlifecycle.New("/tmp/work")
	if h.Name() != "team-lifecycle" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestTeamLifecycle_NoWorkDir(t *testing.T) {
	h := &teamlifecycle.Hook{WorkCurrentDir: "", NowFn: fixedNow}
	out, err := h.Run(context.Background(), makeInput("TeamCreate", "t", "s"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestTeamLifecycle_KanbanMoveNoop_NoKanban(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	// No kanban.md exists — should be a no-op, no error.
	out, err := h.Run(context.Background(), makeInput("TeamCreate", "t", "s"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestTeamLifecycle_KanbanMoveTODOtoInProgress(t *testing.T) {
	work := t.TempDir()
	kanbanContent := `# Kanban — test

## TODO
- [ ] K-1 — do the thing
  - category: feat
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`
	_ = os.WriteFile(filepath.Join(work, "kanban.md"), []byte(kanbanContent), 0644)

	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamCreate", "team-k", "sess-k"))

	data, err := os.ReadFile(filepath.Join(work, "kanban.md"))
	if err != nil {
		t.Fatalf("kanban.md not readable: %v", err)
	}
	content := string(data)
	// The task should now be in IN PROGRESS.
	if indexOf(content, "## IN PROGRESS") < 0 {
		t.Fatal("IN PROGRESS section not found")
	}
	inProgress := content[indexOf(content, "## IN PROGRESS"):]
	if indexOf(inProgress, "K-1") < 0 {
		t.Error("K-1 should be in IN PROGRESS after TeamCreate")
	}
}

func TestTeamLifecycle_KanbanMoveInProgressToDone(t *testing.T) {
	work := t.TempDir()
	kanbanContent := `# Kanban — test

## TODO

## IN PROGRESS
- [-] K-2 — ongoing work
  - category: feat
  - assigned: someone
  - blockers: none
  - notes:

## DONE
`
	_ = os.WriteFile(filepath.Join(work, "kanban.md"), []byte(kanbanContent), 0644)

	// Set up sessions.ndjson parent dir.
	base := filepath.Dir(work)
	_ = os.MkdirAll(base, 0755)

	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "team-kd", "sess-kd"))

	data, err := os.ReadFile(filepath.Join(work, "kanban.md"))
	if err != nil {
		t.Fatalf("kanban.md: %v", err)
	}
	content := string(data)
	doneIdx := indexOf(content, "## DONE")
	if doneIdx < 0 {
		t.Fatal("DONE section not found")
	}
	doneSection := content[doneIdx:]
	if indexOf(doneSection, "K-2") < 0 {
		t.Error("K-2 should be in DONE after TeamDelete")
	}
}

func TestTeamLifecycle_TimestampInLog(t *testing.T) {
	work := t.TempDir()
	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamCreate", "t", "s"))
	rec := readLastLog(t, filepath.Join(work, "logs", "team-lifecycle.ndjson"))
	if rec["ts"] != "2026-01-15T10:00:00Z" {
		t.Errorf("ts=%v", rec["ts"])
	}
}

// indexOf is a helper since strings package isn't imported.
// TestTeamLifecycle_KanbanMoveFreeFormTaskID_NoID confirms kanbanMoveFirst
// moves the first bullet under the source column verbatim, regardless of
// whether it contains a "K-<digits>" token — bash's kanban_move_first is a
// pure line-based awk state machine with no ID-format requirement at all
// (S-6 A-2a round 2 review finding 2). A prior Go port used an ID-based
// internal/kanban.Move call that silently no-op'd for exactly this case.
func TestTeamLifecycle_KanbanMoveFreeFormTaskID_NoID(t *testing.T) {
	work := t.TempDir()
	kanbanContent := `# Kanban — test

## TODO
- [ ] Free-form task with no ID token
  - category: chore
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`
	_ = os.WriteFile(filepath.Join(work, "kanban.md"), []byte(kanbanContent), 0644)

	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamCreate", "team-ff", "sess-ff"))

	data, err := os.ReadFile(filepath.Join(work, "kanban.md"))
	if err != nil {
		t.Fatalf("kanban.md not readable: %v", err)
	}
	content := string(data)
	if indexOf(content, "## IN PROGRESS") < 0 {
		t.Fatal("IN PROGRESS section not found")
	}
	inProgress := content[indexOf(content, "## IN PROGRESS"):]
	if indexOf(inProgress, "Free-form task with no ID token") < 0 {
		t.Error("free-form task (no K-<digits> token) should still move to IN PROGRESS")
	}
	todo := content[indexOf(content, "## TODO"):indexOf(content, "## IN PROGRESS")]
	if indexOf(todo, "Free-form task") >= 0 {
		t.Error("task should no longer appear under TODO after the move")
	}
	// Checkbox character must flip from "[ ]" to "[-]".
	if indexOf(inProgress, "- [-] Free-form task") < 0 {
		t.Error("expected checkbox to flip to '[-]' on the moved task")
	}
}

// TestTeamLifecycle_KanbanMoveFreeFormTaskID_TeamDelete mirrors the above
// for the TeamDelete (IN PROGRESS → DONE, checkbox "x") path.
func TestTeamLifecycle_KanbanMoveFreeFormTaskID_TeamDelete(t *testing.T) {
	work := t.TempDir()
	kanbanContent := `# Kanban — test

## TODO

## IN PROGRESS
- [-] Another free-form task
  - category: chore

## DONE
`
	_ = os.WriteFile(filepath.Join(work, "kanban.md"), []byte(kanbanContent), 0644)
	base := filepath.Dir(work)
	_ = os.MkdirAll(base, 0755)

	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "team-ffd", "sess-ffd"))

	data, err := os.ReadFile(filepath.Join(work, "kanban.md"))
	if err != nil {
		t.Fatalf("kanban.md: %v", err)
	}
	content := string(data)
	doneIdx := indexOf(content, "## DONE")
	if doneIdx < 0 {
		t.Fatal("DONE section not found")
	}
	doneSection := content[doneIdx:]
	if indexOf(doneSection, "- [x] Another free-form task") < 0 {
		t.Error("expected free-form task to move to DONE with checkbox '[x]'")
	}
}

// TestTeamLifecycle_TeamDelete_ScratchpadSizeBytes_NotHardcodedZero confirms
// scratchpad_size_bytes reflects the actual work/current/ directory size
// rather than the previous hardcoded 0 (S-6 A-2a round 2 review finding 2).
func TestTeamLifecycle_TeamDelete_ScratchpadSizeBytes_NotHardcodedZero(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "work", "current")
	_ = os.MkdirAll(work, 0755)
	// Give the scratchpad some real content so its on-disk size is > 0.
	if err := os.WriteFile(filepath.Join(work, "notes.md"), make([]byte, 16384), 0644); err != nil {
		t.Fatalf("write notes.md: %v", err)
	}
	sessLog := filepath.Join(base, "work", "sessions.ndjson")

	h := &teamlifecycle.Hook{WorkCurrentDir: work, NowFn: fixedNow}
	_, _ = h.Run(context.Background(), makeInput("TeamDelete", "team-sz", "sess-sz"))

	data, err := os.ReadFile(sessLog)
	if err != nil {
		t.Fatalf("sessions.ndjson not written: %v", err)
	}
	var rec map[string]any
	_ = json.Unmarshal(data[:len(data)-1], &rec)
	size, ok := rec["scratchpad_size_bytes"].(float64)
	if !ok {
		t.Fatalf("scratchpad_size_bytes missing or wrong type: %v", rec["scratchpad_size_bytes"])
	}
	if size <= 0 {
		t.Errorf("expected scratchpad_size_bytes > 0 for a non-empty work dir; got %v", size)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
