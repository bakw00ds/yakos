//go:build windows

package consoleui

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive advisory lock on the file using LockFileEx.
// This provides real cross-process mutual exclusion on Windows for concurrent
// appenders to the same NDJSON transcript file.
// Best-effort: if the lock call fails (e.g. on a network share that doesn't
// support mandatory locking), the append proceeds without the lock.
func lockFile(f *os.File) {
	var ol windows.Overlapped
	// LOCKFILE_EXCLUSIVE_LOCK (0x00000002): exclusive lock.
	// Passing 0 for nNumberOfBytesToLockLow/High is intentional per the
	// Windows docs pattern for "lock entire file" when using Overlapped I/O
	// semantics; we use a generous range instead.
	_ = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ol)
}

// unlockFile releases the exclusive lock acquired by lockFile.
func unlockFile(f *os.File) {
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
