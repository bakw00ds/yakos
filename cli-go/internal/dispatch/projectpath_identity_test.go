package dispatch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// K-86 item 1 (k82-security-review-2026-09-23.md K5/K6): validateProjectPath
// must compare by file identity (os.SameFile), not by spelling. Case variants
// on case-insensitive filesystems and macOS firmlinks resolve to the same
// inode as a denied directory but never match a textual denylist.

const broadScopeMsg = "scope materially equivalent to the filesystem root"

// withBroadScopeInfos swaps the identity denylist for the duration of a test.
func withBroadScopeInfos(t *testing.T, dirs ...string) {
	t.Helper()
	old := broadScopeInfos
	var infos []os.FileInfo
	for _, d := range dirs {
		fi, err := os.Stat(d)
		if err != nil {
			t.Fatalf("stat %s: %v", d, err)
		}
		infos = append(infos, fi)
	}
	broadScopeInfos = infos
	t.Cleanup(func() { broadScopeInfos = old })
}

func wantDenied(t *testing.T, project string) {
	t.Helper()
	err := validateProjectPath(project)
	if err == nil {
		t.Fatalf("validateProjectPath(%q) = nil; want rejection", project)
	}
}

// Hermetic: a symlink whose spelling is unrelated to any denylist string is
// still denied because it is the same inode as a denied directory.
func TestValidateProjectPath_SameFile_SymlinkToDeniedDir(t *testing.T) {
	denied := t.TempDir()
	withBroadScopeInfos(t, denied)
	link := filepath.Join(t.TempDir(), "innocent-name")
	if err := os.Symlink(denied, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	wantDenied(t, link)
	wantDenied(t, link+string(filepath.Separator)+".")
	wantDenied(t, denied+string(filepath.Separator)+".")
	// A different directory is still fine.
	if err := validateProjectPath(t.TempDir()); err != nil {
		t.Fatalf("ordinary dir rejected: %v", err)
	}
	// A child of the denied dir is fine (only the dir itself is broad).
	child := filepath.Join(denied, "proj")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateProjectPath(child); err != nil {
		t.Fatalf("child of denied dir rejected: %v", err)
	}
}

// A stat error other than not-exist must deny (fail closed), not allow.
func TestValidateProjectPath_StatErrorFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission-based stat failure needs a non-root POSIX user")
	}
	base := t.TempDir()
	locked := filepath.Join(base, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(locked, "proj")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	err := validateProjectPath(target)
	if err == nil {
		t.Fatal("validateProjectPath allowed a path whose stat failed with EACCES; must fail closed")
	}
}

// A nonexistent project (to be created later) keeps working: it cannot alias
// an existing denied directory.
func TestValidateProjectPath_NonexistentStillAllowed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "not-yet")
	if err := validateProjectPath(p); err != nil {
		t.Fatalf("nonexistent project rejected: %v", err)
	}
}

// A denylist entry that does not exist on this OS is skipped at init, never
// panics, and never matches.
func TestComputeBroadScopeInfos_SkipsMissingAndNonDirs(t *testing.T) {
	infos := computeBroadScopeInfos()
	for _, fi := range infos {
		if !fi.IsDir() {
			t.Fatalf("non-directory in identity denylist: %v", fi.Name())
		}
	}
	// Every collected entry must be one that actually exists; duplicates by
	// identity are collapsed.
	for i := range infos {
		for j := i + 1; j < len(infos); j++ {
			if os.SameFile(infos[i], infos[j]) {
				t.Fatalf("duplicate identity entries %d and %d", i, j)
			}
		}
	}
}

// Real-OS: case variants of every existing non-symlink broad dir are denied
// wherever the filesystem is case-insensitive (macOS default, Windows).
func TestValidateProjectPath_CaseVariants(t *testing.T) {
	checked := 0
	for _, dir := range []string{"/Users", "/Library", "/Applications", "/Volumes", "/System", "/Windows", "/ProgramData"} {
		orig, err := os.Stat(dir)
		if err != nil {
			continue
		}
		variant := "/" + strings.ToUpper(dir[1:])
		if variant == dir {
			variant = "/" + strings.ToLower(dir[1:])
		}
		alt, err := os.Stat(variant)
		if err != nil || !os.SameFile(orig, alt) {
			continue // case-sensitive filesystem: variant is a different (absent) path
		}
		checked++
		t.Run(dir, func(t *testing.T) {
			wantDenied(t, variant)
			wantDenied(t, variant+"/.")
		})
	}
	if checked == 0 {
		t.Skip("no case-insensitive broad-scope directory on this filesystem")
	}
}

// Real-OS: macOS firmlinks under /System/Volumes/Data are the same inode as
// the broad dirs they mirror.
func TestValidateProjectPath_Firmlinks(t *testing.T) {
	root := "/System/Volumes/Data"
	if _, err := os.Stat(root); err != nil {
		t.Skip("no /System/Volumes/Data on this OS")
	}
	wantDenied(t, root)
	wantDenied(t, "/System/Volumes")
	for _, d := range []string{"Users", "Library", "Applications", "Volumes", "private/etc"} {
		p := root + "/" + d
		if _, err := os.Stat(p); err != nil {
			continue
		}
		t.Run(d, func(t *testing.T) { wantDenied(t, p) })
	}
}

// The extras named by the review must be in the identity set.
func TestBroadScopeExtras_IncludeDataVolumeAndVarRoot(t *testing.T) {
	for _, want := range []string{"/System/Volumes/Data", "/System/Volumes", "/var/root"} {
		found := false
		for _, e := range broadScopeIdentityExtras {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Errorf("broadScopeIdentityExtras missing %q", want)
		}
	}
}
