//go:build !windows

package modelreg

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// isolateProcess starts the command in a session of its own, which is also a
// process group of its own with the command as its leader, and makes the
// cancellation kill that whole group.
//
// Without it the context's cancel kills only the program: whatever the CLI had
// started (a helper, a language server, the sleep of a wedged script) is
// reparented to init and keeps running, and a daemon that refreshes a wedged
// harness every few minutes would pile them up. A session of its own also means
// no controlling terminal, so the program cannot prompt on /dev/tty although its
// standard input is the null device.
//
// A program that starts a descendant in a session of its own escapes the group; no
// portable call reaches it.
//
// The command no longer shares the terminal's foreground process group, so a
// Ctrl-C at the terminal no longer reaches it. The caller binds the probe to the
// process's interrupt (cmd/yakos does) so the context ends, this cancel runs and
// the group dies with the command.
func isolateProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone // the group is already gone
		}
		return err
	}
}
