//go:build !windows

package supervisorstream

import (
	"os"
	"syscall"
)

// openNoFollow makes an open fail on a symbolic link instead of following it.
const openNoFollow = syscall.O_NOFOLLOW

// openNonblock makes an open of a FIFO return (or fail) at once instead of waiting
// for the other end. It has no effect on a regular file.
const openNonblock = syscall.O_NONBLOCK

// hardLinked reports whether fi has more than one name. A model with write access
// to the work directory can link a file it should not write to (another session's
// pending file, a file in the project) under the name this code appends to; the
// open then writes, and chmods, the other file. It is a guard in depth, since the
// seatbelt refuses to create such a link.
func hardLinked(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Nlink > 1
}
