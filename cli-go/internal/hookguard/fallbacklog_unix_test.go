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
