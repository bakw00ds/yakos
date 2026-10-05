package runtime

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// unsetenv removes key for the duration of the test. t.Setenv registers the
// cleanup that restores the original value; the Unsetenv that follows makes
// the variable absent rather than empty.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

// resetSandboxNotes forgets which one-time sandbox notes were printed.
func resetSandboxNotes() {
	sandboxNotes.Range(func(k, _ any) bool {
		sandboxNotes.Delete(k)
		return true
	})
}

// useEmptyHome gives the test a clean HOME so the codex and agy adapters never
// read the developer's real ~/.yakos-state (router policy, codex-home profile)
// or ~/.codex, and clears the variables that relocate those. It also silences
// the one-time sandbox notes unless the test captures them. It returns the
// temporary home directory.
func useEmptyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	unsetenv(t, "YAKOS_DISPATCH_LOG")
	unsetenv(t, "CODEX_HOME")
	unsetenv(t, "OPENAI_API_KEY")
	resetSandboxNotes()
	prev := sandboxNoteWriter
	sandboxNoteWriter = io.Discard
	t.Cleanup(func() {
		sandboxNoteWriter = prev
		resetSandboxNotes()
	})
	return home
}

// captureSandboxNotes redirects the sandbox notes into the returned buffer for
// the rest of the test.
func captureSandboxNotes(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := sandboxNoteWriter
	sandboxNoteWriter = &buf
	resetSandboxNotes()
	t.Cleanup(func() { sandboxNoteWriter = prev })
	return &buf
}

// captureModelDrops redirects the "model not passed" notes into the returned
// buffer for the rest of the test.
func captureModelDrops(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := modelDropLog
	modelDropLog = &buf
	t.Cleanup(func() { modelDropLog = prev })
	return &buf
}
