package consoleui_test

// chat_routing_test.go — K-132 P0a: the console chat pane reaches the runtime an
// agent is pinned to, validates models per runtime, and a one-shot claude chat
// remembers the conversation.
//
// Before: an empty runtime was forced to "claude" (so a pane could never reach
// general-codex), every model had to be a Claude tier, a codex pane's model was
// ignored, and each claude turn was a fresh `claude -p` with no memory.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// routingYakosRoot builds a roster with the two framework pins and a plain agent.
func routingYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := map[string]string{
		"backend":       "domain: backend-service\nmodel: sonnet\n",
		"general-codex": "domain: cross-cutting\nruntime: codex\nmodel: gpt-5\n",
		"general-agy":   "domain: cross-cutting\nruntime: agy\nmodel: gemini-3.5\n",
	}
	for id, fm := range agents {
		body := "---\nid: " + id + "\n" + fm + "---\n\n## Purpose\n\nRouting test agent " + id + ".\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// newRoutingServer builds a console server over yakosRoot. svc may be nil (the
// handler then answers 503 once every validation has passed).
func newRoutingServer(t *testing.T, yakosRoot string, svc *dispatch.Service) (ts *httptest.Server, tok, workDir, workspace string) {
	t.Helper()
	// Resolution reads the operator's ~/.yakos-state/default-runtime; point the
	// state directory at an empty one so these tests do not depend on it.
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	stateDir := t.TempDir()
	workDir = t.TempDir()
	workspace = t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
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
		WorkspaceRoot:     workspace,
		DispatchService:   svc,
	})
	wrapped := consoleui.RequireTokenForNonStatic(tok, consoleui.RequireJSONForMutations(srv.HandlerForTest()))
	ts = httptest.NewServer(wrapped)
	t.Cleanup(ts.Close)
	return ts, tok, workDir, workspace
}

func dispatchBody(fields map[string]any) string {
	base := map[string]any{"task": "hi", "operatorId": "alice", "sessionId": "s-1", "agent": "backend"}
	for k, v := range fields {
		base[k] = v
	}
	b, _ := json.Marshal(base)
	return string(b)
}

func postDispatch(t *testing.T, ts *httptest.Server, tok string, fields map[string]any) (int, string) {
	t.Helper()
	resp := post(t, ts.URL+"/api/chat/dispatch", tok, dispatchBody(fields))
	defer drainClose(resp)
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.String()
}

// D5: an empty runtime used to be forced to claude, so the pane could never
// reach a pinned agent. It now resolves from the agent's pin, and the model is
// judged against the runtime it resolved to.
func TestChatDispatch_EmptyRuntimeResolvesFromAgentPin(t *testing.T) {
	ts, tok, _, _ := newRoutingServer(t, routingYakosRoot(t), nil)

	cases := []struct {
		name   string
		fields map[string]any
		want   int // 503 = every validation passed, no service configured
	}{
		// gpt-5 is codex's model: valid only because "" resolved to codex.
		{"empty runtime, codex-pinned agent, codex model", map[string]any{"runtime": "", "agent": "general-codex", "model": "gpt-5", "sessionId": "s-a"}, http.StatusServiceUnavailable},
		{"auto runtime, codex-pinned agent, codex model", map[string]any{"runtime": "auto", "agent": "general-codex", "model": "gpt-5", "sessionId": "s-b"}, http.StatusServiceUnavailable},
		{"omitted runtime, agy-pinned agent, agy model", map[string]any{"agent": "general-agy", "model": "gemini-3.5", "sessionId": "s-c"}, http.StatusServiceUnavailable},
		{"empty runtime, alias on a pinned runtime", map[string]any{"runtime": "", "agent": "general-agy", "model": "best", "sessionId": "s-d"}, http.StatusServiceUnavailable},
		{"empty runtime, no model", map[string]any{"runtime": "", "agent": "general-codex", "model": "", "sessionId": "s-e"}, http.StatusServiceUnavailable},
		// An unpinned agent resolves to claude: a Claude tier is fine.
		{"empty runtime, plain agent, claude tier", map[string]any{"runtime": "", "agent": "backend", "model": "sonnet", "sessionId": "s-f"}, http.StatusServiceUnavailable},
		// An explicit runtime still wins over the pin.
		{"explicit claude on a codex-pinned agent", map[string]any{"runtime": "claude", "agent": "general-codex", "model": "haiku", "sessionId": "s-g"}, http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		if got, body := postDispatch(t, ts, tok, c.fields); got != c.want {
			t.Errorf("%s: status %d (%s), want %d", c.name, got, strings.TrimSpace(body), c.want)
		}
	}
}

// The model is invalid for the runtime the request resolves to: 400 before any
// work is queued.
func TestChatDispatch_InvalidModelForResolvedRuntimeIs400(t *testing.T) {
	ts, tok, _, _ := newRoutingServer(t, routingYakosRoot(t), nil)

	cases := []struct {
		name   string
		fields map[string]any
	}{
		{"claude takes only tiers: an explicit claude pane with a codex model", map[string]any{"runtime": "claude", "agent": "backend", "model": "gpt-5", "sessionId": "s-1"}},
		{"auto on an unpinned agent resolves to claude", map[string]any{"runtime": "", "agent": "backend", "model": "gpt-5", "sessionId": "s-2"}},
		{"codex pin: not a model id", map[string]any{"runtime": "", "agent": "general-codex", "model": "Bad Model!", "sessionId": "s-3"}},
		{"codex pin: shell metacharacters", map[string]any{"runtime": "", "agent": "general-codex", "model": "gpt-5; rm -rf /", "sessionId": "s-4"}},
		{"codex pin: leading dash", map[string]any{"runtime": "", "agent": "general-codex", "model": "-gpt5", "sessionId": "s-5"}},
		{"codex pin: too long", map[string]any{"runtime": "", "agent": "general-codex", "model": strings.Repeat("a", 65), "sessionId": "s-6"}},
		{"explicit codex: a Claude tier is not a codex model", map[string]any{"runtime": "codex", "agent": "general-codex", "model": "sonnet", "sessionId": "s-7"}},
		{"agy pin: a Claude tier is not an agy model", map[string]any{"runtime": "", "agent": "general-agy", "model": "opus", "sessionId": "s-8"}},
	}
	for _, c := range cases {
		if got, body := postDispatch(t, ts, tok, c.fields); got != http.StatusBadRequest {
			t.Errorf("%s: status %d (%s), want 400", c.name, got, strings.TrimSpace(body))
		}
	}
}

func TestChatDispatch_AliasesAreValidPerRuntime(t *testing.T) {
	ts, tok, _, _ := newRoutingServer(t, routingYakosRoot(t), nil)
	for _, alias := range []string{"cheap", "balanced", "best", "reasoning", "frontier"} {
		for _, agent := range []string{"backend", "general-codex", "general-agy"} {
			sess := "s-alias-" + alias + "-" + agent
			if got, body := postDispatch(t, ts, tok, map[string]any{"runtime": "", "agent": agent, "model": alias, "sessionId": sess}); got != http.StatusServiceUnavailable {
				t.Errorf("alias %q on %s: status %d (%s), want it accepted (503: no service)", alias, agent, got, strings.TrimSpace(body))
			}
		}
	}
}

// D14: gemini is gone from the runtime list.
func TestChatDispatch_GeminiIsNotARuntime(t *testing.T) {
	ts, tok, _, _ := newRoutingServer(t, routingYakosRoot(t), nil)
	if got, _ := postDispatch(t, ts, tok, map[string]any{"runtime": "gemini", "agent": "backend", "model": "", "sessionId": "s-gem"}); got != http.StatusBadRequest {
		t.Errorf("runtime gemini: status %d, want 400", got)
	}
}

// Interactive mode is a claude process. Toggling it on a pane that resolves to
// codex/agy used to answer from claude silently; it now says no.
func TestChatDispatch_InteractiveRefusedForNonClaudeRuntime(t *testing.T) {
	ts, tok, _, _ := newRoutingServer(t, routingYakosRoot(t), nil)

	for _, c := range []struct {
		name   string
		fields map[string]any
		want   int
	}{
		{"auto pane resolving to codex", map[string]any{"runtime": "", "agent": "general-codex", "interactive": true, "sessionId": "s-i1"}, http.StatusBadRequest},
		{"explicit agy pane", map[string]any{"runtime": "agy", "agent": "backend", "interactive": true, "sessionId": "s-i2"}, http.StatusBadRequest},
		{"auto pane resolving to claude", map[string]any{"runtime": "", "agent": "backend", "model": "sonnet", "interactive": true, "sessionId": "s-i3"}, http.StatusServiceUnavailable},
		{"explicit claude pane on a codex-pinned agent", map[string]any{"runtime": "claude", "agent": "general-codex", "interactive": true, "sessionId": "s-i4"}, http.StatusServiceUnavailable},
	} {
		got, body := postDispatch(t, ts, tok, c.fields)
		if got != c.want {
			t.Errorf("%s: status %d (%s), want %d", c.name, got, strings.TrimSpace(body), c.want)
		}
		if c.want == http.StatusBadRequest && !strings.Contains(body, "interactive mode is only available for the claude runtime") {
			t.Errorf("%s: body %q does not explain", c.name, body)
		}
	}
}

// ---- /api/skills reports the real runtime ---------------------------------------

func TestSkillsHandler_ReportsAgentRuntime(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir()) // no operator default-runtime
	ts, tok := newSkillsTestServer(t, routingYakosRoot(t))
	resp := doSkillsGET(t, ts, tok)
	defer resp.Body.Close()
	var got struct {
		Agents []struct {
			Name    string `json:"name"`
			Runtime string `json:"runtime"`
		} `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	rts := map[string]string{}
	for _, a := range got.Agents {
		rts[a.Name] = a.Runtime
	}
	// Before: every agent was reported as claude unless its id was a runtime name.
	want := map[string]string{"general-codex": "codex", "general-agy": "agy", "backend": "claude"}
	for name, rt := range want {
		if rts[name] != rt {
			t.Errorf("agent %q runtime = %q, want %q (all: %v)", name, rts[name], rt, rts)
		}
	}
}

// ---- continuity end to end -------------------------------------------------------

// fakeChatClaude puts a `claude` stub on PATH. It logs one block per call (its
// argv), then answers according to the files it reads: modeFile "stale" makes a
// --resume call fail the way the real CLI does for an unknown session (recorded
// from claude 2.1.289); sidFile is the session id it stamps on a good result.
type fakeChatClaude struct {
	argvLog, modeFile, sidFile string
}

func installFakeChatClaude(t *testing.T) *fakeChatClaude {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	bin := t.TempDir()
	f := &fakeChatClaude{
		argvLog:  filepath.Join(t.TempDir(), "argv.log"),
		modeFile: filepath.Join(t.TempDir(), "mode"),
		sidFile:  filepath.Join(t.TempDir(), "sid"),
	}
	script := `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '` + f.argvLog + `'
MODE=$(cat '` + f.modeFile + `' 2>/dev/null)
SID=$(cat '` + f.sidFile + `' 2>/dev/null)
case " $* " in *" --resume "*) RESUMED=1 ;; esac
if [ "$MODE" = "stale" ] && [ -n "$RESUMED" ]; then
  echo "No conversation found with session ID: gone" >&2
  printf '%s\n' '{"type":"result","subtype":"error_during_execution","is_error":true,"session_id":"gone","total_cost_usd":0,"usage":{}}'
  exit 1
fi
printf '%s\n' "{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\",\"session_id\":\"$SID\",\"total_cost_usd\":0.001,\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}"
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	return f
}

func (f *fakeChatClaude) set(t *testing.T, mode, sid string) {
	t.Helper()
	if err := os.WriteFile(f.modeFile, []byte(mode), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.sidFile, []byte(sid), 0o644); err != nil {
		t.Fatal(err)
	}
}

// calls returns each recorded invocation's argv.
func (f *fakeChatClaude) calls(t *testing.T) [][]string {
	t.Helper()
	raw, err := os.ReadFile(f.argvLog)
	if err != nil {
		return nil
	}
	var out [][]string
	for _, block := range strings.Split(string(raw), "--- call\n")[1:] {
		out = append(out, strings.Split(strings.TrimRight(block, "\n"), "\n"))
	}
	return out
}

func hasResume(argv []string, id string) bool {
	for i, a := range argv {
		if a == "--resume" && i+1 < len(argv) && argv[i+1] == id {
			return true
		}
	}
	return false
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The symptom: "follow-ups forget the conversation". Turn 1 stores claude's
// session id; turn 2 resumes it; a session that has vanished is forgotten
// rather than breaking every later turn.
func TestChatDispatch_ClaudeFollowUpsResumeTheConversation(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	svcWorkspace := t.TempDir()
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: svcWorkspace})
	ts, tok, workDir, _ := newRoutingServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)

	const conv = "conv-resume-1"
	send := func(sess string) {
		t.Helper()
		got, body := postDispatch(t, ts, tok, map[string]any{
			"runtime": "", "agent": "backend", "model": "sonnet", "task": "next question",
			"conversationId": conv, "sessionId": sess,
		})
		if got != http.StatusAccepted {
			t.Fatalf("dispatch %s: status %d (%s)", sess, got, body)
		}
	}

	// Turn 1: no session yet, so no --resume; its result is remembered.
	fake.set(t, "ok", "sess-A")
	send("s-turn-1")
	waitUntil(t, "turn 1 to store its session id", func() bool { return store.NativeSession(conv, "claude") == "sess-A" })
	calls := fake.calls(t)
	if len(calls) != 1 || hasResume(calls[0], "sess-A") {
		t.Fatalf("turn 1 must not resume anything: %v", calls)
	}

	// Turn 2: resumes turn 1's session; the new result replaces the stored id.
	fake.set(t, "ok", "sess-B")
	send("s-turn-2")
	waitUntil(t, "turn 2 to store its session id", func() bool { return store.NativeSession(conv, "claude") == "sess-B" })
	calls = fake.calls(t)
	if len(calls) != 2 || !hasResume(calls[1], "sess-A") {
		t.Fatalf("turn 2 must --resume sess-A: %v", calls)
	}

	// Turn 3: the saved session has gone. claude fails exactly as the real CLI
	// does; the stale id is forgotten (the failed result's echo is not stored).
	fake.set(t, "stale", "sess-C")
	send("s-turn-3")
	waitUntil(t, "the stale session id to be forgotten", func() bool { return store.NativeSession(conv, "claude") == "" })
	calls = fake.calls(t)
	if len(calls) != 3 || !hasResume(calls[2], "sess-B") {
		t.Fatalf("turn 3 must have tried --resume sess-B: %v", calls)
	}

	// Turn 4: starts fresh, and is remembered again.
	fake.set(t, "ok", "sess-D")
	send("s-turn-4")
	waitUntil(t, "turn 4 to store a fresh session id", func() bool { return store.NativeSession(conv, "claude") == "sess-D" })
	calls = fake.calls(t)
	if len(calls) != 4 {
		t.Fatalf("want 4 claude calls, got %d: %v", len(calls), calls)
	}
	for _, a := range calls[3] {
		if a == "--resume" {
			t.Errorf("turn 4 must start a fresh session after the stale one was dropped: %v", calls[3])
		}
	}
}

// Conversations do not share sessions.
func TestChatDispatch_ResumeIsPerConversation(t *testing.T) {
	root := routingYakosRoot(t)
	fake := installFakeChatClaude(t)
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, workDir, _ := newRoutingServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)

	fake.set(t, "ok", "sess-one")
	if got, body := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": "conv-one", "sessionId": "s-one"}); got != http.StatusAccepted {
		t.Fatalf("status %d %s", got, body)
	}
	waitUntil(t, "conv-one to store", func() bool { return store.NativeSession("conv-one", "claude") == "sess-one" })

	// A different conversation's first turn must not resume conv-one's session.
	fake.set(t, "ok", "sess-two")
	if got, body := postDispatch(t, ts, tok, map[string]any{"agent": "backend", "conversationId": "conv-two", "sessionId": "s-two"}); got != http.StatusAccepted {
		t.Fatalf("status %d %s", got, body)
	}
	waitUntil(t, "conv-two to store", func() bool { return store.NativeSession("conv-two", "claude") == "sess-two" })
	calls := fake.calls(t)
	if len(calls) != 2 || hasResume(calls[1], "sess-one") {
		t.Errorf("conv-two's first turn resumed another conversation's session: %v", calls)
	}
}

// Codex and agy panes run the pinned agent's runtime, not claude.
func TestChatDispatch_PinnedAgentRunsOnItsRuntime(t *testing.T) {
	root := routingYakosRoot(t)
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "invoked.txt")
	for _, name := range []string{"claude", "codex", "agy"} {
		script := "#!/bin/sh\nprintf '%s\\n' " + name + " >> '" + rec + "'\nprintf '%s\\n' '" + name + " says hi'\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", home)
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	// A signed-in codex, so the chain's probe passes.
	t.Setenv("OPENAI_API_KEY", "sk-test")

	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, _, _ := newRoutingServer(t, root, svc)

	if got, body := postDispatch(t, ts, tok, map[string]any{"runtime": "", "agent": "general-codex", "model": "gpt-5", "sessionId": "s-pin", "conversationId": "conv-pin"}); got != http.StatusAccepted {
		t.Fatalf("status %d %s", got, body)
	}
	waitUntil(t, "the pinned runtime to run", func() bool {
		b, _ := os.ReadFile(rec)
		return strings.TrimSpace(string(b)) != ""
	})
	b, _ := os.ReadFile(rec)
	if got := strings.Fields(string(b)); len(got) != 1 || got[0] != "codex" {
		t.Errorf("a pane with runtime auto and agent general-codex executed %v, want only codex", got)
	}
}
