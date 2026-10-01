//go:build !windows

package supervisorstream

import (
	"os"
	"syscall"
)

func ownedByCurrentUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return true
	}
	return int(st.Uid) == os.Geteuid()
}
