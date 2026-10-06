package consoleui

// chat_account_test.go — unit tests of the interactive turn ledger (K-136): which
// turns write an event pair, in what order, and what they carry. The HTTP-level
// behaviour is in chat_account_e2e_test.go.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
)

func ledgerEvents(t *testing.T, dir string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "dispatch-log.ndjson"))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if l == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func newTestLedger(t *testing.T) (*turnLedger, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)
	l := newTurnLedger()
	l.register("conv", dispatch.Request{
		AgentName: "backend", Project: "/p", Runtime: "claude", ModelResolved: "sonnet",
		OperatorID: "alice", ConversationID: "conv", SessionID: "sess", Surface: dispatch.SurfaceConsoleChat,
	})
	return l, dir
}

func cliSummary(in, out, cacheRead, cacheCreate int64, usd float64) dispatch.StreamChunk {
	return dispatch.StreamChunk{
		Type: "summary", RuntimeResolved: "claude", TotalCostUSD: usd, NativeSessionID: "ses_FIXTURE_0001",
		ModelResolved: "claude-sonnet-4-5-20250929",
		Usage:         &cost.Usage{InputTokens: in, OutputTokens: out, CacheRead: cacheRead, CacheCreation: cacheCreate, TotalCostUSD: usd},
	}
}

// Nothing is written when a turn begins; the pair is written when it ends, started
// first and carrying the turn's task and its own output size.
func TestTurnLedger_WritesThePairAtTheEndOfTheTurn(t *testing.T) {
	l, dir := newTestLedger(t)
	turn := l.begin("conv", "what is two plus two", "", "alice")
	if turn == nil {
		t.Fatal("begin returned nil for a registered conversation")
	}
	if ev := ledgerEvents(t, dir); len(ev) != 0 {
		t.Fatalf("beginning a turn writes nothing: %v", ev)
	}
	l.noteOutput("conv", 3)
	l.noteOutput("conv", 4)
	l.finish("conv", cliSummary(10, 5, 100, 20, 0.25))

	ev := ledgerEvents(t, dir)
	if len(ev) != 2 || ev[0]["type"] != "dispatch_started" || ev[1]["type"] != "dispatch_finished" {
		t.Fatalf("want a started/finished pair, got %v", ev)
	}
	if ev[0]["task_preview"] != "what is two plus two" || ev[0]["agent"] != "backend" || ev[0]["conversation_id"] != "conv" {
		t.Errorf("started = %v", ev[0])
	}
	f := ev[1]
	if f["output_bytes"] != float64(7) || f["task_bytes"] != float64(len("what is two plus two")) || f["surface"] != "console-chat" {
		t.Errorf("finished = %v", f)
	}
	if f["billing"] != "subscription" || f["model_id"] != "claude-sonnet-4-5-20250929" || f["native_session_id"] != "ses_FIXTURE_0001" {
		t.Errorf("ledger fields = %v", f)
	}
}

// Turns are answered in order: two begun turns finish oldest-first, each with its
// own task and its own summary.
func TestTurnLedger_TurnsFinishInOrder(t *testing.T) {
	l, dir := newTestLedger(t)
	l.begin("conv", "one", "", "alice")
	l.begin("conv", "two", "other-tab", "alice")
	l.finish("conv", cliSummary(1, 1, 0, 0, 0))
	l.finish("conv", cliSummary(2, 2, 0, 0, 0))
	ev := ledgerEvents(t, dir)
	if len(ev) != 4 {
		t.Fatalf("two turns, two pairs: %v", ev)
	}
	if ev[0]["task_preview"] != "one" || ev[2]["task_preview"] != "two" {
		t.Errorf("tasks out of order: %v / %v", ev[0]["task_preview"], ev[2]["task_preview"])
	}
	if u, _ := ev[1]["usage"].(map[string]any); u["input_tokens"] != float64(1) {
		t.Errorf("first turn's usage: %v", u)
	}
	if u, _ := ev[3]["usage"].(map[string]any); u["input_tokens"] != float64(2) {
		t.Errorf("second turn's usage: %v", u)
	}
	if ev[3]["session_id"] != "other-tab" {
		t.Errorf("a follow-up from another tab is attributed to that session: %v", ev[3]["session_id"])
	}
}

// A turn whose frame was not delivered is dropped and leaves nothing.
func TestTurnLedger_DroppedTurnLeavesNothing(t *testing.T) {
	l, dir := newTestLedger(t)
	turn := l.begin("conv", "refused", "", "alice")
	l.drop("conv", turn)
	l.drop("conv", nil) // tolerated
	l.finish("conv", cliSummary(0, 0, 0, 0, 0))
	if ev := ledgerEvents(t, dir); len(ev) != 0 {
		t.Fatalf("a dropped turn writes nothing, and an empty result with no turn is ignored: %v", ev)
	}
}

// A result with no turn waiting is a turn of its own when it used tokens or cost
// (claude can start one by itself) and is ignored when it used none (a handshake).
func TestTurnLedger_UnsolicitedResult(t *testing.T) {
	l, dir := newTestLedger(t)
	l.finish("conv", cliSummary(0, 0, 0, 0, 0))
	if ev := ledgerEvents(t, dir); len(ev) != 0 {
		t.Fatalf("an empty result is a handshake: %v", ev)
	}
	l.finish("conv", cliSummary(7, 3, 0, 0, 0))
	if ev := ledgerEvents(t, dir); len(ev) != 2 || ev[1]["type"] != "dispatch_finished" {
		t.Fatalf("a result that used tokens is accounted: %v", ev)
	}
}

// The Agent SDK sidecar reports a turn's dollar cost and no token counts; the cost
// is kept (the sidecar is API-key billed, so it is spend when a key is present).
func TestTurnLedger_CostOnlySummary(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "fake-key-for-billing-detection")
	l, dir := newTestLedger(t)
	l.begin("conv", "sdk turn", "", "alice")
	l.finish("conv", dispatch.StreamChunk{Type: "summary", TotalCostUSD: 0.75})
	ev := ledgerEvents(t, dir)
	if len(ev) != 2 {
		t.Fatalf("%v", ev)
	}
	if ev[1]["billing"] != "api" {
		t.Errorf("billing = %v", ev[1]["billing"])
	}
	if u, _ := ev[1]["usage"].(map[string]any); u["total_cost_usd"] != 0.75 || u["input_tokens"] != float64(0) {
		t.Errorf("usage = %v", u)
	}
}

// A session that dies under a running turn finishes it as failed, so the pair is
// never left half-written, and the conversation is forgotten.
func TestTurnLedger_ClosingASessionFinishesTurnsInFlightAsFailed(t *testing.T) {
	l, dir := newTestLedger(t)
	l.begin("conv", "never answered", "", "alice")
	l.noteOutput("conv", 9)
	l.closeConversation("conv")
	ev := ledgerEvents(t, dir)
	if len(ev) != 2 || ev[1]["exit_code"] != float64(-1) || ev[1]["output_bytes"] != float64(9) {
		t.Fatalf("an unanswered turn finishes failed: %v", ev)
	}
	if !strings.Contains(ev[1]["stderr_tail"].(string), "closed") {
		t.Errorf("stderr_tail = %v", ev[1]["stderr_tail"])
	}
	l.closeConversation("conv") // idempotent
	if l.begin("conv", "after close", "", "alice") != nil {
		t.Error("a closed conversation is forgotten: begin must return nil")
	}
	if got := ledgerEvents(t, dir); len(got) != 2 {
		t.Fatalf("closing twice writes nothing more: %v", got)
	}
}

// An unregistered conversation is not accounted: there is no agent to attribute it to.
func TestTurnLedger_UnregisteredConversationIsIgnored(t *testing.T) {
	l, dir := newTestLedger(t)
	if l.begin("unknown", "x", "", "alice") != nil {
		t.Fatal("begin for an unregistered conversation must return nil")
	}
	l.noteOutput("unknown", 5)
	l.finish("unknown", cliSummary(1, 1, 1, 1, 1))
	if ev := ledgerEvents(t, dir); len(ev) != 0 {
		t.Fatalf("%v", ev)
	}
}

// The turns remembered per conversation are bounded.
func TestTurnLedger_PendingTurnsAreBounded(t *testing.T) {
	l, _ := newTestLedger(t)
	n := 0
	for i := 0; i < maxPendingTurns+5; i++ {
		if l.begin("conv", "t", "", "alice") != nil {
			n++
		}
	}
	if n != maxPendingTurns {
		t.Fatalf("accepted %d turns, want the cap %d", n, maxPendingTurns)
	}
}

// The agent a session started as is the agent every later turn is accounted to
// and budget-checked as. A second register for a live conversation (a second
// dispatch on it, naming whatever agent the client likes) changes nothing.
func TestTurnLedger_RegisterPinsTheSessionsAgent(t *testing.T) {
	l, dir := newTestLedger(t)
	got := l.register("conv", dispatch.Request{
		AgentName: "someone-else", Project: "/other", Runtime: "codex", ModelResolved: "gpt",
		OperatorID: "mallory", ConversationID: "conv", Surface: dispatch.SurfaceConsoleChat,
	})
	if got.AgentName != "backend" || got.Runtime != "claude" || got.OperatorID != "alice" {
		t.Fatalf("register returned %+v, want the session's own template", got)
	}
	if agent, owner, ok := l.sessionOf("conv"); !ok || agent != "backend" || owner != "alice" {
		t.Fatalf("sessionOf = %q, %q, %v: the session stays pinned to the agent and owner it started with", agent, owner, ok)
	}
	l.begin("conv", "task", "", "alice")
	l.finish("conv", cliSummary(1, 1, 0, 0, 0))
	ev := ledgerEvents(t, dir)
	if len(ev) != 2 || ev[1]["agent"] != "backend" || ev[1]["runtime"] != "claude" || ev[1]["operator_id"] != "alice" {
		t.Fatalf("the turn is accounted to the pinned agent and runtime: %v", ev)
	}
}

// A conversation with no registered session has no pinned agent, and a closed one
// is forgotten (a new session may then start as another agent).
func TestTurnLedger_SessionOf(t *testing.T) {
	l, _ := newTestLedger(t)
	if _, _, ok := l.sessionOf("never-registered"); ok {
		t.Error("an unregistered conversation has no session")
	}
	l.closeConversation("conv")
	if _, _, ok := l.sessionOf("conv"); ok {
		t.Error("a closed conversation is forgotten")
	}
	got := l.register("conv", dispatch.Request{AgentName: "second", Runtime: "claude", OperatorID: "bob", ConversationID: "conv"})
	if got.AgentName != "second" {
		t.Fatalf("a new session after the old one closed registers afresh: %+v", got)
	}
	if agent, owner, ok := l.sessionOf("conv"); !ok || agent != "second" || owner != "bob" {
		t.Fatalf("sessionOf = %q, %q, %v", agent, owner, ok)
	}
}

// Only the session's owner can open a turn: the engine refuses anyone else's
// frame, and a refused frame must not sit pending where the owner's next result
// would be pinned on it.
func TestTurnLedger_BeginRefusesANonOwner(t *testing.T) {
	l, dir := newTestLedger(t)
	if l.begin("conv", "steal", "", "mallory") != nil {
		t.Fatal("begin must return nil for an operator who does not own the session")
	}
	if l.begin("conv", "anonymous", "", "") != nil {
		t.Fatal("begin must return nil for an empty operator on an owned session")
	}
	l.finish("conv", cliSummary(0, 0, 0, 0, 0)) // a handshake result: nothing may be waiting for it
	if ev := ledgerEvents(t, dir); len(ev) != 0 {
		t.Fatalf("a refused turn leaves nothing: %v", ev)
	}
	// The owner's own turn is accepted and is accounted under its own task.
	if l.begin("conv", "mine", "", "alice") == nil {
		t.Fatal("the owner's turn must be accepted")
	}
	l.finish("conv", cliSummary(3, 3, 0, 0, 0))
	if ev := ledgerEvents(t, dir); len(ev) != 2 || ev[0]["task_preview"] != "mine" {
		t.Fatalf("%v", ev)
	}
}
