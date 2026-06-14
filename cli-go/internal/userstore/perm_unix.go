//go:build !windows

package userstore

import "os"

// filePermOK returns true when fi has safe permissions for users.json on Unix.
// The file must NOT be writable by group or other (perm & 0o022 == 0).
//
// This mirrors the LOW-1 file-trust hardening in netid/roles_perm_unix.go.
// If an attacker can write users.json they can replace argon2id hashes with
// hashes for passwords they know, granting arbitrary login access.
// Requiring owner-only write (0600 or stricter) is the minimum bar.
func filePermOK(fi os.FileInfo) bool {
	return fi.Mode().Perm()&0o022 == 0
}
