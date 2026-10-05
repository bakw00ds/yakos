//go:build !windows

package routerpolicy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeInfo is a FileInfo whose Sys() reports an arbitrary owner, so the owner
// check can be exercised without a second user or root.
type fakeInfo struct {
	mode os.FileMode
	sys  any
}

func (fakeInfo) Name() string        { return FileName }
func (fakeInfo) Size() int64         { return 0 }
func (f fakeInfo) Mode() os.FileMode { return f.mode }
func (fakeInfo) ModTime() time.Time  { return time.Time{} }
func (fakeInfo) IsDir() bool         { return false }
func (f fakeInfo) Sys() any          { return f.sys }

// TestCheckInfo_OwnedByAnotherUserIsUntrusted: a policy file that another user
// owns is not the operator's. Every other check passes here (regular file, 0600),
// so only the ownership check can refuse it.
func TestCheckInfo_OwnedByAnotherUserIsUntrusted(t *testing.T) {
	mine := uint32(os.Geteuid())
	path := filepath.Join(t.TempDir(), FileName)

	err := checkInfo(path, fakeInfo{mode: 0o600, sys: &syscall.Stat_t{Uid: mine + 1}})
	if !errors.Is(err, ErrUntrusted) || err == nil || !strings.Contains(err.Error(), "owned by another user") {
		t.Fatalf("a file owned by uid %d must be untrusted (we are %d), got %v", mine+1, mine, err)
	}
	// Root-owned is another user too, whoever is running the test.
	if mine != 0 {
		if err := checkInfo(path, fakeInfo{mode: 0o600, sys: &syscall.Stat_t{Uid: 0}}); !errors.Is(err, ErrUntrusted) {
			t.Errorf("a root-owned file must be untrusted for uid %d, got %v", mine, err)
		}
	}
	// The same file owned by the current user passes.
	if err := checkInfo(path, fakeInfo{mode: 0o600, sys: &syscall.Stat_t{Uid: mine}}); err != nil {
		t.Errorf("a file owned by the current user must pass, got %v", err)
	}
}

// TestLoad_FileSwappedAfterTheLstatIsRefused: the path is vetted with Lstat, then
// opened. If another file takes its place in between, the file read is not the
// file vetted. The replacement here is a regular file of the same owner and
// mode, so only the same-file comparison on the open descriptor refuses it; the
// symlink, owner and mode checks all pass.
func TestLoad_FileSwappedAfterTheLstatIsRefused(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := t.TempDir()
	writePolicy(t, dir, "allow_unsandboxed_runtimes: []\n", 0o600)
	other := filepath.Join(dir, "other.yml")
	if err := os.WriteFile(other, []byte("allow_unsandboxed_runtimes: [codex]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev := afterLstat
	afterLstat = func(path string) {
		if err := os.Rename(other, path); err != nil {
			t.Errorf("swap failed: %v", err)
		}
	}
	t.Cleanup(func() { afterLstat = prev })

	p, err := Load(dir)
	if !errors.Is(err, ErrUntrusted) || err == nil || !strings.Contains(err.Error(), "changed while being read") {
		t.Fatalf("a file swapped after the Lstat must be refused, got policy %+v, err %v", p, err)
	}
	if len(p.AllowUnsandboxedRuntimes) != 0 {
		t.Errorf("a refused file must yield an empty policy, got %+v", p)
	}
	afterLstat = prev
	// The swap left the second file in place: read normally it is a fine policy,
	// which shows the refusal above came from the swap, not from the content.
	if ok, err := AllowsUnsandboxed(dir, "codex"); err != nil || !ok {
		t.Errorf("control: the swapped-in file is a valid policy on its own, got ok=%v err=%v", ok, err)
	}
}
