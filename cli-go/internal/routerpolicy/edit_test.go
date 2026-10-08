package routerpolicy_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

func stateWith(t *testing.T, content string) string {
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
	return dir
}

const privileged = "allow_unsandboxed_runtimes: [codex]\nhooks_endpoint: true\nopenai_endpoint: true\n"

func TestMaxRulesEqualsTheRouters(t *testing.T) {
	if routerpolicy.MaxRules != router.MaxRules {
		t.Fatalf("routerpolicy.MaxRules=%d, router.MaxRules=%d", routerpolicy.MaxRules, router.MaxRules)
	}
}

func TestSetRulesReplacesRulesAndKeepsThePrivilegedKeys(t *testing.T) {
	dir := stateWith(t, privileged+"rules:\n  - match: {class: chat}\n    action: {runtime: codex}\n")
	rules := "- match: {agent: backend}\n  action: {runtime: claude, model: sonnet}\n"
	res, err := routerpolicy.SetRules(dir, []byte(rules), router.CheckPolicy)
	if err != nil || !res.Changed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	f, err := routerpolicy.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f.SHA != res.SHAAfter {
		t.Errorf("Load sha %s != write sha %s", f.SHA, res.SHAAfter)
	}
	if len(f.AllowUnsandboxedRuntimes) != 1 || !f.HooksEndpoint || !f.OpenAIEndpoint() {
		t.Errorf("privileged keys changed: %+v", f)
	}
	p := router.BuildPolicy(f)
	if len(p.Rules) != 1 || p.Rules[0].Match.Agent != "backend" || p.Rules[0].Action.Model != "sonnet" {
		t.Errorf("rules = %+v", p.Rules)
	}
}

func TestSetRulesCannotSetAPrivilegedKey(t *testing.T) {
	dir := stateWith(t, "")
	// A rules document that tries to smuggle a top-level key is not a list.
	for _, bad := range []string{"allow_unsandboxed_runtimes: [codex]\n", "hooks_endpoint: true\n", "not a list", "- {match: {agent: a}, action: {runtime: nosuch}}\n"} {
		if _, err := routerpolicy.SetRules(dir, []byte(bad), router.CheckPolicy); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := os.Stat(routerpolicy.Path(dir)); err == nil {
		t.Error("a refused write created the file")
	}
}

func TestSetRulesRefusesMoreThanTheRouterReads(t *testing.T) {
	dir := stateWith(t, "")
	one := "- {match: {agent: a}, action: {runtime: claude}}\n"
	if _, err := routerpolicy.SetRules(dir, []byte(strings.Repeat(one, routerpolicy.MaxRules+1)), router.CheckPolicy); err == nil {
		t.Fatal("accepted 7 rules")
	}
}

func TestSetRulesEmptyListRemovesTheKey(t *testing.T) {
	dir := stateWith(t, privileged+"rules:\n  - match: {class: chat}\n    action: {runtime: codex}\n")
	if _, err := routerpolicy.SetRules(dir, []byte("[]\n"), router.CheckPolicy); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(routerpolicy.Path(dir))
	if strings.Contains(string(b), "rules") || !strings.Contains(string(b), "hooks_endpoint: true") {
		t.Errorf("file:\n%s", b)
	}
}

func TestPinsRoundTripAndLeaveOtherRulesAlone(t *testing.T) {
	dir := stateWith(t, privileged+"rules:\n  - match: {class: chat}\n    action: {runtime: codex}\n")
	pin := routerpolicy.Pin{Agent: "backend", Runtime: "codex", Model: "gpt-5.5"}
	if _, err := routerpolicy.SetPin(dir, pin, router.CheckPolicy); err != nil {
		t.Fatal(err)
	}
	f, _ := routerpolicy.Load(dir)
	if got := routerpolicy.Pins(f); len(got) != 1 || got[0] != pin {
		t.Fatalf("pins = %+v", got)
	}
	p := router.BuildPolicy(f)
	if len(p.Rules) != 2 || p.Rules[0].Match.Agent != "backend" || !p.Rules[0].OverridePins || p.Rules[1].Match.Class != "chat" {
		t.Fatalf("a new pin must come first and keep the operator's own rule: %+v", p.Rules)
	}
	// Re-pinning replaces in place.
	pin.Model = "gpt-5.6-sol"
	if _, err := routerpolicy.SetPin(dir, pin, router.CheckPolicy); err != nil {
		t.Fatal(err)
	}
	f, _ = routerpolicy.Load(dir)
	if got := routerpolicy.Pins(f); len(got) != 1 || got[0].Model != "gpt-5.6-sol" || len(router.BuildPolicy(f).Rules) != 2 {
		t.Fatalf("re-pin: %+v", got)
	}
	// Clearing removes only the pin.
	if _, err := routerpolicy.ClearPin(dir, "backend", nil); err != nil {
		t.Fatal(err)
	}
	f, _ = routerpolicy.Load(dir)
	if len(routerpolicy.Pins(f)) != 0 || len(router.BuildPolicy(f).Rules) != 1 || !f.HooksEndpoint {
		t.Fatalf("after clear: %+v", f)
	}
	// Clearing what is not there is not an error.
	if res, err := routerpolicy.ClearPin(dir, "nobody", nil); err != nil || res.Changed {
		t.Errorf("clear of a missing pin: %+v %v", res, err)
	}
}

func TestOnlyPinShapedRulesCountAsPins(t *testing.T) {
	dir := stateWith(t, "rules:\n"+
		"  - {match: {agent: a}, action: {runtime: codex, model: x1}}\n"+ // no override_pins
		"  - {match: {agent: b, class: chat}, action: {runtime: codex, model: x2}, override_pins: true}\n"+ // two match keys
		"  - {match: {agent: c}, action: {runtime: codex}, override_pins: true}\n"+ // no model
		"  - {match: {agent: d}, action: {runtime: codex, model: x3}, override_pins: true}\n")
	f, _ := routerpolicy.Load(dir)
	got := routerpolicy.Pins(f)
	if len(got) != 1 || got[0].Agent != "d" {
		t.Fatalf("pins = %+v, want only d", got)
	}
	if _, err := routerpolicy.ClearPin(dir, "a", nil); err != nil {
		t.Fatal(err)
	}
	f, _ = routerpolicy.Load(dir)
	if n := len(router.BuildPolicy(f).Rules); n != 4 {
		t.Errorf("ClearPin removed a rule that is not a pin: %d rules left", n)
	}
}

func TestSetPinRefusesWhenAllSlotsAreInUse(t *testing.T) {
	var b strings.Builder
	b.WriteString("rules:\n")
	for i := 0; i < routerpolicy.MaxRules; i++ {
		b.WriteString("  - {match: {class: chat}, action: {runtime: codex}}\n")
	}
	dir := stateWith(t, b.String())
	if _, err := routerpolicy.SetPin(dir, routerpolicy.Pin{Agent: "a", Runtime: "codex", Model: "x"}, nil); err == nil {
		t.Fatal("pinned into a full rules list")
	}
}

func TestEditRefusesAnUntrustedPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := stateWith(t, privileged)
	if err := os.Chmod(routerpolicy.Path(dir), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := routerpolicy.SetPin(dir, routerpolicy.Pin{Agent: "a", Runtime: "codex", Model: "x"}, nil); err == nil {
		t.Fatal("wrote over a group-writable policy")
	}
	if _, err := routerpolicy.Edit("", nil, nil); err == nil {
		t.Error("empty state dir accepted")
	}
	if _, err := routerpolicy.Edit("relative/dir", nil, nil); err == nil {
		t.Error("relative state dir accepted")
	}
}

func TestWrittenPolicyIsOwnerOnlyAndAtomic(t *testing.T) {
	dir := stateWith(t, "")
	if _, err := routerpolicy.SetPin(dir, routerpolicy.Pin{Agent: "a", Runtime: "claude", Model: "sonnet"}, router.CheckPolicy); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(routerpolicy.Path(dir)); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %o", fi.Mode().Perm())
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}
