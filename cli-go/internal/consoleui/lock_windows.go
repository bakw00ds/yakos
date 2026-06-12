//go:build windows

package consoleui

import "os"

// On Windows, file locking is best-effort and not implemented.
// O_APPEND on Windows serializes small writes anyway; cross-process safety
// degrades gracefully.
func lockFile(f *os.File)   {}
func unlockFile(f *os.File) {}
