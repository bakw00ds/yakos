package paritytest

// ceiling_hostile_test.go: K-168 sec-364 H1. A max_model ceiling must bound what
// the BASH relay runs even when bash reads a different agent file than the Go
// clamp ranked (a symlinked agent file, a symlinked agents directory, a helper
// file whose id: collides). The differential: the same hostile project under a
// sonnet ceiling, dispatched through the Go-native path and through the bash
// passthrough, must run the same, non-dearer model.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

// claudeModels lists every model the claude argv names: --model values and the
// models inside the --agents JSON.
func claudeModels(argv []string) []string {
	var out []string
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "--model":
			if i+1 < len(argv) {
				out = append(out, argv[i+1])
			}
		case "--agents":
			var m map[string]struct {
				Model string `json:"model"`
			}
			if i+1 < len(argv) && json.Unmarshal([]byte(argv[i+1]), &m) == nil {
				for _, a := range m {
					if a.Model != "" {
						out = append(out, a.Model)
					}
				}
			}
		}
	}
	return out
}

func hostileProject(t *testing.T, kind string) string {
	t.Helper()
	proj, elsewhere := t.TempDir(), t.TempDir()
	opus := "---\nid: backend\nrole: specialist\nmodel: opus\nmodel-policy: opus\n---\n\n## Purpose\n\nhostile.\n"
	switch kind {
	case "symlinked-file":
		mustWrite(t, filepath.Join(elsewhere, "backend.md"), opus)
		mustWrite(t, filepath.Join(proj, ".claude", "agents", ".keep"), "")
		if err := os.Symlink(filepath.Join(elsewhere, "backend.md"), filepath.Join(proj, ".claude", "agents", "backend.md")); err != nil {
			t.Fatal(err)
		}
	case "symlinked-dir":
		mustWrite(t, filepath.Join(elsewhere, "backend.md"), opus)
		if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(proj, ".claude", "agents")); err != nil {
			t.Fatal(err)
		}
	case "id-collision":
		mustWrite(t, filepath.Join(proj, ".claude", "agents", "helper.md"), opus)
	}
	return proj
}

func TestCeilingBoundsBashRelayOnHostileAgentFiles(t *testing.T) {
	e := newParityEnv(t)
	for _, kind := range []string{"symlinked-file", "symlinked-dir", "id-collision"} {
		t.Run(kind, func(t *testing.T) {
			proj := hostileProject(t, kind)
			var models = map[string][]string{}
			for _, side := range []string{"go", "bash"} {
				home := t.TempDir()
				if err := budget.SetMaxModel(filepath.Join(home, ".yakos-state"), "backend", "sonnet"); err != nil {
					t.Fatal(err)
				}
				stubs, argvFile := makeStubs(t)
				cmd := exec.Command(e.goBin, "dispatch", "backend", "do the thing", "--project", proj) //nolint:gosec // controlled test paths
				cmd.Dir = proj
				cmd.Env = []string{"HOME=" + home, "PATH=" + stubs + ":" + e.tools, "ANTHROPIC_API_KEY=x", "TMPDIR=" + os.TempDir(), "YAKOS_IMPL=" + side}
				out, _ := cmd.CombinedOutput()
				ms := claudeModels(readArgv(argvFile))
				if len(ms) == 0 {
					t.Fatalf("%s: no model reached the claude CLI:\n%s", side, out)
				}
				models[side] = ms
			}
			for side, ms := range models {
				for _, m := range ms {
					if m != "sonnet" && m != "haiku" {
						t.Errorf("%s ran model %q under a sonnet ceiling (%v)", side, m, models)
					}
				}
			}
			if strings.Join(models["go"], ",") != strings.Join(models["bash"], ",") {
				t.Errorf("Go native and bash passthrough ran different models: %v", models)
			}
		})
	}
}

// toolsWithout is e.tools minus the named commands, so "claude is not installed"
// holds even on a host that has a real one.
func toolsWithout(t *testing.T, e parityEnv, drop ...string) string {
	t.Helper()
	dir := t.TempDir()
	ents, err := os.ReadDir(e.tools)
	if err != nil {
		t.Fatal(err)
	}
next:
	for _, en := range ents {
		for _, d := range drop {
			if en.Name() == d {
				continue next
			}
		}
		if dst, err := os.Readlink(filepath.Join(e.tools, en.Name())); err == nil {
			_ = os.Symlink(dst, filepath.Join(dir, en.Name()))
		}
	}
	return dir
}

// K-168 sec-364b N1 to N4: bash must never run codex (unranked, sandbox bypassed)
// for an agent under a sonnet ceiling, however the argv, the fallback lists or the
// overlay are arranged, and where Go native refuses the dispatch bash must not run
// anything dearer. The codex stub leaves a marker file when it is started.
func TestCeilingNeverRunsCodexOnBashRelay(t *testing.T) {
	e := newParityEnv(t)
	cases := []struct {
		name     string
		fm       string
		yml      string
		extra    []string
		noClaude bool
		overlay  bool
	}{
		{name: "N1-claude-absent-agent-fallback", fm: "id: cx\nmodel: sonnet\nruntime-fallback: [codex]\n", extra: []string{"--runtime", "claude"}, noClaude: true},
		{name: "N1-claude-absent-project-fallback", fm: "id: cx\nmodel: sonnet\n", yml: "default-fallback: [codex]\n", noClaude: true},
		{name: "N2-runtime-auto", fm: "id: cx\nmodel: sonnet\nruntime-fallback: [codex]\n", extra: []string{"--runtime", "auto"}},
		{name: "N3-eval-id-decoy", fm: "id: cx\nruntime: codex\nmodel: sonnet\n", extra: []string{"--eval-run-id", "--runtime=claude"}},
		{name: "N4-overlay-ranked-codex-model", fm: "id: cx\nruntime: codex\nmodel: gpt-5.4-mini\n", overlay: true},
		{name: "explicit-codex", fm: "id: cx\nmodel: sonnet\n", extra: []string{"--runtime", "codex"}, overlay: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj := t.TempDir()
			mustWrite(t, filepath.Join(proj, ".claude", "agents", "cx.md"), "---\n"+c.fm+"---\n\n## Purpose\n\nx.\n")
			if c.yml != "" {
				mustWrite(t, filepath.Join(proj, ".yakos.yml"), c.yml)
			}
			for _, side := range []string{"go", "bash"} {
				home := t.TempDir()
				state := filepath.Join(home, ".yakos-state")
				if err := budget.SetMaxModel(state, "cx", "sonnet"); err != nil {
					t.Fatal(err)
				}
				if c.overlay {
					if err := os.WriteFile(filepath.Join(state, "model-registry.yml"), []byte("aliases:\n  cheap: {codex: gpt-5.4-mini}\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				stubs := t.TempDir()
				claudeOut, codexOut := filepath.Join(stubs, "claude.argv"), filepath.Join(stubs, "codex.argv")
				for cli, out := range map[string]string{"claude": claudeOut, "codex": codexOut} {
					if cli == "claude" && c.noClaude {
						continue
					}
					body := "#!/bin/bash\nprintf '%s\\0' \"$@\" > '" + out + "'\nexit 0\n"
					if err := os.WriteFile(filepath.Join(stubs, cli), []byte(body), 0o755); err != nil { //nolint:gosec
						t.Fatal(err)
					}
				}
				tools := e.tools
				if c.noClaude {
					tools = toolsWithout(t, e, "claude")
				}
				args := append([]string{"dispatch", "cx", "do the thing", "--project", proj}, c.extra...)
				cmd := exec.Command(e.goBin, args...) //nolint:gosec // controlled test paths
				cmd.Dir = proj
				cmd.Env = []string{"HOME=" + home, "PATH=" + stubs + ":" + tools, "ANTHROPIC_API_KEY=x", "OPENAI_API_KEY=x", "TMPDIR=" + os.TempDir(), "YAKOS_IMPL=" + side}
				out, _ := cmd.CombinedOutput()
				if _, err := os.Stat(codexOut); err == nil && (side == "bash" || !c.overlay) {
					t.Errorf("%s ran codex under a sonnet ceiling:\n%s", side, out)
				}
				for _, m := range claudeModels(readArgv(claudeOut)) {
					if m != "sonnet" && m != "haiku" {
						t.Errorf("%s ran model %q under a sonnet ceiling", side, m)
					}
				}
			}
		})
	}
}
