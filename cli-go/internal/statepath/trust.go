package statepath

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// UntrustedError is returned by ReadTrusted for a state file (or the directory
// holding it) that this user cannot be sure only they wrote.
type UntrustedError struct {
	Path   string
	Reason string
}

func (e *UntrustedError) Error() string { return fmt.Sprintf("%s: %s", e.Path, e.Reason) }

// ReadTrusted reads at most max bytes of a state file whose content steers
// behaviour (the default runtime, say) and refuses it unless only this user
// could have written it: the file must not be a symlink, must be a regular file
// owned by the current user, and must not be group- or world-writable, and the
// directory holding it must be a real directory (not a symlink) owned by the
// current user and not group- or world-writable. The rule is the one the budget
// policy file already applies, plus the directory half: statepath.Dir() falls
// back to a path under os.TempDir() when HOME is unset, where another local
// user can pre-create both.
//
// A missing file is returned as an error that satisfies errors.Is(err,
// fs.ErrNotExist), so callers can tell "absent" from "untrusted"
// (*UntrustedError). The file is opened and then compared with the entry that
// was checked, so a swap between the check and the read is refused too.
func ReadTrusted(path string, max int64) ([]byte, error) {
	if err := checkTrustedDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, &UntrustedError{path, "is a symlink"}
	}
	if !fi.Mode().IsRegular() {
		return nil, &UntrustedError{path, "is not a regular file"}
	}
	if !ownedByCurrentUser(fi) {
		return nil, &UntrustedError{path, "is owned by another user"}
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o022 != 0 {
		return nil, &UntrustedError{path, "is group or world writable (chmod go-w)"}
	}
	f, err := os.Open(path) //nolint:gosec // checked above
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(fi, opened) {
		return nil, &UntrustedError{path, "changed while it was being opened"}
	}
	data, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// checkTrustedDir applies the directory half of ReadTrusted. A directory that
// does not exist is reported as fs.ErrNotExist, like a missing file.
func checkTrustedDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return &UntrustedError{dir, "is a symlink (possible planted-directory attack)"}
	}
	if !fi.IsDir() {
		return &UntrustedError{dir, "is not a directory"}
	}
	if !ownedByCurrentUser(fi) {
		return &UntrustedError{dir, "is owned by another user"}
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o022 != 0 {
		return &UntrustedError{dir, "is group or world writable (chmod go-w)"}
	}
	return nil
}

// IsUntrusted reports whether err is an *UntrustedError.
func IsUntrusted(err error) bool {
	var u *UntrustedError
	return errors.As(err, &u)
}
