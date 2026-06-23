//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execSelf replaces the current process with exe invoked with argv using
// syscall.Exec.  On Unix this is a true exec: the current process image is
// replaced and this function never returns on success.  On error the error
// is returned so the caller can fall back gracefully.
//
// env is inherited from the current process (os.Environ()).
func execSelf(exe string, argv []string) error {
	return syscall.Exec(exe, argv, os.Environ())
}
