package consoleui_test

// chat_card_k173fix1_test.go: K-173 fix round 1. The knowledge-pack pre-check
// runs for every pane, the Authorization / Cookie redaction covers the header
// shapes a pasted request prints, stored cards are cut at 16 KiB with a text
// marker and scanned (thinking included), and an SDK pane keeps the project
// disable and the sensitive handling.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// k173PackRun starts an auto interactive pane whose router rule moves any task
// over 5 bytes to codex, with ruleBody as the only rules file in the pack, and
// returns the route frame and the codex argv calls.
func k173PackRun(t *testing.T, ruleBody, task, forcePack string) (map[string]any, [][]string) {
	t.Helper()
	k := newK148(t)
	if forcePack != "" {
		t.Cleanup(consoleui.SetPrecheckPackForTest(forcePack))
	}
	dir := routerpolicy.StateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := "rules:\n  - match: {task_bytes_gt: 5}\n    action: {runtime: codex}\n"
	if err := os.WriteFile(routerpolicy.Path(dir), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(k.yakosRoot, "lib", "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rules, "deploy.md"), []byte(ruleBody), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if st := k.interactiveFirstTurn("s-kb-auto", "conv-kb-auto", "", task); st != http.StatusAccepted {
		t.Fatalf("status %d", st)
	}
	ev := k.firstOf("s-kb-auto")
	if ev["type"] != "route" {
		t.Fatalf("first frame = %v, want a route", ev)
	}
	return routeOf(t, ev), argvCalls(t, k.codexLog)
}

// An auto pane whose pre-route runtime is claude, with a router rule that would
// move the turn to codex (task_bytes_gt). The rule file lives where the router
// reads it (routerpolicy.StateDir, under HOME), so the benign case pins the
// precondition: the rule fires and the turn goes to codex. A pack holding a
// credential then makes the turn sensitive, stays on claude, and codex is never
// called (the sec-366 M1 gate, `runtimeName != "claude"`, would let codex run).
func TestK173Fix1_PackIsScannedWhenARuleMovesAClaudePaneToCodex(t *testing.T) {
	run := func(t *testing.T, ruleBody string) (map[string]any, [][]string) {
		return k173PackRun(t, ruleBody, "say hello", "")
	}
	t.Run("benign pack: the rule moves the pane to codex", func(t *testing.T) {
		r, _ := run(t, "# deploy\n\nRun the release job after the tests pass.\n")
		if r["runtime"] != "codex" || r["class"] == "sensitive" {
			t.Errorf("route = %v, want codex (rule fired)", r)
		}
	})
	t.Run("pack with a credential: claude, sensitive, codex never called", func(t *testing.T) {
		// Compose refuses a rules file holding a secret shape, so the pack is
		// forced here: the pre-check's own scan is what is under test.
		r, calls := k173PackRun(t, "# deploy\n\nRun the release job.\n", "say hello", "# deploy\n\nkey: "+"AKIA"+"IOSFODNN7EXAMPLE\n")
		if r["runtime"] != "claude" || r["class"] != "sensitive" {
			t.Errorf("route = %v, want claude/sensitive", r)
		}
		if len(calls) != 0 {
			t.Errorf("codex was started with an unscanned pack: %v", calls)
		}
	})
}

// The four Authorization shapes the first pattern missed, plus Cookie values,
// directly and through the handoff digest. Values are fixtures.
func TestK173Fix1_AuthorizationAndCookieShapes(t *testing.T) {
	cases := []struct{ name, text, secret string }{
		{"json header array", `{"Authorization":["Basic ` + "dXNlcjpodW50ZXIy" + `"]}`, "dXNlcjpodW50ZXIy"},
		{"json header array bearer", `{"authorization": ["Bearer ` + "abc123def456ghi" + `"]}`, "abc123def456ghi"},
		{"go header map", "headers: map[Authorization:[Basic " + "dXNlcjpodW50ZXIy" + "] Accept:[*/*]]", "dXNlcjpodW50ZXIy"},
		{"cgi env", "HTTP_AUTHORIZATION=Basic " + "dXNlcjpodW50ZXIy", "dXNlcjpodW50ZXIy"},
		{"proxy cgi env", "HTTP_PROXY_AUTHORIZATION=Basic " + "cHJveHk6cGFzc3dvcmQ=", "cHJveHk6cGFzc3dvcmQ="},
		{"plain header", "Authorization: Basic " + "dXNlcjpodW50ZXIy", "dXNlcjpodW50ZXIy"},
		{"cookie", "Cookie: session=" + "9f8e7d6c5b4a3210; theme=dark", "9f8e7d6c5b4a3210"},
		{"set-cookie", "Set-Cookie: sid=" + "abcdef0123456789; HttpOnly", "abcdef0123456789"},
	}
	for _, c := range cases {
		if got, n := consoleui.ScanSecretsForTest(c.text); strings.Contains(got, c.secret) || n == 0 {
			t.Errorf("%s: secret survived the scan (%d): %q", c.name, n, got)
		}
		digest, _, n := consoleui.BuildHandoffDigestForTest([]consoleui.TranscriptEntry{{Role: consoleui.RoleUser, Text: c.text}}, "claude")
		if strings.Contains(digest, c.secret) || n == 0 {
			t.Errorf("%s: secret survived the digest (%d):\n%s", c.name, n, digest)
		}
	}
	// Prose that names the words is left alone.
	for _, prose := range []string{"the authorization step comes first", "a cookie recipe: flour, butter", "authorization: none"} {
		if got, n := consoleui.ScanSecretsForTest(prose); n != 0 {
			t.Errorf("prose redacted: %q -> %q", prose, got)
		}
	}
}

// A codex turn whose command output and reasoning are larger than the card cap
// and hold a secret-shaped value in the reasoning.
const fakeCodexBigCardsScript = `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '@FAKE_ARGV_LOG@'
echo $$ >> '@FAKE_PIDS@'
PAD=$(head -c 40000 /dev/zero | tr '\0' a)
printf '%s\n' '{"type":"thread.started","thread_id":"thread-codex-1"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"r","type":"reasoning","text":"AWS_SECRET_ACCESS_KEY=@KEY@ '"$PAD"'"}}'
printf '%s\n' '{"type":"item.started","item":{"id":"c","type":"command_execution","command":"cat big.txt","status":"in_progress"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"c","type":"command_execution","command":"cat big.txt","aggregated_output":"'"$PAD"'","exit_code":0,"status":"completed"}}'
printf '%s\n' '{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"all done"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}'
`

// A stored tool result and a thinking block are cut at the 16 KiB cap with a
// text marker (and the flag), and the thinking text is scanned for secrets.
func TestK173Fix1_CardsAreCutAtTheCapWithAMarkerAndThinkingIsScanned(t *testing.T) {
	const key = "wJalrXUtnFEMI" + "K7MDENGbPxRfiCYEXAMPLE"
	script := strings.ReplaceAll(fakeCodexBigCardsScript, "@KEY@", key)
	f := newResumeServer(t, "codex", script)
	const conv = "conv-bigcards"
	t.Cleanup(func() { f.mgr.Close(conv) })
	f.dispatchTurn(t, "codex", conv, "show the big file")
	store := consoleui.NewTranscripts(f.workDir)
	waitForTurns(t, store, conv, 1)
	entries, _ := store.Read(conv, "")
	const capBytes = 16 << 10
	var sawResult, sawThink bool
	for _, e := range entries {
		if e.Role != consoleui.RoleToolResult && e.Role != consoleui.RoleThinking {
			continue
		}
		if e.Role == consoleui.RoleToolResult {
			sawResult = true
		} else {
			sawThink = true
		}
		if !e.Truncated || !strings.HasSuffix(e.Text, "truncated at 16 KiB …]") {
			t.Errorf("%s: truncated=%v, text ends %q", e.Role, e.Truncated, e.Text[max(0, len(e.Text)-40):])
		}
		if len(e.Text) > capBytes+64 {
			t.Errorf("%s: stored %d bytes, want <= cap plus the marker", e.Role, len(e.Text))
		}
		if strings.Contains(e.Text, "tool output truncated") {
			t.Errorf("%s: the dispatch layer's marker survived the cut", e.Role)
		}
		if strings.Contains(e.Text, key) {
			t.Errorf("%s: kept the secret-shaped value", e.Role)
		}
	}
	if !sawResult || !sawThink {
		t.Fatalf("result=%v thinking=%v in %d entries", sawResult, sawThink, len(entries))
	}
	// A card under the cap carries no marker.
	if got, n := consoleui.ScanSecretsForTest("short"); got != "short" || n != 0 {
		t.Errorf("scan changed short text: %q", got)
	}
}

// The thinking buffer stops at the cap even when one delta alone is far larger.
func TestK173Fix1_CapCardText(t *testing.T) {
	for _, tc := range []struct {
		in    int
		wantT bool
	}{{100, false}, {16 << 10, false}, {(16 << 10) + 1, true}, {100000, true}} {
		got, cut := consoleui.CapCardTextForTest(strings.Repeat("é", tc.in/2) + strings.Repeat("x", tc.in%2))
		if cut != tc.wantT {
			t.Errorf("in=%d: cut=%v want %v", tc.in, cut, tc.wantT)
		}
		if cut && (!strings.HasSuffix(got, "[… truncated at 16 KiB …]") || len(got) > (16<<10)+64) {
			t.Errorf("in=%d: bad cut: len %d", tc.in, len(got))
		}
	}
}

// sendSDK posts an SDK first turn and returns the HTTP status and, on 202, the
// first route or error frame.
func (s *sdkK173) sendSDK(t *testing.T, sess, task string) (int, consoleui.SSEEvent) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"agent": "claude", "task": task, "sessionId": sess,
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
		return resp.StatusCode, consoleui.SSEEvent{}
	}
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-s.frames:
			if ev.SessionID == sess && (ev.Type == "route" || ev.Type == "error") {
				return resp.StatusCode, ev
			}
		case <-deadline:
			t.Fatalf("no route or error frame for %s", sess)
		}
	}
}

// A project that disabled claude keeps an SDK pane from starting: refused, no
// engine, no spawn (the deleted tolerance test's "project disable stays").
func TestK173Fix1_SDKPaneProjectDisableStays(t *testing.T) {
	s := newSDKK173(t, "router:\n  disable_runtimes: [claude]\n", "")
	st, ev := s.sendSDK(t, "s-rtdis", "hi")
	if st == http.StatusAccepted && ev.Type != "error" {
		t.Fatalf("status %d frame %+v: a disabled runtime started an SDK pane", st, ev)
	}
	time.Sleep(200 * time.Millisecond)
	if models, spawns := s.seen(); len(models) != 0 || spawns != 0 {
		t.Errorf("an engine was built (%v) or spawned (%d) on a disabled runtime", models, spawns)
	}
}

// A sensitive first turn on an SDK pane runs on claude/sensitive (K-140: claude
// is where a sensitive task goes), and with claude disabled by the project it is
// refused rather than placed elsewhere (the "sensitive refusal stays" case).
func TestK173Fix1_SDKPaneSensitiveFirstTurn(t *testing.T) {
	task := "deploy with AKIA" + "IOSFODNN7EXAMPLE please"
	s := newSDKK173(t, "", "")
	st, ev := s.sendSDK(t, "s-sens", task)
	if st != http.StatusAccepted || ev.Type != "route" || ev.Route == nil || ev.Route.Runtime != "claude" || ev.Route.Class != "sensitive" {
		t.Fatalf("status %d frame %+v, want a claude/sensitive route", st, ev)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if models, _ := s.seen(); len(models) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sensitive SDK pane never built its engine")
		}
		time.Sleep(20 * time.Millisecond)
	}

	d := newSDKK173(t, "router:\n  disable_runtimes: [claude]\n", "")
	st, ev = d.sendSDK(t, "s-sens-dis", task)
	if st == http.StatusAccepted && ev.Type != "error" {
		t.Fatalf("status %d frame %+v: a sensitive turn found a way around the disable", st, ev)
	}
	time.Sleep(200 * time.Millisecond)
	if models, spawns := d.seen(); len(models) != 0 || spawns != 0 {
		t.Errorf("an engine was built (%v) or spawned (%d) for a refused sensitive turn", models, spawns)
	}
}
