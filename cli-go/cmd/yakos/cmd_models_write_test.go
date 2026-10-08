package main

// cmd_models_write_test.go: `yakos models enable|disable|alias|pin|pricing` and
// `yakos router policy get|set` (K-153). Each runs against a scratch state
// directory and a scratch ledger; none touches the operator's real state.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// ledgerFor points the audit trail at a scratch directory and returns a reader
// of its config_changed lines.
func ledgerFor(t *testing.T) func() []map[string]any {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)
	return func() []map[string]any {
		b, err := os.ReadFile(filepath.Join(dir, "dispatch-log.ndjson"))
		if err != nil {
			return nil
		}
		var out []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(l), &m) == nil && m["type"] == "config_changed" {
				out = append(out, m)
			}
		}
		return out
	}
}

func overlayOf(t *testing.T, r *modelsRig) modelreg.Overlay {
	t.Helper()
	ov, warns := modelreg.LoadOverlay(r.stateDir)
	if len(warns) != 0 {
		t.Fatalf("overlay warnings: %v", warns)
	}
	return ov
}

func TestModelsDisableEnable(t *testing.T) {
	audit := ledgerFor(t)
	r := newModelsRig(t)
	code, out, errs := r.do("disable", "gpt-5.5")
	if code != 0 || !strings.HasPrefix(out, "ok: disabled gpt-5.5 (model-registry.yml none -> ") {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	if strings.Contains(out+errs, r.stateDir) {
		t.Errorf("output names the state directory: %s%s", out, errs)
	}
	if p := overlayOf(t, r).Models["gpt-5.5"]; p.Enabled == nil || *p.Enabled {
		t.Errorf("overlay = %+v", p)
	}
	_, list, _ := r.do("list")
	if row := listRows(list)["gpt-5.5"]; len(row) < 4 || row[3] != "disabled" {
		t.Errorf("list row = %v", row)
	}
	lines := audit()
	if len(lines) != 1 || lines[0]["action"] != "models.disable" || lines[0]["file"] != "model-registry.yml" || lines[0]["surface"] != "cli" ||
		lines[0]["policy_sha_before"] != "" || len(lines[0]["policy_sha_after"].(string)) != 64 {
		t.Fatalf("audit = %v", lines)
	}
	if lines[0]["operator_id"] == "" {
		t.Error("audit has no operator")
	}
	// The same change again writes nothing and audits nothing.
	if code, out, _ = r.do("disable", "gpt-5.5"); code != 0 || !strings.HasPrefix(out, "unchanged:") {
		t.Errorf("repeat: %d %q", code, out)
	}
	if len(audit()) != 1 {
		t.Error("an unchanged write was audited")
	}
	if code, _, errs = r.do("enable", "gpt-5.5"); code != 0 {
		t.Fatalf("enable: %s", errs)
	}
	if len(audit()) != 2 {
		t.Error("enable not audited")
	}
}

func TestModelsEnableNotesAProjectDisable(t *testing.T) {
	ledgerFor(t)
	r := newModelsRig(t)
	r.writeProject("models:\n  disable: [gpt-5.5]\n")
	code, _, errs := r.do("enable", "gpt-5.5")
	if code != 0 || !strings.Contains(errs, "a project can only switch models off") {
		t.Errorf("exit %d err=%q", code, errs)
	}
}

func TestModelsWriteUsageAndRefusals(t *testing.T) {
	audit := ledgerFor(t)
	r := newModelsRig(t)
	for name, args := range map[string][]string{
		"unknown id":          {"enable", "no-such-model"},
		"missing id":          {"disable"},
		"extra arg":           {"enable", "gpt-5.5", "x"},
		"alias claude column": {"alias", "best", "claude", "opus"},
		"alias unknown model": {"alias", "best", "codex", "no-such-model"},
		"alias not an alias":  {"alias", "fastest", "codex", "gpt-5.5"},
		"alias arity":         {"alias", "best", "codex"},
		"pricing no id":       {"pricing"},
		"pricing no output":   {"pricing", "claude-opus-5-5-high", "--input", "5"},
		"pricing text":        {"pricing", "claude-opus-5-5-high", "--input", "abc", "--output", "1"},
		"pricing negative":    {"pricing", "claude-opus-5-5-high", "--input", "-1", "--output", "1"},
		"pricing nan":         {"pricing", "claude-opus-5-5-high", "--input", "NaN", "--output", "1"},
		"pricing inf":         {"pricing", "claude-opus-5-5-high", "--input", "Inf", "--output", "1"},
		"pricing huge":        {"pricing", "claude-opus-5-5-high", "--input", "1e9", "--output", "1"},
		"pricing billing":     {"pricing", "claude-opus-5-5-high", "--billing", "free"},
		"pin unknown model":   {"pin", "backend", "no-such-model"},
		"pin bad agent":       {"pin", "../etc", "gpt-5.5"},
		"pin disabled model":  {"pin", "backend", "gpt-reserve"},
		"pin arity":           {"pin", "backend"},
		"pin clear + model":   {"pin", "backend", "gpt-5.5", "--clear"},
		"unknown flag":        {"enable", "gpt-5.5", "--nope"},
	} {
		if code, out, errs := r.do(args...); code != 1 || out != "" || errs == "" {
			t.Errorf("%s: exit %d out=%q err=%q; want exit 1 with a message", name, code, out, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(r.stateDir, modelreg.OverlayFileName)); err == nil {
		t.Error("a refused command created the overlay")
	}
	if _, err := os.Stat(routerpolicy.Path(r.stateDir)); err == nil {
		t.Error("a refused command created the router policy")
	}
	if n := len(audit()); n != 0 {
		t.Errorf("%d audit lines for refused commands", n)
	}
}

func TestModelsAliasWritesAndResolves(t *testing.T) {
	audit := ledgerFor(t)
	r := newModelsRig(t)
	if code, out, errs := r.do("alias", "balanced", "codex", "gpt-5.6-terra"); code != 0 || !strings.Contains(out, "balanced on codex is now gpt-5.6-terra") {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	_, list, _ := r.do("list")
	if row := listRows(list)["gpt-5.6-terra"]; len(row) < 5 || !strings.Contains(row[len(row)-1], "balanced") {
		t.Errorf("list row = %v", row)
	}
	if code, out, _ := r.do("alias", "balanced", "codex", "default"); code != 0 || !strings.Contains(out, "the harness default") {
		t.Errorf("default: %d %q", code, out)
	}
	if v, ok := overlayOf(t, r).Aliases["balanced"]["codex"]; !ok || v != "" {
		t.Errorf("default not stored as an empty target: %+v", overlayOf(t, r).Aliases)
	}
	if n := len(audit()); n != 2 {
		t.Errorf("%d audit lines, want 2", n)
	}
}

func TestModelsPricing(t *testing.T) {
	audit := ledgerFor(t)
	r := newModelsRig(t)
	code, out, errs := r.do("pricing", "claude-opus-5-5-high", "--input", "5", "--output", "25", "--cache-read", "0.5", "--billing", "api")
	if code != 0 || strings.Count(out, "ok:") != 2 {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	if n := len(audit()); n != 2 {
		t.Errorf("billing and price are two writes, %d audit lines", n)
	}
	_, show, _ := r.do("show", "claude-opus-5-5-high", "--harness", "agy")
	if !strings.Contains(show, "api") || !strings.Contains(show, "25") {
		t.Errorf("show:\n%s", show)
	}
	// A price on a model that is not billed per call is refused: the registry would
	// ignore it and warn on every load.
	r2 := newModelsRig(t)
	code, _, errs = r2.do("pricing", "gpt-5.5", "--input", "1", "--output", "2")
	if code != 1 || !strings.Contains(errs, "a price counts only for api billing") {
		t.Errorf("a price on a subscription model: %d %q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(r2.stateDir, modelreg.OverlayFileName)); err == nil {
		t.Error("the refused price was written")
	}
	if code, out, _ = r.do("pricing", "claude-opus-5-5-high", "--clear"); code != 0 || !strings.Contains(out, "cleared the price") {
		t.Errorf("clear: %d %q", code, out)
	}
	if p := overlayOf(t, r).Models["claude-opus-5-5-high"]; p.Pricing != nil {
		t.Errorf("price kept after --clear: %+v", p)
	}
}

func TestModelsPinAndClear(t *testing.T) {
	audit := ledgerFor(t)
	r := newModelsRig(t)
	code, out, errs := r.do("pin", "backend", "gpt-5.5")
	if code != 0 || !strings.Contains(out, "pinned backend to codex/gpt-5.5") {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	f, err := routerpolicy.Load(r.stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if pins := routerpolicy.Pins(f); len(pins) != 1 || pins[0] != (routerpolicy.Pin{Agent: "backend", Runtime: "codex", Model: "gpt-5.5"}) {
		t.Fatalf("pins = %+v", pins)
	}
	if rules := router.BuildPolicy(f).Rules; len(rules) != 1 || !rules[0].OverridePins {
		t.Errorf("rules = %+v", rules)
	}
	if code, out, _ = r.do("pin", "backend", "--clear"); code != 0 || !strings.Contains(out, "unpinned backend") {
		t.Errorf("clear: %d %q", code, out)
	}
	lines := audit()
	if len(lines) != 2 || lines[0]["file"] != "router-policy.yml" || lines[0]["action"] != "models.pin" || lines[1]["action"] != "models.unpin" {
		t.Errorf("audit = %v", lines)
	}
	if lines[1]["policy_sha_before"] != lines[0]["policy_sha_after"] {
		t.Error("the second change does not start from the first one's result")
	}
}

func TestModelsWriteRefusesWithNoHome(t *testing.T) {
	ledgerFor(t)
	r := newModelsRig(t)
	e := r.env()
	e.stateDir = ""
	var out, errb bytes.Buffer
	if code := modelsMain([]string{"disable", "gpt-5.5"}, &out, &errb, e); code != 1 || !strings.Contains(errb.String(), "no home directory") {
		t.Errorf("exit %d err=%q", code, errb.String())
	}
}

func TestModelsWriteReportsAnUnrecordableChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YAKOS_DISPATCH_LOG", blocker) // a file where the ledger directory should be
	r := newModelsRig(t)
	code, out, errs := r.do("disable", "gpt-5.5")
	if code != 1 || out != "" || !strings.Contains(errs, "could not be recorded in the dispatch log") {
		t.Errorf("exit %d out=%q err=%q", code, out, errs)
	}
	if _, err := os.Stat(filepath.Join(r.stateDir, modelreg.OverlayFileName)); err != nil {
		t.Error("the write itself should have happened (and be reported as unaudited)")
	}
}

// ---- router policy ------------------------------------------------------------------

const policyForGet = `# operator's policy
allow_unsandboxed_runtimes: [codex]
hooks_endpoint: true
gateway_classes:
  opus: claude-opus-4-1-20250805
rules:
  - match: {domain: code-review}
    action: {runtime: codex, model: gpt-5.5, fallbacks: [claude]}
  - match: {agent: backend}
    action: {runtime: codex, model: gpt-5.6-terra}
    override_pins: true
`

func polEnv(t *testing.T, content string) (explainEnv, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".yakos-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		if err := os.WriteFile(routerpolicy.Path(dir), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return explainEnv{stateDir: func() string { return dir }}, dir
}

func runPolicy(t *testing.T, env explainEnv, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := routerMain("", append([]string{"policy"}, args...), &out, &errb, env)
	return code, out.String(), errb.String()
}

func TestRouterPolicyGetGolden(t *testing.T) {
	env, _ := polEnv(t, policyForGet)
	code, out, errs := runPolicy(t, env, "get")
	if code != 0 || errs != "" {
		t.Fatalf("exit %d err=%q", code, errs)
	}
	goldenCompareIn(t, "router-policy", "get-text", out)
	code, out, _ = runPolicy(t, env, "get", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	goldenCompareIn(t, "router-policy", "get-json", out)
	var v router.PolicyView
	if err := json.Unmarshal([]byte(out), &v); err != nil || len(v.Rules) != 2 || len(v.Pins) != 1 {
		t.Errorf("json view: %v %+v", err, v)
	}
}

func TestRouterPolicyGetWithNoFile(t *testing.T) {
	env, _ := polEnv(t, "")
	code, out, _ := runPolicy(t, env, "get")
	if code != 0 || !strings.Contains(out, "policy_sha: (no trusted policy file)") {
		t.Errorf("exit %d out=%q", code, out)
	}
}

func TestRouterPolicySet(t *testing.T) {
	audit := ledgerFor(t)
	env, dir := polEnv(t, "allow_unsandboxed_runtimes: [agy]\nhooks_endpoint: true\n")
	rules := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(rules, []byte("- match: {class: chat}\n  action: {runtime: claude, model: sonnet}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runPolicy(t, env, "set", "--rules-file", rules)
	if code != 0 || !strings.HasPrefix(out, "ok: router rules (router-policy.yml ") {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	if strings.Contains(out+errs, dir) || strings.Contains(out+errs, rules) {
		t.Errorf("output names a path: %q %q", out, errs)
	}
	f, err := routerpolicy.Load(dir)
	if err != nil || !f.HooksEndpoint || len(f.AllowUnsandboxedRuntimes) != 1 || len(router.BuildPolicy(f).Rules) != 1 {
		t.Fatalf("policy after set: %+v %v", f, err)
	}
	lines := audit()
	if len(lines) != 1 || lines[0]["action"] != "router.policy.set" || lines[0]["policy_sha_after"] != f.SHA {
		t.Errorf("audit = %v (policy sha %s)", lines, f.SHA)
	}
}

func TestRouterPolicySetRefusals(t *testing.T) {
	audit := ledgerFor(t)
	env, dir := polEnv(t, policyForGet)
	before, _ := os.ReadFile(routerpolicy.Path(dir))
	tmp := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := map[string][]string{
		"unknown runtime":  {"set", "--rules-file", write("a", "- {match: {agent: a}, action: {runtime: nosuch}}\n")},
		"not a list":       {"set", "--rules-file", write("b", "hooks_endpoint: true\n")},
		"too many":         {"set", "--rules-file", write("c", strings.Repeat("- {match: {agent: a}, action: {runtime: claude}}\n", 7))},
		"oversize":         {"set", "--rules-file", write("d", "[]\n# "+strings.Repeat("x", maxPolicyInput)+"\n")},
		"directory":        {"set", "--rules-file", tmp},
		"missing file":     {"set", "--rules-file", filepath.Join(tmp, "nope")},
		"no flag":          {"set"},
		"get with args":    {"get", "extra"},
		"unknown subcmd":   {"frobnicate"},
		"unknown flag":     {"get", "--nope"},
		"set + positional": {"set", "--rules-file", write("e", "[]\n"), "extra"},
	}
	for name, args := range cases {
		if code, out, errs := runPolicy(t, env, args...); code == 0 || out != "" || errs == "" {
			t.Errorf("%s: exit %d out=%q err=%q; want failure with a message", name, code, out, errs)
		}
	}
	if after, _ := os.ReadFile(routerpolicy.Path(dir)); string(after) != string(before) {
		t.Errorf("a refused set changed the policy:\n%s", after)
	}
	if n := len(audit()); n != 0 {
		t.Errorf("%d audit lines for refused sets", n)
	}
}

func TestRouterHelpMentionsPolicy(t *testing.T) {
	var out bytes.Buffer
	printRouterHelp(&out)
	for _, want := range []string{"policy get", "policy set", "--rules-file"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

// goldenCompareIn compares got with testdata/<sub>/<name>.golden;
// YAKOS_UPDATE_GOLDEN=1 rewrites it.
func goldenCompareIn(t *testing.T, sub, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", sub, name+".golden")
	if os.Getenv("YAKOS_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (YAKOS_UPDATE_GOLDEN=1 writes it): %v", name, err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden:\n--- want\n%s--- got\n%s", name, want, got)
	}
}
