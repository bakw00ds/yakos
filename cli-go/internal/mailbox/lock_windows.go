//go:build windows

package mailbox

import "os"

// Phase 1: file locking not implemented on Windows. See dispatch/lock_windows.go.
func lockFile(f *os.File) error   { return nil }
func unlockFile(f *os.File) error { return nil }
