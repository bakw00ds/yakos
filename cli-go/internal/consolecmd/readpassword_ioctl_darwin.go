//go:build darwin

package consolecmd

import "golang.org/x/sys/unix"

// ioctlGetTermios and ioctlSetTermios are the Darwin ioctl numbers for
// reading and writing terminal attributes.
const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
