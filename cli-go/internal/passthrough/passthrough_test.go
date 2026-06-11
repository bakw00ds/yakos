package passthrough_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
	want := filepath.FromSlash("/some/root/cli/yakos")
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
	// Bash scripts are not executable on Windows; skip on that platform.
	if runtime.GOOS == "windows" {
		t.Skip("bash passthrough not supported on Windows")
	}
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
	if !strings.Contains(err.Error(), "passthrough") && !strings.Contains(err.Error(), "yakos") {
		t.Errorf("error should mention yakos/passthrough; got %q", err.Error())
	}
}

// TestRun_NoBashYakos_ActionableError simulates a Go-only install:
// a root directory exists but contains no cli/yakos file.
// The error returned must be ErrNoBashYakos and must mention YAKOS_IMPL=go.
func TestRun_NoBashYakos_ActionableError(t *testing.T) {
	t.Parallel()

	// Build a temp dir that mimics a Go-only install: has a root dir but
	// no cli/yakos beneath it.
	goOnlyRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(goOnlyRoot, "cli"), 0755); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}
	// Intentionally do NOT create cli/yakos.

	_, err := passthrough.Run(goOnlyRoot, []string{"somethingunported"})
	if err == nil {
		t.Fatal("expected error for missing bash yakos; got nil")
	}

	// Must be the actionable ErrNoBashYakos sentinel.
	var noYakos *passthrough.ErrNoBashYakos
	if !errors.As(err, &noYakos) {
		t.Errorf("expected *passthrough.ErrNoBashYakos; got %T: %v", err, err)
	}

	// Error text must be actionable: mention YAKOS_IMPL=go so the user
	// knows how to proceed on a Go-only install.
	if !strings.Contains(err.Error(), "YAKOS_IMPL=go") {
		t.Errorf("actionable error should mention YAKOS_IMPL=go; got %q", err.Error())
	}
	// Must mention docs/go-shadow-mode.md.
	if !strings.Contains(err.Error(), "go-shadow-mode") {
		t.Errorf("actionable error should reference go-shadow-mode.md; got %q", err.Error())
	}
	// Must include the path so the user can diagnose.
	if !strings.Contains(err.Error(), goOnlyRoot) {
		t.Errorf("actionable error should include the root path; got %q", err.Error())
	}
}

// TestExec_NoBashYakos_ActionableError tests the Exec path with the same
// missing-bash scenario.
func TestExec_NoBashYakos_ActionableError(t *testing.T) {
	t.Parallel()

	goOnlyRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(goOnlyRoot, "cli"), 0755); err != nil {
		t.Fatalf("setup mkdir: %v", err)
	}

	err := passthrough.Exec(goOnlyRoot, []string{"somethingunported"})
	if err == nil {
		t.Fatal("expected error for missing bash yakos; got nil")
	}

	var noYakos *passthrough.ErrNoBashYakos
	if !errors.As(err, &noYakos) {
		t.Errorf("expected *passthrough.ErrNoBashYakos; got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "YAKOS_IMPL=go") {
		t.Errorf("actionable error should mention YAKOS_IMPL=go; got %q", err.Error())
	}
}
