// Package passthrough executes the existing bash yakos binary for any
// subcommand not yet ported to Go. It propagates exit codes, stdin, stdout,
// and stderr unchanged so the caller sees exactly what bash yakos would have
// produced.
//
// The passthrough is the mechanism behind the "shadow-mode" strategy: the Go
// binary is a thin wrapper that calls into bash for everything until each
// subcommand is individually ported.
package passthrough

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// BashYakosPath returns the expected location of the bash yakos script,
// inferred from yakosRoot (<repo-root>/cli/yakos).
func BashYakosPath(yakosRoot string) string {
	return filepath.Join(yakosRoot, "cli", "yakos")
}

// Exec replaces the current process with the bash yakos binary, forwarding
// all args. On platforms that support it (all POSIX targets), this uses
// syscall.Exec so the bash process inherits the PID and its exit code becomes
// the process exit code directly.
//
// args should be the arguments to forward (not including the binary name).
// yakosRoot is the absolute path to the repository root.
//
// Returns an error only if the exec setup fails; on success it never returns.
func Exec(yakosRoot string, args []string) error {
	bashPath := BashYakosPath(yakosRoot)

	// Resolve to absolute path; also validates the file exists.
	absPath, err := exec.LookPath(bashPath)
	if err != nil {
		// LookPath fails for relative paths not on $PATH; try the path directly.
		absPath = bashPath
		if _, statErr := os.Stat(absPath); statErr != nil {
			return fmt.Errorf("passthrough: bash yakos not found at %s: %w", absPath, statErr)
		}
	}

	argv := append([]string{absPath}, args...)
	return syscall.Exec(absPath, argv, os.Environ())
}

// Run executes the bash yakos binary as a child process (not replacing the
// current process) and returns its exit code. stdout and stderr are inherited
// from the calling process so the output appears directly in the terminal.
//
// Use Run (rather than Exec) when you need to capture the exit code in Go
// code after the call — e.g., in tests or when you need to do post-processing.
func Run(yakosRoot string, args []string) (int, error) {
	bashPath := BashYakosPath(yakosRoot)

	cmd := exec.Command(bashPath, args...) //nolint:gosec // intentional passthrough
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, fmt.Errorf("passthrough: running bash yakos: %w", err)
	}
	return 0, nil
}
