//go:build !windows

package gwtoken

import (
	"os"
	"syscall"
)

// openToken opens the token file without following a symlink at the final path
// element and without blocking on a FIFO (O_NONBLOCK; the handle is checked to
// be a regular file before anything is read from it).
func openToken(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // path is under the state dir
}
