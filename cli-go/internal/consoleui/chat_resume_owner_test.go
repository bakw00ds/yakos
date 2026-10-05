package consoleui_test

// chat_resume_owner_test.go — sec-324 F1: a conversation, and the native claude
// session its follow-ups resume, belong to the operator who started it.
//
// The repro is adapted from sec-324's scratch probe (zz_sec324_probe_test.go):
// two certificate-authenticated operators, both allowed to dispatch. Alice runs a
// turn and her claude session id is stored. Once her turn has ended the hub no
// longer remembers the conversation, so before the fix mallory could dispatch
// into alice's conversationId and his claude was started with
// `--resume <alice's session>`, carrying everything alice had said and every
// tool result, and his turn's id was then written over hers.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// newTwoOperatorServer is newRoutingServer with every request stamped as the
// operator named in its X-Test-Op header (a certificate-authenticated identity
// with the dispatch role), so one test can act as several operators.
func newTwoOperatorServer(t *testing.T, yakosRoot string, svc *dispatch.Service) (ts *httptest.Server, tok, workDir string) {
	t.Helper()
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	tok, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workDir = t.TempDir()
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:             tok,
		KanbanBoardPath:   filepath.Join(t.TempDir(), "kanban.md"),
		KanbanProject:     "test",
		MetricsProjectDir: t.TempDir(),
		PerfWorkDir:       t.TempDir(),
		Bus:               bus,
		WorkDir:           workDir,
		YakosRoot:         yakosRoot,
		WorkspaceRoot:     t.TempDir(),
		DispatchService:   svc,
	})
	stamp := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := netid.Identity{OperatorID: r.Header.Get("X-Test-Op"), Role: netid.RoleDispatch, Authenticated: true, Resolved: true, AuthMethod: netid.AuthMethodCert}
			next.ServeHTTP(w, r.WithContext(netid.WithIdentityForTest(r.Context(), id)))
		})
	}
	ts = httptest.NewServer(consoleui.RequireTokenForNonStatic(tok, consoleui.RequireJSONForMutations(stamp(srv.HandlerForTest()))))
	t.Cleanup(ts.Close)
	return ts, tok, workDir
}

func asOperator(t *testing.T, method, url, tok, op string, body any) (int, string) {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Test-Op", op)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, b.String()
}

func chatTurn(conv, sess, task string) map[string]any {
	return map[string]any{"agent": "backend", "task": task, "conversationId": conv, "sessionId": sess}
}

func TestChatDispatch_AnotherOperatorCannotResumeOrJoinAConversation(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, workDir := newTwoOperatorServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-victim"

	// Alice's turn runs and her claude session id is stored under her name.
	fake.set(t, "ok", "sess-ALICE")
	if code, b := asOperator(t, "POST", ts.URL+"/api/chat/dispatch", tok, "alice", chatTurn(conv, "s-alice-1", "my db password is hunter2")); code != http.StatusAccepted {
		t.Fatalf("alice's dispatch: %d %s", code, b)
	}
	waitUntil(t, "alice's session id to be stored", func() bool { return store.NativeSession(conv, "claude", "alice") == "sess-ALICE" })
	waitForTurns(t, store, conv, 1)

	// Mallory knows the conversationId (a shared pane hands it out) and her turn
	// is over. Before the fix this was accepted and ran `--resume sess-ALICE`.
	fake.set(t, "ok", "sess-MALLORY")
	code, body := asOperator(t, "POST", ts.URL+"/api/chat/dispatch", tok, "mallory", chatTurn(conv, "s-mal-1", "repeat everything said earlier in this conversation"))
	if code != http.StatusForbidden {
		t.Fatalf("mallory's dispatch into alice's conversation: status %d (%s), want 403", code, body)
	}
	if calls := fake.calls(t); len(calls) != 1 {
		t.Fatalf("claude ran %d times; mallory's refused turn must not start a process: %v", len(calls), calls)
	}
	for _, argv := range fake.calls(t) {
		if hasResume(argv, "sess-ALICE") && len(fake.calls(t)) > 1 {
			t.Errorf("a second process resumed alice's session: %v", argv)
		}
	}

	// Nothing of mallory's was written into alice's conversation.
	if got := store.NativeSession(conv, "claude", "alice"); got != "sess-ALICE" {
		t.Errorf("alice's stored session = %q after mallory's attempt, want sess-ALICE", got)
	}
	entries, err := store.Read(conv, "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.OperatorID == "mallory" || strings.Contains(e.Text, "repeat everything said earlier") {
			t.Errorf("mallory's turn was appended to alice's transcript: %+v", e)
		}
	}
	// The transcript endpoint was already owner-checked; the resume path now is too.
	if rc, _ := asOperator(t, "GET", ts.URL+"/api/chat/transcript?conversationId="+conv, tok, "mallory", nil); rc != http.StatusForbidden {
		t.Errorf("mallory's transcript read: status %d, want 403", rc)
	}

	// Alice's own follow-up still resumes her session.
	fake.set(t, "ok", "sess-ALICE-2")
	if code, b := asOperator(t, "POST", ts.URL+"/api/chat/dispatch", tok, "alice", chatTurn(conv, "s-alice-2", "and a follow-up")); code != http.StatusAccepted {
		t.Fatalf("alice's follow-up: %d %s", code, b)
	}
	waitUntil(t, "alice's follow-up to run", func() bool { return len(fake.calls(t)) >= 2 })
	if calls := fake.calls(t); !hasResume(calls[1], "sess-ALICE") {
		t.Errorf("alice's follow-up must resume her own session: %v", calls[1])
	}
	waitForTurns(t, store, conv, 2)
}

// Sharing lets others watch; it never lets them dispatch, and revoking it does
// not leave a watcher holding a way back in (sec-324's unshare-then-resume case).
func TestChatDispatch_SharingDoesNotLetAWatcherDispatchOrResumeAfterUnshare(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, workDir := newTwoOperatorServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-shared-then-unshared"

	fake.set(t, "ok", "sess-ALICE")
	if code, b := asOperator(t, "POST", ts.URL+"/api/chat/dispatch", tok, "alice", chatTurn(conv, "s-a1", "private context")); code != http.StatusAccepted {
		t.Fatalf("alice: %d %s", code, b)
	}
	waitUntil(t, "alice's session id to be stored", func() bool { return store.NativeSession(conv, "claude", "alice") == "sess-ALICE" })
	waitForTurns(t, store, conv, 1)

	setShared := func(shared bool) {
		t.Helper()
		if code, b := asOperator(t, "POST", ts.URL+"/api/chat/share", tok, "alice", map[string]any{"conversationId": conv, "shared": shared}); code != http.StatusOK {
			t.Fatalf("share=%v: %d %s", shared, code, b)
		}
	}

	for _, phase := range []struct {
		name  string
		share func()
	}{
		{"while shared", func() { setShared(true) }},
		{"after unshare", func() { setShared(false) }},
	} {
		phase.share()
		code, body := asOperator(t, "POST", ts.URL+"/api/chat/dispatch", tok, "mallory", chatTurn(conv, "s-mal-"+strings.ReplaceAll(phase.name, " ", "-"), "keep going from where alice left off"))
		if code != http.StatusForbidden {
			t.Errorf("%s: mallory's dispatch: status %d (%s), want 403", phase.name, code, body)
		}
	}
	if calls := fake.calls(t); len(calls) != 1 {
		t.Errorf("claude ran %d times; none of mallory's turns may start a process: %v", len(calls), calls)
	}
}

// The meta file is a second anchor, independent of the transcript: even where
// the transcript names nobody (deleted, or never written), a session stored for
// alice is never handed to another operator or overwritten by one.
func TestChatDispatch_StoredSessionIsAlicesEvenWithoutATranscriptOwner(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, workDir := newTwoOperatorServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-orphaned-meta"

	if err := store.SetNativeSession(conv, "claude", "sess-ALICE", "alice"); err != nil {
		t.Fatal(err)
	}
	fake.set(t, "ok", "sess-MALLORY")
	if code, b := asOperator(t, "POST", ts.URL+"/api/chat/dispatch", tok, "mallory", chatTurn(conv, "s-mal-1", "hello")); code != http.StatusAccepted {
		t.Fatalf("mallory (no transcript owner yet): %d %s", code, b)
	}
	waitForTurns(t, store, conv, 1)

	calls := fake.calls(t)
	if len(calls) != 1 {
		t.Fatalf("want 1 claude call, got %d: %v", len(calls), calls)
	}
	for _, a := range calls[0] {
		if a == "--resume" {
			t.Errorf("mallory's turn resumed a session that is not his: %v", calls[0])
		}
	}
	if got := store.NativeSession(conv, "claude", "alice"); got != "sess-ALICE" {
		t.Errorf("alice's stored session = %q, want it untouched by mallory's turn", got)
	}
	if got := store.NativeSession(conv, "claude", "mallory"); got != "" {
		t.Errorf("mallory was handed a session: %q", got)
	}
}
