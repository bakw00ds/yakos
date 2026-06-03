//go:build windows

package dispatch

import "os"

// On Windows, file locking is best-effort and not implemented in Phase 1.
// O_APPEND on Windows serializes small writes anyway; cross-process safety
// degrades gracefully. Daemon-era flock is Phase 2 (see Decision Q8).
func lockFile(f *os.File)   {}
func unlockFile(f *os.File) {}
