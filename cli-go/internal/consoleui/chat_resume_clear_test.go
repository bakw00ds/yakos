package consoleui_test

// chat_resume_clear_test.go — when a stored claude session id is forgotten.
//
// The id is cleared when a resumed turn fails and the failure says the session
// is gone (any wording that calls a conversation or session "not found", not
// only claude 2.1.289's "No conversation found"), and always after two such
// turns in a row, so a CLI that rewords its message cannot leave a dead id
// failing every follow-up. A single failure that does not look like a missing
// session (an overloaded API, a rate limit) keeps the id, so one hiccup does
// not cost a conversation its memory.

import (
	"net/http"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
)

// resumeScript drives a conversation through steps. Each step is one turn: ok
// stores sess-<n>; a failure message makes that turn's --resume fail with it on
// stderr. after is what the stored id must be once the turn has finished, "" for
// forgotten.
type resumeStep struct {
	failWith     string // "" = a good turn
	wantID       string // the stored id after the turn
	wantFailures int    // the consecutive resume failures counted after the turn
}

func runResumeScript(t *testing.T, steps []resumeStep) {
	t.Helper()
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, workDir, _ := newRoutingServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-clear"

	// Turn 0 stores the session every later turn tries to resume.
	fake.set(t, "ok", "sess-0")
	if code, b := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": conv, "sessionId": "s-0"}); code != http.StatusAccepted {
		t.Fatalf("turn 0: %d %s", code, b)
	}
	waitUntil(t, "turn 0 to store its id", func() bool { return store.NativeSession(conv, "claude", "alice") == "sess-0" })
	waitForTurns(t, store, conv, 1)

	for i, st := range steps {
		n := i + 1
		sid := "s-" + string(rune('0'+n))
		if st.failWith == "" {
			fake.set(t, "ok", "sess-"+string(rune('0'+n)))
		} else {
			fake.failResumes(t, st.failWith)
		}
		if code, b := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": conv, "sessionId": sid}); code != http.StatusAccepted {
			t.Fatalf("turn %d: %d %s", n, code, b)
		}
		waitForTurns(t, store, conv, n+1)
		// The summary is written before the handler decides about the id, so wait
		// for the stored state (id and failure count) to settle on what this step
		// expects. Both are checked: "kept" is only proven once the failure has
		// been counted, and the next turn must not start before that.
		waitUntil(t, "the stored state after turn "+sid, func() bool {
			return store.NativeSession(conv, "claude", "alice") == st.wantID && store.ResumeFailures(conv, "claude", "alice") == st.wantFailures
		})
	}
}

func TestChatDispatch_StoredSessionIsForgottenWhenItsGone(t *testing.T) {
	cases := map[string]string{
		"claude's own wording": "No conversation found with session ID: gone",
		"a reworded message":   "Error: session 3f2a does not exist",
		"another phrasing":     "claude: could not find a previous conversation to resume",
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			runResumeScript(t, []resumeStep{{failWith: msg, wantID: "", wantFailures: 0}})
		})
	}
}

func TestChatDispatch_OneUnrelatedFailureKeepsTheSession(t *testing.T) {
	runResumeScript(t, []resumeStep{
		{failWith: "API Error: 529 overloaded_error", wantID: "sess-0", wantFailures: 1}, // kept
		{wantID: "sess-2"}, // and the next turn still resumed it, so it stored a fresh id
	})
}

// The backstop for a message nobody recognises.
func TestChatDispatch_TwoFailuresInARowForgetTheSession(t *testing.T) {
	runResumeScript(t, []resumeStep{
		{failWith: "Error: unable to continue", wantID: "sess-0", wantFailures: 1}, // 1st: kept
		{failWith: "Error: unable to continue", wantID: "", wantFailures: 0},       // 2nd in a row: forgotten
	})
}

// A good turn in between resets the count.
func TestChatDispatch_ASuccessfulTurnResetsTheFailureCount(t *testing.T) {
	runResumeScript(t, []resumeStep{
		{failWith: "Error: unable to continue", wantID: "sess-0", wantFailures: 1},
		{wantID: "sess-2"},
		{failWith: "Error: unable to continue", wantID: "sess-2", wantFailures: 1}, // 1st again, so kept
	})
}

// Cancelling a resumed turn is not a failed resume. The cancel kills claude, so
// the turn ends non-zero, and counting that would forget the stored session after
// two cancelled turns. The handler skips the failure accounting for a turn whose
// context was cancelled; this pins that guard.
//
// Each turn has its own session id. A cancel removes the session's entry at once
// and the old goroutine removes it again when it finishes, so reusing the id right
// after a cancel would let the old goroutine delete the new turn's entry (and the
// new turn could no longer be cancelled). The failure accounting runs after the
// turn's summary is written, so the test waits for the summaries and then gives the
// bad state, if the guard is gone, time to appear before it looks.
func TestChatDispatch_CancelledResumedTurnsDoNotForgetTheSession(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, workDir, _ := newRoutingServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)
	const conv = "conv-cancel-resume"

	dispatchTurn := func(sess string) {
		t.Helper()
		if code, body := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": conv, "sessionId": sess}); code != http.StatusAccepted {
			t.Fatalf("dispatch %s: %d %s", sess, code, body)
		}
	}

	// Turn 0 stores the session.
	fake.set(t, "ok", "sess-A")
	dispatchTurn("s-0")
	waitUntil(t, "turn 0 to store its session id", func() bool { return store.NativeSession(conv, "claude", "alice") == "sess-A" })
	waitForTurns(t, store, conv, 1)

	// Two resumed turns, each cancelled while claude is running.
	fake.set(t, "hang", "unused")
	for i := 1; i <= 2; i++ {
		sess := "s-cancel-" + string(rune('0'+i))
		dispatchTurn(sess)
		want := i + 1
		waitUntil(t, "the resumed claude to start", func() bool { return len(fake.calls(t)) >= want })
		resp := post(t, ts.URL+"/api/chat/cancel", tok, `{"sessionId":"`+sess+`","operatorId":"alice"}`)
		drainClose(resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cancel %s: status %d", sess, resp.StatusCode)
		}
		waitForTurns(t, store, conv, 1+i) // the cancelled turn's summary is written
	}

	// If the guard were gone, the failure accounting now runs and, after the second
	// cancelled turn, forgets the session. Give it time to show.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.NativeSession(conv, "claude", "alice") != "sess-A" || store.ResumeFailures(conv, "claude", "alice") != 0 {
			t.Fatalf("cancelled turns were counted as failed resumes: session %q, %d failures",
				store.NativeSession(conv, "claude", "alice"), store.ResumeFailures(conv, "claude", "alice"))
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The next turn still resumes turn 0's session.
	fake.set(t, "ok", "sess-B")
	dispatchTurn("s-last")
	waitUntil(t, "the last turn to store its session id", func() bool { return store.NativeSession(conv, "claude", "alice") == "sess-B" })
	calls := fake.calls(t)
	if last := calls[len(calls)-1]; !hasResume(last, "sess-A") {
		t.Errorf("the turn after two cancelled turns did not resume the stored session: %v", last)
	}
	waitForTurns(t, store, conv, 4)
}
