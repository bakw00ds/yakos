//go:build windows

package statepath

import "os"

// ownedByCurrentUser is a no-op on Windows: ownership is expressed through
// ACLs (see internal/winsec), not a uid the stdlib exposes.
func ownedByCurrentUser(os.FileInfo) bool { return true }
