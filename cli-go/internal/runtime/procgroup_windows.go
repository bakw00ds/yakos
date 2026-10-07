//go:build windows

package runtime

import "os/exec"

// platformGroupKill on Windows kills the direct child only: there are no POSIX
// process groups and the repo has no job-object helper. A backgrounded
// grandchild may survive as an orphan (a known limitation, identical to
// interactive/procattr_windows.go); WaitDelay still bounds cmd.Wait.
func platformGroupKill(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
