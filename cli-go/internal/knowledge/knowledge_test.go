package knowledge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a framework root and a project with a fixed rule set.
func fixture(t *testing.T) (root, project string) {
	t.Helper()
	d := t.TempDir()
	root, project = filepath.Join(d, "fw"), filepath.Join(d, "proj")
	write(t, filepath.Join(root, "lib/rules/b-second.md"), "# Second\nbeta\n")
	write(t, filepath.Join(root, "lib/rules/a-first.md"), "# First\nalpha\n")
	write(t, filepath.Join(root, "lib/rules/scoped.md"), "---\npaths:\n  - \"**/*.go\"\n---\nscoped body\n")
	write(t, filepath.Join(root, "lib/rules/INDEX.md"), "index\n")
	write(t, filepath.Join(root, "lib/rules/fm.md"), "---\nname: fm\ndescription: d\n---\nfm body\n")
	write(t, filepath.Join(project, ".claude/rules/proj.md"), "project rule\n")
	write(t, filepath.Join(project, ".claude/rules/a-first.md"), "project override\n")
	return root, project
}

const expectedSHA = "00cc87ec77da2d1da75144c7b6718d6be2ed53e147260a3886031c3ef4332d75"

func opts(root, project string) Options {
	return Options{YakosRoot: root, Project: project, Agent: "backend", AgentBody: "You are backend.\n"}
}

func TestComposeContentAndOrder(t *testing.T) {
	root, project := fixture(t)
	p := Compose(opts(root, project))
	want := "## rule: b-second\n# Second\nbeta\n\n" +
		"## rule: fm\nfm body\n\n" +
		"## project-rule: a-first\nproject override\n\n" +
		"## project-rule: proj\nproject rule\n\n" +
		"## agent: backend\nYou are backend.\n\n"
	if p.Text != want {
		t.Fatalf("text mismatch:\n%q\nwant\n%q", p.Text, want)
	}
	if p.SHA != SHA(p.Text) {
		t.Fatal("sha is not the hash of the text")
	}
	for _, s := range []string{"scoped body", "index", "# First\nalpha"} {
		if strings.Contains(p.Text, s) {
			t.Errorf("pack must not contain %q", s)
		}
	}
}

// The same tree at another location (a restart, another checkout) composes to
// the same bytes: no path, time or id is in the block.
func TestComposeStableAcrossLocations(t *testing.T) {
	r1, p1 := fixture(t)
	r2, p2 := fixture(t)
	a, b := Compose(opts(r1, p1)), Compose(opts(r2, p2))
	if a.SHA != b.SHA || a.Text != b.Text {
		t.Fatal("block differs between two identical trees")
	}
	if got := Compose(opts(r1, p1)); got.SHA != a.SHA {
		t.Fatal("block differs between two composes of one tree")
	}
}

func TestComposeGoldenSHA(t *testing.T) {
	root, project := fixture(t)
	p := Compose(opts(root, project))
	if p.SHA != expectedSHA {
		t.Fatalf("sha = %s, want %s", p.SHA, expectedSHA)
	}
}

func TestTruncationOrder(t *testing.T) {
	d := t.TempDir()
	root, project := filepath.Join(d, "fw"), filepath.Join(d, "proj")
	big := strings.Repeat("x", 7000)
	for _, n := range []string{"a", "b", "c"} {
		write(t, filepath.Join(root, "lib/rules", n+".md"), big+"\n")
	}
	write(t, filepath.Join(project, ".claude/rules/p1.md"), big+"\n")
	write(t, filepath.Join(project, ".claude/rules/p2.md"), big+"\n")
	o := Options{YakosRoot: root, Project: project, Agent: "lead", AgentBody: "body\n"}
	p := Compose(o)
	if len(p.Text) > MaxBytes {
		t.Fatalf("over cap: %d", len(p.Text))
	}
	incl := map[string]bool{}
	for _, pt := range p.Parts {
		incl[pt.Name] = pt.Included
	}
	// 5 rules of ~7 KB + agent: only 3 fit. Framework rules go first, last by
	// name first: c, b, a; the project rules and the agent stay.
	if incl["c"] || incl["b"] || !incl["p1"] || !incl["p2"] || !incl["lead"] {
		t.Fatalf("wrong set kept: %v", incl)
	}
	if len(p.Parts) != 6 || p.Parts[0].Name != "a" {
		t.Fatalf("parts must list every source in order: %+v", p.Parts)
	}
}

func TestProjectRulesDroppedAfterFramework(t *testing.T) {
	d := t.TempDir()
	root, project := filepath.Join(d, "fw"), filepath.Join(d, "proj")
	big := strings.Repeat("y", 14000)
	write(t, filepath.Join(root, "lib/rules/f.md"), big+"\n")
	write(t, filepath.Join(project, ".claude/rules/p1.md"), big+"\n")
	write(t, filepath.Join(project, ".claude/rules/p2.md"), big+"\n")
	p := Compose(Options{YakosRoot: root, Project: project, Agent: "lead", AgentBody: "body\n"})
	incl := map[string]bool{}
	for _, pt := range p.Parts {
		incl[pt.Name] = pt.Included
	}
	if incl["f"] || incl["p2"] || !incl["p1"] || !incl["lead"] {
		t.Fatalf("order wrong: %v", incl)
	}
}

func TestAgentBodyTruncatedLast(t *testing.T) {
	root, _ := fixture(t)
	body := strings.Repeat("é", MaxBytes) // multi-byte: cut must stay valid UTF-8
	p := Compose(Options{YakosRoot: root, Agent: "lead", AgentBody: body})
	if len(p.Text) > MaxBytes {
		t.Fatalf("over cap: %d", len(p.Text))
	}
	if !strings.Contains(p.Text, TruncationMark) {
		t.Fatal("no truncation mark")
	}
	last := p.Parts[len(p.Parts)-1]
	if last.Kind != KindAgent || !last.Truncated || !last.Included {
		t.Fatalf("agent part: %+v", last)
	}
	for _, pt := range p.Parts[:len(p.Parts)-1] {
		if pt.Included {
			t.Fatalf("rule %s should have been dropped first", pt.Name)
		}
	}
	if strings.ToValidUTF8(p.Text, "\x00") != p.Text {
		t.Fatal("cut inside a rune")
	}
}

func TestSecretRefusedFromPack(t *testing.T) {
	root, project := fixture(t)
	key := "AKIA" + "ABCDEFGHIJKLMNOP"
	write(t, filepath.Join(root, "lib/rules/leaky.md"), "token "+key+"\n")
	p := Compose(Options{YakosRoot: root, Project: project, Agent: "a", AgentBody: "ok " + key + "\n"})
	if strings.Contains(p.Text, key) || strings.Contains(p.Text, "leaky") {
		t.Fatal("secret file reached the pack")
	}
	var warnedRule, warnedAgent bool
	for _, w := range p.Warnings {
		if strings.Contains(w, key) {
			t.Fatalf("warning carries the secret: %q", w)
		}
		warnedRule = warnedRule || strings.Contains(w, "leaky")
		warnedAgent = warnedAgent || strings.Contains(w, "agent a")
	}
	if !warnedRule || !warnedAgent {
		t.Fatalf("warnings: %v", p.Warnings)
	}
}

func TestControlCharsRemoved(t *testing.T) {
	root, _ := fixture(t)
	p := Compose(Options{YakosRoot: root, Agent: "a", AgentBody: "x\x00y\x1b[0m\r\nz\xff\n"})
	for _, r := range p.Text {
		if r < 0x20 && r != '\n' && r != '\t' {
			t.Fatalf("control char %U in block", r)
		}
	}
}

func TestProjectRulesSymlinkedDirSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root, project := fixture(t)
	other := t.TempDir()
	write(t, filepath.Join(other, "evil.md"), "evil rule\n")
	if err := os.RemoveAll(filepath.Join(project, ".claude/rules")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(project, ".claude/rules")); err != nil {
		t.Fatal(err)
	}
	p := Compose(opts(root, project))
	if strings.Contains(p.Text, "evil") {
		t.Fatal("followed a symlinked project rules directory")
	}
}

func TestSymlinkedRuleFileIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root, project := fixture(t)
	secret := filepath.Join(t.TempDir(), "outside.md")
	write(t, secret, "outside text\n")
	if err := os.Symlink(secret, filepath.Join(project, ".claude/rules/link.md")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(Compose(opts(root, project)).Text, "outside text") {
		t.Fatal("followed a symlinked rule file")
	}
}

func TestSkillText(t *testing.T) {
	root, project := fixture(t)
	write(t, filepath.Join(root, "lib/skills/demo/SKILL.md"), "---\nname: demo\n---\nstep one\n")
	write(t, filepath.Join(project, ".claude/skills/own/SKILL.md"), "own steps\n")
	got, err := SkillText(root, project, "demo")
	if err != nil || got != "\n\n---\nSkill /demo (SKILL.md):\nstep one\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, _ := SkillText(root, project, "own"); !strings.Contains(got, "own steps") {
		t.Fatalf("project skill: %q", got)
	}
	for _, bad := range []string{"../demo", "nope", "", "a/b", "DEMO"} {
		if _, err := SkillText(root, project, bad); err != ErrNotFound {
			t.Errorf("slug %q: %v", bad, err)
		}
	}
	write(t, filepath.Join(root, "lib/skills/leak/SKILL.md"), "AKIA"+"ABCDEFGHIJKLMNOP\n")
	if _, err := SkillText(root, project, "leak"); err != ErrSecret {
		t.Fatalf("secret skill: %v", err)
	}
	write(t, filepath.Join(root, "lib/skills/huge/SKILL.md"), strings.Repeat("z", MaxSkillBytes+1))
	if _, err := SkillText(root, project, "huge"); err == nil || err == ErrNotFound {
		t.Fatalf("oversize skill: %v", err)
	}
}

func TestReadSoul(t *testing.T) {
	h := t.TempDir()
	if ReadSoul(h) != "" || ReadSoul("") != "" {
		t.Fatal("absent soul must be empty")
	}
	write(t, filepath.Join(h, ".yakos-state/soul/global.md"), "soul text\n")
	if ReadSoul(h) != "soul text\n" {
		t.Fatal("soul not read")
	}
}

// F3: a key split by a control character is joined by clean, so the scan must
// see the cleaned text too.
func TestSecretSplitByControlCharRefused(t *testing.T) {
	root, project := fixture(t)
	split := "AKIA" + "\x01" + "ABCDEFGHIJKLMNOP"
	joined := "AKIA" + "ABCDEFGHIJKLMNOP"
	write(t, filepath.Join(project, ".claude/rules/split.md"), "token "+split+"\n")
	write(t, filepath.Join(root, "lib/skills/leak/SKILL.md"), "use "+split+"\n")
	p := Compose(Options{YakosRoot: root, Project: project, Agent: "a", AgentBody: "ok " + split + "\n"})
	if strings.Contains(p.Text, joined) || strings.Contains(p.Text, "split") || strings.Contains(p.Text, "## agent") {
		t.Fatalf("split secret reached the pack: %q", p.Text)
	}
	if _, err := SkillText(root, project, "leak"); err != ErrSecret {
		t.Fatalf("skill with a split secret: %v", err)
	}
}

// F4: a project rule that replaces a framework rule says so in the parts, and
// the displaced rule is listed, not included.
func TestReplacedFrameworkRuleListed(t *testing.T) {
	root, project := fixture(t)
	p := Compose(opts(root, project))
	var repl, displaced *Part
	for i := range p.Parts {
		if p.Parts[i].Name == "a-first" && p.Parts[i].Kind == KindProjectRule {
			repl = &p.Parts[i]
		}
		if p.Parts[i].Name == "a-first" && p.Parts[i].Kind == KindRule {
			displaced = &p.Parts[i]
		}
	}
	if repl == nil || repl.Note != "replaces: a-first" || !repl.Included {
		t.Fatalf("project rule part: %+v", repl)
	}
	if displaced == nil || displaced.Included || displaced.Note == "" {
		t.Fatalf("displaced framework rule part: %+v", displaced)
	}
	if strings.Contains(p.Text, "alpha") {
		t.Fatal("displaced rule text is in the pack")
	}
}

// S2: a body far over the cap reports the size the pack carries.
func TestLongAgentBodyPartWithinCap(t *testing.T) {
	root, _ := fixture(t)
	p := Compose(Options{YakosRoot: root, Agent: "big", AgentBody: strings.Repeat("x", 100<<10)})
	for _, part := range p.Parts {
		if part.Kind == KindAgent && (part.Bytes > MaxBytes || !part.Truncated) {
			t.Fatalf("agent part: %+v", part)
		}
	}
	if len(p.Text) > MaxBytes {
		t.Fatalf("pack %d over the cap", len(p.Text))
	}
}
