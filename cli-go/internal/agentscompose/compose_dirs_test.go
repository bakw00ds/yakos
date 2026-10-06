package agentscompose

// compose_dirs_test.go — the project's agent and skill directories are checked
// themselves (rev-324). A file seen through a linked directory is a regular file,
// so it never reaches the symlink rule for files, and a root that is itself a link
// resolves outside by identity. So `.claude/agents` or `.claude/skills` that is a
// symlink, or a symlinked `.claude` above it, must resolve to a directory inside
// the project, or the whole directory is skipped, once, with a warning. Inside
// the project it composes like any other directory.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// dirsFixture is a framework root with a backend agent and a fw skill, and an empty
// project.
func dirsFixture(t *testing.T) (root, project string) {
	t.Helper()
	root, project = t.TempDir(), t.TempDir()
	writeAgentDir(t, filepath.Join(root, "lib", "agents"), map[string]string{"backend": "model: sonnet\n"})
	writeFileT(t, filepath.Join(root, "lib", "skills", "fw", "SKILL.md"), skillBody("fw", "framework skill"))
	return root, project
}

// outsideTree is a directory outside any project with an agent and a skill in the
// layout of a .claude directory, each carrying a marker.
func outsideTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFileT(t, filepath.Join(dir, "agents", "evil.md"), "---\nid: evil\n---\n\n## Purpose\n\nOUTSIDE-AGENT-MARKER\n")
	writeFileT(t, filepath.Join(dir, "skills", "evil", "SKILL.md"), skillBody("evil", "OUTSIDE-SKILL-MARKER"))
	return dir
}

func requireDirWarning(t *testing.T, out, kind, dir, reason string) {
	t.Helper()
	line := "yakos: WARN: ignoring " + kind + " directory " + dir + ": " + reason + "\n"
	if strings.Count(out, line) != 1 {
		t.Errorf("want exactly one %q in:\n%s", line, out)
	}
}

func composeRosterAndSkills(t *testing.T, root, project string) (agentIDs, skillSlugs string, encoded string) {
	t.Helper()
	roster, err := Compose(root, project)
	if err != nil {
		t.Fatalf("Compose = %v", err)
	}
	skills, err := ComposeSkills(root, project)
	if err != nil {
		t.Fatalf("ComposeSkills = %v", err)
	}
	b, err := json.Marshal([]interface{}{roster, skills})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(rosterIDs(roster), ","), skillSlugsOf(skills), string(b)
}

func skillSlugsOf(skills []ComposedSkill) string {
	slugs := make([]string, 0, len(skills))
	for _, s := range skills {
		slugs = append(slugs, s.Slug)
	}
	return strings.Join(slugs, ",")
}

// The directory is a link to a directory outside the project: skipped whole, once,
// with a warning that names it. Its files never reach the roster or the listing.
func TestCompose_SkipsAProjectDirectoryLinkedOutsideTheProject(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	outside := outsideTree(t)
	agentsLink := filepath.Join(project, ".claude", "agents")
	skillsLink := filepath.Join(project, ".claude", "skills")
	symlinkOrSkip(t, filepath.Join(outside, "agents"), agentsLink)
	symlinkOrSkip(t, filepath.Join(outside, "skills"), skillsLink)

	agents, skills, encoded := composeRosterAndSkills(t, root, project)
	if agents != "backend" || skills != "fw" {
		t.Errorf("agents = %q, skills = %q; want backend and fw", agents, skills)
	}
	for _, marker := range []string{"OUTSIDE-AGENT-MARKER", "OUTSIDE-SKILL-MARKER", "evil"} {
		if strings.Contains(encoded, marker) {
			t.Errorf("%s from the linked directory reached the roster or the listing: %s", marker, encoded)
		}
	}
	requireDirWarning(t, warnings.String(), "agent", agentsLink, DirOutsideReason)
	requireDirWarning(t, warnings.String(), "skill", skillsLink, DirOutsideReason)

	// Once per directory, not once per request.
	warnings.Reset()
	composeRosterAndSkills(t, root, project)
	if warnings.Len() != 0 {
		t.Errorf("the second pass warned again: %q", warnings.String())
	}
}

// A `.claude` that is a link to an outside directory skips both directories under
// it, each with its own warning.
func TestCompose_SkipsProjectDirectoriesUnderALinkedDotClaudeOutsideTheProject(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	outside := outsideTree(t)
	symlinkOrSkip(t, outside, filepath.Join(project, ".claude"))

	agents, skills, encoded := composeRosterAndSkills(t, root, project)
	if agents != "backend" || skills != "fw" || strings.Contains(encoded, "MARKER") {
		t.Errorf("agents = %q, skills = %q, encoded = %s; want backend and fw only", agents, skills, encoded)
	}
	requireDirWarning(t, warnings.String(), "agent", filepath.Join(project, ".claude", "agents"), DirOutsideReason)
	requireDirWarning(t, warnings.String(), "skill", filepath.Join(project, ".claude", "skills"), DirOutsideReason)
}

// Linked to a directory inside the project is fine: it composes like a plain one,
// and the rule for files still holds inside it.
func TestCompose_ComposesAProjectDirectoryLinkedInsideTheProject(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	writeAgentDir(t, filepath.Join(project, "config", "agents"), map[string]string{"mine": "model: haiku\n"})
	writeFileT(t, filepath.Join(project, "config", "skills", "mine", "SKILL.md"), skillBody("mine", "project skill"))
	symlinkOrSkip(t, filepath.Join("..", "config", "agents"), filepath.Join(project, ".claude", "agents"))
	symlinkOrSkip(t, filepath.Join("..", "config", "skills"), filepath.Join(project, ".claude", "skills"))
	// the file rule applies inside it: a link to the project's .env is refused, a
	// link into lib/agents (the installed layout) is not
	writeFileT(t, filepath.Join(project, ".env"), secretText+"\n")
	symlinkOrSkip(t, filepath.Join("..", "..", ".env"), filepath.Join(project, "config", "agents", "dotenv.md"))
	symlinkOrSkip(t, filepath.Join(root, "lib", "agents", "backend.md"), filepath.Join(project, "config", "agents", "framework.md"))

	agents, skills, encoded := composeRosterAndSkills(t, root, project)
	if agents != "backend,framework,mine" || skills != "fw,mine" {
		t.Errorf("agents = %q, skills = %q; want backend,framework,mine and fw,mine", agents, skills)
	}
	if strings.Contains(encoded, "TOPSECRET") {
		t.Errorf("the .env text reached the roster: %s", encoded)
	}
	requireLine(t, warnings, filepath.Join(project, ".claude", "agents", "dotenv.md"), AgentOutsideReason)
	if n := strings.Count(warnings.String(), "WARN"); n != 1 {
		t.Errorf("%d warnings, want only the one for dotenv.md: %q", n, warnings.String())
	}
}

// `.claude` linked to a directory inside the project is fine too.
func TestCompose_ComposesUnderADotClaudeLinkedInsideTheProject(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	writeAgentDir(t, filepath.Join(project, "dotclaude", "agents"), map[string]string{"mine": "model: haiku\n"})
	writeFileT(t, filepath.Join(project, "dotclaude", "skills", "mine", "SKILL.md"), skillBody("mine", "project skill"))
	symlinkOrSkip(t, "dotclaude", filepath.Join(project, ".claude"))

	agents, skills, _ := composeRosterAndSkills(t, root, project)
	if agents != "backend,mine" || skills != "fw,mine" {
		t.Errorf("agents = %q, skills = %q; want backend,mine and fw,mine", agents, skills)
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// A linked `.claude` that holds only an agents directory is silent about the
// skills directory it does not have: nothing is read through a path that is not
// there, so there is nothing to warn about.
func TestCompose_ALinkedDotClaudeWithoutASkillsDirectoryIsSilent(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	writeAgentDir(t, filepath.Join(project, "dotclaude", "agents"), map[string]string{"mine": "model: haiku\n"})
	symlinkOrSkip(t, "dotclaude", filepath.Join(project, ".claude"))

	agents, skills, _ := composeRosterAndSkills(t, root, project)
	if agents != "backend,mine" || skills != "fw" {
		t.Errorf("agents = %q, skills = %q; want backend,mine and fw", agents, skills)
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// A linked `.claude` that points at nothing leaves no directory to read either.
func TestCompose_ADanglingDotClaudeLinkIsSilent(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	symlinkOrSkip(t, filepath.Join(project, "gone"), filepath.Join(project, ".claude"))

	agents, skills, _ := composeRosterAndSkills(t, root, project)
	if agents != "backend" || skills != "fw" {
		t.Errorf("agents = %q, skills = %q; want backend and fw", agents, skills)
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// A link to nothing, or to a file, does not resolve to a directory.
func TestCompose_SkipsAProjectDirectoryThatIsALinkToNothingOrToAFile(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	writeFileT(t, filepath.Join(project, "notes.txt"), "not a directory\n")
	agentsLink := filepath.Join(project, ".claude", "agents")
	skillsLink := filepath.Join(project, ".claude", "skills")
	symlinkOrSkip(t, filepath.Join(project, "does-not-exist"), agentsLink)
	symlinkOrSkip(t, filepath.Join(project, "notes.txt"), skillsLink)

	agents, skills, _ := composeRosterAndSkills(t, root, project)
	if agents != "backend" || skills != "fw" {
		t.Errorf("agents = %q, skills = %q; want backend and fw", agents, skills)
	}
	requireDirWarning(t, warnings.String(), "agent", agentsLink, DirUnresolvedReason)
	requireDirWarning(t, warnings.String(), "skill", skillsLink, DirUnresolvedReason)
}

// A plain directory, and no directory at all, are silent, and the framework's own
// directories are never subject to the rule.
func TestCompose_PlainAndMissingProjectDirectoriesAreSilent(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	if got, _, _ := composeRosterAndSkills(t, root, project); got != "backend" {
		t.Errorf("no project directories: agents = %q, want backend", got)
	}
	writeAgentDir(t, filepath.Join(project, ".claude", "agents"), map[string]string{"mine": "model: haiku\n"})
	if got, _, _ := composeRosterAndSkills(t, root, project); got != "backend,mine" {
		t.Errorf("a plain directory: agents = %q, want backend,mine", got)
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// InspectProjectDir by itself, the function the validators call.
func TestInspectProjectDir(t *testing.T) {
	_, project := dirsFixture(t)
	outside := outsideTree(t)
	plain := filepath.Join(project, ".claude", "agents")
	writeFileT(t, filepath.Join(plain, "a.md"), "x\n")
	if got := InspectProjectDir(project, plain); got != DirOK {
		t.Errorf("a plain directory: %v, want DirOK", got)
	}
	if got := InspectProjectDir("", plain); got != DirOK {
		t.Errorf("no project: %v, want DirOK", got)
	}
	link := filepath.Join(project, "elsewhere")
	symlinkOrSkip(t, filepath.Join(outside, "agents"), link)
	if got := InspectProjectDir(project, link); got != DirOutside {
		t.Errorf("a link out of the project: %v, want DirOutside", got)
	}
	if got := InspectProjectDir(project, filepath.Join(project, "missing")); got != DirOK {
		t.Errorf("a missing plain path: %v, want DirOK (nothing to resolve)", got)
	}
	// a link that points at nothing is a problem; a path that is not there under a
	// linked .claude is not
	dangling := filepath.Join(project, "dangling")
	symlinkOrSkip(t, filepath.Join(project, "gone"), dangling)
	if got := InspectProjectDir(project, dangling); got != DirUnresolved {
		t.Errorf("a dangling link: %v, want DirUnresolved", got)
	}
	linkedClaude := t.TempDir()
	writeFileT(t, filepath.Join(linkedClaude, "dotclaude", "agents", "a.md"), "x\n")
	symlinkOrSkip(t, "dotclaude", filepath.Join(linkedClaude, ".claude"))
	if got := InspectProjectDir(linkedClaude, filepath.Join(linkedClaude, ".claude", "skills")); got != DirOK {
		t.Errorf("an absent directory under a linked .claude: %v, want DirOK", got)
	}
	if got := InspectProjectDir(linkedClaude, filepath.Join(linkedClaude, ".claude", "agents")); got != DirOK {
		t.Errorf("a directory under a .claude linked inside the project: %v, want DirOK", got)
	}
}
