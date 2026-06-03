//go:build windows

package start

import (
	"fmt"
	"os"
	"os/exec"
)

// execSyscall on Windows falls back to exec.Command + os.Exit because
// syscall.Exec is not available on Windows. This is acceptable because yakOS
// targets Unix-like systems; Windows is a best-effort build.
func execSyscall(argv0 string, argv []string, env []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("execSyscall: empty argv")
	}
	cmd := exec.Command(argv0, argv[1:]...) //nolint:gosec
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil // unreachable
}
