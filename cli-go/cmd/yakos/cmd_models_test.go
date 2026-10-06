package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/modelreg"
	rt "github.com/bakw00ds/yakos/internal/runtime"
)

// ---- harness for the unit tests ---------------------------------------------------

// agyListing is what a fake `agy models` prints: three catalog ids, one the catalog
// lacks, and a line that is not a model id.
const agyListing = "gemini-3.8-flash-high\tGemini 3.8 Flash (High)\n" +
	"gemini-3.8-flash-low\tGemini 3.8 Flash (Low)\n" +
	"claude-opus-5-5-high\tClaude Opus 5.5 (High)\n" +
	"gemini-9-flash-low\tGemini 9 Flash (Low)\n" +
	"Not A Model\tbroken\n"

// modelsRig builds an environment whose discoverer never touches a real agy.
type modelsRig struct {
	t        *testing.T
	stateDir string
	project  string
	ready    bool
	run      modelreg.Runner
	runs     int
}

func newModelsRig(t *testing.T) *modelsRig {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &modelsRig{t: t, stateDir: dir, project: t.TempDir(), ready: true}
	r.run = func(ctx context.Context, spec modelreg.RunSpec) (modelreg.RunResult, error) {
		r.runs++
		return modelreg.RunResult{Stdout: []byte(agyListing)}, nil
	}
	return r
}

func (r *modelsRig) env() modelsEnv {
	return modelsEnv{
		stateDir: r.stateDir,
		cwd:      func() (string, error) { return r.project, nil },
		discoverer: func(sd string, timeout time.Duration) *modelreg.Discoverer {
			return modelreg.NewDiscoverer(modelreg.DiscovererConfig{
				StateDir: sd,
				Probe: func(context.Context, string) (bool, string) {
					if r.ready {
						return true, ""
					}
					return false, "not signed in; run: yakos auth login agy"
				},
				LookPath: func(string) (string, error) { return filepath.Join(r.t.TempDir(), "agy"), nil },
				Run:      func(ctx context.Context, spec modelreg.RunSpec) (modelreg.RunResult, error) { return r.run(ctx, spec) },
				Timeout:  timeout,
			})
		},
	}
}

func (r *modelsRig) do(args ...string) (code int, stdout, stderr string) {
	r.t.Helper()
	var out, errb bytes.Buffer
	code = modelsMain(args, &out, &errb, r.env())
	return code, out.String(), errb.String()
}

func (r *modelsRig) writeOverlay(body string, mode os.FileMode) {
	r.t.Helper()
	p := filepath.Join(r.stateDir, modelreg.OverlayFileName)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		r.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		r.t.Fatal(err)
	}
}

func (r *modelsRig) writeProject(body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.project, ".yakos.yml"), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// ---- list -------------------------------------------------------------------------

// listRows indexes the rows of a `models list` table by id (fields, so the column
// padding does not matter).
func listRows(out string) map[string][]string {
	rows := map[string][]string{}
	for _, l := range strings.Split(out, "\n")[1:] {
		if f := strings.Fields(l); len(f) > 3 {
			rows[f[0]] = f
		}
	}
	return rows
}

func TestModelsList_Table(t *testing.T) {
	r := newModelsRig(t)
	code, out, errs := r.do("list")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 30 {
		t.Fatalf("%d lines, want a header and 29 models:\n%s", len(lines), out)
	}
	if got := strings.Fields(lines[0]); strings.Join(got, " ") != "ID HARNESS BILLING AVAILABLE ALIASES" {
		t.Errorf("header = %q", lines[0])
	}
	want := map[string]string{
		"haiku":                   "haiku claude subscription unknown cheap",
		"opus":                    "opus claude subscription unknown best,reasoning",
		"gpt-5.6-terra":           "gpt-5.6-terra codex subscription unknown -",
		"gpt-reserve":             "gpt-reserve codex subscription disabled -",
		"gemini-3.8-flash-high":   "gemini-3.8-flash-high agy subscription unknown balanced",
		"claude-opus-5-5-high":    "claude-opus-5-5-high agy subscription unknown frontier",
		"gemini-3.7-flash-medium": "gemini-3.7-flash-medium agy subscription unknown -",
	}
	got := map[string]string{}
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		got[f[0]] = strings.Join(f, " ")
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("row %s = %q, want %q", id, got[id], w)
		}
	}
	// The hint about agy availability goes to stderr, never into the table.
	if !strings.Contains(errs, "run `yakos models probe`") || strings.Contains(out, "probe") {
		t.Errorf("stderr = %q, stdout mentions probe: %v", errs, strings.Contains(out, "probe"))
	}
	if r.runs != 0 {
		t.Errorf("list ran a harness command %d time(s); it must read the cache only", r.runs)
	}
}

// golden: the table is what operators and scripts read, so its bytes are pinned.
func TestModelsList_Golden(t *testing.T) {
	r := newModelsRig(t)
	_, out, _ := r.do("list")
	checkGolden(t, "models-list.golden", out)
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("YAKOS_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// .gitattributes pins the file to LF; the replace keeps a checkout that
	// ignores it from failing a comparison that is about the generated bytes.
	if want := strings.ReplaceAll(string(raw), "\r\n", "\n"); got != want {
		t.Errorf("%s differs from the output (regenerate with YAKOS_UPDATE_GOLDEN=1 and review the diff).\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

func TestModelsList_HarnessFilterAndJSON(t *testing.T) {
	r := newModelsRig(t)
	code, out, _ := r.do("list", "--harness", "codex", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var doc struct {
		Models []struct {
			ID        string   `json:"id"`
			Harness   string   `json:"harness"`
			Billing   string   `json:"billing"`
			Enabled   bool     `json:"enabled"`
			EnabledBy string   `json:"enabled_by"`
			Aliases   []string `json:"aliases"`
			Avail     struct {
				State string `json:"state"`
			} `json:"availability"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(doc.Models) != 7 {
		t.Fatalf("%d codex models, want 7", len(doc.Models))
	}
	for _, m := range doc.Models {
		if m.Harness != "codex" || m.Billing != "subscription" || m.Avail.State != "unknown" || m.Aliases == nil || len(m.Aliases) != 0 {
			t.Errorf("%+v", m)
		}
	}
	if strings.Contains(out, `"at"`) {
		t.Error("an unknown availability must not print a zero timestamp")
	}
	// Same bytes every time.
	_, again, _ := r.do("list", "--harness", "codex", "--json")
	if again != out {
		t.Error("two runs printed different JSON")
	}
}

func TestModelsList_ProjectDisablesAndOverlayEnables(t *testing.T) {
	r := newModelsRig(t)
	r.writeProject("models:\n  disable: [opus, gpt-5.5]\n  enable: [gpt-reserve]\n")
	r.writeOverlay("models:\n  gpt-reserve: {enabled: true}\n  gemini-3.7-flash-low: {enabled: false}\n", 0o600)
	code, out, errs := r.do("list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	rows := map[string][]string{}
	for _, l := range strings.Split(out, "\n")[1:] {
		if f := strings.Fields(l); len(f) > 3 {
			rows[f[0]] = f
		}
	}
	for id, want := range map[string]string{"opus": "disabled", "gpt-5.5": "disabled", "gpt-reserve": "unknown", "gemini-3.7-flash-low": "disabled"} {
		if rows[id][3] != want {
			t.Errorf("%s AVAILABLE = %q, want %q", id, rows[id][3], want)
		}
	}
	if !strings.Contains(errs, `"enable" ignored: a project can only disable models`) {
		t.Errorf("stderr = %q", errs)
	}
}

func TestModelsList_UntrustedOverlayIsReportedAndIgnored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	r := newModelsRig(t)
	r.writeOverlay("models:\n  haiku: {enabled: false}\n", 0o666)
	code, out, errs := r.do("list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errs, "model-registry.yml (yakOS state directory) ignored: the file is group or world writable") {
		t.Errorf("stderr = %q", errs)
	}
	if got := listRows(out)["haiku"]; len(got) < 4 || got[3] != "unknown" {
		t.Errorf("the untrusted overlay took effect (haiku row %q):\n%s", got, out)
	}
	if strings.Contains(errs, r.stateDir) {
		t.Errorf("the warning names the path: %q", errs)
	}
}

func TestModelsList_ShowsDiscoveryFromTheCache(t *testing.T) {
	r := newModelsRig(t)
	if code, _, errs := r.do("probe", "--harness", "agy"); code != 0 {
		t.Fatalf("probe exit %d: %s", code, errs)
	}
	r.runs = 0
	_, out, errs := r.do("list", "--harness", "agy")
	rows := map[string][]string{}
	for _, l := range strings.Split(out, "\n")[1:] {
		if f := strings.Fields(l); len(f) > 3 {
			rows[f[0]] = f
		}
	}
	if rows["gemini-3.8-flash-high"][3] != "yes" || rows["gemini-3.1-pro-high"][3] != "no" {
		t.Errorf("availability after a probe: listed=%q unlisted=%q", rows["gemini-3.8-flash-high"], rows["gemini-3.1-pro-high"])
	}
	if strings.Contains(errs, "probe") {
		t.Errorf("no hint is needed once a listing exists: %q", errs)
	}
	if r.runs != 0 {
		t.Errorf("list ran agy %d time(s)", r.runs)
	}
}

// ---- show -------------------------------------------------------------------------

func TestModelsShow_Text(t *testing.T) {
	r := newModelsRig(t)
	r.writeOverlay("aliases:\n  balanced: {codex: gpt-5.6-terra}\n", 0o600)
	code, out, errs := r.do("show", "gpt-5.6-terra")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"gpt-5.6-terra (codex)",
		"  name:         GPT-5.6-Terra",
		"  provider:     openai",
		"  description:  Older balanced model for straightforward work.",
		"  billing:      subscription (catalog)",
		"  price:        none (billing is subscription: tokens are the unit)",
		"  enabled:      yes (catalog default)",
		"  available:    unknown (no model listing is wired in for codex)",
		"  aliases:      balanced (model-registry.yml)",
		"  effort:       low, medium, high, xhigh, max, ultra (default medium)",
		"  limits:       context 272000 tokens, extended 872000",
		"  input:        text, image",
		"  visibility:   list (the harness's own picker)",
		"  source:       catalog",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing line %q in:\n%s", want, out)
		}
	}
}

func TestModelsShow_AgyEffortInTheIDAndAPIPrice(t *testing.T) {
	r := newModelsRig(t)
	r.writeOverlay("models:\n  claude-opus-5-5-high:\n    billing: api\n    pricing: {input: 5, output: 25, cache_read: 0.5}\n  gemini-3.8-flash-low: {billing: api}\n", 0o600)
	_, out, _ := r.do("show", "claude-opus-5-5-high")
	for _, want := range []string{
		"  effort:       high (carried by the id; do not pass --effort with it)",
		"  billing:      api (overlay)",
		"  price:        $5 in / $25 out per million tokens (cache read $0.5, write $0) (overlay)",
		"  available:    unknown (no listing taken yet: run `yakos models probe`)",
		"  aliases:      frontier",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing line %q in:\n%s", want, out)
		}
	}
	_, out, _ = r.do("show", "gemini-3.8-flash-low")
	if !strings.Contains(out, "  price:        none recorded (billed per call: set one in model-registry.yml; tokens are counted meanwhile)\n") {
		t.Errorf("an api model with no price says so:\n%s", out)
	}
}

func TestModelsShow_CodexAliasHint(t *testing.T) {
	r := newModelsRig(t)
	_, out, _ := r.do("show", "gpt-6-astra")
	if !strings.Contains(out, "  aliases:      none (tier aliases are empty for codex: the catalog gives no tiers; map them in model-registry.yml)\n") {
		t.Errorf("out:\n%s", out)
	}
}

func TestModelsShow_JSONAndFilters(t *testing.T) {
	r := newModelsRig(t)
	code, out, _ := r.do("show", "haiku", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Models) != 1 || doc.Models[0]["id"] != "haiku" || doc.Models[0]["harness"] != "claude" {
		t.Errorf("%v %s", err, out)
	}
	if code, _, errs := r.do("show", "haiku", "--harness", "codex"); code != 1 || !strings.Contains(errs, `no model "haiku"`) {
		t.Errorf("a harness filter that excludes the model: %d %q", code, errs)
	}
	if code, _, errs := r.do("show", "no-such-model"); code != 1 || !strings.Contains(errs, `no model "no-such-model" in the registry`) {
		t.Errorf("unknown id: %d %q", code, errs)
	}
	checkGolden(t, "models-show-terra.golden", func() string {
		_, o, _ := r.do("show", "gpt-5.6-terra", "--json")
		return o
	}())
}

// ---- probe ------------------------------------------------------------------------

func TestModelsProbe_UpdatesAndReportsWhatChanged(t *testing.T) {
	r := newModelsRig(t)
	code, out, errs := r.do("probe")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"claude: no model listing is wired in for claude\n",
		"codex: no model listing is wired in for codex\n",
		"agy: updated, 4 models listed by `agy models`\n",
		"  first listing: nothing to compare with\n",
		"  not in the catalog: gemini-9-flash-low\n",
		"    they stay unregistered unless model-registry.yml says `discovery: {admit: [agy]}`\n",
		"  1 line(s) of the listing were not model ids and were dropped\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "catalog entries this account does not list: gemini-3.8-flash-medium,") {
		t.Errorf("catalog entries the listing lacks are named:\n%s", out)
	}
	// The listing changes: the next probe says what moved.
	r.run = func(ctx context.Context, spec modelreg.RunSpec) (modelreg.RunResult, error) {
		return modelreg.RunResult{Stdout: []byte("gemini-3.8-flash-high\tG\ngemini-9-flash-low\tN\ngemini-10-flash-low\tN2\n")}, nil
	}
	_, out, _ = r.do("probe", "--harness", "agy")
	for _, want := range []string{
		"agy: updated, 3 models listed by `agy models`\n",
		"  new:     gemini-10-flash-low\n",
		"  gone:    claude-opus-5-5-high, gemini-3.8-flash-low\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("second probe: missing %q in:\n%s", want, out)
		}
	}
	// And a third, with nothing new.
	_, out, _ = r.do("probe", "--harness", "agy")
	if !strings.Contains(out, "  no change since the last listing\n") {
		t.Errorf("third probe:\n%s", out)
	}
}

func TestModelsProbe_SkippedIsNotAFailure(t *testing.T) {
	r := newModelsRig(t)
	r.ready = false
	code, out, errs := r.do("probe", "--harness", "agy")
	if code != 0 || errs != "" {
		t.Errorf("exit %d, stderr %q: a signed-out agy is an answer, not an error", code, errs)
	}
	if !strings.Contains(out, "agy: skipped, not signed in; run: yakos auth login agy\n") {
		t.Errorf("out:\n%s", out)
	}
	if r.runs != 0 {
		t.Errorf("agy ran %d time(s) though it is not signed in", r.runs)
	}
}

func TestModelsProbe_FailureExitsOne(t *testing.T) {
	r := newModelsRig(t)
	r.run = func(ctx context.Context, spec modelreg.RunSpec) (modelreg.RunResult, error) {
		return modelreg.RunResult{}, context.DeadlineExceeded
	}
	code, out, errs := r.do("probe", "--harness", "agy", "--timeout", "50ms")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	if !strings.Contains(out, "agy: failed, agy models did not finish within 50ms\n") || !strings.Contains(errs, "yakos models probe: agy failed: ") {
		t.Errorf("out:\n%s\nstderr: %s", out, errs)
	}
	// A failed probe leaves what the cache had.
	r.run = func(ctx context.Context, spec modelreg.RunSpec) (modelreg.RunResult, error) {
		return modelreg.RunResult{Stdout: []byte(agyListing)}, nil
	}
	if code, _, _ := r.do("probe", "--harness", "agy"); code != 0 {
		t.Fatal("setup probe failed")
	}
	r.run = func(ctx context.Context, spec modelreg.RunSpec) (modelreg.RunResult, error) {
		return modelreg.RunResult{Stdout: []byte("garbage that is not a listing\n")}, nil
	}
	if code, _, _ := r.do("probe", "--harness", "agy"); code != 1 {
		t.Error("a listing with no model ids must fail the probe")
	}
	_, list, _ := r.do("list", "--harness", "agy")
	if got := listRows(list)["gemini-3.8-flash-high"]; len(got) < 4 || got[3] != "yes" {
		t.Errorf("the failed probe changed the cached availability:\n%s", list)
	}
}

func TestModelsProbe_JSON(t *testing.T) {
	r := newModelsRig(t)
	code, out, _ := r.do("probe", "--json")
	if code != 0 {
		t.Fatal(code)
	}
	var doc struct {
		Probes []struct {
			Harness string `json:"harness"`
			Status  string `json:"status"`
		} `json:"probes"`
		NotInCatalog map[string][]string `json:"not_in_catalog"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(doc.Probes) != 3 || doc.Probes[0].Harness != "claude" || doc.Probes[0].Status != "unsupported" || doc.Probes[2].Harness != "agy" || doc.Probes[2].Status != "updated" {
		t.Errorf("probes = %+v", doc.Probes)
	}
	if got := doc.NotInCatalog["agy"]; len(got) != 1 || got[0] != "gemini-9-flash-low" {
		t.Errorf("not_in_catalog = %v", doc.NotInCatalog)
	}
}

// Admitting discovered ids is the overlay's decision: with it, the next list shows
// the new model as a registered entry.
func TestModelsProbe_AdmitThroughTheOverlay(t *testing.T) {
	r := newModelsRig(t)
	if code, _, _ := r.do("probe", "--harness", "agy"); code != 0 {
		t.Fatal("probe")
	}
	_, out, _ := r.do("list", "--harness", "agy")
	if strings.Contains(out, "gemini-9-flash-low") {
		t.Fatalf("a discovered id was registered without the overlay:\n%s", out)
	}
	r.writeOverlay("discovery:\n  admit: [agy]\n", 0o600)
	_, out, _ = r.do("list", "--harness", "agy")
	if got := listRows(out)["gemini-9-flash-low"]; len(got) < 4 || got[1] != "agy" || got[3] != "yes" {
		t.Errorf("an admitted id is registered and available:\n%s", out)
	}
}

// ---- argument handling -------------------------------------------------------------

func TestModelsUsageErrors(t *testing.T) {
	r := newModelsRig(t)
	cases := []struct {
		args []string
		code int
		err  string
	}{
		{nil, 1, ""},
		{[]string{"nope"}, 1, `unknown subcommand "nope"`},
		{[]string{"list", "extra"}, 1, "unexpected argument"},
		{[]string{"list", "--harness", "gemini"}, 1, `unknown harness "gemini"`},
		{[]string{"list", "--harness"}, 1, "requires a harness name"},
		{[]string{"list", "--bogus"}, 1, `unknown flag "--bogus"`},
		{[]string{"list", "--timeout", "5s"}, 1, `unknown flag "--timeout"`},
		{[]string{"show"}, 1, "usage: yakos models show <id>"},
		{[]string{"show", "a", "b"}, 1, "usage: yakos models show <id>"},
		{[]string{"probe", "x"}, 1, "unexpected argument"},
		{[]string{"probe", "--timeout", "never"}, 1, `--timeout "never"`},
		{[]string{"probe", "--timeout", "0s"}, 1, `--timeout "0s"`},
		{[]string{"probe", "--timeout", "1h"}, 1, `--timeout "1h"`},
		{[]string{"probe", "--project", "x"}, 1, `unknown flag "--project"`},
	}
	for _, c := range cases {
		code, _, errs := r.do(c.args...)
		if code != c.code || !strings.Contains(errs, c.err) {
			t.Errorf("%v: exit %d stderr %q, want exit %d containing %q", c.args, code, errs, c.code, c.err)
		}
	}
	for _, h := range []string{"--help", "-h", "help"} {
		code, out, _ := r.do(h)
		if code != 0 || !strings.Contains(out, "yakos models <list|show|probe>") {
			t.Errorf("%s: exit %d", h, code)
		}
	}
	if code, out, _ := r.do("list", "--help"); code != 0 || !strings.Contains(out, "yakos models <list|show|probe>") {
		t.Errorf("list --help: exit %d", code)
	}
	if code, out, _ := r.do(); code != 1 || !strings.Contains(out, "yakos models <list|show|probe>") {
		t.Errorf("no arguments prints the help and exits 1: %d", code)
	}
}

func TestModelsSignInProbeWording(t *testing.T) {
	// With no agy on PATH the P0a probe says the CLI is missing; the wording is the
	// dispatcher's ("CLI not found on PATH; <install hint>").
	t.Setenv("PATH", t.TempDir())
	ok, reason := modelsSignInProbe(context.Background(), "agy")
	if ok || !strings.HasPrefix(reason, "CLI not found on PATH") {
		t.Errorf("(%v, %q)", ok, reason)
	}
}

func TestParseProbeTimeout(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 15 * time.Second, "20s": 20 * time.Second, "1500ms": 1500 * time.Millisecond, "2m": 2 * time.Minute} {
		if got, err := parseProbeTimeout(in); err != nil || got != want {
			t.Errorf("%q: %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"x", "0", "-5s", "121s", "3m"} {
		if _, err := parseProbeTimeout(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

// ---- through the real router ------------------------------------------------------

// `yakos models` has no bash twin, so the router must send it to Go even when a
// bash tree sits next to the binary (shadow mode) and even when YAKOS_IMPL=bash
// asks for bash. A missing isModelsForceGo hands it to bash, which answers
// "unknown command". These run main() in a subprocess of the test binary.
func TestModelsThroughTheRouterIgnoresYakosImpl(t *testing.T) {
	for _, impl := range []string{"", "go", "bash"} {
		var env []string
		if impl != "" {
			env = []string{"YAKOS_IMPL=" + impl}
		}
		code, out := runYakos(t, t.TempDir(), env, "models", "--help")
		if code != 0 || !strings.Contains(out, "yakos models <list|show|probe>") {
			t.Errorf("YAKOS_IMPL=%q: exit %d\n%s", impl, code, out)
		}
	}
	code, out := runYakos(t, t.TempDir(), nil, "models", "list", "--harness", "claude")
	if code != 0 || !strings.Contains(out, "haiku") || !strings.Contains(out, "frontier") {
		t.Errorf("models list through the router: exit %d\n%s", code, out)
	}
}

func TestIsModelsForceGo(t *testing.T) {
	if !isModelsForceGo([]string{"models"}) || !isModelsForceGo([]string{"models", "list"}) {
		t.Error("models must be force-go")
	}
	for _, a := range [][]string{nil, {"model-routing"}, {"budget"}, {"list", "models"}} {
		if isModelsForceGo(a) {
			t.Errorf("%v must not be", a)
		}
	}
}

// K-129: a project can set YAKOS_DISPATCH_LOG for the processes it spawns, so the
// overlay and the discovery cache are never read from or written to the directory
// it names. The router test above gives the subprocess HOME=<temp>, so this plants
// an overlay in the relocated directory and checks the registry does not see it.
func TestModelsThroughTheRouterIgnoresARelocatedStateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	state := t.TempDir()
	if err := os.Chmod(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, modelreg.OverlayFileName), []byte("models:\n  haiku: {enabled: false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := runYakos(t, state, nil, "models", "list", "--harness", "claude")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if strings.Contains(out, "disabled") {
		t.Errorf("an overlay planted through YAKOS_DISPATCH_LOG took effect:\n%s", out)
	}
}

// Through the real router and the real P0a probe, with nothing on PATH: agy is not
// installed, so the probe is skipped with the dispatcher's wording, and the cache
// is not created.
func TestModelsProbeThroughTheRouterWithNoAgy(t *testing.T) {
	code, out := runYakos(t, t.TempDir(), nil, "models", "probe", "--harness", "agy")
	if code != 0 || !strings.Contains(out, "agy: skipped, CLI not found on PATH") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

// models has its own row in the flag registry that the help-versus-parser test
// reads, with every flag the command parses. A missing row leaves that test with
// nothing to compare, and nothing else notices (TestBudgetFlagsRegistered is the
// model).
func TestModelsFlagsRegistered(t *testing.T) {
	var spec []string
	found := false
	for _, e := range commandRegistry {
		if e.Name != "models" {
			continue
		}
		found = true
		if e.HelpFn == nil {
			t.Error("models has no HelpFn in the registry")
		}
		for _, s := range e.Specs.Specs {
			spec = append(spec, s.Name)
		}
	}
	if !found {
		t.Fatal("models is not in commandRegistry")
	}
	for _, want := range []string{"--json", "--harness", "--project", "--timeout"} {
		have := false
		for _, s := range spec {
			have = have || s == want
		}
		if !have {
			t.Errorf("models flag %s not registered (have %v)", want, spec)
		}
	}
}

// `yakos help` is the list operators read to learn a command exists: models must
// be in one of its groups with a one-line description.
func TestModelsIsListedInTheHelp(t *testing.T) {
	in := false
	for _, g := range helpGroups {
		for _, c := range g.Commands {
			in = in || c == "models"
		}
	}
	if !in {
		t.Error("models is in no help group")
	}
	if builtinDescs["models"] == "" {
		t.Error("models has no one-line description in builtinDescs")
	}
}

// ---- the environment the listing runs in -------------------------------------------

// agy models is a read-only listing. It sees how to find its login and the network
// and agy's own credential families, and none of what dispatch forwards for an
// agent's git workflow or yakOS's own settings.
func TestModelsDiscoveryEnv(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "HOME=/home/u", "USER=u", "LANG=C", "LC_ALL=C", "XDG_CONFIG_HOME=/x", "TMPDIR=/t", "TERM=xterm",
		"HTTPS_PROXY=http://p", "NO_PROXY=x", "SSL_CERT_FILE=/c", "Path=C:\\bin", "SystemRoot=C:\\Windows",
		"GEMINI_API_KEY=k1", "ANTIGRAVITY_API_KEY=k2", "GOOGLE_API_KEY=k3", "GCLOUD_PROJECT=p",
		// not for a listing:
		"GH_TOKEN=t", "GITHUB_TOKEN=t", "SSH_AUTH_SOCK=/s", "GIT_SSH_COMMAND=x", "GIT_ASKPASS=x", "NODE_OPTIONS=--x",
		"YAKOS_ROOT=/r", "YAKOS_DISPATCH_LOG=/d", "YAKOS_DISPATCH_ENV_PASSTHROUGH=*", "ANTHROPIC_API_KEY=a", "OPENAI_API_KEY=o",
		"AWS_SECRET_ACCESS_KEY=s", "SHELL=/bin/zsh", "PWD=/proj", "CI=true", "COLORTERM=truecolor", "TERM_PROGRAM=x",
		"=C:=C:\\", "NOEQUALS", "EMPTYVALUE=",
	}
	got := map[string]bool{}
	for _, kv := range modelsDiscoveryEnv(environ) {
		k, _, _ := strings.Cut(kv, "=")
		got[k] = true
	}
	for _, k := range []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "XDG_CONFIG_HOME", "TMPDIR", "TERM", "HTTPS_PROXY", "NO_PROXY",
		"SSL_CERT_FILE", "Path", "SystemRoot", "GEMINI_API_KEY", "ANTIGRAVITY_API_KEY", "GOOGLE_API_KEY", "GCLOUD_PROJECT"} {
		if !got[k] {
			t.Errorf("%s was dropped; agy needs it to find its login or the network", k)
		}
	}
	for _, k := range []string{"GH_TOKEN", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "GIT_SSH_COMMAND", "GIT_ASKPASS", "NODE_OPTIONS", "YAKOS_ROOT",
		"YAKOS_DISPATCH_LOG", "YAKOS_DISPATCH_ENV_PASSTHROUGH", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY",
		"SHELL", "PWD", "CI", "COLORTERM", "TERM_PROGRAM", "NOEQUALS"} {
		if got[k] {
			t.Errorf("%s reached a model listing", k)
		}
	}
	if len(modelsDiscoveryEnv(nil)) != 0 {
		t.Error("an empty environment must stay empty")
	}
}

// The listing never sees more than dispatch lets agy see: a variable dispatch
// withholds from agy is not one discovery may add.
func TestModelsDiscoveryEnvIsASubsetOfWhatDispatchGivesAgy(t *testing.T) {
	var environ []string
	for k := range modelsEnvNames {
		environ = append(environ, k+"=v")
	}
	for _, p := range modelsEnvPrefixes {
		environ = append(environ, p+"X=v")
	}
	environ = append(environ, "ANTHROPIC_API_KEY=a", "GH_TOKEN=t", "YAKOS_ROOT=/r", "UNRELATED=1")
	dispatch := map[string]bool{}
	for _, kv := range rt.FilterEnvFor("agy", environ) {
		dispatch[kv] = true
	}
	for _, kv := range modelsDiscoveryEnv(environ) {
		if !dispatch[kv] {
			t.Errorf("%q reaches a model listing but dispatch withholds it from agy", kv)
		}
	}
}

// End to end through the router with a fake agy on PATH: the child sees agy's own
// credential and the basics, and none of the variables dispatch would have
// forwarded for a git workflow.
func TestModelsProbeThroughTheRouterGivesAgyANarrowEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in for agy")
	}
	bin := t.TempDir()
	dump := filepath.Join(t.TempDir(), "env.txt")
	script := "#!/bin/sh\nenv > '" + dump + "'\nprintf 'gemini-3.8-flash-high\\tGemini 3.8 Flash (High)\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	code, out := runYakos(t, t.TempDir(), []string{
		"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "ANTIGRAVITY_API_KEY=agy-key",
		"GH_TOKEN=ghp_sentinel", "GITHUB_TOKEN=ghs_sentinel", "SSH_AUTH_SOCK=/tmp/sock", "NODE_OPTIONS=--require=x",
		"ANTHROPIC_API_KEY=sk-sentinel", "GIT_ASKPASS=x", "YAKOS_SENTINEL=1",
	}, "models", "probe", "--harness", "agy")
	if code != 0 || !strings.Contains(out, "agy: updated, 1 models listed") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the fake agy did not run: %v", err)
	}
	env := string(raw)
	for _, want := range []string{"ANTIGRAVITY_API_KEY=agy-key", "HOME=" + home} {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("the child lacks %q:\n%s", want, env)
		}
	}
	for _, bad := range []string{"GH_TOKEN", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "NODE_OPTIONS", "ANTHROPIC_API_KEY", "GIT_ASKPASS", "YAKOS_"} {
		if strings.Contains(env, bad) {
			t.Errorf("%s reached the model listing:\n%s", bad, env)
		}
	}
	// And the cache landed in the trusted state directory under HOME, 0600.
	if fi, err := os.Stat(filepath.Join(home, ".yakos-state", modelreg.DiscoveryFileName)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("cache: %v %v", fi, err)
	}
}

// ---- shadow mode ---------------------------------------------------------------------

// With a bash tree next to the binary and YAKOS_IMPL unset, the router is in shadow
// mode and hands every command to bash, except the ones with no bash twin. This
// builds that layout (a stub cli/yakos beside a copy of the test binary), proves a
// control command reaches the stub, and that models does not.
func TestModelsInShadowModeStillReachesGo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub for the bash tree")
	}
	root := t.TempDir()
	for _, d := range []string{"bin", "cli"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "cli", "yakos"), []byte("#!/bin/sh\necho STUB-BASH-REACHED \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyExe(t, os.Args[0], filepath.Join(root, "bin", "yakos"))

	run := func(args ...string) (int, string) {
		t.Helper()
		b, _ := json.Marshal(args)
		cmd := exec.Command(filepath.Join(root, "bin", "yakos"), "-test.run=^TestBudgetHelperMain$")
		cmd.Env = []string{"YAKOS_TEST_MAIN_ARGS=" + string(b), "HOME=" + t.TempDir(), "PATH=/usr/bin:/bin"}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, out.String()
	}

	if _, out := run("cost"); !strings.Contains(out, "STUB-BASH-REACHED") {
		t.Fatalf("setup: shadow mode did not hand a plain command to the bash tree:\n%s", out)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"models", "--help"}, "yakos models <list|show|probe>"},
		{[]string{"models", "list", "--harness", "claude"}, "frontier"},
	} {
		code, out := run(c.args...)
		if strings.Contains(out, "STUB-BASH-REACHED") {
			t.Errorf("%v reached the bash tree in shadow mode:\n%s", c.args, out)
		}
		if code != 0 || !strings.Contains(out, c.want) {
			t.Errorf("%v: exit %d, want output containing %q:\n%s", c.args, code, c.want, out)
		}
	}
}

func copyExe(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
