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

// op is the operator the meta tests run as.
const op = "alice"

func TestNativeSession_RoundTripAndIsolation(t *testing.T) {
	tr := consoleui.NewTranscripts(t.TempDir())

	if got := tr.NativeSession("conv-1", "claude", op); got != "" {
		t.Fatalf("a conversation with no meta has no session, got %q", got)
	}
	if err := tr.SetNativeSession("conv-1", "claude", "sess-A", op); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude", op); got != "sess-A" {
		t.Errorf("NativeSession = %q, want sess-A", got)
	}
	// Per runtime and per conversation.
	if got := tr.NativeSession("conv-1", "codex", op); got != "" {
		t.Errorf("codex must not see claude's session, got %q", got)
	}
	if got := tr.NativeSession("conv-2", "claude", op); got != "" {
		t.Errorf("another conversation must not see it, got %q", got)
	}
	// Overwrite with the newest id (each turn's result frame carries one).
	if err := tr.SetNativeSession("conv-1", "claude", "sess-B", op); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude", op); got != "sess-B" {
		t.Errorf("NativeSession = %q, want sess-B", got)
	}
	// A second runtime lives beside it.
	if err := tr.SetNativeSession("conv-1", "codex", "thread-9", op); err != nil {
		t.Fatal(err)
	}
	if tr.NativeSession("conv-1", "claude", op) != "sess-B" || tr.NativeSession("conv-1", "codex", op) != "thread-9" {
		t.Error("runtimes must be stored side by side")
	}
}

func TestNativeSession_Clear(t *testing.T) {
	tr := consoleui.NewTranscripts(t.TempDir())
	if err := tr.ClearNativeSession("conv-1", "claude", op); err != nil {
		t.Errorf("clearing what is not stored is not an error: %v", err)
	}
	_ = tr.SetNativeSession("conv-1", "claude", "sess-A", op)
	_ = tr.SetNativeSession("conv-1", "codex", "thread-9", op)
	if err := tr.ClearNativeSession("conv-1", "claude", op); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude", op); got != "" {
		t.Errorf("after clear, NativeSession = %q", got)
	}
	if got := tr.NativeSession("conv-1", "codex", op); got != "thread-9" {
		t.Errorf("clearing claude must keep codex, got %q", got)
	}
}

// What is stored goes on a command line, so nothing unsafe is ever stored.
func TestNativeSession_RejectsUnsafeInput(t *testing.T) {
	dir := t.TempDir()
	tr := consoleui.NewTranscripts(dir)
	for _, bad := range []string{"", "-resume", "two words", "a;b", "../x", "a\nb", strings.Repeat("a", 129)} {
		if err := tr.SetNativeSession("conv-1", "claude", bad, op); err == nil {
			t.Errorf("session id %q must be rejected", bad)
		}
	}
	if err := tr.SetNativeSession("conv-1", "gemini", "sess-A", op); err == nil {
		t.Error("an unknown runtime must be rejected")
	}
	for _, bad := range []string{"", "../escape", "a/b", "-x"} {
		if err := tr.SetNativeSession(bad, "claude", "sess-A", op); err == nil {
			t.Errorf("conversation id %q must be rejected", bad)
		}
		if got := tr.NativeSession(bad, "claude", op); got != "" {
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
	if err := tr.SetNativeSession("conv-1", "claude", "sess-A", op); err != nil {
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
		"damaged owner":   `{"owner_operator_id":"` + strings.Repeat("a", 600) + `","native_sessions":{"claude":"sess-A"}}`,
	} {
		if err := os.WriteFile(meta, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := tr.NativeSession("conv-1", "claude", op); got != "" {
			t.Errorf("%s: NativeSession = %q, want empty", name, got)
		}
	}

	if err := os.WriteFile(meta, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tr.SetNativeSession("conv-1", "claude", "sess-A", op); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude", op); got != "sess-A" {
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
			_ = tr.SetNativeSession("conv-shared", "claude", "sess-"+string(rune('a'+i)), op)
			_ = tr.SetNativeSession("conv-shared", "codex", "thread-"+string(rune('a'+i)), op)
		}(i)
	}
	wg.Wait()
	c, x := tr.NativeSession("conv-shared", "claude", op), tr.NativeSession("conv-shared", "codex", op)
	if !strings.HasPrefix(c, "sess-") || !strings.HasPrefix(x, "thread-") {
		t.Errorf("after a burst both runtimes must hold a valid id, got claude=%q codex=%q", c, x)
	}
}

// ---- the sessions belong to one operator (sec-324 F1) ----------------------------

// A native session carries the whole conversation, so only the operator whose
// turn produced it can read, replace or clear it.
func TestNativeSession_BelongsToItsOperator(t *testing.T) {
	dir := t.TempDir()
	tr := consoleui.NewTranscripts(dir)
	if err := tr.SetNativeSession("conv-1", "claude", "sess-ALICE", "alice"); err != nil {
		t.Fatal(err)
	}

	if got := tr.NativeSession("conv-1", "claude", "mallory"); got != "" {
		t.Errorf("mallory read alice's session %q", got)
	}
	if got := tr.NativeSession("conv-1", "claude", ""); got != "" {
		t.Errorf("an empty operator read alice's session %q", got)
	}
	if err := tr.SetNativeSession("conv-1", "claude", "sess-MALLORY", "mallory"); err == nil {
		t.Error("mallory replaced alice's session")
	}
	if err := tr.SetNativeSession("conv-1", "codex", "thread-MALLORY", "mallory"); err == nil {
		t.Error("mallory added a session to alice's conversation")
	}
	if err := tr.ClearNativeSession("conv-1", "claude", "mallory"); err == nil {
		t.Error("mallory cleared alice's session")
	}
	if n, err := tr.NoteResumeFailure("conv-1", "claude", "mallory"); err == nil || n != 0 {
		t.Errorf("mallory's failure was counted against alice's session: %d, %v", n, err)
	}
	if got := tr.NativeSession("conv-1", "claude", "alice"); got != "sess-ALICE" {
		t.Errorf("alice's session = %q after mallory's attempts, want it untouched", got)
	}

	// The owner is stored with the id, not derived from the transcript.
	raw, err := os.ReadFile(filepath.Join(dir, "chats", "conv-1.meta.json"))
	if err != nil || !strings.Contains(string(raw), `"owner_operator_id":"alice"`) {
		t.Errorf("owner not recorded in the meta file: %s %v", raw, err)
	}
}

// A meta file with sessions but no owner (written before owners were recorded)
// is not handed to anyone: the next turn starts fresh and claims it.
func TestNativeSession_AnUnownedFileIsNotHandedOut(t *testing.T) {
	dir := t.TempDir()
	tr := consoleui.NewTranscripts(dir)
	if err := os.MkdirAll(filepath.Join(dir, "chats"), 0o700); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "chats", "conv-1.meta.json")
	if err := os.WriteFile(meta, []byte(`{"native_sessions":{"claude":"sess-OLD"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"alice", "mallory", ""} {
		if got := tr.NativeSession("conv-1", "claude", who); got != "" {
			t.Errorf("operator %q was handed an unowned session %q", who, got)
		}
	}
	if err := tr.SetNativeSession("conv-1", "claude", "sess-NEW", "alice"); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude", "alice"); got != "sess-NEW" {
		t.Errorf("after claiming, alice reads %q", got)
	}
}

// An owner is not held to the identity-field alphabet: a certificate CN can
// carry '@' or a space, and its conversations must still resume.
func TestNativeSession_OwnerMayBeAnyName(t *testing.T) {
	tr := consoleui.NewTranscripts(t.TempDir())
	const cn = "Alice Example <alice@corp.example>"
	if err := tr.SetNativeSession("conv-1", "claude", "sess-A", cn); err != nil {
		t.Fatal(err)
	}
	if got := tr.NativeSession("conv-1", "claude", cn); got != "sess-A" {
		t.Errorf("NativeSession = %q, want sess-A for owner %q", got, cn)
	}
}

// The failure count is the backstop for a dead id whose message nobody
// recognises: it counts consecutive failures and a stored id resets it.
func TestNativeSession_ResumeFailuresCountAndReset(t *testing.T) {
	tr := consoleui.NewTranscripts(t.TempDir())
	if _, err := tr.NoteResumeFailure("conv-1", "claude", op); err == nil {
		t.Error("a failure with no stored session has no owner to count against")
	}
	_ = tr.SetNativeSession("conv-1", "claude", "sess-A", op)
	for want := 1; want <= 3; want++ {
		if n, err := tr.NoteResumeFailure("conv-1", "claude", op); err != nil || n != want {
			t.Fatalf("failure %d counted as %d, %v", want, n, err)
		}
	}
	// A turn that works stores its id, and the next failure is the first again.
	_ = tr.SetNativeSession("conv-1", "claude", "sess-B", op)
	if n, _ := tr.NoteResumeFailure("conv-1", "claude", op); n != 1 {
		t.Errorf("after a good turn the count is %d, want 1", n)
	}
	// Storing the same id again (a good resumed turn echoes it) also resets.
	_ = tr.SetNativeSession("conv-1", "claude", "sess-B", op)
	if n, _ := tr.NoteResumeFailure("conv-1", "claude", op); n != 1 {
		t.Errorf("after a good turn with an unchanged id the count is %d, want 1", n)
	}
	// Clearing forgets the count with the session.
	_ = tr.ClearNativeSession("conv-1", "claude", op)
	_ = tr.SetNativeSession("conv-1", "claude", "sess-C", op)
	if n, _ := tr.NoteResumeFailure("conv-1", "claude", op); n != 1 {
		t.Errorf("after clear and a new session the count is %d, want 1", n)
	}
	if _, err := tr.NoteResumeFailure("conv-1", "gemini", op); err == nil {
		t.Error("an unknown runtime must be rejected")
	}
}
