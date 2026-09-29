//go:build !windows

package jsonrpc

import (
	"os"
	"path/filepath"
	"testing"
)

// S-2 N6 (s2-daemon-security-review-r2-2026-09-21.md): on Linux without
// XDG_RUNTIME_DIR the socket lives in /tmp/yakos-<uid>. Listen's MkdirAll(0700)
// is a no-op on an existing directory, so a local attacker who pre-created
// that directory 0777 owned it and could unlink the daemon's socket and bind
// their own at the same path, receiving everything `yakos start` pushes.

func shortTempDir(t *testing.T) string {
	t.Helper()
	// Unix socket paths are limited to ~104 bytes; t.TempDir can exceed that.
	d, err := os.MkdirTemp("/tmp", "yk")
	if err != nil {
		t.Skipf("no /tmp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

func TestListen_TightensPreexistingPermissiveSocketDir(t *testing.T) {
	base := shortTempDir(t)
	dir := filepath.Join(base, "yakos-1000")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(filepath.Join(dir, "a.sock"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("socket dir mode=%o; want 700", fi.Mode().Perm())
	}
}

func TestListen_RefusesSymlinkedSocketDir(t *testing.T) {
	base := shortTempDir(t)
	target := filepath.Join(base, "attacker-dir")
	if err := os.Mkdir(target, 0o777); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "yakos-1000")
	if err := os.Symlink(target, dir); err != nil {
		t.Skipf("symlink: %v", err)
	}
	ln, err := Listen(filepath.Join(dir, "a.sock"))
	if err == nil {
		ln.Close()
		t.Fatal("Listen bound a socket inside a symlinked yakos socket dir")
	}
	if _, statErr := os.Stat(filepath.Join(target, "a.sock")); statErr == nil {
		t.Error("socket was created inside the attacker-controlled symlink target")
	}
}

// A stale socket in a directory that fails the check must not be removed:
// the check runs before any unlink.
func TestListen_DirCheckPrecedesStaleSocketRemoval(t *testing.T) {
	base := shortTempDir(t)
	target := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(target, "a.sock")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "yakos-1000")
	if err := os.Symlink(target, dir); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if ln, err := Listen(filepath.Join(dir, "a.sock")); err == nil {
		ln.Close()
		t.Fatal("Listen succeeded through a symlinked dir")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("file behind the symlinked dir was removed: %v", err)
	}
}

// Caller-chosen socket directories (tests, --socket overrides) are not the
// yakOS-managed directory and are left exactly as before.
func TestListen_CustomDirNotChmodded(t *testing.T) {
	base := shortTempDir(t)
	dir := filepath.Join(base, "custom")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(filepath.Join(dir, "a.sock"))
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
		t.Errorf("custom dir mode=%o; want it untouched (755)", fi.Mode().Perm())
	}
}

func TestIsYakosManagedSocketDir(t *testing.T) {
	for dir, want := range map[string]bool{
		"/tmp/yakos-1000":        true,
		"/run/user/1000/yakos":   true,
		"/var/folders/x/T/yakos": true,
		"/tmp":                   false,
		"/tmp/custom":            false,
		"/tmp/yakosx":            false,
		"/tmp/notyakos-1000":     false,
	} {
		if got := isYakosManagedSocketDir(dir); got != want {
			t.Errorf("isYakosManagedSocketDir(%q)=%v; want %v", dir, got, want)
		}
	}
}
