package agentscompose

// compose_long_line_test.go — an agent or skill file is read whole, or refused
// with an error that names the file; it is never silently cut short.
//
// The line scanner here is bufio.Scanner, whose default limit is 64 KiB per
// line. A line over it made Scan return false without a word, and everything
// after that line, in practice the rest of the agent's persona, was gone from
// the roster entry (found while testing the claude persona cap).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const longLineAgentHead = "---\nid: long\nmodel: sonnet\n---\n\n## Purpose\n\nA fixture with a long line.\n\n"

// A 70 KiB line (over the old 64 KiB limit) is read whole, and what follows it is
// still there. Before, the scan stopped at the long line and TAIL-MARKER was lost.
func TestCompose_ReadsALineOverTheOldLimit(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 70<<10)
	writeFileT(t, filepath.Join(root, "lib", "agents", "long.md"),
		longLineAgentHead+long+"\n\nTAIL-MARKER\n")

	roster, err := Compose(root, "")
	if err != nil || len(roster) != 1 {
		t.Fatalf("Compose = %v, %v", roster, err)
	}
	p := roster[0].Prompt
	if !strings.Contains(p, long) {
		t.Errorf("the 70 KiB line was cut short: the prompt holds %d bytes", len(p))
	}
	if !strings.Contains(p, "TAIL-MARKER") {
		t.Error("everything after the long line was dropped")
	}
}

// A long line in the frontmatter is read whole too (a long description, say).
func TestCompose_ReadsALongFrontmatterLine(t *testing.T) {
	root := t.TempDir()
	desc := strings.Repeat("d", 70<<10)
	writeFileT(t, filepath.Join(root, "lib", "agents", "fm.md"),
		"---\nid: fm\nnotes: "+desc+"\nmodel: opus\n---\n\n## Purpose\n\nBody.\n")
	roster, err := Compose(root, "")
	if err != nil || len(roster) != 1 {
		t.Fatalf("Compose = %v, %v", roster, err)
	}
	if roster[0].Model != "opus" {
		t.Errorf("a key after the long frontmatter line was lost: model %q", roster[0].Model)
	}
}

// A line over the bound is refused, and the error names the file and the line.
func TestCompose_RefusesALineOverTheBound(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "lib", "agents", "ok.md"), longLineAgentHead+"fine\n")
	bad := filepath.Join(root, "lib", "agents", "huge.md")
	writeFileT(t, bad, longLineAgentHead+strings.Repeat("y", maxLineBytes+1)+"\n")

	roster, err := Compose(root, "")
	if err == nil {
		t.Fatalf("a %d byte line was accepted (roster of %d)", maxLineBytes+1, len(roster))
	}
	for _, want := range []string{"huge.md", "line 10", "longer than"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if roster != nil {
		t.Errorf("a partial roster was returned alongside the error: %d agents", len(roster))
	}
}

// The framework template an agent extends is read the same way. lead-template.md
// is not a roster entry, so only the extends step reads it here: the error must
// come from that step and name the template.
func TestCompose_RefusesALongLineInAnExtendedTemplate(t *testing.T) {
	root := t.TempDir()
	tmpl := filepath.Join(root, "lib", "agents", "lead-template.md")
	writeFileT(t, tmpl, longLineAgentHead+strings.Repeat("z", maxLineBytes+1)+"\n")
	writeFileT(t, filepath.Join(root, "lib", "agents", "child.md"), "---\nid: child\nextends: lead-template\n---\n\n## Purpose\n\nChild.\n")

	_, err := Compose(root, "")
	if err == nil || !strings.Contains(err.Error(), "lead-template.md") || !strings.Contains(err.Error(), "child.md") {
		t.Errorf("err = %v, want it to name the extended template and the agent that extends it", err)
	}
}

// A template with a long (but allowed) line extends fine and keeps its tail.
func TestCompose_ExtendsATemplateWithALongLine(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("t", 70<<10)
	writeFileT(t, filepath.Join(root, "lib", "agents", "lead-template.md"), longLineAgentHead+long+"\n\nTEMPLATE-TAIL\n")
	writeFileT(t, filepath.Join(root, "lib", "agents", "child.md"), "---\nid: child\nextends: lead-template\n---\n\n## Purpose\n\nChild.\n")

	roster, err := Compose(root, "")
	if err != nil || len(roster) != 1 {
		t.Fatalf("Compose = %v, %v", roster, err)
	}
	if !strings.Contains(roster[0].Prompt, long) || !strings.Contains(roster[0].Prompt, "TEMPLATE-TAIL") {
		t.Errorf("the extended template was cut short: %d bytes", len(roster[0].Prompt))
	}
}

// Skills are read the same way.
func TestComposeSkills_LongLines(t *testing.T) {
	root := t.TempDir()
	writeFileT(t, filepath.Join(root, "lib", "skills", "fine", "SKILL.md"),
		"---\nname: fine\ndescription: ok\n---\n\n"+strings.Repeat("s", 70<<10)+"\n")
	if skills, err := ComposeSkills(root, ""); err != nil || len(skills) != 1 || skills[0].Description != "ok" {
		t.Fatalf("a 70 KiB line in a skill: %v, %v", skills, err)
	}

	bad := filepath.Join(root, "lib", "skills", "huge", "SKILL.md")
	writeFileT(t, bad, "---\nname: huge\n---\n\n"+strings.Repeat("s", maxLineBytes+1)+"\n")
	_, err := ComposeSkills(root, "")
	if err == nil || !strings.Contains(err.Error(), filepath.Join("huge", "SKILL.md")) {
		t.Errorf("err = %v, want it to name the skill file", err)
	}
}

// Lines up to the bound, and every shape that was fine before, compose exactly
// as before (the cache-stability golden covers the bytes; this pins the edges).
func TestSplitFrontmatter_Shapes(t *testing.T) {
	cases := []struct {
		name, in, fm, body string
	}{
		{"empty", "", "", ""},
		{"no frontmatter", "just text\nmore\n", "", "just text\nmore\n"},
		{"unclosed frontmatter", "---\nid: x\nbody\n", "", "---\nid: x\nbody\n"},
		{"closed", "---\nid: x\n---\nbody\nmore\n", "id: x", "body\nmore"},
		{"crlf", "---\r\nid: x\r\n---\r\nbody\r\n", "id: x", "body"},
		{"no trailing newline", "---\nid: x\n---\nbody", "id: x", "body"},
	}
	for _, c := range cases {
		fm, body, err := splitFrontmatter(c.in)
		if err != nil || fm != c.fm || body != c.body {
			t.Errorf("%s: splitFrontmatter(%q) = %q, %q, %v; want %q, %q", c.name, c.in, fm, body, err, c.fm, c.body)
		}
	}
	// A line at the bound is still accepted.
	atBound := strings.Repeat("a", maxLineBytes-1) // plus the newline: exactly the bound
	if _, body, err := splitFrontmatter("---\nid: x\n---\n" + atBound + "\n"); err != nil || len(body) != maxLineBytes-1 {
		t.Errorf("a line of %d bytes: %d bytes back, err %v", maxLineBytes-1, len(body), err)
	}
}
