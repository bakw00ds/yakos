//go:build !windows

package agentscompose

// compose_bash_parity_test.go — the bash composer (cli/lib/agents-compose.sh)
// applies the same agent-file rules as Compose, and prints the same warnings, byte
// for byte (sec-324): a bare `extends:` id, a symlink only to a regular file
// inside the framework's lib/ or the project. Both run on one fixture. The bash
// side runs under /bin/bash, which is bash 3.2 on macOS, and under the bash on
// PATH when that is another one. The expected warnings are written out here too,
// so two silent twins would not pass.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// bashes returns the distinct bash executables to run the composer under.
func bashes(t *testing.T) []string {
	t.Helper()
	var out []string
	var seen []os.FileInfo
	for _, name := range []string{"/bin/bash", "bash"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		dup := false
		for _, s := range seen {
			if os.SameFile(s, fi) {
				dup = true
			}
		}
		if !dup {
			seen = append(seen, fi)
			out = append(out, path)
		}
	}
	if len(out) == 0 {
		t.Skip("no bash")
	}
	return out
}

const composeScript = `set -eu
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/agents-compose.sh"
yk_agents_compose "$1" "$2"
`

// runBashComposer returns the ids the bash composer composed and the warning
// lines it printed about files it ignored.
func runBashComposer(t *testing.T, shell, root, project string) (ids, warns []string) {
	t.Helper()
	repoLib, err := filepath.Abs(filepath.Join("..", "..", "..", "cli", "lib"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, "-c", composeScript, "composer", root, project)
	cmd.Env = append(os.Environ(), "YAKOS_LIB="+repoLib, "HOME="+t.TempDir())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: composer failed: %v\nstderr:\n%s", shell, err, stderr.String())
	}
	var composed map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &composed); err != nil {
		t.Fatalf("%s: composer output is not JSON: %v\n%s", shell, err, stdout.String())
	}
	for id := range composed {
		ids = append(ids, id)
	}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "yakos: WARN: ignoring agent file ") {
			warns = append(warns, line)
		}
	}
	sort.Strings(ids)
	sort.Strings(warns)
	return ids, warns
}

func TestBashComposerAppliesTheAgentFileRulesLikeCompose(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed; the bash composer needs it")
	}
	warnings := captureWarnings(t)
	root, project := t.TempDir(), t.TempDir()
	fw := filepath.Join(root, "lib", "agents")
	agents := filepath.Join(project, ".claude", "agents")
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "secret")
	writeFileT(t, secret, secretText+"\n")

	writeAgentDir(t, fw, map[string]string{"backend": "model: sonnet\n"})
	writeFileT(t, filepath.Join(fw, "tmpl.md"), templateText)
	writeFileT(t, filepath.Join(root, "lib", "agents-extra", "real.md"), templateText)
	symlinkOrSkip(t, filepath.Join("..", "agents-extra", "real.md"), filepath.Join(fw, "linked-tmpl.md")) // inside lib/: fine
	symlinkOrSkip(t, secret, filepath.Join(fw, "escape-tmpl.md"))                                         // outside: refused

	child := func(id, extends string) {
		writeFileT(t, filepath.Join(agents, id+".md"), "---\nid: "+id+"\nextends: "+extends+"\n---\n\n## Purpose\n\nChild "+id+".\n")
	}
	writeAgentDir(t, agents, map[string]string{"plain": "model: haiku\n"})
	child("uses-tmpl", "tmpl")
	child("uses-linked", "linked-tmpl")
	child("uses-missing", "nosuch")
	child("uses-escape", "escape-tmpl")
	badValues := []string{
		"../outside", "../../outside", filepath.Join(outsideDir, "abs"), "sub/dir", `back\slash`,
		".hidden", "..", "x..y", `"quoted"`, "tmpl # note", "café", "a\tb", strings.Repeat("a", 70) + "/x",
	}
	var expected []string
	warn := func(file, reason string) {
		expected = append(expected, "yakos: WARN: ignoring agent file "+file+": "+reason)
	}
	for i, v := range badValues {
		id := fmt.Sprintf("bad%02d", i)
		child(id, v)
		warn(filepath.Join(agents, id+".md"), "extends value "+DisplayValue(v)+" is not a bare agent id ("+BareIDRule+")")
	}
	const outside = "symlink resolves outside the framework lib/ and the project directory"
	const unresolved = "symlink does not resolve to a regular file"
	warn(filepath.Join(fw, "escape-tmpl.md"), outside) // the framework walk meets the link too
	warn(filepath.Join(agents, "uses-escape.md"), `extends "escape-tmpl": `+outside)

	writeFileT(t, filepath.Join(project, "shared", "shared.md"), "---\nid: shared\nrole: specialist\n---\n\n## Purpose\n\nShared.\n")
	symlinkOrSkip(t, filepath.Join("..", "..", "shared", "shared.md"), filepath.Join(agents, "inproject.md")) // fine
	symlinkOrSkip(t, filepath.Join(fw, "backend.md"), filepath.Join(agents, "inlib.md"))                      // the installed layout: fine
	symlinkOrSkip(t, secret, filepath.Join(agents, "leak.md"))
	symlinkOrSkip(t, filepath.Join(project, "nothing"), filepath.Join(agents, "ghost.md"))
	if err := os.MkdirAll(filepath.Join(project, "somedir"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(project, "somedir"), filepath.Join(agents, "dirlink.md"))
	warn(filepath.Join(agents, "leak.md"), outside)
	warn(filepath.Join(agents, "ghost.md"), unresolved)
	warn(filepath.Join(agents, "dirlink.md"), unresolved)
	sort.Strings(expected)

	roster, err := Compose(root, project)
	if err != nil {
		t.Fatalf("Compose = %v", err)
	}
	goIDs := rosterIDs(roster)
	sort.Strings(goIDs)
	goWarns := strings.Split(strings.TrimRight(warnings.String(), "\n"), "\n")
	sort.Strings(goWarns)
	if strings.Join(goWarns, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("Compose warnings differ from the expected set\n got:\n%s\nwant:\n%s", strings.Join(goWarns, "\n"), strings.Join(expected, "\n"))
	}
	wantIDs := []string{"backend", "inlib", "inproject", "linked-tmpl", "plain", "tmpl", "uses-linked", "uses-missing", "uses-tmpl"}
	if strings.Join(goIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("Compose roster = %v, want %v", goIDs, wantIDs)
	}

	for _, shell := range bashes(t) {
		ids, warns := runBashComposer(t, shell, root, project)
		t.Logf("bash composer checked under %s", shell)
		if strings.Join(ids, ",") != strings.Join(goIDs, ",") {
			t.Errorf("%s: the bash composer composed %v, Compose %v", shell, ids, goIDs)
		}
		if strings.Join(warns, "\n") != strings.Join(expected, "\n") {
			t.Errorf("%s: the bash composer's warnings differ from Compose's\n got:\n%s\nwant:\n%s", shell, strings.Join(warns, "\n"), strings.Join(expected, "\n"))
		}
	}
}

// The bash composer never opens a link to a FIFO: it is classified before
// anything reads the file. A hang here is the failure, so the run has a timeout
// through the test deadline of the package, and a reader stuck on the pipe is
// released if the composer returns an error.
func TestBashComposerDoesNotOpenALinkToAFIFO(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed; the bash composer needs it")
	}
	root, project := t.TempDir(), t.TempDir()
	writeAgentDir(t, filepath.Join(root, "lib", "agents"), map[string]string{"backend": "model: sonnet\n"})
	agents := filepath.Join(project, ".claude", "agents")
	writeAgentDir(t, agents, map[string]string{"plain": "model: haiku\n"})
	fifo := filepath.Join(project, "pipe")
	mkfifoOrSkip(t, fifo)
	symlinkOrSkip(t, fifo, filepath.Join(agents, "pipe.md"))
	for _, shell := range bashes(t) {
		done := make(chan struct{})
		var ids, warns []string
		go func() {
			ids, warns = runBashComposer(t, shell, root, project)
			close(done)
		}()
		select {
		case <-done:
		case <-afterSeconds(10):
			releaseFIFOForTest(fifo)
			t.Fatalf("%s: the bash composer is blocked on the FIFO", shell)
		}
		if strings.Join(ids, ",") != "backend,plain" {
			t.Errorf("%s: composed %v, want backend,plain", shell, ids)
		}
		want := "yakos: WARN: ignoring agent file " + filepath.Join(agents, "pipe.md") + ": symlink does not resolve to a regular file"
		if len(warns) != 1 || warns[0] != want {
			t.Errorf("%s: warnings = %q, want %q", shell, warns, want)
		}
	}
}
