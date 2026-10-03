package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorProjectRules(t *testing.T) {
	root := filepath.Join("..", "..", "..") // repo root: lib/rules exists
	if _, err := os.Stat(filepath.Join(root, "lib", "rules", "git-hygiene.md")); err != nil {
		t.Skip("framework rules not present")
	}
	proj := t.TempDir()
	var buf bytes.Buffer
	r := &runner{cfg: Config{Writer: &buf, YakosRoot: root, ProjectPath: proj}, w: &buf, home: t.TempDir(), env: func(string) string { return "" }, report: &Report{}}
	r.checkProjectRules()
	out := buf.String()
	if !strings.Contains(out, "git-hygiene.md: missing") || r.report.Warnings != 5 {
		t.Fatalf("want 5 missing warnings, got %d:\n%s", r.report.Warnings, out)
	}
}
