package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every executable hook source under lib/hooks must be mode +x. A 0644 hook
// deploys (or used to deploy) as a script that exits 126, which Claude Code
// treats as non-blocking: a silent fail-open. lib/hooks/lib/ holds sourced
// helpers and is exempt from the check (but kept executable for consistency).
func TestLibHooksScriptsAreExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on Windows")
	}
	root := filepath.Join(repoRootForParity(t), "lib", "hooks")
	var bad []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".sh") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if strings.HasPrefix(filepath.ToSlash(rel), "lib/") {
			return nil
		}
		if fi, ierr := os.Stat(p); ierr == nil && fi.Mode().Perm()&0o111 == 0 {
			bad = append(bad, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("lib/hooks scripts not executable (git update-index --chmod=+x): %v", bad)
	}
}
