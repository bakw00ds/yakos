//go:build unix

package projfile

import (
	"errors"
	"syscall"
)

// readFlags never blocks (a FIFO cannot hold open(2)) and never follows a
// symlink in the last component.
const readFlags = syscall.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

// linkRefused reports an open that failed on a symlink: ELOOP, or EMLINK on FreeBSD.
func linkRefused(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
