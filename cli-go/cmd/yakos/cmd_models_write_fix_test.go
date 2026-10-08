package main

// cmd_models_write_fix_test.go: regression tests for the sec-356 findings on the
// K-153 writers (M1 alias smuggling through `router policy set`, M2 the audit log
// location and open-before-write, L2 pricing validated before any write).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// M1: an operator file whose allow_unsandboxed_runtimes aliases an empty anchor,
// and a rules input that redefines the anchor, must not widen the allow-list.
func TestRouterPolicySetCannotRedefineAnAnchorAPrivilegedKeyAliases(t *testing.T) {
	ledgerFor(t)
	env, dir := polEnv(t, "ext: &rts []\nrules:\n  - match: {class: chat}\n    action: {runtime: claude}\nallow_unsandboxed_runtimes: *rts\n")
	before, _ := os.ReadFile(routerpolicy.Path(dir))
	rules := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(rules, []byte("- match: {agent: a}\n  action: {runtime: claude, fallbacks: &rts [codex]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runPolicy(t, env, "set", "--rules-file", rules)
	if code == 0 || out != "" {
		t.Errorf("exit %d out=%q err=%q; the write must be refused", code, out, errs)
	}
	if after, _ := os.ReadFile(routerpolicy.Path(dir)); string(after) != string(before) {
		t.Errorf("the policy file changed:\n%s", after)
	}
	_, get, _ := runPolicy(t, env, "get")
	if !strings.Contains(get, "allow_unsandboxed_runtimes: []") {
		t.Errorf("policy get:\n%s", get)
	}
}

// M2: with the home log unopenable, `router policy set` writes nothing.
func TestRouterPolicySetRefusedWhenTheHomeLogCannotBeOpened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX paths")
	}
	t.Setenv("YAKOS_DISPATCH_LOG", os.DevNull)
	env, dir := polEnv(t, "hooks_endpoint: true\n")
	if err := os.Mkdir(filepath.Join(dir, "dispatch-log.ndjson"), 0o700); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(routerpolicy.Path(dir))
	rules := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(rules, []byte("- match: {class: chat}\n  action: {runtime: claude}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runPolicy(t, env, "set", "--rules-file", rules)
	if code == 0 || !strings.Contains(errs, "dispatch log cannot be opened") {
		t.Errorf("exit %d err=%q", code, errs)
	}
	if after, _ := os.ReadFile(routerpolicy.Path(dir)); string(after) != string(before) {
		t.Errorf("the policy file changed:\n%s", after)
	}
}

// M2: a project's YAKOS_DISPATCH_LOG does not redirect the policy audit line.
func TestRouterPolicySetAuditsInTheHomeLog(t *testing.T) {
	project := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", project)
	env, dir := polEnv(t, "")
	rules := filepath.Join(t.TempDir(), "rules.yml")
	if err := os.WriteFile(rules, []byte("- match: {class: chat}\n  action: {runtime: claude}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPolicy(t, env, "set", "--rules-file", rules); code != 0 {
		t.Fatalf("exit %d err=%q", code, errs)
	}
	if n := len(configChangedLines(dir)); n != 1 {
		t.Errorf("home log holds %d config_changed lines, want 1", n)
	}
	if es, _ := os.ReadDir(project); len(es) != 0 {
		t.Errorf("the project override received files: %v", es)
	}
}

// L2: --billing api with an invalid price saves nothing, billing included.
func TestModelsPricingWithAnInvalidPriceWritesNothing(t *testing.T) {
	audit := ledgerFor(t)
	for name, args := range map[string][]string{
		"not a number": {"--input", "x", "--output", "25"},
		"negative":     {"--input", "-1", "--output", "25"},
		"missing out":  {"--input", "5"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newModelsRig(t)
			code, out, errs := r.do(append([]string{"pricing", "claude-opus-5-5-high", "--billing", "api"}, args...)...)
			if code == 0 {
				t.Errorf("accepted: out=%q", out)
			}
			if _, err := os.Stat(filepath.Join(r.stateDir, modelreg.OverlayFileName)); err == nil {
				t.Errorf("the overlay was written (err=%q)", errs)
			}
			if n := len(audit()); n != 0 {
				t.Errorf("%d audit lines for a refused write", n)
			}
		})
	}
}
