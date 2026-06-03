//go:build !windows

package serve

import (
	"os"
	"syscall"
)

// isProcessRunning returns true if the process with the given PID is running.
// Uses kill(pid, 0) via syscall.Kill — sends no signal; only checks existence.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	// FindProcess always succeeds on Unix (does not check existence).
	// We use Kill(pid, 0) to test liveness.
	err := syscall.Kill(pid, 0)
	// nil = process running; EPERM = process running (no permission to signal);
	// ESRCH = no such process.
	return err == nil || err == syscall.EPERM
}

// selfProcess returns the current os.Process (for cleanup).
func selfProcess() *os.Process {
	p, _ := os.FindProcess(os.Getpid())
	return p
}
