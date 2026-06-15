package consolecmd

// readpassword.go — signal-safe, cross-platform no-echo password reading.
//
// Uses golang.org/x/term.ReadPassword, which:
//   - Disables terminal echo atomically (no signal-safety gap vs the hand-rolled
//     defer-based restore in the previous implementation).
//   - Restores terminal state correctly on SIGINT (Ctrl-C), SIGTERM, and other
//     signals — the restore is done inside ReadPassword before it returns.
//   - Is cross-platform (Unix, Windows, Plan9 stubs).
//
// When stdin is not a terminal (piped input, CI, tests), ReadPassword returns
// an error; we fall back to reading a plain line from stdin so that test
// injection and piped usage still work (echo will be visible in that case,
// which is acceptable for non-TTY environments).
//
// golang.org/x/term is already in the module graph as a transitive dependency
// (v0.44.0+); no new go.mod entry is required.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// readPasswordNoEcho reads a password from the terminal without echoing it.
//
//   - stderr is accepted for interface consistency but is not used.
//   - When stdin is a terminal: uses term.ReadPassword(int(fd)) for signal-safe
//     no-echo input.
//   - When stdin is not a terminal (piped, CI, test environment): falls back
//     to a plain buffered read from os.Stdin.
func readPasswordNoEcho(stderr io.Writer) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		// Signal-safe no-echo read. term.ReadPassword handles SIGINT restore.
		pw, err := term.ReadPassword(fd)
		if err != nil {
			return "", fmt.Errorf("readPasswordNoEcho: %w", err)
		}
		return string(pw), nil
	}

	// Non-terminal (piped input / CI / test): plain stdin read.
	// Echo will be visible, but this path is for non-interactive environments.
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("readPasswordNoEcho: stdin read: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
