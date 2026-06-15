//go:build !windows

package setuptoken

import "os"

// markerPermOK returns true when fi has safe permissions for the setup-token
// marker file on Unix.  The file must not be writable by group or other
// (perm & 0o022 == 0).
//
// This mirrors the LOW-1 file-trust hardening in internal/userstore/perm_unix.go.
// A world- or group-writable marker file could be tampered to inject a known token
// value, bypassing the single-use and constant-time protections.
//
// On perm drift (e.g. umask 0): LoadOrGenerate re-tightens via Chmod after writing;
// on an existing file that drifted, readFile rejects it and generates a fresh token.
func markerPermOK(fi os.FileInfo) bool {
	return fi.Mode().Perm()&0o022 == 0
}
