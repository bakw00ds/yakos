//go:build windows

package serve

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// isProcessRunning returns true if the process with the given PID is running.
// On Windows, we use the tasklist command as a best-effort check.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}

// selfProcess returns the current os.Process (for cleanup).
func selfProcess() *os.Process {
	p, _ := os.FindProcess(os.Getpid())
	return p
}
