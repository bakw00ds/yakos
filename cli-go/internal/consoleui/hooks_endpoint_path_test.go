package consoleui

import "testing"

// The comparison is pure so the Windows rules run on every platform.
func TestPathWithinWindowsAndUnix(t *testing.T) {
	cases := []struct {
		goos, project, dir string
		want               bool
	}{
		{"windows", `C:\Users\runneradmin\proj`, `C:\Users\runneradmin\proj`, true},
		{"windows", `C:\Users\runneradmin\proj`, `c:/users/RunnerAdmin/proj/sub`, true},
		{"windows", `C:\Users\runneradmin\proj`, `C:\Users\runneradmin\proj\`, true},
		{"windows", `C:\Users\runneradmin\proj`, `C:\Users\runneradmin\proj-evil`, false},
		{"windows", `C:\Users\runneradmin\proj`, `C:\Users\runneradmin\proj\..\other`, false},
		{"windows", `C:\Users\runneradmin\proj`, `D:\Users\runneradmin\proj`, false},
		{"linux", "/p/proj", "/p/proj/sub", true},
		{"linux", "/p/proj", "/P/proj", false},
		{"linux", "/p/proj", "/p/proj-evil", false},
		{"linux", "/p/proj", "/p/proj/../x", false},
		{"linux", "/", "/etc", true},
	}
	for _, c := range cases {
		if got := pathWithin(c.goos, c.project, c.dir); got != c.want {
			t.Errorf("%s within(%q, %q) = %v, want %v", c.goos, c.project, c.dir, got, c.want)
		}
	}
	abs := []struct {
		goos, p string
		want    bool
	}{
		{"windows", `C:\x`, true}, {"windows", `c:/x`, true}, {"windows", `\\srv\share`, true},
		{"windows", `x`, false}, {"windows", `C:x`, false}, {"windows", `/x`, false},
		{"linux", "/x", true}, {"linux", "x", false}, {"linux", `C:\x`, false},
	}
	for _, c := range abs {
		if got := isAbsFor(c.goos, c.p); got != c.want {
			t.Errorf("%s isAbs(%q) = %v, want %v", c.goos, c.p, got, c.want)
		}
	}
}
