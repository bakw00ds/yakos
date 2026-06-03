//go:build windows

package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// devModeOnce guards the one-time DevMode probe.
var devModeOnce sync.Once

// devModeEnabled caches the result of the DevMode probe after the first call.
var devModeEnabled bool

// devModeProbeFn is the function used to detect whether Windows Developer Mode
// is active (i.e. whether os.Symlink succeeds for a regular user).
// It is a package-level variable so tests can inject a replacement without
// touching the real filesystem and without needing elevated privileges.
//
// The default implementation creates a throwaway symlink in os.TempDir() and
// removes it immediately.  The result is cached via devModeOnce so the probe
// runs at most once per install invocation.
var devModeProbeFn func() bool = probeDevMode

// probeDevMode is the real DevMode probe.  It attempts os.Symlink on a
// throwaway path in os.TempDir(); if that succeeds Windows Developer Mode (or
// elevated privileges) is available.
func probeDevMode() bool {
	tmp := os.TempDir()
	link := filepath.Join(tmp, ".yakos-devmode-probe")
	target := filepath.Join(tmp, ".yakos-devmode-target")

	// Write a throwaway target file so the symlink has something to point at.
	if err := os.WriteFile(target, []byte("probe"), 0600); err != nil {
		return false
	}
	defer func() {
		_ = os.Remove(target)
		_ = os.Remove(link)
	}()

	// Clean up any stale probe link from a previous crashed run.
	_ = os.Remove(link)

	if err := os.Symlink(target, link); err != nil {
		return false
	}
	return true
}

// isDevModeEnabled returns true when the Windows Developer Mode probe
// succeeds.  The probe runs at most once per process lifetime.
func isDevModeEnabled() bool {
	devModeOnce.Do(func() {
		devModeEnabled = devModeProbeFn()
	})
	return devModeEnabled
}

// resetDevModeProbe resets the package-level DevMode cache.  It is exported
// only for tests (via export_test.go) so each test can choose its own probe
// outcome without cross-test pollution.
func resetDevModeProbe(probeFn func() bool) {
	devModeOnce = sync.Once{}
	devModeProbeFn = probeFn
}

// symlinkFile attempts to create a symlink from link → target on Windows.
//
// Windows symlinks require Developer Mode or elevated privileges. If
// os.Symlink fails, this function falls back:
//
//   - When DevMode is detected (real symlinks work): os.Symlink is used directly
//     for all operations after the probe succeeds.
//   - Directories: create an NTFS junction via `cmd /c mklink /J`. The
//     junctionFn field on Config can override the shell call for tests.
//   - Files: copy the file to link (binary copy). The operator is warned
//     that the result is a copy and that `yakos update` is needed to refresh.
//
// The junctionFn signature is func(target, link string) error.
func symlinkFile(target, link string, junctionFn func(target, link string) error) error {
	// When DevMode is active, real symlinks succeed — use os.Symlink directly
	// instead of going through the fallback path.
	if isDevModeEnabled() {
		return os.Symlink(target, link)
	}

	// DevMode not available: attempt os.Symlink anyway (it may succeed if the
	// process is elevated), and fall back on failure.
	if err := os.Symlink(target, link); err == nil {
		return nil
	}

	fi, statErr := os.Stat(target)
	if statErr != nil {
		// Cannot stat target; give up and return the original symlink error.
		return fmt.Errorf("windows symlink fallback: target %q not found: %w", target, statErr)
	}

	if fi.IsDir() {
		// Directory → NTFS junction.
		if junctionFn != nil {
			return junctionFn(target, link)
		}
		return defaultJunction(target, link)
	}

	// File → copy fallback (warn separately in install.go).
	return copyFile(target, link)
}

// defaultJunction creates an NTFS directory junction using cmd /c mklink /J.
func defaultJunction(target, link string) error {
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target) //nolint:gosec
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %q %q: %w (output: %s)", link, target, err, string(out))
	}
	return nil
}

// copyFile copies src to dst, creating dst. Used as file-symlink fallback on Windows.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
