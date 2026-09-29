//go:build !windows

package supervisorstream

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session so it outlives the hook process.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
