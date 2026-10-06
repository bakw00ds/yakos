package modelreg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goodOverlay = `# the operator's registry overlay
version: 1
models:
  gpt-5.6-sol:
    enabled: false
  gpt-reserve:
    enabled: true
  claude-opus-5-5-high:
    billing: api
    pricing: {input: 5, output: 25, cache_read: 0.5}
aliases:
  balanced:
    codex: gpt-5.6-terra
  best:
    agy: ""
discovery:
  admit: [agy]
`

func TestLoadOverlay_MissingFileIsEmptyAndSilent(t *testing.T) {
	ov, warns := LoadOverlay(privateStateDir(t))
	if len(ov.Models)+len(ov.Aliases)+len(ov.Admit) != 0 || len(warns) != 0 {
		t.Errorf("missing overlay: %+v, %v", ov, warns)
	}
	ov, warns = LoadOverlay("")
	if len(ov.Models) != 0 || len(warns) != 0 {
		t.Errorf("no state directory: %+v, %v", ov, warns)
	}
	if _, warns := LoadOverlay(filepath.Join(t.TempDir(), "no-such-dir")); len(warns) != 0 {
		t.Errorf("a missing state directory is not a trust failure: %v", warns)
	}
}

func TestLoadOverlay_ReadsAGoodFile(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, goodOverlay, 0o600)
	ov, warns := LoadOverlay(dir)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	if p := ov.Models["gpt-5.6-sol"]; p.Enabled == nil || *p.Enabled {
		t.Errorf("gpt-5.6-sol patch = %+v", p)
	}
	if p := ov.Models["gpt-reserve"]; p.Enabled == nil || !*p.Enabled {
		t.Errorf("gpt-reserve patch = %+v", p)
	}
	p := ov.Models["claude-opus-5-5-high"]
	if p.Billing != BillingAPI || p.Pricing == nil || p.Pricing.Input != 5 || p.Pricing.Output != 25 || p.Pricing.CacheRead != 0.5 {
		t.Errorf("opus patch = %+v / %+v", p, p.Pricing)
	}
	if ov.Aliases["balanced"]["codex"] != "gpt-5.6-terra" {
		t.Errorf("aliases = %v", ov.Aliases)
	}
	if v, ok := ov.Aliases["best"]["agy"]; !ok || v != "" {
		t.Errorf("an explicit empty mapping must survive (it clears an alias): %v", ov.Aliases)
	}
	if len(ov.Admit) != 1 || ov.Admit[0] != "agy" {
		t.Errorf("admit = %v", ov.Admit)
	}
}

// The overlay LOOSENS, so it is read only when nobody else could have written it.
// Each case here makes one of the trust conditions false and checks the whole
// file is refused: nothing from a refused file may reach the registry.
func TestLoadOverlay_UntrustedFilesAreIgnored(t *testing.T) {
	skipIfNoPosixModes(t)
	type tc struct {
		name  string
		setup func(t *testing.T) string // returns the state dir
		want  string
	}
	cases := []tc{
		{"symlink to a real file", func(t *testing.T) string {
			dir := privateStateDir(t)
			elsewhere := filepath.Join(t.TempDir(), "planted.yml")
			if err := os.WriteFile(elsewhere, []byte(goodOverlay), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, filepath.Join(dir, OverlayFileName)); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "is a symlink"},
		{"dangling symlink", func(t *testing.T) string {
			dir := privateStateDir(t)
			if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), filepath.Join(dir, OverlayFileName)); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "is a symlink"},
		{"directory in place of the file", func(t *testing.T) string {
			dir := privateStateDir(t)
			if err := os.Mkdir(filepath.Join(dir, OverlayFileName), 0o700); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "is not a regular file"},
		{"world-writable file", func(t *testing.T) string {
			dir := privateStateDir(t)
			writeOverlay(t, dir, goodOverlay, 0o666)
			return dir
		}, "group or world writable"},
		{"group-writable file", func(t *testing.T) string {
			dir := privateStateDir(t)
			writeOverlay(t, dir, goodOverlay, 0o660)
			return dir
		}, "group or world writable"},
		{"other-writable only", func(t *testing.T) string {
			dir := privateStateDir(t)
			writeOverlay(t, dir, goodOverlay, 0o602)
			return dir
		}, "group or world writable"},
		{"group-writable directory", func(t *testing.T) string {
			dir := privateStateDir(t)
			writeOverlay(t, dir, goodOverlay, 0o600)
			if err := os.Chmod(dir, 0o770); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "group or world writable"},
		{"world-writable directory", func(t *testing.T) string {
			dir := privateStateDir(t)
			writeOverlay(t, dir, goodOverlay, 0o600)
			if err := os.Chmod(dir, 0o777); err != nil {
				t.Fatal(err)
			}
			return dir
		}, "group or world writable"},
		{"state directory is a symlink", func(t *testing.T) string {
			real := privateStateDir(t)
			writeOverlay(t, real, goodOverlay, 0o600)
			link := filepath.Join(t.TempDir(), "state-link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			return link
		}, "is a symlink"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := c.setup(t)
			ov, warns := LoadOverlay(dir)
			if len(ov.Models)+len(ov.Aliases)+len(ov.Admit) != 0 {
				t.Fatalf("a refused overlay must contribute nothing, got %+v", ov)
			}
			if len(warns) != 1 || !strings.Contains(warns[0], "ignored") || !strings.Contains(warns[0], c.want) {
				t.Errorf("warnings = %v, want one saying it was ignored because %q", warns, c.want)
			}
			// Warnings reach stderr (and later API responses): they name the role
			// of what was refused and never a path.
			if strings.Contains(warns[0], dir) || strings.Contains(warns[0], os.TempDir()) {
				t.Errorf("the warning carries a path: %q", warns[0])
			}
		})
	}
}

// A file the operator made with an editor is 0644 in a 0755 directory: nobody else
// can write it, so it is trusted. This is the rule routerpolicy and the budget
// policy apply (routerpolicy's ReadableByOthersIsStillTrusted), kept here so a
// stricter reading cannot slip in unnoticed.
func TestLoadOverlay_ReadableByOthersIsTrusted(t *testing.T) {
	skipIfNoPosixModes(t)
	dir := privateStateDir(t)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o600, 0o644, 0o640, 0o400} {
		writeOverlay(t, dir, goodOverlay, mode)
		ov, warns := LoadOverlay(dir)
		if len(warns) != 0 || len(ov.Models) != 3 {
			t.Errorf("mode %o: ov=%+v warns=%v, want the file read", mode, ov, warns)
		}
		if err := os.Chmod(filepath.Join(dir, OverlayFileName), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadOverlay_OversizedFileIsIgnored(t *testing.T) {
	dir := privateStateDir(t)
	writeOverlay(t, dir, goodOverlay+"#"+strings.Repeat("x", maxOverlayBytes), 0o600)
	ov, warns := LoadOverlay(dir)
	if len(ov.Models) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "larger than") {
		t.Errorf("ov=%+v warns=%v, want the oversized file refused whole", ov, warns)
	}
	// Exactly at the cap is read.
	body := goodOverlay + "#" + strings.Repeat("x", maxOverlayBytes-len(goodOverlay)-2) + "\n"
	if len(body) != maxOverlayBytes {
		t.Fatalf("test arithmetic: body is %d bytes, want %d", len(body), maxOverlayBytes)
	}
	writeOverlay(t, dir, body, 0o600)
	if ov, warns := LoadOverlay(dir); len(warns) != 0 || len(ov.Models) != 3 {
		t.Errorf("a file at the cap must load: ov=%+v warns=%v", ov, warns)
	}
}

// The overlay is found through statepath.TrustedDir, which reads HOME only. A
// project can set YAKOS_DISPATCH_LOG (K-129); if that moved the overlay, a cloned
// repository could plant one.
func TestDefaultStateDir_IgnoresDispatchLogRelocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	planted := privateStateDir(t)
	writeOverlay(t, planted, goodOverlay, 0o600)
	t.Setenv("YAKOS_DISPATCH_LOG", planted)

	want := filepath.Join(home, ".yakos-state")
	if got := DefaultStateDir(); got != want {
		t.Fatalf("DefaultStateDir() = %q, want %q (not the relocated directory)", got, want)
	}
	r := mustLoad(t, Options{StateDir: DefaultStateDir()})
	if e := entry(t, r, "codex", "gpt-5.6-sol"); !e.Enabled || e.EnabledBy != FromCatalog {
		t.Errorf("the planted overlay disabled gpt-5.6-sol: %+v", e)
	}
	if len(r.Warnings()) != 0 {
		t.Errorf("warnings = %v", r.Warnings())
	}
}

func TestParseOverlay_MalformedInputs(t *testing.T) {
	cases := []struct {
		name, body, warn string
	}{
		{"not YAML", "models: [unclosed\n  - x: {", "cannot parse"},
		{"top level is a list", "- a\n- b\n", "top level must be a mapping"},
		{"top level is a scalar", "hello\n", "top level must be a mapping"},
		{"version 2", "version: 2\nmodels:\n  haiku: {enabled: false}\n", "version must be 1"},
		{"version a string", "version: \"1\"\n", "version must be 1"},
		{"duplicate keys", "models:\n  haiku: {enabled: false}\nmodels:\n  opus: {enabled: false}\n", "cannot parse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ov, warns := ParseOverlay([]byte(c.body))
			if len(ov.Models)+len(ov.Aliases)+len(ov.Admit) != 0 {
				t.Errorf("a refused document must contribute nothing: %+v", ov)
			}
			if len(warns) != 1 || !strings.Contains(warns[0], c.warn) {
				t.Errorf("warnings = %v, want one containing %q", warns, c.warn)
			}
		})
	}
	for _, empty := range []string{"", "\n", "# only a comment\n", "---\n", "null\n"} {
		if ov, warns := ParseOverlay([]byte(empty)); len(ov.Models) != 0 || len(warns) != 0 {
			t.Errorf("%q: %+v %v, want nothing and no warning", empty, ov, warns)
		}
	}
}

// A mistyped entry is dropped with a warning and its siblings survive, so one typo
// does not discard the operator's other settings.
func TestParseOverlay_EntriesAreIndependent(t *testing.T) {
	body := `
mystery: 1
models:
  Bad Id: {enabled: false}
  haiku: {enabled: "no"}
  sonnet: {enabled: false, colour: red}
  opus: oops
  gpt-5.5:
    billing: free
    pricing: {input: "3", output: 15}
  gpt-5.6-luna:
    enabled: false
    pricing: {input: 1, output: 2, extra: 3}
  fable:
    billing: api
    pricing: {input: 5}
aliases:
  fastest: {codex: gpt-5.5}
  cheap:
    claude: haiku
    gemini: x
    codex: "bad id"
    agy: gemini-3.8-flash-low
  best: [oops]
discovery:
  admit: [agy, codex, nope, 7]
  other: 1
`
	ov, warns := ParseOverlay([]byte(body))
	joined := strings.Join(warns, "\n")
	for _, want := range []string{
		`unknown key "mystery" ignored`,
		`"Bad Id" is not a valid model id`,
		"models.haiku.enabled: want true or false",
		`models.sonnet: unknown key "colour" ignored`,
		"models.opus: want a mapping",
		"models.gpt-5.5.billing: want subscription, api or local",
		"models.gpt-5.5.pricing: want input and output",
		"models.gpt-5.6-luna.pricing: want input and output",
		"models.fable.pricing: want input and output",
		`aliases: "fastest" is not a tier alias`,
		"aliases.cheap.claude: the claude column is fixed",
		`aliases.cheap: "gemini" is not a harness with an alias column`,
		"aliases.cheap.codex: want a model id",
		"aliases.best: want a mapping of harness to model id",
		"discovery.admit: codex has no model listing wired in",
		"discovery.admit: skipping an entry that is not a harness",
		`discovery: unknown key "other" ignored`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in:\n%s", want, joined)
		}
	}
	// What survived: valid siblings of the broken entries.
	if p := ov.Models["sonnet"]; p.Enabled == nil || *p.Enabled {
		t.Errorf("sonnet's valid enabled:false must survive its typo'd sibling: %+v", p)
	}
	if p := ov.Models["gpt-5.6-luna"]; p.Enabled == nil || *p.Enabled || p.Pricing != nil {
		t.Errorf("luna: enabled false kept, bad pricing dropped: %+v", p)
	}
	if _, ok := ov.Models["Bad Id"]; ok {
		t.Error("an invalid id was kept")
	}
	if ov.Aliases["cheap"]["agy"] != "gemini-3.8-flash-low" || len(ov.Aliases["cheap"]) != 1 {
		t.Errorf("cheap: only the valid agy mapping survives: %v", ov.Aliases["cheap"])
	}
	if len(ov.Admit) != 1 || ov.Admit[0] != "agy" {
		t.Errorf("admit = %v", ov.Admit)
	}
	// Order of warnings is stable run to run.
	_, again := ParseOverlay([]byte(body))
	if strings.Join(again, "\n") != joined {
		t.Error("warning order changed between two parses of the same bytes")
	}
}

func TestParseOverlay_PricingIsValidated(t *testing.T) {
	cases := []struct {
		pricing string
		ok      bool
	}{
		{"{input: 3, output: 15}", true},
		{"{input: 3.5, output: 15, cache_read: 0.3, cache_write: 3.75}", true},
		{"{input: 0, output: 15}", true},
		{"{input: 0, output: 0}", false},
		{"{input: -1, output: 15}", false},
		{"{input: 3}", false},
		{"{output: 3}", false},
		{"{input: 3, output: 15, cache_read: -1}", false},
		{"{input: 3, output: 1e9}", false},
		{"{input: .nan, output: 15}", false},
		{"{input: .inf, output: 15}", false},
		{"{input: true, output: 15}", false},
		{"{input: 3, output: 15, speed: 2}", false},
		{"[3, 15]", false},
		{"3", false},
	}
	for _, c := range cases {
		ov, warns := ParseOverlay([]byte("models:\n  haiku:\n    pricing: " + c.pricing + "\n"))
		got := ov.Models["haiku"].Pricing != nil
		if got != c.ok {
			t.Errorf("pricing %s: accepted=%v, want %v (warnings %v)", c.pricing, got, c.ok, warns)
		}
		if !c.ok && len(warns) != 1 {
			t.Errorf("pricing %s: want one warning, got %v", c.pricing, warns)
		}
	}
}

func TestParseOverlay_BoundsTheNumberOfEntries(t *testing.T) {
	var b strings.Builder
	b.WriteString("models:\n")
	for i := 0; i < maxOverlayEntries+50; i++ {
		b.WriteString("  m" + strings.Repeat("a", 3) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26)) + ": {enabled: false}\n")
	}
	ov, warns := ParseOverlay([]byte(b.String()))
	if len(ov.Models) != maxOverlayEntries {
		t.Errorf("read %d model entries, want the cap %d", len(ov.Models), maxOverlayEntries)
	}
	if !hasWarning(warns, "only the first") {
		t.Errorf("no cap warning: %v", warns)
	}
}
