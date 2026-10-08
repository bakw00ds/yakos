//go:build !windows

package workflow

import "syscall"

// oNonblock keeps an open of a FIFO or device from blocking; oNoFollow makes
// an open refuse a symlink in the final path element.
const (
	oNonblock = syscall.O_NONBLOCK
	oNoFollow = syscall.O_NOFOLLOW
)
