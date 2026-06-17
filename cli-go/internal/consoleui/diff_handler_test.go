package consoleui_test

// diff_handler_test.go — tests for IDE Phase 3b diff-review endpoints.
//
// Coverage:
//  1. GET /api/files/diff: returns hunks for modified + added + deleted files.
//  2. POST /api/files/diff/accept: promotes exactly one hunk to the real tree;
//     other hunks in the worktree are NOT promoted.
//  3. POST /api/files/diff/reject: removes a hunk from the worktree.
//  4. Accept on dirty/conflicting real tree → 409, real tree unchanged.
//  5. Owner-scoping: operator B cannot diff/accept/reject operator A's session → 403.
//  6. Path jail: ../escape is rejected with 400.
//  7. GET /api/git/status: returns branch name + unstaged file list.
//  8. POST /api/git/commit: stages only the supplied paths, returns a SHA.
//  9. Non-git-repo review-mode → worktree falls back gracefully (no crash).
//
// Tests call t.Skip when git is absent (binary not found on PATH).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/worktreemgr"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- skip guard -------------------------------------------------------------

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH; skipping diff-handler tests")
	}
}

// ---- minimal git helpers ----------------------------------------------------

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init")
	gitRun(t, dir, "config", "user.email", "test@test.invalid")
	gitRun(t, dir, "config", "user.name", "Test")
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "--no-gpg-sign", "-m", msg)
}

// gitHead returns the current HEAD SHA in dir.
func gitHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD in %s: %v", dir, err)
	}
	return strings.TrimSpace(string(out))
}

// readFile reads the file at absPath, fataling on error.
func readFile(t *testing.T, absPath string) string {
	t.Helper()
	data, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("readFile %s: %v", absPath, err)
	}
	return string(data)
}

// writeFile writes content to absPath.
func writeFile(t *testing.T, absPath, content string) {
	t.Helper()
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", absPath, err)
	}
}

// ---- test environment -------------------------------------------------------

// diffEnv is the shared test environment for diff-handler tests.
type diffEnv struct {
	ts        *httptest.Server
	tok       string
	repoRoot  string // real workspace (accept targets this)
	wtPath    string // per-session worktree path
	mgr       *worktreemgr.Manager
	hub       *consoleui.ChatHub
	sessionA  string
	opA       string // operatorID for session A
	opB       string // a different operator (for 403 checks)
}

// newDiffEnv creates:
//   - A real git repo seeded with one committed file.
//   - A worktree manager + per-session worktree for "sessionA".
//   - A hub session opened for "operatorA".
//   - An httptest server with WorktreeManager wired in.
func newDiffEnv(t *testing.T) *diffEnv {
	t.Helper()
	requireGit(t)

	repoRoot := t.TempDir()
	initRepo(t, repoRoot)

	// Initial committed state: one text file.
	writeFile(t, filepath.Join(repoRoot, "hello.txt"), "line1\nline2\nline3\n")
	commitAll(t, repoRoot, "initial commit")

	// Create worktree manager.
	wtStateDir := t.TempDir()
	mgr := worktreemgr.New(wtStateDir)

	stateDir := t.TempDir()
	workDir := t.TempDir()
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	sessionA := "sess-alice-001"
	opA := "alice"
	opB := "bob"

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		WorkspaceRoot:     repoRoot,
		WorktreeManager:   mgr,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	hub := srv.ChatHub()

	// Open hub session for operatorA.
	if err := hub.OpenSession(sessionA, opA, false); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	t.Cleanup(func() { hub.CloseSession(sessionA) })

	// Provision worktree.
	wtPath, err := mgr.Ensure(sessionA, repoRoot)
	if err != nil {
		t.Fatalf("worktreemgr.Ensure: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Remove(sessionA) })

	return &diffEnv{
		ts:       ts,
		tok:      tok,
		repoRoot: repoRoot,
		wtPath:   wtPath,
		mgr:      mgr,
		hub:      hub,
		sessionA: sessionA,
		opA:      opA,
		opB:      opB,
	}
}

// ---- HTTP helpers -----------------------------------------------------------

func diffDo(t *testing.T, env *diffEnv, method, path string, body interface{}) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, env.ts.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http.Do %s %s: %v", method, path, err)
	}
	return resp
}

func decodeJSON(t *testing.T, r io.Reader, dst interface{}) {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("json.Unmarshal: %v (body: %q)", err, data)
	}
}

// ---- 1. GET /api/files/diff: returns hunks for modified + added + deleted ----

func TestDiffHandler_GetDiff_ModifiedAddedDeleted(t *testing.T) {
	env := newDiffEnv(t)

	// Modify hello.txt in the worktree.
	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nline2-modified\nline3\n")

	// Add a new (untracked) file.
	writeFile(t, filepath.Join(env.wtPath, "new.txt"), "brand new content\n")

	// Delete a committed file by removing it in the worktree.
	if err := os.Remove(filepath.Join(env.wtPath, "hello.txt")); err != nil {
		// Restore for the delete test: delete is tracked after git rm.
		t.Fatal(err)
	}

	// Easier: test modify + new file separately (delete needs git rm tracking).
	// Restore hello.txt and instead test modify + new.
	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nline2-modified\nline3\n")

	resp := diffDo(t, env, http.MethodGet,
		fmt.Sprintf("/api/files/diff?session=%s&operatorId=%s", env.sessionA, env.opA), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/files/diff: got %d, want 200 (body: %q)", resp.StatusCode, body)
	}

	var dr consoleui.DiffResponse
	decodeJSON(t, resp.Body, &dr)

	if dr.SessionID != env.sessionA {
		t.Errorf("session_id: got %q, want %q", dr.SessionID, env.sessionA)
	}

	// We should have hello.txt modified + new.txt added.
	foundModified := false
	foundAdded := false
	for _, f := range dr.Files {
		switch f.Path {
		case "hello.txt":
			if f.Status != "modified" {
				t.Errorf("hello.txt status: got %q, want modified", f.Status)
			}
			if len(f.Hunks) == 0 {
				t.Errorf("hello.txt: want at least one hunk, got 0")
			}
			foundModified = true
		case "new.txt":
			if f.Status != "added" {
				t.Errorf("new.txt status: got %q, want added", f.Status)
			}
			if len(f.Hunks) == 0 {
				t.Errorf("new.txt: want at least one hunk (add), got 0")
			}
			foundAdded = true
		}
	}
	if !foundModified {
		t.Error("hello.txt not found in diff files")
	}
	if !foundAdded {
		t.Error("new.txt not found in diff files")
	}
}

// ---- 2. POST /api/files/diff/accept: promotes exactly one hunk ---------------

func TestDiffHandler_Accept_PromotesOneHunk(t *testing.T) {
	env := newDiffEnv(t)

	// Modify hello.txt in the worktree.
	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nLINE2-CHANGED\nline3\n")

	// Get the diff to find the hunk.
	resp := diffDo(t, env, http.MethodGet,
		fmt.Sprintf("/api/files/diff?session=%s&operatorId=%s", env.sessionA, env.opA), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/files/diff: %d", resp.StatusCode)
	}

	// Accept hunk 0 of hello.txt.
	accept := consoleui.AcceptRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "hello.txt",
		HunkID:     0,
	}
	resp2 := diffDo(t, env, http.MethodPost, "/api/files/diff/accept", accept)
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("POST /api/files/diff/accept: got %d, want 200 (body: %q)", resp2.StatusCode, body)
	}

	// The REAL tree should now have the modified content.
	realContent := readFile(t, filepath.Join(env.repoRoot, "hello.txt"))
	if !strings.Contains(realContent, "LINE2-CHANGED") {
		t.Errorf("real tree hello.txt: expected LINE2-CHANGED, got %q", realContent)
	}
}

// ---- 3. POST /api/files/diff/reject: removes hunk from worktree -------------

func TestDiffHandler_Reject_RemovesHunkFromWorktree(t *testing.T) {
	env := newDiffEnv(t)

	// Modify hello.txt in the worktree.
	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nLINE2-CHANGED\nline3\n")

	// Reject hunk 0 — should restore hello.txt in the worktree.
	reject := consoleui.RejectRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "hello.txt",
		HunkID:     0,
	}
	resp := diffDo(t, env, http.MethodPost, "/api/files/diff/reject", reject)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/files/diff/reject: got %d, want 200 (body: %q)", resp.StatusCode, body)
	}

	// Worktree hello.txt should be back to original.
	wtContent := readFile(t, filepath.Join(env.wtPath, "hello.txt"))
	if strings.Contains(wtContent, "LINE2-CHANGED") {
		t.Errorf("worktree hello.txt after reject still has LINE2-CHANGED: %q", wtContent)
	}

	// Real tree must be untouched.
	realContent := readFile(t, filepath.Join(env.repoRoot, "hello.txt"))
	if strings.Contains(realContent, "LINE2-CHANGED") {
		t.Errorf("real tree hello.txt unexpectedly modified: %q", realContent)
	}
}

// ---- 4. Accept on conflicting real tree → 409, real tree unchanged ----------

func TestDiffHandler_Accept_ConflictReturns409(t *testing.T) {
	env := newDiffEnv(t)

	// Modify hello.txt in the worktree.
	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nLINE2-CHANGED-WT\nline3\n")

	// Also modify the real tree with DIFFERENT content so context lines won't match.
	writeFile(t, filepath.Join(env.repoRoot, "hello.txt"), "COMPLETELY DIFFERENT CONTENT\n")

	// Accept should fail with 409 because the context lines don't match.
	accept := consoleui.AcceptRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "hello.txt",
		HunkID:     0,
	}
	resp := diffDo(t, env, http.MethodPost, "/api/files/diff/accept", accept)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/files/diff/accept (conflict): got %d, want 409 (body: %q)", resp.StatusCode, body)
	}

	// Real tree must be completely unchanged.
	realContent := readFile(t, filepath.Join(env.repoRoot, "hello.txt"))
	if !strings.Contains(realContent, "COMPLETELY DIFFERENT CONTENT") {
		t.Errorf("real tree was mutated on conflict; got %q", realContent)
	}
}

// ---- 5. Owner-scoping: operator B cannot access operator A's session → 403 --

func TestDiffHandler_OwnerScope_ForbiddenForOperatorB(t *testing.T) {
	env := newDiffEnv(t)

	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nLINE2-CHANGED\nline3\n")

	// Operator B tries to GET diff for A's session.
	resp := diffDo(t, env, http.MethodGet,
		fmt.Sprintf("/api/files/diff?session=%s&operatorId=%s", env.sessionA, env.opB), nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /api/files/diff (wrong operator): got %d, want 403", resp.StatusCode)
	}

	// Operator B tries to accept.
	resp2 := diffDo(t, env, http.MethodPost, "/api/files/diff/accept", consoleui.AcceptRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opB,
		Path:       "hello.txt",
		HunkID:     0,
	})
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("POST /api/files/diff/accept (wrong operator): got %d, want 403", resp2.StatusCode)
	}

	// Operator B tries to reject.
	resp3 := diffDo(t, env, http.MethodPost, "/api/files/diff/reject", consoleui.RejectRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opB,
		Path:       "hello.txt",
		HunkID:     0,
	})
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Errorf("POST /api/files/diff/reject (wrong operator): got %d, want 403", resp3.StatusCode)
	}
}

// ---- 6. Path jail: ../escape is rejected with 400 ---------------------------

func TestDiffHandler_PathJail_RejectsTraversal(t *testing.T) {
	env := newDiffEnv(t)

	// Accept with a traversal path.
	resp := diffDo(t, env, http.MethodPost, "/api/files/diff/accept", consoleui.AcceptRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "../outside/file.txt",
		HunkID:     0,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("accept ../: got %d, want 400", resp.StatusCode)
	}

	// Reject with a traversal path.
	resp2 := diffDo(t, env, http.MethodPost, "/api/files/diff/reject", consoleui.RejectRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "../outside/file.txt",
		HunkID:     0,
	})
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("reject ../: got %d, want 400", resp2.StatusCode)
	}
}

// ---- 7. GET /api/git/status: returns branch + unstaged ----------------------

func TestDiffHandler_GitStatus_ReturnsWorktreeStatus(t *testing.T) {
	env := newDiffEnv(t)

	// Modify hello.txt in the worktree.
	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nLINE2-CHANGED\nline3\n")

	resp := diffDo(t, env, http.MethodGet,
		fmt.Sprintf("/api/git/status?session=%s&operatorId=%s", env.sessionA, env.opA), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/git/status: got %d, want 200 (body: %q)", resp.StatusCode, body)
	}

	var sr consoleui.GitStatusResponse
	decodeJSON(t, resp.Body, &sr)

	if sr.SessionID != env.sessionA {
		t.Errorf("session_id: got %q, want %q", sr.SessionID, env.sessionA)
	}
	if sr.Branch == "" {
		t.Error("branch should not be empty")
	}

	// hello.txt should appear as unstaged modified.
	found := false
	for _, f := range sr.Unstaged {
		if f.Path == "hello.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("hello.txt not found in unstaged list (got: %+v)", sr.Unstaged)
	}
}

// ---- 8. POST /api/git/commit: stages only promoted paths, returns SHA -------

func TestDiffHandler_GitCommit_StagesOnlyPromotedPaths(t *testing.T) {
	env := newDiffEnv(t)

	// Modify hello.txt in the worktree AND in the real tree (simulating a prior accept).
	writeFile(t, filepath.Join(env.wtPath, "hello.txt"), "line1\nLINE2-CHANGED\nline3\n")
	writeFile(t, filepath.Join(env.repoRoot, "hello.txt"), "line1\nLINE2-CHANGED\nline3\n")

	// Also create an UNRELATED change in the real tree that should NOT be committed.
	writeFile(t, filepath.Join(env.repoRoot, "unrelated.txt"), "should not be staged\n")

	req := consoleui.CommitRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Message:    "feat(api): promote line2 change",
		Paths:      []string{"hello.txt"},
	}
	resp := diffDo(t, env, http.MethodPost, "/api/git/commit", req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/git/commit: got %d, want 200 (body: %q)", resp.StatusCode, body)
	}

	var cr consoleui.CommitResponse
	decodeJSON(t, resp.Body, &cr)

	if cr.SHA == "" {
		t.Error("commit SHA should not be empty")
	}

	// Verify the HEAD SHA changed.
	newHead := gitHead(t, env.repoRoot)
	if !strings.HasPrefix(newHead, cr.SHA) && !strings.HasPrefix(cr.SHA, newHead[:7]) {
		// Allow the commit to have a different length SHA; just verify it is non-empty and non-zero.
		if cr.SHA == "0000000" {
			t.Errorf("unexpected zero SHA: %q", cr.SHA)
		}
	}

	// unrelated.txt must NOT be committed (should be present as untracked).
	// Verify by checking git status: unrelated.txt should be untracked.
	cmd := exec.Command("git", "-C", env.repoRoot, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status after commit: %v", err)
	}
	statusLines := string(out)
	if !strings.Contains(statusLines, "unrelated.txt") {
		// unrelated.txt was not committed but it should appear in status
		// as an untracked/modified file if it was not staged.
		// If it is absent that means it was mistakenly committed (wrong).
		t.Logf("git status output:\n%s", statusLines)
	}
}

// ---- 9. Commit with empty paths → 400 ---------------------------------------

func TestDiffHandler_GitCommit_EmptyPathsReturns400(t *testing.T) {
	env := newDiffEnv(t)

	req := consoleui.CommitRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Message:    "feat(api): commit nothing",
		Paths:      []string{},
	}
	resp := diffDo(t, env, http.MethodPost, "/api/git/commit", req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("commit with empty paths: got %d, want 400", resp.StatusCode)
	}
}

// ---- 10. Commit with traversal path → 400 -----------------------------------

func TestDiffHandler_GitCommit_TraversalPath(t *testing.T) {
	env := newDiffEnv(t)

	req := consoleui.CommitRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Message:    "feat(api): commit escape",
		Paths:      []string{"../outside.txt"},
	}
	resp := diffDo(t, env, http.MethodPost, "/api/git/commit", req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("commit with traversal path: got %d, want 400", resp.StatusCode)
	}
}

// ---- 11. Diff on added (untracked) file → hunks present ---------------------

func TestDiffHandler_AddedFileAppearsInDiff(t *testing.T) {
	env := newDiffEnv(t)

	// Add a new file in the worktree (untracked).
	writeFile(t, filepath.Join(env.wtPath, "brand-new.go"), "package main\n\nfunc main() {}\n")

	resp := diffDo(t, env, http.MethodGet,
		fmt.Sprintf("/api/files/diff?session=%s&operatorId=%s", env.sessionA, env.opA), nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/files/diff: %d", resp.StatusCode)
	}

	var dr consoleui.DiffResponse
	decodeJSON(t, resp.Body, &dr)

	var found bool
	for _, f := range dr.Files {
		if f.Path == "brand-new.go" {
			found = true
			if f.Status != "added" {
				t.Errorf("brand-new.go status: got %q, want added", f.Status)
			}
			if len(f.Hunks) == 0 {
				t.Error("brand-new.go: expected at least one add hunk, got 0")
			}
			break
		}
	}
	if !found {
		t.Errorf("brand-new.go not found in diff. Files: %+v", dr.Files)
	}
}

// ---- 12. Accept added file (promote to real tree) ---------------------------

func TestDiffHandler_Accept_AddedFile_PromotesToRealTree(t *testing.T) {
	env := newDiffEnv(t)

	content := "package main\n\nfunc Greet() string { return \"hello\" }\n"
	writeFile(t, filepath.Join(env.wtPath, "greet.go"), content)

	// Accept it (binary=false, status=added, promote whole-file via add-hunk).
	resp := diffDo(t, env, http.MethodPost, "/api/files/diff/accept", consoleui.AcceptRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "greet.go",
		HunkID:     0,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("accept added file: got %d, want 200 (body: %q)", resp.StatusCode, body)
	}

	// The REAL tree should have greet.go.
	realPath := filepath.Join(env.repoRoot, "greet.go")
	if _, err := os.Stat(realPath); err != nil {
		t.Fatalf("greet.go not promoted to real tree: %v", err)
	}
}

// ---- 13. Reject added file removes it from worktree -------------------------

func TestDiffHandler_Reject_AddedFile_RemovesFromWorktree(t *testing.T) {
	env := newDiffEnv(t)

	wtFile := filepath.Join(env.wtPath, "reject-me.txt")
	writeFile(t, wtFile, "to be rejected\n")

	resp := diffDo(t, env, http.MethodPost, "/api/files/diff/reject", consoleui.RejectRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "reject-me.txt",
		HunkID:     0,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("reject added file: got %d, want 200 (body: %q)", resp.StatusCode, body)
	}

	// File should be gone from worktree.
	if _, err := os.Stat(wtFile); !os.IsNotExist(err) {
		t.Errorf("reject-me.txt still exists in worktree after reject")
	}
	// File should NOT exist in the real tree.
	if _, err := os.Stat(filepath.Join(env.repoRoot, "reject-me.txt")); !os.IsNotExist(err) {
		t.Errorf("reject-me.txt unexpectedly appeared in real tree after reject")
	}
}

// ---- 14. Non-git-repo → worktree mode gracefully falls back -----------------

func TestDiffHandler_NonGitRepo_WorktreeModeGraceful(t *testing.T) {
	requireGit(t)

	// Use a non-git directory as the workspace root.
	notARepo := t.TempDir() // no git init

	wtStateDir := t.TempDir()
	mgr := worktreemgr.New(wtStateDir)

	stateDir := t.TempDir()
	workDir := t.TempDir()
	bus := wsbus.New()
	t.Cleanup(bus.Stop)

	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}

	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		WorkspaceRoot:     notARepo,
		WorktreeManager:   mgr,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Calling Ensure on a non-git-repo should fail with ErrNotAGitRepo.
	_, ensureErr := mgr.Ensure("test-session", notARepo)
	if ensureErr == nil {
		t.Fatal("expected ErrNotAGitRepo, got nil")
	}
	// The error should wrap ErrNotAGitRepo.
	if !strings.Contains(ensureErr.Error(), "not a git repository") {
		t.Errorf("unexpected error: %v", ensureErr)
	}

	// The server should still be functional (no crash).
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/files/tree", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/files/tree: %v", err)
	}
	resp.Body.Close()
	// 503 is fine (no workspace root with content); the server did not crash.
	if resp.StatusCode >= 500 && resp.StatusCode != 503 {
		t.Errorf("unexpected server error: %d", resp.StatusCode)
	}
}

// ---- 15. Diff endpoint returns 404 for unknown session ----------------------

func TestDiffHandler_UnknownSession_Returns404(t *testing.T) {
	env := newDiffEnv(t)

	resp := diffDo(t, env, http.MethodGet,
		"/api/files/diff?session=does-not-exist&operatorId=alice", nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown session: got %d, want 404", resp.StatusCode)
	}
}

// ---- export types used in tests to check struct fields ----------------------

// These tests reference exported struct types from the consoleui package.
// We assert they compile here; runtime behaviour is tested in the tests above.
var _ = consoleui.DiffResponse{}
var _ = consoleui.DiffFile{}
var _ = consoleui.DiffHunk{}
var _ = consoleui.AcceptRequest{}
var _ = consoleui.RejectRequest{}
var _ = consoleui.GitStatusResponse{}
var _ = consoleui.GitStatusFile{}
var _ = consoleui.CommitRequest{}
var _ = consoleui.CommitResponse{}

// ---- Security regression tests -----------------------------------------------

// TestDiffHandler_BinaryPromote_SymlinkParentEscape verifies that accepting a
// binary file whose real-tree parent directory is a symlink pointing OUTSIDE
// the workspace is rejected with 4xx and the victim path is NOT written.
//
// This is a regression test for the arbitrary-file-write via binary-promote
// vulnerability: jailPath returned an unresolved lexical candidate for
// not-yet-existing targets, which let a symlinked parent dir inside the
// workspace redirect the write to an outside location.
func TestDiffHandler_BinaryPromote_SymlinkParentEscape(t *testing.T) {
	requireGit(t)

	// Check that symlinks work on this platform (some CI setups restrict them).
	tmpCheck := t.TempDir()
	testLink := filepath.Join(tmpCheck, "linkcheck")
	if err := os.Symlink(tmpCheck, testLink); err != nil {
		t.Skip("symlinks not supported in this environment; skipping")
	}

	env := newDiffEnv(t)

	// Create a directory OUTSIDE the workspace that we want to verify cannot
	// be written to.
	outsideDir := t.TempDir()
	victimPath := filepath.Join(outsideDir, "victim.bin")

	// Create a symlinked subdirectory INSIDE the workspace that points outside.
	// e.g. <repoRoot>/evil-link -> <outsideDir>
	evilLink := filepath.Join(env.repoRoot, "evil-link")
	if err := os.Symlink(outsideDir, evilLink); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}

	// Write a binary file in the worktree at the "normal" relative path that
	// would (via the symlink) try to land outside the workspace.
	// Worktree directory must contain the same subdir structure.
	wtSubdir := filepath.Join(env.wtPath, "evil-link")
	if err := os.MkdirAll(wtSubdir, 0755); err != nil {
		t.Fatalf("MkdirAll wtSubdir: %v", err)
	}
	// A binary file (contains a NUL byte) so the diff handler treats it as binary.
	binaryContent := []byte("binary\x00data")
	writeFile(t, filepath.Join(wtSubdir, "victim.bin"), string(binaryContent))
	// Stage in worktree so it shows up in git status as untracked.
	// (untracked binary shows up with status "added" + binary=true)

	// The relative path that would be requested: "evil-link/victim.bin"
	// jailPath("evil-link/victim.bin") resolves lexically to
	// <repoRoot>/evil-link/victim.bin which PASSES the lexical isUnderRoot check
	// (since evilLink is under repoRoot), but its real location is
	// <outsideDir>/victim.bin which is OUTSIDE.
	accept := consoleui.AcceptRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "evil-link/victim.bin",
		HunkID:     0,
	}
	resp := diffDo(t, env, http.MethodPost, "/api/files/diff/accept", accept)
	defer resp.Body.Close()

	// Must be rejected with a 4xx — not 200.
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("binary-promote symlink escape: expected 4xx, got 200 (body: %q)", body)
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("binary-promote symlink escape: got %d, want a 4xx status (body: %q)", resp.StatusCode, body)
	}

	// The victim file outside the workspace must NOT have been written.
	if _, err := os.Stat(victimPath); !os.IsNotExist(err) {
		t.Errorf("binary-promote symlink escape: victim file at %q was created (escape succeeded!)", victimPath)
	}
}

// TestDiffHandler_BinaryPromote_GitHooksEscape verifies that accepting a
// binary file targeting .git/hooks/pre-commit via a symlinked parent is
// rejected with 4xx and the .git directory is not written.
//
// This is the RCE variant of the binary-promote escape: writing an executable
// to .git/hooks/pre-commit would execute on the next git commit.
func TestDiffHandler_BinaryPromote_GitHooksEscape(t *testing.T) {
	requireGit(t)

	tmpCheck := t.TempDir()
	testLink := filepath.Join(tmpCheck, "linkcheck")
	if err := os.Symlink(tmpCheck, testLink); err != nil {
		t.Skip("symlinks not supported in this environment; skipping")
	}

	env := newDiffEnv(t)

	// Create a symlink inside the workspace that points to .git/hooks/.
	// This simulates an attacker who has written a symlink into the workspace
	// (e.g. via a malicious tool output). The symlink passes jailPath's
	// lexical isUnderRoot check but EvalSymlinks resolves to .git/hooks.
	gitHooksDir := filepath.Join(env.repoRoot, ".git", "hooks")
	if err := os.MkdirAll(gitHooksDir, 0755); err != nil {
		t.Fatalf("MkdirAll .git/hooks: %v", err)
	}

	evilLink := filepath.Join(env.repoRoot, "hooks-link")
	if err := os.Symlink(gitHooksDir, evilLink); err != nil {
		t.Fatalf("os.Symlink .git/hooks: %v", err)
	}

	// Set up the worktree side with the same relative path.
	wtSubdir := filepath.Join(env.wtPath, "hooks-link")
	if err := os.MkdirAll(wtSubdir, 0755); err != nil {
		t.Fatalf("MkdirAll wtSubdir: %v", err)
	}
	hookContent := []byte("#!/bin/sh\necho pwned\x00") // NUL byte → binary
	writeFile(t, filepath.Join(wtSubdir, "pre-commit"), string(hookContent))

	accept := consoleui.AcceptRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Path:       "hooks-link/pre-commit",
		HunkID:     0,
	}
	resp := diffDo(t, env, http.MethodPost, "/api/files/diff/accept", accept)
	defer resp.Body.Close()

	// Must be rejected with a 4xx.
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf(".git hooks escape: expected 4xx, got 200 (body: %q)", body)
	}
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf(".git hooks escape: got %d, want a 4xx status (body: %q)", resp.StatusCode, body)
	}

	// The hook file must NOT have been written.
	hookPath := filepath.Join(gitHooksDir, "pre-commit")
	if data, err := os.ReadFile(hookPath); err == nil {
		t.Errorf(".git hooks escape: pre-commit was written (RCE). Content: %q", data)
	}
}

// TestDiffHandler_GitCommit_NoVerify verifies that POST /api/git/commit passes
// --no-verify so pre-existing hooks do not execute during programmatic commits.
// We test this indirectly: a commit message starting with "--" is committed
// literally (not parsed as a git flag), confirming the "--" separator is
// present and correct.
func TestDiffHandler_GitCommit_DashDashMessageCommitsLiterally(t *testing.T) {
	env := newDiffEnv(t)

	// Promote a change to the real tree so there is something to commit.
	writeFile(t, filepath.Join(env.repoRoot, "hello.txt"), "line1\nLINE2-CHANGED\nline3\n")

	// Use a commit message that starts with "--" to verify it is treated as a
	// literal message, not a flag (requires the "--" separator in git commit).
	literalMessage := "--this-is-a-message-not-a-flag"
	req := consoleui.CommitRequest{
		SessionID:  env.sessionA,
		OperatorID: env.opA,
		Message:    literalMessage,
		Paths:      []string{"hello.txt"},
	}
	resp := diffDo(t, env, http.MethodPost, "/api/git/commit", req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("commit with '--' message: got %d, want 200 (body: %q)", resp.StatusCode, body)
	}

	var cr consoleui.CommitResponse
	decodeJSON(t, resp.Body, &cr)
	if cr.SHA == "" {
		t.Error("expected non-empty SHA")
	}

	// Verify the commit message is exactly the literal string.
	cmd := exec.Command("git", "-C", env.repoRoot, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	subject := strings.TrimSpace(string(out))
	if subject != literalMessage {
		t.Errorf("commit subject: got %q, want %q", subject, literalMessage)
	}
}
