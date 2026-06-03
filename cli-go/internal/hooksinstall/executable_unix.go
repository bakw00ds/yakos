//go:build !windows

package hooksinstall

import "os"

// isExecutable reports whether fi represents an executable file on Unix.
// Uses the owner-executable bit (mode & 0o100).
func isExecutable(fi os.FileInfo) bool {
	return fi.Mode()&0o100 != 0
}
