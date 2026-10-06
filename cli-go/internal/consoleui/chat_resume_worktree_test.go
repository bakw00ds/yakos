package consoleui_test

// chat_resume_worktree_test.go — an IDE review pane (worktree mode) gives every
// turn its own working directory, and claude files sessions by directory, so a
// worktree turn neither resumes nor stores a native session. Two guards in the
// handler enforce it, and each is pinned here separately: one on the lookup
// before the turn, one on the store after it. Both used to survive mutation.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/worktreemgr"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// newWorktreeServer is a console server over a real git workspace with a
// worktree manager, so a dispatch with worktreeMode gets a worktree.
func newWorktreeServer(t *testing.T, yakosRoot string) (ts *httptest.Server, tok, workDir string) {
	t.Helper()
	requireGit(t)
	repo := t.TempDir()
	initRepo(t, repo)
	writeFile(t, filepath.Join(repo, "hello.txt"), "hello\n")
	commitAll(t, repo, "initial commit")

	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	tok, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workDir = t.TempDir()
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: repo})
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		YakosRoot:         yakosRoot,
		WorkspaceRoot:     repo,
		WorktreeManager:   worktreemgr.New(t.TempDir()),
		DispatchService:   svc,
	})
	ts = httptest.NewServer(consoleui.RequireTokenForNonStatic(tok, consoleui.RequireJSONForMutations(srv.HandlerForTest())))
	t.Cleanup(ts.Close)
	return ts, tok, workDir
}

// The lookup guard: a stored session exists, and a worktree turn must still not
// resume it (it would be a session of another directory).
func TestChatDispatch_WorktreeTurnDoesNotResumeAStoredSession(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	ts, tok, workDir := newWorktreeServer(t, root)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-wt-lookup"

	if err := store.SetNativeSession(conv, "claude", "sess-STORED", "alice"); err != nil {
		t.Fatal(err)
	}
	fake.set(t, "ok", "sess-WT")
	code, body := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": conv, "sessionId": "s-wt-1", "worktreeMode": true})
	if code != http.StatusAccepted {
		t.Fatalf("status %d %s", code, body)
	}
	waitForTurns(t, store, conv, 1)

	calls := fake.calls(t)
	if len(calls) != 1 {
		t.Fatalf("want 1 claude call, got %d: %v", len(calls), calls)
	}
	for _, a := range calls[0] {
		if a == "--resume" {
			t.Errorf("a worktree turn resumed a stored session: %v", calls[0])
		}
	}
}

// The store guard: a worktree turn's own session id is not remembered, because
// the next worktree turn is in a different directory and could not resume it.
func TestChatDispatch_WorktreeTurnDoesNotStoreItsSession(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	ts, tok, workDir := newWorktreeServer(t, root)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-wt-store"

	fake.set(t, "ok", "sess-WT")
	code, body := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": conv, "sessionId": "s-wt-1", "worktreeMode": true})
	if code != http.StatusAccepted {
		t.Fatalf("status %d %s", code, body)
	}
	waitForTurns(t, store, conv, 1)
	if got := store.NativeSession(conv, "claude", "alice"); got != "" {
		t.Errorf("a worktree turn's session id %q was stored", got)
	}

	// A normal turn of the same conversation afterwards starts fresh, and is
	// remembered as usual.
	fake.set(t, "ok", "sess-NORMAL")
	if code, body := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": conv, "sessionId": "s-n-1"}); code != http.StatusAccepted {
		t.Fatalf("status %d %s", code, body)
	}
	waitUntil(t, "the normal turn's id to be stored", func() bool { return store.NativeSession(conv, "claude", "alice") == "sess-NORMAL" })
	waitForTurns(t, store, conv, 2)
}
