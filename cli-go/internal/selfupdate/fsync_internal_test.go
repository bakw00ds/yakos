package selfupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAtomicReplace_FsyncsBeforeRename proves the temp file is synced (K-110)
// and that a sync failure aborts the replace, leaving the target untouched.
func TestAtomicReplace_FsyncsBeforeRename(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "yakos")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := syncFile
	defer func() { syncFile = orig }()

	called := 0
	syncFile = func(f *os.File) error {
		called++
		// The target must still hold the old bytes at sync time.
		if b, _ := os.ReadFile(exe); string(b) != "old" {
			t.Errorf("target replaced before fsync: %q", b)
		}
		return f.Sync()
	}
	if err := atomicReplace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("syncFile called %d times, want 1", called)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("target = %q, want new", b)
	}

	syncFile = func(*os.File) error { return errors.New("boom") }
	if err := atomicReplace(exe, []byte("newer")); err == nil {
		t.Fatal("expected error when fsync fails")
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("target changed despite fsync failure: %q", b)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp file leaked: %v", ents)
	}
}

// The parent directory is synced after the rename, so the swap itself is durable.
func TestAtomicReplace_SyncsParentDirAfterRename(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "yakos")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := syncDir
	defer func() { syncDir = orig }()
	var got string
	syncDir = func(d string) error {
		got = d
		if b, _ := os.ReadFile(exe); string(b) != "new" {
			t.Errorf("directory synced before the rename landed: %q", b)
		}
		return nil
	}
	if err := atomicReplace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && got != dir {
		t.Fatalf("syncDir called with %q, want %q", got, dir)
	}
}
