//go:build !windows

package workflow

import "os"

// openRunJSONForRead is just os.Open on non-Windows platforms. POSIX
// rename() can always replace a file that another process still has open
// (the old inode simply stays alive until the last handle closes) — there
// is no Windows-style "sharing violation" to work around. See the Windows
// build's runstate_open_windows.go for why this exists there.
func openRunJSONForRead(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec
}
