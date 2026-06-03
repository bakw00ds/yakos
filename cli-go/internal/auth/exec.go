package auth

import (
	"os"
	"os/exec"
)

// commandOnPath returns true when the given binary name is found on PATH.
func commandOnPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// defaultExecImpl runs name with args, forwarding stdin/stdout/stderr to the
// parent process. This produces the interactive UX that codex login / codex
// logout require.
func defaultExecImpl(name string, args []string) error {
	cmd := exec.Command(name, args...) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
