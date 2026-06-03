//go:build windows

package peerclaim

import (
	"os/exec"
	"strconv"
	"strings"
)

// isProcessRunning returns true if the process with the given PID is running.
// On Windows, uses tasklist /FI as a best-effort check.
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
