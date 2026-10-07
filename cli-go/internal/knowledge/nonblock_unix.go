//go:build !windows

package knowledge

import "syscall"

// openNonblock keeps an open of a FIFO from blocking until a writer arrives.
const openNonblock = syscall.O_NONBLOCK
