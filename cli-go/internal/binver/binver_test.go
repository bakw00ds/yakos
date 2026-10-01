package binver

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtLeast(t *testing.T) {
	cases := []struct {
		have string
		want bool
	}{
		{"0.60.0.0", true}, {"0.60.1.0 (go)", true}, {"0.61.0.0", true}, {"1.0.0.0", true},
		{"0.59.9.9", false}, {"0.58.0.0", false}, {"dev", false}, {"", false},
	}
	for _, c := range cases {
		if got := AtLeast(c.have, MinHookRun); got != c.want {
			t.Errorf("AtLeast(%q) = %v, want %v", c.have, got, c.want)
		}
	}
}

func TestSupportsHookRunProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	mk := func(body string) string {
		p := filepath.Join(t.TempDir(), "yakos")
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name, body string
		want       bool
	}{
		{"new", "#!/bin/sh\necho 'yakos: no Go implementation for hook' >&2\nexit 2\n", true},
		{"old reads --impl as a hook name", "#!/bin/sh\necho 'unknown hook \"--impl\"' >&2\nexit 0\n", false},
		{"exit 2 for another reason", "#!/bin/sh\necho boom >&2\nexit 2\n", false},
		{"crash", "#!/bin/sh\nexit 139\n", false},
	}
	for _, c := range cases {
		if got, _ := SupportsHookRun(mk(c.body)); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if ok, _ := SupportsHookRun(filepath.Join(t.TempDir(), "nope")); ok {
		t.Error("missing binary reported as supporting hook run")
	}
}
