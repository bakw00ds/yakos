//go:build windows

package budget

import (
	"errors"
	"syscall"
)

// lockBusy: on Windows an O_EXCL create on a lock file that another process
// just deleted, and that is still pending delete, fails with
// ERROR_ACCESS_DENIED (or ERROR_SHARING_VIOLATION, errno 32) instead of
// ERROR_FILE_EXISTS. Both mean "busy, retry within the lock budget".
func lockBusy(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, syscall.Errno(32))
}
