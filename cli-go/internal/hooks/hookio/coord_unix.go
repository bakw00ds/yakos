//go:build !windows

package hookio

import (
	"os"

	"golang.org/x/sys/unix"
)

func writable(dir string, _ os.FileInfo) bool {
	return unix.Access(dir, unix.W_OK) == nil
}
