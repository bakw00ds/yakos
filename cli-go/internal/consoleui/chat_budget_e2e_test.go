package consoleui_test

// chat_budget_e2e_test.go — K-136 (security review of #330): an interactive chat
// session is held to the same budget a one-shot turn is. Before this, both
// interactive engines skipped the pre-flight, so an agent in hard_stop was refused
// as a one-shot turn yet ran full turns when the pane was interactive: on a new
// session, on a dispatch to a live one, and on every follow-up send.
//
// The tests drive the real handlers over HTTP. The budget is a token limit (the
// primary unit): a fake claude, or a fake Agent SDK sidecar, reports 135 tokens a
// turn, so a limit of 100 trips after the first turn, for any billing class.

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
)

// fakeSDKSidecarScript speaks the sidecar protocol: ready, then one token and one
// summary (135 tokens of usage) per user turn.
const fakeSDKSidecarScript = `printf '%s\n' '{"v":1,"kind":"ready"}'
while read -r _line; do
  printf '%s\n' '{"v":1,"kind":"token","text":"reply"}'
  printf '%s\n' '{"v":1,"kind":"summary","totalCostUsd":0,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}'
done
`

// sdkFactory returns an SDK engine factory whose sidecar is the fake script, and
// the counter of how many times a sidecar process was built. The engine's own
// API-key gate (K-137) needs a key to be present, so the test sets a fake one.
func sdkFactory(t *testing.T) (*interactive.SDKEngineFactory, *atomic.Int32) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "fake-key-for-the-budget-tests")
	var spawns atomic.Int32
	var f interactive.SDKEngineFactory = func(p interactive.SDKEngineParams) (*interactive.SDKEngine, error) {
		return interactive.NewSDKEngineWithProvider(p, func() *exec.Cmd {
			spawns.Add(1)
			return exec.Command("sh", "-c", fakeSDKSidecarScript) //nolint:gosec
		})
	}
	return &f, &spawns
}

// limitClaude gives agent a token limit of 100 in the test's isolated state.
func (s ledgerServer) limit(t *testing.T, agent string) {
	t.Helper()
	if err := budget.SetTokenLimit(s.logDir, agent, 100, budget.Monthly); err != nil {
		t.Fatal(err)
	}
}

// resetWindow starts agent's window over, lifting a hard stop.
func (s ledgerServer) resetWindow(t *testing.T, agent string) {
	t.Helper()
	if _, err := budget.Reset(agent, budget.Options{StateDir: s.logDir}); err != nil {
		t.Fatal(err)
	}
}

// seedTokens writes one finished dispatch of n input tokens for agent through the
// real ledger writer, so the agent's spend stands without a turn having run.
func seedTokens(t *testing.T, agent string, n int64) {
	t.Helper()
	dispatch.NewAccount(dispatch.Request{AgentName: agent, Runtime: "claude", Project: "/p", Surface: dispatch.SurfaceREST}).
		Finish(dispatch.Result{Runtime: "claude", Usage: &cost.Usage{InputTokens: n}})
}

// nextError waits for the next "error" frame on the operator's stream and returns
// its text; every other frame on the way is skipped.
func nextError(t *testing.T, frames <-chan map[string]any, label string) string {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-frames:
			if !ok {
				t.Fatalf("%s: the stream closed before an error frame", label)
			}
			if ev["type"] == "error" {
				text, _ := ev["text"].(string)
				return text
			}
		case <-deadline:
			t.Fatalf("%s: no error frame within 15s: the pane would never learn why nothing ran", label)
		}
	}
}

// transcriptErrors returns the texts of the error entries in the conversation's
// stored transcript, which is what a page reload shows.
func (s ledgerServer) transcriptErrors(t *testing.T, conversationID string) []string {
	t.Helper()
	resp := get(t, s.ts.URL+"/api/chat/transcript?conversationId="+conversationID+"&operatorId=alice", s.tok)
	defer resp.Body.Close()
	var entries []consoleui.TranscriptEntry
	_ = json.NewDecoder(resp.Body).Decode(&entries)
	var out []string
	for _, e := range entries {
		if e.Role == consoleui.RoleError {
			out = append(out, e.Text)
		}
	}
	return out
}

func (s ledgerServer) noMoreEvents(t *testing.T, want int, why string) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	if ev := s.events(t); len(ev) != want {
		t.Fatalf("%s: the log has %d events, want %d: %v", why, len(ev), want, ev)
	}
}

const refusedPrefix = "dispatch failed: budget: dispatch refused: agent "

// A new interactive session for an agent at its hard stop is refused exactly as a
// one-shot turn is, before claude is started, and writes nothing.
func TestInteractiveChat_NewCLISessionAtAHardStopIsRefusedLikeAOneShot(t *testing.T) {
	s := newLedgerServer(t)
	s.limit(t, "claude")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := s.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond)

	// One one-shot turn is allowed (nothing spent yet) and spends 135 tokens.
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "seed", "sessionId": "sess-pf-seed", "operatorId": "alice", "conversationId": "conv-pf-seed",
	})
	resp.Body.Close()
	s.waitForEvents(t, 2)
	if got := s.launchCount(t); got != 1 {
		t.Fatalf("the seeding turn launches claude once, got %d", got)
	}

	// The same agent is now refused as a one-shot turn: this is the reference text.
	resp = s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "again", "sessionId": "sess-pf-os", "operatorId": "alice", "conversationId": "conv-pf-os",
	})
	resp.Body.Close()
	oneShot := nextError(t, frames, "one-shot")
	if !strings.HasPrefix(oneShot, refusedPrefix+`"claude"`) {
		t.Fatalf("the one-shot refusal = %q", oneShot)
	}

	// An interactive session for it is refused with the same text.
	resp = s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "hello", "sessionId": "sess-pf-cli", "operatorId": "alice",
		"conversationId": "conv-pf-cli", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pf-cli") })
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("the refusal is reported on the stream, as a one-shot's is: dispatch status %d", resp.StatusCode)
	}
	if got := nextError(t, frames, "interactive"); got != oneShot {
		t.Fatalf("the interactive refusal must have the one-shot's shape:\n got  %q\n want %q", got, oneShot)
	}
	if errs := s.transcriptErrors(t, "conv-pf-cli"); len(errs) != 1 || errs[0] != oneShot {
		t.Errorf("the refusal must be in the stored transcript for a page reload: %v", errs)
	}
	if got := s.launchCount(t); got != 1 {
		t.Errorf("claude was launched for a refused session (%d launches)", got)
	}
	s.noMoreEvents(t, 2, "a refused session writes nothing")

	// Control: the refusal was the budget's. Once the window is reset the same
	// request starts the session and runs its first turn.
	s.resetWindow(t, "claude")
	resp = s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "hello", "sessionId": "sess-pf-cli2", "operatorId": "alice",
		"conversationId": "conv-pf-cli2", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pf-cli2") })
	s.waitForEvents(t, 4)
	if got := s.launchCount(t); got != 2 {
		t.Errorf("with the budget clear the session starts: %d launches", got)
	}
}

// A follow-up send to a live CLI session whose agent has since reached its hard
// stop is refused with a 429 and the same text, delivers nothing to claude, and
// leaves the session alive for when the budget is raised or reset.
func TestInteractiveChat_FollowUpSendToACLISessionAtAHardStopIsRefused(t *testing.T) {
	s := newLedgerServer(t)
	s.limit(t, "claude")

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "first", "sessionId": "sess-pf-send", "operatorId": "alice",
		"conversationId": "conv-pf-send", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pf-send") })
	s.waitForEvents(t, 2) // the first turn ran and spent 135 tokens: over the limit of 100

	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-pf-send", "operatorId": "alice", "sessionId": "sess-pf-send", "text": "second",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a follow-up at a hard stop: status %d, want 429", resp.StatusCode)
	}
	body := strings.TrimSpace(readBody(t, resp))
	if !strings.HasPrefix(body, refusedPrefix+`"claude"`) {
		t.Fatalf("the pane shows the response body, so it must say why: %q", body)
	}
	if errs := s.transcriptErrors(t, "conv-pf-send"); len(errs) != 1 || errs[0] != body {
		t.Errorf("the refusal is in the stored transcript: %v", errs)
	}
	s.noMoreEvents(t, 2, "a refused follow-up is not a turn and writes nothing")

	// Only the owner learns the budget state: another operator gets the engine's 403.
	resp = s.post(t, "/api/chat/send", map[string]any{"conversationId": "conv-pf-send", "operatorId": "mallory", "text": "peek"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("a send from another operator: status %d, want 403 (not the owner's budget refusal)", resp.StatusCode)
	}
	if text := readBody(t, resp); strings.Contains(text, "budget") {
		t.Errorf("another operator must not learn the owner's budget state: %q", text)
	}

	// Control: the same follow-up is delivered, on the same live process, once the
	// window is reset.
	s.resetWindow(t, "claude")
	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-pf-send", "operatorId": "alice", "sessionId": "sess-pf-send", "text": "second",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("with the budget clear the follow-up is accepted, got %d", resp.StatusCode)
	}
	s.waitForEvents(t, 4)
	if got := s.launchCount(t); got != 1 {
		t.Errorf("the follow-up reuses the live process: %d launches", got)
	}
}

// A second dispatch on a live conversation cannot name another agent to dodge the
// limit: the session keeps the persona it started with, so its turns are held to
// that agent's budget.
func TestInteractiveChat_DispatchOnALiveSessionIsHeldToTheSessionsAgent(t *testing.T) {
	s := newLedgerServer(t)
	s.limit(t, "alpha") // beta has no limit at all
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := s.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond)

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "alpha", "runtime": "claude", "task": "first", "sessionId": "sess-pin-1", "operatorId": "alice",
		"conversationId": "conv-pin", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pin") })
	s.waitForEvents(t, 2) // alpha spent 135 tokens: at its hard stop

	// The client names beta, which is in budget, on the same live conversation.
	resp = s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "beta", "runtime": "claude", "task": "relabelled", "sessionId": "sess-pin-2", "operatorId": "alice",
		"conversationId": "conv-pin", "interactive": true,
	})
	resp.Body.Close()
	got := nextError(t, frames, "relabelled dispatch")
	if !strings.HasPrefix(got, refusedPrefix+`"alpha"`) {
		t.Fatalf("the turn runs in alpha's session, so alpha's budget refuses it: %q", got)
	}
	s.noMoreEvents(t, 2, "the refused turn writes nothing")
	if n := s.launchCount(t); n != 1 {
		t.Errorf("%d launches", n)
	}
}

// With no limit in the way, a second dispatch that names another agent on a live
// conversation is still accounted to the agent the session started as.
func TestInteractiveChat_DispatchOnALiveSessionIsAccountedToTheSessionsAgent(t *testing.T) {
	s := newLedgerServer(t)

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "alpha", "runtime": "claude", "task": "first", "sessionId": "sess-pin2-1", "operatorId": "alice",
		"conversationId": "conv-pin2", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pin2") })
	s.waitForEvents(t, 2)

	resp = s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "beta", "runtime": "claude", "task": "second, as beta", "sessionId": "sess-pin2-2", "operatorId": "alice",
		"conversationId": "conv-pin2", "interactive": true,
	})
	resp.Body.Close()
	ev := s.waitForEvents(t, 4)
	time.Sleep(200 * time.Millisecond)
	if ev = s.events(t); len(ev) != 4 {
		t.Fatalf("two turns, two pairs: %v", ev)
	}
	for i, e := range ev {
		if e["agent"] != "alpha" {
			t.Errorf("event %d is attributed to %v: the session is alpha's, whatever a later dispatch names", i, e["agent"])
		}
	}
	if ev[2]["task_preview"] != "second, as beta" {
		t.Errorf("the second turn is still its own pair: %v", ev[2])
	}
}

// A new Agent SDK session for an agent at its hard stop is refused with the
// one-shot's text and starts no sidecar. Without the pre-flight the sidecar would
// start and run the turn.
func TestInteractiveChat_NewSDKSessionAtAHardStopIsRefused(t *testing.T) {
	factory, spawns := sdkFactory(t)
	s := newLedgerServerWith(t, factory)
	s.limit(t, "claude")
	seedTokens(t, "claude", 500) // over the limit of 100, without a turn having run
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := s.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond)

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "task": "hello", "sessionId": "sess-pf-sdk", "operatorId": "alice",
		"conversationId": "conv-pf-sdk", "interactive": true, "structuredQuestions": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pf-sdk") })
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch status %d, want 202 (the refusal is reported on the stream)", resp.StatusCode)
	}
	got := nextError(t, frames, "sdk dispatch")
	if !strings.HasPrefix(got, refusedPrefix+`"claude"`) || strings.Contains(got, "SDK start failed") {
		t.Fatalf("the refusal is the budget's, not a start failure: %q", got)
	}
	if errs := s.transcriptErrors(t, "conv-pf-sdk"); len(errs) != 1 || errs[0] != got {
		t.Errorf("the refusal is in the stored transcript: %v", errs)
	}
	if n := spawns.Load(); n != 0 {
		t.Errorf("the sidecar was built %d time(s) for a refused session", n)
	}
	s.noMoreEvents(t, 2, "only the seed is in the log") // started+finished written by seedTokens

	// Control: with the window reset the same request starts the sidecar and runs
	// its turn, so the refusal above was the budget's doing.
	s.resetWindow(t, "claude")
	resp = s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "task": "hello", "sessionId": "sess-pf-sdk2", "operatorId": "alice",
		"conversationId": "conv-pf-sdk2", "interactive": true, "structuredQuestions": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pf-sdk2") })
	s.waitForEvents(t, 4)
	if n := spawns.Load(); n != 1 {
		t.Errorf("with the budget clear the sidecar starts once: %d", n)
	}
}

// A follow-up send to a live Agent SDK session at its hard stop is refused, and
// delivers nothing to the sidecar.
func TestInteractiveChat_FollowUpSendToAnSDKSessionAtAHardStopIsRefused(t *testing.T) {
	factory, spawns := sdkFactory(t)
	s := newLedgerServerWith(t, factory)
	s.limit(t, "claude")

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "task": "first", "sessionId": "sess-pf-sdks", "operatorId": "alice",
		"conversationId": "conv-pf-sdks", "interactive": true, "structuredQuestions": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-pf-sdks") })
	s.waitForEvents(t, 2) // the first turn ran and spent 135 tokens: over the limit of 100

	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-pf-sdks", "operatorId": "alice", "sessionId": "sess-pf-sdks", "text": "second",
	})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a follow-up at a hard stop: status %d, want 429", resp.StatusCode)
	}
	if body := strings.TrimSpace(readBody(t, resp)); !strings.HasPrefix(body, refusedPrefix+`"claude"`) {
		t.Fatalf("the response says why: %q", body)
	}
	s.noMoreEvents(t, 2, "a refused follow-up is not a turn")

	// Control: delivered on the same sidecar once the window is reset.
	s.resetWindow(t, "claude")
	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-pf-sdks", "operatorId": "alice", "sessionId": "sess-pf-sdks", "text": "second",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("with the budget clear the follow-up is accepted, got %d", resp.StatusCode)
	}
	s.waitForEvents(t, 4)
	if n := spawns.Load(); n != 1 {
		t.Errorf("the follow-up reuses the sidecar: %d spawns", n)
	}
}
