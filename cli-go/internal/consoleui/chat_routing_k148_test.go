package consoleui_test

// chat_routing_k148_test.go: K-148. The console chat goes through the router:
// every one-shot turn starts with a `route` event (persisted as a transcript
// `route` turn), an @prefix override beats the pane's runtime and model, every
// runtime keeps its own native session across turns, and an operator-initiated
// runtime switch carries a bounded, scanned digest of the earlier turns.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

// k148Claude is a fake claude that logs its argv, answers a one-shot with one
// text delta and stamps sess-claude-1 as its native session id.
const k148Claude = `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '@ARGV@'
case " $* " in
  *" --input-format "*) exec sleep 120 ;;
esac
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"claude says hi"}}}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_stop","index":0}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":100,"session_id":"sess-claude-1","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}'
`

type k148 struct {
	ledgerServer
	claudeLog, codexLog string
	store               *consoleui.Transcripts
	frames              <-chan map[string]any
	t                   *testing.T
}

func newK148(t *testing.T) k148 {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	dir := t.TempDir()
	claudeLog, codexLog := filepath.Join(dir, "claude.log"), filepath.Join(dir, "codex.log")
	t.Setenv("HOME", t.TempDir()) // no operator default-runtime / overlay
	t.Setenv("YAKOS_RUNTIME", "")
	t.Setenv("OPENAI_API_KEY", "sk-test-not-real")
	s := newLedgerServerScript(t, nil, strings.ReplaceAll(k148Claude, "@ARGV@", claudeLog))
	codexBin := t.TempDir()
	script := strings.NewReplacer("@FAKE_ARGV_LOG@", codexLog, "@FAKE_PIDS@", filepath.Join(dir, "codex.pids"), "@FAKE_HANG@", filepath.Join(dir, "nohang")).Replace(fakeCodexScript)
	if err := os.WriteFile(filepath.Join(codexBin, "codex"), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", codexBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := s.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond) // the stream registers before the first turn
	return k148{ledgerServer: s, claudeLog: claudeLog, codexLog: codexLog, store: consoleui.NewTranscripts(s.workDir), frames: frames, t: t}
}

// turn posts one auto-routed turn (agent alpha has no pin) and returns the
// frames of that session, up to and including its summary.
func (k k148) turn(sess, conv, task string, extra map[string]any) (frames []map[string]any, status int) {
	k.t.Helper()
	body := map[string]any{"agent": "alpha", "runtime": "", "task": task, "sessionId": sess,
		"operatorId": "alice", "conversationId": conv}
	for key, v := range extra {
		body[key] = v
	}
	resp := k.post(k.t, "/api/chat/dispatch", body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return nil, resp.StatusCode
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev, ok := <-k.frames:
			if !ok {
				k.t.Fatal("stream closed")
			}
			if ev["session_id"] != sess {
				continue
			}
			frames = append(frames, ev)
			if ev["type"] == "summary" || ev["type"] == "error" {
				return frames, http.StatusAccepted
			}
		case <-deadline:
			k.t.Fatalf("no summary for %s; saw %v", sess, frames)
		}
	}
}

func types(frames []map[string]any) []string {
	var out []string
	for _, f := range frames {
		out = append(out, f["type"].(string))
	}
	return out
}

func argvCalls(t *testing.T, path string) [][]string {
	t.Helper()
	raw, _ := os.ReadFile(path)
	var out [][]string
	for _, block := range strings.Split(string(raw), "--- call\n")[1:] {
		out = append(out, strings.Split(strings.TrimRight(block, "\n"), "\n"))
	}
	return out
}

func routeOf(t *testing.T, ev map[string]any) map[string]any {
	t.Helper()
	r, ok := ev["route"].(map[string]any)
	if !ok {
		t.Fatalf("event %v has no route", ev)
	}
	return r
}

// The route is the first event of the turn, before any token, and says where the
// router sent it and who decided.
func TestK148_RouteEventComesBeforeTheFirstToken(t *testing.T) {
	k := newK148(t)
	frames, st := k.turn("s-route-1", "conv-route-1", "hello", nil)
	if st != http.StatusAccepted {
		t.Fatalf("status %d", st)
	}
	got := types(frames)
	if got[0] != "route" {
		t.Fatalf("first event = %v, want route first", got)
	}
	for i, ty := range got {
		if ty == "token" && i == 0 {
			t.Fatalf("a token preceded the route: %v", got)
		}
	}
	r := routeOf(t, frames[0])
	if r["runtime"] != "claude" || r["pinned"] != "router" || r["rule_id"] != "R0" || r["reason"] == "" {
		t.Errorf("route = %v", r)
	}
	// Persisted as a `route` transcript turn that precedes the reply.
	waitForTurns(t, k.store, "conv-route-1", 1)
	entries, _ := k.store.Read("conv-route-1", "")
	var roles []string
	for _, e := range entries {
		roles = append(roles, string(e.Role))
	}
	if strings.Join(roles, ",") != "user,route,assistant,summary" {
		t.Fatalf("transcript roles = %v", roles)
	}
	if e := entries[1]; e.Runtime != "claude" || e.RuleID != "R0" || e.Pinned != "router" || e.Text == "" {
		t.Errorf("route turn = %+v", e)
	}
}

// A pane pinned to a runtime says so; an @prefix override beats it.
func TestK148_OverrideBeatsThePaneSelection(t *testing.T) {
	k := newK148(t)
	frames, _ := k.turn("s-pane", "conv-pane", "q", map[string]any{"runtime": "claude", "model": "sonnet"})
	if r := routeOf(t, frames[0]); r["runtime"] != "claude" || r["pinned"] != "pane" {
		t.Errorf("pane route = %v", r)
	}
	frames, _ = k.turn("s-ovr", "conv-ovr", "q", map[string]any{"runtime": "claude", "model": "sonnet", "overrideRuntime": "codex"})
	r := routeOf(t, frames[0])
	if r["runtime"] != "codex" || r["pinned"] != "override" {
		t.Fatalf("override route = %v", r)
	}
	if calls := argvCalls(t, k.codexLog); len(calls) != 1 {
		t.Fatalf("codex must have run once: %v", calls)
	}
	// The pane's model (sonnet) belongs to claude: it is not handed to codex.
	for _, a := range argvCalls(t, k.codexLog)[0] {
		if a == "sonnet" {
			t.Errorf("the pane's claude model reached codex: %v", argvCalls(t, k.codexLog)[0])
		}
	}
	if len(argvCalls(t, k.claudeLog)) != 1 {
		t.Errorf("claude should have run only for the first turn: %v", argvCalls(t, k.claudeLog))
	}
}

func TestK148_OverrideIsValidated(t *testing.T) {
	k := newK148(t)
	for name, extra := range map[string]map[string]any{
		"unknown runtime":         {"overrideRuntime": "gemini"},
		"model without a runtime": {"overrideModel": "gpt-5"},
		"hostile model":           {"overrideRuntime": "codex", "overrideModel": "x; rm -rf /"},
		"flag as model":           {"overrideRuntime": "codex", "overrideModel": "--yolo"},
		"claude tier on codex":    {"overrideRuntime": "codex", "overrideModel": "opus"},
	} {
		if _, st := k.turn("s-bad-"+strings.ReplaceAll(name, " ", "-"), "conv-bad", "q", extra); st != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, st)
		}
	}
	if n := len(argvCalls(t, k.codexLog)) + len(argvCalls(t, k.claudeLog)); n != 0 {
		t.Errorf("a rejected override started %d processes", n)
	}
}

func TestApplyRouteOverride(t *testing.T) {
	cases := []struct {
		name                        string
		req                         consoleui.DispatchRequest
		wantRT, wantModel, wantPins string
		bad                         bool
	}{
		{"auto pane", consoleui.DispatchRequest{}, "", "", "router", false},
		{"auto sentinel", consoleui.DispatchRequest{Runtime: "auto"}, "auto", "", "router", false},
		{"pinned runtime", consoleui.DispatchRequest{Runtime: "codex", Model: "balanced"}, "codex", "balanced", "pane", false},
		{"override replaces both", consoleui.DispatchRequest{Runtime: "claude", Model: "opus", OverrideRuntime: "codex", OverrideModel: "gpt-5"}, "codex", "gpt-5", "override", false},
		{"override drops the pane model", consoleui.DispatchRequest{Runtime: "claude", Model: "opus", OverrideRuntime: "codex"}, "codex", "", "override", false},
		{"same runtime keeps the pane model", consoleui.DispatchRequest{Runtime: "claude", Model: "opus", OverrideRuntime: "claude"}, "claude", "opus", "override", false},
		{"override on an auto pane", consoleui.DispatchRequest{OverrideRuntime: "agy"}, "agy", "", "override", false},
		{"model alone", consoleui.DispatchRequest{OverrideModel: "gpt-5"}, "", "", "", true},
		{"unknown runtime", consoleui.DispatchRequest{OverrideRuntime: "gemini"}, "", "", "", true},
		{"bad model", consoleui.DispatchRequest{OverrideRuntime: "codex", OverrideModel: "A B"}, "", "", "", true},
	}
	for _, c := range cases {
		rt, model, pins, err := consoleui.ApplyRouteOverrideForTest(c.req)
		if (err != nil) != c.bad {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if !c.bad && (rt != c.wantRT || model != c.wantModel || pins != c.wantPins) {
			t.Errorf("%s: got %q/%q/%q, want %q/%q/%q", c.name, rt, model, pins, c.wantRT, c.wantModel, c.wantPins)
		}
	}
}

// Every runtime keeps its own session id: a conversation that goes claude,
// codex, claude, codex resumes each runtime's own session, not the last one's.
func TestK148_EachRuntimeResumesItsOwnNativeSession(t *testing.T) {
	k := newK148(t)
	const conv = "conv-native"
	k.turn("s-n1", conv, "one", map[string]any{"runtime": "claude"})
	k.turn("s-n2", conv, "two", map[string]any{"runtime": "codex"})
	waitUntil(t, "both ids stored", func() bool {
		return k.store.NativeSession(conv, "claude", "alice") == "sess-claude-1" && k.store.NativeSession(conv, "codex", "alice") == "thread-codex-1"
	})
	k.turn("s-n3", conv, "three", map[string]any{"runtime": "claude"})
	k.turn("s-n4", conv, "four", map[string]any{"runtime": "codex"})

	cl, cx := argvCalls(t, k.claudeLog), argvCalls(t, k.codexLog)
	if len(cl) != 2 || len(cx) != 2 {
		t.Fatalf("calls: claude %d codex %d", len(cl), len(cx))
	}
	if hasResume(cl[0], "sess-claude-1") || !hasResume(cl[1], "sess-claude-1") {
		t.Errorf("claude turn 2 must --resume its own session: %v", cl)
	}
	if strings.Contains(strings.Join(cx[0], " "), "thread-codex-1") || !strings.Contains(strings.Join(cx[1], " "), "thread-codex-1") {
		t.Errorf("codex turn 2 must resume its own thread: %v", cx)
	}
	for _, a := range cx[1] {
		if a == "sess-claude-1" {
			t.Errorf("codex was handed claude's session id: %v", cx[1])
		}
	}
}

// Moving the conversation to another runtime appends the digest to that turn,
// announces it after the route, and does not repeat on the next turn.
func TestK148_RuntimeSwitchCarriesADigest(t *testing.T) {
	k := newK148(t)
	const conv = "conv-handoff"
	k.turn("s-h1", conv, "what is the capital of France", map[string]any{"runtime": "claude"})
	frames, _ := k.turn("s-h2", conv, "and its population", map[string]any{"runtime": "claude", "overrideRuntime": "codex"})
	got := types(frames)
	if got[0] != "route" || got[1] != "handoff" {
		t.Fatalf("events = %v, want route then handoff first", got)
	}
	h, _ := frames[1]["handoff"].(map[string]any)
	if h["from"] != "claude" || h["to"] != "codex" || h["turns"].(float64) < 2 || h["digest_bytes"].(float64) <= 0 {
		t.Errorf("handoff = %v", h)
	}
	prompt := strings.Join(argvCalls(t, k.codexLog)[0], "\n")
	for _, want := range []string{"and its population", "[yakOS handoff", "ran on claude", "user: what is the capital of France", "assistant: claude says hi"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("codex's task lacks %q:\n%s", want, prompt)
		}
	}
	// The transcript keeps the operator's own words.
	waitForTurns(t, k.store, conv, 2)
	entries, _ := k.store.Read(conv, "")
	var users []string
	for _, e := range entries {
		if e.Role == consoleui.RoleUser {
			users = append(users, e.Text)
		}
	}
	if strings.Join(users, "|") != "what is the capital of France|and its population" {
		t.Errorf("user turns = %v", users)
	}
	// Next turn on codex: it has its own session now, no digest, no handoff.
	waitUntil(t, "codex session stored", func() bool { return k.store.NativeSession(conv, "codex", "alice") != "" })
	frames, _ = k.turn("s-h3", conv, "thanks", map[string]any{"runtime": "codex"})
	if types(frames)[1] == "handoff" {
		t.Errorf("a second turn on the same runtime must not hand off: %v", types(frames))
	}
	if strings.Contains(strings.Join(argvCalls(t, k.codexLog)[1], "\n"), "yakOS handoff") {
		t.Errorf("the digest was repeated")
	}
}

// The router's own choices are not handoffs: only an explicit runtime counts.
func TestK148_AutoRoutingNeverHandsOff(t *testing.T) {
	k := newK148(t)
	k.turn("s-a1", "conv-auto", "one", map[string]any{"runtime": "codex"})
	frames, _ := k.turn("s-a2", "conv-auto", "two", nil) // auto: default chain says claude
	for _, ty := range types(frames) {
		if ty == "handoff" {
			t.Fatalf("auto routing produced a handoff: %v", types(frames))
		}
	}
	if len(argvCalls(t, k.claudeLog)) == 1 && strings.Contains(strings.Join(argvCalls(t, k.claudeLog)[0], "\n"), "yakOS handoff") {
		t.Errorf("auto turn carried a digest")
	}
}

func entriesFor(n int, text string) []consoleui.TranscriptEntry {
	var out []consoleui.TranscriptEntry
	for i := 0; i < n; i++ {
		out = append(out,
			consoleui.TranscriptEntry{Role: consoleui.RoleUser, Text: text},
			consoleui.TranscriptEntry{Role: consoleui.RoleRoute, Text: "route noise"},
			consoleui.TranscriptEntry{Role: consoleui.RoleAssistant, Text: text},
			consoleui.TranscriptEntry{Role: consoleui.RoleSummary})
	}
	return out
}

func TestHandoffDigest_IsBounded(t *testing.T) {
	big := strings.Repeat("0123456789", 800) // 8 KB per turn
	text, turns, _ := consoleui.BuildHandoffDigestForTest(entriesFor(200, big), "claude")
	// The caps are written out, not read from the constants: the test pins them.
	if len(text) > 6144 {
		t.Errorf("digest is %d bytes, cap 6144", len(text))
	}
	if turns == 0 || turns > 12 {
		t.Errorf("turns = %d, cap 12", turns)
	}
	// Small turns: the turn cap binds, not the byte cap.
	if _, small, _ := consoleui.BuildHandoffDigestForTest(entriesFor(100, "x"), "claude"); small != 12 {
		t.Errorf("200 tiny turns gave %d, want the cap of 12", small)
	}
	if strings.Contains(text, "route noise") {
		t.Errorf("a route turn leaked into the digest")
	}
	if !strings.Contains(text, "ran on claude") {
		t.Errorf("no label:\n%s", text)
	}
	// Newest turns win and the order is chronological.
	es := []consoleui.TranscriptEntry{
		{Role: consoleui.RoleUser, Text: "oldest"}, {Role: consoleui.RoleAssistant, Text: "middle"},
		{Role: consoleui.RoleUser, Text: "newest"},
	}
	text, turns, _ = consoleui.BuildHandoffDigestForTest(es, "agy")
	if turns != 3 || !(strings.Index(text, "oldest") < strings.Index(text, "middle") && strings.Index(text, "middle") < strings.Index(text, "newest")) {
		t.Errorf("order/turns wrong (%d):\n%s", turns, text)
	}
	if text, turns, _ = consoleui.BuildHandoffDigestForTest(nil, "agy"); text != "" || turns != 0 {
		t.Errorf("an empty conversation has no digest, got %q", text)
	}
	// A multi-byte rune is never cut in half.
	text, _, _ = consoleui.BuildHandoffDigestForTest([]consoleui.TranscriptEntry{{Role: consoleui.RoleUser, Text: strings.Repeat("é", 2000)}}, "agy")
	if !bytes.Equal([]byte(text), []byte(strings.ToValidUTF8(text, ""))) {
		t.Errorf("digest is not valid UTF-8")
	}
}

func TestHandoffDigest_IsScanned(t *testing.T) {
	aws := "AKIA" + "IOSFODNN7EXAMPLE"
	pat := "ghp_" + strings.Repeat("a1", 12)
	sk := "sk-ant-" + strings.Repeat("x9", 12)
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIEabc\n-----END RSA PRIVATE KEY-----"
	jwt := "eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiIxMjM0NTY3ODkwIn0" + ".dozjgNryP4J3jVmNHl0w5N"
	text := strings.Join([]string{"aws " + aws, "gh " + pat, "key " + sk, pem, "jwt " + jwt,
		"Authorization: Bearer abcdefghijklmnop1234", `password = "hunter2hunter2"`, "api_key: abcdef123456"}, "\n")
	digest, _, n := consoleui.BuildHandoffDigestForTest([]consoleui.TranscriptEntry{{Role: consoleui.RoleAssistant, Text: text}}, "claude")
	for _, secret := range []string{aws, pat, sk, "MIIEabc", jwt, "abcdefghijklmnop1234", "hunter2hunter2", "abcdef123456"} {
		if strings.Contains(digest, secret) {
			t.Errorf("secret %q reached the digest:\n%s", secret, digest)
		}
	}
	if n != 8 {
		t.Errorf("redactions = %d, want 8", n)
	}
	// Ordinary prose is untouched, and a scan of clean text counts nothing.
	clean := "the sky is blue; use the token bucket algorithm for rate limiting"
	if got, n := consoleui.ScanSecretsForTest(clean); got != clean || n != 0 {
		t.Errorf("clean text changed: %q (%d)", got, n)
	}
}

// GET /api/models serves the registry; the overlay's switch-off shows as
// usable:false; nothing in the body names a path.
func TestModelsEndpoint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "model-registry.yml"), []byte("version: 1\nmodels:\n  gpt-5.6-sol:\n    enabled: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := routingYakosRoot(t)
	ts, tok, _, _ := newRoutingServer(t, root, nil)

	if resp := get(t, ts.URL+"/api/models", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", resp.StatusCode)
	}
	resp := get(t, ts.URL+"/api/models", tok)
	defer drainClose(resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	var body struct {
		Harnesses []string `json:"harnesses"`
		Models    []struct {
			ID, Harness string
			Usable      bool
			Aliases     []string
		} `json:"models"`
	}
	if err := json.Unmarshal(buf.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if strings.Join(body.Harnesses, ",") != "claude,codex,agy" {
		t.Errorf("harnesses = %v", body.Harnesses)
	}
	seen := map[string]bool{}
	for _, m := range body.Models {
		seen[m.Harness+"/"+m.ID] = m.Usable
		if m.Aliases == nil {
			t.Errorf("%s/%s: aliases must be [] not null", m.Harness, m.ID)
		}
	}
	if usable, ok := seen["claude/opus"]; !ok || !usable {
		t.Errorf("claude/opus missing or unusable: %v", seen["claude/opus"])
	}
	if usable, ok := seen["codex/gpt-5.6-sol"]; !ok || usable {
		t.Errorf("the overlay switched gpt-5.6-sol off: present=%v usable=%v", ok, usable)
	}
	if strings.Contains(buf.String(), home) || strings.Contains(buf.String(), "model-registry") {
		t.Errorf("the body names a path or file: %s", buf.String())
	}
	// Read-only.
	if r := post(t, ts.URL+"/api/models", tok, "{}"); r.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", r.StatusCode)
	}
}

// The transcript schema only grew: a user turn writes none of the new keys, and
// a route turn is an ordinary entry an older reader skips by role.
func TestTranscriptSchemaIsBackwardCompatible(t *testing.T) {
	b, _ := json.Marshal(consoleui.TranscriptEntry{TS: "t", SessionID: "s", ConversationID: "c", Role: consoleui.RoleUser, Text: "hi", Runtime: "claude"})
	for _, key := range []string{"rule_id", "fallback_from", "pinned"} {
		if strings.Contains(string(b), key) {
			t.Errorf("a user turn writes %s: %s", key, b)
		}
	}
	old := `{"ts":"t","session_id":"s","conversation_id":"c","operator_id":"alice","role":"user","text":"old line"}` + "\n" +
		`{"ts":"t","session_id":"s","conversation_id":"c","role":"route","text":"why","runtime":"codex","rule_id":"R1","pinned":"pane"}` + "\n"
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "chats"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chats", "c.ndjson"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := consoleui.NewTranscripts(dir).Read("c", "")
	if err != nil || len(entries) != 2 || entries[1].Role != consoleui.RoleRoute || entries[1].RuleID != "R1" || entries[0].Text != "old line" {
		t.Fatalf("read = %+v err=%v", entries, err)
	}
}

// An interactive pane is one long-lived engine, so the router decides once, at
// its first turn: the handler sends that route; the engine's own per-turn runs do
// not repeat it.
func TestK148_InteractivePaneRoutesOncePerPane(t *testing.T) {
	f := newResumeServer(t, "codex", fakeCodexScript)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	frames := f.sseFrames(t, ctx, "alice")
	time.Sleep(100 * time.Millisecond)
	const conv = "conv-int-route"
	t.Cleanup(func() { f.mgr.Close(conv) })

	var routes []map[string]any
	summaries := 0
	collect := func(wantSummaries int) {
		deadline := time.After(15 * time.Second)
		for summaries < wantSummaries {
			select {
			case ev := <-frames:
				switch ev["type"] {
				case "route":
					routes = append(routes, ev)
				case "summary":
					summaries++
				}
			case <-deadline:
				t.Fatalf("timed out after %d summaries; routes=%d", summaries, len(routes))
			}
		}
	}
	f.dispatchTurn(t, "codex", conv, "first")
	collect(1)
	if st := f.sendTurn(t, conv, "second"); st != http.StatusAccepted {
		t.Fatalf("send: %d", st)
	}
	collect(2)
	if len(routes) != 1 {
		t.Fatalf("want exactly one route for the pane, got %d", len(routes))
	}
	if r := routeOf(t, routes[0]); r["runtime"] != "codex" || r["pinned"] != "pane" {
		t.Errorf("route = %v", r)
	}
	store := consoleui.NewTranscripts(f.workDir)
	entries, _ := store.Read(conv, "")
	n := 0
	for _, e := range entries {
		if e.Role == consoleui.RoleRoute {
			n++
		}
	}
	if n != 1 {
		t.Errorf("transcript holds %d route turns, want 1", n)
	}
}
