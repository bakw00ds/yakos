package consoleui_test

// chat_sdk_route_k173_test.go: K-173. An SDK pane (interactive + structured
// questions) is refused exactly like a CLI pane when the model it would run is
// disabled by the project or over the agent's max_model ceiling, and it runs the
// routed model. It does so on a machine with no claude CLI: the SDK engine is a
// sidecar on ANTHROPIC_API_KEY, so the CLI is not the thing under test.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/statepath"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

type sdkK173 struct {
	ts     *httptest.Server
	tok    string
	frames chan consoleui.SSEEvent
	mu     sync.Mutex
	models []string // Model of each SDKEngineParams the factory saw
	spawns int
}

func (s *sdkK173) seen() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.models...), s.spawns
}

// newSDKK173 builds a console with an SDK factory that counts engines, an API
// key, NO claude on PATH, a scratch HOME (with an optional budget policy body)
// and a workspace whose .yakos.yml is projectYML.
func newSDKK173(t *testing.T, projectYML, policyYAML string) *sdkK173 {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YAKOS_RUNTIME", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-FIXTURE0000000000000000")
	t.Setenv("PATH", t.TempDir()) // no claude CLI
	if policyYAML != "" {
		dir := statepath.Dir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(budget.PolicyPath(dir), []byte(policyYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspace := t.TempDir()
	if projectYML != "" {
		if err := os.WriteFile(filepath.Join(workspace, ".yakos.yml"), []byte(projectYML), 0o644); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	tok, err := consoleui.LoadOrCreateToken(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	t.Cleanup(mgr.Stop)

	s := &sdkK173{tok: tok, frames: make(chan consoleui.SSEEvent, 64)}
	var factory interactive.SDKEngineFactory = func(p interactive.SDKEngineParams) (*interactive.SDKEngine, error) {
		s.mu.Lock()
		s.models = append(s.models, p.Model)
		s.mu.Unlock()
		return interactive.NewSDKEngineWithProvider(p, func() *exec.Cmd {
			s.mu.Lock()
			s.spawns++
			s.mu.Unlock()
			return exec.Command(os.Args[0], "-test.run=^$")
		})
	}
	srv := consoleui.MustNew(t, consoleui.Config{
		Token: tok, KanbanBoardPath: t.TempDir() + "/kanban.md", KanbanProject: "test",
		MetricsProjectDir: t.TempDir(), PerfWorkDir: t.TempDir(), Bus: bus,
		WorkDir: t.TempDir(), WorkspaceRoot: workspace,
		DispatchService:    dispatch.NewService(dispatch.ServiceConfig{WorkspaceRoot: workspace}),
		InteractiveManager: mgr, SDKEngineFactory: &factory,
	})
	s.ts = httptest.NewServer(consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.HandlerForTest())))
	t.Cleanup(s.ts.Close)

	sseCtx, sseCancel := context.WithCancel(ctx)
	t.Cleanup(sseCancel)
	sse, err := sseGetWithCancel(sseCtx, s.ts.URL+"/api/chat/stream?operatorId=alice", tok)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer func() { _ = sse.Body.Close() }()
		sc := bufio.NewScanner(sse.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev consoleui.SSEEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
				s.frames <- ev
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)
	return s
}

// first posts an SDK first turn and returns the first route or error frame.
func (s *sdkK173) first(t *testing.T, sess, model string) consoleui.SSEEvent {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"agent": "claude", "task": "hi", "model": model, "sessionId": sess,
		"conversationId": "conv-" + sess, "operatorId": "alice",
		"interactive": true, "structuredQuestions": true,
	})
	req, _ := http.NewRequest(http.MethodPost, s.ts.URL+"/api/chat/dispatch", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch status %d", resp.StatusCode)
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-s.frames:
			if ev.SessionID == sess && (ev.Type == "route" || ev.Type == "error") {
				return ev
			}
		case <-deadline:
			t.Fatalf("no route or error frame for %s", sess)
		}
	}
}

func TestK173_SDKPaneIsRefusedForADisabledModelWithoutACLI(t *testing.T) {
	s := newSDKK173(t, "router:\n  disable_models: [opus]\n", "")
	ev := s.first(t, "s-dis", "opus")
	if ev.Type != "error" || !strings.Contains(ev.Text, "disable_models") {
		t.Fatalf("frame = %+v, want a disable_models refusal", ev)
	}
	if models, spawns := s.seen(); len(models) != 0 || spawns != 0 {
		t.Errorf("an engine was built (%v) or spawned (%d) for a refused model", models, spawns)
	}
}

func TestK173_SDKPaneRunsTheRoutedModelAndTheCeilingLowersIt(t *testing.T) {
	s := newSDKK173(t, "", "agents:\n  claude:\n    max_model: sonnet\n")
	ev := s.first(t, "s-low", "opus")
	if ev.Type != "route" {
		t.Fatalf("frame = %+v, want a route", ev)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if models, _ := s.seen(); len(models) == 1 {
			if !strings.Contains(strings.ToLower(models[0]), "sonnet") {
				t.Fatalf("the SDK engine got model %q, want the ceiling's sonnet", models[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the SDK engine was never built")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestK173_SDKPaneGetsTheRequestedModelWhenNothingForbidsIt(t *testing.T) {
	s := newSDKK173(t, "router:\n  disable_models: [haiku]\n", "")
	if ev := s.first(t, "s-ok", "sonnet"); ev.Type != "route" {
		t.Fatalf("frame = %+v, want a route", ev)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if models, _ := s.seen(); len(models) == 1 {
			if models[0] == "" {
				t.Fatal("the SDK engine got no model")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the SDK engine was never built")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A CLI pane started with a model over the agent's max_model ceiling runs the
// lowered model, not the one the browser asked for.
func TestK173_CLIPaneRunsTheCeilingLoweredModel(t *testing.T) {
	k := newK148(t)
	dir := statepath.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(budget.PolicyPath(dir), []byte("agents:\n  alpha:\n    max_model: sonnet\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const conv = "conv-cli-ceil"
	t.Cleanup(func() { k.mgr.Close(conv) })
	resp := k.post(t, "/api/chat/dispatch", map[string]any{"agent": "alpha", "runtime": "claude", "model": "opus",
		"task": "hi", "sessionId": "s-cli-ceil", "operatorId": "alice", "conversationId": conv, "interactive": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d", resp.StatusCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, call := range argvCalls(t, k.claudeLog) {
			joined := " " + strings.Join(call, " ") + " "
			if strings.Contains(joined, " --model ") {
				if strings.Contains(joined, "opus") || !strings.Contains(joined, "sonnet") {
					t.Fatalf("the CLI pane ran %q, want the ceiling's sonnet", joined)
				}
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("claude never started with a model: %v", argvCalls(t, k.claudeLog))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A sensitive-class string that sits only in the knowledge block (a rule the
// pane would send with every turn) makes the interactive first turn sensitive:
// it is placed on claude, never started on codex. The user text is clean.
func TestK173_InteractiveFirstTurnScansTheKnowledgeBlock(t *testing.T) {
	k := newK148(t)
	rules := filepath.Join(k.yakosRoot, "lib", "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	// A path the sensitive class guards, not a secret shape (the pack drops
	// those itself), so only the router's scan can see it.
	rule := "# deploy\n\nThe release job reads the file ~/.ssh/" + "id_rsa and ." + "env.production.\n"
	if err := os.WriteFile(filepath.Join(rules, "deploy.md"), []byte(rule), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if st := k.interactiveFirstTurn("s-kb", "conv-kb", "codex", "say hello"); st != http.StatusAccepted {
		t.Fatalf("status %d", st)
	}
	ev := k.firstOf("s-kb")
	if ev["type"] != "route" {
		t.Fatalf("first frame = %v, want a route", ev)
	}
	if r := routeOf(t, ev); r["runtime"] != "claude" || r["class"] != "sensitive" {
		t.Errorf("route = %v, want claude/sensitive", r)
	}
	if got := argvCalls(t, k.codexLog); len(got) != 0 {
		t.Errorf("codex was started: %v", got)
	}
}

// fakeCodexCardsScript answers a turn with a reasoning item, a command whose
// output holds a secret-shaped value, and the reply.
const fakeCodexCardsScript = `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '@FAKE_ARGV_LOG@'
echo $$ >> '@FAKE_PIDS@'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-codex-1"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"r","type":"reasoning","text":"thinking it over"}}'
printf '%s\n' '{"type":"item.started","item":{"id":"c","type":"command_execution","command":"cat notes.txt","status":"in_progress"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"c","type":"command_execution","command":"cat notes.txt","aggregated_output":"hello\nAWS_SECRET_ACCESS_KEY=@KEY@\n","exit_code":0,"status":"completed"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"all done"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}'
`

// The cards a pane showed are persisted in order (thinking, tool use, tool
// result, reply), with a secret-shaped value in a tool output redacted; the
// handoff digest never carries them; and the transcript API serves them back.
func TestK173_ToolAndThinkingCardsArePersistedAndReplayable(t *testing.T) {
	const key = "wJalrXUtnFEMI" + "K7MDENGbPxRfiCYEXAMPLE"
	script := strings.ReplaceAll(fakeCodexCardsScript, "@KEY@", key)
	f := newResumeServer(t, "codex", script)
	const conv = "conv-cards"
	t.Cleanup(func() { f.mgr.Close(conv) })
	f.dispatchTurn(t, "codex", conv, "list the notes")
	store := consoleui.NewTranscripts(f.workDir)
	waitForTurns(t, store, conv, 1)

	entries, _ := store.Read(conv, "")
	var roles []string
	var use, result, think consoleui.TranscriptEntry
	for _, e := range entries {
		roles = append(roles, string(e.Role))
		switch e.Role {
		case consoleui.RoleToolUse:
			use = e
		case consoleui.RoleToolResult:
			result = e
		case consoleui.RoleThinking:
			think = e
		}
	}
	if got := strings.Join(roles, ","); got != "user,route,thinking,tool_use,tool_result,assistant,summary" {
		t.Fatalf("transcript roles = %s", got)
	}
	if think.Text != "thinking it over" {
		t.Errorf("thinking = %q", think.Text)
	}
	if use.ToolName == "" || !strings.Contains(use.Text, "cat notes.txt") {
		t.Errorf("tool_use = %+v", use)
	}
	if !strings.Contains(result.Text, "hello") || result.IsError {
		t.Errorf("tool_result = %+v", result)
	}
	if strings.Contains(result.Text, key) {
		t.Errorf("the stored tool output kept the secret-shaped value: %q", result.Text)
	}
	// The handoff digest carries user and assistant turns only.
	digest, _, _ := consoleui.BuildHandoffDigestForTest(entries, "codex")
	for _, leak := range []string{"thinking it over", "cat notes.txt", "hello"} {
		if strings.Contains(digest, leak) {
			t.Errorf("the digest carries card text %q:\n%s", leak, digest)
		}
	}
	// And the API a reload reads returns them.
	resp := get(t, f.ts.URL+"/api/chat/transcript?conversationId="+conv+"&operatorId=alice", f.tok)
	defer func() { _ = resp.Body.Close() }()
	var served []consoleui.TranscriptEntry
	if err := json.NewDecoder(resp.Body).Decode(&served); err != nil || len(served) != len(entries) {
		t.Errorf("served %d entries (err %v), want %d", len(served), err, len(entries))
	}
}

// A non-owner's send writes nothing to the owner's transcript, and the owner's
// follow-up announces the pane's route again.
func TestK173_FollowUpRouteIsOwnerOnly(t *testing.T) {
	f := newResumeServer(t, "codex", fakeCodexScript)
	const conv = "conv-fu-owner"
	t.Cleanup(func() { f.mgr.Close(conv) })
	f.dispatchTurn(t, "codex", conv, "first")
	store := consoleui.NewTranscripts(f.workDir)
	waitForTurns(t, store, conv, 1)
	count := func() int {
		entries, _ := store.Read(conv, "")
		n := 0
		for _, e := range entries {
			if e.Role == consoleui.RoleRoute {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("routes after the first turn = %d", count())
	}
	resp := f.post(t, "/api/chat/send", map[string]any{"conversationId": conv, "operatorId": "mallory", "text": "hijack"})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner send = %d, want 403", resp.StatusCode)
	}
	if count() != 1 {
		t.Errorf("a refused non-owner send wrote a route turn")
	}
	if st := f.sendTurn(t, conv, "second"); st != http.StatusAccepted {
		t.Fatalf("owner send = %d", st)
	}
	waitUntil(t, "the follow-up route turn", func() bool { return count() == 2 })
}

// The routing module replays server text; it builds data and writes DOM text only
// through textContent / setAttribute (the escaping esc()/escLines() renderers do
// the rest, outside this module).
func TestK173_ChatRoutingJSHasNoMarkupSink(t *testing.T) {
	src, err := os.ReadFile("dist/chat-routing.js")
	if err != nil {
		t.Fatal(err)
	}
	// modeBadgeHTML is the module's one escaped-string builder (esc on every
	// interpolation, pane state only); nothing else may write markup.
	if re := regexp.MustCompile(`\.innerHTML\s*=|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function`); re.Match(src) {
		t.Fatalf("markup sink in chat-routing.js: %s", re.Find(src))
	}
}
