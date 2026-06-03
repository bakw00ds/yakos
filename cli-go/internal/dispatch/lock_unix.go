//go:build !windows

package dispatch

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on the file. Best-effort:
// errors are swallowed because some filesystems (NFS, FUSE, tmpfs on
// certain kernels) don't support flock — log appends still proceed.
func lockFile(f *os.File)   { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
