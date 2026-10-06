//go:build !windows

package statepath

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The owner tests above replace the ownedBy seam, so none of them would notice the
// seam's default being replaced by a predicate that is always true (or
// ownedByCurrentUser being broken): the trust check would pass every file and every
// test would stay green. These run the real predicate against a path another user
// really owns. The only such paths a test can use are the system's: root's.

// rootOwnedFile finds a regular file owned by uid 0 whose directory is a real
// directory (not a symlink) owned by uid 0 and not group- or world-writable, so the
// owner of the directory is the only reason the trust check can refuse it.
func rootOwnedFile(t *testing.T) (file, dir string) {
	t.Helper()
	for _, f := range []string{
		"/usr/bin/env", "/bin/ls", "/usr/bin/true", "/bin/sh", "/etc/hosts", "/etc/passwd",
		"/usr/lib/os-release", "/System/Library/CoreServices/SystemVersion.plist",
	} {
		fi, err := os.Lstat(f)
		if err != nil || !fi.Mode().IsRegular() || uidOf(fi) != 0 {
			continue
		}
		d := filepath.Dir(f)
		di, err := os.Lstat(d)
		if err != nil || di.Mode()&os.ModeSymlink != 0 || !di.IsDir() || uidOf(di) != 0 || di.Mode().Perm()&0o022 != 0 {
			continue
		}
		return f, d
	}
	if os.Getenv("CI") != "" {
		t.Fatal("no root-owned system file found on this runner: the owner check would go untested")
	}
	t.Skip("no root-owned system file with a root-owned, non-writable directory on this machine")
	return "", ""
}

func uidOf(fi os.FileInfo) int {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return -1
	}
	return int(st.Uid)
}

// A path another user owns is refused, with the real predicate. Mutate
// `var ownedBy = ownedByCurrentUser` to a function that returns true, or break
// ownedByCurrentUser, and this goes red.
func TestReadTrusted_RefusesAPathAnotherUserReallyOwns(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a root-owned path is this user's")
	}
	file, dir := rootOwnedFile(t)

	data, err := ReadTrusted(file, 16)
	if err == nil {
		t.Fatalf("read %q from %s, which is in a directory root owns", data, file)
	}
	var u *UntrustedError
	if !errors.As(err, &u) || u.Reason != "is owned by another user" {
		t.Fatalf("err = %v, want an untrusted error saying the path is owned by another user", err)
	}
	if u.Path != dir {
		t.Errorf("the refusal names %s, want the directory %s (the directory half runs first)", u.Path, dir)
	}
}

// The same predicate, on its own: a file this user made is theirs, root's is not.
func TestOwnedByCurrentUser_SeesTheRealOwner(t *testing.T) {
	mine := filepath.Join(t.TempDir(), "mine")
	if err := os.WriteFile(mine, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(mine)
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCurrentUser(fi) || !ownedBy(fi) {
		t.Error("a file this user just created is not seen as theirs")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: nothing is owned by another user")
	}
	file, _ := rootOwnedFile(t)
	rfi, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if ownedByCurrentUser(rfi) || ownedBy(rfi) {
		t.Errorf("%s is owned by uid 0 and was taken for this user's (euid %d)", file, os.Geteuid())
	}
}

// fakeStat is a FileInfo whose owner is whatever the test says, so the predicate
// can be exercised for any uid without a second account.
type fakeStat struct{ uid uint32 }

func (fakeStat) Name() string       { return "x" }
func (fakeStat) Size() int64        { return 0 }
func (fakeStat) Mode() os.FileMode  { return 0o600 }
func (fakeStat) ModTime() time.Time { return time.Time{} }
func (fakeStat) IsDir() bool        { return false }
func (f fakeStat) Sys() any         { return &syscall.Stat_t{Uid: f.uid} }

func TestOwnedByCurrentUser_ComparesTheUID(t *testing.T) {
	me := uint32(os.Geteuid())
	if !ownedByCurrentUser(fakeStat{uid: me}) {
		t.Errorf("uid %d (the effective uid) is not this user's", me)
	}
	for _, other := range []uint32{me + 1, me + 1000, 0} {
		if other == me {
			continue
		}
		if ownedByCurrentUser(fakeStat{uid: other}) {
			t.Errorf("uid %d was taken for this user's (euid %d)", other, me)
		}
	}
}
