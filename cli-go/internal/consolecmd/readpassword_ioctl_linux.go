//go:build linux

package consolecmd

import "golang.org/x/sys/unix"

// ioctlGetTermios and ioctlSetTermios are the Linux ioctl numbers for
// reading and writing terminal attributes.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
