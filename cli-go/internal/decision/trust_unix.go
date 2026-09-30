//go:build !windows

package decision

import (
	"os"
	"syscall"
)

// OwnedByCurrentUser reports whether fi is owned by the effective user.
func OwnedByCurrentUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(st.Uid) == os.Geteuid()
}

// groupOrWorldWritable reports mode bits 0o022.
func groupOrWorldWritable(fi os.FileInfo) bool { return fi.Mode().Perm()&0o022 != 0 }
