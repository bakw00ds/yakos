package consoleui_test

// chat_fleet_finished_test.go — K-136 (review of #330): the fleet.finished event of a chat
// dispatch carries how the dispatch ended. The handler's dispatch goroutine publishes it from
// a deferred closure that reads the dispatch's outcome (consoleui.dispatchOutcome): the status
// and the exit code come from the outcome, which the chunk callback fills from every summary
// and the goroutine from every failure path. Nothing asserted that payload, so a summary no
// longer recorded in the outcome, or a status hard-coded in the closure, left every test green.
//
// The tests subscribe to the bus the handlers publish on, drive the real handlers over HTTP
// against a fake `claude` or a fake Agent SDK sidecar (an `sh -c` script), and assert the
// payload: session, agent, status, exit code, a timestamp, and the routing metadata that keeps
// the event owner-scoped. Each dispatch ends with exactly one event.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// finishedSub subscribes to fleet.finished before anything is dispatched.
func (s ledgerServer) finishedSub(t *testing.T) *wsbus.Subscription {
	t.Helper()
	sub := s.bus.Subscribe(wsbus.TopicFleetFinished)
	t.Cleanup(sub.Unsubscribe)
	return sub
}

// waitFinished waits for the fleet.finished event of every named session and returns the
// payloads by session id, with the timestamp (checked to be set) cleared so a payload can be
// compared whole. It fails on an event for any other session, on a second event for one
// session, on routing metadata that is not "owned by alice, not shared", and on an event that
// arrives after the last one is in.
func (s ledgerServer) waitFinished(t *testing.T, sub *wsbus.Subscription, sessions ...string) map[string]wsbus.FleetFinishedPayload {
	t.Helper()
	want := map[string]bool{}
	for _, id := range sessions {
		want[id] = true
	}
	got := map[string]wsbus.FleetFinishedPayload{}
	deadline := time.After(15 * time.Second)
	for len(got) < len(want) {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				t.Fatalf("the bus closed before every fleet.finished arrived: have %v", got)
			}
			var p wsbus.FleetFinishedPayload
			if err := json.Unmarshal(ev.Payload, &p); err != nil {
				t.Fatalf("fleet.finished payload %q: %v", ev.Payload, err)
			}
			if !want[p.SessionID] {
				t.Fatalf("fleet.finished for a session nobody dispatched here: %+v", p)
			}
			if _, dup := got[p.SessionID]; dup {
				t.Fatalf("fleet.finished twice for session %q", p.SessionID)
			}
			if ev.Meta == nil || ev.Meta.OwnerOperatorID != "alice" || ev.Meta.Shared {
				t.Errorf("fleet.finished for %q must be scoped to its owner and not shared: meta %+v", p.SessionID, ev.Meta)
			}
			if p.TS.IsZero() {
				t.Errorf("fleet.finished for %q has no timestamp", p.SessionID)
			}
			p.TS = time.Time{}
			got[p.SessionID] = p
		case <-deadline:
			t.Fatalf("fleet.finished did not arrive for every session within 15s: want %v, have %v", sessions, got)
		}
	}
	select {
	case ev := <-sub.C():
		t.Errorf("an extra fleet.finished after the last one: %s", ev.Payload)
	case <-time.After(300 * time.Millisecond):
	}
	return got
}

// failingTurnScript is a fake claude that answers every user frame with a failed result frame
// (is_error true), which the interactive engine reports as a summary with exit code 1.
const failingTurnScript = `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"ses_FIXTURE_0001","model":"claude-sonnet-4-5-20250929"}'
while IFS= read -r line; do
  printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"duration_ms":100,"session_id":"ses_FIXTURE_0001","total_cost_usd":0,"usage":{"input_tokens":10,"output_tokens":5}}'
done
`

// exitingClaudeScript is a one-shot fake claude whose run ends with exit status 3 after a
// failed result frame, so the one-shot summary carries exit code 3.
const exitingClaudeScript = `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"ses_FIXTURE_0001","model":"claude-sonnet-4-5-20250929"}'
printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"duration_ms":100,"session_id":"ses_FIXTURE_0001","total_cost_usd":0,"usage":{"input_tokens":1,"output_tokens":1}}'
exit 3
`

// dispatchChat posts a chat dispatch and requires the 202 a dispatch that starts is given
// (a refusal is reported on the stream, after the 202).
func (s ledgerServer) dispatchChat(t *testing.T, body map[string]any) {
	t.Helper()
	resp := s.post(t, "/api/chat/dispatch", body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch %v: %d", body["sessionId"], resp.StatusCode)
	}
}

// A one-shot dispatch ends with the exit code of its summary and, when it fails before or
// without a summary, with -1. The turn that exits non-zero is failed with that code (this is
// the chunk callback's write of the outcome); a turn the budget refuses is failed with -1 (the
// goroutine's own failure path); a clean turn is finished with 0.
func TestFleetFinished_OneShotPayloads(t *testing.T) {
	cases := []struct {
		name   string
		script string // "" is the default fake claude, which answers once and exits 0
		refuse bool   // put the agent at its token hard stop first
		want   wsbus.FleetFinishedPayload
	}{
		{name: "a clean turn", want: wsbus.FleetFinishedPayload{Status: "finished", ExitCode: 0}},
		{name: "a turn that exits non-zero", script: exitingClaudeScript, want: wsbus.FleetFinishedPayload{Status: "failed", ExitCode: 3}},
		{name: "a turn the budget refuses", refuse: true, want: wsbus.FleetFinishedPayload{Status: "failed", ExitCode: -1}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newLedgerServerScript(t, nil, tc.script)
			if tc.refuse {
				s.limit(t, "claude")
				seedTokens(t, "claude", 500)
			}
			sub := s.finishedSub(t)
			session := "sess-ff-os-" + string(rune('a'+i))
			s.dispatchChat(t, map[string]any{
				"agent": "claude", "runtime": "claude", "task": "question", "sessionId": session,
				"operatorId": "alice", "conversationId": "conv-" + session,
			})
			tc.want.SessionID, tc.want.Agent = session, "claude"
			if got := s.waitFinished(t, sub, session)[session]; got != tc.want {
				t.Errorf("fleet.finished = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// An interactive CLI session lives until it is closed, and its dispatch ends then: finished
// with 0 after clean turns, failed with 1 after a turn whose result frame says is_error (the
// summary's exit code, written to the outcome by the chunk callback), failed with -1 when the
// budget refuses the start, which ends the dispatch at once.
func TestFleetFinished_InteractiveCLIPayloads(t *testing.T) {
	t.Run("a clean turn", func(t *testing.T) {
		s := newLedgerServer(t)
		sub := s.finishedSub(t)
		s.dispatchChat(t, map[string]any{
			"agent": "claude", "runtime": "claude", "task": "question", "sessionId": "sess-ff-cli-ok",
			"operatorId": "alice", "conversationId": "conv-ff-cli-ok", "interactive": true,
		})
		s.waitForEvents(t, 2) // the turn's ledger pair: its summary has been handled
		s.mgr.Close("conv-ff-cli-ok")
		want := wsbus.FleetFinishedPayload{SessionID: "sess-ff-cli-ok", Agent: "claude", Status: "finished", ExitCode: 0}
		if got := s.waitFinished(t, sub, "sess-ff-cli-ok")["sess-ff-cli-ok"]; got != want {
			t.Errorf("fleet.finished = %+v, want %+v", got, want)
		}
	})

	t.Run("a turn whose result says is_error", func(t *testing.T) {
		s := newLedgerServerScript(t, nil, failingTurnScript)
		sub := s.finishedSub(t)
		s.dispatchChat(t, map[string]any{
			"agent": "claude", "runtime": "claude", "task": "question", "sessionId": "sess-ff-cli-err",
			"operatorId": "alice", "conversationId": "conv-ff-cli-err", "interactive": true,
		})
		ev := s.waitForEvents(t, 2)
		if ev[1]["exit_code"] != float64(1) {
			t.Fatalf("the failed turn is finished in the ledger with exit code 1: %v", ev[1])
		}
		s.mgr.Close("conv-ff-cli-err")
		want := wsbus.FleetFinishedPayload{SessionID: "sess-ff-cli-err", Agent: "claude", Status: "failed", ExitCode: 1}
		if got := s.waitFinished(t, sub, "sess-ff-cli-err")["sess-ff-cli-err"]; got != want {
			t.Errorf("fleet.finished = %+v, want %+v", got, want)
		}
	})

	t.Run("a start the budget refuses", func(t *testing.T) {
		s := newLedgerServer(t)
		s.limit(t, "claude")
		seedTokens(t, "claude", 500)
		sub := s.finishedSub(t)
		s.dispatchChat(t, map[string]any{
			"agent": "claude", "runtime": "claude", "task": "question", "sessionId": "sess-ff-cli-ref",
			"operatorId": "alice", "conversationId": "conv-ff-cli-ref", "interactive": true,
		})
		want := wsbus.FleetFinishedPayload{SessionID: "sess-ff-cli-ref", Agent: "claude", Status: "failed", ExitCode: -1}
		if got := s.waitFinished(t, sub, "sess-ff-cli-ref")["sess-ff-cli-ref"]; got != want {
			t.Errorf("fleet.finished = %+v, want %+v", got, want)
		}
	})
}

// The Agent SDK engine ends its dispatches the same way, through the same closure: its
// summary frames always carry exit code 0, so a clean session is finished with 0, and a start
// the budget refuses is failed with -1.
func TestFleetFinished_InteractiveSDKPayloads(t *testing.T) {
	t.Run("a clean turn", func(t *testing.T) {
		factory, _ := sdkFactory(t)
		s := newLedgerServerWith(t, factory)
		sub := s.finishedSub(t)
		s.dispatchChat(t, map[string]any{
			"agent": "claude", "task": "question", "sessionId": "sess-ff-sdk-ok", "operatorId": "alice",
			"conversationId": "conv-ff-sdk-ok", "interactive": true, "structuredQuestions": true,
		})
		s.waitForEvents(t, 2)
		s.mgr.Close("conv-ff-sdk-ok")
		want := wsbus.FleetFinishedPayload{SessionID: "sess-ff-sdk-ok", Agent: "claude", Status: "finished", ExitCode: 0}
		if got := s.waitFinished(t, sub, "sess-ff-sdk-ok")["sess-ff-sdk-ok"]; got != want {
			t.Errorf("fleet.finished = %+v, want %+v", got, want)
		}
	})

	t.Run("a start the budget refuses", func(t *testing.T) {
		factory, _ := sdkFactory(t)
		s := newLedgerServerWith(t, factory)
		s.limit(t, "claude")
		seedTokens(t, "claude", 500)
		sub := s.finishedSub(t)
		s.dispatchChat(t, map[string]any{
			"agent": "claude", "task": "question", "sessionId": "sess-ff-sdk-ref", "operatorId": "alice",
			"conversationId": "conv-ff-sdk-ref", "interactive": true, "structuredQuestions": true,
		})
		want := wsbus.FleetFinishedPayload{SessionID: "sess-ff-sdk-ref", Agent: "claude", Status: "failed", ExitCode: -1}
		if got := s.waitFinished(t, sub, "sess-ff-sdk-ref")["sess-ff-sdk-ref"]; got != want {
			t.Errorf("fleet.finished = %+v, want %+v", got, want)
		}
	})
}

// Each dispatch has an outcome of its own. A second dispatch to a live conversation whose
// frame the engine refuses fails (-1, "send failed") while the session lives on, and the
// dispatch that started the session, which ran a clean turn, finishes clean: closing the
// session ends both goroutines, and each publishes its own payload.
func TestFleetFinished_EachDispatchOfASessionHasItsOwnOutcome(t *testing.T) {
	for _, engine := range []string{"cli", "sdk"} {
		t.Run(engine, func(t *testing.T) {
			wrap := func(real consoleui.InteractiveSender) consoleui.InteractiveSender { return refusingSender{real: real} }
			conv := "conv-ff-rf-" + engine
			body := map[string]any{"agent": "claude", "runtime": "claude", "operatorId": "alice", "conversationId": conv, "interactive": true}
			var s ledgerServer
			if engine == "sdk" {
				factory, _ := sdkFactory(t)
				s = newLedgerServerOpts(t, factory, "", wrap)
				body["structuredQuestions"] = true
			} else {
				s = newLedgerServerOpts(t, nil, "", wrap)
			}
			t.Cleanup(func() { s.mgr.Close(conv) })
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			frames := s.sseFrames(t, ctx, "alice")
			time.Sleep(100 * time.Millisecond)
			sub := s.finishedSub(t)

			dispatchTask := func(task, session string) {
				b := map[string]any{"task": task, "sessionId": session}
				for k, v := range body {
					b[k] = v
				}
				s.dispatchChat(t, b)
			}
			first, second := "sess-ff-rf-1-"+engine, "sess-ff-rf-2-"+engine
			dispatchTask("first", first)
			s.waitForEvents(t, 2)
			dispatchTask("refuse-me", second)
			if got := nextError(t, frames, "refused dispatch"); !strings.Contains(got, "send failed") {
				t.Fatalf("the refused frame is reported: %q", got)
			}
			s.mgr.Close(conv)

			got := s.waitFinished(t, sub, first, second)
			if want := (wsbus.FleetFinishedPayload{SessionID: first, Agent: "claude", Status: "finished", ExitCode: 0}); got[first] != want {
				t.Errorf("the dispatch that ran a clean turn: fleet.finished = %+v, want %+v", got[first], want)
			}
			if want := (wsbus.FleetFinishedPayload{SessionID: second, Agent: "claude", Status: "failed", ExitCode: -1}); got[second] != want {
				t.Errorf("the dispatch whose frame was refused: fleet.finished = %+v, want %+v", got[second], want)
			}
		})
	}
}
