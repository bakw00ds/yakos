//go:build !windows

package consoleui

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on the file (flock).
// Best-effort: errors on unsupported filesystems (NFS, FUSE, some tmpfs) are
// silently swallowed — appends proceed anyway.
func lockFile(f *os.File)   { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX) }
func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
