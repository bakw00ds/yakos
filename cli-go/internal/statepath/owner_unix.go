//go:build !windows

package statepath

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether fi is owned by the effective user.
func ownedByCurrentUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true // unknown platform detail: do not block
	}
	return int(st.Uid) == os.Geteuid()
}
