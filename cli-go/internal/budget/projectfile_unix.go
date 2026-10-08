//go:build unix

package budget

import (
	"errors"
	"syscall"
)

// projectReadFlags never blocks (a FIFO cannot hold open(2)) and never follows a
// symlink in the last component.
const projectReadFlags = syscall.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

// projectLinkRefused reports an open that failed on a symlink: ELOOP, or EMLINK on FreeBSD.
func projectLinkRefused(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
