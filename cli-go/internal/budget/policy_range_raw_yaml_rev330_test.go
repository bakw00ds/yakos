package budget

// Reviewer table (PR 330, final round), scratch only: spec item 2 of
// work/current/briefs/k136-final-push.md through RAW YAML, so every spelling reaches the
// decoder. An out-of-range limit in the trusted policy is ignored with a warning and the
// built-in stays; an explicit 0 still turns that unit off; the largest accepted values
// (tokens 2^50, dollars 1e9) are accepted with a finite, marshalable stop.

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

func zzWritePolicy(t *testing.T, dir, yml string) {
	t.Helper()
	if err := os.WriteFile(PolicyPath(dir), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
}

func zzHasWarn(st Status, sub string) bool {
	for _, w := range st.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestZZR330_OutOfRangeSpellingsKeepTheBuiltin(t *testing.T) {
	type bad struct{ unit, yaml string }
	cases := []bad{
		{"limit_tokens", "-1"}, {"limit_tokens", "-9223372036854775808"}, {"limit_tokens", "1500000.5"},
		{"limit_tokens", "1125899906842625"}, // 2^50 + 1
		{"limit_tokens", "9223372036854775807"}, {"limit_tokens", ".nan"}, {"limit_tokens", ".inf"}, {"limit_tokens", "1e30"},
		{"limit_usd", "-5"}, {"limit_usd", ".nan"}, {"limit_usd", ".inf"}, {"limit_usd", "-.inf"},
		{"limit_usd", "1000000001"}, {"limit_usd", "1e308"}, {"limit_usd", "1.7976931348623157e308"},
	}
	for _, agent := range []string{"supervisor", "librarian"} {
		wantUSD, wantTok := 100.0, int64(33_000_000)
		if agent == "librarian" {
			wantUSD, wantTok = 40, 13_000_000
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/%s=%s", agent, c.unit, c.yaml), func(t *testing.T) {
				dir := t.TempDir()
				zzWritePolicy(t, dir, fmt.Sprintf("agents:\n  %s:\n    %s: %s\n  backend:\n    limit_usd: 50\n", agent, c.unit, c.yaml))
				st := mustEval(t, agent, Options{StateDir: dir, Now: clock(octMid)})
				if st.LimitUSD != wantUSD || st.LimitTokens != wantTok {
					t.Errorf("the built-in must stay: limit_usd=%v limit_tokens=%d (want %v and %d): %+v", st.LimitUSD, st.LimitTokens, wantUSD, wantTok, st)
				}
				if !zzHasWarn(st, c.unit+" for "+agent+" ignored") {
					t.Errorf("the ignored value must be reported: %v", st.Warnings)
				}
				if math.IsInf(st.StopUSD, 0) || math.IsNaN(st.StopUSD) {
					t.Errorf("stop_usd must be finite: %v", st.StopUSD)
				}
				if _, err := json.Marshal(st); err != nil {
					t.Errorf("status must marshal: %v", err)
				}
				// A valid sibling entry is untouched.
				if b := mustEval(t, "backend", Options{StateDir: dir, Now: clock(octMid)}); b.LimitUSD != 50 {
					t.Errorf("backend's $50 entry must survive a bad supervisor value: %+v", b)
				}
			})
		}
	}
}

func TestZZR330_ExplicitZeroStillTurnsAUnitOffAndTheLargestValuesAreAccepted(t *testing.T) {
	dir := t.TempDir()
	zzWritePolicy(t, dir, "agents:\n  supervisor:\n    limit_tokens: 0\n  librarian:\n    limit_usd: 0\n")
	o := Options{StateDir: dir, Now: clock(octMid)}
	if st := mustEval(t, "supervisor", o); st.LimitTokens != 0 || st.LimitUSD != 100 || zzHasWarn(st, "ignored") {
		t.Errorf("limit_tokens 0 turns the token unit off, no warning: %+v", st)
	}
	if st := mustEval(t, "librarian", o); st.LimitUSD != 0 || st.LimitTokens != 13_000_000 || zzHasWarn(st, "ignored") {
		t.Errorf("limit_usd 0 turns the dollar unit off, no warning: %+v", st)
	}
	dir2 := t.TempDir()
	zzWritePolicy(t, dir2, "agents:\n  supervisor:\n    limit_usd: 1000000000\n    limit_tokens: 1125899906842624\n  backend:\n    limit_tokens: 50000000\n")
	o2 := Options{StateDir: dir2, Now: clock(octMid)}
	st := mustEval(t, "supervisor", o2)
	if st.LimitUSD != 1e9 || st.StopUSD != 2e9 || st.LimitTokens != 1<<50 || st.StopTokens != 2<<50 || zzHasWarn(st, "ignored") {
		t.Errorf("exactly 1e9 dollars and 2^50 tokens are in range, with the x2 stop: %+v", st)
	}
	if _, err := json.Marshal(st); err != nil {
		t.Errorf("must marshal: %v", err)
	}
	if b := mustEval(t, "backend", o2); b.LimitTokens != 50_000_000 || zzHasWarn(b, "ignored") {
		t.Errorf("50M is a normal value: %+v", b)
	}
}
