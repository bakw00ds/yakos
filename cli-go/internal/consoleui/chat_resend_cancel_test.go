package consoleui_test

// chat_resend_cancel_test.go — a pane that cancels a turn and resends at once on
// the same session id must be able to cancel the new turn.
//
// A cancel removes the session's entry from the in-flight registry immediately, so
// the resend is accepted while the cancelled turn's goroutine is still unwinding.
// That goroutine removes "its" entry when it ends; keyed by session id alone it
// removed the new turn's entry instead, and the new turn could no longer be
// cancelled. The registry is keyed by generation now.
//
// The test makes the race deterministic. The first turn's claude leaves a
// background child holding its stdout, so killing claude does not end the
// goroutine reading that pipe; the test ends it on purpose after the resend.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

func TestChatDispatch_ResendAfterCancelIsStillCancellable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	fake.killStubsAtCleanup(t)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})

	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	tok, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: filepath.Join(t.TempDir(), "kanban.md"), KanbanProject: "test",
		MetricsProjectDir: t.TempDir(), PerfWorkDir: t.TempDir(), Bus: bus, WorkDir: workDir,
		YakosRoot: root, WorkspaceRoot: t.TempDir(), DispatchService: svc,
	})
	ts := httptest.NewServer(consoleui.RequireTokenForNonStatic(tok, consoleui.RequireJSONForMutations(srv.HandlerForTest())))
	t.Cleanup(ts.Close)
	hub := srv.ChatHub()
	store := consoleui.NewTranscripts(workDir)
	const conv, sess = "conv-resend", "s-resend"

	dispatchTurn := func(what string) {
		t.Helper()
		if code, body := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": conv, "sessionId": sess}); code != http.StatusAccepted {
			t.Fatalf("%s: status %d (%s)", what, code, body)
		}
	}
	cancelTurn := func(what string) {
		t.Helper()
		resp := post(t, ts.URL+"/api/chat/cancel", tok, `{"sessionId":"`+sess+`","operatorId":"alice"}`)
		drainClose(resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: cancel status %d", what, resp.StatusCode)
		}
	}

	// Turn 1 runs; the pane cancels it. claude dies, but its stdout stays open.
	fake.set(t, "linger", "unused")
	dispatchTurn("turn 1")
	waitUntil(t, "turn 1's claude to start and leave its child behind", func() bool { return fake.lingerPID() > 0 })
	linger := fake.lingerPID()
	cancelTurn("turn 1")

	// The pane resends at once on the same session id. The first turn's goroutine
	// is still blocked on the open pipe.
	fake.set(t, "hang", "unused")
	dispatchTurn("turn 2 (the resend)")
	waitUntil(t, "turn 2's claude to start", func() bool { return len(fake.calls(t)) >= 2 })

	// Let the first turn's goroutine finish. It releases the session's slot as it
	// ends (and the hub's entry, which is how the test knows it is done).
	killPID(linger)
	waitUntil(t, "the cancelled turn's goroutine to finish", func() bool {
		_, exists := hub.SessionOwner(sess)
		return !exists
	})

	// The second turn is still registered, so cancelling reaches it and it ends.
	cancelTurn("turn 2")
	deadline := time.Now().Add(5 * time.Second)
	for {
		entries, _ := store.Read(conv, "alice")
		summaries := 0
		for _, e := range entries {
			if e.Role == consoleui.RoleSummary {
				summaries++
			}
		}
		if summaries >= 2 {
			return // both turns ended: the second was cancelled
		}
		if time.Now().After(deadline) {
			t.Fatalf("the resent turn was never cancelled (%d turn(s) ended): the first turn's goroutine released the second turn's slot", summaries)
		}
		time.Sleep(15 * time.Millisecond)
	}
}
