//go:build windows

package supervisorstream

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// setProcessGroup starts the child in a new process group.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// killTree ends the child and its descendants with taskkill /T /F (there is no
// POSIX process group on Windows); grace is unused because /F is immediate.
func killTree(pid int, _ time.Duration) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run() //nolint:gosec
}
