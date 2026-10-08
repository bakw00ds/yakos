//go:build windows

package supervisorstream

import "os"

// openNoFollow is 0 on Windows, which has no O_NOFOLLOW: the Lstat check before
// the open is the only guard there.
const openNoFollow = 0

// openNonblock is 0 on Windows, which has no FIFO to block an open on.
const openNonblock = 0

// hardLinked is false on Windows: Go does not report a link count there.
func hardLinked(os.FileInfo) bool { return false }
