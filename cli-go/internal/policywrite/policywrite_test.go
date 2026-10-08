package policywrite

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/statepath"
)

func fixture(t *testing.T) (string, *modelreg.Registry) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("owner-only modes")
	}
	dir := filepath.Join(t.TempDir(), ".yakos-state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	reg, err := modelreg.Load(modelreg.Options{StateDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return dir, reg
}

type recorded struct{ changes []Change }

func (r *recorded) rec(c Change) error { r.changes = append(r.changes, c); return nil }

func read(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestRefusalsCarryAFixedCodeAndWriteNothing(t *testing.T) {
	dir, reg := fixture(t)
	var r recorded
	cases := map[string]struct {
		err  error
		code Code
	}{
		"unknown model":             {SetEnabled(dir, reg, "no-such-model", true, r.rec), CodeUnknownModel},
		"unknown alias id":          {SetAlias(dir, reg, "balanced", "codex", "no-such-model", r.rec), CodeUnknownModel},
		"bad agent":                 {SetPin(dir, reg, "../x", "gpt-5.6-terra", "codex", false, r.rec), CodeInvalid},
		"unknown pin model":         {SetPin(dir, reg, "backend", "no-such-model", "", false, r.rec), CodeUnknownModel},
		"price, no output":          {SetPricing(dir, reg, "gpt-5.6-terra", PricingArgs{Billing: "api", Input: "1"}, r.rec), CodeInvalid},
		"price, bad number":         {SetPricing(dir, reg, "gpt-5.6-terra", PricingArgs{Billing: "api", Input: "x", Output: "1"}, r.rec), CodeInvalid},
		"price, bad billing":        {SetPricing(dir, reg, "gpt-5.6-terra", PricingArgs{Billing: "free", Input: "1", Output: "1"}, r.rec), CodeInvalid},
		"price, subscription model": {SetPricing(dir, reg, "gpt-5.6-terra", PricingArgs{Input: "1", Output: "1"}, r.rec), CodeBilling},
	}
	for name, tc := range cases {
		var pe *Error
		if !errors.As(tc.err, &pe) || pe.Code != tc.code {
			t.Errorf("%s: %v, want code %s", name, tc.err, tc.code)
		}
	}
	if len(r.changes) != 0 {
		t.Errorf("a refusal was recorded: %v", r.changes)
	}
	for _, f := range []string{modelreg.OverlayFileName, "router-policy.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s was created by a refused write", f)
		}
	}
}

// Billing and price land in one write and one record; a price that fails
// validation leaves the billing mode as it was.
func TestSetPricingIsOneAtomicWrite(t *testing.T) {
	dir, reg := fixture(t)
	var r recorded
	if err := SetPricing(dir, reg, "gpt-5.6-terra", PricingArgs{Billing: "api", Input: "1.5", Output: "6"}, r.rec); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != 1 || !r.changes[0].Res.Changed || r.changes[0].Action != "models.pricing" {
		t.Fatalf("records = %+v", r.changes)
	}
	got := read(t, filepath.Join(dir, modelreg.OverlayFileName))
	for _, want := range []string{"billing: api", "input: 1.5", "output: 6"} {
		if !contains(got, want) {
			t.Errorf("overlay lacks %q:\n%s", want, got)
		}
	}
	before := got
	// An out-of-range price with a billing change: nothing is written.
	err := SetPricing(dir, reg, "gpt-5.6-terra", PricingArgs{Billing: "local", Input: "1", Output: "1", CacheRead: "-1"}, r.rec)
	if err == nil {
		t.Fatal("a negative cache price was accepted")
	}
	if read(t, filepath.Join(dir, modelreg.OverlayFileName)) != before || len(r.changes) != 1 {
		t.Error("a failed price left the billing mode changed")
	}
	// Billing alone is its own write.
	if err := SetPricing(dir, reg, "gpt-5.6-terra", PricingArgs{Billing: "local"}, r.rec); err != nil || len(r.changes) != 2 || r.changes[1].Action != "models.billing" {
		t.Errorf("billing only: %v %+v", err, r.changes)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRecorderErrorIsReturnedAsIs(t *testing.T) {
	dir, reg := fixture(t)
	boom := errors.New("boom")
	if err := SetEnabled(dir, reg, "gpt-5.5", false, func(Change) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

func TestSetRulesIfIsACompareAndSwap(t *testing.T) {
	dir, _ := fixture(t)
	var r recorded
	rules := []byte("- {match: {agent: a}, action: {runtime: codex, model: gpt-5.6-terra}}\n")
	empty := ""
	if err := SetRules(dir, rules, &empty, r.rec); err != nil {
		t.Fatalf("first write against a missing file: %v", err)
	}
	wrong := "deadbeef"
	err := SetRules(dir, []byte("[]"), &wrong, r.rec)
	var stale *statepath.StaleError
	if !errors.As(err, &stale) || stale.SHA != r.changes[0].Res.SHAAfter {
		t.Fatalf("err = %v", err)
	}
	if len(r.changes) != 1 {
		t.Error("a stale write was recorded")
	}
	cur := stale.SHA
	if err := SetRules(dir, []byte("[]"), &cur, r.rec); err != nil {
		t.Errorf("write against the current sha: %v", err)
	}
	// A nil base is the CLI's unconditional replace.
	if err := SetRules(dir, rules, nil, r.rec); err != nil {
		t.Errorf("unconditional write: %v", err)
	}
}
