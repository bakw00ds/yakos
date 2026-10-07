package runtime

import (
	"os/exec"
	"time"
)

// GroupWaitDelay bounds how long cmd.Wait lingers for I/O after the child is
// gone or cancelled, so a grandchild holding a pipe cannot stall it.
const GroupWaitDelay = 2 * time.Second

// ConfigureGroupKill prepares a not-yet-started harness command so that
// cancelling its context kills the child's whole process group (not just the
// child), and Wait returns within GroupWaitDelay even if a grandchild still
// holds stdout/stderr. Call before cmd.Start; it overwrites SysProcAttr.
//
// Windows kills the direct child only (see procgroup_windows.go).
func ConfigureGroupKill(cmd *exec.Cmd) {
	cmd.Cancel = platformGroupKill(cmd)
	cmd.WaitDelay = GroupWaitDelay
}
