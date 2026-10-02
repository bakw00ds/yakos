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

func TestSyncProjectRules_RefusesSymlinkedRulesDir(t *testing.T) {
	root, proj := setupRulesFixture(t)
	outside := t.TempDir()
	_ = os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	if err := os.Symlink(outside, filepath.Join(proj, ".claude", "rules")); err != nil {
		t.Skip("symlinks unsupported")
	}
	_, err := syncProjectRules(root, proj, false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want symlink refusal, got %v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote outside the project: %v", entries)
	}
}

func TestSyncProjectRules_RefusesSymlinkedClaudeDir(t *testing.T) {
	root, proj := setupRulesFixture(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(proj, ".claude")); err != nil {
		t.Skip("symlinks unsupported")
	}
	if _, err := syncProjectRules(root, proj, false, io.Discard); err == nil {
		t.Fatal("want refusal for symlinked .claude")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("wrote outside the project: %v", entries)
	}
}

// Go and bash must agree: only a marker on the LAST line makes a file managed.
func TestIsManaged_MarkerOnlyOnLastLine(t *testing.T) {
	m := managedMarkerPrefix + "abc -->"
	if isManaged([]byte(m + "\nproject text\n")) {
		t.Error("marker on line 1 must not count as managed")
	}
	if !isManaged([]byte("text\n" + m + "\n")) {
		t.Error("marker on last line must count as managed")
	}
	if isManaged([]byte("text\n" + m + "\nmore\n")) {
		t.Error("marker followed by more text must not count")
	}
}

func TestCheckProjectRules(t *testing.T) {
	root, proj := setupRulesFixture(t)
	kinds := func() map[string]string {
		m := map[string]string{}
		for _, i := range CheckProjectRules(root, proj) {
			m[i.Rule] = i.Kind
		}
		return m
	}
	if got := kinds(); len(got) != len(specialistRules) || got["git-hygiene.md"] != "missing" {
		t.Fatalf("fresh project: %v", got)
	}
	if _, err := syncProjectRules(root, proj, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := kinds(); len(got) != 0 {
		t.Fatalf("clean install should have no issues: %v", got)
	}
	d := filepath.Join(proj, ".claude", "rules")
	// edited
	b, _ := os.ReadFile(filepath.Join(d, "commit-format.md"))
	_ = os.WriteFile(filepath.Join(d, "commit-format.md"), []byte("EDIT"+string(b)), 0o644)
	// marker stripped
	_ = os.WriteFile(filepath.Join(d, "pr-conventions.md"), []byte("no marker here\n"), 0o644)
	// stale (upstream changed)
	_ = os.WriteFile(filepath.Join(root, "lib", "rules", "git-hygiene.md"), []byte("NEW"), 0o644)
	// removed
	_ = os.Remove(filepath.Join(d, "secret-handling.md"))
	got := kinds()
	want := map[string]string{"commit-format.md": "edited", "pr-conventions.md": "marker-stripped", "git-hygiene.md": "stale", "secret-handling.md": "missing"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q (all: %v)", k, got[k], v, got)
		}
	}
	if _, ok := got["verification-discipline.md"]; ok {
		t.Errorf("untouched rule reported: %v", got)
	}
}
