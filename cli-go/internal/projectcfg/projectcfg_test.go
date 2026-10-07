package projectcfg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// projectWith returns a temp project directory whose .yakos.yml is the named
// fixture from testdata (or empty when name is "").
func projectWith(t *testing.T, fixture string) string {
	t.Helper()
	dir := t.TempDir()
	if fixture == "" {
		return dir
	}
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad_AbsentFileAndEmptyProject(t *testing.T) {
	for name, project := range map[string]string{"no file": t.TempDir(), "empty path": ""} {
		cfg, warns := Load(project)
		if !reflect.DeepEqual(cfg, Config{}) || len(warns) != 0 {
			t.Errorf("%s: got %+v %v, want zero Config and no warnings", name, cfg, warns)
		}
	}
}

func TestLoad_FullFixture(t *testing.T) {
	cfg, warns := Load(projectWith(t, "full.yml"))
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	want := Config{
		DefaultRuntime:  "codex",
		DefaultFallback: []string{"claude", "agy"},
		PerDomain:       map[string]string{"code-review": "codex", "ui": "agy", "security": "claude"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

// Trailing comments are YAML comments, not part of the value.
func TestLoad_TrailingCommentsStripped(t *testing.T) {
	cfg, warns := Load(projectWith(t, "comments.yml"))
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	want := Config{DefaultRuntime: "codex", DefaultFallback: []string{"claude"}, PerDomain: map[string]string{"ui": "agy"}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

// Block-style lists are a superset of what the bash reader understands.
func TestLoad_BlockListAccepted(t *testing.T) {
	cfg, warns := Load(projectWith(t, "block-list.yml"))
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if cfg.DefaultRuntime != "claude" || !reflect.DeepEqual(cfg.DefaultFallback, []string{"codex", "agy"}) {
		t.Errorf("got %+v", cfg)
	}
}

// A file that is not valid YAML must not break dispatch: empty config plus one
// warning that names the file (decision.LoadConfig posture).
func TestLoad_MalformedYAMLYieldsEmptyConfigAndWarning(t *testing.T) {
	cfg, warns := Load(projectWith(t, "malformed.yml"))
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Errorf("malformed file must yield the zero Config, got %+v", cfg)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], FileName) || !strings.Contains(warns[0], "cannot parse") {
		t.Errorf("want one parse warning naming %s, got %v", FileName, warns)
	}
	if strings.Contains(warns[0], "\n") {
		t.Errorf("warning must be a single line: %q", warns[0])
	}
}

func TestParse_NonMappingTopLevel(t *testing.T) {
	for _, doc := range []string{"- a\n- b\n", "just a scalar\n", "42\n"} {
		cfg, warns := Parse([]byte(doc))
		if !reflect.DeepEqual(cfg, Config{}) || len(warns) != 1 {
			t.Errorf("%q: got %+v %v, want zero Config and one warning", doc, cfg, warns)
		}
	}
}

func TestParse_EmptyAndCommentOnly(t *testing.T) {
	for _, doc := range []string{"", "\n", "# nothing here\n"} {
		cfg, warns := Parse([]byte(doc))
		if !reflect.DeepEqual(cfg, Config{}) || len(warns) != 0 {
			t.Errorf("%q: got %+v %v", doc, cfg, warns)
		}
	}
}

// Wrong types are dropped key by key.
func TestParse_MistypedKeysAreIgnored(t *testing.T) {
	cfg, warns := Load(projectWith(t, "mistyped.yml"))
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Errorf("got %+v, want zero Config", cfg)
	}
	if len(warns) != 3 {
		t.Errorf("want one warning per mistyped key, got %v", warns)
	}
}

// A bad key must not cost the operator its valid siblings.
func TestParse_ValidSiblingsSurviveBadKeys(t *testing.T) {
	cfg, warns := Load(projectWith(t, "partial.yml"))
	if cfg.DefaultRuntime != "" {
		t.Errorf("a list is not a runtime id, got DefaultRuntime=%q", cfg.DefaultRuntime)
	}
	if want := []string{"claude", "agy"}; !reflect.DeepEqual(cfg.DefaultFallback, want) {
		t.Errorf("DefaultFallback = %v, want %v", cfg.DefaultFallback, want)
	}
	if want := map[string]string{"code-review": "codex", "ui": "agy"}; !reflect.DeepEqual(cfg.PerDomain, want) {
		t.Errorf("PerDomain = %v, want %v", cfg.PerDomain, want)
	}
	if len(warns) == 0 {
		t.Error("expected warnings for the dropped entries")
	}
}

// Values flow into log lines and error text; nothing outside the id alphabet
// may get through.
func TestParse_HostileValuesRejected(t *testing.T) {
	cfg, warns := Load(projectWith(t, "hostile.yml"))
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Errorf("hostile values must all be dropped, got %+v", cfg)
	}
	if len(warns) == 0 {
		t.Error("expected warnings")
	}
	for _, w := range warns {
		for _, bad := range []string{"rm -rf", "whoami", "passwd", "dangerously"} {
			if strings.Contains(w, bad) {
				t.Errorf("warning must not echo hostile values: %q", w)
			}
		}
	}
}

// A directory (or any non-regular file) called .yakos.yml is ignored rather
// than read; a FIFO here would otherwise block every dispatch.
func TestLoad_NonRegularFileIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, warns := Load(dir)
	if !reflect.DeepEqual(cfg, Config{}) || len(warns) != 1 || !strings.Contains(warns[0], "not a regular file") {
		t.Errorf("got %+v %v", cfg, warns)
	}
}

func TestLoad_OversizeFileIgnored(t *testing.T) {
	dir := t.TempDir()
	big := "default-runtime: codex\n" + strings.Repeat("# padding padding padding\n", maxFileBytes/20)
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, warns := Load(dir)
	if !reflect.DeepEqual(cfg, Config{}) || len(warns) != 1 || !strings.Contains(warns[0], "larger than") {
		t.Errorf("got %+v %v", cfg, warns)
	}
}

// RuntimeFor mirrors yk_pcfg_resolve_runtime below the frontmatter step:
// per-domain, then default-runtime.
func TestConfig_RuntimeFor(t *testing.T) {
	cfg := Config{
		DefaultRuntime: "claude",
		PerDomain:      map[string]string{"code-review": "codex"},
	}
	cases := []struct {
		name         string
		cfg          Config
		domain       string
		wantRT, from string
	}{
		{"per-domain beats default", cfg, "code-review", "codex", SourcePerDomain},
		{"unknown domain uses default", cfg, "ui", "claude", SourceProjectDefault},
		{"empty domain uses default", cfg, "", "claude", SourceProjectDefault},
		{"nothing configured", Config{}, "code-review", "", ""},
		{"per-domain only, other domain", Config{PerDomain: map[string]string{"ui": "agy"}}, "docs", "", ""},
	}
	for _, c := range cases {
		rt, from := c.cfg.RuntimeFor(c.domain)
		if rt != c.wantRT || from != c.from {
			t.Errorf("%s: RuntimeFor(%q) = (%q, %q), want (%q, %q)", c.name, c.domain, rt, from, c.wantRT, c.from)
		}
	}
}

// Differential check against the bash reader this package ports: for the
// syntax bash understands, both must return the same value for every key.
func TestParity_BashReader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash reader needs a POSIX shell")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	lib, err := filepath.Abs(filepath.Join("..", "..", "..", "cli", "lib"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "project-config.sh")); err != nil {
		t.Skip("cli/lib/project-config.sh not present (installed-binary test run)")
	}

	// bashGet runs yk_pcfg_get (or the _list variant) and returns its stdout.
	bashGet := func(project, fn, key string) string {
		t.Helper()
		script := `. "$YAKOS_LIB/project-config.sh"; ` + fn + ` "$1" "$2"`
		cmd := exec.Command(bash, "-c", script, "_", project, key)
		cmd.Env = append(os.Environ(), "YAKOS_LIB="+lib)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("bash %s %s: %v", fn, key, err)
		}
		return strings.TrimRight(string(out), "\n")
	}

	for _, fixture := range []string{"full.yml"} {
		project := projectWith(t, fixture)
		cfg, _ := Load(project)

		if got := bashGet(project, "yk_pcfg_get", "default-runtime"); got != cfg.DefaultRuntime {
			t.Errorf("%s default-runtime: bash %q, go %q", fixture, got, cfg.DefaultRuntime)
		}
		bashList := strings.Fields(bashGet(project, "yk_pcfg_get_list", "default-fallback"))
		if !reflect.DeepEqual(bashList, cfg.DefaultFallback) {
			t.Errorf("%s default-fallback: bash %v, go %v", fixture, bashList, cfg.DefaultFallback)
		}
		for dom, want := range cfg.PerDomain {
			if got := bashGet(project, "yk_pcfg_get", "per-domain."+dom); got != want {
				t.Errorf("%s per-domain.%s: bash %q, go %q", fixture, dom, got, want)
			}
		}
		// And bash must not see a domain Go does not.
		if got := bashGet(project, "yk_pcfg_get", "per-domain.not-a-domain"); got != "" {
			t.Errorf("%s: bash invented per-domain.not-a-domain=%q", fixture, got)
		}
	}
}

// The router: block can only switch runtimes and models off.
func TestParse_RouterBlockOnlyDisables(t *testing.T) {
	cfg, warns := Parse([]byte("router:\n  disable_runtimes: [codex, codex, agy]\n  disable_models: [gpt-5.6-sol, 'bad id']\n  enable_runtimes: [x]\n  rules: []\n"))
	if got := strings.Join(cfg.DisableRuntimes, ","); got != "codex,agy" {
		t.Errorf("DisableRuntimes = %q", got)
	}
	if got := strings.Join(cfg.DisableModels, ","); got != "gpt-5.6-sol" {
		t.Errorf("DisableModels = %q", got)
	}
	if !cfg.RuntimeDisabled("agy") || cfg.RuntimeDisabled("claude") || !cfg.ModelDisabled("gpt-5.6-sol") || cfg.ModelDisabled("sonnet") {
		t.Errorf("predicates wrong: %+v", cfg)
	}
	// One warning for the bad model id and one per ignored key, none naming a path.
	if len(warns) != 3 {
		t.Errorf("warnings = %v", warns)
	}
	if cfg, warns := Parse([]byte("router: nope\n")); len(cfg.DisableRuntimes) != 0 || len(warns) != 1 {
		t.Errorf("a non-mapping router key is ignored with one warning: %+v %v", cfg, warns)
	}
}

// router.never_paths (K-140) adds credential globs; a malformed entry is dropped
// without echoing it, and the key can only add (there is no way to name a
// built-in to remove).
func TestParse_RouterNeverPaths(t *testing.T) {
	cfg, warns := Parse([]byte("router:\n  never_paths: [\"internal/billing/*\", \"bad path\", \"**/.vault*\", 7]\n"))
	if got := strings.Join(cfg.NeverPaths, ","); got != "internal/billing/*,**/.vault*" {
		t.Errorf("NeverPaths = %q", got)
	}
	if len(warns) != 2 {
		t.Errorf("warns = %v", warns)
	}
	for _, w := range warns {
		if strings.Contains(w, "bad path") || strings.Contains(w, "billing") {
			t.Errorf("warning echoes an entry: %q", w)
		}
	}
	if cfg, _ := Parse([]byte("router:\n  never_paths: nope\n")); len(cfg.NeverPaths) != 0 {
		t.Errorf("non-list accepted: %v", cfg.NeverPaths)
	}
}

// router.never_paths is bounded (K-140 fixup F3): at most 32 entries, and no glob
// whose wildcards could make a match expensive.
func TestParse_NeverPathsAreBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("router:\n  never_paths:\n")
	b.WriteString("    - \"*" + strings.Repeat("[!b]", 60) + "b\"\n")
	b.WriteString("    - \"**/a/**/b/**\"\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "    - \"vault%d/*\"\n", i)
	}
	cfg, warns := Parse([]byte(b.String()))
	if len(cfg.NeverPaths) != MaxNeverPaths {
		t.Errorf("kept %d, want %d", len(cfg.NeverPaths), MaxNeverPaths)
	}
	for _, g := range cfg.NeverPaths {
		if strings.Contains(g, "[!b]") || g == "**/a/**/b/**" {
			t.Errorf("kept %q", g)
		}
	}
	if len(warns) == 0 {
		t.Error("no warning for the dropped entries")
	}
	for _, ok := range []string{"internal/billing/*", "**/.vault*", "secrets/**", "*.tfvars"} {
		if !NeverPathSimpleEnough(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
}

// N1: a long bracket expression is a CPU sink even in a short glob, and the
// total length is bounded.
func TestNeverPathSimpleEnough_BracketAndLengthBounds(t *testing.T) {
	if NeverPathSimpleEnough("*[!" + strings.Repeat("c", 30) + "]x") {
		t.Error("30-char bracket accepted")
	}
	if NeverPathSimpleEnough("**/" + strings.Repeat("a", 130)) {
		t.Error("130-byte glob accepted")
	}
	for _, ok := range []string{"**/.env*", "*[!b]x", "secrets/[a-z]*.json", "*[abc"} {
		if !NeverPathSimpleEnough(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
}
