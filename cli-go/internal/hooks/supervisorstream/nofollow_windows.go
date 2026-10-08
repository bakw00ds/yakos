//go:build windows

package supervisorstream

// openNoFollow is 0 on Windows, which has no O_NOFOLLOW: the Lstat check before
// the open is the only guard there.
const openNoFollow = 0

// openNonblock is 0 on Windows, which has no FIFO to block an open on.
const openNonblock = 0
