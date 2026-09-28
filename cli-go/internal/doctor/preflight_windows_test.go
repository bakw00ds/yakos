//go:build windows

package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestActualCasePath_ShortNameAlias is a Windows-only regression test for
// the real CI failure diagnosed in
// work/current/reports/h1-doctor-ci-diag-2026-09-28.md: GitHub Actions'
// windows-latest runner resolves %TEMP% through an NTFS 8.3 short-name
// alias (RUNNER~1 for the longer profile-directory name runneradmin),
// which t.TempDir() then inherits verbatim. actualCasePath's
// strings.EqualFold-based directory-entry walk treated that alias as an
// unmatched path component (an 8.3 alias is not a case-fold of the long
// name — it's a different string) and returned ("", false) for a path that
// fully exists on disk.
//
// This test creates a real directory with a name long enough to earn its
// own 8.3 alias, queries that alias via GetShortPathName, and verifies
// actualCasePath resolves the short-name path back to the long-name form
// — i.e. it reproduces the exact CI failure shape directly, rather than
// relying on %TEMP% happening to alias on the machine running the test.
//
// If 8dot3 name generation is disabled on the test volume (an NTFS mount
// option; not GitHub Actions' default, but possible elsewhere),
// GetShortPathName returns the long name unchanged and this test skips —
// it can only prove the fix where the OS actually exhibits the aliasing
// behavior it fixes.
func TestActualCasePath_ShortNameAlias(t *testing.T) {
	base := t.TempDir()
	longName := "ALongDirectoryNameForShortNameAliasing"
	longDir := filepath.Join(base, longName)
	if err := os.Mkdir(longDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Canonicalize longDir itself first: base (from t.TempDir()) may carry
	// its own short-name alias (the exact CI shape), and the expected
	// value must reflect that, not the raw joined string.
	wantLongDir := resolveLongPath(longDir)

	shortDir, err := getShortPathName(longDir)
	if err != nil {
		t.Fatalf("GetShortPathName(%q): %v", longDir, err)
	}
	if shortDir == wantLongDir {
		t.Skip("8dot3 name generation appears disabled on this volume; nothing to test")
	}

	actual, ok := actualCasePath(shortDir)
	if !ok {
		t.Fatalf("actualCasePath(%q) = (_, false); want ok=true — this is the exact CI failure mode (8.3 alias treated as unmatched)", shortDir)
	}
	if actual != wantLongDir {
		t.Errorf("actualCasePath(%q) = %q, want %q", shortDir, actual, wantLongDir)
	}
}

// getShortPathName wraps windows.GetShortPathName's two-call buffer-length
// dance (mirrors resolveLongPath's own GetLongPathName dance in
// preflight_windows.go), used only to construct this test's input.
func getShortPathName(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	n, err := windows.GetShortPathName(p, nil, 0)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, n)
	n2, err := windows.GetShortPathName(p, &buf[0], n)
	if err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:n2]), nil
}
