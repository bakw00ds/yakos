//go:build windows

package hookio

import "os"

// Windows has no access(2); a directory without the read-only attribute
// (surfaced as no write permission bits) is treated as writable.
func writable(_ string, fi os.FileInfo) bool {
	return fi.Mode().Perm()&0o200 != 0
}
