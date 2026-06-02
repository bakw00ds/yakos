package passthrough_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/passthrough"
)

// repoRoot walks upward from the current working directory to find the repo root
// (the directory containing a "VERSION" regular file). This keeps tests hermetic
// regardless of which working directory `go test` is invoked from.
//
// Note: the check uses os.Stat + explicit IsDir guard because macOS
// case-insensitive filesystems match "VERSION" against a "version" directory,
// which would produce a false positive when walking through cli-go/internal/.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		fi, err := os.Stat(filepath.Join(dir, "VERSION"))
		if err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (no VERSION regular file in any parent dir)")
		}
		dir = parent
	}
}

func TestBashYakosPath(t *testing.T) {
	t.Parallel()

	root := "/some/root"
	got := passthrough.BashYakosPath(root)
	want := "/some/root/cli/yakos"
	if got != want {
		t.Errorf("BashYakosPath(%q) = %q; want %q", root, got, want)
	}
}

func TestBashYakosPath_ActualFile(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	p := passthrough.BashYakosPath(root)
	if _, err := os.Stat(p); err != nil {
		t.Errorf("bash yakos not found at %s: %v", p, err)
	}
}

func TestRun_VersionFlag(t *testing.T) {
	// Skip if bash yakos is unavailable.
	root := repoRoot(t)
	bashPath := passthrough.BashYakosPath(root)
	if _, err := os.Stat(bashPath); err != nil {
		t.Skipf("bash yakos not available at %s: %v", bashPath, err)
	}

	// Capture stdout by redirecting — Run inherits os.Stdout, so we
	// call it in a subprocess via os/exec directly for output capture.
	// For the passthrough package we test that Run returns exit code 0
	// for a known-good command.
	//
	// We use `--version` which is non-interactive and always succeeds.
	code, err := passthrough.Run(root, []string{"--version"})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0; got %d", code)
	}
}

func TestRun_NonexistentRoot(t *testing.T) {
	t.Parallel()

	_, err := passthrough.Run("/nonexistent/yakos/root/that/does/not/exist", []string{"--version"})
	if err == nil {
		t.Fatal("expected error for nonexistent root; got nil")
	}
	if !strings.Contains(err.Error(), "passthrough") {
		t.Errorf("error should mention passthrough package; got %q", err.Error())
	}
}
