//go:build windows

package gwtoken

import "os"

// openToken opens the token file. Windows has no O_NOFOLLOW; the Lstat before it
// and the regular-file check on the handle after it are the guard there.
func openToken(p string) (*os.File, error) {
	return os.Open(p) //nolint:gosec // path is under the state dir
}
