//go:build !windows

package netid

import "os"

// rolesFilePermOK returns true if fi has safe permissions for roles.json on
// Unix: the file must NOT be writable by group or other (perm & 0o022 == 0).
//
// This is the Unix half of the LOW-1 file-trust split.  On Unix, os.FileMode
// permission bits faithfully reflect the underlying filesystem mode bits, so
// the check is reliable.
//
// Rationale: if an attacker can write roles.json, they can grant themselves
// any role.  Requiring owner-only write (0600 or stricter) is the minimum
// bar.  The roles.json created by the yakOS mtls package always uses 0600;
// this check catches accidental chmod or cp operations that loosen permissions.
func rolesFilePermOK(fi os.FileInfo) bool {
	return fi.Mode().Perm()&0o022 == 0
}
