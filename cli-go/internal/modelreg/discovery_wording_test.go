package modelreg

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// The cache-write warning is worded by role and names no path: errors from the state
// directory carry its absolute path, and a probe's output holds none. The wording is
// pinned for each branch, so neither can quietly start printing the error.
func TestCacheWriteWarningNamesNoPath(t *testing.T) {
	const generic = "cache not written: the yakOS state directory is not usable (not private, or not writable)"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"permission", &fs.PathError{Op: "open", Path: "/Users/someone/.yakos-state/.model-discovery-123", Err: fs.ErrPermission},
			"cache not written: permission denied on the yakOS state directory"},
		{"not a directory", &fs.PathError{Op: "mkdir", Path: "/Users/someone/.yakos-state", Err: errors.New("not a directory")}, generic},
		{"refused by SecureDir", errors.New("statepath: refusing state dir /Users/someone/.yakos-state: it is a symlink"), generic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cacheWriteWarning(tc.err)
			if got != tc.want {
				t.Errorf("cacheWriteWarning = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "/Users/") || strings.Contains(got, "someone") {
				t.Errorf("the warning names a path: %q", got)
			}
		})
	}
}
