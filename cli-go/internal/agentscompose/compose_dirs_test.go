package agentscompose

// compose_dirs_test.go — the project's agent and skill directories are refused
// when they are links (rev-324). A file seen through a linked directory is a
// regular file, so it never reaches the symlink rule for files, and a root that is
// itself a link resolves outside by identity. So `.claude/agents` or
// `.claude/skills` that is a symlink, or has a symlinked `.claude` above it, is
// skipped whole, once, with one warning, wherever it leads: outside the project,
// to another directory of the project, to nothing, or to a file. The framework's
// own root and its lib/agents and lib/skills are not the project's, may be links
// (a bare install or a re-pointed upgrade leaves them so), and still compose.

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

// markerFile puts into dir what a read of the directory would carry to the roster
// (kind agents) or to the skills listing (kind skills): a file with a marker.
func markerFile(t *testing.T, dir, kind, where string) {
	t.Helper()
	switch kind {
	case "agents":
		writeFileT(t, filepath.Join(dir, "evil.md"), "---\nid: evil\n---\n\n## Purpose\n\n"+where+"-AGENT-MARKER\n")
	case "skills":
		writeFileT(t, filepath.Join(dir, "evil", "SKILL.md"), skillBody("evil", where+"-SKILL-MARKER"))
	}
}

// outsideTree is a directory outside any project with an agent and a skill in the
// layout of a .claude directory, each carrying a marker.
func outsideTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	markerFile(t, filepath.Join(dir, "agents"), "agents", "OUTSIDE")
	markerFile(t, filepath.Join(dir, "skills"), "skills", "OUTSIDE")
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

// linkTargets are the places a project directory link can lead. Each makes the
// directory it leads to, for one kind (agents or skills), with a file in it that
// would reach the roster or the listing, carrying a marker, if the directory were
// read.
var linkTargets = []struct {
	name   string
	target func(t *testing.T, project, kind string) string
}{
	{"outside the project", func(t *testing.T, project, kind string) string {
		return filepath.Join(outsideTree(t), kind)
	}},
	{"another directory of the project", func(t *testing.T, project, kind string) string {
		dir := filepath.Join(project, "shared", kind)
		markerFile(t, dir, kind, "INSIDE")
		return dir
	}},
	{"nothing", func(t *testing.T, project, kind string) string {
		return filepath.Join(project, "nothing-"+kind)
	}},
	{"a file", func(t *testing.T, project, kind string) string {
		file := filepath.Join(project, "notes-"+kind+".txt")
		writeFileT(t, file, "not a directory\n")
		return file
	}},
}

// The directory is a link, wherever it leads: skipped whole, once, with the one
// warning that names it. Its files never reach the roster or the listing. A link
// into the project is refused like one out of it.
func TestCompose_SkipsAProjectDirectoryThatIsALinkWhereverItLeads(t *testing.T) {
	for _, lt := range linkTargets {
		t.Run(lt.name, func(t *testing.T) {
			warnings := captureWarnings(t)
			root, project := dirsFixture(t)
			agentsLink := filepath.Join(project, ".claude", "agents")
			skillsLink := filepath.Join(project, ".claude", "skills")
			symlinkOrSkip(t, lt.target(t, project, "agents"), agentsLink)
			symlinkOrSkip(t, lt.target(t, project, "skills"), skillsLink)

			agents, skills, encoded := composeRosterAndSkills(t, root, project)
			if agents != "backend" || skills != "fw" || strings.Contains(encoded, "MARKER") {
				t.Errorf("agents = %q, skills = %q, encoded = %s; want backend and fw only", agents, skills, encoded)
			}
			requireDirWarning(t, warnings.String(), "agent", agentsLink, DirLinkReason)
			requireDirWarning(t, warnings.String(), "skill", skillsLink, DirLinkReason)
			if n := strings.Count(warnings.String(), "WARN"); n != 2 {
				t.Errorf("%d warnings, want one per directory: %q", n, warnings.String())
			}
		})
	}
}

// A `.claude` that is a link refuses both directories under it, each with its own
// warning, wherever it leads.
func TestCompose_SkipsBothDirectoriesUnderALinkedDotClaude(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target func(t *testing.T, project string) string
	}{
		{"outside the project", func(t *testing.T, project string) string { return outsideTree(t) }},
		{"another directory of the project", func(t *testing.T, project string) string {
			dir := filepath.Join(project, "dotclaude")
			markerFile(t, filepath.Join(dir, "agents"), "agents", "INSIDE")
			markerFile(t, filepath.Join(dir, "skills"), "skills", "INSIDE")
			return dir
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warnings := captureWarnings(t)
			root, project := dirsFixture(t)
			symlinkOrSkip(t, tc.target(t, project), filepath.Join(project, ".claude"))

			agents, skills, encoded := composeRosterAndSkills(t, root, project)
			if agents != "backend" || skills != "fw" || strings.Contains(encoded, "MARKER") {
				t.Errorf("agents = %q, skills = %q, encoded = %s; want backend and fw only", agents, skills, encoded)
			}
			requireDirWarning(t, warnings.String(), "agent", filepath.Join(project, ".claude", "agents"), DirLinkReason)
			requireDirWarning(t, warnings.String(), "skill", filepath.Join(project, ".claude", "skills"), DirLinkReason)
		})
	}
}

// A linked `.claude` is refused only where there is a directory under it to
// refuse: with no skills directory there is nothing to say about skills.
func TestCompose_ALinkedDotClaudeIsRefusedOnlyWhereThereIsADirectory(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	writeAgentDir(t, filepath.Join(project, "dotclaude", "agents"), map[string]string{"mine": "model: haiku\n"})
	symlinkOrSkip(t, "dotclaude", filepath.Join(project, ".claude"))

	agents, skills, _ := composeRosterAndSkills(t, root, project)
	if agents != "backend" || skills != "fw" {
		t.Errorf("agents = %q, skills = %q; want backend and fw", agents, skills)
	}
	requireDirWarning(t, warnings.String(), "agent", filepath.Join(project, ".claude", "agents"), DirLinkReason)
	if n := strings.Count(warnings.String(), "WARN"); n != 1 {
		t.Errorf("%d warnings, want only the one for the agents directory: %q", n, warnings.String())
	}
}

// A linked `.claude` that leads nowhere has nothing under it to refuse.
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

// One warning per directory however often the roster is composed: the daemon
// composes on every request.
func TestCompose_WarnsOncePerLinkedDirectory(t *testing.T) {
	warnings := captureWarnings(t)
	root, project := dirsFixture(t)
	link := filepath.Join(project, ".claude", "agents")
	symlinkOrSkip(t, filepath.Join(outsideTree(t), "agents"), link)
	for i := 0; i < 3; i++ {
		if _, err := Compose(root, project); err != nil {
			t.Fatalf("Compose = %v", err)
		}
	}
	requireDirWarning(t, warnings.String(), "agent", link, DirLinkReason)
}

// A plain directory, and no directory at all, are silent.
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

// The framework is not the project: a root reached through a link, and lib/agents
// and lib/skills that are links themselves, still compose, with no warning. A bare
// install or a re-pointed upgrade leaves them that way.
func TestCompose_TheFrameworkRootAndItsDirectoriesMayBeLinks(t *testing.T) {
	// the project is plain and has an agent and a skill of its own
	project := t.TempDir()
	writeAgentDir(t, filepath.Join(project, ".claude", "agents"), map[string]string{"mine": "model: haiku\n"})
	writeFileT(t, filepath.Join(project, ".claude", "skills", "mine", "SKILL.md"), skillBody("mine", "project skill"))

	t.Run("the root is a link", func(t *testing.T) {
		warnings := captureWarnings(t)
		root, _ := dirsFixture(t)
		linkedRoot := filepath.Join(t.TempDir(), "root")
		symlinkOrSkip(t, root, linkedRoot)
		agents, skills, _ := composeRosterAndSkills(t, linkedRoot, project)
		if agents != "backend,mine" || skills != "fw,mine" {
			t.Errorf("agents = %q, skills = %q; want backend,mine and fw,mine", agents, skills)
		}
		if warnings.Len() != 0 {
			t.Errorf("unexpected warnings: %q", warnings.String())
		}
	})

	t.Run("lib/agents and lib/skills are links", func(t *testing.T) {
		warnings := captureWarnings(t)
		shared := t.TempDir()
		writeAgentDir(t, filepath.Join(shared, "agents"), map[string]string{"backend": "model: sonnet\n"})
		writeFileT(t, filepath.Join(shared, "skills", "fw", "SKILL.md"), skillBody("fw", "framework skill"))
		root := t.TempDir()
		symlinkOrSkip(t, filepath.Join(shared, "agents"), filepath.Join(root, "lib", "agents"))
		symlinkOrSkip(t, filepath.Join(shared, "skills"), filepath.Join(root, "lib", "skills"))
		agents, skills, _ := composeRosterAndSkills(t, root, project)
		if agents != "backend,mine" || skills != "fw,mine" {
			t.Errorf("agents = %q, skills = %q; want backend,mine and fw,mine", agents, skills)
		}
		if warnings.Len() != 0 {
			t.Errorf("unexpected warnings: %q", warnings.String())
		}
	})

	t.Run("the root is a link and so is lib/agents", func(t *testing.T) {
		warnings := captureWarnings(t)
		shared := t.TempDir()
		writeAgentDir(t, filepath.Join(shared, "agents"), map[string]string{"backend": "model: sonnet\n"})
		root := t.TempDir()
		symlinkOrSkip(t, filepath.Join(shared, "agents"), filepath.Join(root, "lib", "agents"))
		linkedRoot := filepath.Join(t.TempDir(), "root")
		symlinkOrSkip(t, root, linkedRoot)
		if agents, _, _ := composeRosterAndSkills(t, linkedRoot, project); agents != "backend,mine" {
			t.Errorf("agents = %q; want backend,mine", agents)
		}
		if warnings.Len() != 0 {
			t.Errorf("unexpected warnings: %q", warnings.String())
		}
	})
}

// InspectProjectDir by itself, the function the validators call.
func TestInspectProjectDir(t *testing.T) {
	_, project := dirsFixture(t)
	plain := filepath.Join(project, ".claude", "agents")
	writeFileT(t, filepath.Join(plain, "a.md"), "x\n")
	if got := InspectProjectDir(project, plain); got != DirOK {
		t.Errorf("a plain directory: %v, want DirOK", got)
	}
	if got := InspectProjectDir("", plain); got != DirOK {
		t.Errorf("no project: %v, want DirOK", got)
	}
	if got := InspectProjectDir(project, filepath.Join(project, "missing")); got != DirOK {
		t.Errorf("a missing path: %v, want DirOK (nothing to refuse)", got)
	}

	// a link is refused wherever it leads
	outside := outsideTree(t)
	notes := filepath.Join(project, "notes.txt")
	writeFileT(t, notes, "not a directory\n")
	inside := filepath.Join(project, "shared")
	writeFileT(t, filepath.Join(inside, "a.md"), "x\n")
	for name, target := range map[string]string{
		"outside the project":    filepath.Join(outside, "agents"),
		"inside the project":     inside,
		"nothing":                filepath.Join(project, "gone"),
		"a file":                 notes,
		"the project itself":     project,
		"the directory's parent": filepath.Dir(plain),
	} {
		link := filepath.Join(project, "link to "+strings.ReplaceAll(name, " ", "-"))
		symlinkOrSkip(t, target, link)
		if got := InspectProjectDir(project, link); got != DirLinked {
			t.Errorf("a link to %s: %v, want DirLinked", name, got)
		}
	}

	// a linked .claude refuses what is under it, and only what is there
	linked := t.TempDir()
	writeFileT(t, filepath.Join(linked, "dotclaude", "agents", "a.md"), "x\n")
	symlinkOrSkip(t, "dotclaude", filepath.Join(linked, ".claude"))
	if got := InspectProjectDir(linked, filepath.Join(linked, ".claude", "agents")); got != DirLinked {
		t.Errorf("a directory under a linked .claude: %v, want DirLinked", got)
	}
	if got := InspectProjectDir(linked, filepath.Join(linked, ".claude", "skills")); got != DirOK {
		t.Errorf("an absent directory under a linked .claude: %v, want DirOK", got)
	}
	dangling := t.TempDir()
	symlinkOrSkip(t, filepath.Join(dangling, "gone"), filepath.Join(dangling, ".claude"))
	if got := InspectProjectDir(dangling, filepath.Join(dangling, ".claude", "agents")); got != DirOK {
		t.Errorf("a path under a dangling .claude: %v, want DirOK", got)
	}
}

// The text is one, in the type and in the constant, DirOK says nothing, and the
// text is written out here: the bash composer, both validators and the console
// print the same words, and a change of them anywhere is a failure.
func TestDirProblemReason(t *testing.T) {
	const want = "a symlinked directory is not followed (this directory or .claude is a symlink)"
	if DirLinkReason != want || DirLinked.Reason() != want || DirOK.Reason() != "" {
		t.Errorf("DirLinkReason = %q, DirLinked.Reason() = %q, DirOK.Reason() = %q; want %q and nothing", DirLinkReason, DirLinked.Reason(), DirOK.Reason(), want)
	}
}
