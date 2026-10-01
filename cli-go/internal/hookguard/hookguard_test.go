package hookguard

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStripRoundTripAndTamper(t *testing.T) {
	for _, bin := range []string{"/opt/yakos/bin/yakos", "/Users/a b/bin/yakos", "/it's/yakos"} {
		c := Build(bin, "secret-scan")
		plain, ok := Strip(c)
		if !ok || plain != Plain(bin, "secret-scan") {
			t.Fatalf("Strip(%q) = %q, %v", c, plain, ok)
		}
		// Any edit to the wrapper is not a recognized guard.
		for _, bad := range []string{
			strings.Replace(c, "exit 2;", "exit 0;", 1),
			strings.Replace(c, "[ -s ", "[ -r ", 1),
			strings.Replace(c, "scripts/hooks/secret-scan.sh", "scripts/hooks/other.sh", 1),
			c + " ; true",
		} {
			if _, ok := Strip(bad); ok {
				t.Errorf("tampered guard accepted: %s", bad)
			}
		}
	}
	if _, ok := Strip(Plain("/x/yakos", "path-log")); ok {
		t.Error("a plain command is not a guard")
	}
}

// run executes the guarded command under shell with a fake yakos at bin and a
// bash twin that exits 2 saying so. It returns exit code, stdout, stderr.
func run(t *testing.T, shell, bin, stdin string, twin bool) (int, string, string) {
	t.Helper()
	proj := t.TempDir()
	hooks := filepath.Join(proj, "scripts", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if twin {
		body := "#!/bin/sh\ncat >/dev/null\necho twin-ran >&2\nexit 2\n"
		if err := os.WriteFile(filepath.Join(hooks, "secret-scan.sh"), []byte(body), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	cmd := exec.Command(shell, "-c", Build(bin, "secret-scan")) //nolint:gosec
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+proj)
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, so.String(), se.String()
}

func shells() []string {
	var out []string
	for _, s := range []string{"sh", "bash", "zsh"} {
		if p, err := exec.LookPath(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func TestGuardBehaviour(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	fake := func(t *testing.T, body string, mode os.FileMode) string {
		p := filepath.Join(t.TempDir(), "yakos")
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, shell := range shells() {
		shell := shell
		t.Run(filepath.Base(shell), func(t *testing.T) {
			// Unusable binary: fall back to the bash twin, which blocks (2).
			fallbacks := map[string]string{
				"missing":        filepath.Join(t.TempDir(), "nope"),
				"directory":      t.TempDir(),
				"empty exec":     fake(t, "", 0o755),
				"not executable": fake(t, "#!/bin/sh\nexit 0\n", 0o644),
			}
			for name, bin := range fallbacks {
				code, _, se := run(t, shell, bin, "{}", true)
				if code != 2 || !strings.Contains(se, "twin-ran") {
					t.Errorf("%s: want the bash twin (exit 2), got %d %q", name, code, se)
				}
			}
			// A binary that runs but dies: exit 2 with a reason, never 0.
			for _, rc := range []string{"1", "126", "127", "134", "137", "139", "255"} {
				bin := fake(t, "#!/bin/sh\ncat >/dev/null\nexit "+rc+"\n", 0o755)
				code, _, se := run(t, shell, bin, "{}", true)
				if code != 2 || !strings.Contains(se, "yakos exited "+rc) {
					t.Errorf("rc %s: want exit 2 + reason, got %d %q", rc, code, se)
				}
			}
			// A real signal kill.
			bin := fake(t, "#!/bin/sh\nkill -9 $$\n", 0o755)
			if code, _, se := run(t, shell, bin, "{}", true); code != 2 || !strings.Contains(se, "yakos exited") {
				t.Errorf("SIGKILL: want exit 2 + reason, got %d %q", code, se)
			}
			// 0 and 2 pass through, with stdin delivered and output kept.
			bin = fake(t, "#!/bin/sh\ncat\necho out-err >&2\nexit 0\n", 0o755)
			if code, so, se := run(t, shell, bin, "payload-xyz", true); code != 0 || so != "payload-xyz" || !strings.Contains(se, "out-err") {
				t.Errorf("rc 0 passthrough: %d %q %q", code, so, se)
			}
			bin = fake(t, "#!/bin/sh\ncat >/dev/null\necho blocked >&2\nexit 2\n", 0o755)
			if code, _, se := run(t, shell, bin, "{}", false); code != 2 || !strings.Contains(se, "blocked") || strings.Contains(se, "yakos exited") {
				t.Errorf("rc 2 passthrough: %d %q", code, se)
			}
		})
	}
}
