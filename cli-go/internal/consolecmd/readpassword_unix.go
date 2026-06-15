//go:build !windows

package consolecmd

// readpassword_unix.go — no-echo terminal password reading on Unix-like systems.
//
// Opens /dev/tty for direct terminal access (independent of stdout/stderr
// redirection).  Disables terminal echo via the TIOCSETA/TIOCGETA (BSD/Darwin)
// or TCSETS/TCGETS (Linux) ioctl, reads a line, then restores echo.
//
// If /dev/tty cannot be opened (non-interactive, piped, test environment),
// falls back to a plain buffered read from os.Stdin (echo will be visible).
//
// Implementation note: We use golang.org/x/sys/unix instead of the syscall
// package to avoid the TCGETS/TIOCGETA dichotomy between Linux and Darwin.
// golang.org/x/sys/unix exports IoctlGetTermios and IoctlSetTermios with the
// correct ioctl number per GOOS, hiding the platform difference.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// readPasswordNoEcho reads a password from the terminal without echoing it.
// stderr is accepted for interface consistency but is not used directly.
func readPasswordNoEcho(stderr io.Writer) (string, error) {
	// Open the controlling terminal directly.
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// Non-interactive or piped — fall back to plain stdin read.
		reader := bufio.NewReader(os.Stdin)
		line, rerr := reader.ReadString('\n')
		if rerr != nil && rerr != io.EOF {
			return "", fmt.Errorf("readPasswordNoEcho: stdin fallback: %w", rerr)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	defer tty.Close()

	fd := int(tty.Fd())

	// Read current terminal attributes (IoctlGetTermios is GOOS-aware:
	// uses TIOCGETA on Darwin/BSD, TCGETS on Linux).
	origTermios, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return "", fmt.Errorf("readPasswordNoEcho: get termios: %w", err)
	}

	// Disable echo; restore on return.
	newTermios := *origTermios
	newTermios.Lflag &^= unix.ECHO | unix.ECHONL
	defer unix.IoctlSetTermios(fd, ioctlSetTermios, origTermios) //nolint:errcheck
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &newTermios); err != nil {
		return "", fmt.Errorf("readPasswordNoEcho: set termios (disable echo): %w", err)
	}

	reader := bufio.NewReader(tty)
	line, rerr := reader.ReadString('\n')
	if rerr != nil && rerr != io.EOF {
		return "", fmt.Errorf("readPasswordNoEcho: read: %w", rerr)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
