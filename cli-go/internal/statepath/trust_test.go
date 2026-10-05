package statepath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// state returns a private state directory holding file name with content.
func trustedState(t *testing.T, name, content string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestReadTrusted_ReadsAnOwnerOnlyFile(t *testing.T) {
	_, path := trustedState(t, "default-runtime", "codex\n")
	got, err := ReadTrusted(path, 256)
	if err != nil || string(got) != "codex\n" {
		t.Fatalf("ReadTrusted = %q, %v", got, err)
	}
}

// The mode a file gets from `yakos auth set-default` (0644 in a 0755 dir) is
// fine: nobody else can write it.
func TestReadTrusted_AcceptsReadableByOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	dir, path := trustedState(t, "default-runtime", "agy\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTrusted(path, 256); err != nil {
		t.Fatalf("a 0644 file in a 0755 directory must be trusted: %v", err)
	}
}

func TestReadTrusted_MissingFileIsNotUntrusted(t *testing.T) {
	dir, _ := trustedState(t, "other", "x")
	_, err := ReadTrusted(filepath.Join(dir, "default-runtime"), 256)
	if !errors.Is(err, fs.ErrNotExist) || IsUntrusted(err) {
		t.Fatalf("err = %v, want not-exist and not untrusted", err)
	}
	_, err = ReadTrusted(filepath.Join(dir, "no-such-dir", "default-runtime"), 256)
	if !errors.Is(err, fs.ErrNotExist) || IsUntrusted(err) {
		t.Fatalf("missing directory: err = %v, want not-exist and not untrusted", err)
	}
}

// The planted-file cases of sec-324 F2: a symlink to a file elsewhere, a file
// anyone can write, and a directory anyone can write or that is itself a link.
func TestReadTrusted_RefusesPlantedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks and mode bits")
	}
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		want  string
	}{
		{"symlinked file", func(t *testing.T) string {
			dir, _ := trustedState(t, "unused", "x")
			elsewhere := filepath.Join(t.TempDir(), "planted")
			if err := os.WriteFile(elsewhere, []byte("codex\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(dir, "default-runtime")
			if err := os.Symlink(elsewhere, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "symlink"},
		{"world-writable file", func(t *testing.T) string {
			_, p := trustedState(t, "default-runtime", "agy\n")
			if err := os.Chmod(p, 0o666); err != nil {
				t.Fatal(err)
			}
			return p
		}, "writable"},
		{"group-writable file", func(t *testing.T) string {
			_, p := trustedState(t, "default-runtime", "agy\n")
			if err := os.Chmod(p, 0o660); err != nil {
				t.Fatal(err)
			}
			return p
		}, "writable"},
		{"world-writable directory", func(t *testing.T) string {
			dir, p := trustedState(t, "default-runtime", "agy\n")
			if err := os.Chmod(dir, 0o777); err != nil {
				t.Fatal(err)
			}
			return p
		}, "writable"},
		{"symlinked directory", func(t *testing.T) string {
			real, _ := trustedState(t, "default-runtime", "codex\n")
			link := filepath.Join(t.TempDir(), "state-link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(link, "default-runtime")
		}, "symlink"},
		{"directory in place of the file", func(t *testing.T) string {
			dir, _ := trustedState(t, "unused", "x")
			p := filepath.Join(dir, "default-runtime")
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
			return p
		}, "not a regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup(t)
			data, err := ReadTrusted(path, 256)
			if err == nil {
				t.Fatalf("read %q from an untrusted file", data)
			}
			if !IsUntrusted(err) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want an untrusted error mentioning %q", err, tc.want)
			}
		})
	}
}

func TestReadTrusted_CapsTheRead(t *testing.T) {
	_, path := trustedState(t, "f", strings.Repeat("a", 1000))
	got, err := ReadTrusted(path, 16)
	if err != nil || len(got) != 16 {
		t.Fatalf("read %d bytes, err %v, want 16", len(got), err)
	}
}
