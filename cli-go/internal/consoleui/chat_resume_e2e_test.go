package consoleui_test

// chat_resume_e2e_test.go: K-147. A codex or agy pane with the Interactive
// toggle on keeps its context across turns through a ResumeEngine: one
// RunStream per turn, the harness's own session id carried from turn to turn
// (and across a manager restart) through the conversation's meta store, one
// Account event pair per turn.
//
// The harnesses are shell stubs on PATH that log their argv and pid. The
// hanging mode is `exec sleep` so a kill reaches the sleep itself.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

const (
	fakeCodexScript = `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '@FAKE_ARGV_LOG@'
echo $$ >> '@FAKE_PIDS@'
if [ -f '@FAKE_HANG@' ]; then exec sleep 120; fi
printf '%s\n' '{"type":"thread.started","thread_id":"thread-codex-1"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"reply"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}'
`
	fakeAgyScript = `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '@FAKE_ARGV_LOG@'
echo $$ >> '@FAKE_PIDS@'
if [ -f '@FAKE_HANG@' ]; then exec sleep 120; fi
printf '%s\n' '{"event":"init","conversation_id":"conv-agy-1","init":{"model":"agy-model"}}'
printf '%s\n' '{"event":"result","result":{"conversation_id":"conv-agy-1","status":"SUCCESS","response":"reply","model":"agy-model"}}'
`
)

type resumeFixture struct {
	ledgerServer
	argvLog, pids, hang string
	convs               *[]string // conversations dispatched, for the fail-fast refusal check
}

func newResumeServer(t *testing.T, harness, script string) resumeFixture {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	s := newLedgerServer(t)
	dir := t.TempDir()
	f := resumeFixture{ledgerServer: s, argvLog: filepath.Join(dir, "argv.log"), pids: filepath.Join(dir, "pids"), hang: filepath.Join(dir, "hang"), convs: new([]string)}
	bin := t.TempDir()
	script = strings.NewReplacer("@FAKE_ARGV_LOG@", f.argvLog, "@FAKE_PIDS@", f.pids, "@FAKE_HANG@", f.hang).Replace(script)
	if err := os.WriteFile(filepath.Join(bin, harness), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The runtime availability probe (auth.ProbeRuntime) needs a signed-in
	// harness: seed both so the result does not depend on the machine's own
	// sign-in state (an unseeded agy fails the probe on a clean Linux runner).
	t.Setenv("OPENAI_API_KEY", "sk-test-not-real")
	t.Setenv("ANTIGRAVITY_API_KEY", "k-test-not-real")
	t.Cleanup(func() {
		for _, pid := range f.pidList() {
			killPID(pid)
		}
	})
	return f
}

func (f resumeFixture) pidList() []int {
	raw, _ := os.ReadFile(f.pids)
	var out []int
	for _, fld := range strings.Fields(string(raw)) {
		if pid, err := strconv.Atoi(fld); err == nil && pid > 1 {
			out = append(out, pid)
		}
	}
	return out
}

func (f resumeFixture) calls(t *testing.T) [][]string {
	t.Helper()
	raw, _ := os.ReadFile(f.argvLog)
	var out [][]string
	for _, block := range strings.Split(string(raw), "--- call\n")[1:] {
		out = append(out, strings.Split(strings.TrimRight(block, "\n"), "\n"))
	}
	return out
}

// waitForEvents shadows the ledger's: it fails at once, with the refusal's
// text, when a dispatched conversation holds an error entry (the availability
// probe or the budget refused the turn) instead of waiting out 15 s.
func (f resumeFixture) waitForEvents(t *testing.T, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ev := f.events(t); len(ev) >= n {
			return ev
		}
		for _, c := range *f.convs {
			if errs := f.transcriptErrors(t, c); len(errs) > 0 {
				t.Fatalf("conversation %s was refused before it ran: %q", c, errs)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d dispatch-log events; have %v", n, f.events(t))
	return nil
}

func (f resumeFixture) dispatchTurn(t *testing.T, harness, conv, task string) {
	t.Helper()
	*f.convs = append(*f.convs, conv)
	resp := f.post(t, "/api/chat/dispatch", map[string]any{
		"agent": harness, "runtime": harness, "task": task, "sessionId": "sess-" + conv,
		"operatorId": "alice", "conversationId": conv, "interactive": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
}

func (f resumeFixture) sendTurn(t *testing.T, conv, text string) int {
	t.Helper()
	resp := f.post(t, "/api/chat/send", map[string]any{
		"conversationId": conv, "operatorId": "alice", "sessionId": "sess-" + conv, "text": text,
	})
	resp.Body.Close()
	return resp.StatusCode
}

func has(argv []string, want ...string) bool {
	for i := range argv {
		if i+len(want) <= len(argv) && strings.Join(argv[i:i+len(want)], "\x00") == strings.Join(want, "\x00") {
			return true
		}
	}
	return false
}

func (f resumeFixture) pairsAfter(t *testing.T, n int) {
	t.Helper()
	f.waitForEvents(t, 2*n)
	time.Sleep(250 * time.Millisecond) // nothing more may arrive
	var started, finished int
	for _, ev := range f.events(t) {
		switch ev["type"] {
		case "dispatch_started":
			started++
		case "dispatch_finished":
			finished++
		}
	}
	if started != n || finished != n {
		t.Fatalf("%d turns must leave %d started and %d finished events, got %d / %d", n, n, n, started, finished)
	}
}

func waitCalls(t *testing.T, f resumeFixture, n int) {
	t.Helper()
	waitUntil(t, "harness call", func() bool { return len(f.calls(t)) >= n })
}

func TestResumePane_CodexCarriesThreadAcrossTurnsAndRestart(t *testing.T) {
	f := newResumeServer(t, "codex", fakeCodexScript)
	const conv = "conv-codex-resume"

	f.dispatchTurn(t, "codex", conv, "first")
	f.waitForEvents(t, 2)
	if has(f.calls(t)[0], "resume") {
		t.Fatalf("turn 1 must not resume: %v", f.calls(t)[0])
	}
	store := consoleui.NewTranscripts(f.workDir)
	waitUntil(t, "thread id stored", func() bool { return store.NativeSession(conv, "codex", "alice") == "thread-codex-1" })

	if got := f.sendTurn(t, conv, "second"); got != http.StatusAccepted {
		t.Fatalf("send: %d", got)
	}
	f.waitForEvents(t, 4)
	if c := f.calls(t); len(c) != 2 || !has(c[1], "exec", "resume") || !has(c[1], "--", "thread-codex-1", "second") {
		t.Fatalf("turn 2 must run `exec resume ... -- thread-codex-1 second`: %v", c)
	}
	f.pairsAfter(t, 2)
	if f.mgr.AccountsOwnTurns(conv, "alice") != true {
		t.Error("a codex pane accounts its own turns")
	}

	// A manager restart: the engine is gone, the id is on disk.
	f.mgr.Close(conv)
	waitUntil(t, "engine gone", func() bool { return f.mgr.ActiveCount() == 0 })
	f.dispatchTurn(t, "codex", conv, "third")
	f.waitForEvents(t, 6)
	if c := f.calls(t); len(c) != 3 || !has(c[2], "exec", "resume") || !has(c[2], "--", "thread-codex-1", "third") {
		t.Fatalf("turn after a restart must resume the stored thread: %v", c)
	}
	f.pairsAfter(t, 3)
	t.Cleanup(func() { f.mgr.Close(conv) })
}

func TestResumePane_AgyCarriesConversation(t *testing.T) {
	f := newResumeServer(t, "agy", fakeAgyScript)
	const conv = "conv-agy-resume"

	f.dispatchTurn(t, "agy", conv, "first")
	f.waitForEvents(t, 2)
	t.Cleanup(func() { f.mgr.Close(conv) })
	if has(f.calls(t)[0], "--conversation") {
		t.Fatalf("turn 1 must not resume: %v", f.calls(t)[0])
	}
	store := consoleui.NewTranscripts(f.workDir)
	waitUntil(t, "conversation id stored", func() bool { return store.NativeSession(conv, "agy", "alice") == "conv-agy-1" })
	if got := f.sendTurn(t, conv, "second"); got != http.StatusAccepted {
		t.Fatalf("send: %d", got)
	}
	f.waitForEvents(t, 4)
	if c := f.calls(t); len(c) != 2 || !has(c[1], "--conversation", "conv-agy-1") {
		t.Fatalf("turn 2 must pass --conversation conv-agy-1: %v", c)
	}
	f.pairsAfter(t, 2)
}

func TestResumePane_SecondSendDuringTurnIs409AndCloseLeavesNoOrphan(t *testing.T) {
	f := newResumeServer(t, "codex", fakeCodexScript)
	const conv = "conv-codex-hang"
	if err := os.WriteFile(f.hang, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f.dispatchTurn(t, "codex", conv, "hang")
	waitCalls(t, f, 1)
	waitUntil(t, "pid", func() bool { return len(f.pidList()) == 1 })
	pid := f.pidList()[0]

	if got := f.sendTurn(t, conv, "too soon"); got != http.StatusConflict {
		t.Fatalf("a send during a turn: %d, want 409", got)
	}

	f.mgr.Close(conv)
	waitUntil(t, "harness process gone", func() bool { return !pidAlive(pid) })
	// The killed turn is not an error pane: nothing re-launched the harness.
	time.Sleep(200 * time.Millisecond)
	if n := len(f.calls(t)); n != 1 {
		t.Errorf("harness launched %d times, want 1", n)
	}
}

// A claude pane is untouched: it keeps the turn ledger, not a ResumeEngine.
func TestResumePane_ClaudePaneStillUsesTurnLedger(t *testing.T) {
	s := newLedgerServer(t)
	resp := s.post(t, "/api/chat/dispatch", map[string]any{
		"agent": "claude", "runtime": "claude", "task": "hello", "sessionId": "sess-cl",
		"operatorId": "alice", "conversationId": "conv-cl", "interactive": true,
	})
	resp.Body.Close()
	t.Cleanup(func() { s.mgr.Close("conv-cl") })
	s.waitForEvents(t, 2)
	if s.mgr.AccountsOwnTurns("conv-cl", "alice") {
		t.Error("a claude pane must not be a ResumeEngine")
	}
}

// A codex or agy pane at its agent's hard stop is refused exactly as a one-shot
// turn is, before the harness is launched, and writes nothing. The refusal comes
// from RunStream's budget pre-flight and surfaces as a turn error on the stream
// and in the transcript (a claude pane answers a send with 429; a resume pane
// has no pre-flight at the send, so the refusal is a turn error).
func TestResumePane_AtAHardStopIsRefusedBeforeTheHarnessRuns(t *testing.T) {
	for _, tc := range []struct{ harness, script string }{{"codex", fakeCodexScript}, {"agy", fakeAgyScript}} {
		t.Run(tc.harness, func(t *testing.T) {
			f := newResumeServer(t, tc.harness, tc.script)
			f.limit(t, tc.harness)
			seedTokens(t, tc.harness, 500) // over the limit of 100, without a turn having run
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			frames := f.sseFrames(t, ctx, "alice")
			time.Sleep(100 * time.Millisecond)

			conv := "conv-hs-" + tc.harness
			f.dispatchTurn(t, tc.harness, conv, "hello")
			t.Cleanup(func() { f.mgr.Close(conv) })
			got := nextError(t, frames, tc.harness+" dispatch")
			if !strings.HasPrefix(got, refusedPrefix+`"`+tc.harness+`"`) {
				t.Fatalf("the refusal is the budget's: %q", got)
			}
			if errs := f.transcriptErrors(t, conv); len(errs) != 1 || errs[0] != got {
				t.Errorf("the refusal is in the stored transcript: %v", errs)
			}
			if n := len(f.calls(t)); n != 0 {
				t.Errorf("the harness was launched %d time(s) for a refused turn", n)
			}
			f.noMoreEvents(t, 2, "only the seed is in the log")

			// Control: with the window reset the same request runs.
			f.resetWindow(t, tc.harness)
			*f.convs = nil // the refused conversation is expected to hold its error
			conv2 := conv + "-ok"
			f.dispatchTurn(t, tc.harness, conv2, "hello")
			t.Cleanup(func() { f.mgr.Close(conv2) })
			f.waitForEvents(t, 4)
			if n := len(f.calls(t)); n != 1 {
				t.Errorf("with the budget clear the harness runs once: %d", n)
			}
		})
	}
}

// A conversation's live engine fixes its kind: a dispatch for the other kind of
// runtime is a 409, not a turn delivered to the wrong engine.
// closeAndSettle closes the conversation's engine and waits until the work
// directory stops changing, so the dispatch goroutine's last transcript and
// ledger writes land before the test's TempDir is removed (K-130 cleanup race).
func (f resumeFixture) closeAndSettle(conv string) {
	f.mgr.Close(conv)
	settleDirs(f.workDir)
}

func TestResumePane_DispatchToAnotherEngineKindIs409(t *testing.T) {
	post := func(f resumeFixture, runtime, conv, sess string) int {
		resp := f.post(t, "/api/chat/dispatch", map[string]any{
			"agent": runtime, "runtime": runtime, "task": "x", "sessionId": sess,
			"operatorId": "alice", "conversationId": conv, "interactive": true,
		})
		resp.Body.Close()
		return resp.StatusCode
	}
	t.Run("claude into a codex pane", func(t *testing.T) {
		f := newResumeServer(t, "codex", fakeCodexScript)
		f.dispatchTurn(t, "codex", "conv-mix-a", "first")
		t.Cleanup(func() { f.closeAndSettle("conv-mix-a") })
		f.waitForEvents(t, 2)
		if got := post(f, "claude", "conv-mix-a", "sess-mix-a2"); got != http.StatusConflict {
			t.Fatalf("claude into a live codex ResumeEngine: %d, want 409", got)
		}
		f.pairsAfter(t, 1) // nothing ran
		if got := post(f, "codex", "conv-mix-a", "sess-mix-a3"); got != http.StatusAccepted {
			t.Fatalf("the pane's own runtime still dispatches: %d", got)
		}
	})
	t.Run("agy into a codex pane", func(t *testing.T) {
		f := newResumeServer(t, "codex", fakeCodexScript)
		f.dispatchTurn(t, "codex", "conv-mix-c", "first")
		t.Cleanup(func() { f.closeAndSettle("conv-mix-c") })
		f.waitForEvents(t, 2)
		if got := post(f, "agy", "conv-mix-c", "sess-mix-c2"); got != http.StatusConflict {
			t.Fatalf("agy into a live codex ResumeEngine: %d, want 409", got)
		}
		f.pairsAfter(t, 1)
	})
	t.Run("codex into a claude pane", func(t *testing.T) {
		f := newResumeServer(t, "codex", fakeCodexScript)
		if got := post(f, "claude", "conv-mix-b", "sess-mix-b1"); got != http.StatusAccepted {
			t.Fatalf("claude dispatch: %d", got)
		}
		t.Cleanup(func() { f.closeAndSettle("conv-mix-b") })
		f.waitForEvents(t, 2)
		if got := post(f, "codex", "conv-mix-b", "sess-mix-b2"); got != http.StatusConflict {
			t.Fatalf("codex into a live claude engine: %d, want 409", got)
		}
		f.pairsAfter(t, 1)
		if n := len(f.calls(t)); n != 0 {
			t.Errorf("the codex harness ran %d time(s)", n)
		}
	})
}
