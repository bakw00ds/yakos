package statepath

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func setKey(key, val string) func(*yaml.Node) error {
	return func(top *yaml.Node) error {
		YAMLSet(top, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val})
		return nil
	}
}

func newState(t *testing.T) (dir, file string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "state")
	return dir, filepath.Join(dir, "policy.yml")
}

func TestEditYAML_CreatesPrivateFileAndLeavesNoDebris(t *testing.T) {
	dir, file := newState(t)
	res, err := EditYAML(file, 1<<16, setKey("a", "1"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || res.SHABefore != "" || len(res.SHAAfter) != 64 {
		t.Fatalf("result = %+v", res)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %o, want 0600", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %o, want 0700", fi.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("state dir holds %d entries, want only the file: %v", len(entries), entries)
	}
}

func TestEditYAML_KeepsKeysAndCommentsItDidNotTouch(t *testing.T) {
	_, file := newState(t)
	if err := SecureDir(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	orig := "# keep this note\nallow_unsandboxed_runtimes: [codex]\nhooks_endpoint: true\nunknown_future_key: {x: 1}\n"
	if err := os.WriteFile(file, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EditYAML(file, 1<<16, setKey("rules_marker", "m"), nil); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(file)
	for _, want := range []string{"# keep this note", "allow_unsandboxed_runtimes:", "hooks_endpoint: true", "unknown_future_key:", "rules_marker: m"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("result lost %q:\n%s", want, got)
		}
	}
}

func TestEditYAML_NoChangeWritesNothing(t *testing.T) {
	_, file := newState(t)
	if _, err := EditYAML(file, 1<<16, setKey("a", "1"), nil); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(file)
	time.Sleep(20 * time.Millisecond)
	res, err := EditYAML(file, 1<<16, setKey("a", "1"), nil)
	if err != nil || res.Changed || res.SHABefore != res.SHAAfter {
		t.Fatalf("res=%+v err=%v; want unchanged", res, err)
	}
	after, _ := os.Stat(file)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an unchanged edit rewrote the file")
	}
}

func TestEditYAML_RefusedCheckAndFailedEditLeaveTheFileAlone(t *testing.T) {
	_, file := newState(t)
	if _, err := EditYAML(file, 1<<16, setKey("a", "1"), nil); err != nil {
		t.Fatal(err)
	}
	orig, _ := os.ReadFile(file)
	if _, err := EditYAML(file, 1<<16, setKey("a", "2"), func([]byte) error { return errors.New("no") }); err == nil {
		t.Error("check refusal did not fail the edit")
	}
	if _, err := EditYAML(file, 1<<16, func(*yaml.Node) error { return errors.New("bad edit") }, nil); err == nil {
		t.Error("edit error did not fail the edit")
	}
	if now, _ := os.ReadFile(file); string(now) != string(orig) {
		t.Errorf("file changed:\n%s", now)
	}
}

func TestEditYAML_RefusesAnUntrustedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes and symlinks")
	}
	t.Run("symlink", func(t *testing.T) {
		dir, file := newState(t)
		_ = SecureDir(dir)
		target := filepath.Join(t.TempDir(), "elsewhere")
		if err := os.WriteFile(target, []byte("a: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, file); err != nil {
			t.Fatal(err)
		}
		if _, err := EditYAML(file, 1<<16, setKey("b", "2"), nil); err == nil {
			t.Fatal("wrote through a symlink")
		}
		if got, _ := os.ReadFile(target); string(got) != "a: 1\n" {
			t.Errorf("symlink target changed: %q", got)
		}
	})
	t.Run("group-writable", func(t *testing.T) {
		dir, file := newState(t)
		_ = SecureDir(dir)
		if err := os.WriteFile(file, []byte("a: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(file, 0o660); err != nil {
			t.Fatal(err)
		}
		if _, err := EditYAML(file, 1<<16, setKey("b", "2"), nil); err == nil {
			t.Fatal("edited a group-writable file")
		}
	})
	t.Run("other-owner", func(t *testing.T) {
		dir, file := newState(t)
		_ = SecureDir(dir)
		if err := os.WriteFile(file, []byte("a: 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := ownedBy
		ownedBy = func(os.FileInfo) bool { return false }
		defer func() { ownedBy = old }()
		if _, err := EditYAML(file, 1<<16, setKey("b", "2"), nil); err == nil {
			t.Fatal("edited another user's file")
		}
	})
}

func TestEditYAML_RejectsBadInputWithoutNamingThePath(t *testing.T) {
	for name, content := range map[string]string{
		"not yaml":     "a: [unclosed\n",
		"not mapping":  "- a\n- b\n",
		"too large":    "a: " + strings.Repeat("x", 200) + "\n",
		"scalar topic": "just text\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir, file := newState(t)
			_ = SecureDir(dir)
			if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := EditYAML(file, 100, setKey("b", "2"), nil)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), dir) || strings.Contains(err.Error(), file) {
				t.Errorf("error names the path: %v", err)
			}
			if got, _ := os.ReadFile(file); string(got) != content {
				t.Error("file changed")
			}
		})
	}
}

func TestEditYAML_ConcurrentEditsAreAllKept(t *testing.T) {
	_, file := newState(t)
	if err := SecureDir(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := EditYAML(file, 1<<16, setKey(fmt.Sprintf("k%02d", i), "v"), nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(file)
	for i := 0; i < n; i++ {
		if !strings.Contains(string(got), fmt.Sprintf("k%02d: v", i)) {
			t.Errorf("update k%02d lost:\n%s", i, got)
		}
	}
}

func TestEditYAML_LockHandling(t *testing.T) {
	oldWait, oldStale := editLockWait, editLockStale
	editLockWait, editLockStale = 150*time.Millisecond, time.Hour
	defer func() { editLockWait, editLockStale = oldWait, oldStale }()

	dir, file := newState(t)
	_ = SecureDir(dir)
	lock := file + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EditYAML(file, 1<<16, setKey("a", "1"), nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("held lock: err = %v, want ErrBusy", err)
	}
	editLockStale = time.Millisecond // now the holder counts as dead
	time.Sleep(5 * time.Millisecond)
	if _, err := EditYAML(file, 1<<16, setKey("a", "1"), nil); err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
	if _, err := os.Lstat(lock); err == nil {
		t.Error("lock file left behind")
	}
}

func TestYAMLHelpers(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("a: 1\nb: ~\nc: [x]\n"), &doc); err != nil {
		t.Fatal(err)
	}
	top := doc.Content[0]
	if n, ok := YAMLMap(top, "new"); !ok || n.Kind != yaml.MappingNode || YAMLGet(top, "new") != n {
		t.Error("YAMLMap did not create and attach a mapping")
	}
	if _, ok := YAMLMap(top, "b"); !ok {
		t.Error("a null value should become a mapping")
	}
	if _, ok := YAMLMap(top, "c"); ok {
		t.Error("a list is not a mapping")
	}
	YAMLDelete(top, "a")
	if YAMLGet(top, "a") != nil {
		t.Error("YAMLDelete kept the key")
	}
	if got := (EditResult{SHAAfter: strings.Repeat("ab", 32)}).String(); got != "none -> abababab" {
		t.Errorf("String() = %q", got)
	}
}

func TestBreakStale_GivesBackALiveLockAndRemovesAStaleOne(t *testing.T) {
	old := editLockStale
	editLockStale = time.Hour
	defer func() { editLockStale = old }()
	dir, file := newState(t)
	_ = SecureDir(dir)
	lock := file + ".lock"
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	breakStale(lock) // a waiter that lost the race: the lock is fresh
	if _, err := os.Lstat(lock); err != nil {
		t.Fatalf("a live lock was not given back: %v", err)
	}
	editLockStale = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	breakStale(lock)
	if _, err := os.Lstat(lock); err == nil {
		t.Error("a stale lock survived")
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("debris left: %v", es)
	}
}

// A lock its holder has just removed can refuse the next creation for a moment
// (Windows: the name is delete-pending, so the open fails with access denied, not
// "exists"). That is the holder letting go, not a failure: the waiter must retry,
// where it used to give up at once with "cannot take the edit lock" (the
// TestEditYAML_ConcurrentEditsAreAllKept flake on windows-latest, K-163).
func TestEditYAML_ReleasePendingLockIsWaitedOut(t *testing.T) {
	oldOpen, oldPending := openLockFile, lockReleasePending
	defer func() { openLockFile, lockReleasePending = oldOpen, oldPending }()
	refusals := 3
	openLockFile = func(lock string) (*os.File, error) {
		if refusals > 0 {
			refusals--
			return nil, &fs.PathError{Op: "open", Path: lock, Err: fs.ErrPermission}
		}
		return oldOpen(lock)
	}
	lockReleasePending = func(err error) bool { return errors.Is(err, fs.ErrPermission) }

	_, file := newState(t)
	if err := SecureDir(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	if _, err := EditYAML(file, 1<<16, setKey("a", "1"), nil); err != nil {
		t.Fatalf("a release-pending refusal was not waited out: %v", err)
	}
	if refusals != 0 {
		t.Errorf("the refusals were not retried: %d left", refusals)
	}

	// A refusal that never clears is still a failure, after the wait.
	oldWait := editLockWait
	editLockWait = 50 * time.Millisecond
	defer func() { editLockWait = oldWait }()
	openLockFile = func(lock string) (*os.File, error) {
		return nil, &fs.PathError{Op: "open", Path: lock, Err: fs.ErrPermission}
	}
	if _, err := EditYAML(file, 1<<16, setKey("b", "1"), nil); err == nil || !strings.Contains(err.Error(), "cannot take the edit lock") {
		t.Fatalf("a permanent refusal: err = %v, want cannot take the edit lock", err)
	}

	// Off Windows nothing is release-pending, so the same refusal fails at once.
	lockReleasePending = oldPending
	if runtime.GOOS != "windows" {
		start := time.Now()
		editLockWait = 5 * time.Second
		if _, err := EditYAML(file, 1<<16, setKey("c", "1"), nil); err == nil {
			t.Fatal("expected a failure")
		}
		if time.Since(start) > time.Second {
			t.Error("a refusal that is not release-pending was waited on")
		}
	}
}
