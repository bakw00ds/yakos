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
