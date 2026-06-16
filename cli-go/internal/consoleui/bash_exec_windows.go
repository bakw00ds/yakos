//go:build windows

package consoleui

import (
	"os/exec"
)

// configureProcAttr is a no-op on Windows.
//
// Windows does not expose POSIX process groups.  The best-effort kill in
// killProcessGroup (below) terminates the direct child only; backgrounded
// grandchildren may outlive the deadline.  This is a known limitation of
// the Windows build: operators running yakOS on Windows with commands that
// background sub-processes should expect those processes to survive past
// the 30-second exec timeout.
func configureProcAttr(_ *exec.Cmd) {}

// killProcessGroup kills the direct child process on Windows.
//
// There is no cross-process-group kill primitive in the standard library
// that is safe to use here without a Job Object; using a Job Object would
// require elevated privileges and a significant increase in platform-split
// complexity that is out of scope.  The direct-child kill covers the
// common case (the sh wrapper itself) and is sufficient for correctness
// on Windows.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
