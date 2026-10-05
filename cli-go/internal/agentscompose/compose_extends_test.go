package agentscompose

// compose_extends_test.go — `extends:` names a framework template and nothing
// else (sec-324, rev-324). The value came from the agent's own file, so a project
// could use it to read any .md file the daemon can reach (one outside lib/agents
// put its text into claude's command line) or to point the extends step at a huge
// file and fail every dispatch. Now:
//
//   - the value must be a bare agent id;
//   - the template is lib/agents/<id>.md, read under the rules for an agent file:
//     a symlink only to a regular file inside the framework's lib/ or the project;
//   - a bad value or a template that may not be read skips that agent, with the
//     once-per-file warning naming the file and the value, and never fails the
//     roster. A missing template still means "the project body alone".

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// requireLine requires the warning text to hold the line exactly once. The
// total is not asserted: a bad template is also an agent file in lib/agents, and
// is warned about as one.
func requireLine(t *testing.T, warnings *bytes.Buffer, file, reason string) {
	t.Helper()
	line := "yakos: WARN: ignoring agent file " + file + ": " + reason + "\n"
	if n := strings.Count(warnings.String(), line); n != 1 {
		t.Errorf("want exactly one %q, found %d, in:\n%s", line, n, warnings.String())
	}
}

const templateText = "---\nid: tmpl\n---\n\n## Purpose\n\nTemplate persona TEMPLATE-MARKER.\n"

func TestBareAgentID(t *testing.T) {
	long := strings.Repeat("a", 128)
	for _, v := range []string{"backend", "general-codex", "lead-template", "A1", "a.b", "a_b", "v1.2-x", "x", long} {
		if !BareAgentID(v) {
			t.Errorf("BareAgentID(%q) = false, want true", v)
		}
	}
	for _, v := range []string{
		"", ".", "..", ".hidden", "-lead", "_x",
		"../x", "../../etc/passwd", "/etc/passwd", "a/b", "a\\b", "x/", "./x", "x..y",
		"has space", "tab\there", `"quoted"`, "lead # note", "a:b", "café", "a\x00b", "%2e%2e",
		long + "a",
	} {
		if BareAgentID(v) {
			t.Errorf("BareAgentID(%q) = true, want false", v)
		}
	}
}

// DisplayValue is what a warning or a validate finding prints of an untrusted
// value. The bash twin prints the same bytes; tests/run-agent-enums-test.sh and
// compose_bash_parity_test.go compare them.
func TestDisplayValue(t *testing.T) {
	sixtyFour := strings.Repeat("a", 64)
	cases := []struct{ in, want string }{
		{"backend", `"backend"`},
		{"../x", `"../x"`},
		{"café", `"caf??"`},                 // two bytes, two marks
		{"a\tb\x01c\x7fd", `"a?b?c?d"`},     // controls and DEL
		{"esc\x1b[31mred", `"esc?[31mred"`}, // no terminal escapes in a log line
		{sixtyFour, `"` + sixtyFour + `"`},  // exactly the limit: no ellipsis
		{sixtyFour + "b", `"` + sixtyFour + `..."`},
		{"", `""`},
	}
	for _, c := range cases {
		if got := DisplayValue(c.in); got != c.want {
			t.Errorf("DisplayValue(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// Ten values that reach outside lib/agents or are not a bare id. For the ones
// that name a path, a file with a marker really is at the target (a name with a
// leading dot is rejected on its shape alone), so the marker not reaching the
// roster is the rule working and not the file being missing.
func TestCompose_SkipsAnAgentWhoseExtendsIsNotABareID(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	outsideDir := t.TempDir()
	writeFileT(t, filepath.Join(root, "lib", "outside.md"), "OUTSIDE-MARKER-lib\n")              // ../outside from lib/agents
	writeFileT(t, filepath.Join(root, "outside.md"), "OUTSIDE-MARKER-root\n")                    // ../../outside
	writeFileT(t, filepath.Join(outsideDir, "abs.md"), "OUTSIDE-MARKER-abs\n")                   // an absolute path
	writeFileT(t, filepath.Join(root, "lib", "agents", "sub", "dir.md"), "OUTSIDE-MARKER-sub\n") // sub/dir
	values := []string{
		"../outside", "../../outside", filepath.Join(outsideDir, "abs"), "sub/dir", `back\slash`,
		".hidden", "..", "x..y", `"quoted"`, "tmpl # note",
	}
	writeFileT(t, filepath.Join(root, "lib", "agents", "tmpl.md"), templateText)
	for i, v := range values {
		id := "bad" + string(rune('a'+i))
		writeFileT(t, filepath.Join(agents, id+".md"), "---\nid: "+id+"\nextends: "+v+"\n---\n\n## Purpose\n\nBad "+id+".\n")
	}

	// The framework's own agents are backend and tmpl, then the project's helper.
	// None of the bad* agents is in it.
	got, roster := composeIDs(t, root, project)
	if got != "backend,tmpl,helper" {
		t.Fatalf("roster = %q, want backend,tmpl,helper: every agent with a bad value must be skipped", got)
	}
	encoded, err := json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "OUTSIDE-MARKER") {
		t.Errorf("a file outside lib/agents reached the roster: %s", encoded)
	}
	out := warnings.String()
	for i, v := range values {
		id := "bad" + string(rune('a'+i))
		line := "ignoring agent file " + filepath.Join(agents, id+".md") + ": extends value " + DisplayValue(v) + " is not a bare agent id (" + BareIDRule + ")"
		if strings.Count(out, line) != 1 {
			t.Errorf("want exactly one warning %q in:\n%s", line, out)
		}
	}
	if n := strings.Count(out, "WARN"); n != len(values) {
		t.Errorf("%d warnings, want %d (one per file)", n, len(values))
	}
}

// A bare id still extends. Names with an upper-case letter, a dot or an
// underscore are bare ids too.
func TestCompose_StillExtendsABareID(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	for _, name := range []string{"tmpl", "Tmpl_2", "tmpl.v2"} {
		writeFileT(t, filepath.Join(root, "lib", "agents", name+".md"), strings.Replace(templateText, "tmpl", name, 1))
		writeFileT(t, filepath.Join(agents, "uses-"+name+".md"), "---\nid: u\nextends: "+name+"\n---\n\n## Purpose\n\nChild of "+name+".\n")
	}
	_, roster := composeIDs(t, root, project)
	n := 0
	for _, a := range roster {
		if strings.HasPrefix(a.ID, "uses-") {
			n++
			if !strings.Contains(a.Prompt, "TEMPLATE-MARKER") || !strings.Contains(a.Prompt, "Child of") {
				t.Errorf("%s did not get its template: %.80q", a.ID, a.Prompt)
			}
		}
	}
	if n != 3 {
		t.Errorf("%d extending agents composed, want 3", n)
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// No such template: the agent is composed from its own body, as before.
func TestCompose_AMissingTemplateMeansTheProjectBodyAlone(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	writeFileT(t, filepath.Join(agents, "alone.md"), "---\nid: alone\nextends: nosuch\n---\n\n## Purpose\n\nOnly me.\n")
	_, roster := composeIDs(t, root, project)
	var alone *ComposedAgent
	for i := range roster {
		if roster[i].ID == "alone" {
			alone = &roster[i]
		}
	}
	if alone == nil || strings.Contains(alone.Prompt, "---") || !strings.Contains(alone.Prompt, "Only me.") {
		t.Fatalf("alone = %+v, want the project body alone", alone)
	}
	if warnings.Len() != 0 {
		t.Errorf("unexpected warnings: %q", warnings.String())
	}
}

// A template that is a symlink to a file inside lib/ is the installed layout and
// works.
func TestCompose_ExtendsFollowsATemplateSymlinkedInsideLib(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	writeFileT(t, filepath.Join(root, "lib", "agents-extra", "real.md"), templateText)
	symlinkOrSkip(t, filepath.Join(root, "lib", "agents-extra", "real.md"), filepath.Join(root, "lib", "agents", "tmpl.md"))
	writeFileT(t, filepath.Join(agents, "child.md"), "---\nid: child\nextends: tmpl\n---\n\n## Purpose\n\nChild.\n")

	_, roster := composeIDs(t, root, project)
	for _, a := range roster {
		if a.ID == "child" {
			if !strings.Contains(a.Prompt, "TEMPLATE-MARKER") {
				t.Errorf("child did not get the template through the link: %.80q", a.Prompt)
			}
			if warnings.Len() != 0 {
				t.Errorf("unexpected warnings: %q", warnings.String())
			}
			return
		}
	}
	t.Fatal("child was not composed")
}

// A template that escapes the roots through a symlink is not read: the agent is
// skipped with a warning naming the file and the value, and the outside text is
// nowhere.
func TestCompose_SkipsAnAgentWhoseTemplateIsASymlinkOutOfTheRoots(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, agents := filesFixture(t)
	outside := filepath.Join(t.TempDir(), "secret")
	writeFileT(t, outside, secretText+"\n")
	symlinkOrSkip(t, outside, filepath.Join(root, "lib", "agents", "tmpl.md"))
	child := filepath.Join(agents, "child.md")
	writeFileT(t, child, "---\nid: child\nextends: tmpl\n---\n\n## Purpose\n\nChild.\n")

	got, roster := composeIDs(t, root, project)
	if got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	encoded, _ := json.Marshal(roster)
	if strings.Contains(string(encoded), "TOPSECRET") {
		t.Errorf("the outside file reached the roster: %s", encoded)
	}
	requireLine(t, warnings, child, `extends "tmpl": symlink resolves outside the framework lib/ and the project directory`)
}

func TestCompose_SkipsAnAgentWhoseTemplateDoesNotResolveToAFile(t *testing.T) {
	for _, c := range []struct{ name, kind string }{{"dangling", "dangling"}, {"directory", "directory"}} {
		t.Run(c.name, func(t *testing.T) {
			warnings := captureWarnings(t)
			root, project, agents := filesFixture(t)
			link := filepath.Join(root, "lib", "agents", "tmpl.md")
			if c.kind == "dangling" {
				symlinkOrSkip(t, filepath.Join(root, "nothing"), link)
			} else {
				writeFileT(t, filepath.Join(root, "somedir", "x"), "x")
				symlinkOrSkip(t, filepath.Join(root, "somedir"), link)
			}
			child := filepath.Join(agents, "child.md")
			writeFileT(t, child, "---\nid: child\nextends: tmpl\n---\n\n## Purpose\n\nChild.\n")
			if got, _ := composeIDs(t, root, project); got != "backend,helper" {
				t.Errorf("roster = %q, want backend,helper", got)
			}
			requireLine(t, warnings, child, `extends "tmpl": symlink does not resolve to a regular file`)
		})
	}
}

// The same value rule applies to a framework agent: its file is the framework's,
// and still it skips, not fails the roster.
func TestCompose_ABadExtendsInAFrameworkAgentIsASkipToo(t *testing.T) {
	warnings := captureWarnings(t)
	root, project, _ := filesFixture(t)
	fw := filepath.Join(root, "lib", "agents", "odd.md")
	writeFileT(t, fw, "---\nid: odd\nextends: ../x\n---\n\n## Purpose\n\nOdd.\n")
	if got, _ := composeIDs(t, root, project); got != "backend,helper" {
		t.Errorf("roster = %q, want backend,helper", got)
	}
	requireWarning(t, warnings, fw, `extends value "../x" is not a bare agent id`)
}
