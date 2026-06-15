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
