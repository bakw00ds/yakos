// schema.go — .schema-version sidecar reader/writer for kanban.md.
//
// Decision A (go-port-decisions-2026-06-02.md): use a sidecar .schema-version
// file instead of embedding a header in kanban.md, so that kanban.md remains
// byte-identical to the bash-written version. The sidecar lives at:
//
//	<kanban-dir>/.kanban.schema-version
//
// Format: a single line containing the version string, e.g. "1\n".
// Current version: "1".
//
// On read: a missing sidecar is treated as version "1" (legacy boards).
// On write: the sidecar is created/updated with an atomic temp-rename.
package kanban

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CurrentSchemaVersion is written by SaveSchema and expected by LoadSchema.
const CurrentSchemaVersion = "1"

// sidecarPath returns the sidecar path for a kanban.md at kanbanPath.
// The sidecar lives in the same directory as kanban.md.
func sidecarPath(kanbanPath string) string {
	return filepath.Join(filepath.Dir(kanbanPath), ".kanban.schema-version")
}

// LoadSchema reads the schema version from the sidecar file next to kanbanPath.
// If the sidecar does not exist, it returns CurrentSchemaVersion (treat legacy
// boards as v1). All other errors are returned to the caller.
func LoadSchema(kanbanPath string) (string, error) {
	p := sidecarPath(kanbanPath)
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return CurrentSchemaVersion, nil
	}
	if err != nil {
		return "", fmt.Errorf("kanban: read schema sidecar %s: %w", p, err)
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return CurrentSchemaVersion, nil
	}
	return v, nil
}

// SaveSchema writes the current schema version to the sidecar file next to
// kanbanPath. The write is atomic: a temp file is written first, then renamed.
// The sidecar is never written if the version already matches CurrentSchemaVersion
// AND the file already exists with that content — but for simplicity we always
// write it (the file is tiny and the write is idempotent).
func SaveSchema(kanbanPath string) error {
	p := sidecarPath(kanbanPath)
	content := CurrentSchemaVersion + "\n"

	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil { //nolint:gosec
		return fmt.Errorf("kanban: write schema sidecar tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("kanban: rename schema sidecar %s: %w", p, err)
	}
	return nil
}
