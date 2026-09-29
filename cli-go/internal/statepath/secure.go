package statepath

import (
	"fmt"
	"os"
	"runtime"
)

// SecureDir creates dir if absent (0700) and, whether new or pre-existing,
// verifies it is a real directory (not a symlink) owned by the current user
// with no group/other permission bits, tightening the mode when needed.
//
// S-2 R12 / N5 (s2-daemon-security-review-r2-2026-09-21.md): MkdirAll applies
// its mode only when it CREATES a directory, so a pre-existing directory
// (a bash-created 0755 install, or one an attacker pre-created in a shared
// location such as the os.TempDir() fallback) silently keeps its mode. A
// symlink is rejected rather than followed: tightening whatever an attacker
// pointed it at would be worse than refusing. A directory owned by another
// user is refused for the same reason (it cannot be made private, and its
// owner can swap files under us).
func SecureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("statepath: mkdir %s: %w", dir, err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("statepath: stat %s: %w", dir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("statepath: refusing state dir %s: it is a symlink (possible planted-directory attack)", dir)
	}
	if !fi.IsDir() {
		return fmt.Errorf("statepath: refusing state dir %s: not a directory", dir)
	}
	if !ownedByCurrentUser(fi) {
		return fmt.Errorf("statepath: refusing state dir %s: owned by another user", dir)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec
			return fmt.Errorf("statepath: state dir %s has permissive mode %o and could not be tightened: %w", dir, fi.Mode().Perm(), err)
		}
	}
	return nil
}

// SecureFile verifies an already-open state file is owned by the current user
// and tightens it to 0600. It uses the open descriptor (fstat/fchmod), never
// the path, so a rename between open and check cannot redirect it.
//
// S-2 R12 / N5: O_CREATE's mode is ignored for an existing file, so an
// attacker who pre-creates the log 0666 would otherwise receive every task
// preview the daemon appends. A file owned by someone else cannot be secured
// and is refused (returning an error makes the caller skip the write).
func SecureFile(f *os.File) error {
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("statepath: stat %s: %w", f.Name(), err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("statepath: refusing %s: not a regular file", f.Name())
	}
	if !ownedByCurrentUser(fi) {
		return fmt.Errorf("statepath: refusing %s: owned by another user", f.Name())
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		if err := f.Chmod(0o600); err != nil {
			return fmt.Errorf("statepath: %s has permissive mode %o and could not be tightened: %w", f.Name(), fi.Mode().Perm(), err)
		}
	}
	return nil
}
