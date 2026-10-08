package budget

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/projfile"
)

func writeProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// within fails the test if fn does not return by the deadline, so a regression
// that blocks on a FIFO reports instead of hanging the run.
func within(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("read did not return within %s", d)
	}
}

func TestReadProjectConfigRegularFile(t *testing.T) {
	dir := writeProject(t, "agent_budgets:\n  backend: 5\nsupervisor:\n  agent: chief\n")
	c := readProjectConfig(dir)
	if c.warn != "" {
		t.Fatalf("warn = %q", c.warn)
	}
	if v := c.projectUSD("backend"); v == nil || *v != 5 {
		t.Fatalf("backend = %v", v)
	}
	if c.aliasFor("chief") != supervisorAgent {
		t.Fatalf("supervisor alias lost: %v", c.supervisor)
	}
}

func TestReadProjectConfigMissingIsQuiet(t *testing.T) {
	c := readProjectConfig(t.TempDir())
	if c.warn != "" || len(c.limits) != 0 {
		t.Fatalf("missing file: %+v", c)
	}
}

func TestReadProjectConfigOversized(t *testing.T) {
	// A valid prefix with a limit in it, padded past the cap by a comment: the
	// whole file is refused, its limits are not used.
	body := "agent_budgets:\n  backend: 5\n#" + strings.Repeat("x", projfile.MaxBytes)
	c := readProjectConfig(writeProject(t, body))
	if !strings.Contains(c.warn, "larger than") || len(c.limits) != 0 {
		t.Fatalf("oversized file accepted: %+v", c)
	}
	// Exactly at the cap is still read.
	pad := projfile.MaxBytes - len("agent_budgets:\n  backend: 5\n#")
	c = readProjectConfig(writeProject(t, "agent_budgets:\n  backend: 5\n#"+strings.Repeat("x", pad)))
	if c.warn != "" || c.projectUSD("backend") == nil {
		t.Fatalf("file at the cap refused: %+v", c)
	}
}

func TestReadProjectConfigSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := writeProject(t, "agent_budgets:\n  backend: 5\n")
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(real, ".yakos.yml"), filepath.Join(dir, ".yakos.yml")); err != nil {
		t.Fatal(err)
	}
	c := readProjectConfig(dir)
	if !strings.Contains(c.warn, "symlink") || len(c.limits) != 0 {
		t.Fatalf("symlink followed: %+v", c)
	}
}

func TestReadProjectConfigDeviceSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero")
	}
	dir := t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(dir, ".yakos.yml")); err != nil {
		t.Fatal(err)
	}
	var c projectConfig
	within(t, 5*time.Second, func() { c = readProjectConfig(dir) })
	if !strings.Contains(c.warn, "symlink") {
		t.Fatalf("warn = %q", c.warn)
	}
}

func TestReadProjectConfigDirectoryRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".yakos.yml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if c := readProjectConfig(dir); !strings.Contains(c.warn, "not a regular file") {
		t.Fatalf("warn = %q", c.warn)
	}
}

const renamesSupervisorToBackend = "supervisor:\n  agent: backend\n  model: opus\n"

// refusedProjects builds a project whose .yakos.yml renames the supervisor to
// backend but is refused: once as a symlink, once padded past the cap.
func refusedProjects(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	big := t.TempDir()
	body := renamesSupervisorToBackend + "#" + strings.Repeat("x", projfile.MaxBytes)
	if err := os.WriteFile(filepath.Join(big, ".yakos.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out["oversized"] = big
	if runtime.GOOS != "windows" {
		real := writeProject(t, renamesSupervisorToBackend)
		link := t.TempDir()
		if err := os.Symlink(filepath.Join(real, ".yakos.yml"), filepath.Join(link, ".yakos.yml")); err != nil {
			t.Fatal(err)
		}
		out["symlink"] = link
	}
	return out
}

// sec-360 H1: a refused file is absent, so it cannot rename the supervisor, and
// the supervisor keeps its built-in budget and sonnet ceiling (never OFF).
func TestRefusedProjectFileIsAbsentAndNeverTurnsTheSupervisorBudgetOff(t *testing.T) {
	for name, proj := range refusedProjects(t) {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			o := Options{StateDir: dir, Project: proj}
			if !ProjectRefused(proj) {
				t.Fatal("ProjectRefused = false")
			}
			if got := ProjectSupervisorAgents(proj); len(got) != 0 {
				t.Fatalf("a refused file named a supervisor: %v", got)
			}
			st, err := Evaluate("supervisor", o)
			if err != nil {
				t.Fatal(err)
			}
			if st.State == StateOff || st.LimitUSD != 100 || st.LimitTokens != 33_000_000 || st.Source != "builtin" {
				t.Fatalf("supervisor budget with a refused file: %+v", st)
			}
			if got := MaxModel("supervisor", o); got != "sonnet" {
				t.Fatalf("supervisor ceiling = %q", got)
			}
			// The same answer as no project file at all.
			none, _ := Evaluate("supervisor", Options{StateDir: dir})
			if none.LimitUSD != st.LimitUSD || none.LimitTokens != st.LimitTokens {
				t.Fatalf("refused %+v differs from absent %+v", st, none)
			}
			if len(st.Warnings) == 0 {
				t.Fatal("no warning for a refused file")
			}
		})
	}
}

func TestRefusedProjectWarningsCarryNoPath(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a mode 000 file and a non-root user")
	}
	proj := writeProject(t, "agent_budgets:\n  backend: 5\n")
	if err := os.Chmod(filepath.Join(proj, ".yakos.yml"), 0); err != nil {
		t.Fatal(err)
	}
	st, err := Evaluate("backend", Options{StateDir: t.TempDir(), Project: proj})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Warnings) == 0 {
		t.Fatal("no warning")
	}
	for _, w := range st.Warnings {
		if strings.Contains(w, proj) || strings.Contains(w, string(filepath.Separator)) {
			t.Fatalf("warning carries a path: %q", w)
		}
	}
}
