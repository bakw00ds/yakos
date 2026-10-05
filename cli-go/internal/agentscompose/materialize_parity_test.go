package agentscompose

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Byte parity between the Go materializers and the bash emitters they port
// (cli/lib/runtimes/codex.sh yk_rt_codex_emit_toml, agy.sh yk_rt_agy_emit_md).
// The bash emitters are the oracle: for every agent in the corpus the two must
// produce identical bytes, and for every pre-existing file they must make the
// same overwrite-or-leave decision.

type parityAgent struct {
	name  string
	agent ComposedAgent
}

func parityCorpus() []parityAgent {
	long := strings.Repeat("A long line of instructions that keeps going and going.\n", 400)
	return []parityAgent{
		{"simple", ComposedAgent{ID: "simple", Description: "Does one thing.", Prompt: "# Simple\n\nDo it.\n"}},
		{"empty-description-and-body", ComposedAgent{ID: "empty", Description: "", Prompt: ""}},
		{"quotes-and-backslashes", ComposedAgent{ID: "quotes", Description: `He said "hi" and C:\temp\new`, Prompt: "say \"hi\"\nC:\\temp\\new\n\\\n\\\\\n"}},
		{"triple-quotes", ComposedAgent{ID: "triple", Description: `""" in desc`, Prompt: "a \"\"\" b\n\"\"\"\"\n\"\"\"\"\"\nend \"\"\""}},
		{"trailing-newlines-trimmed", ComposedAgent{ID: "trim", Description: "d", Prompt: "body\n\n\n\n"}},
		{"leading-newlines-kept", ComposedAgent{ID: "lead", Description: "d", Prompt: "\n\n  indented\n"}},
		{"whitespace-only-body", ComposedAgent{ID: "ws", Description: "d", Prompt: "\n\n  \n"}},
		{"crlf", ComposedAgent{ID: "crlf", Description: "line one\r\nline two\rline three\nend", Prompt: "a\r\nb\r\n"}},
		{"unicode", ComposedAgent{ID: "uni", Description: "h\u00e9llo \u65e5\u672c\u8a9e \U0001F642", Prompt: "caf\u00e9 \u2014 \u2713\n\U0001F680\n"}},
		{"model-and-tools", ComposedAgent{ID: "mt", Description: "d", Prompt: "p\n", Model: "gpt-5", Tools: []string{"Read", "Edit", "Bash(git *)", "mcp__x__y", `we"ird`}}},
		{"model-with-quote", ComposedAgent{ID: "mq", Description: "d", Prompt: "p\n", Model: `odd"model\x`}},
		{"literal-escapes-in-text", ComposedAgent{ID: "esc", Description: `\n \t \u0041`, Prompt: "\\n \\t \\u0041 \\x41 \\\"\n"}},
		{"tab-in-description", ComposedAgent{ID: "tab", Description: "a\tb", Prompt: "p\n"}},
		{"dotted-id", ComposedAgent{ID: "a.b_c-1", Description: "d", Prompt: "p\n"}},
		{"long", ComposedAgent{ID: "long", Description: "d", Prompt: long}},
		{"toml-lookalikes", ComposedAgent{ID: "toml", Description: "d", Prompt: "true\n123\n[array]\n# not a comment\nkey = \"value\"\n"}},
	}
}

func repoLibDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "cli", "lib")
	if _, err := os.Stat(filepath.Join(dir, "runtimes", "codex.sh")); err != nil {
		t.Skipf("bash runtime adapters not reachable from the package dir: %v", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func requireBashPython(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash emitters; skipping on Windows")
	}
	for _, bin := range []string{"bash", "python3", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
}

func agentJSON(t *testing.T, a ComposedAgent) string {
	t.Helper()
	obj := map[string]any{"description": a.Description, "prompt": a.Prompt}
	if a.Model != "" {
		obj["model"] = a.Model
	}
	if len(a.Tools) > 0 {
		obj["tools"] = a.Tools
	}
	b, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runBashEmitter sources the runtime adapter and calls one emitter function.
func runBashEmitter(t *testing.T, lib, adapter, fn, id, agentJSON, outDir string) {
	t.Helper()
	script := `set -eu
export YAKOS_LIB="$1"
. "$YAKOS_LIB/runtimes/` + adapter + `.sh"
` + fn + ` "$2" "$3" "$4" >/dev/null`
	cmd := exec.Command("bash", "-c", script, "bash", lib, id, agentJSON, outDir)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "PYTHONUTF8=0") // prove the emitter does not depend on the locale
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash %s %s: %v\n%s", adapter, fn, err, out)
	}
}

func TestMaterializeParity_CodexTOMLMatchesBashEmitter(t *testing.T) {
	requireBashPython(t)
	lib := repoLibDir(t)
	for _, tc := range parityCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			runBashEmitter(t, lib, "codex", "yk_rt_codex_emit_toml", tc.agent.ID, agentJSON(t, tc.agent), out)
			want, err := os.ReadFile(filepath.Join(out, "yakos-"+tc.agent.ID+".toml"))
			if err != nil {
				t.Fatal(err)
			}
			if got := EmitCodexTOML(tc.agent); string(got) != string(want) {
				t.Errorf("Go and bash codex emitters differ\n go:   %q\n bash: %q", got, want)
			}
			// The materializer writes the same bytes at the same relative path.
			work := t.TempDir()
			if _, err := MaterializeCodexAgent(work, tc.agent); err != nil {
				t.Fatal(err)
			}
			onDisk, _ := os.ReadFile(CodexAgentPath(work, tc.agent.ID))
			if string(onDisk) != string(want) {
				t.Errorf("materialized file differs from bash output")
			}
		})
	}
}

func TestMaterializeParity_AgySkillMatchesBashEmitter(t *testing.T) {
	requireBashPython(t)
	lib := repoLibDir(t)
	for _, tc := range parityCorpus() {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir() // plays the role of <project>/.agents/skills
			runBashEmitter(t, lib, "agy", "yk_rt_agy_emit_md", tc.agent.ID, agentJSON(t, tc.agent), out)
			want, err := os.ReadFile(filepath.Join(out, "yakos-"+tc.agent.ID, "SKILL.md"))
			if err != nil {
				t.Fatal(err)
			}
			if got := EmitAgySkill(tc.agent); string(got) != string(want) {
				t.Errorf("Go and bash agy emitters differ\n go:   %q\n bash: %q", got, want)
			}
			wantGI, _ := os.ReadFile(filepath.Join(out, "yakos-"+tc.agent.ID, ".gitignore"))
			work := t.TempDir()
			if _, err := MaterializeAgyAgent(work, tc.agent); err != nil {
				t.Fatal(err)
			}
			gotGI, _ := os.ReadFile(filepath.Join(AgySkillDir(work, tc.agent.ID), ".gitignore"))
			if string(gotGI) != string(wantGI) {
				t.Errorf(".gitignore differs: go %q, bash %q", gotGI, wantGI)
			}
			onDisk, _ := os.ReadFile(AgySkillPath(work, tc.agent.ID))
			if string(onDisk) != string(want) {
				t.Errorf("materialized SKILL.md differs from bash output")
			}
		})
	}
}

// TestMaterializeParity_OverwriteDecisionsMatch gives both implementations the
// same pre-existing file and requires them to agree whether it is replaced.
func TestMaterializeParity_OverwriteDecisionsMatch(t *testing.T) {
	requireBashPython(t)
	lib := repoLibDir(t)
	a := ComposedAgent{ID: "backend", Description: "d", Prompt: "p\n"}
	filler := strings.Repeat("# filler\n", markerScanLines)
	codexCases := map[string]string{
		"operator file":             "name = \"mine\"\ndescription = \"x\"\n",
		"legacy generated":          "name = \"backend\"\ndescription = \"old\"\ndeveloper_instructions = \"\"\"\nold\n\"\"\"\n",
		"legacy with crlf":          "name = \"backend\"\r\ndescription = \"old\"\r\n",
		"legacy but other id":       "name = \"other\"\ndescription = \"old\"\n",
		"legacy missing desc":       "name = \"backend\"\nmodel = \"x\"\n",
		"marked":                    codexMarkerLine + "\nname = \"backend\"\nedited by hand\n",
		"marker on line 12":         strings.Repeat("#\n", 11) + codexMarkerLine + "\n",
		"marker too late (line 13)": filler + codexMarkerLine + "\n",
		"empty file":                "",
	}
	for name, existing := range codexCases {
		t.Run("codex/"+name, func(t *testing.T) {
			bashDir, goWork := t.TempDir(), t.TempDir()
			bashFile := filepath.Join(bashDir, "yakos-backend.toml")
			goFile := CodexAgentPath(goWork, "backend")
			must(t, os.MkdirAll(filepath.Dir(goFile), 0o755))
			must(t, os.WriteFile(bashFile, []byte(existing), 0o644))
			must(t, os.WriteFile(goFile, []byte(existing), 0o644))

			runBashEmitter(t, lib, "codex", "yk_rt_codex_emit_toml", "backend", agentJSON(t, a), bashDir)
			if _, err := MaterializeCodexAgent(goWork, a); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(bashFile)
			g, _ := os.ReadFile(goFile)
			if string(b) != string(g) {
				t.Errorf("decisions differ\n bash: %q\n go:   %q", b, g)
			}
		})
	}
	agyCases := map[string]string{
		"operator skill":    "---\nname: yakos-backend\n---\nmine\n",
		"marked":            "---\nname: x\n---\n" + agyMarkerLine + "\nstale\n",
		"marker too late":   filler + agyMarkerLine + "\n",
		"empty file":        "",
		"legacy flat style": "---\nname: backend\ndescription: \"d\"\n---\n\nold body\n", // no marker: an operator's file at the new path
	}
	for name, existing := range agyCases {
		t.Run("agy/"+name, func(t *testing.T) {
			bashDir, goWork := t.TempDir(), t.TempDir()
			bashFile := filepath.Join(bashDir, "yakos-backend", "SKILL.md")
			goFile := AgySkillPath(goWork, "backend")
			must(t, os.MkdirAll(filepath.Dir(bashFile), 0o755))
			must(t, os.MkdirAll(filepath.Dir(goFile), 0o755))
			must(t, os.WriteFile(bashFile, []byte(existing), 0o644))
			must(t, os.WriteFile(goFile, []byte(existing), 0o644))

			runBashEmitter(t, lib, "agy", "yk_rt_agy_emit_md", "backend", agentJSON(t, a), bashDir)
			if _, err := MaterializeAgyAgent(goWork, a); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(bashFile)
			g, _ := os.ReadFile(goFile)
			if string(b) != string(g) {
				t.Errorf("decisions differ\n bash: %q\n go:   %q", b, g)
			}
			_, bErr := os.Stat(filepath.Join(filepath.Dir(bashFile), ".gitignore"))
			_, gErr := os.Stat(filepath.Join(filepath.Dir(goFile), ".gitignore"))
			// bash writes .gitignore only on its emit path; both must agree.
			if os.IsNotExist(bErr) != os.IsNotExist(gErr) {
				t.Errorf(".gitignore presence differs: bash exists=%v go exists=%v", !os.IsNotExist(bErr), !os.IsNotExist(gErr))
			}
		})
	}
}
