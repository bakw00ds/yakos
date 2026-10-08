package openai_test

// protocol_test.go: the wire contract of the OpenAI-compatible endpoint (K-150).
// CI runs these as the gateway-protocol job; they need no network.

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/modelreg"
)

func writeFile(path string) error { return os.WriteFile(path, []byte("x"), 0o600) }

func TestProtocol_ModelsListShape(t *testing.T) {
	f := newFixture(t)
	f.signedIn["codex"] = false
	resp, raw := f.do(http.MethodGet, "/v1/models", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	m := f.decode(raw)
	if m["object"] != "list" {
		t.Fatalf("object = %v", m["object"])
	}
	ids := map[string]bool{}
	for _, d := range m["data"].([]any) {
		o := d.(map[string]any)
		if o["object"] != "model" || o["owned_by"] != "yakos" {
			t.Fatalf("bad model object %v", o)
		}
		if _, ok := o["created"].(float64); !ok {
			t.Fatalf("created missing in %v", o)
		}
		ids[o["id"].(string)] = true
	}
	for _, want := range []string{"yakos/auto", "yakos/agent/lead", "yakos/agent/alpha"} {
		if !ids[want] {
			t.Errorf("missing %s in %v", want, ids)
		}
	}
	var claudeSeen bool
	for id := range ids {
		if strings.HasPrefix(id, "claude/") {
			claudeSeen = true
		}
		if strings.HasPrefix(id, "codex/") {
			t.Errorf("codex is not signed in but %s is listed", id)
		}
	}
	if !claudeSeen {
		t.Errorf("no claude/<model> entry in %v", ids)
	}
	f.signedIn["codex"] = true
	_, raw = f.do(http.MethodGet, "/v1/models", nil, nil)
	if !strings.Contains(string(raw), `"id":"codex/`) {
		t.Errorf("codex signed in but not listed: %s", raw)
	}
}

func TestProtocol_ChatNonStream(t *testing.T) {
	f := newFixture(t)
	resp, raw := f.chat("yakos/auto", []map[string]any{user("hello")}, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	m := f.decode(raw)
	if m["object"] != "chat.completion" || !strings.HasPrefix(m["id"].(string), "chatcmpl-") || m["model"] != "yakos/auto" {
		t.Fatalf("envelope %v", m)
	}
	ch := m["choices"].([]any)[0].(map[string]any)
	msg := ch["message"].(map[string]any)
	if msg["role"] != "assistant" || msg["content"] != "claude says hi" || ch["finish_reason"] != "stop" {
		t.Fatalf("choice %v", ch)
	}
	u := m["usage"].(map[string]any)
	if u["prompt_tokens"] != 130.0 || u["completion_tokens"] != 5.0 || u["total_tokens"] != 135.0 {
		t.Fatalf("usage %v", u)
	}
	if u["prompt_tokens_details"].(map[string]any)["cached_tokens"] != 100.0 {
		t.Fatalf("cached tokens %v", u)
	}
	for k := range u {
		if strings.Contains(k, "cost") || strings.Contains(k, "usd") {
			t.Fatalf("usage carries a price: %v", u)
		}
	}
	y := m["yakos"].(map[string]any)
	rt := y["route"].(map[string]any)
	if rt["runtime"] != "claude" || rt["rule"] == "" || rt["reason"] == "" || rt["class"] != "default" {
		t.Fatalf("route %v", rt)
	}
	if _, ok := rt["policy_sha"]; !ok {
		t.Fatalf("route has no policy_sha: %v", rt)
	}
	if _, ok := rt["model"]; !ok {
		t.Fatalf("route has no model: %v", rt)
	}
	if y["conversation"] == "" || resp.Header.Get("X-Yakos-Conversation") != y["conversation"] {
		t.Fatalf("conversation %v vs header %q", y["conversation"], resp.Header.Get("X-Yakos-Conversation"))
	}
}

func readSSE(raw []byte) (frames []string) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
	}
	return frames
}

func TestProtocol_ChatStream(t *testing.T) {
	f := newFixture(t)
	resp, raw := f.chat("yakos/auto", []map[string]any{user("hello")},
		map[string]any{"stream": true, "stream_options": map[string]any{"include_usage": true}}, nil)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d type %q: %s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
	}
	frames := readSSE(raw)
	if len(frames) < 5 || frames[len(frames)-1] != "[DONE]" {
		t.Fatalf("frames %q", frames)
	}
	var text strings.Builder
	var sawRole, sawFinish, sawUsage bool
	for _, fr := range frames[:len(frames)-1] {
		c := f.decode([]byte(fr))
		if c["object"] != "chat.completion.chunk" {
			t.Fatalf("object %v", c["object"])
		}
		choices := c["choices"].([]any)
		if len(choices) == 0 {
			u := c["usage"].(map[string]any)
			if u["total_tokens"] != 135.0 {
				t.Fatalf("usage %v", u)
			}
			sawUsage = true
			continue
		}
		ch := choices[0].(map[string]any)
		d := ch["delta"].(map[string]any)
		if d["role"] == "assistant" {
			sawRole = true
			if c["yakos"].(map[string]any)["route"].(map[string]any)["runtime"] != "claude" {
				t.Fatalf("first chunk route %v", c["yakos"])
			}
		}
		if s, ok := d["content"].(string); ok {
			text.WriteString(s)
		}
		if ch["finish_reason"] == "stop" {
			sawFinish = true
		}
	}
	if text.String() != "claude says hi" || !sawRole || !sawFinish || !sawUsage {
		t.Fatalf("text %q role %v finish %v usage %v", text.String(), sawRole, sawFinish, sawUsage)
	}
	// Without include_usage there is no usage frame.
	_, raw = f.chat("yakos/auto", []map[string]any{user("again")}, map[string]any{"stream": true}, nil)
	if strings.Contains(string(raw), `"usage"`) {
		t.Fatalf("usage frame without include_usage: %s", raw)
	}
}

func TestProtocol_AuthHostOriginNegatives(t *testing.T) {
	f := newFixture(t)
	body := map[string]any{"model": "yakos/auto", "messages": []map[string]any{user("x")}}
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no token", map[string]string{"Authorization": ""}, 401},
		{"wrong token", map[string]string{"Authorization": "Bearer " + strings.Repeat("0", 64)}, 401},
		{"not bearer", map[string]string{"Authorization": "Basic " + testToken}, 401},
		{"foreign host", map[string]string{"Host": "evil.example:" + f.port}, 403},
		{"wrong port in host", map[string]string{"Host": "127.0.0.1:1"}, 403},
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}, 403},
		{"loopback origin, other port", map[string]string{"Origin": "http://127.0.0.1:3000"}, 403},
		{"own origin", map[string]string{"Origin": "http://127.0.0.1:" + f.port}, 200},
		{"localhost host", map[string]string{"Host": "localhost:" + f.port}, 200},
	}
	for _, c := range cases {
		resp, raw := f.do(http.MethodPost, "/v1/chat/completions", body, c.hdr)
		if resp.StatusCode != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, resp.StatusCode, c.want, raw)
		}
	}
	if resp, _ := f.do(http.MethodGet, "/v1/models", nil, map[string]string{"Authorization": ""}); resp.StatusCode != 401 {
		t.Errorf("models without token: %d", resp.StatusCode)
	}
	if resp, _ := f.do(http.MethodGet, "/v1/models", nil, map[string]string{"Host": "evil.example:" + f.port}); resp.StatusCode != 403 {
		t.Errorf("models foreign host: %d", resp.StatusCode)
	}
}

func TestProtocol_RequestRefusals(t *testing.T) {
	f := newFixture(t)
	tool := []map[string]any{{"type": "function", "function": map[string]any{"name": "x"}}}
	cases := []struct {
		name  string
		model string
		msgs  []map[string]any
		extra map[string]any
		want  int
		frag  string
	}{
		{"unknown model", "gpt-4o", []map[string]any{user("x")}, nil, 404, "model_not_found"},
		{"unknown agent", "yakos/agent/nope", []map[string]any{user("x")}, nil, 404, "model_not_found"},
		{"tools", "yakos/auto", []map[string]any{user("x")}, map[string]any{"tools": tool}, 400, "tools and functions"},
		{"functions", "yakos/auto", []map[string]any{user("x")}, map[string]any{"functions": tool}, 400, "tools and functions"},
		{"tool_choice", "yakos/auto", []map[string]any{user("x")}, map[string]any{"tool_choice": "auto"}, 400, "tools and functions"},
		{"tool role", "yakos/auto", []map[string]any{user("x"), {"role": "tool", "content": "r"}, user("y")}, nil, 400, "role"},
		{"tool_calls", "yakos/auto", []map[string]any{{"role": "assistant", "content": "", "tool_calls": tool}, user("y")}, nil, 400, "tool calls"},
		{"last is assistant", "yakos/auto", []map[string]any{user("x"), {"role": "assistant", "content": "a"}}, nil, 400, "last message"},
		{"no messages", "yakos/auto", nil, nil, 400, "messages"},
		{"image part", "yakos/auto", []map[string]any{{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "x"}}}}}, nil, 400, "not supported"},
		{"n=2", "yakos/auto", []map[string]any{user("x")}, map[string]any{"n": 2}, 400, "n must be 1"},
	}
	for _, c := range cases {
		resp, raw := f.chat(c.model, c.msgs, c.extra, nil)
		if resp.StatusCode != c.want || !strings.Contains(string(raw), c.frag) {
			t.Errorf("%s: status %d body %s", c.name, resp.StatusCode, raw)
		}
	}
	if n := strings.Count(readFile(f.claudeLog), "--- call"); n != 0 {
		t.Errorf("a refused request launched claude %d times", n)
	}
	// An empty tools array is not a tool request; text parts and sampling fields pass.
	resp, raw := f.chat("yakos/auto", []map[string]any{{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hi"}}}},
		map[string]any{"tools": []any{}, "temperature": 0.2, "max_tokens": 10, "user": "u"}, nil)
	if resp.StatusCode != 200 {
		t.Errorf("benign request: %d %s", resp.StatusCode, raw)
	}
	if resp, _ := f.do(http.MethodPost, "/v1/chat/completions", []byte("{nope"), nil); resp.StatusCode != 400 {
		t.Errorf("bad json: %d", resp.StatusCode)
	}
	if resp, _ := f.do(http.MethodPost, "/v1/chat/completions", []byte(`{}`), map[string]string{"Content-Type": "text/plain"}); resp.StatusCode != 415 {
		t.Errorf("content type: %d", resp.StatusCode)
	}
	big := `{"model":"yakos/auto","messages":[{"role":"user","content":"` + strings.Repeat("a", 1<<20) + `"}]}`
	if resp, _ := f.do(http.MethodPost, "/v1/chat/completions", []byte(big), nil); resp.StatusCode != 413 {
		t.Errorf("oversized body: %d", resp.StatusCode)
	}
	if resp, _ := f.do(http.MethodGet, "/v1/chat/completions", nil, nil); resp.StatusCode != 405 {
		t.Errorf("GET completions: %d", resp.StatusCode)
	}
	if resp, _ := f.do(http.MethodPost, "/v1/models", []byte(`{}`), nil); resp.StatusCode != 405 {
		t.Errorf("POST models: %d", resp.StatusCode)
	}
	if resp, _ := f.do(http.MethodGet, "/v1/nope", nil, nil); resp.StatusCode != 404 {
		t.Errorf("unknown route: %d", resp.StatusCode)
	}
}

func TestProtocol_ConversationResume(t *testing.T) {
	f := newFixture(t)
	resp, raw := f.chat("yakos/auto", []map[string]any{user("first question")}, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("turn 1: %d %s", resp.StatusCode, raw)
	}
	conv := resp.Header.Get("X-Yakos-Conversation")
	if conv == "" {
		t.Fatal("no conversation header")
	}
	if strings.Contains(readFile(f.claudeLog), "--resume") {
		t.Fatal("first turn resumed a session")
	}
	// Turn 2 resumes: the client's earlier messages are not replayed (the
	// transcript and the native session are the truth).
	resp, raw = f.chat("yakos/auto", []map[string]any{user("first question"), {"role": "assistant", "content": "OLDREPLY"}, user("second question")},
		nil, map[string]string{"X-Yakos-Conversation": conv})
	if resp.StatusCode != 200 || resp.Header.Get("X-Yakos-Conversation") != conv {
		t.Fatalf("turn 2: %d %s", resp.StatusCode, raw)
	}
	calls := strings.Split(readFile(f.claudeLog), "--- call")
	if len(calls) != 3 {
		t.Fatalf("claude launched %d times", len(calls)-1)
	}
	second := calls[2]
	if !strings.Contains(second, "--resume\nsess-claude-1\n") {
		t.Errorf("turn 2 did not resume the native session:\n%s", second)
	}
	if strings.Contains(second, "OLDREPLY") || strings.Contains(second, "yakOS handoff") {
		t.Errorf("turn 2 replayed earlier messages:\n%s", second)
	}
	entries, err := f.store.Read(conv, "openai-compat")
	must(t, err)
	var users, assistants int
	for _, e := range entries {
		switch e.Role {
		case consoleui.RoleUser:
			users++
		case consoleui.RoleAssistant:
			assistants++
		}
	}
	if users != 2 || assistants != 2 {
		t.Errorf("transcript has %d user and %d assistant turns, want 2 and 2", users, assistants)
	}
	if resp, _ := f.chat("yakos/auto", []map[string]any{user("x")}, nil, map[string]string{"X-Yakos-Conversation": "../etc"}); resp.StatusCode != 400 {
		t.Errorf("bad conversation id: %d", resp.StatusCode)
	}
}

func TestProtocol_ConversationOwnerMismatch(t *testing.T) {
	f := newFixture(t)
	must(t, f.store.Append(consoleui.TranscriptEntry{SessionID: "s", ConversationID: "console-conv-1", OperatorID: "alice",
		Role: consoleui.RoleUser, Text: "private"}))
	must(t, f.store.SetNativeSession("console-conv-1", "claude", "sess-alice", "alice"))
	resp, raw := f.chat("yakos/auto", []map[string]any{user("let me in")}, nil, map[string]string{"X-Yakos-Conversation": "console-conv-1"})
	if resp.StatusCode != 403 || !strings.Contains(string(raw), "conversation_forbidden") {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if readFile(f.claudeLog) != "" {
		t.Errorf("a refused resume launched claude:\n%s", readFile(f.claudeLog))
	}
	entries, _ := f.store.Read("console-conv-1", "alice")
	if len(entries) != 1 {
		t.Errorf("the owner's transcript grew: %d entries", len(entries))
	}
}

func TestProtocol_DigestBoundedAndScanned(t *testing.T) {
	f := newFixture(t)
	key := "AKIA" + "ABCDEFGHIJKLMNOP"
	msgs := []map[string]any{{"role": "system", "content": "be brief"}}
	for i := 0; i < 80; i++ {
		msgs = append(msgs, user("question "+strings.Repeat("q", 600)), map[string]any{"role": "assistant", "content": "answer " + strings.Repeat("a", 600)})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": "my key is " + key}, map[string]any{"role": "assistant", "content": "noted"}, user("final question"))
	resp, raw := f.chat("yakos/auto", msgs, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	rt := f.decode(raw)["yakos"].(map[string]any)["route"].(map[string]any)
	if rt["class"] != "sensitive" || rt["runtime"] != "claude" {
		t.Fatalf("a key in the history did not classify the turn sensitive: %v", rt)
	}
	argv := readFile(f.claudeLog)
	if strings.Contains(argv, key) {
		t.Fatal("the key reached the runtime unredacted")
	}
	i := strings.Index(argv, "[yakOS handoff")
	if i < 0 {
		t.Fatalf("no digest in the task:\n%.400s", argv)
	}
	if n := len(argv[i:]); n > 8<<10 {
		t.Errorf("digest is %d bytes, want it bounded near 6 KiB", n)
	}
	if !strings.Contains(argv, "[redacted]") || !strings.Contains(argv, "final question") {
		t.Errorf("redaction marker or the turn missing:\n%.600s", argv)
	}
}

func TestProtocol_SensitiveHistoryOverridesRuntimeChoice(t *testing.T) {
	f := newFixture(t)
	reg, err := modelreg.Load(modelreg.Options{})
	must(t, err)
	var codexModel string
	for _, e := range reg.Entries() {
		if e.Harness == "codex" && e.Usable() {
			codexModel = e.ID
			break
		}
	}
	if codexModel == "" {
		t.Skip("no codex model in the embedded catalog")
	}
	// Without a secret the explicit runtime holds.
	resp, raw := f.chat("codex/"+codexModel, []map[string]any{user("hello")}, nil, nil)
	if resp.StatusCode != 200 || f.decode(raw)["yakos"].(map[string]any)["route"].(map[string]any)["runtime"] != "codex" {
		t.Fatalf("plain codex turn: %d %s", resp.StatusCode, raw)
	}
	// With a key in the history the same request is placed on claude.
	awsKey := "AKIA" + "ABCDEFGHIJKLMNOP"
	resp, raw = f.chat("codex/"+codexModel, []map[string]any{
		user("token is " + awsKey), {"role": "assistant", "content": "ok"}, user("go on")}, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	rt := f.decode(raw)["yakos"].(map[string]any)["route"].(map[string]any)
	if rt["runtime"] != "claude" || rt["class"] != "sensitive" {
		t.Fatalf("route %v", rt)
	}
	if strings.Count(readFile(f.codexLog), "--- call") != 1 {
		t.Errorf("codex ran for the sensitive turn:\n%s", readFile(f.codexLog))
	}
}

func TestProtocol_SystemMessageAfterPersona(t *testing.T) {
	f := newFixture(t)
	for _, sys := range []string{"CLIENTSYS-ONE", "CLIENTSYS-TWO"} {
		resp, raw := f.chat("yakos/agent/alpha", []map[string]any{{"role": "system", "content": sys}, user("hi")}, nil, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("%d %s", resp.StatusCode, raw)
		}
	}
	calls := strings.Split(readFile(f.claudeLog), "--- call\n")[1:]
	if len(calls) != 2 {
		t.Fatalf("calls %d", len(calls))
	}
	var personas []string
	for i, c := range calls {
		args := strings.Split(strings.TrimRight(c, "\n"), "\n")
		for j, a := range args {
			if (a == "--append-system-prompt" || a == "--agents") && j+1 < len(args) {
				if strings.Contains(args[j+1], "CLIENTSYS") {
					t.Errorf("call %d: the client system text entered %s", i, a)
				}
				personas = append(personas, a+"="+args[j+1])
			}
		}
		if !strings.Contains(c, "Test agent alpha") {
			t.Errorf("call %d: the turn did not run as agent alpha", i)
		}
		if !strings.Contains(c, "CLIENTSYS") {
			t.Errorf("call %d: the client system text was dropped", i)
		}
	}
	if len(personas) != 2 || personas[0] != personas[1] {
		t.Errorf("persona bytes differ between conversations: %q", personas)
	}
}

func TestProtocol_LedgerRowsCarrySurface(t *testing.T) {
	f := newFixture(t)
	if resp, raw := f.chat("yakos/auto", []map[string]any{user("hello")}, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	var started, finished int
	for _, ev := range f.ledger() {
		switch ev["type"] {
		case "dispatch_started":
			started++
		case "dispatch_finished":
			finished++
			if ev["surface"] != "openai-compat" {
				t.Errorf("finished row surface = %v", ev["surface"])
			}
		}
	}
	if started != 1 || finished != 1 {
		t.Fatalf("ledger rows: %d started, %d finished (%v)", started, finished, f.ledger())
	}
}

func TestProtocol_RunFailure(t *testing.T) {
	f := newFixture(t)
	must(t, writeFile(f.fail))
	resp, raw := f.chat("yakos/auto", []map[string]any{user("x")}, nil, nil)
	if resp.StatusCode != 502 || !strings.Contains(string(raw), "model_run_failed") || strings.Contains(string(raw), f.workspace) {
		t.Fatalf("failed run: %d %s", resp.StatusCode, raw)
	}
	resp, raw = f.chat("yakos/auto", []map[string]any{user("x")}, map[string]any{"stream": true}, nil)
	if !strings.Contains(string(raw), "model_run_failed") {
		t.Fatalf("failed streamed run: %d %s", resp.StatusCode, raw)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") && !strings.HasSuffix(strings.TrimSpace(string(raw)), "[DONE]") {
		t.Fatalf("an errored stream did not end with [DONE]: %s", raw)
	}
}

// A second turn on a conversation that is still running is refused, not queued
// (two claude processes must not write one session).
func TestProtocol_ConversationBusy(t *testing.T) {
	f := newFixture(t)
	must(t, writeFile(f.hang))
	hdr := map[string]string{"X-Yakos-Conversation": "busy-conv-1"}
	first := make(chan int, 1)
	go func() {
		resp, _ := f.chat("yakos/auto", []map[string]any{user("slow")}, nil, hdr)
		first <- resp.StatusCode
	}()
	deadline := time.Now().Add(20 * time.Second)
	for readFile(f.started) == "" {
		if time.Now().After(deadline) {
			t.Fatal("the first turn never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, raw := f.chat("yakos/auto", []map[string]any{user("again")}, nil, hdr)
	if resp.StatusCode != 409 || !strings.Contains(string(raw), "conversation_busy") {
		t.Fatalf("second turn: %d %s", resp.StatusCode, raw)
	}
	// Let the hung first turn end: remove the hang marker is too late (it is
	// already sleeping), so the test ends by closing the server in Cleanup,
	// which cancels the request context and kills the fake.
	_ = os.Remove(f.hang)
}

// A conversation moved to an explicit other runtime carries the console's
// bounded digest of the earlier turns, taken from the transcript.
func TestProtocol_ResumeOnOtherRuntimeCarriesDigest(t *testing.T) {
	f := newFixture(t)
	resp, raw := f.chat("yakos/auto", []map[string]any{user("remember the word PINEAPPLE")}, nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	conv := resp.Header.Get("X-Yakos-Conversation")
	reg, err := modelreg.Load(modelreg.Options{})
	must(t, err)
	var codexModel string
	for _, e := range reg.Entries() {
		if e.Harness == "codex" && e.Usable() {
			codexModel = e.ID
			break
		}
	}
	if codexModel == "" {
		t.Skip("no codex model in the embedded catalog")
	}
	resp, raw = f.chat("codex/"+codexModel, []map[string]any{user("what was the word")}, nil, map[string]string{"X-Yakos-Conversation": conv})
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, raw)
	}
	codex := readFile(f.codexLog)
	if !strings.Contains(codex, "[yakOS handoff") || !strings.Contains(codex, "PINEAPPLE") {
		t.Errorf("codex got no digest of the claude turn:\n%.800s", codex)
	}
}
