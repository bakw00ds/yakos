// Package filewatch implements a recursive directory watcher that emits
// debounced file-change events for the yakOS IDE file-watcher feature.
//
// # Overview
//
// [New] constructs a [Watcher] rooted at a workspace directory. Events are
// delivered on the channel returned by [Watcher.Events]. The caller must
// call [Watcher.Close] to release all underlying OS resources.
//
// # What is watched
//
// All regular files and directories under root, subject to:
//   - Skipped dirs: ".git", "node_modules", ".yakos". These are never added
//     to the watch set and never appear in events.
//   - Secret-pattern paths: files matching the same deny-set as
//     consoleui/files_handler.go → isSecretFile are silently dropped before
//     any event is emitted. (Mirror of that logic; see isSecretPath below for
//     the canonical source comment.)
//
// # Debounce and action coalescing
//
// Rapid bursts of OS events for the same path are coalesced into a single
// event after a 100 ms quiet window.  The coalesced action is determined by
// precedence over all raw events seen during the window, not last-wins:
//
//  1. CREATE seen AND DELETE seen → net no-op; emit nothing (born and died in
//     the same window).
//  2. CREATE seen, no DELETE → emit "created" (a create followed by writes is
//     still a creation; guards the Linux/Windows pattern of CREATE+WRITE on
//     os.WriteFile).
//  3. DELETE seen, no CREATE → emit "deleted".
//  4. Only writes seen → emit "modified".
//
// This is platform-independent: the decision is made from the set of observed
// fsnotify op-classes, not from a single raw event order.
//
// # Invariant: paths only
//
// Events carry ONLY the relative path, action, and timestamp.
// File contents are NEVER read or included. This is an architectural invariant.
//
// # Caps
//
// At most [maxWatchedDirs] directories may be added to the OS watch set.
// If the cap is exceeded, a slog.Warn is emitted and the directory is skipped
// (the watcher continues operating on the directories already watched).
//
// # Concurrency
//
// Watcher is safe for concurrent use. Start() may only be called once;
// Close() may be called from any goroutine and is idempotent.
//
// # Stability: experimental
package filewatch

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ChangeAction describes what happened to a file.
type ChangeAction string

const (
	// ActionCreated is emitted when a new file appears.
	ActionCreated ChangeAction = "created"
	// ActionModified is emitted when an existing file's content changes.
	ActionModified ChangeAction = "modified"
	// ActionDeleted is emitted when a file is removed.
	ActionDeleted ChangeAction = "deleted"
)

// ChangeEvent is one debounced file-change notification.
// Invariant: Path and Action are the only data fields. No content, no bytes.
type ChangeEvent struct {
	// Path is the workspace-relative path to the changed file, using
	// forward slashes on all platforms.
	Path string `json:"path"`
	// Action is one of "created", "modified", or "deleted".
	Action ChangeAction `json:"action"`
	// TS is the server-side time at which the debounced event fired.
	TS time.Time `json:"ts"`
}

// maxWatchedDirs is the safety cap on the number of directories held in the
// OS inotify/kqueue/FSEvents watch set. Exceeding this cap emits a warning
// and stops adding new dirs (the watcher continues on already-watched dirs).
// 8192 is well within the default inotify max_user_watches (65536) while
// guarding against runaway deep trees.
const maxWatchedDirs = 8192

// debounceDuration is the quiet-window before a coalesced event fires.
const debounceDuration = 100 * time.Millisecond

// newDirRearmDelay is how long after registering a newly created directory
// the watcher re-registers it and rescans it. It must stay well below
// debounceDuration so a rescan-reported file coalesces with the file's own
// raw events into a single event.
//
// Why re-register at all: on BSD/macOS, fsnotify's kqueue backend reacts to
// the parent's "new entry" event by watching the new directory itself with
// only delete/rename flags, on its own goroutine and concurrently with our
// Add. If that internal registration lands after ours it silently replaces
// our flags, and writes inside the directory are never reported (observed as
// a lost event in roughly 1 of 3000 create-dir-then-write sequences under
// load). Add is idempotent and restores the flags, so one delayed Add after
// the internal registration has settled closes the window.
const newDirRearmDelay = 25 * time.Millisecond

// skipDirNames is the set of directory base-names that are never watched.
// Must stay in sync with consoleui/files_handler.go skipDirs.
var skipDirNames = map[string]bool{
	".git":         true,
	"node_modules": true,
	".yakos":       true,
}

// Watcher watches a workspace root directory recursively and publishes
// debounced [ChangeEvent]s on the channel returned by [Watcher.Events].
type Watcher struct {
	root    string // absolute, cleaned root
	fw      *fsnotify.Watcher
	eventCh chan ChangeEvent
	closeCh chan struct{}

	mu          sync.Mutex
	watchedDirs map[string]bool           // absolute paths of directories currently in the OS watch set
	pending     map[string]pendingEvent   // keyed by relative path
	timers      map[string]*debounceTimer // debounce timers, keyed by relative path

	genSeq uint64 // monotonic debounce generation counter; guarded by mu

	// afterFunc schedules f after d. Defaults to time.AfterFunc; tests
	// inject a manually-driven clock so debounce behavior is deterministic.
	afterFunc func(d time.Duration, f func()) stopper

	// addWatch registers one directory with the OS watcher. Defaults to
	// fw.Add; tests wrap it to interleave filesystem changes or to model a
	// watch that lost its flags.
	addWatch func(dir string) error

	once sync.Once // guards Close
}

// stopper is the subset of *time.Timer the debouncer needs.
type stopper interface{ Stop() bool }

// debounceTimer is the live timer for one path. gen identifies the most
// recent scheduling; a flush callback whose gen no longer matches is stale
// (its window was superseded by a later event) and must do nothing.
type debounceTimer struct {
	t   stopper
	gen uint64
}

func realAfterFunc(d time.Duration, f func()) stopper { return time.AfterFunc(d, f) }

// pendingEvent accumulates the set of raw fsnotify op-classes observed for a
// path during one debounce window.  The final action is resolved at flush time
// using the precedence rules documented in the package comment.
type pendingEvent struct {
	sawCreate bool // a CREATE op was observed
	sawDelete bool // a DELETE or RENAME op was observed
	// sawWrite is implicit: any pending entry that exists and has neither
	// sawCreate nor sawDelete has only writes.  Tracked separately for
	// completeness so the flush logic is a simple four-case switch.
	sawWrite bool
	// absPath is the absolute filesystem path; used at flush to stat-confirm
	// that a created file still exists before emitting "created".
	absPath string
}

// New constructs a Watcher rooted at root. root must be an existing directory.
// Call [Watcher.Start] to begin watching, then read from [Watcher.Events].
// Call [Watcher.Close] when done.
func New(root string) (*Watcher, error) {
	root = filepath.Clean(root)

	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		root:        root,
		fw:          fw,
		eventCh:     make(chan ChangeEvent, 512),
		closeCh:     make(chan struct{}),
		watchedDirs: make(map[string]bool),
		pending:     make(map[string]pendingEvent),
		timers:      make(map[string]*debounceTimer),
		afterFunc:   realAfterFunc,
	}
	w.addWatch = fw.Add

	// Recursively add all existing subdirs to the watch set.
	if err := w.addDirRecursive(root); err != nil {
		_ = fw.Close()
		return nil, err
	}

	return w, nil
}

// Events returns the channel on which debounced [ChangeEvent]s are delivered.
// The channel is never closed by the Watcher; callers stop reading when done.
func (w *Watcher) Events() <-chan ChangeEvent {
	return w.eventCh
}

// Start begins the watch loop in a background goroutine. It returns
// immediately. Must be called exactly once after [New].
func (w *Watcher) Start() {
	go w.loop()
}

// Close stops the watcher and releases all OS resources. Safe to call
// multiple times; subsequent calls are no-ops. After Close returns, no
// further events will be delivered on [Watcher.Events].
func (w *Watcher) Close() {
	w.once.Do(func() {
		close(w.closeCh)
		_ = w.fw.Close()

		// Cancel all pending debounce timers.
		w.mu.Lock()
		for _, t := range w.timers {
			t.t.Stop()
		}
		w.mu.Unlock()
	})
}

// loop is the main event-processing goroutine.
func (w *Watcher) loop() {
	for {
		select {
		case <-w.closeCh:
			return

		case ev, ok := <-w.fw.Events:
			if !ok {
				// Watcher closed externally.
				return
			}
			w.handleFSEvent(ev)

		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			slog.Warn("filewatch: fsnotify error", "err", err)
		}
	}
}

// handleFSEvent processes one raw fsnotify event.
func (w *Watcher) handleFSEvent(ev fsnotify.Event) {
	// Determine relative path; drop if outside root (shouldn't happen).
	rel, ok := w.toRelPath(ev.Name)
	if !ok {
		return
	}

	// Skip secret-pattern files (path/action only — we never read content).
	base := filepath.Base(ev.Name)
	if isSecretPath(base) {
		return
	}

	// Skip files inside skipped dirs (belt-and-suspenders: fsnotify may
	// deliver events for files inside a watched dir even after we skipped
	// adding a subdir, if the OS coalesces parent and child events).
	if w.insideSkippedDir(rel) {
		return
	}

	// Map fsnotify Op to our ChangeAction.
	action, emit := mapOp(ev.Op)
	if !emit {
		return
	}

	// When a new directory is created, add it to the watch set.
	if ev.Op.Has(fsnotify.Create) {
		if fi, err := os.Lstat(ev.Name); err == nil && fi.IsDir() {
			if err := w.addDirRecursive(ev.Name); err != nil {
				slog.Warn("filewatch: could not add new dir", "path", ev.Name, "err", err)
			}
			// Files created inside the directory before its watch existed
			// produced no raw events; report them now, and again after the
			// re-arm below closes the kqueue flag race.
			w.rescanTree(ev.Name)
			dir := ev.Name
			w.afterFunc(newDirRearmDelay, func() { w.rearmTree(dir) })
			// Dir creation itself: don't emit a "created" event for the dir —
			// only file events are surfaced. Skip.
			return
		}
	}

	// When a dir is removed, fsnotify automatically stops watching it on
	// most platforms; no explicit action needed. We just do bookkeeping.
	if ev.Op.Has(fsnotify.Remove) || ev.Op.Has(fsnotify.Rename) {
		// Always check the watchedDirs set first. If the removed path was a
		// watched directory, remove it from the set. This is authoritative —
		// we do NOT stat the path (it may be gone) and do NOT fall back to
		// heuristic inference from stat errors, which previously caused watchN
		// to decrement for deleted regular files as well (drifting below the
		// true dir count and silently re-allowing the cap to be exceeded).
		w.mu.Lock()
		wasWatchedDir := w.watchedDirs[ev.Name]
		if wasWatchedDir {
			delete(w.watchedDirs, ev.Name)
		}
		w.mu.Unlock()

		if wasWatchedDir {
			// This was a directory. Don't emit a deleted event for dirs.
			return
		}

		// Not a watched directory. It might be a regular file or a directory
		// that was never added to our watch set. Stat to distinguish — but only
		// to decide whether to suppress the event, not to update the dir count.
		if fi, statErr := os.Lstat(ev.Name); statErr == nil && fi.IsDir() {
			// It's a dir that existed but wasn't in watchedDirs (e.g. a skipped
			// dir that was removed). Don't emit a deleted event.
			return
		}
		// Fall through: path is gone (stat error) or is a regular file.
		// Emit a deleted event below.
	}

	// Enqueue debounced emit.
	w.debounce(rel, ev.Name, action)
}

// debounce schedules or resets a debounce timer for relPath and accumulates
// the observed op-class (create / delete / write) into the pending entry.
// Multiple calls within the window do NOT last-action-win; instead each
// op-class flag is OR'd in so the flush closure has the full picture.
func (w *Watcher) debounce(relPath string, absPath string, action ChangeAction) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Accumulate op-class flags into the pending entry for this path.
	p := w.pending[relPath]
	p.absPath = absPath
	switch action {
	case ActionCreated:
		p.sawCreate = true
	case ActionDeleted:
		p.sawDelete = true
	default: // ActionModified
		p.sawWrite = true
	}
	w.pending[relPath] = p

	// Restart the quiet window. We stop the old timer and schedule a fresh
	// one under a new generation instead of calling Timer.Reset: Reset on a
	// timer whose callback already fired (but is blocked on w.mu) re-arms the
	// same callback, which would later flush a *newer* window's pending
	// entry early and emit a second event for one burst. The generation
	// check below makes any superseded callback a no-op.
	// gen comes from a watcher-wide counter so a stale callback can never
	// match a timer created after its own entry was flushed and deleted.
	if old, exists := w.timers[relPath]; exists {
		old.t.Stop()
	}
	w.genSeq++
	gen := w.genSeq

	// Capture path for the closure; don't capture the map value directly.
	path := relPath
	w.timers[path] = &debounceTimer{gen: gen, t: w.afterFunc(debounceDuration, func() {
		w.mu.Lock()
		if cur, live := w.timers[path]; !live || cur.gen != gen {
			// Superseded by a later event (or already flushed).
			w.mu.Unlock()
			return
		}
		p, ok := w.pending[path]
		delete(w.pending, path)
		delete(w.timers, path)
		w.mu.Unlock()

		if !ok {
			return
		}

		// Resolve the coalesced action using precedence rules (see package doc):
		//  1. CREATE + DELETE in same window → net no-op.
		//  2. CREATE only (+ optional writes) → "created" if file still exists.
		//  3. DELETE only → "deleted".
		//  4. Writes only → "modified".
		var action ChangeAction
		switch {
		case p.sawCreate && p.sawDelete:
			// Net no-op: file was born and died within the debounce window.
			return
		case p.sawCreate:
			// CREATE followed by WRITEs is still a creation.  Stat-confirm the
			// file still exists — it could have been deleted after the last
			// debounce reset but before the timer fired (extremely tight race).
			if _, err := os.Lstat(p.absPath); err != nil {
				// File is gone; treat as net no-op.
				return
			}
			action = ActionCreated
		case p.sawDelete:
			action = ActionDeleted
		default:
			action = ActionModified
		}

		// Check closeCh before attempting send.
		select {
		case <-w.closeCh:
			return
		default:
		}

		select {
		case w.eventCh <- ChangeEvent{
			Path:   path,
			Action: action,
			TS:     time.Now().UTC(),
		}:
		case <-w.closeCh:
		default:
			// eventCh full (slow consumer); drop and warn.
			slog.Warn("filewatch: event channel full; dropping event", "path", path)
		}
	})}
}

// addDirRecursive adds dir and all non-skipped subdirectories to the OS watch
// set. It respects the maxWatchedDirs cap and the skipDirNames deny-set.
func (w *Watcher) addDirRecursive(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Ignore permission errors on individual dirs (log at debug level).
			slog.Debug("filewatch: walkdir error", "path", path, "err", err)
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		// Skip designated dirs (don't descend).
		if skipDirNames[base] {
			return filepath.SkipDir
		}

		w.mu.Lock()
		alreadyWatched := w.watchedDirs[path]
		n := len(w.watchedDirs)
		w.mu.Unlock()

		// Don't double-add a dir already in the watch set.
		if alreadyWatched {
			return nil
		}

		if n >= maxWatchedDirs {
			slog.Warn("filewatch: max watched dirs reached; skipping subtree",
				"path", path,
				"cap", maxWatchedDirs,
			)
			return filepath.SkipAll
		}

		if err := w.addWatch(path); err != nil {
			slog.Warn("filewatch: could not add dir to watch set", "path", path, "err", err)
			return nil // non-fatal; skip this dir
		}

		w.mu.Lock()
		w.watchedDirs[path] = true
		w.mu.Unlock()

		return nil
	})
}

// rearmTree re-registers every still-watched directory under dir with the OS
// watcher and then rescans the tree. See [newDirRearmDelay].
func (w *Watcher) rearmTree(dir string) {
	select {
	case <-w.closeCh:
		return
	default:
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			return nil
		}
		if skipDirNames[d.Name()] {
			return filepath.SkipDir
		}
		w.mu.Lock()
		watched := w.watchedDirs[path]
		w.mu.Unlock()
		if watched {
			if err := w.addWatch(path); err != nil {
				slog.Debug("filewatch: re-arm failed", "path", path, "err", err)
			}
		}
		return nil
	})
	w.rescanTree(dir)
}

// rescanTree reports every regular file already present under dir as a
// created file. It covers files that appeared before the directory's watch
// was active. Duplicates of files whose own raw events also arrive coalesce
// in the debounce window. Secret-pattern names and skipped directories are
// filtered exactly as for live events.
func (w *Watcher) rescanTree(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if skipDirNames[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || isSecretPath(d.Name()) {
			return nil
		}
		rel, ok := w.toRelPath(path)
		if !ok || w.insideSkippedDir(rel) {
			return nil
		}
		w.debounce(rel, path, ActionCreated)
		return nil
	})
}

// watchedDirCount returns the number of directories currently in the OS watch
// set. Exposed for testing.
func (w *Watcher) watchedDirCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.watchedDirs)
}

// toRelPath converts an absolute path to a workspace-relative forward-slash
// path. Returns ("", false) if the path is outside the root.
func (w *Watcher) toRelPath(abs string) (string, bool) {
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return "", false
	}
	// Reject paths that escape the root.
	if strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// insideSkippedDir reports whether the relative path passes through any of
// the skipDirNames at ANY ancestor component. For example "node_modules/foo"
// returns true.
func (w *Watcher) insideSkippedDir(relPath string) bool {
	parts := strings.Split(relPath, "/")
	// Check all but the last component (the file itself).
	for _, part := range parts[:len(parts)-1] {
		if skipDirNames[part] {
			return true
		}
	}
	return false
}

// mapOp converts an fsnotify.Op bitmask to a ChangeAction.
// Returns (action, true) when the event should be emitted,
// ("", false) for unrecognised / CHMOD-only events.
func mapOp(op fsnotify.Op) (ChangeAction, bool) {
	switch {
	case op.Has(fsnotify.Create):
		return ActionCreated, true
	case op.Has(fsnotify.Write):
		return ActionModified, true
	case op.Has(fsnotify.Remove):
		return ActionDeleted, true
	case op.Has(fsnotify.Rename):
		// fsnotify emits Rename on the old name and Create on the new name.
		// Treat the old-name Rename as a deletion.
		return ActionDeleted, true
	default:
		// CHMOD or unrecognised: do not emit.
		return "", false
	}
}

// ---- Secret-path filtering ---------------------------------------------------
//
// isSecretPath mirrors the deny-set in consoleui/files_handler.go (isSecretFile).
// Source of truth: consoleui/files_handler.go. If that function changes,
// update this one to match.
//
// Deny-set layers (evaluated against lowercased basename):
//  1. Exact basename: id_rsa, id_dsa, id_ecdsa, id_ed25519, .npmrc, .netrc,
//     .pgpass, .htpasswd.
//  2. Extension: .pem, .key, .p12, .pfx, .crt, .cer, .der, .keystore, .jks,
//     .asc, .gpg.
//  3. Substring: ".env" anywhere in the basename, "credentials" anywhere.

var secretExactBasenames = map[string]bool{
	"id_rsa":     true,
	"id_dsa":     true,
	"id_ecdsa":   true,
	"id_ed25519": true,
	".npmrc":     true,
	".netrc":     true,
	".pgpass":    true,
	".htpasswd":  true,
}

var secretExtensions = map[string]bool{
	".pem":      true,
	".key":      true,
	".p12":      true,
	".pfx":      true,
	".crt":      true,
	".cer":      true,
	".der":      true,
	".keystore": true,
	".jks":      true,
	".asc":      true,
	".gpg":      true,
}

// isSecretPath reports whether the given file basename matches the secret
// deny-set. This function deliberately MIRRORS consoleui/files_handler.go
// isSecretFile — see the comment above for the canonical source of truth.
func isSecretPath(name string) bool {
	lower := strings.ToLower(name)

	// Layer 1: exact basename.
	if secretExactBasenames[lower] {
		return true
	}

	// Layer 2: extension.
	ext := strings.ToLower(filepath.Ext(lower))
	if ext != "" && secretExtensions[ext] {
		return true
	}

	// Layer 3: substrings.
	if strings.Contains(lower, ".env") {
		return true
	}
	if strings.Contains(lower, "credentials") {
		return true
	}

	return false
}
