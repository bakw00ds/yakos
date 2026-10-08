package modelreg

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProject_NoFileNoPolicy(t *testing.T) {
	if pol, warns := LoadProject(""); len(pol.Disable) != 0 || len(warns) != 0 {
		t.Errorf("empty project: %+v %v", pol, warns)
	}
	if pol, warns := LoadProject(t.TempDir()); len(pol.Disable) != 0 || len(warns) != 0 {
		t.Errorf("no .yakos.yml: %+v %v", pol, warns)
	}
	dir := writeProject(t, "default-runtime: codex\nper-domain:\n  code-review: codex\n")
	if pol, warns := LoadProject(dir); len(pol.Disable) != 0 || len(warns) != 0 {
		t.Errorf("a .yakos.yml without models: must say nothing: %+v %v", pol, warns)
	}
}

func TestParseProject_DisableList(t *testing.T) {
	pol, warns := ParseProject([]byte("default-runtime: claude\nmodels:\n  disable: [opus, gpt-5.6-sol, opus, \"Bad Id\", 7]\n"))
	if got := strings.Join(pol.Disable, ","); got != "opus,gpt-5.6-sol" {
		t.Errorf("Disable = %q, want the valid ids once each", got)
	}
	if len(warns) != 2 || !strings.Contains(strings.Join(warns, "\n"), "skipping an entry that is not a model id") {
		t.Errorf("warnings = %v", warns)
	}
}

// A project can narrow what runs and never widen it: `disable` is the only key
// that does anything, and every key that tries to add, enable, alias or price is
// reported and has no effect.
func TestParseProject_CannotWiden(t *testing.T) {
	body := `
models:
  disable: [haiku]
  enable: [gpt-reserve]
  add:
    - id: my-model
      harnesses: [claude]
  aliases:
    best: {codex: gpt-5.6-sol}
  providers:
    evil: {url: "http://example.invalid"}
  pricing: {haiku: {input: 0, output: 0}}
  gpt-5.5: {enabled: true}
`
	pol, warns := ParseProject([]byte(body))
	if len(pol.Disable) != 1 || pol.Disable[0] != "haiku" {
		t.Errorf("Disable = %v", pol.Disable)
	}
	joined := strings.Join(warns, "\n")
	for _, k := range []string{`"enable"`, `"add"`, `"aliases"`, `"providers"`, `"pricing"`, `"gpt-5.5"`} {
		if !strings.Contains(joined, k+" ignored: a project can only disable models") {
			t.Errorf("no warning for the key %s in:\n%s", k, joined)
		}
	}
	// The only field of the policy is the disable list: there is nothing else a
	// project could set, by construction.
	if got := reflectFieldCount(pol); got != 1 {
		t.Errorf("ProjectPolicy has %d fields; it must stay disable-only", got)
	}
}

func TestParseProject_Malformed(t *testing.T) {
	cases := []struct{ name, body, warn string }{
		{"not YAML", "models: [unclosed {", "cannot parse"},
		{"models is a list", "models:\n  - opus\n", "want a mapping with a disable: list"},
		{"models is a string", "models: opus\n", "want a mapping with a disable: list"},
		{"disable is a string", "models:\n  disable: opus\n", "disable: want a list"},
		{"disable is a mapping", "models:\n  disable: {opus: true}\n", "disable: want a list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pol, warns := ParseProject([]byte(c.body))
			if len(pol.Disable) != 0 {
				t.Errorf("Disable = %v", pol.Disable)
			}
			if len(warns) == 0 || !strings.Contains(strings.Join(warns, "\n"), c.warn) {
				t.Errorf("warnings = %v, want %q", warns, c.warn)
			}
		})
	}
}

func TestParseProject_BoundsTheList(t *testing.T) {
	var b strings.Builder
	b.WriteString("models:\n  disable:\n")
	for i := 0; i < maxProjectDisables+40; i++ {
		b.WriteString("    - m-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)) + "\n")
	}
	pol, warns := ParseProject([]byte(b.String()))
	if len(pol.Disable) != maxProjectDisables {
		t.Errorf("read %d ids, want the cap %d", len(pol.Disable), maxProjectDisables)
	}
	if !hasWarning(warns, "only the first") {
		t.Errorf("no cap warning: %v", warns)
	}
}

// .yakos.yml is a repository file. A FIFO, a device or an enormous file must not
// block or slow every registry load.
func TestLoadProject_HostileFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".yakos.yml")
	if err := os.WriteFile(path, []byte("models:\n  disable: [haiku]\n"+"#"+strings.Repeat("x", maxProjectBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	pol, warns := LoadProject(dir)
	if len(pol.Disable) != 0 || !hasWarning(warns, "larger than") {
		t.Errorf("an oversized .yakos.yml: %+v %v", pol, warns)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil { // a directory in place of the file
		t.Fatal(err)
	}
	pol, warns = LoadProject(dir)
	if len(pol.Disable) != 0 || !hasWarning(warns, "not a regular file") {
		t.Errorf("a directory named .yakos.yml: %+v %v", pol, warns)
	}
}

func TestLoadProject_ReadsTheFile(t *testing.T) {
	dir := writeProject(t, "models:\n  disable: [opus]\n")
	pol, warns := LoadProject(dir)
	if len(pol.Disable) != 1 || pol.Disable[0] != "opus" || len(warns) != 0 {
		t.Errorf("%+v %v", pol, warns)
	}
}

func TestParseProject_CapsTheWarnings(t *testing.T) {
	var b strings.Builder
	b.WriteString("models:\n")
	for i := 0; i < 3000; i++ {
		b.WriteString("  k" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)) + ": 1\n")
	}
	_, warns := ParseProject([]byte(b.String()))
	if len(warns) != maxWarningsPerSource+1 || !strings.Contains(warns[len(warns)-1], " more problems not shown") {
		t.Fatalf("%d warnings, last %q", len(warns), warns[len(warns)-1])
	}
}

// A project whose .yakos.yml does not parse loses its models: restrictions, and the
// warning says so, so a typo in an unrelated key is not mistaken for a working
// restriction.
func TestParseProject_ParseErrorSaysTheDisableListIsNotApplied(t *testing.T) {
	pol, warns := ParseProject([]byte("models:\n  disable: [opus]\nother: [unclosed\n"))
	if len(pol.Disable) != 0 {
		t.Errorf("Disable = %v", pol.Disable)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "a models: disable list in it is NOT applied") {
		t.Errorf("warnings = %v", warns)
	}
}

// An OS error carries the project's path, and a directory can be named with anything.
// Warnings are printed to a terminal and, later, served over an API, so a read
// failure is worded by role and names no path.
func TestLoadProject_ReadErrorsNameNoPath(t *testing.T) {
	skipIfNoPosixModes(t)
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-000 file")
	}
	dir := filepath.Join(t.TempDir(), "p\x1b]0;owned\x07roj")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Skipf("this filesystem refuses such a name: %v", err)
	}
	path := filepath.Join(dir, ".yakos.yml")
	if err := os.WriteFile(path, []byte("models:\n  disable: [opus]\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, warns := LoadProject(dir)
	want := ".yakos.yml: permission denied, so a models: disable list in it is NOT applied"
	if len(warns) != 1 || warns[0] != want {
		t.Fatalf("warnings = %q, want exactly [%q]", warns, want)
	}
	if strings.Contains(warns[0], "roj") || strings.Contains(warns[0], os.TempDir()) {
		t.Errorf("the warning names the project's path: %q", warns[0])
	}
}

// projectReadWarning words a read failure by role, whatever the error: an OS error
// carries the project's path (and the text of the failure), and the warning reaches a
// terminal and, later, an API. The permission branch has its own test above; this
// reaches every other branch, each with a path in the error that must not appear.
func TestProjectReadWarning_WordsEveryBranchWithoutThePath(t *testing.T) {
	const path = "/secret/project-dir/.yakos.yml"
	lost := ", so a models: disable list in it is NOT applied"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"permission", &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}, ".yakos.yml: permission denied" + lost},
		{"wrapped permission", fmt.Errorf("while reading %s: %w", path, fs.ErrPermission), ".yakos.yml: permission denied" + lost},
		{"too many links", &fs.PathError{Op: "stat", Path: path, Err: errors.New("too many levels of symbolic links")}, ".yakos.yml: it could not be read" + lost},
		{"short read", &fs.PathError{Op: "read", Path: path, Err: io.ErrUnexpectedEOF}, ".yakos.yml: it could not be read" + lost},
		{"vanished", &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}, ".yakos.yml: it could not be read" + lost},
		{"plain text", errors.New("read " + path + ": input/output error"), ".yakos.yml: it could not be read" + lost},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := projectReadWarning(c.err)
			if got != c.want {
				t.Errorf("projectReadWarning = %q, want %q", got, c.want)
			}
			if strings.Contains(got, "secret") || strings.Contains(got, "project-dir") || strings.Contains(got, "input/output") {
				t.Errorf("the warning carries the path or the OS text: %q", got)
			}
		})
	}
}

// A .yakos.yml that is a symlink to itself is refused as a link, and the warning
// names no path.
func TestLoadProject_AReadErrorThatIsNotAPermissionErrorNamesNoPath(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := filepath.Join(t.TempDir(), "proj-secret-name")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".yakos.yml", filepath.Join(dir, ".yakos.yml")); err != nil {
		t.Skipf("cannot make a symlink loop here: %v", err)
	}
	pol, warns := LoadProject(dir)
	want := ".yakos.yml: is a symlink, which is not followed; the models: key is ignored"
	if len(pol.Disable) != 0 || len(warns) != 1 || warns[0] != want {
		t.Fatalf("policy %+v, warnings %q, want one warning %q", pol, warns, want)
	}
	if strings.Contains(warns[0], "proj-secret-name") || strings.Contains(warns[0], os.TempDir()) {
		t.Errorf("the warning names the project's path: %q", warns[0])
	}
}
