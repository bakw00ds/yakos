//go:build windows

package supervisorstream

import "os"

func ownedByCurrentUser(os.FileInfo) bool { return true }
