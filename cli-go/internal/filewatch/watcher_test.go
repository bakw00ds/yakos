package filewatch

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test timing policy
//
// Nothing here asserts on a fixed wall-clock settle window. A fixed sleep is
// a guess about scheduler latency; under CPU load (parallel packages, -race,
// CI) the guess loses and the test flakes. Instead:
//
//   - Positive assertions wait for the event with a generous deadline
//     (waitForEvent) or poll a condition (awaitCond). A slow machine only
//     makes the test slower, never wrong.
//   - Negative assertions ("no event for X") run on a fakeClock, so the
//     debounce window cannot expire on its own. The test writes a sentinel
//     file that sorts after every other name, waits until the watcher has
//     observed it (barrier), and only then advances the clock. The watcher
//     processes raw events in order, so everything written before the
//     sentinel has been seen by then. The flush is synchronous, which makes
//     the assertion exact with no window to tune.
//
// Residual assumption: the barrier relies on the OS delivering directory
// events in order. Per-file events (a write to a file the kernel watches
// individually) can still trail the directory event; on the fake clock a
// trailing event lands in an unflushed window and is invisible, so it cannot
// cause a false failure.

// waitForEvent reads from ch with a timeout and returns the received event.
// Fails the test if no event arrives within the timeout.
func waitForEvent(t *testing.T, ch <-chan ChangeEvent, timeout time.Duration) ChangeEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(timeout):
		t.Fatal("timeout waiting for file-change event")
		return ChangeEvent{}
	}
}

// eventDeadline bounds every positive wait. It is deliberately far above any
// plausible scheduling delay; it only matters when the code is genuinely broken.
const eventDeadline = 10 * time.Second

// awaitCond polls cond every millisecond until it holds or the deadline passes.
func awaitCond(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(eventDeadline)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", eventDeadline, what)
		}
		time.Sleep(time.Millisecond)
	}
}

// hasPending reports whether the watcher holds an unflushed debounce entry
// for rel, i.e. the raw event has been observed but the window is still open.
func (w *Watcher) hasPending(rel string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.pending[rel]
	return ok
}

// isWatched reports whether dir is in the OS watch set.
func (w *Watcher) isWatched(dir string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.watchedDirs[dir]
}

// newWatcher creates a Watcher on root with the real clock, starts it, and
// registers cleanup.
func newWatcher(t *testing.T, root string) *Watcher {
	t.Helper()
	w, err := New(root)
	if err != nil {
		t.Fatalf("filewatch.New: %v", err)
	}
	w.Start()
	t.Cleanup(w.Close)
	return w
}

// newClockWatcher creates a Watcher over the real filesystem whose debounce
// windows are driven by a fakeClock. setup, if non-nil, runs before Start
// (used to install test seams).
func newClockWatcher(t *testing.T, root string, setup func(*Watcher)) (*Watcher, *fakeClock) {
	t.Helper()
	w, err := New(root)
	if err != nil {
		t.Fatalf("filewatch.New: %v", err)
	}
	clk := &fakeClock{}
	w.afterFunc = clk.afterFunc
	if setup != nil {
		setup(w)
	}
	w.Start()
	t.Cleanup(w.Close)
	return w, clk
}

var barrierSeq atomic.Int64

// barrierPrefix names sentinel files. It sorts after every other name the
// tests create, so a directory rescan that reports several new files reports
// the sentinel last.
const barrierPrefix = "zzzz-barrier-"

// barrier writes a sentinel file into dir and blocks until the watcher has
// observed it. See the timing policy above.
func barrier(t *testing.T, w *Watcher, dir string) {
	t.Helper()
	name := barrierPrefix + strconv.FormatInt(barrierSeq.Add(1), 10)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("b"), 0644); err != nil {
		t.Fatalf("barrier WriteFile: %v", err)
	}
	rel, _ := w.toRelPath(filepath.Join(dir, name))
	awaitCond(t, "watcher to observe barrier file "+rel, func() bool { return w.hasPending(rel) })
}

// flushAll expires every open debounce window and returns the emitted events,
// excluding barrier sentinels. The flush is synchronous.
func flushAll(w *Watcher, clk *fakeClock) []ChangeEvent {
	clk.advance(debounceDuration)
	var out []ChangeEvent
	for {
		select {
		case ev := <-w.Events():
			if !strings.HasPrefix(filepath.Base(ev.Path), barrierPrefix) {
				out = append(out, ev)
			}
		default:
			return out
		}
	}
}

// flushExpect flushes like flushAll but keeps flushing until at least want
// events have been collected (or the deadline passes). A raw event for the
// same path can land between the flush snapshot and the debounce restart,
// which re-arms the window instead of flushing it; the next flush then emits
// the single coalesced event. want == 0 flushes exactly once, which is exact
// for negative assertions because the clock never advances on its own.
func flushExpect(t *testing.T, w *Watcher, clk *fakeClock, want int) []ChangeEvent {
	t.Helper()
	events := flushAll(w, clk)
	deadline := time.Now().Add(eventDeadline)
	for len(events) < want {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d event(s); have %+v", want, events)
		}
		time.Sleep(time.Millisecond)
		events = append(events, flushAll(w, clk)...)
	}
	return events
}

// requireOne asserts events holds exactly one event with the given path and action.
func requireOne(t *testing.T, events []ChangeEvent, path string, action ChangeAction) {
	t.Helper()
	if len(events) != 1 || events[0].Path != path || events[0].Action != action {
		t.Fatalf("events = %+v; want exactly one %s %q", events, action, path)
	}
}

// requireNone asserts that no non-barrier event was emitted.
func requireNone(t *testing.T, events []ChangeEvent) {
	t.Helper()
	if len(events) != 0 {
		t.Fatalf("unexpected events: %+v", events)
	}
}

// fakeClock is a manually driven replacement for time.AfterFunc so debounce
// tests do not depend on wall-clock scheduling or fsnotify delivery latency.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Duration
	entries []*fakeTimer
}

type fakeTimer struct {
	c       *fakeClock
	due     time.Duration
	f       func()
	stopped bool // guarded by c.mu
}

func (ft *fakeTimer) Stop() bool {
	ft.c.mu.Lock()
	defer ft.c.mu.Unlock()
	was := !ft.stopped
	ft.stopped = true
	return was
}

func (c *fakeClock) afterFunc(d time.Duration, f func()) stopper {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTimer{c: c, due: c.now + d, f: f}
	c.entries = append(c.entries, ft)
	return ft
}

// advance moves time forward and runs every live timer that became due.
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due []*fakeTimer
	for _, e := range c.entries {
		if !e.stopped && e.due <= c.now {
			e.stopped = true
			due = append(due, e)
		}
	}
	c.mu.Unlock()
	for _, e := range due {
		e.f()
	}
}

// lastCallback returns the callback of the most recently scheduled timer,
// whether or not it was stopped (a stopped timer's callback may already be
// running on another goroutine in production, which is the race under test).
func (c *fakeClock) lastCallback() func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[len(c.entries)-1].f
}

// newFakeWatcher returns an unstarted Watcher whose debouncer runs on a
// fakeClock. Events are injected by calling w.debounce directly.
func newFakeWatcher(t *testing.T) (*Watcher, *fakeClock) {
	t.Helper()
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("filewatch.New: %v", err)
	}
	t.Cleanup(w.Close)
	clk := &fakeClock{}
	w.afterFunc = clk.afterFunc
	return w, clk
}

// touch creates an empty file under the watcher root and returns its path. A
// modified-only window stat-checks its path (directories and vanished files
// are dropped), so debounce tests need a real file.
func touch(t *testing.T, w *Watcher, name string) string {
	t.Helper()
	p := filepath.Join(w.root, name)
	if err := os.WriteFile(p, nil, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestDebounceCollapses verifies that rapid writes to the same file within
// the debounce window produce a single event, and that the window restarts on
// every write. Driven by a fake clock: no real fsnotify delivery or sleeps,
// so a loaded CI runner cannot straddle the window boundary.
func TestDebounceCollapses(t *testing.T) {
	t.Parallel()
	w, clk := newFakeWatcher(t)
	burst := touch(t, w, "burst.txt")

	for i := 0; i < 5; i++ {
		w.debounce("burst.txt", burst, ActionModified)
		clk.advance(debounceDuration - 1) // just inside the window each time
		if n := len(w.Events()); n != 0 {
			t.Fatalf("event emitted mid-burst after write #%d", i)
		}
	}
	clk.advance(1) // quiet window now fully elapsed since the last write

	ev := waitForEvent(t, w.Events(), time.Second)
	if ev.Path != "burst.txt" || ev.Action != ActionModified {
		t.Errorf("event = %+v; want burst.txt modified", ev)
	}
	clk.advance(10 * debounceDuration)
	if n := len(w.Events()); n != 0 {
		t.Errorf("%d extra event(s) after collapse", n)
	}
}

// TestDebounceStaleCallbackIgnored guards the Timer.Reset race: a callback
// whose timer already fired (or was superseded) but which runs after a newer
// event must not flush the newer window early.
func TestDebounceStaleCallbackIgnored(t *testing.T) {
	t.Parallel()
	w, clk := newFakeWatcher(t)
	a := touch(t, w, "a.txt")

	// Superseded before firing.
	w.debounce("a.txt", a, ActionModified)
	stale := clk.lastCallback()
	w.debounce("a.txt", a, ActionModified)
	stale() // late-running callback of the superseded window
	if n := len(w.Events()); n != 0 {
		t.Fatalf("stale callback flushed early: %d event(s)", n)
	}
	clk.advance(debounceDuration)
	waitForEvent(t, w.Events(), time.Second)

	// Fired, flushed, then a new burst starts; the old callback runs again
	// (as a Reset-rearmed timer would) and must not eat the new window.
	w.debounce("a.txt", a, ActionModified)
	stale2 := clk.lastCallback()
	clk.advance(debounceDuration)
	waitForEvent(t, w.Events(), time.Second)
	w.debounce("a.txt", a, ActionModified)
	stale2()
	if n := len(w.Events()); n != 0 {
		t.Fatalf("stale callback flushed next window early: %d event(s)", n)
	}
	clk.advance(debounceDuration)
	waitForEvent(t, w.Events(), time.Second)
	clk.advance(10 * debounceDuration)
	if n := len(w.Events()); n != 0 {
		t.Errorf("%d extra event(s)", n)
	}
}

// TestCreateEmitsCreated verifies that creating a new file emits exactly one
// "created" event regardless of platform.
//
// Cross-platform note: on Linux (inotify) and Windows, os.WriteFile on a new
// path emits a raw CREATE followed by one or more WRITEs.  The old last-wins
// coalescer turned that sequence into "modified", breaking this test on CI.
// The coalescer uses flag accumulation + precedence: any window that
// contains a CREATE (with or without subsequent WRITEs) and no DELETE flushes
// as "created", so the result is deterministic on all platforms. The fake
// clock keeps the window open until the barrier proves every raw event of
// the write was seen, so "exactly one" is exact rather than time-boxed.
func TestCreateEmitsCreated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w, clk := newClockWatcher(t, root, nil)

	target := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(target, []byte("hi"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	barrier(t, w, root)

	events := flushExpect(t, w, clk, 1)
	requireOne(t, events, "hello.txt", ActionCreated)
	if events[0].TS.IsZero() {
		t.Error("TS is zero")
	}
}

// TestModifyEmitsModified verifies that writing to an EXISTING file emits a
// "modified" event.
//
// The file is created before the watcher starts so no CREATE is ever observed
// during the debounce window; only WRITEs arrive, which coalesce to "modified"
// on all platforms. New registers every watch before it returns, so the
// setup write can never be observed and no settle wait is needed.
func TestModifyEmitsModified(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "data.txt")
	if err := os.WriteFile(target, []byte("initial"), 0644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	w := newWatcher(t, root)

	if err := os.WriteFile(target, []byte("updated"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := waitForEvent(t, w.Events(), eventDeadline)
	if ev.Path != "data.txt" {
		t.Errorf("Path = %q; want %q", ev.Path, "data.txt")
	}
	if ev.Action != ActionModified {
		t.Errorf("Action = %q; want %q", ev.Action, ActionModified)
	}
}

// TestDeleteEmitsDeleted verifies that removing a file emits a "deleted" event.
//
// The file is created before the watcher starts; the watcher sees only the
// DELETE op, which coalesces to "deleted" on all platforms.
func TestDeleteEmitsDeleted(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "gone.txt")
	if err := os.WriteFile(target, []byte("bye"), 0644); err != nil {
		t.Fatalf("setup WriteFile: %v", err)
	}

	w := newWatcher(t, root)

	if err := os.Remove(target); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	ev := waitForEvent(t, w.Events(), eventDeadline)
	if ev.Path != "gone.txt" {
		t.Errorf("Path = %q; want %q", ev.Path, "gone.txt")
	}
	if ev.Action != ActionDeleted {
		t.Errorf("Action = %q; want %q", ev.Action, ActionDeleted)
	}
}

// TestCreateDeleteCoalescesToNothing pins the net-no-op rule at the unit
// level, independent of the OS: a path that is created AND deleted in one
// debounce window (either raw order) produces no event.
func TestCreateDeleteCoalescesToNothing(t *testing.T) {
	t.Parallel()
	for _, order := range [][2]ChangeAction{
		{ActionCreated, ActionDeleted},
		{ActionDeleted, ActionCreated},
	} {
		w, clk := newFakeWatcher(t)
		w.debounce("transient.txt", "/x/transient.txt", order[0])
		w.debounce("transient.txt", "/x/transient.txt", order[1])
		clk.advance(debounceDuration)
		if n := len(w.Events()); n != 0 {
			t.Errorf("order %v: %d event(s) emitted; want none", order, n)
		}
	}
}

// TestCreateThenDeleteInWindowEmitsNothing verifies against the real
// filesystem that a file created AND deleted inside one debounce window
// produces no event.
//
// The window is held open by the fake clock, so a starved test goroutine
// between the write and the remove can no longer let the window expire
// between them (which is a legitimate "created" event, not a net no-op).
func TestCreateThenDeleteInWindowEmitsNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w, clk := newClockWatcher(t, root, nil)

	target := filepath.Join(root, "transient.txt")
	if err := os.WriteFile(target, []byte("ephemeral"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	barrier(t, w, root)

	requireNone(t, flushAll(w, clk))
}

// TestPathsAreRelative asserts that event paths are relative to the root and
// use forward slashes.
func TestPathsAreRelative(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	// Create a subdirectory first so we can test a nested path.
	subDir := filepath.Join(root, "src")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	w := newWatcher(t, root)

	target := filepath.Join(subDir, "main.go")
	if err := os.WriteFile(target, []byte("package main"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := waitForEvent(t, w.Events(), eventDeadline)
	// Path must use forward slashes and be relative.
	if ev.Path != "src/main.go" {
		t.Errorf("Path = %q; want %q", ev.Path, "src/main.go")
	}
}

// TestSecretPathSkipped verifies that files matching the secret deny-set are
// silently suppressed — no event must arrive.
func TestSecretPathSkipped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w, clk := newClockWatcher(t, root, nil)

	secretFiles := []string{
		".env",
		".env.local",
		"credentials",
		"id_rsa",
		"id_ed25519",
		"server.pem",
		"private.key",
		"keystore.jks",
	}

	for _, name := range secretFiles {
		target := filepath.Join(root, name)
		if err := os.WriteFile(target, []byte("secret"), 0644); err != nil {
			t.Fatalf("WriteFile %q: %v", name, err)
		}
	}
	barrier(t, w, root)

	for _, name := range secretFiles {
		if w.hasPending(name) {
			t.Errorf("secret file %q reached the debouncer", name)
		}
	}
	requireNone(t, flushAll(w, clk))
}

// testSkippedDir checks that writes inside a skipped directory never reach
// the debouncer and that the directory is never watched.
func testSkippedDir(t *testing.T, name, file string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatalf("Mkdir %s: %v", name, err)
	}

	w, clk := newClockWatcher(t, root, nil)

	if w.isWatched(dir) {
		t.Errorf("%s is in the watch set", name)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	barrier(t, w, root)

	if w.hasPending(name + "/" + file) {
		t.Errorf("%s/%s reached the debouncer", name, file)
	}
	requireNone(t, flushAll(w, clk))
}

// TestGitDirSkipped verifies that .git directory changes produce no events.
func TestGitDirSkipped(t *testing.T) {
	t.Parallel()
	testSkippedDir(t, ".git", "HEAD")
}

// TestNodeModulesSkipped verifies that node_modules directory changes produce
// no events.
func TestNodeModulesSkipped(t *testing.T) {
	t.Parallel()
	testSkippedDir(t, "node_modules", "react.js")
}

// TestYakosSkipped verifies that the .yakos directory is not watched.
func TestYakosSkipped(t *testing.T) {
	t.Parallel()
	testSkippedDir(t, ".yakos", "state.json")
}

// TestNewSubdirGetsWatched verifies that a directory created after the watcher
// starts is itself watched — files inside it emit events.
//
// It polls for the watch to be registered instead of sleeping, then writes.
// Note this test alone cannot detect a lost watch, because the rescan after
// registration would report the file anyway; TestNewSubdirRearmedAfterWatchLoss
// is the guard for the lost-watch bug.
func TestNewSubdirGetsWatched(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w := newWatcher(t, root)

	newDir := filepath.Join(root, "newpkg")
	if err := os.Mkdir(newDir, 0755); err != nil {
		t.Fatalf("Mkdir newpkg: %v", err)
	}
	awaitCond(t, "new directory to be watched", func() bool { return w.isWatched(newDir) })

	target := filepath.Join(newDir, "lib.go")
	if err := os.WriteFile(target, []byte("package newpkg"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ev := waitForEvent(t, w.Events(), eventDeadline)
	if ev.Path != "newpkg/lib.go" {
		t.Errorf("Path = %q; want %q", ev.Path, "newpkg/lib.go")
	}
}

// TestNewSubdirRearmedAfterWatchLoss is the regression test for the lost
// new-subdirectory watch. On kqueue platforms fsnotify's own goroutine can
// re-register a just-created directory with fewer flags right after our Add,
// after which writes inside it are never reported (and a second Add heals
// it). The test models that end state deterministically: the first Add is
// swallowed, so the directory has no working watch. The watcher must
// re-register it on its re-arm timer so that a later write is still seen.
//
// It runs on the real clock on purpose. The re-arm delay exists to let
// fsnotify's concurrent internal registration settle, so collapsing it to
// zero with a fake clock would re-create the very race under test. The test
// waits for the re-arm to complete (a condition, not a sleep) before writing.
func TestNewSubdirRearmedAfterWatchLoss(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	newDir := filepath.Join(root, "newpkg")

	var adds atomic.Int32
	var rearmed atomic.Bool
	w, err := New(root)
	if err != nil {
		t.Fatalf("filewatch.New: %v", err)
	}
	w.addWatch = func(dir string) error {
		if dir == newDir && adds.Add(1) == 1 {
			return nil // first Add lost: no working watch on the new directory
		}
		return w.fw.Add(dir)
	}
	w.afterFunc = func(d time.Duration, f func()) stopper {
		if d != newDirRearmDelay {
			return realAfterFunc(d, f)
		}
		return realAfterFunc(d, func() { f(); rearmed.Store(true) })
	}
	w.Start()
	t.Cleanup(w.Close)

	if err := os.Mkdir(newDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	awaitCond(t, "re-arm of the new directory to complete", rearmed.Load)
	if got := adds.Load(); got < 2 {
		t.Fatalf("directory was not re-registered: %d Add call(s)", got)
	}

	if err := os.WriteFile(filepath.Join(newDir, "lib.go"), []byte("package newpkg"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev := waitForEvent(t, w.Events(), eventDeadline)
	if ev.Path != "newpkg/lib.go" || ev.Action != ActionCreated {
		t.Fatalf("event = %+v; want created newpkg/lib.go", ev)
	}
}

// TestNewSubdirReportsFilesCreatedBeforeWatch covers the gap between a
// directory appearing and its watch becoming active: a file written in that
// gap produces no raw event on any platform, so the watcher must rescan the
// new directory and report it.
func TestNewSubdirReportsFilesCreatedBeforeWatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	newDir := filepath.Join(root, "newpkg")

	var once sync.Once
	w, clk := newClockWatcher(t, root, func(w *Watcher) {
		w.addWatch = func(dir string) error {
			if dir == newDir {
				once.Do(func() {
					// Lands inside the gap: after mkdir, before the watch exists.
					if err := os.WriteFile(filepath.Join(newDir, "early.go"), []byte("package newpkg"), 0644); err != nil {
						t.Errorf("gap WriteFile: %v", err)
					}
				})
			}
			return w.fw.Add(dir)
		}
	})

	if err := os.Mkdir(newDir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	awaitCond(t, "rescan to report the early file", func() bool { return w.hasPending("newpkg/early.go") })
	requireOne(t, flushExpect(t, w, clk, 1), "newpkg/early.go", ActionCreated)
}

// TestDirectoryModifiedEventDropped: Windows reports "modified" on the parent
// directory when a child changes. Only file events are surfaced, so a
// modified-only window on a directory (or a vanished path) emits nothing.
// Injecting the event keeps this platform-neutral.
func TestDirectoryModifiedEventDropped(t *testing.T) {
	t.Parallel()
	w, clk := newFakeWatcher(t)
	dir := filepath.Join(w.root, "newpkg")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	w.debounce("newpkg", dir, ActionModified)
	w.debounce("gone.txt", filepath.Join(w.root, "gone.txt"), ActionModified)
	clk.advance(debounceDuration)
	if n := len(w.Events()); n != 0 {
		t.Fatalf("%d event(s) emitted for a directory/vanished path; want none", n)
	}
	// A real file still flows.
	f := filepath.Join(w.root, "f.txt")
	if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	w.debounce("f.txt", f, ActionModified)
	clk.advance(debounceDuration)
	if ev := waitForEvent(t, w.Events(), time.Second); ev.Path != "f.txt" || ev.Action != ActionModified {
		t.Errorf("event = %+v; want f.txt modified", ev)
	}
}

// TestRescanCap checks both sides of rescanFileCap: at the cap every file is
// reported individually; one over, a single summary event for the directory
// carries the count.
func TestRescanCap(t *testing.T) {
	// Serial on purpose: it opens 500+ files, and a serial test runs before
	// the parallel tests resume, so no sibling watcher is closing descriptors
	// concurrently (seen once as EBADF on a write under heavy load).
	for _, tc := range []struct {
		name  string
		files int
	}{{"at-cap", rescanFileCap}, {"over-cap", rescanFileCap + 1}} {
		t.Run(tc.name, func(t *testing.T) {
			w, clk := newFakeWatcher(t)
			dir := filepath.Join(w.root, "bulk")
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.files; i++ {
				if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".txt"), nil, 0644); err != nil {
					t.Fatal(err)
				}
			}
			w.rescanTree(dir)
			clk.advance(debounceDuration)
			var evs []ChangeEvent
			for len(w.Events()) > 0 {
				evs = append(evs, <-w.Events())
			}
			if tc.files <= rescanFileCap {
				if len(evs) != tc.files {
					t.Fatalf("got %d events; want %d per-file events", len(evs), tc.files)
				}
				for _, ev := range evs {
					if ev.Count != 0 || ev.Action != ActionCreated {
						t.Fatalf("unexpected event %+v", ev)
					}
				}
				return
			}
			if len(evs) != 1 || evs[0].Path != "bulk" || evs[0].Action != ActionCreated || evs[0].Count != tc.files {
				t.Fatalf("events = %d (first %+v); want one summary for bulk with Count=%d", len(evs), evs[0], tc.files)
			}
		})
	}
}

// TestCloseIsClean verifies that Close() does not block or panic, is
// idempotent, and that a debounce callback that is already running when the
// watcher closes delivers nothing after Close returns.
func TestCloseIsClean(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w, clk := newClockWatcher(t, root, nil)

	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	awaitCond(t, "watcher to observe f.txt", func() bool { return w.hasPending("f.txt") })
	inflight := clk.lastCallback() // the callback a racing timer would run

	done := make(chan struct{})
	go func() {
		w.Close()
		w.Close() // idempotent
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(eventDeadline):
		t.Fatal("Close() timed out")
	}

	inflight()
	clk.advance(10 * debounceDuration)
	if n := len(w.Events()); n != 0 {
		t.Errorf("%d event(s) delivered after Close", n)
	}
}

// TestIsSecretPath verifies the secret deny-set function covers all required
// cases, mirroring the consoleui/files_handler.go isSecretFile test surface.
func TestIsSecretPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		secret bool
	}{
		{".env", true},
		{".env.local", true},
		{".envrc", true},
		{"credentials", true},
		{"aws_credentials", true},
		{"id_rsa", true},
		{"id_dsa", true},
		{"id_ecdsa", true},
		{"id_ed25519", true},
		{".npmrc", true},
		{".netrc", true},
		{".pgpass", true},
		{".htpasswd", true},
		{"server.pem", true},
		{"private.key", true},
		{"cert.p12", true},
		{"cert.pfx", true},
		{"server.crt", true},
		{"server.cer", true},
		{"server.der", true},
		{"keystore.jks", true},
		{"archive.gpg", true},
		{"key.asc", true},
		// Safe files.
		{"main.go", false},
		{"README.md", false},
		{"config.yaml", false},
		{"Makefile", false},
		{"package.json", false},
		{".gitignore", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSecretPath(tc.name)
			if got != tc.secret {
				t.Errorf("isSecretPath(%q) = %v; want %v", tc.name, got, tc.secret)
			}
		})
	}
}

// TestPayloadNoContentField is the structural invariant test.
// It asserts that ChangeEvent has no Content, Data, Body, or Bytes field —
// enforcing the "paths only" invariant at the type level.
func TestPayloadNoContentField(t *testing.T) {
	t.Parallel()
	forbidden := []string{"Content", "Data", "Body", "Bytes", "Text", "Raw"}
	typ := reflect.TypeOf(ChangeEvent{})
	for _, name := range forbidden {
		if _, ok := typ.FieldByName(name); ok {
			t.Errorf("ChangeEvent has forbidden field %q — paths-only invariant violated", name)
		}
	}
}

// TestDeleteFilesDoesNotChangeWatchedDirCount verifies that creating and then
// deleting many regular files does not alter the watched-directory count.
//
// Regression test for the watchN drift bug: the previous implementation
// decremented watchN whenever a removed path stat-failed (which includes
// deleted regular files), causing watchN to drift below the true dir count
// and silently letting the 8192 cap be re-exceeded.
//
// The new implementation maintains an explicit watchedDirs set and only
// decrements when a path that IS in that set is removed. Barriers, not
// sleeps, establish that the watcher has processed the creates and deletes
// before the count is read.
func TestDeleteFilesDoesNotChangeWatchedDirCount(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	w, clk := newClockWatcher(t, root, nil)

	// Record the initial directory count (should be 1: the root itself).
	initialCount := w.watchedDirCount()
	if initialCount == 0 {
		t.Fatal("expected watchedDirCount > 0 after New(root)")
	}

	const nFiles = 20
	names := make([]string, nFiles)
	for i := range names {
		names[i] = filepath.Join(root, "tmpfile_"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(names[i], []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	barrier(t, w, root)
	flushAll(w, clk)

	for _, name := range names {
		if err := os.Remove(name); err != nil {
			t.Fatalf("Remove: %v", err)
		}
	}
	barrier(t, w, root)
	flushAll(w, clk)

	afterCount := w.watchedDirCount()
	if afterCount != initialCount {
		t.Errorf("watchedDirCount changed: was %d before file creates/deletes, got %d after; "+
			"file deletes must not alter the watched-directory count", initialCount, afterCount)
	}
}
