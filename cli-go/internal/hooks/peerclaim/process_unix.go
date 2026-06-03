//go:build !windows

package peerclaim

import "syscall"

// isProcessRunning returns true if the process with the given PID is running.
// Uses kill(pid, 0) — sends no signal; only checks existence.
// nil = running; EPERM = running (no permission to signal); ESRCH = gone.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
