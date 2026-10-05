package agentscompose

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Byte parity between the Go materializers and the bash emitters they port
// (cli/lib/runtimes/codex.sh yk_rt_codex_emit_toml, agy.sh yk_rt_agy_emit_md).
//
// The bash emitters are the oracle: for the same agent JSON the Go emitter, the
// bash python path and the bash jq fallback (taken when python3 is absent) must
// write identical bytes, and for every pre-existing file they must make the same
// overwrite-or-leave decision. This is what "the emitters are byte-identical"
// means. It does not mean the files for a real agent are identical when the two
// composers feed them different JSON; TestMaterializeParity_RealFrameworkAgents
// checks that end to end for every framework agent.

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
		{"quote-runs", ComposedAgent{ID: "qruns", Description: "d", Prompt: strings.Repeat(`"`, 7) + "\n\"\"\n\\\"\"\"\n"}},
		{"trailing-newlines-trimmed", ComposedAgent{ID: "trim", Description: "d", Prompt: "body\n\n\n\n"}},
		// The bash composer keeps the blank line after the frontmatter in a prompt
		// and the Go composer drops it; both emitters drop leading line breaks, so
		// either composer's output yields the same file.
		{"leading-newlines-trimmed", ComposedAgent{ID: "lead", Description: "d", Prompt: "\n\n  indented\n"}},
		{"leading-crlf-trimmed", ComposedAgent{ID: "leadcrlf", Description: "d", Prompt: "\r\n\r\nbody\r\n"}},
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

		// A Claude tier is what the composers produce for an agent pinned to an
		// alias such as balanced. It must never reach a codex or agy file.
		{"tier-haiku", ComposedAgent{ID: "t1", Description: "d", Prompt: "p\n", Model: "haiku", Tools: []string{"Read"}}},
		{"tier-sonnet", ComposedAgent{ID: "t2", Description: "d", Prompt: "p\n", Model: "sonnet", Tools: []string{"Read"}}},
		{"tier-opus", ComposedAgent{ID: "t3", Description: "d", Prompt: "p\n", Model: "opus"}},
		{"tier-fable", ComposedAgent{ID: "t4", Description: "d", Prompt: "p\n", Model: "fable"}},
		// Only the four exact names are tiers (the composers emit nothing else), so
		// a lookalike is an operator's model string and is written as given.
		{"tier-lookalike-case", ComposedAgent{ID: "t5", Description: "d", Prompt: "p\n", Model: "Sonnet"}},
		{"tier-lookalike-claude-id", ComposedAgent{ID: "t6", Description: "d", Prompt: "p\n", Model: "claude-sonnet-5-5-high"}},

		// Characters TOML and YAML double-quoted scalars may not hold raw.
		{"control-chars-in-body", ComposedAgent{ID: "ctl", Description: "d", Prompt: "a\x01b\x02\x03 \x1b[31mred\x1b[0m \x7f del \x0b\x0c\x08 tab\t end\x1f\n"}},
		{"lone-cr-in-body", ComposedAgent{ID: "lcr", Description: "d", Prompt: "one\rtwo\r\nthree\r\n\r\nfour\r"}},
		{"cr-before-crlf", ComposedAgent{ID: "crcrlf", Description: "d", Prompt: "x\r\r\ny\n"}},
		{"trailing-crlf", ComposedAgent{ID: "tcrlf", Description: "d", Prompt: "abc\r\n"}},
		{"control-chars-in-fields", ComposedAgent{ID: "ctlf", Description: "d\x01\x1b\x7f\ttab", Prompt: "p\n", Model: "m\x01\x7f", Tools: []string{"a\x02", "b\x7f", "c\td"}}},
		{"unicode-separators-stay-raw", ComposedAgent{ID: "sep", Description: "a\u0085b\u2028c\u2029d", Prompt: "e\u0085f\u2028g\u2029h\ufeffi\n"}},
		{"every-c0-control", ComposedAgent{ID: "c0", Description: allC0("d"), Prompt: allC0("p") + "\n"}},
	}
}

// allC0 returns tag with every C0 control character except NUL spliced in.
func allC0(tag string) string {
	var b strings.Builder
	b.WriteString(tag)
	for c := 1; c < 0x20; c++ {
		b.WriteByte(byte(c))
		b.WriteString(tag)
	}
	b.WriteByte(0x7f)
	return b.String()
}

// skipOrFailInCI skips the test, except under CI (GitHub Actions sets CI=true),
// where a missing prerequisite is a failure: byte parity with the bash emitters
// must not quietly turn into a skip on the runners that are meant to enforce it.
func skipOrFailInCI(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func repoLibDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "cli", "lib")
	if _, err := os.Stat(filepath.Join(dir, "runtimes", "codex.sh")); err != nil {
		skipOrFailInCI(t, "bash runtime adapters not reachable from the package dir: %v", err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// repoRootDir is the checkout root (the directory holding lib/agents and cli/).
func repoRootDir(t *testing.T) string {
	t.Helper()
	return filepath.Dir(filepath.Dir(repoLibDir(t)))
}

func requireBashPython(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash emitters; skipping on Windows")
	}
	for _, bin := range []string{"bash", "python3", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			skipOrFailInCI(t, "%s not on PATH: %v", bin, err)
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

// bashMode selects which bash implementation of an emitter runs.
type bashMode string

const (
	bashPython bashMode = "python" // python3 present: the normal path
	bashJQ     bashMode = "jq"     // python3 absent: the fallback
)

// batchEmitScript runs both bash emitters over every <in>/<k>.json (agent
// JSON) with its id in <in>/<k>.id, writing the results under <out>/<k>/.
// One bash process serves a whole batch so the corpus stays fast.
const batchEmitScript = `set -u
export YAKOS_LIB="$1"; in="$2"; out="$3"; mode="$4"
. "$YAKOS_LIB/runtimes/codex.sh"
. "$YAKOS_LIB/runtimes/agy.sh"
if [ "$mode" = jq ]; then yk_emit_check_python() { return 1; }; fi
for f in "$in"/*.json; do
    k="$(basename "$f" .json)"
    id="$(cat "$in/$k.id")"
    json="$(cat "$f")"
    mkdir -p "$out/$k/codex" "$out/$k/agy"
    yk_rt_codex_emit_toml "$id" "$json" "$out/$k/codex" >/dev/null 2>"$out/$k/codex.err"
    echo "$?" > "$out/$k/codex.rc"
    yk_rt_agy_emit_md "$id" "$json" "$out/$k/agy" >/dev/null 2>"$out/$k/agy.err"
    echo "$?" > "$out/$k/agy.rc"
done
`

type bashBatch struct {
	out string
}

// emitWithBash runs the bash emitters over agents (case key -> id, JSON) in the
// given mode and returns where the results are.
func emitWithBash(t *testing.T, lib string, mode bashMode, ids, jsons []string) bashBatch {
	t.Helper()
	in, out := t.TempDir(), t.TempDir()
	for i := range ids {
		k := strconv.Itoa(i)
		must(t, os.WriteFile(filepath.Join(in, k+".id"), []byte(ids[i]), 0o644))
		must(t, os.WriteFile(filepath.Join(in, k+".json"), []byte(jsons[i]), 0o644))
	}
	cmd := exec.Command("bash", "-c", batchEmitScript, "bash", lib, in, out, string(mode))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "PYTHONUTF8=0") // prove the emitters do not depend on the locale
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash batch emit (%s): %v\n%s", mode, err, b)
	}
	return bashBatch{out: out}
}

func (b bashBatch) codexFile(i int, id string) string {
	return filepath.Join(b.out, strconv.Itoa(i), "codex", "yakos-"+id+".toml")
}

func (b bashBatch) agyDir(i int, id string) string {
	return filepath.Join(b.out, strconv.Itoa(i), "agy", "yakos-"+id)
}

func (b bashBatch) rc(t *testing.T, i int, kind string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(b.out, strconv.Itoa(i), kind+".rc"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func corpusIDsAndJSON(t *testing.T, corpus []parityAgent) (ids, jsons []string) {
	t.Helper()
	for _, c := range corpus {
		ids = append(ids, c.agent.ID)
		jsons = append(jsons, agentJSON(t, c.agent))
	}
	return ids, jsons
}

// TestMaterializeParity_Corpus holds the Go emitters and both bash paths to the
// same bytes for every agent in the corpus.
func TestMaterializeParity_Corpus(t *testing.T) {
	requireBashPython(t)
	t.Parallel()
	lib := repoLibDir(t)
	corpus := parityCorpus()
	ids, jsons := corpusIDsAndJSON(t, corpus)
	for _, mode := range []bashMode{bashPython, bashJQ} {
		batch := emitWithBash(t, lib, mode, ids, jsons)
		for i, tc := range corpus {
			t.Run(string(mode)+"/codex/"+tc.name, func(t *testing.T) {
				want, err := os.ReadFile(batch.codexFile(i, tc.agent.ID))
				if err != nil {
					t.Fatalf("bash (%s) wrote no file (rc=%s): %v", mode, batch.rc(t, i, "codex"), err)
				}
				if got := emitCodex(t, tc.agent); got != string(want) {
					t.Errorf("Go and bash (%s) codex emitters differ\n go:   %q\n bash: %q", mode, got, want)
				}
				if mode != bashPython {
					return
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
			t.Run(string(mode)+"/agy/"+tc.name, func(t *testing.T) {
				dir := batch.agyDir(i, tc.agent.ID)
				want, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
				if err != nil {
					t.Fatalf("bash (%s) wrote no SKILL.md (rc=%s): %v", mode, batch.rc(t, i, "agy"), err)
				}
				if got := emitAgy(t, tc.agent); got != string(want) {
					t.Errorf("Go and bash (%s) agy emitters differ\n go:   %q\n bash: %q", mode, got, want)
				}
				wantGI, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
				if string(wantGI) != agySkillGitignore {
					t.Errorf("bash (%s) .gitignore = %q, want %q", mode, wantGI, agySkillGitignore)
				}
				if mode != bashPython {
					return
				}
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
}

// TestMaterializeParity_NULIsRefusedEverywhere: an agent text holding a NUL byte
// cannot be written to a TOML or Markdown file, so the Go emitter and both bash
// paths refuse it and write no file. (codex files do not list tools, so a NUL
// there is harmless to codex and is not refused.)
func TestMaterializeParity_NULIsRefusedEverywhere(t *testing.T) {
	requireBashPython(t)
	t.Parallel()
	lib := repoLibDir(t)
	cases := []struct {
		name           string
		agent          ComposedAgent
		codexRefuses   bool
		agyRefuses     bool
		expectNULInOut string
	}{
		{"prompt", ComposedAgent{ID: "n1", Description: "d", Prompt: "a\x00b\n"}, true, true, ""},
		{"description", ComposedAgent{ID: "n2", Description: "a\x00b", Prompt: "p\n"}, true, true, ""},
		{"model", ComposedAgent{ID: "n3", Description: "d", Prompt: "p\n", Model: "m\x00x"}, true, true, ""},
		{"tool", ComposedAgent{ID: "n4", Description: "d", Prompt: "p\n", Tools: []string{"a\x00b"}}, false, true, ""},
		{"nowhere", ComposedAgent{ID: "n5", Description: "d", Prompt: "p\n"}, false, false, ""},
	}
	ids := make([]string, len(cases))
	jsons := make([]string, len(cases))
	for i, c := range cases {
		ids[i] = c.agent.ID
		jsons[i] = agentJSON(t, c.agent)
	}
	for _, mode := range []bashMode{bashPython, bashJQ} {
		batch := emitWithBash(t, lib, mode, ids, jsons)
		for i, tc := range cases {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				_, goCodexErr := EmitCodexTOML(tc.agent)
				_, goAgyErr := EmitAgySkill(tc.agent)
				if (goCodexErr != nil) != tc.codexRefuses {
					t.Errorf("Go codex emitter: err = %v, want refusal = %v", goCodexErr, tc.codexRefuses)
				}
				if (goAgyErr != nil) != tc.agyRefuses {
					t.Errorf("Go agy emitter: err = %v, want refusal = %v", goAgyErr, tc.agyRefuses)
				}
				if goCodexErr != nil && !strings.Contains(goCodexErr.Error(), "NUL") {
					t.Errorf("the error should name the NUL byte, got %v", goCodexErr)
				}
				_, codexErr := os.Stat(batch.codexFile(i, tc.agent.ID))
				_, agyErr := os.Stat(filepath.Join(batch.agyDir(i, tc.agent.ID), "SKILL.md"))
				if wrote := codexErr == nil; wrote == tc.codexRefuses {
					t.Errorf("bash (%s) codex: file written = %v, want refusal = %v", mode, wrote, tc.codexRefuses)
				}
				if wrote := agyErr == nil; wrote == tc.agyRefuses {
					t.Errorf("bash (%s) agy: file written = %v, want refusal = %v", mode, wrote, tc.agyRefuses)
				}
				if tc.agyRefuses {
					if _, err := os.Stat(batch.agyDir(i, tc.agent.ID)); err == nil {
						t.Errorf("bash (%s) agy left an empty skill directory behind", mode)
					}
				}
				// A refused agent must not stop the dispatch: the emitter returns 0.
				if rc := batch.rc(t, i, "codex"); rc != "0" {
					t.Errorf("bash (%s) codex emitter exit status = %s, want 0", mode, rc)
				}
				// The materializer refuses before it creates anything.
				work := t.TempDir()
				if _, err := MaterializeCodexAgent(work, tc.agent); (err != nil) != tc.codexRefuses {
					t.Errorf("MaterializeCodexAgent err = %v, want refusal = %v", err, tc.codexRefuses)
				}
				if tc.codexRefuses {
					if _, err := os.Stat(filepath.Join(work, ".codex")); err == nil {
						t.Errorf("a refused codex agent must not create .codex")
					}
				}
				workAgy := t.TempDir()
				if _, err := MaterializeAgyAgent(workAgy, tc.agent); (err != nil) != tc.agyRefuses {
					t.Errorf("MaterializeAgyAgent err = %v, want refusal = %v", err, tc.agyRefuses)
				}
				if tc.agyRefuses {
					if _, err := os.Stat(filepath.Join(workAgy, ".agents")); err == nil {
						t.Errorf("a refused agy agent must not create .agents")
					}
				}
			})
		}
	}
}

// bashComposeFramework runs the bash composer over the checkout's framework
// agents and returns the raw JSON of each agent, keyed by id.
func bashComposeFramework(t *testing.T, root string) map[string]json.RawMessage {
	t.Helper()
	script := `set -eu
export YAKOS_ROOT="$1" YAKOS_LIB="$1/cli/lib"
. "$YAKOS_LIB/compat.sh"
. "$YAKOS_LIB/agents-compose.sh"
yk_agents_compose "$YAKOS_ROOT" ""`
	cmd := exec.Command("bash", "-c", script, "bash", root)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("bash composer: %v\n%s", err, stderr.String())
	}
	var roster map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &roster); err != nil {
		t.Fatalf("bash composer output is not a JSON object: %v", err)
	}
	return roster
}

// TestMaterializeParity_RealFrameworkAgents is the end-to-end check the corpus
// cannot give: for every framework agent, the file the bash path writes (bash
// composer, then the bash emitter) is byte-identical to the file the Go path
// writes (Go composer, then the Go emitter). The two composers hand the emitters
// different JSON (the bash one keeps a model tier and the blank line after the
// frontmatter), and the emitters absorb both differences, so the files agree.
func TestMaterializeParity_RealFrameworkAgents(t *testing.T) {
	requireBashPython(t)
	t.Parallel()
	lib := repoLibDir(t)
	root := repoRootDir(t)

	goAgents, err := Compose(root, "")
	if err != nil {
		t.Fatal(err)
	}
	bashRoster := bashComposeFramework(t, root)
	if len(goAgents) < 30 {
		t.Fatalf("Go composer returned only %d framework agents", len(goAgents))
	}
	goByID := map[string]ComposedAgent{}
	for _, a := range goAgents {
		if _, ok := bashRoster[a.ID]; !ok {
			t.Errorf("agent %q composed by Go but not by bash", a.ID)
			continue
		}
		goByID[a.ID] = a
	}
	for id := range bashRoster {
		if _, ok := goByID[id]; !ok {
			t.Errorf("agent %q composed by bash but not by Go", id)
		}
	}
	ids := make([]string, 0, len(goByID))
	for id := range goByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	jsons := make([]string, len(ids))
	for i, id := range ids {
		jsons[i] = string(bashRoster[id])
	}

	for _, mode := range []bashMode{bashPython, bashJQ} {
		batch := emitWithBash(t, lib, mode, ids, jsons)
		for i, id := range ids {
			t.Run(string(mode)+"/"+id, func(t *testing.T) {
				a := goByID[id]
				wantCodex, err := os.ReadFile(batch.codexFile(i, id))
				if err != nil {
					t.Fatal(err)
				}
				if got := emitCodex(t, a); got != string(wantCodex) {
					t.Errorf("codex file differs between the bash path and the Go path\n go:   %q\n bash: %q", got, wantCodex)
				}
				wantAgy, err := os.ReadFile(filepath.Join(batch.agyDir(i, id), "SKILL.md"))
				if err != nil {
					t.Fatal(err)
				}
				if got := emitAgy(t, a); got != string(wantAgy) {
					t.Errorf("agy skill differs between the bash path and the Go path\n go:   %q\n bash: %q", got, wantAgy)
				}
				// And no Claude tier in either, whichever composer ran.
				for _, f := range [][]byte{wantCodex, wantAgy} {
					for _, line := range strings.Split(string(f), "\n") {
						if isModelLine(line) {
							t.Errorf("a model line reached a non-claude agent file: %q", line)
						}
					}
				}
			})
		}
	}
}

// isModelLine reports whether a generated file line sets a model in either
// format (model = "..." in TOML, model: "..." in the skill frontmatter).
func isModelLine(line string) bool {
	return strings.HasPrefix(line, "model = ") || strings.HasPrefix(line, "model: ")
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
