// Package statepath provides the single canonical resolver for the yakOS
// state directory and its well-known files.
//
// Resolution order (matches the dispatch-log writer in dispatch/events.go
// and the reader in perfdash/server.go — they must agree or the dashboard
// reads from a different file than the daemon writes to):
//
//  1. YAKOS_DISPATCH_LOG env var — override directory (full path OR directory).
//  2. $HOME/.yakos-state         — default for normal users.
//  3. os.TempDir()/.yakos-state  — last-resort fallback when $HOME is unset.
//
// Using os.TempDir() (not "/tmp") keeps the fallback cross-platform.
package statepath

import (
	"os"
	"path/filepath"
)

const (
	dispatchLogName = "dispatch-log.ndjson"
	stateDirName    = ".yakos-state"
)

// Dir returns the yakOS state directory.
// Callers should not hard-code this path; always call Dir() so changes to
// the resolution order are inherited automatically.
func Dir() string {
	if v := os.Getenv("YAKOS_DISPATCH_LOG"); v != "" {
		return v
	}
	home := os.Getenv("HOME")
	if home == "" {
		// os.UserHomeDir tries $HOME first, then OS-specific lookups.
		// If all of those fail we fall back to os.TempDir() — the same
		// last resort as the writer uses — so reader and writer always
		// agree on the directory.
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), stateDirName)
		}
	}
	return filepath.Join(home, stateDirName)
}

// DispatchLog returns the path to the active dispatch-log NDJSON file.
func DispatchLog() string {
	return filepath.Join(Dir(), dispatchLogName)
}

// DispatchLogIn returns the dispatch-log path inside dir.
func DispatchLogIn(dir string) string { return filepath.Join(dir, dispatchLogName) }

// TrustedDir returns the user's real state directory, $HOME/.yakos-state,
// resolved from the home directory only. Unlike Dir it deliberately ignores
// YAKOS_DISPATCH_LOG.
//
// Use it for files that loosen a security default (the owner-only router
// policy) or that choose which credentials a subprocess runs under (the
// yakOS-owned CODEX_HOME profile). A project can set environment variables
// for the processes it spawns (a committed .claude/settings.json env block,
// K-129), so letting YAKOS_DISPATCH_LOG relocate those files would let a
// cloned repository plant its own policy or credential profile.
//
// An absolute $HOME is trusted as given, even when it lies inside a project
// directory (K-176, sec-356 Q1). That is deliberate: every reader of these
// files resolves the same $HOME, so a process whose HOME a project controls
// already sees the project's files as the user's own, and a writer that used
// the passwd entry instead would write a file the readers never look at. The
// writers (yakos models and router policy) are therefore a correctness and audit
// layer, not a boundary against code that can set the caller's environment.
// What stops an agent from running them is the budget-guard hook.
//
// It returns "" when no home directory can be determined; callers must then
// treat the feature as off rather than fall back to a temp directory.
func TrustedDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, stateDirName)
}

// OwnedByCurrentUser reports whether fi is owned by the effective user. It is
// a no-op (true) on Windows, where ownership is expressed through ACLs.
func OwnedByCurrentUser(fi os.FileInfo) bool { return ownedByCurrentUser(fi) }
