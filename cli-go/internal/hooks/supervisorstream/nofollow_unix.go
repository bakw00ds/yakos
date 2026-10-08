//go:build !windows

package supervisorstream

import "syscall"

// openNoFollow makes an open fail on a symbolic link instead of following it.
const openNoFollow = syscall.O_NOFOLLOW

// openNonblock makes an open of a FIFO return (or fail) at once instead of waiting
// for the other end. It has no effect on a regular file.
const openNonblock = syscall.O_NONBLOCK
