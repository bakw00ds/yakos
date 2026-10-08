//go:build !windows

package workflow_test

import (
	"os"
	"syscall"
)

func mkfifo(p string) error { return syscall.Mkfifo(p, 0o644) }

// releaseFifo opens both ends without blocking so a goroutine stuck in open
// can finish.
func releaseFifo(p string) {
	if f, err := os.OpenFile(p, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
		_ = f.Close()
	}
}
