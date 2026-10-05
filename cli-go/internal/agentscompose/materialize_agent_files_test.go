package agentscompose

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func sampleAgent() ComposedAgent {
	return ComposedAgent{
		ID:          "backend",
		Description: `Implements "server" code, C:\x`,
		Prompt:      "# Backend\n\nYou write Go.\nUse \"quotes\" and a \\ backslash.\n\n\n",
		Tools:       []string{"Read", "Edit"},
		Model:       "",
	}
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits and symlinks; skipping on Windows")
	}
}

// emitCodex and emitAgy run an emitter that must succeed.
func emitCodex(t *testing.T, a ComposedAgent) string {
	t.Helper()
	b, err := EmitCodexTOML(a)
	if err != nil {
		t.Fatalf("EmitCodexTOML: %v", err)
	}
	return string(b)
}

func emitAgy(t *testing.T, a ComposedAgent) string {
	t.Helper()
	b, err := EmitAgySkill(a)
	if err != nil {
		t.Fatalf("EmitAgySkill: %v", err)
	}
	return string(b)
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- golden bytes --------------------------------------------------------------

func TestEmitCodexTOML_Golden(t *testing.T) {
	got := emitCodex(t, sampleAgent())
	want := "# yakos-generated: rewritten on every dispatch. Delete this line to keep your edits.\n" +
		"name = \"backend\"\n" +
		"description = \"Implements \\\"server\\\" code, C:\\\\x\"\n" +
		"developer_instructions = \"\"\"\n" +
		"# Backend\n\nYou write Go.\nUse \"quotes\" and a \\\\ backslash.\n" +
		"\"\"\"\n"
	if got != want {
		t.Errorf("codex TOML mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitCodexTOML_ModelEmptyDescriptionAndTripleQuotes(t *testing.T) {
	got := emitCodex(t, ComposedAgent{ID: "x", Model: "gpt-5", Prompt: `a """ b`})
	want := "# yakos-generated: rewritten on every dispatch. Delete this line to keep your edits.\n" +
		"name = \"x\"\n" +
		"description = \"Agent: x\"\n" +
		"model = \"gpt-5\"\n" +
		"developer_instructions = \"\"\"\n" +
		"a \\\"\\\"\\\" b\n" +
		"\"\"\"\n"
	if got != want {
		t.Errorf("mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitCodexTOML_DescriptionStaysOnOneLine(t *testing.T) {
	got := emitCodex(t, ComposedAgent{ID: "x", Description: "line one\nline two\r\nline three\rend"})
	if !strings.Contains(got, "description = \"line one line two line three end\"\n") {
		t.Errorf("description must be collapsed to one line, got %q", got)
	}
}

func TestEmitAgySkill_Golden(t *testing.T) {
	a := sampleAgent()
	a.Model = "gemini-3.1-pro"
	got := emitAgy(t, a)
	want := "---\n" +
		"name: yakos-backend\n" +
		"description: \"Implements \\\"server\\\" code, C:\\\\x\"\n" +
		"model: \"gemini-3.1-pro\"\n" +
		"tools: [\"Read\", \"Edit\"]\n" +
		"---\n" +
		"<!-- yakos-generated: rewritten on every dispatch. Delete this line to keep your edits. -->\n" +
		"\n" +
		"# Backend\n\nYou write Go.\nUse \"quotes\" and a \\ backslash.\n"
	if got != want {
		t.Errorf("agy skill mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitAgySkill_MinimalAgent(t *testing.T) {
	got := emitAgy(t, ComposedAgent{ID: "x"})
	want := "---\nname: yakos-x\ndescription: \"Agent: x\"\n---\n" +
		"<!-- yakos-generated: rewritten on every dispatch. Delete this line to keep your edits. -->\n\n\n"
	if got != want {
		t.Errorf("mismatch\n got: %q\nwant: %q", got, want)
	}
}

// ---- the model line: a Claude tier never reaches a non-claude agent file -------

var claudeTiers = []string{"haiku", "sonnet", "opus", "fable"}

// TestEmitters_NeverWriteAClaudeTierAsTheModel pins the regression found in
// review: general-codex is pinned to the alias balanced, the bash composer turns
// that into the tier "sonnet", and the emitter wrote model = "sonnet" into the
// codex agent file, which codex then refused to run ("its fixed `sonnet` model
// is not supported with this Codex ChatGPT account").
func TestEmitters_NeverWriteAClaudeTierAsTheModel(t *testing.T) {
	skipWindows(t)
	for _, tier := range claudeTiers {
		a := ComposedAgent{ID: "pinned", Description: "d", Prompt: "p\n", Model: tier, Tools: []string{"Read"}}
		for _, runtimeName := range []string{"codex", "agy"} {
			t.Run(runtimeName+"/"+tier, func(t *testing.T) {
				work := t.TempDir()
				res, err := MaterializeRuntimeAgent(runtimeName, work, a)
				if err != nil {
					t.Fatal(err)
				}
				for _, line := range strings.Split(mustRead(t, res.Path), "\n") {
					if isModelLine(line) {
						t.Errorf("%s file for model %q has a model line: %q", runtimeName, tier, line)
					}
				}
			})
		}
	}
}

func TestEmitters_WriteAModelThatIsNotATier(t *testing.T) {
	a := ComposedAgent{ID: "x", Description: "d", Prompt: "p\n", Model: "gemini-3.8-flash-high"}
	if got := emitCodex(t, a); !strings.Contains(got, "\nmodel = \"gemini-3.8-flash-high\"\n") {
		t.Errorf("a non-tier model must still be written to the codex file:\n%s", got)
	}
	if got := emitAgy(t, a); !strings.Contains(got, "\nmodel: \"gemini-3.8-flash-high\"\n") {
		t.Errorf("a non-tier model must still be written to the agy skill:\n%s", got)
	}
}

// TestFrameworkAgentsNeverGetAModelLine runs every framework agent through both
// materializers. general-codex and general-agy are the two runtime-pinned ones;
// the rest carry Claude tiers, which no codex or agy file may name.
func TestFrameworkAgentsNeverGetAModelLine(t *testing.T) {
	skipWindows(t)
	root := filepath.Join("..", "..", "..")
	agents, err := Compose(root, "")
	if err != nil || len(agents) < 30 {
		t.Skipf("framework agents not reachable from the package dir (%d, %v)", len(agents), err)
	}
	sawTier := map[string]bool{}
	for _, a := range agents {
		if a.Model != "" {
			sawTier[a.Model] = true
		}
		for _, runtimeName := range []string{"codex", "agy"} {
			res, err := MaterializeRuntimeAgent(runtimeName, t.TempDir(), a)
			if err != nil {
				t.Fatalf("%s/%s: %v", runtimeName, a.ID, err)
			}
			for _, line := range strings.Split(mustRead(t, res.Path), "\n") {
				if isModelLine(line) {
					t.Errorf("%s/%s: a model line reached a non-claude agent file: %q", runtimeName, a.ID, line)
				}
			}
		}
	}
	if !sawTier["sonnet"] {
		t.Errorf("the sweep saw no agent composed with a Claude tier (%v); it would pass vacuously", sawTier)
	}
}

// ---- control characters, lone CR and NUL -------------------------------------

// tomlRoundTrip decodes a generated codex file with python's tomllib (3.11+),
// the only TOML parser available to the tests, and returns the document.
func tomlRoundTrip(t *testing.T, content string) map[string]any {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("python3 may be a Store stub on Windows; the TOML round trip runs on the POSIX runners")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	// A python that cannot import tomllib (before 3.11) skips; one that can must
	// parse the file, and a failure from then on is the file's.
	if err := exec.Command(py, "-c", "import tomllib").Run(); err != nil {
		t.Skipf("python3 has no tomllib (needs 3.11+): %v", err)
	}
	cmd := exec.Command(py, "-c", "import sys, json, tomllib\nprint(json.dumps(tomllib.loads(sys.stdin.read(), parse_float=str)))")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the generated codex file is not valid TOML: %v\n%s", err, content)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestEmitCodexTOML_ControlCharactersAreEscaped(t *testing.T) {
	a := ComposedAgent{
		ID:          "ctl",
		Description: "d\x01e\x1b\x7ff",
		Prompt:      "a\x01b\x7fc \x1b[0m\rlone\r\ncrlf\tTAB\n\x0b\x0c",
		Model:       "m\x02",
	}
	got := emitCodex(t, a)
	for _, want := range []string{
		`description = "d\u0001e\u001B\u007Ff"`,
		"model = \"m\\u0002\"",
		"a\\u0001b\\u007Fc \\u001B[0m\\u000Dlone\r\ncrlf\tTAB\n\\u000B\\u000C\n\"\"\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%q", want, got)
		}
	}
	// No raw C0 control other than TAB, LF and a CR that starts a CRLF.
	for i := 0; i < len(got); i++ {
		c := got[i]
		switch {
		case c == '\t' || c == '\n':
		case c == '\r' && i+1 < len(got) && got[i+1] == '\n':
		case c < 0x20 || c == 0x7f:
			t.Fatalf("raw control byte %#x at %d in %q", c, i, got)
		}
	}
	doc := tomlRoundTrip(t, got)
	if doc["description"] != "d\x01e\x1b\x7ff" {
		t.Errorf("description decoded as %q", doc["description"])
	}
	if doc["model"] != "m\x02" {
		t.Errorf("model decoded as %q", doc["model"])
	}
	// TOML reads a raw CRLF as LF; the escaped lone CR and the controls survive.
	wantBody := "a\x01b\x7fc \x1b[0m\rlone\ncrlf\tTAB\n\x0b\x0c\n"
	if doc["developer_instructions"] != wantBody {
		t.Errorf("developer_instructions decoded as %q, want %q", doc["developer_instructions"], wantBody)
	}
}

// TestEmitCodexTOML_EveryC0ByteSurvivesTheRoundTrip decodes the file for a
// persona holding each control character and compares the text.
func TestEmitCodexTOML_EveryC0ByteSurvivesTheRoundTrip(t *testing.T) {
	var prompt strings.Builder
	for c := 1; c < 0x20; c++ {
		prompt.WriteString("x")
		prompt.WriteByte(byte(c))
	}
	prompt.WriteString("x\x7fy")
	doc := tomlRoundTrip(t, emitCodex(t, ComposedAgent{ID: "c0", Description: "d", Prompt: prompt.String()}))
	// A raw LF stays LF and a CR that is not followed by LF is escaped, so the
	// decoded text equals the persona, plus the newline before the closing quotes.
	if got, want := doc["developer_instructions"], prompt.String()+"\n"; got != want {
		t.Errorf("decoded persona differs:\n got %q\nwant %q", got, want)
	}
}

func TestEmitAgySkill_FrontmatterIsValidYAMLWithControlCharacters(t *testing.T) {
	a := ComposedAgent{
		ID:          "ctl",
		Description: "d\x01e \"q\" \\ \x1b\x7f\ttab",
		Prompt:      "body \x01 stays raw in the markdown\n",
		Model:       "m\x02\"x",
		Tools:       []string{"Read", "b\x7f", "c\"d"},
	}
	got := emitAgy(t, a)
	front, _, ok := strings.Cut(strings.TrimPrefix(got, "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("no frontmatter in %q", got)
	}
	var fm struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Model       string   `yaml:"model"`
		Tools       []string `yaml:"tools"`
	}
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		t.Fatalf("frontmatter is not valid YAML: %v\n%q", err, front)
	}
	if fm.Name != "yakos-ctl" || fm.Description != a.Description || fm.Model != a.Model {
		t.Errorf("frontmatter decoded as %+v", fm)
	}
	if strings.Join(fm.Tools, "|") != strings.Join(a.Tools, "|") {
		t.Errorf("tools decoded as %q, want %q", fm.Tools, a.Tools)
	}
	if strings.ContainsAny(front, "\x01\x02\x1b\x7f") {
		t.Errorf("the frontmatter holds a raw control character: %q", front)
	}
}

func TestEmitters_RefuseNULAndMaterializeWritesNothing(t *testing.T) {
	skipWindows(t)
	for _, tc := range []struct {
		field string
		agent ComposedAgent
		codex bool // refused for codex too (it does not list tools)
	}{
		{"prompt", ComposedAgent{ID: "n", Prompt: "a\x00b"}, true},
		{"description", ComposedAgent{ID: "n", Description: "a\x00b"}, true},
		{"model", ComposedAgent{ID: "n", Model: "a\x00b"}, true},
		{"tool", ComposedAgent{ID: "n", Tools: []string{"a\x00b"}}, false},
	} {
		t.Run(tc.field, func(t *testing.T) {
			if _, err := EmitAgySkill(tc.agent); err == nil || !strings.Contains(err.Error(), "NUL") {
				t.Errorf("EmitAgySkill: want an error naming NUL, got %v", err)
			}
			if _, err := EmitCodexTOML(tc.agent); (err != nil) != tc.codex {
				t.Errorf("EmitCodexTOML err = %v, want refusal = %v", err, tc.codex)
			}
			work := t.TempDir()
			if _, err := MaterializeAgyAgent(work, tc.agent); err == nil {
				t.Errorf("MaterializeAgyAgent must refuse")
			}
			if entries, _ := os.ReadDir(work); len(entries) != 0 {
				t.Errorf("a refused agent must leave the project untouched, found %v", entries)
			}
		})
	}
}

func TestEmitters_PromptLeadingLineBreaksAreDropped(t *testing.T) {
	// The bash composer keeps the blank line after the frontmatter, the Go
	// composer drops it. Either way the file is the same.
	with := ComposedAgent{ID: "x", Description: "d", Prompt: "\n\r\n# Title\n\n"}
	without := ComposedAgent{ID: "x", Description: "d", Prompt: "# Title"}
	if emitCodex(t, with) != emitCodex(t, without) || emitAgy(t, with) != emitAgy(t, without) {
		t.Errorf("leading line breaks and trailing newlines must not change the file")
	}
	// Spaces are not line breaks: an indented first line keeps its indent.
	if got := emitCodex(t, ComposedAgent{ID: "x", Description: "d", Prompt: "\n  indented"}); !strings.Contains(got, "\"\"\"\n  indented\n\"\"\"\n") {
		t.Errorf("indent lost: %q", got)
	}
}

// ---- codex materializer -------------------------------------------------------

func TestMaterializeCodexAgent_WritesPublicFileUnderCodexAgents(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(work, ".codex", "agents", "yakos-backend.toml")
	if res.Path != want || !res.Written || res.Skipped != "" {
		t.Fatalf("result = %+v, want a fresh write of %s", res, want)
	}
	if got := mustRead(t, want); got != emitCodex(t, sampleAgent()) {
		t.Errorf("file content differs from EmitCodexTOML:\n%s", got)
	}
	fi, _ := os.Stat(want)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 0644", fi.Mode().Perm())
	}
	dirFi, _ := os.Stat(filepath.Dir(want))
	if dirFi.Mode().Perm() != 0o755 {
		t.Errorf("dir mode = %o, want 0755", dirFi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(want), ".yakos-tmp-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestMaterializeCodexAgent_UnchangedFileIsNotRewritten(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	if _, err := MaterializeCodexAgent(work, sampleAgent()); err != nil {
		t.Fatal(err)
	}
	path := CodexAgentPath(work, "backend")
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	if res.Written || res.Skipped != SkipUnchanged {
		t.Errorf("result = %+v, want skipped as unchanged", res)
	}
	fi, _ := os.Stat(path)
	if !fi.ModTime().Equal(old) {
		t.Errorf("mtime moved to %v: an unchanged file must not be rewritten", fi.ModTime())
	}

	changed := sampleAgent()
	changed.Prompt += "one more line\n"
	res, err = MaterializeCodexAgent(work, changed)
	if err != nil || !res.Written {
		t.Fatalf("a changed prompt must rewrite the file: %+v, %v", res, err)
	}
	if !strings.Contains(mustRead(t, path), "one more line") {
		t.Error("rewritten file lacks the new prompt")
	}
}

func TestMaterializeCodexAgent_RefusesFileWithoutMarker(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	path := CodexAgentPath(work, "backend")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "name = \"my-own\"\ndescription = \"hand written\"\ndeveloper_instructions = \"\"\"\nkeep me\n\"\"\"\n"
	if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	if res.Written || res.Skipped != SkipNotYakosManaged {
		t.Errorf("result = %+v, want skipped as not yakos-generated", res)
	}
	if got := mustRead(t, path); got != mine {
		t.Errorf("an operator's file was modified:\n%s", got)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %o", fi.Mode().Perm())
	}
}

func TestMaterializeCodexAgent_UpgradesLegacyFileInPlace(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	path := CodexAgentPath(work, "backend")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "name = \"backend\"\ndescription = \"old\"\ndeveloper_instructions = \"\"\"\nold body\n\"\"\"\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil || !res.Written {
		t.Fatalf("a legacy yakOS-generated file must be upgraded: %+v, %v", res, err)
	}
	if !strings.HasPrefix(mustRead(t, path), codexMarkerLine+"\n") {
		t.Error("the upgraded file must carry the marker")
	}
}

func TestMaterializeCodexAgent_MarkerPastTheScanWindowDoesNotCount(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	path := CodexAgentPath(work, "backend")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("# filler\n", markerScanLines) + "# yakos-generated: too late\nname = \"other\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil || res.Written || res.Skipped != SkipNotYakosManaged {
		t.Fatalf("result = %+v, %v; the marker must be in the first %d lines", res, err, markerScanLines)
	}
}

func TestMaterializeCodexAgent_RefusesSymlinks(t *testing.T) {
	skipWindows(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "victim.toml")
	if err := os.WriteFile(target, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, setup := range map[string]func(work string){
		"symlinked .codex": func(work string) {
			must(t, os.Symlink(outside, filepath.Join(work, ".codex")))
		},
		"symlinked .codex/agents": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".codex"), 0o755))
			must(t, os.Symlink(outside, filepath.Join(work, ".codex", "agents")))
		},
		"symlinked target file": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".codex", "agents"), 0o755))
			must(t, os.Symlink(target, CodexAgentPath(work, "backend")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			setup(work)
			res, err := MaterializeCodexAgent(work, sampleAgent())
			if err == nil {
				t.Fatalf("expected a refusal, got %+v", res)
			}
			if mustRead(t, target) != "precious\n" {
				t.Fatal("a file outside the project was modified through a symlink")
			}
			if left, _ := filepath.Glob(filepath.Join(outside, "*")); len(left) != 1 {
				t.Errorf("files appeared outside the project: %v", left)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMaterialize_RejectsUnsafeIDsAndRelativeDirs(t *testing.T) {
	work := t.TempDir()
	for _, id := range []string{"", "../escape", "a/b", `a\b`, "-leading-dash", ".hidden", "has space", "semi;colon", strings.Repeat("a", 101), "nul\x00byte"} {
		a := sampleAgent()
		a.ID = id
		if _, err := MaterializeCodexAgent(work, a); err == nil {
			t.Errorf("codex: id %q must be refused", id)
		}
		if _, err := MaterializeAgyAgent(work, a); err == nil {
			t.Errorf("agy: id %q must be refused", id)
		}
	}
	for _, id := range []string{"backend", "general-codex", "a.b_c-1", "X9"} {
		a := sampleAgent()
		a.ID = id
		if _, err := MaterializeCodexAgent(work, a); err != nil {
			t.Errorf("codex: id %q must be accepted: %v", id, err)
		}
	}
	if _, err := MaterializeCodexAgent("relative/dir", sampleAgent()); err == nil {
		t.Error("a relative working directory must be refused")
	}
	if _, err := MaterializeCodexAgent("", sampleAgent()); err == nil {
		t.Error("an empty working directory must be refused")
	}
	if entries, _ := os.ReadDir(work); len(entries) != 1 { // only .codex from the accepted ids
		t.Errorf("unexpected entries after refusals: %v", entries)
	}
}

func TestMaterializeRuntimeAgent_Dispatches(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	if res, err := MaterializeRuntimeAgent("codex", work, sampleAgent()); err != nil || res.Path != CodexAgentPath(work, "backend") {
		t.Errorf("codex: %+v, %v", res, err)
	}
	if res, err := MaterializeRuntimeAgent("agy", work, sampleAgent()); err != nil || res.Path != AgySkillPath(work, "backend") {
		t.Errorf("agy: %+v, %v", res, err)
	}
	for _, rt := range []string{"claude", "gemini", "", "nope"} {
		if _, err := MaterializeRuntimeAgent(rt, work, sampleAgent()); !errors.Is(err, ErrUnsupportedRuntime) {
			t.Errorf("%q: err = %v, want ErrUnsupportedRuntime", rt, err)
		}
	}
}

func TestMaterializeCodexAgent_ConcurrentDispatchesOfOneAgent(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := MaterializeCodexAgent(work, sampleAgent()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent materialize: %v", err)
	}
	if got := mustRead(t, CodexAgentPath(work, "backend")); got != emitCodex(t, sampleAgent()) {
		t.Error("file is not the complete expected content after concurrent writes")
	}
	if left, _ := filepath.Glob(filepath.Join(work, ".codex", "agents", ".yakos-tmp-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// ---- agy materializer ----------------------------------------------------------

func TestMaterializeAgyAgent_WritesSkillDirectory(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	res, err := MaterializeAgyAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(work, ".agents", "skills", "yakos-backend", "SKILL.md")
	if res.Path != skill || !res.Written {
		t.Fatalf("result = %+v, want a fresh write of %s", res, skill)
	}
	if got := mustRead(t, skill); got != emitAgy(t, sampleAgent()) {
		t.Errorf("SKILL.md differs from EmitAgySkill:\n%s", got)
	}
	gi := filepath.Join(filepath.Dir(skill), ".gitignore")
	if got := mustRead(t, gi); got != "*\n" {
		t.Errorf(".gitignore = %q", got)
	}
	for _, p := range []string{skill, gi} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
			t.Errorf("%s mode = %o, want 0644", p, fi.Mode().Perm())
		}
	}
	// Idempotent and content-stable.
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	must(t, os.Chtimes(skill, old, old))
	res, err = MaterializeAgyAgent(work, sampleAgent())
	if err != nil || res.Written || res.Skipped != SkipUnchanged {
		t.Fatalf("second call = %+v, %v; want unchanged", res, err)
	}
	if fi, _ := os.Stat(skill); !fi.ModTime().Equal(old) {
		t.Error("an unchanged SKILL.md was rewritten")
	}
}

func TestMaterializeAgyAgent_LeavesOperatorSkillAlone(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	skill := AgySkillPath(work, "backend")
	must(t, os.MkdirAll(filepath.Dir(skill), 0o755))
	mine := "---\nname: yakos-backend\ndescription: mine\n---\nhand written\n"
	must(t, os.WriteFile(skill, []byte(mine), 0o644))
	res, err := MaterializeAgyAgent(work, sampleAgent())
	if err != nil || res.Written || res.Skipped != SkipNotYakosManaged {
		t.Fatalf("result = %+v, %v; want skipped as not yakos-generated", res, err)
	}
	if mustRead(t, skill) != mine {
		t.Error("an operator's SKILL.md was modified")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(skill), ".gitignore")); !os.IsNotExist(err) {
		t.Error("a .gitignore must not be written into a directory the operator owns")
	}
}

func TestMaterializeAgyAgent_RefusesSymlinks(t *testing.T) {
	skipWindows(t)
	outside := t.TempDir()
	for name, setup := range map[string]func(work string){
		"symlinked .agents": func(work string) {
			must(t, os.Symlink(outside, filepath.Join(work, ".agents")))
		},
		"symlinked skills dir": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".agents"), 0o755))
			must(t, os.Symlink(outside, filepath.Join(work, ".agents", "skills")))
		},
		"symlinked skill dir": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".agents", "skills"), 0o755))
			must(t, os.Symlink(outside, AgySkillDir(work, "backend")))
		},
		"symlinked SKILL.md": func(work string) {
			must(t, os.MkdirAll(AgySkillDir(work, "backend"), 0o755))
			must(t, os.WriteFile(filepath.Join(outside, "x"), []byte("precious"), 0o644))
			must(t, os.Symlink(filepath.Join(outside, "x"), AgySkillPath(work, "backend")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			setup(work)
			if res, err := MaterializeAgyAgent(work, sampleAgent()); err == nil {
				t.Fatalf("expected a refusal, got %+v", res)
			}
			if entries, _ := os.ReadDir(outside); len(entries) > 1 {
				t.Errorf("files appeared outside the project: %v", entries)
			}
		})
	}
}

// TestMaterializeAgyAgent_GeneratedFilesAreInvisibleToGit proves the nested
// .gitignore does its job without the project's own .gitignore.
func TestMaterializeAgyAgent_GeneratedFilesAreInvisibleToGit(t *testing.T) {
	skipWindows(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	if _, err := MaterializeAgyAgent(work, sampleAgent()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run("status", "--porcelain", "-uall")); got != "" {
		t.Errorf("generated skill files are visible to git: %q", got)
	}
}

func TestMaterializeAgyAgent_NormalisesGitignore(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	_, err := MaterializeAgyAgent(work, sampleAgent())
	must(t, err)
	gi := filepath.Join(AgySkillDir(work, "backend"), ".gitignore")
	must(t, os.WriteFile(gi, []byte("something else\n"), 0o644))
	if _, err := MaterializeAgyAgent(work, sampleAgent()); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, gi) != "*\n" {
		t.Error(".gitignore inside the generated skill directory must be restored")
	}
}

func TestHasMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"first line", "# yakos-generated: x\nname", true},
		{"line 12", strings.Repeat("a\n", 11) + "yakos-generated: x\n", true},
		{"line 13", strings.Repeat("a\n", 12) + "yakos-generated: x\n", false},
		{"absent", "name = \"x\"\n", false},
		{"empty", "", false},
		{"similar", "# yakos generated\n", false},
	} {
		if got := hasMarker([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: hasMarker = %v, want %v", tc.name, got, tc.want)
		}
	}
}
