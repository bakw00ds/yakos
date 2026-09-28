//go:build windows

// preflight_windows.go — Windows-only helper for actualCasePath (preflight.go).
//
// See preflight_unix.go for why this split exists: `actualCasePath` walks a
// path's components and matches each one against real directory entries via
// strings.EqualFold, which handles ordinary case differences (YakOS vs
// yakos) but not an NTFS 8.3 short-name alias (RUNNER~1 for a long name
// like runneradmin) — an alias is not a case-fold of the long name, it's a
// different string. GitHub Actions' windows-latest runner resolves %TEMP%
// through exactly such an alias, which t.TempDir() then inherits, so this
// is not a hypothetical: it failed deterministically in CI (see
// work/current/reports/h1-doctor-ci-diag-2026-09-28.md).
//
// The fix expands short-name segments to their long-name form up front via
// the Win32 GetLongPathNameW API, before actualCasePath's case-fold walk
// ever sees them. This mirrors internal/winsec's secure_windows.go /
// secure_unix.go build-tag split.
package doctor

import "golang.org/x/sys/windows"

// resolveLongPath expands any 8.3 short-name components in path to their
// long-name form. It is best-effort: on any failure (a component doesn't
// exist, the path is malformed, buffer sizing fails) it returns path
// unchanged, and actualCasePath's own walk proceeds/fails exactly as it
// would without this step — this function only ever helps, never turns a
// working path into a broken one.
func resolveLongPath(path string) string {
	if path == "" {
		return path
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return path
	}

	// First call with a nil/zero-length buffer returns the required
	// buffer length (including the NUL terminator), per the Win32
	// GetLongPathNameW contract.
	n, err := windows.GetLongPathName(p, nil, 0)
	if err != nil || n == 0 {
		return path
	}

	buf := make([]uint16, n)
	n2, err := windows.GetLongPathName(p, &buf[0], n)
	if err != nil || n2 == 0 || n2 > uint32(len(buf)) {
		return path
	}
	return windows.UTF16ToString(buf[:n2])
}
