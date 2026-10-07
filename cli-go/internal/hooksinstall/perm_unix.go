//go:build !windows

package hooksinstall

import (
	"os"
	"syscall"
)

// modeWritableByOthers reports a group- or world-writable file or directory.
func modeWritableByOthers(fi os.FileInfo) bool { return fi.Mode().Perm()&0o022 != 0 }

// ownedByCurrentUser reports whether the file belongs to the running user.
func ownedByCurrentUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
