//go:build windows

package hooksinstall

import "os"

// Windows permission bits and owners are not meaningful here; the profile's
// ACL (inherited from the user's home) is the guard.
func modeWritableByOthers(os.FileInfo) bool { return false }
func ownedByCurrentUser(os.FileInfo) bool   { return true }
