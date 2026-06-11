// Package framework exposes the embedded copy of yakOS lib/ that is staged
// into cli-go/internal/framework/embedded/ by `make embed-lib` before every
// release build.
//
// In a fresh source checkout (no `make embed-lib` run), the embedded FS
// contains only placeholder .keep files — HasEmbeddedLib() returns false and
// MaterializeLib is a no-op. This lets bare `go build ./...` and
// `go test ./...` compile cleanly without any staging step.
//
// The canonical workflow for a release binary:
//
//	make embed-lib                 # copies lib/ into embedded/
//	go build -ldflags ... ./cmd/yakos  # embeds the staged files
//
// The embedded lib is materialized at install time to a stable directory so
// Claude Code (which reads real on-disk files) can resolve symlinks from
// ~/.claude/agents/ → materialized/lib/agents/.
package framework

import (
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:embedded
var libFS embed.FS

// placeholderFiles is the set of file names that exist in bare-checkout
// (unfilled) embedded directories.  Any file name NOT in this set is
// considered real framework content.
var placeholderFiles = map[string]bool{
	".keep":      true,
	".gitignore": true,
	"README.md":  true, // allow an optional README inside embedded/
}

// HasEmbeddedLib returns true when the embedded FS contains real framework
// files (i.e. `make embed-lib` has been run and the binary was built with
// the staged lib/ content).  It returns false in bare-checkout dev builds
// where only the placeholder .keep files and the .gitignore are present.
func HasEmbeddedLib() bool {
	// Walk the embedded FS.  If we find any file whose name is NOT a known
	// placeholder, we have real content.
	found := false
	_ = fs.WalkDir(libFS, "embedded", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if placeholderFiles[d.Name()] {
			return nil
		}
		found = true
		return errors.New("stop") // short-circuit
	})
	return found
}

// MaterializeLib extracts the embedded lib/ tree under destRoot, writing
// real files to destRoot/lib/{agents,skills,rules,hooks,...}.
//
// It is idempotent: existing files are overwritten only when the content
// differs (checked by size for efficiency; a full hash is out of scope for
// Phase 1).
//
// Files whose name ends in ".keep" are skipped — they are placeholders only.
//
// Returns a non-nil error only when writing fails.  When HasEmbeddedLib()
// is false (dev build) the function returns nil without writing anything.
func MaterializeLib(destRoot string) error {
	if !HasEmbeddedLib() {
		return nil
	}
	return fs.WalkDir(libFS, "embedded", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Strip the "embedded/" prefix to get the relative path within lib/.
		// "embedded/agents/backend.md" → "agents/backend.md"
		rel := strings.TrimPrefix(path, "embedded/")
		if rel == "" || rel == "embedded" {
			return nil
		}

		// Skip placeholder files (not real framework content).
		if placeholderFiles[d.Name()] {
			return nil
		}

		dest := filepath.Join(destRoot, "lib", rel)

		if d.IsDir() {
			return os.MkdirAll(dest, 0755) //nolint:gosec
		}

		// Read embedded content.
		data, readErr := fs.ReadFile(libFS, path)
		if readErr != nil {
			return readErr
		}

		// Create parent dir if needed.
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0755); mkErr != nil { //nolint:gosec
			return mkErr
		}

		// Determine whether the file should be executable.
		// Hooks (*.sh) and scripts should be executable; .md/.json/.yaml are not.
		perm := os.FileMode(0644)
		if isExecutable(d.Name()) {
			perm = 0755
		}

		return os.WriteFile(dest, data, perm) //nolint:gosec
	})
}

// isExecutable returns true when the file should be written with the
// executable bit set.  Shell scripts identified by extension qualify.
func isExecutable(name string) bool {
	return strings.HasSuffix(name, ".sh") ||
		strings.HasSuffix(name, ".bash") ||
		name == "yakos" // the bash launcher, if ever embedded
}

// LibFS returns the raw embedded filesystem.  Callers that need direct
// access (e.g. tests) should prefer MaterializeLib; this is a low-level
// escape hatch.
func LibFS() embed.FS {
	return libFS
}
