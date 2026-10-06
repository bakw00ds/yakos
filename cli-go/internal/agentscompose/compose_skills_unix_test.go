//go:build !windows

package agentscompose

// compose_skills_unix_test.go — a FIFO as a SKILL.md, or a link to one, must not
// block the listing, and a SKILL.md that cannot be read is a skip in the project
// and an error in the framework, as for an agent file.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComposeSkills_AFIFOIsNeverOpened(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	entry := filepath.Join(skills, "pipe", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	mkfifoOrSkip(t, entry)
	target := filepath.Join(project, "pipe2")
	mkfifoOrSkip(t, target)
	linked := filepath.Join(skills, "pipelink", "SKILL.md")
	symlinkOrSkip(t, target, linked)

	type result struct {
		skills []ComposedSkill
		err    error
	}
	done := make(chan result, 1)
	go func() {
		s, err := ComposeSkills(root, project)
		done <- result{s, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("ComposeSkills = %v", res.err)
		}
		if skillSlugs(res.skills) != "fw,mine" {
			t.Errorf("skills = %q, want fw,mine", skillSlugs(res.skills))
		}
	case <-time.After(5 * time.Second):
		releaseFIFOForTest(entry)
		releaseFIFOForTest(target)
		t.Fatal("ComposeSkills is blocked on a FIFO")
	}
	requireSkillWarning(t, warnings.String(), entry, "not a regular file")
	requireSkillWarning(t, warnings.String(), linked, "symlink does not resolve to a regular file")
}

func TestComposeSkills_AnUnreadableSkillIsASkipInTheProjectAndAnErrorInTheFramework(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files whatever their mode")
	}
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	locked := filepath.Join(skills, "locked", "SKILL.md")
	writeFileT(t, locked, skillBody("locked", "locked"))
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })

	if got := composeSkillsOK(t, root, project); skillSlugs(got) != "fw,mine" {
		t.Errorf("skills = %q, want fw,mine", skillSlugs(got))
	}
	if !strings.Contains(warnings.String(), "ignoring skill file "+locked+": cannot be read") {
		t.Errorf("warnings %q do not report the unreadable skill", warnings.String())
	}

	sealed := filepath.Join(root, "lib", "skills", "sealed", "SKILL.md")
	writeFileT(t, sealed, skillBody("sealed", "sealed"))
	if err := os.Chmod(sealed, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o644) })
	if _, err := ComposeSkills(root, project); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Errorf("ComposeSkills = %v, want an error naming the unreadable framework skill", err)
	}
}
