//go:build unix

package projfile

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO with no writer blocks a plain open for good. The read must refuse it at
// once. The FIFO sits in a per-test scratch directory, and the read runs under a
// deadline so a regression fails the test instead of hanging it.
func TestReadFIFORefused(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, Name), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	var err error
	within(t, 5*time.Second, func() { _, err = Read(dir) })
	refusedWith(t, err, "not a regular file")
}

// The FIFO takes the place of the file after the Lstat: the open must not block
// and the descriptor check must refuse it.
func TestReadFIFOSwappedAfterLstat(t *testing.T) {
	dir := writeProject(t, "a: 1\n")
	setHook(t, &afterLstat, func(path string) {
		_ = syscall.Unlink(path)
		_ = syscall.Mkfifo(path, 0o600)
	})
	var err error
	within(t, 5*time.Second, func() { _, err = Read(dir) })
	if err == nil {
		t.Fatal("FIFO swapped in after the Lstat was read")
	}
}
