package budget

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

const staleLock = 10 * time.Second

// acquire takes the budget lock: an exclusively created file, so it behaves
// the same on every OS (no flock/LockFileEx split). A lock older than
// staleLock belongs to a crashed process and is broken.
func acquire(dir string, wait time.Duration) (func(), error) {
	if err := statepath.SecureDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, lockFileName)
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) && !lockBusy(err) {
			return nil, err
		}
		// Lstat: a dangling lock symlink must be broken as stale, not waited on.
		if fi, serr := os.Lstat(path); serr == nil && time.Since(fi.ModTime()) > staleLock || serr == nil && fi.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("budget: lock %s busy", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// atomicWrite replaces path with data via a 0600 temp file and a rename.
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".budget-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := statepath.SecureFile(f); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func writeJSON(dir, name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, name), data)
}
