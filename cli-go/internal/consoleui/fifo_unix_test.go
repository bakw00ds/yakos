//go:build !windows

package consoleui_test

import (
	"os"
	"syscall"
)

func mkfifo(p string) error { return syscall.Mkfifo(p, 0o644) }

// releaseFifo unblocks a goroutine stuck opening a FIFO.
func releaseFifo(p string) {
	if f, err := os.OpenFile(p, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
		_ = f.Close()
	}
}
