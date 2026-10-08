//go:build !windows

package gwtoken

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadRefusesAFIFOWithoutHanging(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, testStore.File), 0o600); err != nil {
		t.Skipf("no mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := testStore.Read(dir)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Read trusted a FIFO")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read blocked on a FIFO")
	}
}
