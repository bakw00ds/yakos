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

// ErrNoBashYakos is returned when the bash yakos script is not present at the
// expected location. This distinguishes a Go-only install (no bash tree) from
// other exec failures so callers can emit a targeted help message.
type ErrNoBashYakos struct {
	Path string
}

func (e *ErrNoBashYakos) Error() string {
	return fmt.Sprintf(
		"yakos: this is a Go-only install (no bash yakos found at %s).\n"+
			"  Run Go-native commands with 'YAKOS_IMPL=go yakos <cmd>',\n"+
			"  or install the full yakos tree. See docs/go-shadow-mode.md.",
		e.Path,
	)
}

// checkBashExists returns an ErrNoBashYakos when the bash yakos script is
// absent, or nil when it is present.
func checkBashExists(bashPath string) error {
	if _, err := os.Stat(bashPath); err != nil {
		return &ErrNoBashYakos{Path: bashPath}
	}
	return nil
}

// BashYakosExists reports whether the bash yakos script is present at the
// expected location under yakosRoot. It returns true iff the file exists and
// is stat-able.
//
// Call sites use this to decide whether to enter shadow-mode (passthrough) or
// fall through to Go-native routing when YAKOS_IMPL is unset.
func BashYakosExists(yakosRoot string) bool {
	return checkBashExists(BashYakosPath(yakosRoot)) == nil
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

	if err := checkBashExists(bashPath); err != nil {
		return err
	}

	// Resolve to absolute path; also validates the file exists.
	absPath, err := exec.LookPath(bashPath)
	if err != nil {
		// LookPath fails for relative paths not on $PATH; try the path directly.
		absPath = bashPath
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

	if err := checkBashExists(bashPath); err != nil {
		return 1, err
	}

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
