package consoleui_test

// chat_resume_knowledge_e2e_test.go: K-149 on the interactive (ResumeEngine)
// path. A codex or agy pane gets the same stored knowledge pack, and the skill
// tail of a /<slug> turn, that a one-shot pane gets.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/knowledge"
)

const packMark = "PACK-RULE-MARK-7731"

func seedKnowledgeRoot(t *testing.T, f resumeFixture) {
	t.Helper()
	for path, body := range map[string]string{
		filepath.Join(f.yakosRoot, "lib/rules/zz-mark.md"):     packMark + "\n",
		filepath.Join(f.yakosRoot, "lib/skills/demo/SKILL.md"): "---\nname: demo\n---\nDEMO-STEPS-4410\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
}

func (f resumeFixture) dispatchAgent(t *testing.T, harness, agent, conv, task string) {
	t.Helper()
	*f.convs = append(*f.convs, conv)
	resp := f.post(t, "/api/chat/dispatch", map[string]any{
		"agent": agent, "runtime": harness, "task": task, "sessionId": "sess-" + conv,
		"operatorId": "alice", "conversationId": conv, "interactive": true,
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch: %d", resp.StatusCode)
	}
}

// argWith returns the argv elements that contain mark.
func argWith(argv []string, mark string) []string {
	var out []string
	for _, a := range argv {
		if strings.Contains(a, mark) {
			out = append(out, a)
		}
	}
	return out
}

func devInstructions(argv []string) string {
	for _, a := range argv {
		if strings.HasPrefix(a, "developer_instructions=") {
			return a
		}
	}
	return ""
}

func TestResumePane_CodexCarriesPackAndSkillTail(t *testing.T) {
	f := newResumeServer(t, "codex", fakeCodexScript)
	seedKnowledgeRoot(t, f)
	const conv = "conv-codex-pack"

	f.dispatchAgent(t, "codex", "alpha", conv, "first")
	f.waitForEvents(t, 2)
	t.Cleanup(func() { f.mgr.Close(conv) })
	store := consoleui.NewTranscripts(f.workDir)
	waitUntil(t, "thread id stored", func() bool { return store.NativeSession(conv, "codex", "alice") == "thread-codex-1" })

	c := f.calls(t)
	first := devInstructions(c[0])
	if !strings.Contains(first, packMark) || !strings.Contains(first, "Test agent alpha") {
		t.Fatalf("an interactive codex pane must carry the knowledge pack: %q", first)
	}
	// The stored file is the source: its sha is the pack's.
	sha, _, _, ok := store.KnowledgeInfo(conv, "alice")
	if !ok {
		t.Fatal("no pack stored for the conversation")
	}

	// Turn 2 is a skill: the tail goes on the user text, the pack stays as it was.
	if got := f.sendTurn(t, conv, "/demo go"); got != http.StatusAccepted {
		t.Fatalf("send: %d", got)
	}
	f.waitForEvents(t, 4)
	c = f.calls(t)
	if len(c) != 2 || devInstructions(c[1]) != first {
		t.Fatalf("turn 2 must re-send identical pack bytes")
	}
	// The argv log is one element per line; the user text follows the "--".
	last := ""
	for i, a := range c[1] {
		if a == "--" {
			last = strings.Join(c[1][i+1:], "\n")
		}
	}
	if !strings.Contains(last, "/demo go") || !strings.Contains(last, "DEMO-STEPS-4410") || strings.Contains(devInstructions(c[1]), "DEMO-STEPS") {
		t.Fatalf("skill tail must be on the user turn only: %q", last)
	}

	// A restart, and the rules change on disk: the stored bytes still win.
	f.mgr.Close(conv)
	waitUntil(t, "engine gone", func() bool { return f.mgr.ActiveCount() == 0 })
	if err := os.WriteFile(filepath.Join(f.yakosRoot, "lib/rules/zz-mark.md"), []byte("CHANGED-AFTER-START\n"), 0o644); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	f.dispatchAgent(t, "codex", "alpha", conv, "third")
	f.waitForEvents(t, 6)
	c = f.calls(t)
	if len(c) != 3 || devInstructions(c[2]) != first || strings.Contains(devInstructions(c[2]), "CHANGED-AFTER-START") {
		t.Fatalf("a restart must re-send the identical pack bytes")
	}
	if got, _, _, _ := store.KnowledgeInfo(conv, "alice"); got != sha || knowledge.SHA("") == sha {
		t.Fatalf("stored pack changed across the restart")
	}
}

// S1: the agy native-resume skip. A resumed agy turn (the stored agy
// conversation id is passed as --conversation) sends no pack because that
// conversation already holds it; the first turn does. Reachable only through
// the ResumeEngine: the one-shot console path never passes native sessions.
func TestResumePane_AgyPackOnFirstTurnOnlyAndSkillTailStays(t *testing.T) {
	f := newResumeServer(t, "agy", fakeAgyScript)
	seedKnowledgeRoot(t, f)
	const conv = "conv-agy-pack"

	f.dispatchAgent(t, "agy", "alpha", conv, "first")
	f.waitForEvents(t, 2)
	t.Cleanup(func() { f.mgr.Close(conv) })
	store := consoleui.NewTranscripts(f.workDir)
	waitUntil(t, "conversation id stored", func() bool { return store.NativeSession(conv, "agy", "alice") == "conv-agy-1" })
	if c := f.calls(t); len(argWith(c[0], packMark)) != 1 {
		t.Fatalf("agy turn 1 must carry the pack: %v", c[0])
	}

	if got := f.sendTurn(t, conv, "/demo go"); got != http.StatusAccepted {
		t.Fatalf("send: %d", got)
	}
	f.waitForEvents(t, 4)
	c := f.calls(t)
	if len(c) != 2 || !has(c[1], "--conversation", "conv-agy-1") {
		t.Fatalf("turn 2 must resume: %v", c)
	}
	if len(argWith(c[1], packMark)) != 0 {
		t.Fatalf("a resumed agy turn must not re-send the pack: %v", c[1])
	}
	if len(argWith(c[1], "DEMO-STEPS-4410")) != 1 {
		t.Fatalf("the skill tail still rides the resumed turn: %v", c[1])
	}
}
