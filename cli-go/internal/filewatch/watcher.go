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
// # Debounce
//
// Rapid bursts of OS events for the same path are coalesced into a single
// event after a 100 ms quiet window. The action of the coalesced event is
// the LAST action seen for the path within the window.
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
	watchedDirs map[string]bool         // absolute paths of directories currently in the OS watch set
	pending     map[string]pendingEvent // keyed by relative path
	timers      map[string]*time.Timer  // debounce timers, keyed by relative path

	once sync.Once // guards Close
}

type pendingEvent struct {
	action ChangeAction
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
		timers:      make(map[string]*time.Timer),
	}

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
			t.Stop()
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
	w.debounce(rel, action)
}

// debounce schedules or resets a timer to emit a ChangeEvent for relpath
// after debounceDuration of silence. Multiple calls within the window are
// coalesced; the last action wins.
func (w *Watcher) debounce(relPath string, action ChangeAction) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Update or set the pending action for this path.
	w.pending[relPath] = pendingEvent{action: action}

	if t, exists := w.timers[relPath]; exists {
		t.Reset(debounceDuration)
		return
	}

	// Capture path for the closure; don't capture the map value.
	path := relPath
	w.timers[path] = time.AfterFunc(debounceDuration, func() {
		w.mu.Lock()
		p, ok := w.pending[path]
		delete(w.pending, path)
		delete(w.timers, path)
		w.mu.Unlock()

		if !ok {
			return
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
			Action: p.action,
			TS:     time.Now().UTC(),
		}:
		case <-w.closeCh:
		default:
			// eventCh full (slow consumer); drop and warn.
			slog.Warn("filewatch: event channel full; dropping event", "path", path)
		}
	})
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

		if err := w.fw.Add(path); err != nil {
			slog.Warn("filewatch: could not add dir to watch set", "path", path, "err", err)
			return nil // non-fatal; skip this dir
		}

		w.mu.Lock()
		w.watchedDirs[path] = true
		w.mu.Unlock()

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
