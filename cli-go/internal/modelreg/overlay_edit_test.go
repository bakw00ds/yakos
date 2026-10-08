package modelreg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

func overlayDir(t *testing.T, content string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(OverlayPath(dir), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestOverlayWritersRoundTripThroughTheReader(t *testing.T) {
	dir := overlayDir(t, "")
	if _, err := SetEnabled(dir, "gpt-5.5", false); err != nil {
		t.Fatal(err)
	}
	if _, err := SetBilling(dir, "claude-opus-5-5-high", BillingAPI); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPricing(dir, "claude-opus-5-5-high", &Pricing{Input: 5, Output: 25, CacheRead: 0.5}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetAlias(dir, "balanced", "codex", "gpt-5.6-terra"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetAlias(dir, "cheap", "agy", ""); err != nil {
		t.Fatal(err)
	}
	ov, warns := LoadOverlay(dir)
	if len(warns) != 0 {
		t.Fatalf("the writer produced an overlay the reader warns about: %v", warns)
	}
	if p := ov.Models["gpt-5.5"]; p.Enabled == nil || *p.Enabled {
		t.Errorf("enabled: %+v", p)
	}
	if p := ov.Models["claude-opus-5-5-high"]; p.Billing != BillingAPI || p.Pricing == nil || *p.Pricing != (Pricing{Input: 5, Output: 25, CacheRead: 0.5}) {
		t.Errorf("billing/pricing: %+v", p)
	}
	if ov.Aliases["balanced"]["codex"] != "gpt-5.6-terra" {
		t.Errorf("aliases: %+v", ov.Aliases)
	}
	if v, ok := ov.Aliases["cheap"]["agy"]; !ok || v != "" {
		t.Errorf("an empty alias target (the harness default) must be kept: %+v", ov.Aliases)
	}
}

func TestSetPricingNilClearsAndPrunes(t *testing.T) {
	dir := overlayDir(t, "")
	if _, err := SetPricing(dir, "gpt-5.5", &Pricing{Input: 1, Output: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetPricing(dir, "gpt-5.5", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(OverlayPath(dir))
	if strings.Contains(string(b), "gpt-5.5") || strings.Contains(string(b), "pricing") {
		t.Errorf("cleared price left residue:\n%s", b)
	}
}

func TestOverlayWritersKeepWhatTheyDoNotTouch(t *testing.T) {
	dir := overlayDir(t, "# my overlay\nversion: 1\nmodels:\n  other-model: {enabled: false}\naliases:\n  best:\n    codex: gpt-5.5\ndiscovery:\n  admit: [agy]\nfuture_key: 7\n")
	if _, err := SetEnabled(dir, "gpt-5.5", true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(OverlayPath(dir))
	for _, want := range []string{"# my overlay", "version: 1", "other-model:", "best:", "admit:", "future_key: 7", "gpt-5.5:"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("lost %q:\n%s", want, b)
		}
	}
}

func TestOverlayWritersRejectBadInput(t *testing.T) {
	dir := overlayDir(t, "")
	cases := map[string]func() error{
		"bad id":          func() error { _, e := SetEnabled(dir, "Bad ID", true); return e },
		"leading dash":    func() error { _, e := SetEnabled(dir, "-x", true); return e },
		"bad billing":     func() error { _, e := SetBilling(dir, "gpt-5.5", "free"); return e },
		"nan price":       func() error { _, e := SetPricing(dir, "gpt-5.5", &Pricing{Input: nan(), Output: 1}); return e },
		"negative price":  func() error { _, e := SetPricing(dir, "gpt-5.5", &Pricing{Input: -1, Output: 1}); return e },
		"huge price":      func() error { _, e := SetPricing(dir, "gpt-5.5", &Pricing{Input: 1e12, Output: 1}); return e },
		"zero price":      func() error { _, e := SetPricing(dir, "gpt-5.5", &Pricing{}); return e },
		"claude column":   func() error { _, e := SetAlias(dir, "best", "claude", "x"); return e },
		"not an alias":    func() error { _, e := SetAlias(dir, "fastest", "codex", "x"); return e },
		"bad alias id":    func() error { _, e := SetAlias(dir, "best", "codex", "../etc"); return e },
		"relative state":  func() error { _, e := SetEnabled("rel/dir", "gpt-5.5", true); return e },
		"empty state dir": func() error { _, e := SetEnabled("", "gpt-5.5", true); return e },
	}
	for name, f := range cases {
		if err := f(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := os.Stat(OverlayPath(dir)); err == nil {
		t.Error("a refused write created the overlay")
	}
}

func nan() float64 { var z float64; return z / z }

func TestOverlayWriteRefusesAnUntrustedOverlay(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes and symlinks")
	}
	dir := overlayDir(t, "models: {gpt-5.5: {enabled: false}}\n")
	if err := os.Chmod(OverlayPath(dir), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEnabled(dir, "gpt-5.5", true); err == nil {
		t.Fatal("edited a world-writable overlay")
	}
	dir2 := overlayDir(t, "")
	target := filepath.Join(t.TempDir(), "elsewhere.yml")
	if err := os.WriteFile(target, []byte("models: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, OverlayPath(dir2)); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEnabled(dir2, "gpt-5.5", true); err == nil {
		t.Fatal("wrote through a symlinked overlay")
	}
	if b, _ := os.ReadFile(target); string(b) != "models: {}\n" {
		t.Errorf("symlink target changed: %q", b)
	}
}

func TestOverlayWriteRefusesAShapeItCannotEdit(t *testing.T) {
	dir := overlayDir(t, "models: [a, b]\n")
	if _, err := SetEnabled(dir, "gpt-5.5", true); err == nil {
		t.Fatal("edited a models: list")
	}
	dir = overlayDir(t, "models:\n  gpt-5.5: just-text\n")
	if _, err := SetEnabled(dir, "gpt-5.5", true); err == nil {
		t.Fatal("edited a non-mapping model entry")
	}
}

func TestOverlayWriteIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := overlayDir(t, "")
	if _, err := SetEnabled(dir, "gpt-5.5", true); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(OverlayPath(dir)); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", fi.Mode().Perm())
	}
}

// The writer's last gate: an edit that leaves the overlay with an entry the reader
// would ignore is refused, whatever the edit function did.
func TestEditOverlayRefusesAnEditTheReaderWouldIgnore(t *testing.T) {
	dir := overlayDir(t, "models: {gpt-5.5: {enabled: false}}\n")
	before, _ := os.ReadFile(OverlayPath(dir))
	_, err := editOverlay(dir, func(top *yaml.Node) error {
		statepath.YAMLSet(top, "bogus_key", scalar("!!str", "x"))
		return nil
	})
	if err == nil {
		t.Fatal("accepted an overlay with an unknown key")
	}
	if after, _ := os.ReadFile(OverlayPath(dir)); string(after) != string(before) {
		t.Errorf("overlay changed:\n%s", after)
	}
	// A warning the file already had is not the writer's doing.
	dir = overlayDir(t, "bogus_key: 1\n")
	if _, err := SetEnabled(dir, "gpt-5.5", true); err != nil {
		t.Errorf("refused to edit an overlay that already carried a warning: %v", err)
	}
}
