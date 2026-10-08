//go:build unix

package budget

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO with no writer blocks a plain open for good. The read must refuse it at
// once. The FIFO sits in a per-test scratch directory, and the read runs under a
// deadline so a regression fails the test instead of hanging it.
func TestReadProjectConfigFIFORefused(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, ".yakos.yml"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var c projectConfig
	within(t, 5*time.Second, func() { c = readProjectConfig(dir) })
	if !strings.Contains(c.warn, "not a regular file") {
		t.Fatalf("warn = %q", c.warn)
	}
}

// The FIFO takes the place of the file after the Lstat: the open must not block
// and the descriptor check must refuse it.
func TestReadProjectFileFIFOSwappedAfterLstat(t *testing.T) {
	dir := writeProject(t, "agent_budgets:\n  backend: 5\n")
	afterProjectLstat = func(path string) {
		_ = syscall.Unlink(path)
		_ = syscall.Mkfifo(path, 0o600)
	}
	defer func() { afterProjectLstat = func(string) {} }()
	var err error
	within(t, 5*time.Second, func() { _, err = readProjectFile(dir) })
	if err == nil {
		t.Fatal("FIFO swapped in after the Lstat was read")
	}
}
