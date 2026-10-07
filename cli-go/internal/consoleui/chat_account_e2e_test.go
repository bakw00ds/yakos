package consoleui_test

// chat_account_e2e_test.go — K-136: every turn of an interactive chat session is
// accounted in the dispatch log, exactly one dispatch_started / dispatch_finished
// pair per turn, written by dispatch.Account with surface console-chat.
//
// The test drives the real handlers over HTTP against a real interactive.Manager
// and a fake `claude` on PATH that answers each stream-json user frame with a
// text delta and a result frame (usage, cost, session id), the way the CLI does.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// fakeClaudeScript answers each user frame with one text delta and one result frame.
const fakeClaudeScript = `#!/bin/sh
printf '%s\n' '{"type":"system","subtype":"init","session_id":"ses_FIXTURE_0001","model":"claude-sonnet-4-5-20250929"}'
case " $* " in
  *" --input-format "*) ;;
  *)
    # one-shot (no stream-json input): answer once and exit
    printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}'
    printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"reply"}}}'
    printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_stop","index":0}}'
    printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":100,"session_id":"ses_FIXTURE_0001","total_cost_usd":0.25,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}'
    exit 0
    ;;
esac
while IFS= read -r line; do
  printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}'
  printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"reply"}}}'
  printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_stop","index":0}}'
  printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":100,"session_id":"ses_FIXTURE_0001","total_cost_usd":0.25,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}'
done
`

type ledgerServer struct {
	ts        *httptest.Server
	tok       string
	logDir    string
	workDir   string
	mgr       *interactive.Manager
	bus       *wsbus.Bus // the bus the handlers publish fleet.* events on
	yakosRoot string     // the framework root the handlers read rules and skills from
	launches  string     // the fake claude appends a line here each time it is started
}

// launchCount is how many times the fake claude has been started. A turn refused
// before it launches anything leaves it unchanged.
func (s ledgerServer) launchCount(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(s.launches)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "\n")
}

func newLedgerServer(t *testing.T) ledgerServer { return newLedgerServerWith(t, nil) }

// newLedgerServerWith is newLedgerServer with an Agent SDK engine factory wired
// into the chat handlers (nil: the SDK engine is not configured).
func newLedgerServerWith(t *testing.T, sdk *interactive.SDKEngineFactory) ledgerServer {
	return newLedgerServerScript(t, sdk, "")
}

// newLedgerServerScript is newLedgerServerWith with a script of its own for the
// fake claude ("" is the default one, which answers every turn).
func newLedgerServerScript(t *testing.T, sdk *interactive.SDKEngineFactory, claudeScript string) ledgerServer {
	return newLedgerServerOpts(t, sdk, claudeScript, nil)
}

// newLedgerServerOpts is newLedgerServerScript with a wrapper for the handlers'
// send path: wrap receives the real manager and returns what the handlers send
// through, so a test can make the engine refuse a frame on demand (nil: none).
func newLedgerServerOpts(t *testing.T, sdk *interactive.SDKEngineFactory, claudeScript string, wrap func(real consoleui.InteractiveSender) consoleui.InteractiveSender) ledgerServer {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	logDir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", logDir)

	binDir := t.TempDir()
	launches := filepath.Join(t.TempDir(), "launches")
	if claudeScript == "" {
		claudeScript = fakeClaudeScript
	}
	script := strings.Replace(claudeScript, "#!/bin/sh\n", "#!/bin/sh\nprintf 'x\\n' >> '"+launches+"'\n", 1)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 2, IdleTimeout: time.Minute})
	t.Cleanup(mgr.Stop)

	workspace := t.TempDir()
	yakosRoot := t.TempDir()
	// A roster with two agents, for the tests that name an agent other than a
	// bare runtime (the budget tests pin a session to one and name the other).
	agentsDir := filepath.Join(yakosRoot, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		body := "---\nid: " + name + "\n---\n\n## Purpose\n\nTest agent " + name + ".\n"
		if err := os.WriteFile(filepath.Join(agentsDir, name+".md"), []byte(body), 0o644); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	workDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            workDir,
		WorkspaceRoot:      workspace,
		YakosRoot:          yakosRoot,
		DispatchService:    dispatch.NewService(dispatch.ServiceConfig{YakosRoot: yakosRoot, WorkspaceRoot: workspace, OperatorID: "alice"}),
		InteractiveManager: mgr,
		SDKEngineFactory:   sdk,
	})
	if wrap != nil {
		consoleui.SetInteractiveSender(srv, wrap(mgr))
	}
	ts := httptest.NewServer(consoleui.RequireTokenForNonStatic(tok, consoleui.RequireJSONForMutations(srv.HandlerForTest())))
	t.Cleanup(ts.Close)
	return ledgerServer{ts: ts, tok: tok, logDir: logDir, workDir: workDir, mgr: mgr, bus: bus, launches: launches, yakosRoot: yakosRoot}
}

func (s ledgerServer) post(t *testing.T, path string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.ts.URL+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// events reads the dispatch log written so far.
func (s ledgerServer) events(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.logDir, "dispatch-log.ndjson"))
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("log line %q: %v", l, err)
		}
		out = append(out, ev)
	}
	return out
}

func (s ledgerServer) waitForEvents(t *testing.T, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ev := s.events(t); len(ev) >= n {
			return ev
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d dispatch-log events; have %v", n, s.events(t))
	return nil
}

// Two turns of one interactive session (the dispatch, then a follow-up send) leave
// exactly two event pairs, each with the turn's own usage and the console-chat surface.
func TestInteractiveChat_EveryTurnWritesOneEventPair(t *testing.T) {
	s := newLedgerServer(t)

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "first question", "sessionId": "sess-ledger-1",
		"operatorId": "alice", "conversationId": "conv-ledger-1", "interactive": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	t.Cleanup(func() { s.mgr.Close("conv-ledger-1") })
	s.waitForEvents(t, 2)

	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-ledger-1", "operatorId": "alice", "sessionId": "sess-ledger-1", "text": "second question",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	events := s.waitForEvents(t, 4)
	time.Sleep(200 * time.Millisecond) // nothing more may arrive
	if events = s.events(t); len(events) != 4 {
		t.Fatalf("two turns must write exactly two pairs (4 events), got %d: %v", len(events), events)
	}

	var started, finished []map[string]any
	for _, ev := range events {
		switch ev["type"] {
		case "dispatch_started":
			started = append(started, ev)
		case "dispatch_finished":
			finished = append(finished, ev)
		}
	}
	if len(started) != 2 || len(finished) != 2 {
		t.Fatalf("pairs: %d started, %d finished", len(started), len(finished))
	}
	if started[0]["task_preview"] != "first question" || started[1]["task_preview"] != "second question" {
		t.Errorf("each turn's start carries its own task: %v / %v", started[0]["task_preview"], started[1]["task_preview"])
	}
	for i, f := range finished {
		for k, want := range map[string]any{
			"surface": "console-chat", "agent": "claude", "runtime": "claude", "provider": "anthropic",
			"billing": "subscription", "cost_source": "harness", "api_equivalent_usd": 0.25,
			"model_id": "claude-sonnet-4-5-20250929", "native_session_id": "ses_FIXTURE_0001",
			"conversation_id": "conv-ledger-1", "operator_id": "alice", "session_id": "sess-ledger-1",
			"exit_code": float64(0), "output_bytes": float64(len("reply")),
		} {
			if f[k] != want {
				t.Errorf("turn %d: %s = %v, want %v", i+1, k, f[k], want)
			}
		}
		u, _ := f["usage"].(map[string]any)
		if u["input_tokens"] != float64(10) || u["output_tokens"] != float64(5) || u["cache_read"] != float64(100) ||
			u["cache_creation"] != float64(20) || u["total_cost_usd"] != float64(0) {
			t.Errorf("turn %d: usage = %v (a subscription turn: tokens, no spend)", i+1, u)
		}
	}
	// Read back through the canonical reader: tokens for both turns, no spend.
	files, _ := cost.LogFiles(s.logDir)
	var tokens int64
	var spend float64
	for ev := range cost.StreamFiles(files, "") {
		tokens += ev.Tokens().Total()
		spend += ev.SpendUSD()
	}
	if tokens != 2*(10+5+100+20) || spend != 0 {
		t.Errorf("reader totals: tokens=%d spend=%v", tokens, spend)
	}
	// An interactive session's native session id is not stored for resuming: its
	// process still holds the session, and a one-shot --resume of the same id while
	// it is open would have two claude processes writing it.
	if got := consoleui.NewTranscripts(s.workDir).NativeSession("conv-ledger-1", "claude", "alice"); got != "" {
		t.Errorf("an interactive turn must not store a native session id, got %q", got)
	}
}

// A send the engine refuses leaves no event: a turn that never ran is not accounted.
func TestInteractiveChat_RefusedSendWritesNothing(t *testing.T) {
	s := newLedgerServer(t)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "hello", "sessionId": "sess-ledger-2",
		"operatorId": "alice", "conversationId": "conv-ledger-2", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-ledger-2") })
	s.waitForEvents(t, 2)

	// Another operator's send: 403, no turn.
	resp = s.post(t, "/api/chat/send", map[string]any{"conversationId": "conv-ledger-2", "operatorId": "mallory", "text": "steal"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a send from another operator: %d, want 403", resp.StatusCode)
	}
	// A conversation with no session: 404, no turn.
	resp = s.post(t, "/api/chat/send", map[string]any{"conversationId": "conv-nobody", "operatorId": "alice", "text": "hello?"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a send to no session: %d, want 404", resp.StatusCode)
	}
	time.Sleep(300 * time.Millisecond)
	if ev := s.events(t); len(ev) != 2 {
		t.Fatalf("refused sends must leave no events; the log has %d: %v", len(ev), ev)
	}

	// A refused turn must not linger as a pending turn that absorbs the next real
	// turn's result: the valid follow-up is accounted under its own task.
	resp = s.post(t, "/api/chat/send", map[string]any{"conversationId": "conv-ledger-2", "operatorId": "alice", "text": "valid follow-up"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("a valid follow-up: %d, want 202", resp.StatusCode)
	}
	ev := s.waitForEvents(t, 4)
	time.Sleep(200 * time.Millisecond)
	if ev = s.events(t); len(ev) != 4 {
		t.Fatalf("one more turn, one more pair: got %d events: %v", len(ev), ev)
	}
	if ev[2]["type"] != "dispatch_started" || ev[2]["task_preview"] != "valid follow-up" {
		t.Errorf("the follow-up's pair must carry its own task, not a refused one's: %v", ev[2])
	}
}

// The chat handler has one chunk callback for every kind of turn. A one-shot turn on
// a conversation whose interactive session is still alive is accounted once, inside
// RunStream, and the live session's ledger must not count its summary again.
func TestInteractiveChat_OneShotTurnOnALiveInteractiveConversationIsNotCountedTwice(t *testing.T) {
	s := newLedgerServer(t)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "interactive first", "sessionId": "sess-ledger-4",
		"operatorId": "alice", "conversationId": "conv-ledger-4", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-ledger-4") })
	s.waitForEvents(t, 2)

	// The pane toggles interactive off: the next turn is one-shot, same conversation.
	resp = s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "one shot after", "sessionId": "sess-ledger-4b",
		"operatorId": "alice", "conversationId": "conv-ledger-4",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("one-shot dispatch: %d", resp.StatusCode)
	}
	s.waitForEvents(t, 4)
	time.Sleep(400 * time.Millisecond)
	if ev := s.events(t); len(ev) != 4 {
		t.Fatalf("an interactive turn and a one-shot turn are two pairs (4 events), got %d: %v", len(ev), ev)
	}
}

// sseFrames opens the chat SSE stream for operator and delivers each data frame.
func (s ledgerServer) sseFrames(t *testing.T, ctx context.Context, operator string) <-chan map[string]any {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.ts.URL+"/api/chat/stream?operatorId="+operator, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.tok)
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan map[string]any, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev map[string]any
			if json.Unmarshal([]byte(line[len("data: "):]), &ev) == nil {
				out <- ev
			}
		}
	}()
	return out
}

// The persistent CLI engine used to parse claude's result line and drop it, so the
// browser never received an end-of-turn event for an interactive turn and the Chat pane
// stayed "streaming". Every turn, the first and each follow-up, now ends with a summary
// frame on the operator's stream, after the turn's text.
func TestInteractiveChat_EveryTurnEndsWithASummaryFrameInTheBrowserStream(t *testing.T) {
	s := newLedgerServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := s.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond) // the stream registers before the first turn starts

	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "first", "sessionId": "sess-sse-1",
		"operatorId": "alice", "conversationId": "conv-sse-1", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-sse-1") })

	// nextTurn reads frames until the turn's summary and returns the frame types it saw.
	nextTurn := func(label string) (types []string, summary map[string]any) {
		deadline := time.After(15 * time.Second)
		for {
			select {
			case ev, ok := <-frames:
				if !ok {
					t.Fatalf("%s: the stream closed before a summary", label)
				}
				typ, _ := ev["type"].(string)
				types = append(types, typ)
				if typ == "summary" {
					return types, ev
				}
			case <-deadline:
				t.Fatalf("%s: no summary frame arrived (saw %v): the pane would never learn the turn ended", label, types)
			}
		}
	}

	types, sum := nextTurn("first turn")
	if types[len(types)-1] != "summary" || !contains(types, "token") {
		t.Errorf("first turn frames = %v, want text then a summary", types)
	}
	if sum["exit_code"] != float64(0) || sum["runtime_resolved"] != "claude" || sum["conversation_id"] != "conv-sse-1" ||
		sum["model_resolved"] != "claude-sonnet-4-5-20250929" {
		t.Errorf("first summary = %v", sum)
	}

	resp = s.post(t, "/api/chat/send", map[string]any{
		"conversationId": "conv-sse-1", "operatorId": "alice", "sessionId": "sess-sse-1", "text": "second",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("send: %d", resp.StatusCode)
	}
	types, sum = nextTurn("follow-up turn")
	if types[len(types)-1] != "summary" || sum["exit_code"] != float64(0) {
		t.Errorf("follow-up frames = %v, summary = %v", types, sum)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// A one-shot chat turn is accounted once, inside RunStream, and must not be counted
// again by the interactive ledger. Its stream carries the same result frame an
// interactive turn does, and the chat handler's one chunk callback serves both, so
// this guards the call sites that feed the ledger.
func TestInteractiveChat_OneShotTurnsAreNotCountedByTheTurnLedger(t *testing.T) {
	s := newLedgerServer(t)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "one shot", "sessionId": "sess-ledger-3",
		"operatorId": "alice", "conversationId": "conv-ledger-3", // interactive: false
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
	// The one-shot turn runs the fake claude once through RunStream: one pair.
	ev := s.waitForEvents(t, 2)
	time.Sleep(300 * time.Millisecond)
	if ev = s.events(t); len(ev) != 2 {
		t.Fatalf("a one-shot turn writes exactly one pair, got %d events: %v", len(ev), ev)
	}
	if fin := ev[1]; fin["type"] != "dispatch_finished" || fin["surface"] != "console-chat" {
		t.Errorf("one-shot finished event = %v", fin)
	}
	if u, _ := ev[1]["usage"].(map[string]any); u["cache_read"] != float64(100) {
		t.Errorf("the streamed one-shot turn reports its cache tokens: %v", u)
	}
}
