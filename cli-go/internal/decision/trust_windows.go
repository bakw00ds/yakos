//go:build windows

package decision

import "os"

// OwnedByCurrentUser: Windows ACLs are not modelled here.
func OwnedByCurrentUser(os.FileInfo) bool { return true }

func groupOrWorldWritable(os.FileInfo) bool { return false }
