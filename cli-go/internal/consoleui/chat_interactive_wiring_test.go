package consoleui_test

// chat_interactive_wiring_test.go: handler-level tests of what an interactive chat
// session leaves behind (K-136, review of #330): each turn's own text in the stored
// transcript, the turn in flight finished as failed when its session closes, and an
// Agent SDK turn accounted with the sidecar's tokens. They drive the real handlers over
// HTTP against a fake `claude` or a fake sidecar process (an `sh -c` script, no node and
// no bundle: interactive.NewSDKEngineWithProvider is the seam).

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/interactive"
)

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// assistantEntries waits until the conversation's stored transcript holds n assistant
// entries, lets any extra one arrive, and returns their texts.
func (s ledgerServer) assistantEntries(t *testing.T, conversationID string, n int) []string {
	t.Helper()
	read := func() []string {
		entries, err := consoleui.NewTranscripts(s.workDir).Read(conversationID, "alice")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			if e.Role == consoleui.RoleAssistant {
				out = append(out, e.Text)
			}
		}
		return out
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(read()) >= n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	return read()
}

// An interactive session emits a summary for every turn, and each turn's assistant text is
// stored as its own entry. The text buffer was never emptied, so every entry after the first
// repeated the turns before it ("reply", then "replyreply"), and the stored transcript, which
// the history endpoint serves, was wrong for good.
func TestInteractiveChat_TranscriptHoldsEachTurnsOwnText(t *testing.T) {
	s := newLedgerServer(t)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "first question", "sessionId": "sess-tr-1",
		"operatorId": "alice", "conversationId": "conv-tr-1", "interactive": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	t.Cleanup(func() { s.mgr.Close("conv-tr-1") })
	s.waitForEvents(t, 2)
	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-tr-1", "operatorId": "alice", "sessionId": "sess-tr-1", "text": "second question",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	s.waitForEvents(t, 4)
	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-tr-1", "operatorId": "alice", "sessionId": "sess-tr-1", "text": "third question",
	})
	resp.Body.Close()
	s.waitForEvents(t, 6)

	// The fake claude answers every turn with exactly "reply".
	got := s.assistantEntries(t, "conv-tr-1", 3)
	if len(got) != 3 {
		t.Fatalf("want one assistant entry per turn (3), got %d: %q", len(got), got)
	}
	for i, a := range got {
		if a != "reply" {
			t.Errorf("assistant entry %d = %q, want only that turn's own text %q", i+1, a, "reply")
		}
	}
}

// The same for the Agent SDK engine, which already emitted a summary per turn.
func TestInteractiveChat_SDKTranscriptHoldsEachTurnsOwnText(t *testing.T) {
	factory, _ := sdkFactory(t)
	s := newLedgerServerWith(t, factory)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "task": "first question", "sessionId": "sess-tr-2", "operatorId": "alice",
		"conversationId": "conv-tr-2", "interactive": true, "structuredQuestions": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	t.Cleanup(func() { s.mgr.Close("conv-tr-2") })
	s.waitForEvents(t, 2)
	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-tr-2", "operatorId": "alice", "sessionId": "sess-tr-2", "text": "second question",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	s.waitForEvents(t, 4)
	got := s.assistantEntries(t, "conv-tr-2", 2)
	if len(got) != 2 || got[0] != "reply" || got[1] != "reply" {
		t.Fatalf("each SDK turn's entry is only its own text: %q", got)
	}
}

// A turn the CLI session never answers is finished as failed when the session closes, so
// the pair is never left half-written. Without the close-time flush the turn leaves nothing.
func TestInteractiveChat_CLISessionClosedMidTurnFinishesTheTurnAsFailed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "got-frame")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"ses_FIXTURE_0001\",\"model\":\"claude-sonnet-4-5-20250929\"}'\n" +
		"read -r line\n: > " + marker + "\nsleep 60\n"
	s := newLedgerServerScript(t, nil, script)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "never answered", "sessionId": "sess-cl-1",
		"operatorId": "alice", "conversationId": "conv-cl-1", "interactive": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	waitForFile(t, marker) // the engine has the frame, so the turn is in flight
	s.mgr.Close("conv-cl-1")
	ev := s.waitForEvents(t, 2)
	if ev[0]["type"] != "dispatch_started" || ev[0]["task_preview"] != "never answered" {
		t.Errorf("started = %v", ev[0])
	}
	fin := ev[1]
	tail, _ := fin["stderr_tail"].(string)
	if fin["type"] != "dispatch_finished" || fin["exit_code"] != float64(-1) || !strings.Contains(tail, "closed before the turn finished") || fin["surface"] != "console-chat" {
		t.Errorf("finished = %v", fin)
	}
}

// The same for the Agent SDK engine.
func TestInteractiveChat_SDKSessionClosedMidTurnFinishesTheTurnAsFailed(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "got-frame")
	sidecar := "printf '%s\\n' '{\"v\":1,\"kind\":\"ready\"}'\nread -r _line\n: > " + marker + "\nsleep 60\n"
	factory, _ := sdkFactoryWith(t, sidecar)
	s := newLedgerServerWith(t, factory)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "task": "sdk never answered", "sessionId": "sess-cl-2", "operatorId": "alice",
		"conversationId": "conv-cl-2", "interactive": true, "structuredQuestions": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	waitForFile(t, marker)
	s.mgr.Close("conv-cl-2")
	ev := s.waitForEvents(t, 2)
	fin := ev[1]
	tail, _ := fin["stderr_tail"].(string)
	if fin["type"] != "dispatch_finished" || fin["exit_code"] != float64(-1) || !strings.Contains(tail, "closed before the turn finished") {
		t.Errorf("finished = %v", fin)
	}
}

// An Agent SDK turn is accounted end to end: the turn's own task, the sidecar's tokens and
// cost, billing api (the engine needs a key), surface console-chat. One pair per turn.
func TestInteractiveChat_SDKTurnIsAccountedAsAnAPITurnWithTokens(t *testing.T) {
	sidecar := `printf '%s\n' '{"v":1,"kind":"ready"}'
while read -r _line; do
  printf '%s\n' '{"v":1,"kind":"token","text":"reply"}'
  printf '%s\n' '{"v":1,"kind":"summary","totalCostUsd":0.5,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}'
done
`
	factory, _ := sdkFactoryWith(t, sidecar)
	s := newLedgerServerWith(t, factory)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "task": "sdk question", "sessionId": "sess-acct-1", "operatorId": "alice",
		"conversationId": "conv-acct-1", "interactive": true, "structuredQuestions": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	t.Cleanup(func() { s.mgr.Close("conv-acct-1") })
	s.waitForEvents(t, 2)
	time.Sleep(200 * time.Millisecond)
	ev := s.events(t)
	if len(ev) != 2 {
		t.Fatalf("one SDK turn, one pair; got %d: %v", len(ev), ev)
	}
	fin := ev[1]
	u, _ := fin["usage"].(map[string]any)
	if fin["type"] != "dispatch_finished" || fin["billing"] != "api" || fin["surface"] != "console-chat" || fin["runtime"] != "claude" ||
		u["input_tokens"] != float64(10) || u["output_tokens"] != float64(5) || u["cache_read"] != float64(100) ||
		u["cache_creation"] != float64(20) || u["total_cost_usd"] != float64(0.5) {
		t.Errorf("finished = %v", fin)
	}
	if ev[0]["task_preview"] != "sdk question" {
		t.Errorf("started = %v", ev[0])
	}
}

// refusingSender sits in front of the real manager and refuses any frame whose text holds
// "refuse-me", as an engine does when it cannot take a turn (ErrTurnInFlight, a closed
// session). Everything else goes to the manager.
type refusingSender struct{ real consoleui.InteractiveSender }

func (r refusingSender) Send(conversationID, ownerOperatorID string, frame []byte) error {
	if strings.Contains(string(frame), "refuse-me") {
		return interactive.ErrTurnInFlight
	}
	return r.real.Send(conversationID, ownerOperatorID, frame)
}

// A turn the engine refuses on a dispatch to a live conversation is dropped from the ledger
// and leaves no event, and it must not linger as a pending turn: the next result would be
// pinned on it and the next real turn accounted under the refused turn's task.
func TestInteractiveChat_DispatchFrameTheEngineRefusesIsNotAccounted(t *testing.T) {
	for _, engine := range []string{"cli", "sdk"} {
		t.Run(engine, func(t *testing.T) {
			wrap := func(real consoleui.InteractiveSender) consoleui.InteractiveSender { return refusingSender{real: real} }
			body := map[string]any{"agent": "claude", "runtime": "claude", "operatorId": "alice", "conversationId": "conv-rf-" + engine, "interactive": true}
			var s ledgerServer
			if engine == "sdk" {
				factory, _ := sdkFactory(t)
				s = newLedgerServerOpts(t, factory, "", wrap)
				body["structuredQuestions"] = true
			} else {
				s = newLedgerServerOpts(t, nil, "", wrap)
			}
			conv := "conv-rf-" + engine
			t.Cleanup(func() { s.mgr.Close(conv) })
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			frames := s.sseFrames(t, ctx, "alice")
			time.Sleep(100 * time.Millisecond)

			dispatchTask := func(task, session string) {
				b := map[string]any{"task": task, "sessionId": session}
				for k, v := range body {
					b[k] = v
				}
				resp := s.post(t, "/api/chat/dispatch", b)
				resp.Body.Close()
				if resp.StatusCode != http.StatusAccepted {
					t.Fatalf("dispatch %q: %d", task, resp.StatusCode)
				}
			}
			dispatchTask("first", "sess-rf-1-"+engine)
			s.waitForEvents(t, 2)

			dispatchTask("refuse-me", "sess-rf-2-"+engine)
			if got := nextError(t, frames, "refused dispatch"); !strings.Contains(got, "send failed") {
				t.Fatalf("the refused frame is reported: %q", got)
			}
			s.noMoreEvents(t, 2, "a frame the engine refused is not a turn")

			resp := s.post(t, "/api/chat/send", map[string]any{
				"conversationId": conv, "operatorId": "alice", "sessionId": "sess-rf-1-" + engine, "text": "third",
			})
			resp.Body.Close()
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("send: %d", resp.StatusCode)
			}
			ev := s.waitForEvents(t, 4)
			time.Sleep(200 * time.Millisecond)
			if ev = s.events(t); len(ev) != 4 || ev[2]["type"] != "dispatch_started" || ev[2]["task_preview"] != "third" {
				t.Fatalf("the next real turn is accounted under its own task, not a refused turn's: %v", ev)
			}
		})
	}
}
