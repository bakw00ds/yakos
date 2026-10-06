package auth

// default_runtime_umask_test.go — the writers of ~/.yakos-state/default-runtime
// leave a file the dispatcher will honour whatever the umask (sec-324 F2).
//
// ReadDefaultRuntime refuses a file that is group- or world-writable, or one in
// a directory that is. A shell redirection creates a file 0666 minus the umask,
// which is 0664 under the common umask 002 (user-private-group distributions),
// so the bash writer used to produce a file the Go dispatcher then ignored with
// a warning. These tests run the real writers and read the result back through
// the real reader.

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

// modeOf returns the permission bits of path.
func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// requireTrusted fails unless the reader accepts the file and returns want.
func requireTrusted(t *testing.T, stateDir, want string) {
	t.Helper()
	name, warn := ReadDefaultRuntime(stateDir)
	if name != want || warn != "" {
		t.Fatalf("ReadDefaultRuntime = %q, warning %q; want %q with no warning (file mode %v, dir mode %v)",
			name, warn, want, modeOf(t, filepath.Join(stateDir, "default-runtime")), modeOf(t, stateDir))
	}
}

// leftover recreates what an older bash writer left under umask 002: a state
// directory 0775 holding a default-runtime file 0664.
func leftover(t *testing.T, home string) string {
	t.Helper()
	stateDir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "default-runtime"), []byte("claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(stateDir, "default-runtime"), 0o664); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stateDir, 0o775); err != nil {
		t.Fatal(err)
	}
	return stateDir
}

// The Go writer creates the file 0600 and repairs one that is group-writable
// (os.WriteFile keeps an existing file's mode).
func TestWriteDefaultRuntime_FileIsOwnerOnly(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("mode bits")
	}
	t.Run("a new file", func(t *testing.T) {
		home := t.TempDir()
		if err := writeDefaultRuntime(Config{HomeDir: home}, "codex"); err != nil {
			t.Fatal(err)
		}
		state := filepath.Join(home, ".yakos-state")
		if m := modeOf(t, filepath.Join(state, "default-runtime")); m != 0o600 {
			t.Errorf("file mode = %v, want 0600", m)
		}
		requireTrusted(t, state, "codex")
	})
	t.Run("a group-writable file left by an older writer", func(t *testing.T) {
		home := t.TempDir()
		state := leftover(t, home)
		// The directory is the bash mkdir's 0775; the dispatcher tightens it on its
		// first dispatch (statepath.SecureDir), so only the file is under test here.
		if err := os.Chmod(state, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeDefaultRuntime(Config{HomeDir: home}, "agy"); err != nil {
			t.Fatal(err)
		}
		if m := modeOf(t, filepath.Join(state, "default-runtime")); m != 0o600 {
			t.Errorf("file mode = %v after the rewrite, want 0600 (the old 0664 must be repaired)", m)
		}
		requireTrusted(t, state, "agy")
	})
}

// The bash writer, run under umask 002, leaves a file and directory the Go
// reader trusts. This runs the real yk_rt_set_default from cli/lib.
func TestBashSetDefaultRuntime_IsUmaskProof(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("bash and umask")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	lib, err := filepath.Abs(filepath.Join("..", "..", "..", "cli", "lib"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "runtime-resolve.sh")); err != nil {
		t.Skipf("cli/lib is not beside this source tree: %v", err)
	}
	root := filepath.Dir(filepath.Dir(lib))

	run := func(t *testing.T, home, id string) {
		t.Helper()
		script := `set -eu; umask 002; . "$YAKOS_LIB/compat.sh"; . "$YAKOS_LIB/runtime-resolve.sh"; yk_rt_set_default "$1"`
		cmd := exec.Command(bash, "-c", script, "bash", id)
		cmd.Env = []string{"HOME=" + home, "YAKOS_LIB=" + lib, "YAKOS_ROOT=" + root, "PATH=" + os.Getenv("PATH")}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("yk_rt_set_default %s: %v\\n%s", id, err, out)
		}
	}

	t.Run("a fresh home: bash creates the directory and the file", func(t *testing.T) {
		home := t.TempDir()
		run(t, home, "codex")
		state := filepath.Join(home, ".yakos-state")
		if m := modeOf(t, filepath.Join(state, "default-runtime")); m&0o077 != 0 {
			t.Errorf("file mode = %v, want owner-only", m)
		}
		requireTrusted(t, state, "codex")
	})
	t.Run("what an older writer left: a 0775 directory and a 0664 file", func(t *testing.T) {
		home := t.TempDir()
		state := leftover(t, home)
		run(t, home, "agy")
		if m := modeOf(t, filepath.Join(state, "default-runtime")); m != 0o600 {
			t.Errorf("file mode = %v, want 0600: a redirect does not change an existing file's mode, so it must be chmod-ed", m)
		}
		requireTrusted(t, state, "agy")
	})
}
