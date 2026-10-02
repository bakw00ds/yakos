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

func TestFallbackStripRoundTripAndTamper(t *testing.T) {
	for _, bin := range []string{"/opt/yakos/bin/yakos", "/Users/a b/bin/yakos", "/it's/yakos"} {
		c := BuildFallback(bin, "supervisor-stream")
		plain, ok := Strip(c)
		if !ok || plain != Plain(bin, "supervisor-stream") {
			t.Fatalf("Strip(%q) = %q, %v", c, plain, ok)
		}
		if c == Build(bin, "supervisor-stream") {
			t.Fatal("fallback wrapper must differ from the fail-closed one")
		}
		for _, bad := range []string{
			strings.Replace(c, "exit 0; fi; ", "exit 2; fi; ", 1),
			strings.Replace(c, "scripts/hooks/supervisor-stream.sh", "scripts/hooks/other.sh", 1),
			strings.Replace(c, "exit 0; else", "exit 2; else", 1),
			c + " ; true",
		} {
			if _, ok := Strip(bad); ok {
				t.Errorf("tampered fallback accepted: %s", bad)
			}
		}
	}
}

// runFallback executes the fallback command with a fake binary at bin and a
// bash twin that records that it ran and exits 0.
func runFallback(t *testing.T, shell, bin, stdin string, twin bool) (int, string, string) {
	t.Helper()
	proj := t.TempDir()
	hooks := filepath.Join(proj, "scripts", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if twin {
		body := "#!/bin/sh\ncat >/dev/null\necho twin-ran >&2\nexit 0\n"
		if err := os.WriteFile(filepath.Join(hooks, "supervisor-stream.sh"), []byte(body), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	cmd := exec.Command(shell, "-c", BuildFallback(bin, "supervisor-stream")) //nolint:gosec
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

func TestFallbackBehaviour(t *testing.T) {
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
			// Unusable binary: the bash twin runs, supervision is not skipped.
			unusable := map[string]string{
				"missing":        filepath.Join(t.TempDir(), "nope"),
				"directory":      t.TempDir(),
				"empty exec":     fake(t, "", 0o755),
				"not executable": fake(t, "#!/bin/sh\nexit 0\n", 0o644),
			}
			for name, bin := range unusable {
				code, _, se := runFallback(t, shell, bin, "{}", true)
				if code != 0 || !strings.Contains(se, "twin-ran") {
					t.Errorf("%s: want the bash twin and exit 0, got %d %q", name, code, se)
				}
			}
			// A binary that runs and fails, panics (2), is stale (unknown
			// command) or is killed: never a non-zero exit, always a warning.
			for _, rc := range []string{"1", "2", "126", "127", "134", "137", "139", "255"} {
				bin := fake(t, "#!/bin/sh\ncat >/dev/null\nexit "+rc+"\n", 0o755)
				code, _, se := runFallback(t, shell, bin, "{}", true)
				if code != 0 || !strings.Contains(se, "WARN yakos exited "+rc) || strings.Contains(se, "twin-ran") {
					t.Errorf("rc %s: want exit 0 + WARN, got %d %q", rc, code, se)
				}
			}
			bin := fake(t, "#!/bin/sh\nkill -9 $$\n", 0o755)
			if code, _, se := runFallback(t, shell, bin, "{}", true); code != 0 || !strings.Contains(se, "WARN yakos exited") {
				t.Errorf("SIGKILL: want exit 0 + WARN, got %d %q", code, se)
			}
			// Stale binary: an old yakos prints usage and exits non-zero.
			bin = fake(t, "#!/bin/sh\ncat >/dev/null\necho 'unknown command \"hook\"' >&2\nexit 1\n", 0o755)
			if code, _, se := runFallback(t, shell, bin, "{}", true); code != 0 || !strings.Contains(se, "WARN") {
				t.Errorf("stale: want exit 0 + WARN, got %d %q", code, se)
			}
			// Success passes through with stdin delivered and output kept.
			bin = fake(t, "#!/bin/sh\ncat\necho out-err >&2\nexit 0\n", 0o755)
			if code, so, se := runFallback(t, shell, bin, "payload-xyz", true); code != 0 || so != "payload-xyz" || !strings.Contains(se, "out-err") || strings.Contains(se, "WARN") {
				t.Errorf("rc 0 passthrough: %d %q %q", code, so, se)
			}
		})
	}
}
