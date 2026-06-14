//go:build windows

package userstore

import "os"

// filePermOK is the Windows stub for the users.json permission check.
//
// On Windows, Go's os.FileMode permission bits are synthesized from NTFS ACLs
// and do not map to the Unix rwxrwxrwx model — files frequently report 0666 or
// 0777 regardless of their actual ACL, so the perm & 0o022 check used on Unix
// would false-positive on every file.
//
// Windows trust model: the parent directory (<stateDir>/users/) is created with
// 0700 (restricted NTFS ACLs via MkdirAll), which prevents writes by
// non-owners through inherited permissions.  yakOS is not a primary
// networked-server target on Windows; this stub accepts any file that passes
// the cross-platform symlink check in Open.
//
// Symlink rejection (os.ModeSymlink) remains active cross-platform; only the
// permission-bit check is skipped here.
func filePermOK(_ os.FileInfo) bool {
	return true // no-op on Windows; ACL trust comes from parent directory
}
