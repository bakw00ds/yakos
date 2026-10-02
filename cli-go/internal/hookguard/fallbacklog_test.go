package hookguard

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runFallbackIn runs the fallback command for a fake binary that exits rc, with
// the state dir pinned to state (YAKOS_DISPATCH_LOG) and HOME at home.
func runFallbackIn(t *testing.T, shell, state, home, rc string) (int, string) {
	t.Helper()
	proj := t.TempDir()
	bin := filepath.Join(t.TempDir(), "yakos")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\nexit "+rc+"\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	cmd := exec.Command(shell, "-c", BuildFallback(bin, "supervisor-stream")) //nolint:gosec
	env := append(os.Environ(), "CLAUDE_PROJECT_DIR="+proj, "HOME="+home)
	if state != "" {
		env = append(env, "YAKOS_DISPATCH_LOG="+state)
	}
	cmd.Env = env
	cmd.Stdin = strings.NewReader("{}")
	var se bytes.Buffer
	cmd.Stderr = &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, se.String()
}

func TestFallbackWritesOneLinePerFailureAndStillExitsZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	for _, shell := range shells() {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "state") // does not exist yet
			for _, rc := range []string{"2", "137"} {
				if code, se := runFallbackIn(t, shell, state, t.TempDir(), rc); code != 0 || !strings.Contains(se, "WARN yakos exited "+rc) {
					t.Fatalf("rc %s: want exit 0 + WARN, got %d %q", rc, code, se)
				}
			}
			path := filepath.Join(state, FallbackLogName)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			if len(lines) != 2 || !strings.HasSuffix(lines[0], " supervisor-stream rc=2") || !strings.HasSuffix(lines[1], " supervisor-stream rc=137") {
				t.Fatalf("want one line per failure, got %q", data)
			}
			if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
				t.Errorf("log mode = %v, want 0600", fi.Mode().Perm())
			}
			// A success writes nothing.
			if code, _ := runFallbackIn(t, shell, state, t.TempDir(), "0"); code != 0 {
				t.Fatalf("rc 0 exit %d", code)
			}
			if after, _ := os.ReadFile(path); string(after) != string(data) {
				t.Error("a successful run must not log")
			}
		})
	}
}

func TestFallbackLogDefaultsToHomeStateAndSurvivesUnwritableState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	shell := shells()[0]
	home := t.TempDir()
	if code, _ := runFallbackIn(t, shell, "", home, "1"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".yakos-state", FallbackLogName)); err != nil || !strings.Contains(string(b), "rc=1") {
		t.Fatalf("default location not used: %q %v", b, err)
	}
	// State dir is a regular file: the log write fails, the hook still exits 0.
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)
	if code, se := runFallbackIn(t, shell, blocker, t.TempDir(), "1"); code != 0 || !strings.Contains(se, "WARN") {
		t.Fatalf("unwritable state changed the outcome: %d %q", code, se)
	}
	// A log past 64 KiB stops growing.
	state := t.TempDir()
	big := strings.Repeat("x", 70000)
	_ = os.WriteFile(filepath.Join(state, FallbackLogName), []byte(big), 0o600)
	runFallbackIn(t, shell, state, t.TempDir(), "1")
	if b, _ := os.ReadFile(filepath.Join(state, FallbackLogName)); len(b) != len(big) {
		t.Errorf("oversized log grew to %d", len(b))
	}
}

func TestFallbackLogsUnusableBinaryAndStillRunsTheTwin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX sh")
	}
	for _, shell := range shells() {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			proj := t.TempDir()
			hooks := filepath.Join(proj, "scripts", "hooks")
			_ = os.MkdirAll(hooks, 0o755)                                                                                                           //nolint:gosec
			_ = os.WriteFile(filepath.Join(hooks, "supervisor-stream.sh"), []byte("#!/bin/sh\ncat >/dev/null\necho twin-ran >&2\nexit 0\n"), 0o755) //nolint:gosec
			state := filepath.Join(t.TempDir(), "state")
			cmd := exec.Command(shell, "-c", BuildFallback(filepath.Join(t.TempDir(), "missing"), "supervisor-stream")) //nolint:gosec
			cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+proj, "YAKOS_DISPATCH_LOG="+state, "HOME="+t.TempDir())
			cmd.Stdin = strings.NewReader("{}")
			var se bytes.Buffer
			cmd.Stderr = &se
			if err := cmd.Run(); err != nil || !strings.Contains(se.String(), "twin-ran") {
				t.Fatalf("want the twin and exit 0, got %v %q", err, se.String())
			}
			b, err := os.ReadFile(filepath.Join(state, FallbackLogName))
			if err != nil || !strings.HasSuffix(strings.TrimSpace(string(b)), " supervisor-stream reason=unusable") {
				t.Fatalf("unusable fallback not logged: %q %v", b, err)
			}
			if fi, _ := os.Stat(filepath.Join(state, FallbackLogName)); fi.Mode().Perm() != 0o600 {
				t.Errorf("mode %v", fi.Mode().Perm())
			}
		})
	}
}
