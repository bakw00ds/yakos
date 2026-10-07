package consoleui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/knowledge"
	"github.com/bakw00ds/yakos/internal/runtime"
)

func kwrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func kroot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	kwrite(t, filepath.Join(root, "lib/rules/alpha.md"), "alpha rule\n")
	kwrite(t, filepath.Join(root, "lib/agents/backend.md"), "---\nid: backend\n---\n\n## Purpose\n\nBackend persona.\n")
	kwrite(t, filepath.Join(root, "lib/skills/demo/SKILL.md"), "---\nname: demo\n---\nDEMO STEPS\n")
	return root
}

func newKH(t *testing.T, root string) *chatHandlers {
	t.Helper()
	return &chatHandlers{transcripts: NewTranscripts(t.TempDir()), yakosRoot: root, workspaceRoot: t.TempDir()}
}

func TestEnsureKnowledge_StoredOnceAndStableAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	compose := func(text string) func() knowledge.Pack {
		return func() knowledge.Pack {
			calls++
			return knowledge.Pack{Text: text, SHA: knowledge.SHA(text),
				Parts: []knowledge.Part{{Name: "alpha", Kind: knowledge.KindRule, Bytes: len(text), Included: true}}}
		}
	}
	tr := NewTranscripts(dir)
	first, err := tr.EnsureKnowledge("conv-1", "alice", compose("## rule: alpha\nv1\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	// A restart: a new store over the same directory. The rules have changed
	// since, and the stored bytes must still win.
	tr2 := NewTranscripts(dir)
	again, err := tr2.EnsureKnowledge("conv-1", "alice", compose("## rule: alpha\nv2 CHANGED\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Text != first.Text || again.SHA != first.SHA || calls != 1 {
		t.Fatalf("block changed across restart: calls=%d %q", calls, again.Text)
	}
	if sha, size, parts, ok := tr2.KnowledgeInfo("conv-1", "alice"); !ok || sha != first.SHA || size != len(first.Text) || len(parts) != 1 {
		t.Fatalf("info: %v %v %v %v", sha, size, parts, ok)
	}
	if _, _, _, ok := tr2.KnowledgeInfo("conv-1", "mallory"); ok {
		t.Fatal("another operator read the info")
	}
	if _, err := tr2.EnsureKnowledge("conv-1", "mallory", compose("x")); err == nil {
		t.Fatal("another operator got the block")
	}
}

func TestEnsureKnowledge_TamperedTextIsRecomposed(t *testing.T) {
	dir := t.TempDir()
	tr := NewTranscripts(dir)
	text := "## rule: a\nbody\n\n"
	mk := func() knowledge.Pack { return knowledge.Pack{Text: text, SHA: knowledge.SHA(text)} }
	if _, err := tr.EnsureKnowledge("conv-t", "alice", mk); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "chats", "conv-t.knowledge.txt"))
	if len(matches) != 1 {
		t.Fatalf("knowledge file not found under %s", dir)
	}
	if err := os.WriteFile(matches[0], []byte("EVIL"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := tr.EnsureKnowledge("conv-t", "alice", mk)
	if err != nil || got.Text != text {
		t.Fatalf("tampered text served: %q %v", got.Text, err)
	}
}

func TestValidKnowledgeMeta(t *testing.T) {
	good := conversationMeta{KnowledgeSHA: strings.Repeat("a", 64), KnowledgeBytes: 10,
		KnowledgeParts: []knowledge.Part{{Name: "x", Kind: knowledge.KindRule, Bytes: 1}}}
	if !validKnowledgeMeta(&good) {
		t.Fatal("good meta refused")
	}
	for name, mut := range map[string]func(m *conversationMeta){
		"short sha": func(m *conversationMeta) { m.KnowledgeSHA = "abc" },
		"big bytes": func(m *conversationMeta) { m.KnowledgeBytes = knowledge.MaxBytes + 1 },
		"bad name":  func(m *conversationMeta) { m.KnowledgeParts[0].Name = "../x" },
		"bad kind":  func(m *conversationMeta) { m.KnowledgeParts[0].Kind = "evil" },
	} {
		m := good
		m.KnowledgeParts = append([]knowledge.Part(nil), good.KnowledgeParts...)
		mut(&m)
		if validKnowledgeMeta(&m) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestNonClaudeTurn_ClaudeUntouched(t *testing.T) {
	ch := newKH(t, kroot(t))
	block, task := ch.nonClaudeTurn("claude", "c1", "alice", "backend", "/demo go")
	if block != "" || task != "/demo go" {
		t.Fatalf("claude changed: %q %q", block, task)
	}
	if _, _, _, ok := ch.transcripts.KnowledgeInfo("c1", "alice"); ok {
		t.Fatal("a pack was stored for claude")
	}
}

// The skill goes on the tail of the user turn; the knowledge block, which is
// what codex gets as developer instructions, is byte for byte what it is
// without a skill. Checked on the real codex argv.
func TestNonClaudeTurn_SkillIsTailOnly_CodexArgvGolden(t *testing.T) {
	ch := newKH(t, kroot(t))
	block1, plain := ch.nonClaudeTurn("codex", "c2", "alice", "backend", "just do it")
	block2, withSkill := ch.nonClaudeTurn("codex", "c2", "alice", "backend", "/demo go")
	if block1 == "" || block1 != block2 {
		t.Fatalf("block differs between turns")
	}
	if !strings.Contains(block1, "alpha rule") || !strings.Contains(block1, "Backend persona.") {
		t.Fatalf("block misses rule or agent: %q", block1)
	}
	if plain != "just do it" || !strings.HasPrefix(withSkill, "/demo go") || !strings.Contains(withSkill, "DEMO STEPS") {
		t.Fatalf("task: %q / %q", plain, withSkill)
	}
	if strings.Contains(block2, "DEMO STEPS") {
		t.Fatal("skill text reached the knowledge block")
	}

	argv := func(task string) []string {
		cmd := (&runtime.CodexAdapter{}).ChatExecCmd(t.Context(), runtime.ChatDispatchRequest{
			Project: t.TempDir(), UserText: task, AgentSystemPrompt: block1})
		return cmd.Args
	}
	a, b := argv(plain), argv(withSkill)
	if len(a) != len(b) {
		t.Fatalf("argv length differs: %d vs %d", len(a), len(b))
	}
	for i := 0; i < len(a)-1; i++ { // everything but the user text (last)
		if a[i] != b[i] {
			t.Fatalf("argv[%d] differs when a skill is used", i)
		}
	}
	if a[len(a)-1] == b[len(b)-1] {
		t.Fatal("user text did not change")
	}
	// The same on agy: the persona prefix is identical, the skill only follows.
	agy := func(task string) string {
		cmd := (&runtime.AgyAdapter{}).ChatExecCmd(t.Context(), runtime.ChatDispatchRequest{
			Project: t.TempDir(), UserText: task, AgentSystemPrompt: block1})
		return cmd.Args[len(cmd.Args)-1]
	}
	prefix := block1 + "\n\n---\n\n"
	if !strings.HasPrefix(agy(plain), prefix) || !strings.HasPrefix(agy(withSkill), prefix) {
		t.Fatal("agy prefix changed")
	}
}

func TestNonClaudeTurn_UnknownAndSecretSkillsNotAppended(t *testing.T) {
	root := kroot(t)
	kwrite(t, filepath.Join(root, "lib/skills/leaky/SKILL.md"), "key AKIA"+"ABCDEFGHIJKLMNOP\n")
	ch := newKH(t, root)
	for _, task := range []string{"/nosuch x", "/leaky x", "/../etc/passwd", "plain / text", "/ x"} {
		if _, got := ch.nonClaudeTurn("codex", "c3", "alice", "backend", task); got != task {
			t.Errorf("%q was changed to %q", task, got)
		}
	}
}

func TestSkillSlug(t *testing.T) {
	for in, want := range map[string]string{"/demo": "demo", "/demo go on": "demo", "/demo\nx": "demo"} {
		if got, ok := skillSlug(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "demo", "/", "/ demo", "/" + strings.Repeat("a", 65)} {
		if _, ok := skillSlug(in); ok {
			t.Errorf("%q accepted", in)
		}
	}
}
