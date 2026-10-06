//go:build !windows

package supervisorstream

import "syscall"

// openNoFollow makes an open fail on a symbolic link instead of following it.
const openNoFollow = syscall.O_NOFOLLOW
