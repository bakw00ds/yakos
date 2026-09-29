package statepath

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skipIfNoPosixModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
}

func TestSecureDir_TightensExistingPermissiveDir(t *testing.T) {
	skipIfNoPosixModes(t)
	d := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(d, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := SecureDir(d); err != nil {
		t.Fatalf("SecureDir: %v", err)
	}
	fi, _ := os.Stat(d)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%o; want 700 (MkdirAll alone leaves a pre-existing dir permissive)", fi.Mode().Perm())
	}
}

func TestSecureDir_CreatesPrivate(t *testing.T) {
	skipIfNoPosixModes(t)
	d := filepath.Join(t.TempDir(), "a", "b")
	if err := SecureDir(d); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(d)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%o; want 700", fi.Mode().Perm())
	}
}

func TestSecureDir_RefusesSymlinkAndNonDir(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := SecureDir(link); err == nil {
		t.Error("SecureDir followed a symlink; must refuse")
	}
	// The symlink target must not have been chmod'd either.
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(target)
		if fi.Mode().Perm() == 0o700 {
			t.Error("SecureDir tightened the symlink's target")
		}
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SecureDir(file); err == nil {
		t.Error("SecureDir accepted a regular file as a directory")
	}
}

func TestSecureFile_TightensPreCreatedFile(t *testing.T) {
	skipIfNoPosixModes(t)
	p := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(p, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := SecureFile(f); err != nil {
		t.Fatalf("SecureFile: %v", err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o; want 600 (O_CREATE's mode is ignored for an existing file)", fi.Mode().Perm())
	}
}

func TestSecureFile_RefusesNonRegular(t *testing.T) {
	f, err := os.Open(t.TempDir()) // a directory handle
	if err != nil {
		t.Skip("cannot open dir handle")
	}
	defer f.Close()
	if err := SecureFile(f); err == nil {
		t.Error("SecureFile accepted a non-regular file")
	}
}
