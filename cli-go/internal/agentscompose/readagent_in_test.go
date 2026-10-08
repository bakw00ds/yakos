package agentscompose

// readagent_in_test.go: the two readers `yakos validate` and other tree walkers
// use (K-167): ReadAgentFileIn, which holds the symlink roots, and ReadRegularFile,
// which holds none. Each refuses an out-of-root link (the first), a file over the
// cap, and a non-regular file, and each reads the entry it inspected.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAgentFileIn_RefusesWhatComposeRefuses(t *testing.T) {
	agents := t.TempDir()
	outside := filepath.Join(t.TempDir(), "credentials")
	writeFileT(t, outside, secretText+"\n")
	symlinkOrSkip(t, outside, filepath.Join(agents, "linked.md"))
	if err := os.WriteFile(filepath.Join(agents, "huge.md"), make([]byte, MaxAgentFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, filepath.Join(agents, "ok.md"), "fine\n")
	roots := []string{agents}
	for _, name := range []string{"linked.md", "huge.md"} {
		data, err := ReadAgentFileIn(filepath.Join(agents, name), roots)
		if !errors.Is(err, ErrRefused) || len(data) != 0 {
			t.Errorf("%s: data %q err %v, want ErrRefused and no data", name, data, err)
		}
		if err != nil && strings.Contains(err.Error(), agents) {
			t.Errorf("%s: the error carries a path: %v", name, err)
		}
	}
	data, err := ReadAgentFileIn(filepath.Join(agents, "ok.md"), roots)
	if err != nil || string(data) != "fine\n" {
		t.Errorf("ok.md: %q %v", data, err)
	}
}

func TestReadAgentFileIn_LinkInsideRootsIsFollowed(t *testing.T) {
	agents := t.TempDir()
	writeFileT(t, filepath.Join(agents, "real.md"), "REAL\n")
	symlinkOrSkip(t, "real.md", filepath.Join(agents, "alias.md"))
	data, err := ReadAgentFileIn(filepath.Join(agents, "alias.md"), []string{agents})
	if err != nil || string(data) != "REAL\n" {
		t.Errorf("%q %v", data, err)
	}
}

func TestReadRegularFile_FollowsLinksButRefusesSizeAndType(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	writeFileT(t, target, "ELSEWHERE\n")
	symlinkOrSkip(t, target, filepath.Join(dir, "alias.md"))
	data, err := ReadRegularFile(filepath.Join(dir, "alias.md"))
	if err != nil || string(data) != "ELSEWHERE\n" {
		t.Errorf("a link to a regular file: %q %v", data, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "huge.md"), make([]byte, MaxAgentFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFile(filepath.Join(dir, "huge.md")); !errors.Is(err, ErrRefused) {
		t.Errorf("huge file: %v, want ErrRefused", err)
	}
	// exactly the cap is read
	if err := os.WriteFile(filepath.Join(dir, "cap.md"), make([]byte, MaxAgentFileBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, err := ReadRegularFile(filepath.Join(dir, "cap.md")); err != nil || len(d) != MaxAgentFileBytes {
		t.Errorf("a file of exactly the cap: %d bytes, %v", len(d), err)
	}
	if _, err := ReadRegularFile(dir); !errors.Is(err, ErrRefused) {
		t.Errorf("a directory: %v, want ErrRefused", err)
	}
	if _, err := ReadRegularFile(filepath.Join(dir, "missing.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing file: %v, want ErrNotExist", err)
	}
}
