//go:build !windows

// preflight_unix.go — non-Windows no-op counterpart to preflight_windows.go.
//
// 8.3 short-name path aliasing (RUNNER~1 for runneradmin, etc.) is an NTFS/
// Win32 concept with no equivalent on macOS/Linux filesystems, so there is
// nothing for actualCasePath (preflight.go) to resolve here.
package doctor

// resolveLongPath is a no-op on non-Windows platforms.
func resolveLongPath(path string) string { return path }
