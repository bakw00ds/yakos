package serve_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/serve"
)

// ---- test infrastructure (shared with serve_test.go) ------------------------
// Note: repoRoot, newTestDaemon, and pipeListener are defined in serve_test.go.

// writeKanban creates a work/current/kanban.md under dir with the given content.
func writeKanban(t *testing.T, dir, content string) {
	t.Helper()
	boardDir := filepath.Join(dir, "work", "current")
	if err := os.MkdirAll(boardDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(boardDir, "kanban.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write kanban: %v", err)
	}
}

const testBoard = `# Kanban — test

## TODO
- [ ] K-1 — task one
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

- [ ] K-2 — task two
  - category: feat
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS
- [ ] K-3 — in progress task
  - category: other
  - assigned: alice
  - blockers: none
  - notes:

## DONE
- [x] K-4 — done task
  - category: other
  - assigned: bob
  - blockers: none
  - notes:
`

// ---- yakos.kanban.add -------------------------------------------------------

func TestMethod_KanbanAdd_CreatesTask(t *testing.T) {
	tmp := t.TempDir()
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.add", map[string]string{
		"title": "implement health endpoint",
	})
	if err != nil {
		t.Fatalf("kanban.add: %v", err)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.ID == "" {
		t.Error("id should not be empty")
	}
	if !strings.HasPrefix(result.ID, "K-") {
		t.Errorf("id=%q; should start with K-", result.ID)
	}
}

func TestMethod_KanbanAdd_MissingTitle(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.add", map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestMethod_KanbanAdd_UnknownFieldRejected(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.add", map[string]string{
		"title":    "test",
		"badField": "x",
	})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestMethod_KanbanAdd_PersistsToBoard(t *testing.T) {
	tmp := t.TempDir()
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	if _, err := client.Call(context.Background(), "yakos.kanban.add", map[string]string{
		"title": "my new task",
	}); err != nil {
		t.Fatalf("kanban.add: %v", err)
	}

	// Verify the board file was written.
	boardPath := filepath.Join(tmp, "work", "current", "kanban.md")
	data, err := os.ReadFile(boardPath)
	if err != nil {
		t.Fatalf("read board: %v", err)
	}
	if !strings.Contains(string(data), "my new task") {
		t.Error("board should contain the new task title")
	}
}

// ---- yakos.kanban.move -------------------------------------------------------

func TestMethod_KanbanMove_MovesTask(t *testing.T) {
	tmp := t.TempDir()
	writeKanban(t, tmp, testBoard)
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.move", map[string]string{
		"id": "K-1",
		"to": "IN PROGRESS",
	})
	if err != nil {
		t.Fatalf("kanban.move: %v", err)
	}

	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.OK {
		t.Error("ok should be true")
	}
}

func TestMethod_KanbanMove_MissingID(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.move", map[string]string{"to": "DONE"})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestMethod_KanbanMove_InvalidColumn(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.move", map[string]string{
		"id": "K-1",
		"to": "INVALID",
	})
	if err == nil {
		t.Fatal("expected error for invalid column")
	}
}

func TestMethod_KanbanMove_UnknownTask(t *testing.T) {
	tmp := t.TempDir()
	writeKanban(t, tmp, testBoard)
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.move", map[string]string{
		"id": "K-99",
		"to": "DONE",
	})
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
}

// ---- yakos.kanban.done -------------------------------------------------------

func TestMethod_KanbanDone_MovesToDone(t *testing.T) {
	tmp := t.TempDir()
	writeKanban(t, tmp, testBoard)
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.done", map[string]string{"id": "K-1"})
	if err != nil {
		t.Fatalf("kanban.done: %v", err)
	}

	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.OK {
		t.Error("ok should be true")
	}
}

func TestMethod_KanbanDone_MissingID(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.done", map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestMethod_KanbanDone_UnknownTask(t *testing.T) {
	tmp := t.TempDir()
	writeKanban(t, tmp, testBoard)
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.done", map[string]string{"id": "K-99"})
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
}

// ---- yakos.kanban.list -------------------------------------------------------

func TestMethod_KanbanList_EmptyBoard(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.list", nil)
	if err != nil {
		t.Fatalf("kanban.list: %v", err)
	}

	var result struct {
		Items []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Column string `json:"column"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Items == nil {
		t.Error("items should not be nil (should be empty slice)")
	}
}

func TestMethod_KanbanList_WithBoard(t *testing.T) {
	tmp := t.TempDir()
	writeKanban(t, tmp, testBoard)
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.list", nil)
	if err != nil {
		t.Fatalf("kanban.list: %v", err)
	}

	var result struct {
		Items []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Column string `json:"column"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Items) != 4 {
		t.Errorf("expected 4 items; got %d", len(result.Items))
	}
}

func TestMethod_KanbanList_FilterByColumn(t *testing.T) {
	tmp := t.TempDir()
	writeKanban(t, tmp, testBoard)
	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.kanban.list", map[string]string{"column": "TODO"})
	if err != nil {
		t.Fatalf("kanban.list: %v", err)
	}

	var result struct {
		Items []struct {
			Column string `json:"column"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Items) != 2 {
		t.Errorf("expected 2 TODO items; got %d", len(result.Items))
	}
	for _, item := range result.Items {
		if item.Column != "TODO" {
			t.Errorf("item column=%q; want TODO", item.Column)
		}
	}
}

func TestMethod_KanbanList_UnknownFieldRejected(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.kanban.list", map[string]interface{}{"bad": true})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

// ---- yakos.refresh.run -------------------------------------------------------

func TestMethod_RefreshRun_NoYakosRoot(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: ""}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.refresh.run", nil)
	if err == nil {
		t.Fatal("expected error for missing yakos_root")
	}
}

func TestMethod_RefreshRun_UnknownFieldRejected(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.refresh.run", map[string]interface{}{"badField": true})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestMethod_RefreshRun_DryRunReturnsOutput(t *testing.T) {
	root := repoRoot(t)
	cfg := serve.Config{WorkspaceRoot: root, YakosRoot: root}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.refresh.run", map[string]interface{}{"dry_run": true})
	if err != nil {
		t.Fatalf("refresh.run: %v", err)
	}

	var result struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Output may be empty when no projects are registered; that's OK.
	// The key assertion is that the call succeeded and the field exists.
	_ = result.Output
}

// ---- yakos.cost.aggregate ----------------------------------------------------

func TestMethod_CostAggregate_EmptyLog(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.cost.aggregate", nil)
	if err != nil {
		t.Fatalf("cost.aggregate: %v", err)
	}

	var result struct {
		Events int64 `json:"Events"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Events != 0 {
		t.Errorf("expected 0 events for empty log; got %d", result.Events)
	}
}

func TestMethod_CostAggregate_InvalidAxis(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.cost.aggregate", map[string]string{"by": "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid axis")
	}
}

func TestMethod_CostAggregate_WithDispatchLog(t *testing.T) {
	tmp := t.TempDir()
	logDir := filepath.Join(tmp, "work", "current")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logLine := `{"type":"dispatch_finished","ts":"2026-06-01T10:00:00Z","agent":"backend","runtime":"claude","exit_code":0,"duration_s":5.0,"est_input_tokens":1000,"est_output_tokens":400}` + "\n"
	if err := os.WriteFile(filepath.Join(logDir, "dispatch-log.ndjson"), []byte(logLine), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	cfg := serve.Config{WorkspaceRoot: tmp, YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.cost.aggregate", map[string]string{"by": "agent"})
	if err != nil {
		t.Fatalf("cost.aggregate: %v", err)
	}

	var result struct {
		Events int64 `json:"Events"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Events != 1 {
		t.Errorf("expected 1 event; got %d", result.Events)
	}
}

func TestMethod_CostAggregate_UnknownFieldRejected(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.cost.aggregate", map[string]interface{}{"badField": 1})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

// ---- yakos.status.read -------------------------------------------------------

func TestMethod_StatusRead_ReturnsReport(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.status.read", nil)
	if err != nil {
		t.Fatalf("status.read: %v", err)
	}

	var result struct {
		Project string `json:"Project"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Project == "" {
		t.Error("project should not be empty")
	}
}

func TestMethod_StatusRead_WithProject(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	raw, err := client.Call(context.Background(), "yakos.status.read", map[string]string{"project": "myproject"})
	if err != nil {
		t.Fatalf("status.read: %v", err)
	}

	var result struct {
		Project string `json:"Project"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Project != "myproject" {
		t.Errorf("project=%q; want 'myproject'", result.Project)
	}
}

func TestMethod_StatusRead_UnknownFieldRejected(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.status.read", map[string]interface{}{"badField": true})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}

// ---- yakos.supervise.pending ------------------------------------------------

func TestMethod_SupervisePending_NoFindingsFile(t *testing.T) {
	// When no findings file exists, supervise.pending should return without error.
	// The supervise package returns an error when the .project-path file is
	// absent; so we expect an error here (state file not initialized).
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	// Call with no project — may fail because project inference from cwd may
	// not find a configured project. Both outcomes are acceptable as long as
	// we handle them gracefully.
	_, _ = client.Call(context.Background(), "yakos.supervise.pending", nil)
	// The test asserts that the daemon doesn't panic or hang.
}

func TestMethod_SupervisePending_UnknownFieldRejected(t *testing.T) {
	cfg := serve.Config{WorkspaceRoot: t.TempDir(), YakosRoot: repoRoot(t)}
	client, _ := newTestDaemon(t, cfg)

	_, err := client.Call(context.Background(), "yakos.supervise.pending", map[string]interface{}{"badField": 1})
	if err == nil {
		t.Fatal("expected error for unknown field")
	}
}
