//go:build windows

package dispatch

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBroadScopeKey_StripsDriveLetter verifies that broadScopeKey recovers
// the POSIX-shaped map key from a Windows-absolute, drive-qualified path.
// filepath.Abs("/Users") on Windows resolves to "<CurrentDrive>:\Users" (a
// rooted path with no volume is resolved against the current working
// directory's volume, not re-rooted at "/"), which never equals the
// forward-slash literal "/Users" in broadScopeDirs without this
// normalization -- see validateProjectPath's doc comment and
// TestRun_RejectsBroadScopeDirs (internal/dispatch/identity_enforcement_test.go),
// which failed on Windows CI before this fix because the guard was a
// silent no-op there.
func TestBroadScopeKey_StripsDriveLetter(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`C:\Users`, "/Users"},
		{`D:\Windows`, "/Windows"},
		{`C:\Program Files`, "/Program Files"},
		{`C:\`, "/"},
	}
	for _, c := range cases {
		got := broadScopeKey(filepath.Clean(c.in))
		if got != c.want {
			t.Errorf("broadScopeKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestValidateProjectPath_RejectsBroadScopeDirs_Windows is the Windows-native
// analogue of TestRun_RejectsBroadScopeDirs: it drives validateProjectPath
// directly with the same POSIX-spelled project values the daemon accepts
// from any caller regardless of the daemon's own OS, and asserts each is
// rejected on a real Windows filepath.Abs/Clean resolution.
func TestValidateProjectPath_RejectsBroadScopeDirs_Windows(t *testing.T) {
	for _, project := range []string{"/Users", "/home", "/etc", "/private", "/tmp", "/Windows", "/Program Files"} {
		t.Run(project, func(t *testing.T) {
			err := validateProjectPath(project)
			if err == nil || !strings.Contains(err.Error(), "scope materially equivalent to the filesystem root") {
				t.Fatalf("validateProjectPath(%q): got %v; want broad-scope rejection", project, err)
			}
		})
	}
}
