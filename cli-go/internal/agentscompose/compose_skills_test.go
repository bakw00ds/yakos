package agentscompose

// compose_skills_test.go — ComposeSkills reads a SKILL.md under the rules for an
// agent file (sec-324), and a skill that may not be read is skipped with the
// once-per-file warning while the listing goes on. Before, a symlinked SKILL.md
// was read wherever it led, and one that pointed at a directory failed the whole
// listing, which GET /api/skills then served as an empty one. The FIFO and
// permission cases are in compose_skills_unix_test.go.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func skillBody(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n\n## Purpose\n\nBody.\n"
}

// skillsFixture is a framework root with one skill and a project with one skill.
// It returns the project's skills directory too.
func skillsFixture(t *testing.T) (root, project, skills string) {
	t.Helper()
	root, project = t.TempDir(), t.TempDir()
	writeFileT(t, filepath.Join(root, "lib", "skills", "fw", "SKILL.md"), skillBody("fw", "framework skill"))
	skills = filepath.Join(project, ".claude", "skills")
	writeFileT(t, filepath.Join(skills, "mine", "SKILL.md"), skillBody("mine", "project skill"))
	return root, project, skills
}

func skillSlugs(skills []ComposedSkill) string {
	slugs := make([]string, 0, len(skills))
	for _, s := range skills {
		slugs = append(slugs, s.Slug)
	}
	return strings.Join(slugs, ",")
}

// composeSkillsOK composes the listing and requires that it succeeds.
func composeSkillsOK(t *testing.T, root, project string) []ComposedSkill {
	t.Helper()
	skills, err := ComposeSkills(root, project)
	if err != nil {
		t.Fatalf("ComposeSkills = %v; one bad skill must not fail the listing", err)
	}
	return skills
}

func requireSkillWarning(t *testing.T, out, file, reason string) {
	t.Helper()
	line := "yakos: WARN: ignoring skill file " + file + ": " + reason + "\n"
	if strings.Count(out, line) != 1 {
		t.Errorf("want exactly one %q in:\n%s", line, out)
	}
}

func TestComposeSkills_SkipsASymlinkedSkillOutsideTheRoots(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	outside := filepath.Join(t.TempDir(), "credentials")
	writeFileT(t, outside, "---\nname: SECRET-NAME\ndescription: "+secretText+"\n---\n")
	link := filepath.Join(skills, "leak", "SKILL.md")
	symlinkOrSkip(t, outside, link)

	got := composeSkillsOK(t, root, project)
	if skillSlugs(got) != "fw,mine" {
		t.Errorf("skills = %q, want fw,mine", skillSlugs(got))
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "TOPSECRET") || strings.Contains(string(encoded), "SECRET-NAME") {
		t.Errorf("the outside file's content reached the listing: %s", encoded)
	}
	requireSkillWarning(t, warnings.String(), link, SkillOutsideReason)
}

// The project holds files that are not skills, and so does the framework. A link
// to the project's .env or .git/config, to another file of the project, or to an
// agent file is refused: the roots are the skill directories themselves.
func TestComposeSkills_SkipsALinkToAFileOutsideTheSkillDirectories(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	writeFileT(t, filepath.Join(project, ".env"), "---\nname: ENV-NAME\ndescription: "+secretText+"\n---\n")
	writeFileT(t, filepath.Join(project, ".git", "config"), "---\nname: GIT-NAME\ndescription: TOKEN-9999\n---\n")
	writeFileT(t, filepath.Join(project, "docs", "SKILL.md"), skillBody("DOCS-NAME", "DOCS-MARKER"))
	writeFileT(t, filepath.Join(root, "lib", "agents", "agent.md"), "---\nname: AGENT-NAME\ndescription: AGENT-MARKER\n---\n")
	links := map[string]string{
		"env":   filepath.Join(project, ".env"),
		"git":   filepath.Join(project, ".git", "config"),
		"docs":  filepath.Join(project, "docs", "SKILL.md"),
		"agent": filepath.Join(root, "lib", "agents", "agent.md"),
	}
	for slug, target := range links {
		symlinkOrSkip(t, target, filepath.Join(skills, slug, "SKILL.md"))
	}

	got := composeSkillsOK(t, root, project)
	if skillSlugs(got) != "fw,mine" {
		t.Errorf("skills = %q, want fw,mine", skillSlugs(got))
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"TOPSECRET", "ENV-NAME", "GIT-NAME", "TOKEN-9999", "DOCS-NAME", "DOCS-MARKER", "AGENT-NAME", "AGENT-MARKER"} {
		if strings.Contains(string(encoded), marker) {
			t.Errorf("%s reached the listing: %s", marker, encoded)
		}
	}
	for slug := range links {
		requireSkillWarning(t, warnings.String(), filepath.Join(skills, slug, "SKILL.md"), SkillOutsideReason)
	}
}

// A SKILL.md that is a link to a directory, or to nothing, used to fail the whole
// listing.
func TestComposeSkills_ASymlinkThatDoesNotResolveToAFileKeepsTheListing(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	dir := filepath.Join(project, "somedir")
	writeFileT(t, filepath.Join(dir, "x"), "x")
	dirLink := filepath.Join(skills, "dir", "SKILL.md")
	symlinkOrSkip(t, dir, dirLink)
	ghostLink := filepath.Join(skills, "ghost", "SKILL.md")
	symlinkOrSkip(t, filepath.Join(project, "nothing"), ghostLink)

	if got := composeSkillsOK(t, root, project); skillSlugs(got) != "fw,mine" {
		t.Errorf("skills = %q, want fw,mine", skillSlugs(got))
	}
	requireSkillWarning(t, warnings.String(), dirLink, "symlink does not resolve to a regular file")
	requireSkillWarning(t, warnings.String(), ghostLink, "symlink does not resolve to a regular file")
}

// Links inside the skill directories are the installed layout and work: one to
// another skill in the project's .claude/skills, and one into lib/skills.
func TestComposeSkills_FollowsSymlinksInsideTheSkillDirectories(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	writeFileT(t, filepath.Join(skills, "shared", "SKILL.md"), skillBody("shared", "shared skill"))
	symlinkOrSkip(t, filepath.Join("..", "shared", "SKILL.md"), filepath.Join(skills, "inproject", "SKILL.md"))
	symlinkOrSkip(t, filepath.Join(root, "lib", "skills", "fw", "SKILL.md"), filepath.Join(skills, "inlib", "SKILL.md"))

	got := composeSkillsOK(t, root, project)
	if skillSlugs(got) != "fw,inlib,inproject,mine,shared" {
		t.Fatalf("skills = %q, want fw,inlib,inproject,mine,shared", skillSlugs(got))
	}
	for _, s := range got {
		switch s.Slug {
		case "inproject":
			if s.Description != "shared skill" {
				t.Errorf("inproject was not read through the link: %+v", s)
			}
		case "inlib":
			if s.Description != "framework skill" {
				t.Errorf("inlib was not read through the link: %+v", s)
			}
		}
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

func TestComposeSkills_SkipsASkillOverTheSizeCap(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	head := skillBody("big", "too big")
	line := strings.Repeat("x", 99) + "\n"
	big := filepath.Join(skills, "big", "SKILL.md")
	writeFileT(t, big, head+strings.Repeat(line, (MaxAgentFileBytes-len(head))/len(line)+2))

	if got := composeSkillsOK(t, root, project); skillSlugs(got) != "fw,mine" {
		t.Errorf("skills = %q, want fw,mine", skillSlugs(got))
	}
	requireSkillWarning(t, warnings.String(), big, "larger than 4194304 bytes")
}

// A skill directory without a SKILL.md is not a skill: skipped silently, as ever.
func TestComposeSkills_ADirectoryWithoutASkillFileIsSkippedSilently(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, skills := skillsFixture(t)
	writeFileT(t, filepath.Join(skills, "notes", "README.md"), "just notes\n")

	if got := composeSkillsOK(t, root, project); skillSlugs(got) != "fw,mine" {
		t.Errorf("skills = %q, want fw,mine", skillSlugs(got))
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}
