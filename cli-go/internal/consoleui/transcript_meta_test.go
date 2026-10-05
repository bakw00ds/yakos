package consoleui_test

// transcript_meta_test.go — the per-conversation meta file that remembers a
// runtime's own session id (K-132), so a follow-up one-shot claude turn can
// `--resume` it.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
)

func TestNativeSession_RoundTripAndIsolation(t *testing.T) {
	tr := consoleui.NewTranscripts(t.TempDir())

	if got := tr.NativeSession("conv-1", "claude"); got != "" {
		t.Fatalf("a conversation with no meta has no session, got %q", got)
	}
	if err := tr.SetNativeSession("conv-1", "claude", "sess-A"); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude"); got != "sess-A" {
		t.Errorf("NativeSession = %q, want sess-A", got)
	}
	// Per runtime and per conversation.
	if got := tr.NativeSession("conv-1", "codex"); got != "" {
		t.Errorf("codex must not see claude's session, got %q", got)
	}
	if got := tr.NativeSession("conv-2", "claude"); got != "" {
		t.Errorf("another conversation must not see it, got %q", got)
	}
	// Overwrite with the newest id (each turn's result frame carries one).
	if err := tr.SetNativeSession("conv-1", "claude", "sess-B"); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude"); got != "sess-B" {
		t.Errorf("NativeSession = %q, want sess-B", got)
	}
	// A second runtime lives beside it.
	if err := tr.SetNativeSession("conv-1", "codex", "thread-9"); err != nil {
		t.Fatal(err)
	}
	if tr.NativeSession("conv-1", "claude") != "sess-B" || tr.NativeSession("conv-1", "codex") != "thread-9" {
		t.Error("runtimes must be stored side by side")
	}
}

func TestNativeSession_Clear(t *testing.T) {
	tr := consoleui.NewTranscripts(t.TempDir())
	if err := tr.ClearNativeSession("conv-1", "claude"); err != nil {
		t.Errorf("clearing what is not stored is not an error: %v", err)
	}
	_ = tr.SetNativeSession("conv-1", "claude", "sess-A")
	_ = tr.SetNativeSession("conv-1", "codex", "thread-9")
	if err := tr.ClearNativeSession("conv-1", "claude"); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude"); got != "" {
		t.Errorf("after clear, NativeSession = %q", got)
	}
	if got := tr.NativeSession("conv-1", "codex"); got != "thread-9" {
		t.Errorf("clearing claude must keep codex, got %q", got)
	}
}

// What is stored goes on a command line, so nothing unsafe is ever stored.
func TestNativeSession_RejectsUnsafeInput(t *testing.T) {
	dir := t.TempDir()
	tr := consoleui.NewTranscripts(dir)
	for _, bad := range []string{"", "-resume", "two words", "a;b", "../x", "a\nb", strings.Repeat("a", 129)} {
		if err := tr.SetNativeSession("conv-1", "claude", bad); err == nil {
			t.Errorf("session id %q must be rejected", bad)
		}
	}
	if err := tr.SetNativeSession("conv-1", "gemini", "sess-A"); err == nil {
		t.Error("an unknown runtime must be rejected")
	}
	for _, bad := range []string{"", "../escape", "a/b", "-x"} {
		if err := tr.SetNativeSession(bad, "claude", "sess-A"); err == nil {
			t.Errorf("conversation id %q must be rejected", bad)
		}
		if got := tr.NativeSession(bad, "claude"); got != "" {
			t.Errorf("conversation id %q must read as empty, got %q", bad, got)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "chats")); len(entries) != 0 {
		t.Errorf("nothing may be written for rejected input, found %d entries", len(entries))
	}
}

// The meta file is private like the transcript, and does not disturb it.
func TestNativeSession_FileModeAndTranscriptUntouched(t *testing.T) {
	dir := t.TempDir()
	tr := consoleui.NewTranscripts(dir)
	if err := tr.Append(consoleui.TranscriptEntry{SessionID: "s", ConversationID: "conv-1", OperatorID: "alice", Role: consoleui.RoleUser, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetNativeSession("conv-1", "claude", "sess-A"); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "chats", "conv-1.meta.json")
	fi, err := os.Stat(meta)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("meta file mode = %v, want 0600", fi.Mode().Perm())
	}
	entries, err := tr.Read("conv-1", "")
	if err != nil || len(entries) != 1 || entries[0].Text != "hi" {
		t.Errorf("transcript must read back unchanged: %v %v", entries, err)
	}
	// No temp files are left behind.
	left, _ := filepath.Glob(filepath.Join(dir, "chats", ".meta-*"))
	if len(left) != 0 {
		t.Errorf("leftover temp files: %v", left)
	}
}

// A damaged or hostile meta file reads as empty, and the next write repairs it.
func TestNativeSession_DamagedFileReadsEmptyAndHeals(t *testing.T) {
	dir := t.TempDir()
	tr := consoleui.NewTranscripts(dir)
	if err := os.MkdirAll(filepath.Join(dir, "chats"), 0o700); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "chats", "conv-1.meta.json")

	for name, content := range map[string]string{
		"not json":        "not json at all",
		"truncated":       `{"native_sessions":{"claude":"sess`,
		"wrong shape":     `{"native_sessions":["claude"]}`,
		"flag as id":      `{"native_sessions":{"claude":"--dangerously-skip-permissions"}}`,
		"id with spaces":  `{"native_sessions":{"claude":"a b"}}`,
		"unknown runtime": `{"native_sessions":{"gemini":"sess-A"}}`,
		"oversize":        `{"native_sessions":{"claude":"` + strings.Repeat("a", 70<<10) + `"}}`,
	} {
		if err := os.WriteFile(meta, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := tr.NativeSession("conv-1", "claude"); got != "" {
			t.Errorf("%s: NativeSession = %q, want empty", name, got)
		}
	}

	if err := os.WriteFile(meta, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetNativeSession("conv-1", "claude", "sess-A"); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude"); got != "sess-A" {
		t.Errorf("a write must replace a damaged file, got %q", got)
	}
}

// Concurrent turns of different conversations, and a burst on one, must not
// corrupt the file (the handler writes from per-dispatch goroutines).
func TestNativeSession_ConcurrentWrites(t *testing.T) {
	tr := consoleui.NewTranscripts(t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = tr.SetNativeSession("conv-shared", "claude", "sess-"+string(rune('a'+i)))
			_ = tr.SetNativeSession("conv-shared", "codex", "thread-"+string(rune('a'+i)))
		}(i)
	}
	wg.Wait()
	c, x := tr.NativeSession("conv-shared", "claude"), tr.NativeSession("conv-shared", "codex")
	if !strings.HasPrefix(c, "sess-") || !strings.HasPrefix(x, "thread-") {
		t.Errorf("after a burst both runtimes must hold a valid id, got claude=%q codex=%q", c, x)
	}
}
