//go:build windows

package setuptoken

import "os"

// markerPermOK is the Windows stub for the setup-token marker file permission
// check.
//
// On Windows, Go's os.FileMode permission bits do not map to Unix rwxrwxrwx —
// files frequently report 0666/0777 regardless of actual NTFS ACLs, so the
// Unix perm & 0o022 check would false-positive on every file.
//
// Windows trust model: the parent directory (<stateDir>) is created with 0700
// (restricted NTFS ACLs via MkdirAll), which prevents writes by non-owners
// through inherited permissions — the same rationale as internal/userstore/perm_windows.go.
// Symlink rejection (os.ModeSymlink) remains active cross-platform via the Lstat
// check in readFile; only the permission-bit check is skipped here.
func markerPermOK(_ os.FileInfo) bool {
	return true // no-op on Windows; ACL trust comes from parent directory
}
