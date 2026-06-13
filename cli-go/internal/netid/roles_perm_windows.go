//go:build windows

package netid

import "os"

// rolesFilePermOK is the Windows stub for the roles.json permission check.
//
// On Windows, Go's os.FileMode permission bits are synthesized from NTFS ACLs
// and do not map to the Unix rwxrwxrwx model — files frequently report 0666 or
// 0777 regardless of their actual ACL, so the perm & 0o022 check used on Unix
// would false-positive on every file, breaking role mapping entirely.
//
// Windows trust model for roles.json: the parent directory
// (<stateDir>/mtls/) is created with restricted NTFS ACLs by the yakOS mtls
// package (mirroring the 0700 behaviour on Unix), which prevents writes by
// non-owners through inherited permissions.  yakOS is not a primary
// networked-server target on Windows; this stub accepts any file that passes
// the cross-platform symlink check in Lookup.
//
// Symlink rejection (os.ModeSymlink) remains active cross-platform; only the
// permission-bit check is skipped here.
func rolesFilePermOK(_ os.FileInfo) bool {
	return true // no-op on Windows; ACL trust comes from parent directory
}
