//go:build unix

package agentscompose

import (
	"errors"
	"syscall"
)

// readFlags opens a file that was inspected a moment ago. It never blocks, so a
// FIFO swapped in for the file cannot hold open(2) for good, and it never follows
// a symlink in the last component, so one swapped in cannot lead anywhere.
const readFlags = syscall.O_RDONLY | syscall.O_NONBLOCK | syscall.O_NOFOLLOW

// linkRefused reports an open that failed because the last component is a
// symlink: ELOOP, or EMLINK on FreeBSD.
func linkRefused(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
