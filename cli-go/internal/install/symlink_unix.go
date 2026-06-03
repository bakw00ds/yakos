//go:build !windows

package install

import "os"

// symlinkFile creates a symlink from link → target on Unix.
// On Unix both file and directory symlinks are handled identically by os.Symlink.
func symlinkFile(target, link string, junctionFn func(target, link string) error) error {
	return os.Symlink(target, link)
}
