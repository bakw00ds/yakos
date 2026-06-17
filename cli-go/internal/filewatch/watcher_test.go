package filewatch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// waitForEvent reads from ch with a timeout and returns the received event.
// Fails the test if no event arrives within the timeout.
func waitForEvent(t *testing.T, ch <-chan ChangeEvent, timeout time.Duration) ChangeEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(timeout):
		t.Fatal("timeout waiting for file-change event")
		return ChangeEvent{}
	}
}

// mustNoEvent asserts that no event is delivered within the given window.
func mustNoEvent(t *testing.T, ch <-chan ChangeEvent, window time.Duration) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Errorf("unexpected event: path=%q action=%q", ev.Path, ev.Action)
	case <-time.After(window):
	}
}

// newWatcher creates a Watcher on root, starts it, and returns it along with
// a cleanup function that closes it.
func newWatcher(t *testing.T, root string) *Watcher {
	t.Helper()
	w, err := New(root)
	if err != nil {
		t.Fatalf("filewatch.New: %v", err)
	}
	w.Start()
	t.Cleanup(w.Close)
	return w
}

// TestCreateEmitsCreated verifies that creating a file emits an "created" event
// with a relative path and no file content.
func TestCreateEmitsCreated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := newWatcher(t, root)

	target := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(target, []byte("hi"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := waitForEvent(t, w.Events(), 3*time.Second)
	if ev.Path != "hello.txt" {
		t.Errorf("Path = %q; want %q", ev.Path, "hello.txt")
	}
	if ev.Action != ActionCreated {
		t.Errorf("Action = %q; want %q", ev.Action, ActionCreated)
	}
	if ev.TS.IsZero() {
		t.Error("TS is zero")
	}
}

// TestModifyEmitsModified verifies that writing to an existing file emits a
// "modified" event.
func TestModifyEmitsModified(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "data.txt")
	if err := os.WriteFile(target, []byte("initial"), 0644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	w := newWatcher(t, root)

	// Drain any spurious create event from setup.
	time.Sleep(200 * time.Millisecond)
	// Flush channel.
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	if err := os.WriteFile(target, []byte("updated"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := waitForEvent(t, w.Events(), 3*time.Second)
	if ev.Path != "data.txt" {
		t.Errorf("Path = %q; want %q", ev.Path, "data.txt")
	}
	if ev.Action != ActionModified {
		t.Errorf("Action = %q; want %q", ev.Action, ActionModified)
	}
}

// TestDeleteEmitsDeleted verifies that removing a file emits a "deleted" event.
func TestDeleteEmitsDeleted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "gone.txt")
	if err := os.WriteFile(target, []byte("bye"), 0644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	w := newWatcher(t, root)

	// Drain setup events.
	time.Sleep(200 * time.Millisecond)
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	if err := os.Remove(target); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	ev := waitForEvent(t, w.Events(), 3*time.Second)
	if ev.Path != "gone.txt" {
		t.Errorf("Path = %q; want %q", ev.Path, "gone.txt")
	}
	if ev.Action != ActionDeleted {
		t.Errorf("Action = %q; want %q", ev.Action, ActionDeleted)
	}
}

// TestPathsAreRelative asserts that event paths are relative to the root and
// use forward slashes.
func TestPathsAreRelative(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Create a subdirectory first so we can test a nested path.
	subDir := filepath.Join(root, "src")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	w := newWatcher(t, root)

	// Drain dir-create events.
	time.Sleep(200 * time.Millisecond)
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	target := filepath.Join(subDir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := waitForEvent(t, w.Events(), 3*time.Second)
	// Path must use forward slashes and be relative.
	if ev.Path != "src/main.go" {
		t.Errorf("Path = %q; want %q", ev.Path, "src/main.go")
	}
}

// TestDebounceCollapses verifies that rapid writes to the same file within
// the debounce window produce a single event.
func TestDebounceCollapses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "burst.txt")
	if err := os.WriteFile(target, []byte("v0"), 0644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	w := newWatcher(t, root)

	// Drain setup events.
	time.Sleep(200 * time.Millisecond)
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	// Write rapidly — all within the 100 ms debounce window.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(target, []byte("burst"), 0644); err != nil {
			t.Fatalf("WriteFile #%d: %v", i, err)
		}
	}

	// We expect exactly ONE event after the debounce window drains.
	ev := waitForEvent(t, w.Events(), 3*time.Second)
	if ev.Path != "burst.txt" {
		t.Errorf("Path = %q; want %q", ev.Path, "burst.txt")
	}

	// No further event should arrive within a short window.
	mustNoEvent(t, w.Events(), 300*time.Millisecond)
}

// TestSecretPathSkipped verifies that files matching the secret deny-set are
// silently suppressed — no event must arrive.
func TestSecretPathSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := newWatcher(t, root)

	secretFiles := []string{
		".env",
		".env.local",
		"credentials",
		"id_rsa",
		"id_ed25519",
		"server.pem",
		"private.key",
		"keystore.jks",
	}

	for _, name := range secretFiles {
		target := filepath.Join(root, name)
		if err := os.WriteFile(target, []byte("secret"), 0644); err != nil {
			t.Fatalf("WriteFile %q: %v", name, err)
		}
	}

	// No event should be received for any of the secret files.
	mustNoEvent(t, w.Events(), 500*time.Millisecond)
}

// TestGitDirSkipped verifies that .git directory changes produce no events.
func TestGitDirSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Create .git manually — the watcher should not add it to the watch set.
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0755); err != nil {
		t.Fatalf("Mkdir .git: %v", err)
	}

	w := newWatcher(t, root)

	// Write a file inside .git.
	target := filepath.Join(gitDir, "HEAD")
	if err := os.WriteFile(target, []byte("ref: refs/heads/main"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mustNoEvent(t, w.Events(), 500*time.Millisecond)
}

// TestNodeModulesSkipped verifies that node_modules directory changes produce
// no events.
func TestNodeModulesSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	nmDir := filepath.Join(root, "node_modules")
	if err := os.Mkdir(nmDir, 0755); err != nil {
		t.Fatalf("Mkdir node_modules: %v", err)
	}

	w := newWatcher(t, root)

	target := filepath.Join(nmDir, "react.js")
	if err := os.WriteFile(target, []byte("module.exports = {}"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mustNoEvent(t, w.Events(), 500*time.Millisecond)
}

// TestNewSubdirGetsWatched verifies that a directory created after the watcher
// starts is itself watched — files inside it emit events.
func TestNewSubdirGetsWatched(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := newWatcher(t, root)

	// Drain startup events.
	time.Sleep(200 * time.Millisecond)
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	// Create a new subdirectory.
	newDir := filepath.Join(root, "newpkg")
	if err := os.Mkdir(newDir, 0755); err != nil {
		t.Fatalf("Mkdir newpkg: %v", err)
	}

	// Give the watcher time to add the new directory.
	time.Sleep(300 * time.Millisecond)
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	// Create a file inside the new subdir.
	target := filepath.Join(newDir, "lib.go")
	if err := os.WriteFile(target, []byte("package newpkg"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := waitForEvent(t, w.Events(), 3*time.Second)
	if ev.Path != "newpkg/lib.go" {
		t.Errorf("Path = %q; want %q", ev.Path, "newpkg/lib.go")
	}
}

// TestCloseIsClean verifies that Close() does not block or panic, and that no
// events are delivered after Close returns.
func TestCloseIsClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w.Start()

	// Close immediately — must not block.
	done := make(chan struct{})
	go func() {
		w.Close()
		w.Close() // idempotent
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close() timed out")
	}

	// No events should arrive after Close.
	mustNoEvent(t, w.Events(), 200*time.Millisecond)
}

// TestIsSecretPath verifies the secret deny-set function covers all required
// cases, mirroring the consoleui/files_handler.go isSecretFile test surface.
func TestIsSecretPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		secret bool
	}{
		{".env", true},
		{".env.local", true},
		{".envrc", true},
		{"credentials", true},
		{"aws_credentials", true},
		{"id_rsa", true},
		{"id_dsa", true},
		{"id_ecdsa", true},
		{"id_ed25519", true},
		{".npmrc", true},
		{".netrc", true},
		{".pgpass", true},
		{".htpasswd", true},
		{"server.pem", true},
		{"private.key", true},
		{"cert.p12", true},
		{"cert.pfx", true},
		{"server.crt", true},
		{"server.cer", true},
		{"server.der", true},
		{"keystore.jks", true},
		{"archive.gpg", true},
		{"key.asc", true},
		// Safe files.
		{"main.go", false},
		{"README.md", false},
		{"config.yaml", false},
		{"Makefile", false},
		{"package.json", false},
		{".gitignore", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSecretPath(tc.name)
			if got != tc.secret {
				t.Errorf("isSecretPath(%q) = %v; want %v", tc.name, got, tc.secret)
			}
		})
	}
}

// TestPayloadNoContentField is the structural invariant test.
// It asserts that ChangeEvent has no Content, Data, Body, or Bytes field —
// enforcing the "paths only" invariant at the type level.
func TestPayloadNoContentField(t *testing.T) {
	t.Parallel()
	forbidden := []string{"Content", "Data", "Body", "Bytes", "Text", "Raw"}
	typ := reflect.TypeOf(ChangeEvent{})
	for _, name := range forbidden {
		if _, ok := typ.FieldByName(name); ok {
			t.Errorf("ChangeEvent has forbidden field %q — paths-only invariant violated", name)
		}
	}
}

// TestDeleteFilesDoesNotChangeWatchedDirCount verifies that creating and then
// deleting many regular files does not alter the watched-directory count.
//
// Regression test for the watchN drift bug: the previous implementation
// decremented watchN whenever a removed path stat-failed (which includes
// deleted regular files), causing watchN to drift below the true dir count
// and silently letting the 8192 cap be re-exceeded.
//
// The new implementation maintains an explicit watchedDirs set and only
// decrements when a path that IS in that set is removed.
func TestDeleteFilesDoesNotChangeWatchedDirCount(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := newWatcher(t, root)

	// Record the initial directory count (should be 1: the root itself).
	initialCount := w.watchedDirCount()
	if initialCount == 0 {
		t.Fatal("expected watchedDirCount > 0 after New(root)")
	}

	// Drain startup events.
	time.Sleep(200 * time.Millisecond)
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	const nFiles = 20
	// Create then immediately delete many regular files.
	for i := 0; i < nFiles; i++ {
		name := filepath.Join(root, "tmpfile_"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// Give fsnotify time to process the creates.
	time.Sleep(200 * time.Millisecond)
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	for i := 0; i < nFiles; i++ {
		name := filepath.Join(root, "tmpfile_"+string(rune('a'+i))+".txt")
		_ = os.Remove(name) // ignore error if already gone
	}
	// Wait for debounce + fsnotify processing.
	time.Sleep(400 * time.Millisecond)
	// Drain delete events.
	for len(w.Events()) > 0 {
		<-w.Events()
	}

	afterCount := w.watchedDirCount()
	if afterCount != initialCount {
		t.Errorf("watchedDirCount changed: was %d before file creates/deletes, got %d after; "+
			"file deletes must not alter the watched-directory count", initialCount, afterCount)
	}
}

// TestYakosSkipped verifies that the .yakos directory is not watched.
func TestYakosSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	yakosDir := filepath.Join(root, ".yakos")
	if err := os.Mkdir(yakosDir, 0755); err != nil {
		t.Fatalf("Mkdir .yakos: %v", err)
	}

	w := newWatcher(t, root)

	target := filepath.Join(yakosDir, "state.json")
	if err := os.WriteFile(target, []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mustNoEvent(t, w.Events(), 500*time.Millisecond)
}
