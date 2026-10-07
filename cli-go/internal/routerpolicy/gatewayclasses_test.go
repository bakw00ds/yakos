package routerpolicy

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseFile(t *testing.T, body string) File {
	t.Helper()
	var f File
	if err := yaml.Unmarshal([]byte(body), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestClasses_ValidTableIsSortedAndMapsToDocumentedVariables(t *testing.T) {
	f := parseFile(t, `
gateway_classes:
  subagent: haiku
  sonnet: claude-sonnet-4-5-20250929
  Haiku: claude-haiku-4-5-20251001
  opus: anthropic.claude-opus-4-1-20250805-v1:0
  fable: us.anthropic.claude-fable-5-v1:0
`)
	got, warn := f.Classes()
	if len(warn) != 0 {
		t.Fatalf("unexpected warnings: %v", warn)
	}
	var lines []string
	for _, c := range got {
		lines = append(lines, c.EnvName+"="+c.Model)
	}
	want := []string{
		"ANTHROPIC_DEFAULT_FABLE_MODEL=us.anthropic.claude-fable-5-v1:0",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=claude-haiku-4-5-20251001",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=anthropic.claude-opus-4-1-20250805-v1:0",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=claude-sonnet-4-5-20250929",
		"CLAUDE_CODE_SUBAGENT_MODEL=haiku",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestClasses_SHAIsStableAndTracksTheTable(t *testing.T) {
	a, _ := parseFile(t, "gateway_classes: {subagent: haiku, opus: claude-opus-x}").Classes()
	b, _ := parseFile(t, "gateway_classes: {opus: claude-opus-x, subagent: haiku}").Classes()
	c, _ := parseFile(t, "gateway_classes: {subagent: sonnet, opus: claude-opus-x}").Classes()
	if a.SHA() == "" || len(a.SHA()) != 64 {
		t.Fatalf("sha = %q", a.SHA())
	}
	if a.SHA() != b.SHA() {
		t.Error("the same table in another order must have the same sha")
	}
	if a.SHA() == c.SHA() {
		t.Error("a different model must change the sha")
	}
	if (GatewayClasses{}).SHA() != "" {
		t.Error("an empty table has no sha")
	}
}

func TestClasses_MissingKeyIsEmptyAndQuiet(t *testing.T) {
	for _, body := range []string{"", "allow_unsandboxed_runtimes: [codex]\n", "gateway_classes:\n"} {
		got, warn := parseFile(t, body).Classes()
		if len(got) != 0 || len(warn) != 0 {
			t.Errorf("%q: got %v %v, want empty and quiet", body, got, warn)
		}
	}
}

// Any bad entry ignores the whole key: a half-valid table never applies.
func TestClasses_RefusesTheWholeKey(t *testing.T) {
	cases := map[string]string{
		"non-Claude id":            "gateway_classes: {subagent: haiku, haiku: gpt-6-astra}",
		"gemini id":                "gateway_classes: {subagent: gemini-3.8-flash-low}",
		"unknown class":            "gateway_classes: {subagent: haiku, compaction: haiku}",
		"tier name for alias":      "gateway_classes: {opus: opus}",
		"tier-looking non-Claude":  "gateway_classes: {subagent: codex}",
		"duplicate class":          "gateway_classes: {subagent: haiku, Subagent: sonnet}",
		"url as model":             "gateway_classes: {subagent: 'https://evil.example/v1'}",
		"flag-shaped id":           "gateway_classes: {subagent: '-claude-x'}",
		"upper case id":            "gateway_classes: {subagent: Claude-Haiku}",
		"too long id":              "gateway_classes: {subagent: claude-" + strings.Repeat("a", 64) + "}",
		"list shape":               "gateway_classes: [subagent, haiku]",
		"scalar shape":             "gateway_classes: haiku",
		"nested value":             "gateway_classes: {subagent: {model: haiku}}",
		"non-string value":         "gateway_classes: {subagent: 5}",
		"claude lookalike suffix":  "gateway_classes: {subagent: not-claude-haiku}",
		"claude prefix substitute": "gateway_classes: {subagent: evil.claude-haiku}",
	}
	for name, body := range cases {
		got, warn := parseFile(t, body).Classes()
		if len(got) != 0 {
			t.Errorf("%s: the key must be ignored, got %v", name, got)
		}
		if len(warn) != 1 || !strings.Contains(warn[0], "gateway_classes ignored") {
			t.Errorf("%s: want one warning, got %v", name, warn)
		}
		for _, w := range warn {
			if strings.Contains(w, "/") || strings.Contains(w, "evil") || strings.Contains(w, "gpt-6") || strings.Contains(w, "gemini") {
				t.Errorf("%s: warning echoes a value or a path: %q", name, w)
			}
		}
	}
}

func TestClasses_TooManyEntriesIsRefused(t *testing.T) {
	var b strings.Builder
	b.WriteString("gateway_classes:\n")
	for i := 0; i <= maxGatewayClasses; i++ {
		b.WriteString("  c" + string(rune('a'+i)) + ": haiku\n")
	}
	if got, warn := parseFile(t, b.String()).Classes(); len(got) != 0 || len(warn) != 1 {
		t.Fatalf("got %v %v", got, warn)
	}
}

func TestLoadGatewayClasses_ReadsThroughTheTrustCheck(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := t.TempDir()
	writePolicy(t, dir, "gateway_classes: {subagent: haiku}\n", 0o600)
	got, warn, err := LoadGatewayClasses(dir)
	if err != nil || len(warn) != 0 || len(got) != 1 || got[0].EnvName != "CLAUDE_CODE_SUBAGENT_MODEL" {
		t.Fatalf("trusted file: %v %v %v", got, warn, err)
	}
	// A group-writable file is refused: fail closed to no aliasing.
	writePolicy(t, dir, "gateway_classes: {subagent: haiku}\n", 0o666)
	got, _, err = LoadGatewayClasses(dir)
	if err == nil || len(got) != 0 {
		t.Fatalf("untrusted file must yield an error and no classes, got %v %v", got, err)
	}
	if got, _, err := LoadGatewayClasses(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("missing file: %v %v", got, err)
	}
	if got, _, err := LoadGatewayClasses(""); err != nil || len(got) != 0 {
		t.Fatalf("empty state dir: %v %v", got, err)
	}
}

// A malformed gateway_classes shape must not take the sandbox key down with it.
func TestLoad_BadGatewayClassesShapeDoesNotBreakTheOtherKey(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := t.TempDir()
	writePolicy(t, dir, "allow_unsandboxed_runtimes: [codex]\ngateway_classes: [x, y]\n", 0o600)
	ok, err := AllowsUnsandboxed(dir, "codex")
	if err != nil || !ok {
		t.Fatalf("got (%v, %v), want (true, nil)", ok, err)
	}
}
