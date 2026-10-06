package budget

// Probe for the PR #330 security review (final round): a trusted policy value that
// cannot be a real limit is ignored with a warning and the built-in limit stays on
// (fail closed); an explicit 0 still switches a limit off. Asserting version.

import (
	"os"
	"testing"
)

func TestSec330f_OutOfRangeLimitsFailClosed(t *testing.T) {
	type want struct {
		agent  string
		usd    float64 // expected LimitUSD
		tokens int64   // expected LimitTokens
		warn   bool    // a warning must be reported
	}
	cases := []struct {
		label string
		pol   string
		want  []want
	}{
		{"supervisor tokens > 2^50", "agents:\n  supervisor:\n    limit_tokens: 99999999999999999\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"supervisor tokens negative", "agents:\n  supervisor:\n    limit_tokens: -5\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"supervisor usd negative", "agents:\n  supervisor:\n    limit_usd: -5\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"supervisor usd .nan", "agents:\n  supervisor:\n    limit_usd: .nan\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"supervisor usd .inf", "agents:\n  supervisor:\n    limit_usd: .inf\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"supervisor usd -.inf", "agents:\n  supervisor:\n    limit_usd: -.inf\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"supervisor usd 1e308", "agents:\n  supervisor:\n    limit_usd: 1e308\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"supervisor usd just above 1e9", "agents:\n  supervisor:\n    limit_usd: 1000000001\n", []want{{"supervisor", 100, 33_000_000, true}}},
		{"librarian tokens > 2^50", "agents:\n  librarian:\n    limit_tokens: 99999999999999999\n", []want{{"librarian", 40, 13_000_000, true}}},
		{"non-integer tokens beside another entry", "agents:\n  supervisor:\n    limit_tokens: 1500000.5\n  backend:\n    limit_usd: 50\n",
			[]want{{"supervisor", 100, 33_000_000, true}, {"backend", 50, 0, false}}},
		{"control: supervisor tokens 0", "agents:\n  supervisor:\n    limit_tokens: 0\n", []want{{"supervisor", 100, 0, false}}},
		{"control: supervisor usd 0", "agents:\n  supervisor:\n    limit_usd: 0\n", []want{{"supervisor", 0, 33_000_000, false}}},
		{"control: supervisor tokens 50M", "agents:\n  supervisor:\n    limit_tokens: 50000000\n", []want{{"supervisor", 100, 50_000_000, false}}},
		{"control: supervisor usd 1e9", "agents:\n  supervisor:\n    limit_usd: 1000000000\n", []want{{"supervisor", 1e9, 33_000_000, false}}},
	}
	for _, c := range cases {
		dir := t.TempDir()
		appendLog(t, dir, ledgerLine("supervisor", octMid, "subscription", 1, 0, 0, 0, 0))
		if err := os.WriteFile(PolicyPath(dir), []byte(c.pol), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			st, _ := Evaluate(w.agent, Options{StateDir: dir, Now: clock(octMid)})
			t.Logf("%-40s %-10s usd=%v/%v tok=%d/%d warns=%d %v", c.label, w.agent, st.LimitUSD, st.StopUSD, st.LimitTokens, st.StopTokens, len(st.Warnings), st.Warnings)
			if st.LimitUSD != w.usd || st.LimitTokens != w.tokens {
				t.Errorf("%s: %s has usd=%v tokens=%d, want usd=%v tokens=%d", c.label, w.agent, st.LimitUSD, st.LimitTokens, w.usd, w.tokens)
			}
			if w.warn && len(st.Warnings) == 0 {
				t.Errorf("%s: %s: an ignored value must be reported with a warning", c.label, w.agent)
			}
		}
	}
}
