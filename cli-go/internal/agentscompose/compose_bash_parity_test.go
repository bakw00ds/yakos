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

// repoCLILib is the repository's cli/lib, as an absolute path found while the
// working directory is still the package directory: a test may change directory.
var repoCLILib, _ = filepath.Abs(filepath.Join("..", "..", "..", "cli", "lib"))

const composeScript = `set -eu
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/agents-compose.sh"
yk_agents_compose "$1" "$2"
`

// runBashComposer returns the ids the bash composer composed and the warning
// lines it printed about files it ignored.
func runBashComposer(t *testing.T, shell, root, project string) (ids, warns []string) {
	t.Helper()
	ids, stderr := bashCompose(t, shell, root, project)
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "yakos: WARN: ignoring agent file ") {
			warns = append(warns, line)
		}
	}
	sort.Strings(warns)
	return ids, warns
}

// bashCompose runs the bash composer and returns the ids it composed, sorted, and
// everything it wrote to stderr.
func bashCompose(t *testing.T, shell, root, project string) (ids []string, stderr string) {
	t.Helper()
	cmd := exec.Command(shell, "-c", composeScript, "composer", root, project)
	cmd.Env = append(os.Environ(), "YAKOS_LIB="+repoCLILib, "HOME="+t.TempDir())
	var stdout, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: composer failed: %v\nstderr:\n%s", shell, err, errBuf.String())
	}
	var composed map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &composed); err != nil {
		t.Fatalf("%s: composer output is not JSON: %v\n%s", shell, err, stdout.String())
	}
	for id := range composed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, errBuf.String()
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
	writeFileT(t, filepath.Join(fw, "shared", "real.md"), templateText)
	symlinkOrSkip(t, filepath.Join("shared", "real.md"), filepath.Join(fw, "linked-tmpl.md")) // inside lib/agents: fine
	symlinkOrSkip(t, secret, filepath.Join(fw, "escape-tmpl.md"))                             // outside: refused

	child := func(id, extends string) {
		writeFileT(t, filepath.Join(agents, id+".md"), "---\nid: "+id+"\nextends: "+extends+"\n---\n\n## Purpose\n\nChild "+id+".\n")
	}
	writeAgentDir(t, agents, map[string]string{"plain": "model: haiku\n"})
	child("uses-tmpl", "tmpl")
	child("uses-linked", "linked-tmpl")
	child("uses-missing", "nosuch")
	child("uses-escape", "escape-tmpl")
	child("uses-rule-tmpl", "rule-tmpl")
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
	const outside = AgentOutsideReason
	const unresolved = "symlink does not resolve to a regular file"
	warn(filepath.Join(fw, "escape-tmpl.md"), outside) // the framework walk meets the link too
	warn(filepath.Join(agents, "uses-escape.md"), `extends "escape-tmpl": `+outside)
	// A template link that stays in lib/ but leaves lib/agents: the framework has
	// files that are not agents.
	writeFileT(t, filepath.Join(root, "lib", "rules", "rule.md"), "RULE-MARKER\n")
	symlinkOrSkip(t, filepath.Join("..", "rules", "rule.md"), filepath.Join(fw, "rule-tmpl.md"))
	warn(filepath.Join(fw, "rule-tmpl.md"), outside)
	warn(filepath.Join(agents, "uses-rule-tmpl.md"), `extends "rule-tmpl": `+outside)

	writeFileT(t, filepath.Join(agents, "shared", "shared.md"), "---\nid: shared\nrole: specialist\n---\n\n## Purpose\n\nShared.\n")
	symlinkOrSkip(t, filepath.Join("shared", "shared.md"), filepath.Join(agents, "inproject.md")) // inside the project's agent directory: fine
	symlinkOrSkip(t, filepath.Join(fw, "backend.md"), filepath.Join(agents, "inlib.md"))          // the installed layout: fine
	symlinkOrSkip(t, secret, filepath.Join(agents, "leak.md"))
	symlinkOrSkip(t, filepath.Join(project, "nothing"), filepath.Join(agents, "ghost.md"))
	if err := os.MkdirAll(filepath.Join(project, "somedir"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(project, "somedir"), filepath.Join(agents, "dirlink.md"))
	// The project's own files and the framework's other files are not agents.
	writeFileT(t, filepath.Join(project, ".env"), secretText+"\n")
	writeFileT(t, filepath.Join(project, ".git", "config"), "TOKEN-9999\n")
	symlinkOrSkip(t, filepath.Join("..", "..", ".env"), filepath.Join(agents, "dotenv.md"))
	symlinkOrSkip(t, filepath.Join("..", "..", ".git", "config"), filepath.Join(agents, "gitcfg.md"))
	symlinkOrSkip(t, filepath.Join(root, "lib", "rules", "rule.md"), filepath.Join(agents, "rule.md"))
	warn(filepath.Join(agents, "dotenv.md"), outside)
	warn(filepath.Join(agents, "gitcfg.md"), outside)
	warn(filepath.Join(agents, "rule.md"), outside)
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

// The project's agent directory is refused when it is a link, in both twins: a
// symlinked `.claude/agents`, or a symlinked `.claude` above it, is skipped whole
// with one warning, wherever it leads, and the framework's own directories are
// never checked. Compose and the bash composer run on the same fixtures, under each
// bash, and the roster and the warning lines must be the same, byte for byte, and
// as written here.
func TestBashComposerAppliesTheDirectoryRuleLikeCompose(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed; the bash composer needs it")
	}
	const dirWarn = "yakos: WARN: ignoring agent directory "
	const fileWarn = "yakos: WARN: ignoring agent file "
	agentsDirWarning := func(project string) []string {
		return []string{dirWarn + filepath.Join(project, ".claude", "agents") + ": " + DirLinkReason}
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, root, project string)
		root  func(t *testing.T, root string) string // the root both composers get, the root itself when nil
		ids   string
		warns func(project string) []string
	}{
		{
			name: "agents linked outside the project",
			setup: func(t *testing.T, root, project string) {
				symlinkOrSkip(t, filepath.Join(outsideTree(t), "agents"), filepath.Join(project, ".claude", "agents"))
			},
			ids:   "backend",
			warns: agentsDirWarning,
		},
		{
			name: "agents linked to a directory of the project",
			setup: func(t *testing.T, root, project string) {
				writeAgentDir(t, filepath.Join(project, "config", "agents"), map[string]string{"mine": "model: haiku\n"})
				symlinkOrSkip(t, filepath.Join("..", "config", "agents"), filepath.Join(project, ".claude", "agents"))
			},
			ids:   "backend",
			warns: agentsDirWarning,
		},
		{
			name: "dot-claude linked outside the project",
			setup: func(t *testing.T, root, project string) {
				symlinkOrSkip(t, outsideTree(t), filepath.Join(project, ".claude"))
			},
			ids:   "backend",
			warns: agentsDirWarning,
		},
		{
			name: "dot-claude linked to a directory of the project",
			setup: func(t *testing.T, root, project string) {
				writeAgentDir(t, filepath.Join(project, "dotclaude", "agents"), map[string]string{"mine": "model: haiku\n"})
				symlinkOrSkip(t, "dotclaude", filepath.Join(project, ".claude"))
			},
			ids:   "backend",
			warns: agentsDirWarning,
		},
		{
			name: "dot-claude linked, without an agents directory",
			setup: func(t *testing.T, root, project string) {
				writeFileT(t, filepath.Join(project, "dotclaude", "rules", "r.md"), "x\n")
				symlinkOrSkip(t, "dotclaude", filepath.Join(project, ".claude"))
			},
			ids:   "backend",
			warns: func(string) []string { return nil },
		},
		{
			name: "agents linked to nothing",
			setup: func(t *testing.T, root, project string) {
				symlinkOrSkip(t, filepath.Join(project, "does-not-exist"), filepath.Join(project, ".claude", "agents"))
			},
			ids:   "backend",
			warns: agentsDirWarning,
		},
		{
			name: "agents linked to a file",
			setup: func(t *testing.T, root, project string) {
				writeFileT(t, filepath.Join(project, "notes.txt"), "not a directory\n")
				symlinkOrSkip(t, filepath.Join(project, "notes.txt"), filepath.Join(project, ".claude", "agents"))
			},
			ids:   "backend",
			warns: agentsDirWarning,
		},
		{
			name: "a plain agents directory, with the rule for files inside it",
			setup: func(t *testing.T, root, project string) {
				agents := filepath.Join(project, ".claude", "agents")
				writeAgentDir(t, agents, map[string]string{"mine": "model: haiku\n"})
				writeFileT(t, filepath.Join(agents, "sub", "shared.md"), "---\nid: shared\n---\n\n## Purpose\n\nShared.\n")
				writeFileT(t, filepath.Join(project, ".env"), secretText+"\n")
				symlinkOrSkip(t, filepath.Join("sub", "shared.md"), filepath.Join(agents, "inproject.md"))
				symlinkOrSkip(t, filepath.Join("..", "..", ".env"), filepath.Join(agents, "dotenv.md"))
				symlinkOrSkip(t, filepath.Join(root, "lib", "agents", "backend.md"), filepath.Join(agents, "framework.md"))
			},
			ids: "backend,framework,inproject,mine",
			warns: func(project string) []string {
				return []string{fileWarn + filepath.Join(project, ".claude", "agents", "dotenv.md") + ": " + AgentOutsideReason}
			},
		},
		{
			name: "a plain agents directory",
			setup: func(t *testing.T, root, project string) {
				writeAgentDir(t, filepath.Join(project, ".claude", "agents"), map[string]string{"mine": "model: haiku\n"})
			},
			ids:   "backend,mine",
			warns: func(string) []string { return nil },
		},
		{
			name: "the framework root is a link",
			setup: func(t *testing.T, root, project string) {
				writeAgentDir(t, filepath.Join(project, ".claude", "agents"), map[string]string{"mine": "model: haiku\n"})
			},
			root: func(t *testing.T, root string) string {
				linked := filepath.Join(t.TempDir(), "root")
				symlinkOrSkip(t, root, linked)
				return linked
			},
			ids:   "backend,mine",
			warns: func(string) []string { return nil },
		},
		{
			name: "the framework's lib/agents is a link",
			setup: func(t *testing.T, root, project string) {
				writeAgentDir(t, filepath.Join(project, ".claude", "agents"), map[string]string{"mine": "model: haiku\n"})
				shared := filepath.Join(t.TempDir(), "agents")
				if err := os.Rename(filepath.Join(root, "lib", "agents"), shared); err != nil {
					t.Fatal(err)
				}
				symlinkOrSkip(t, shared, filepath.Join(root, "lib", "agents"))
			},
			ids:   "backend,mine",
			warns: func(string) []string { return nil },
		},
	}
	shells := bashes(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := captureWarnings(t)
			root, project := dirsFixture(t)
			tc.setup(t, root, project)
			if tc.root != nil {
				root = tc.root(t, root)
			}

			roster, err := Compose(root, project)
			if err != nil {
				t.Fatalf("Compose = %v", err)
			}
			goIDs := rosterIDs(roster)
			sort.Strings(goIDs)
			var goWarns []string
			for _, line := range strings.Split(warnings.String(), "\n") {
				if strings.HasPrefix(line, dirWarn) || strings.HasPrefix(line, fileWarn) {
					goWarns = append(goWarns, line)
				}
			}
			sort.Strings(goWarns)
			want := tc.warns(project)
			sort.Strings(want)
			if strings.Join(goIDs, ",") != tc.ids || strings.Join(goWarns, "\n") != strings.Join(want, "\n") {
				t.Fatalf("Compose: roster %v, warnings\n%s\nwant %s and\n%s", goIDs, strings.Join(goWarns, "\n"), tc.ids, strings.Join(want, "\n"))
			}
			for _, shell := range shells {
				ids, stderr := bashCompose(t, shell, root, project)
				var warns []string
				for _, line := range strings.Split(stderr, "\n") {
					if strings.HasPrefix(line, dirWarn) || strings.HasPrefix(line, fileWarn) {
						warns = append(warns, line)
					}
				}
				sort.Strings(warns)
				if strings.Join(ids, ",") != tc.ids {
					t.Errorf("%s: the bash composer composed %v, want %s", shell, ids, tc.ids)
				}
				if strings.Join(warns, "\n") != strings.Join(want, "\n") {
					t.Errorf("%s: the bash composer's warnings differ\n got:\n%s\nwant:\n%s", shell, strings.Join(warns, "\n"), strings.Join(want, "\n"))
				}
				if strings.Contains(stderr, "MARKER") || strings.Contains(stderr, "TOPSECRET") {
					t.Errorf("%s: text of a refused file reached stderr:\n%s", shell, stderr)
				}
			}
		})
	}
}

// A relative project path with CDPATH set. With CDPATH set, bash's cd given a
// relative path prints the directory it entered, and that second line got into the
// path the bash composer resolved for a link inside an agent directory, which was
// then refused for the wrong reason. A directory that is a link is refused without
// resolving anything, but it must stay refused for a relative path too. Compose
// never looks at CDPATH, so the twins must agree. Both run from the project's
// parent, with the project named relatively.
func TestBashComposerDirectoryRuleIgnoresCDPATH(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not installed; the bash composer needs it")
	}
	const dirWarn = "yakos: WARN: ignoring agent directory "
	const fileWarn = "yakos: WARN: ignoring agent file "
	cases := []struct {
		name  string
		setup func(t *testing.T, root, project string)
		ids   string
		warns []string
	}{
		{
			name: "agents linked outside the project",
			setup: func(t *testing.T, root, project string) {
				symlinkOrSkip(t, filepath.Join(outsideTree(t), "agents"), filepath.Join(project, ".claude", "agents"))
			},
			ids:   "backend",
			warns: []string{dirWarn + filepath.Join("proj", ".claude", "agents") + ": " + DirLinkReason},
		},
		{
			name: "agents linked to a directory of the project",
			setup: func(t *testing.T, root, project string) {
				writeAgentDir(t, filepath.Join(project, "config", "agents"), map[string]string{"mine": "model: haiku\n"})
				symlinkOrSkip(t, filepath.Join("..", "config", "agents"), filepath.Join(project, ".claude", "agents"))
			},
			ids:   "backend",
			warns: []string{dirWarn + filepath.Join("proj", ".claude", "agents") + ": " + DirLinkReason},
		},
		{
			name: "a plain agents directory, with links in it",
			setup: func(t *testing.T, root, project string) {
				agents := filepath.Join(project, ".claude", "agents")
				writeAgentDir(t, agents, map[string]string{"mine": "model: haiku\n"})
				writeFileT(t, filepath.Join(agents, "sub", "shared.md"), "---\nid: shared\n---\n\n## Purpose\n\nShared.\n")
				writeFileT(t, filepath.Join(project, ".env"), secretText+"\n")
				symlinkOrSkip(t, filepath.Join("sub", "shared.md"), filepath.Join(agents, "inproject.md"))
				symlinkOrSkip(t, filepath.Join("..", "..", ".env"), filepath.Join(agents, "dotenv.md"))
			},
			ids:   "backend,inproject,mine",
			warns: []string{fileWarn + filepath.Join("proj", ".claude", "agents", "dotenv.md") + ": " + AgentOutsideReason},
		},
	}
	shells := bashes(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings := captureWarnings(t)
			root, parent := t.TempDir(), t.TempDir()
			writeAgentDir(t, filepath.Join(root, "lib", "agents"), map[string]string{"backend": "model: sonnet\n"})
			project := filepath.Join(parent, "proj")
			tc.setup(t, root, project)
			t.Chdir(parent)
			t.Setenv("CDPATH", ".:/")

			roster, err := Compose(root, "proj")
			if err != nil {
				t.Fatalf("Compose = %v", err)
			}
			goIDs := rosterIDs(roster)
			sort.Strings(goIDs)
			var goWarns []string
			for _, line := range strings.Split(warnings.String(), "\n") {
				if strings.HasPrefix(line, dirWarn) || strings.HasPrefix(line, fileWarn) {
					goWarns = append(goWarns, line)
				}
			}
			sort.Strings(goWarns)
			if strings.Join(goIDs, ",") != tc.ids || strings.Join(goWarns, "\n") != strings.Join(tc.warns, "\n") {
				t.Fatalf("Compose: roster %v, warnings\n%s\nwant %s and\n%s", goIDs, strings.Join(goWarns, "\n"), tc.ids, strings.Join(tc.warns, "\n"))
			}
			for _, shell := range shells {
				ids, stderr := bashCompose(t, shell, root, "proj")
				var warns []string
				for _, line := range strings.Split(stderr, "\n") {
					if strings.HasPrefix(line, dirWarn) || strings.HasPrefix(line, fileWarn) {
						warns = append(warns, line)
					}
				}
				sort.Strings(warns)
				if strings.Join(ids, ",") != tc.ids {
					t.Errorf("%s: the bash composer composed %v, want %s", shell, ids, tc.ids)
				}
				if strings.Join(warns, "\n") != strings.Join(tc.warns, "\n") {
					t.Errorf("%s: the bash composer's warnings differ\n got:\n%s\nwant:\n%s", shell, strings.Join(warns, "\n"), strings.Join(tc.warns, "\n"))
				}
			}
		})
	}
}
