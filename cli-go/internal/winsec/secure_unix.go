//go:build !windows

// Package winsec provides Windows ACL hardening for sensitive files.
//
// On non-Windows platforms POSIX mode 0600 (set at os.WriteFile call-time)
// is the primary access control mechanism; SecureFile is a no-op.
//
// On Windows the real implementation in secure_windows.go calls
// SetNamedSecurityInfo to restrict the file's DACL to the current user
// SID only, removing inherited permissions.
package winsec

// SecureFile is a no-op on non-Windows platforms.
// POSIX 0600 set by os.WriteFile is sufficient access control on Unix/macOS.
func SecureFile(_ string) error { return nil }
