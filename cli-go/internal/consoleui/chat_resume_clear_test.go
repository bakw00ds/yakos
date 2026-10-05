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
