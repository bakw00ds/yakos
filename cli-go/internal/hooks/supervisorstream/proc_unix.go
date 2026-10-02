//go:build !windows

package supervisorstream

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcessGroup puts the child in its own process group so the deadline can
// kill its whole tree.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killTree sends TERM to the group, then KILL after grace.
func killTree(pid int, grace time.Duration) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	time.AfterFunc(grace, func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	})
}
