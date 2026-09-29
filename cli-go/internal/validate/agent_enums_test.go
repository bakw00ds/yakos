package validate

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// K-112 (c)+(d): agent frontmatter runtime / model-policy values must be ones
// the dispatcher understands.

func enumProject(t *testing.T, fm string) (out string, errs, warns int, strictErrs int) {
	t.Helper()
	proj := t.TempDir()
	dir := filepath.Join(proj, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: x\nrole: specialist\n" + fm + "---\n\n# X\n" + strings.Repeat("filler\n", 90) // stay inside the line budget
	if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir()) // no plugins
	var buf bytes.Buffer
	cfg := Config{YakosRoot: t.TempDir(), Writer: &buf, ErrWriter: &buf}
	res := RunProject(cfg, proj)
	for _, f := range res.Findings {
		switch f.Level {
		case LevelErr:
			errs++
		case LevelWarn:
			warns++
		}
	}
	cfg.Strict = true
	var buf2 bytes.Buffer
	cfg.Writer, cfg.ErrWriter = &buf2, &buf2
	for _, f := range RunProject(cfg, proj).Findings {
		if f.Level == LevelErr {
			strictErrs++
		}
	}
	return buf.String(), errs, warns, strictErrs
}

func TestAgentEnums_RejectUnknownRuntime(t *testing.T) {
	out, errs, _, _ := enumProject(t, "runtime: gemni\n")
	if errs != 1 || !strings.Contains(out, `"gemni" is not a known runtime`) {
		t.Fatalf("errs=%d out=%s", errs, out)
	}
}

func TestAgentEnums_AcceptKnownRuntimes(t *testing.T) {
	for _, rt := range []string{"claude", "claude-sdk", "codex", "agy", "antigravity-sdk"} {
		out, errs, warns, _ := enumProject(t, "runtime: "+rt+"\n")
		if errs != 0 || warns != 0 {
			t.Errorf("runtime %s: errs=%d warns=%d\n%s", rt, errs, warns, out)
		}
	}
}

func TestAgentEnums_GeminiIsDeprecationWarning(t *testing.T) {
	out, errs, warns, strictErrs := enumProject(t, "runtime: gemini\n")
	if errs != 0 || warns != 1 || strictErrs != 1 || !strings.Contains(out, "use agy") {
		t.Fatalf("errs=%d warns=%d strictErrs=%d\n%s", errs, warns, strictErrs, out)
	}
}

func TestAgentEnums_RuntimeFallback(t *testing.T) {
	if _, errs, _, _ := enumProject(t, "runtime-fallback: [codex, claude]\n"); errs != 0 {
		t.Errorf("valid fallback flagged: %d", errs)
	}
	out, errs, _, _ := enumProject(t, "runtime-fallback:\n  - codex\n  - bard\n")
	if errs != 1 || !strings.Contains(out, `"bard"`) {
		t.Fatalf("errs=%d out=%s", errs, out)
	}
}

func TestAgentEnums_PluginRuntimeAccepted(t *testing.T) {
	proj := t.TempDir()
	dir := filepath.Join(proj, ".claude", "agents")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "x.md"), []byte("---\nid: x\nruntime: myplug\n---\n# X\n"+strings.Repeat("filler\n", 90)), 0o644)
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".yakos", "plugins", "myplug"), 0o755)
	_ = os.WriteFile(filepath.Join(home, ".yakos", "plugins", "myplug", "runtime.sh"), []byte("#!/bin/sh\n"), 0o755)
	var buf bytes.Buffer
	res := RunProject(Config{YakosRoot: t.TempDir(), Writer: &buf, ErrWriter: &buf}, proj)
	for _, f := range res.Findings {
		if f.Level == LevelErr {
			t.Fatalf("plugin runtime rejected: %s", buf.String())
		}
	}
}

// knownRuntimes must track the bash resolver's built-in list.
func TestKnownRuntimesMatchBashResolver(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "cli", "lib", "runtime-resolve.sh"))
	if err != nil {
		t.Skipf("resolver not reachable: %v", err)
	}
	m := regexp.MustCompile(`(?m)^YK_RT_KNOWN_BUILTIN="([^"]*)"`).FindSubmatch(data)
	if m == nil {
		t.Fatal("YK_RT_KNOWN_BUILTIN not found")
	}
	want := strings.Fields(string(m[1]))
	if strings.Join(want, ",") != strings.Join(knownRuntimes, ",") {
		t.Errorf("knownRuntimes=%v, bash YK_RT_KNOWN_BUILTIN=%v", knownRuntimes, want)
	}
}

// The shipped framework agents must all pass.
func TestFrameworkAgentsPassEnums(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "lib", "agents")); err != nil {
		t.Skip("framework tree not reachable")
	}
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	res := RunFramework(Config{YakosRoot: root, Strict: true, Writer: &buf, ErrWriter: &buf})
	for _, f := range res.Findings {
		if f.Level == LevelErr && (strings.Contains(f.Message, "runtime") || strings.Contains(f.Message, "model-policy")) {
			t.Errorf("framework agent enum finding: %s", f.Message)
		}
	}
}
