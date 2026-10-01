package refresh

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupRulesFixture(t *testing.T) (root, proj string) {
	t.Helper()
	root = t.TempDir()
	proj = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range specialistRules {
		if err := os.WriteFile(filepath.Join(root, "lib", "rules", n), []byte("RULE "+n+" CANARY-7f3a"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, proj
}

func TestSyncProjectRules_InstallsManagedCopies(t *testing.T) {
	root, proj := setupRulesFixture(t)
	rpt, err := syncProjectRules(root, proj, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if rpt.New != len(specialistRules) {
		t.Fatalf("New=%d want %d", rpt.New, len(specialistRules))
	}
	for _, n := range specialistRules {
		p := filepath.Join(proj, ".claude", "rules", n)
		fi, _ := os.Lstat(p)
		if fi == nil || fi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s must be a regular file (claude ignores out-of-project symlinks)", n)
		}
		b, _ := os.ReadFile(p)
		if !strings.Contains(string(b), "CANARY-7f3a") {
			t.Errorf("%s missing canary content", n)
		}
	}
	rpt, _ = syncProjectRules(root, proj, false, io.Discard)
	if rpt.New != 0 || rpt.OK != len(specialistRules) {
		t.Errorf("idempotence: %+v", rpt)
	}
	// upstream change is picked up on the next refresh
	_ = os.WriteFile(filepath.Join(root, "lib", "rules", "git-hygiene.md"), []byte("UPDATED"), 0o644)
	rpt, _ = syncProjectRules(root, proj, false, io.Discard)
	b, _ := os.ReadFile(filepath.Join(proj, ".claude", "rules", "git-hygiene.md"))
	if rpt.New != 1 || !strings.HasPrefix(string(b), "UPDATED") {
		t.Errorf("managed copy not updated: %+v %q", rpt, b)
	}
}

func TestSyncProjectRules_DryRunWritesNothing(t *testing.T) {
	root, proj := setupRulesFixture(t)
	rpt, _ := syncProjectRules(root, proj, true, io.Discard)
	if rpt.New != len(specialistRules) {
		t.Errorf("dry-run should report drift: %+v", rpt)
	}
	if _, err := os.Stat(filepath.Join(proj, ".claude")); !os.IsNotExist(err) {
		t.Error("dry-run created .claude")
	}
}

func TestSyncProjectRules_KeepsProjectOwnedFile(t *testing.T) {
	root, proj := setupRulesFixture(t)
	d := filepath.Join(proj, ".claude", "rules")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "git-hygiene.md"), []byte("project override"), 0o644)
	rpt, _ := syncProjectRules(root, proj, false, io.Discard)
	if rpt.Warns != 1 || rpt.New != len(specialistRules)-1 {
		t.Errorf("warns=%d", rpt.Warns)
	}
	b, _ := os.ReadFile(filepath.Join(d, "git-hygiene.md"))
	if string(b) != "project override" {
		t.Errorf("project-owned file overwritten: %q", b)
	}
}

func TestSyncProjectRules_RepairsStaleSymlink(t *testing.T) {
	root, proj := setupRulesFixture(t)
	d := filepath.Join(proj, ".claude", "rules")
	_ = os.MkdirAll(d, 0o755)
	if err := os.Symlink("/nonexistent/old", filepath.Join(d, "commit-format.md")); err != nil {
		t.Skip("symlinks unsupported")
	}
	rpt, _ := syncProjectRules(root, proj, false, io.Discard)
	if rpt.New != len(specialistRules) {
		t.Errorf("stale link not repaired: %+v", rpt)
	}
	if fi, _ := os.Lstat(filepath.Join(d, "commit-format.md")); fi == nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Error("legacy symlink must be replaced by a regular file")
	}
}

// Real framework rules must exist for every listed name.
func TestSpecialistRules_ExistInFramework(t *testing.T) {
	for _, n := range specialistRules {
		if _, err := os.Stat(filepath.Join("..", "..", "..", "lib", "rules", n)); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}
