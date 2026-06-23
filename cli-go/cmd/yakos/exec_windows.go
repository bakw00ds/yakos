//go:build windows

package main

import (
	"os"
	"os/exec"
)

// execSelf runs exe with argv as a child process on Windows, waits for it to
// complete, and returns its exit status.  Unlike the Unix variant this does
// NOT replace the current process (Windows has no execve(2)); instead we
// start a child, wait, and then exit with the same code.
//
// Because the function must conform to the same signature as the Unix variant
// (returns error), callers treat a non-nil return as "exec failed"; the
// Windows path returns an error only when the child could not be started.
// If the child exits with a non-zero code the caller's process will also exit
// via os.Exit below (matching the Unix behaviour where exec replaces the
// process and the OS propagates the exit code to the parent).
func execSelf(exe string, argv []string) error {
	cmd := exec.Command(exe, argv[1:]...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			// Propagate the exit code so the parent shell sees the same result.
			os.Exit(ee.ExitCode())
		}
		return err
	}
	// Child exited 0 → simulate a successful exec by exiting 0 ourselves.
	os.Exit(0)
	return nil // unreachable
}
