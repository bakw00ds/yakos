//go:build windows

package install

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// symlinkFile attempts to create a symlink from link → target on Windows.
//
// Windows symlinks require Developer Mode or elevated privileges. If
// os.Symlink fails, this function falls back:
//
//   - Directories: create an NTFS junction via `cmd /c mklink /J`. The
//     junctionFn field on Config can override the shell call for tests.
//   - Files: copy the file to link (binary copy). The operator is warned
//     that the result is a copy and that `yakos update` is needed to refresh.
//
// The junctionFn signature is func(target, link string) error.
func symlinkFile(target, link string, junctionFn func(target, link string) error) error {
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
