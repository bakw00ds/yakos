// write.go — atomic writer for kanban.md.
//
// Load-bearing contract: parse(write(x)) == x byte-for-byte.
// This means:
//   - All lines from RawLines are written verbatim, in order.
//   - A single trailing newline is appended (matching bash behavior:
//     awk always terminates its output with a newline, and the bash
//     "cat <<EOF" heredoc in _kanban_seed ends with a newline).
//   - No other whitespace, line-ending, or encoding changes are made.
//
// Atomic strategy (Decision Q8): temp-rename only. No flock in Phase 1.
// The temp file is written to <path>.tmp in the same directory as <path>
// so the rename is guaranteed to be on the same filesystem (atomic on POSIX).
package kanban

import (
	"fmt"
	"os"
	"strings"
)

// Save writes the board to path using an atomic temp-rename.
// The .schema-version sidecar is also written/updated by SaveSchema.
// If path does not exist, Save creates it (including any needed parent
// directories that already exist — it does not mkdir -p).
//
// After Save returns nil, the caller can be certain that:
//   - path contains exactly the lines in b.RawLines, joined by "\n",
//     with a single trailing "\n" appended.
//   - A .kanban.schema-version sidecar exists next to path.
func (b *Board) Save(path string) error {
	content := strings.Join(b.RawLines, "\n") + "\n"

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil { //nolint:gosec
		return fmt.Errorf("kanban: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("kanban: rename %s: %w", path, err)
	}

	// Write the schema sidecar alongside the board file.
	if err := SaveSchema(path); err != nil {
		// Non-fatal: the board is already written correctly. Log-quality error.
		return fmt.Errorf("kanban: board saved but schema sidecar failed: %w", err)
	}
	return nil
}
