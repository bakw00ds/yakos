//go:build !windows

package hookguard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runFallbackTimed is runFallbackIn with a deadline: a wrapper that blocks on a
// FIFO must fail the test instead of hanging it.
func runFallbackTimed(t *testing.T, shell, state string) (int, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "yakos")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >/dev/null\nexit 1\n"), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-c", BuildFallback(bin, "supervisor-stream")) //nolint:gosec
	cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+t.TempDir(), "HOME="+t.TempDir(), "YAKOS_DISPATCH_LOG="+state)
	cmd.Stdin = strings.NewReader("{}")
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("wrapper hung on a non-regular log file")
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, nil
}

// The wrapper must not append through a symlink, create a dangling link's
// target, or block on a FIFO, and must still exit 0.
func TestFallbackLogRefusesNonRegularFiles(t *testing.T) {
	for _, shell := range shells() {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			// symlink to an outside file: target unchanged
			state := t.TempDir()
			outside := filepath.Join(t.TempDir(), "important")
			if err := os.WriteFile(outside, []byte("keep me\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(state, FallbackLogName)
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			if code, _ := runFallbackTimed(t, shell, state); code != 0 {
				t.Fatalf("symlink: exit %d", code)
			}
			if b, _ := os.ReadFile(outside); string(b) != "keep me\n" {
				t.Errorf("wrapper wrote through a symlink: %q", b)
			}

			// dangling symlink: the target must not be created
			state = t.TempDir()
			target := filepath.Join(t.TempDir(), "created-by-append")
			if err := os.Symlink(target, filepath.Join(state, FallbackLogName)); err != nil {
				t.Fatal(err)
			}
			if code, _ := runFallbackTimed(t, shell, state); code != 0 {
				t.Fatalf("dangling: exit %d", code)
			}
			if _, err := os.Lstat(target); err == nil {
				t.Error("wrapper created the target of a dangling symlink")
			}

			// symlink to /dev/zero: nothing is written, no hang
			state = t.TempDir()
			if err := os.Symlink("/dev/zero", filepath.Join(state, FallbackLogName)); err != nil {
				t.Fatal(err)
			}
			if code, _ := runFallbackTimed(t, shell, state); code != 0 {
				t.Fatalf("/dev/zero: exit %d", code)
			}

			// FIFO: wc must never touch it, the wrapper returns promptly
			state = t.TempDir()
			if err := syscall.Mkfifo(filepath.Join(state, FallbackLogName), 0o600); err != nil {
				t.Fatal(err)
			}
			if code, _ := runFallbackTimed(t, shell, state); code != 0 {
				t.Fatalf("fifo: exit %d", code)
			}
		})
	}
}

// On the unusable-binary path the log write happens BEFORE the bash twin is
// exec'd, so a blocked or misdirected write would skip supervision entirely.
// A FIFO, a symlink and a dangling symlink at the log path must neither hang
// nor write, and the twin must still run promptly.
func TestUnusableBinaryPathStillRunsTwinWithNonRegularLog(t *testing.T) {
	for _, shell := range shells() {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			for name, mk := range map[string]func(t *testing.T, log string) (check func()){
				"fifo": func(t *testing.T, log string) func() {
					if err := syscall.Mkfifo(log, 0o600); err != nil {
						t.Fatal(err)
					}
					return func() {}
				},
				"symlink": func(t *testing.T, log string) func() {
					outside := filepath.Join(t.TempDir(), "important")
					_ = os.WriteFile(outside, []byte("keep me\n"), 0o600)
					if err := os.Symlink(outside, log); err != nil {
						t.Fatal(err)
					}
					return func() {
						if b, _ := os.ReadFile(outside); string(b) != "keep me\n" {
							t.Errorf("symlink target written: %q", b)
						}
					}
				},
				"dangling": func(t *testing.T, log string) func() {
					target := filepath.Join(t.TempDir(), "created")
					if err := os.Symlink(target, log); err != nil {
						t.Fatal(err)
					}
					return func() {
						if _, err := os.Lstat(target); err == nil {
							t.Error("dangling target created")
						}
					}
				},
			} {
				state := t.TempDir()
				check := mk(t, filepath.Join(state, FallbackLogName))
				proj := t.TempDir()
				hooks := filepath.Join(proj, "scripts", "hooks")
				_ = os.MkdirAll(hooks, 0o755)                                                                                                           //nolint:gosec
				_ = os.WriteFile(filepath.Join(hooks, "supervisor-stream.sh"), []byte("#!/bin/sh\ncat >/dev/null\necho twin-ran >&2\nexit 0\n"), 0o755) //nolint:gosec
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				cmd := exec.CommandContext(ctx, shell, "-c", BuildFallback(filepath.Join(t.TempDir(), "missing"), "supervisor-stream")) //nolint:gosec
				cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+proj, "HOME="+t.TempDir(), "YAKOS_DISPATCH_LOG="+state)
				cmd.Stdin = strings.NewReader("{}")
				var se strings.Builder
				cmd.Stderr = &se
				err := cmd.Run()
				hung := ctx.Err() != nil
				cancel()
				if hung {
					t.Fatalf("%s: unusable-binary path hung before the bash twin ran", name)
				}
				if err != nil || !strings.Contains(se.String(), "twin-ran") {
					t.Errorf("%s: want the twin to run and exit 0, got %v %q", name, err, se.String())
				}
				check()
			}
		})
	}
}
