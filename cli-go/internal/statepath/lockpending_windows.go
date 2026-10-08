//go:build windows

package statepath

import (
	"errors"
	"io/fs"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION (32).
const errSharingViolation = syscall.Errno(32)

// lockReleasePending reports whether err is what Windows gives for creating a
// name whose previous file is delete-pending: access denied, or a sharing
// violation while the remover's handle is still closing.
var lockReleasePending = func(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, errSharingViolation)
}
