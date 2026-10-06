package modelreg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// skipIfNoPosixModes skips a test that depends on mode bits, ownership or
// symlinks, none of which mean the same on Windows.
func skipIfNoPosixModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits and symlinks")
	}
}

// privateStateDir returns a 0700 temp directory, the shape of ~/.yakos-state.
func privateStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeOverlay writes the overlay file with the given mode (chmod'ed explicitly,
// so the umask cannot change it).
func writeOverlay(t *testing.T, dir, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, OverlayFileName)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeProject writes <dir>/.yakos.yml and returns dir.
func writeProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// mustLoad builds a registry and fails the test on an error (a bad catalog).
func mustLoad(t *testing.T, o Options) *Registry {
	t.Helper()
	r, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

// entry returns the entry for id on harness or fails the test.
func entry(t *testing.T, r *Registry, harness, id string) Entry {
	t.Helper()
	e, ok := r.Lookup(harness, id)
	if !ok {
		t.Fatalf("no entry %s on %s", id, harness)
	}
	return e
}

// hasWarning reports whether any warning contains sub.
func hasWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if containsStr(w, sub) {
			return true
		}
	}
	return false
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
