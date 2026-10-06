package budget

// Probe for the PR #330 security review (final round): every Status that Evaluate
// returns is finite and marshals to JSON, whatever the trusted policy and the
// project file say. A non-finite amount or stop would make `budget check --json`
// print no JSON, and the bash supervisor hook would fail open.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

var sec330gPolicies = []string{
	"",
	"agents:\n  supervisor:\n    limit_usd: 1e308\n",
	"agents:\n  supervisor:\n    limit_usd: .inf\n",
	"agents:\n  supervisor:\n    limit_usd: 1000000000\n",
	"agents:\n  supervisor:\n    limit_tokens: 99999999999999999\n",
	"agents:\n  supervisor:\n    limit_tokens: 1125899906842624\n",
	"agents:\n  supervisor:\n    limit_usd: 0\n    limit_tokens: 0\n",
	"agents:\n  librarian:\n    limit_usd: 0\n    limit_tokens: 0\n",
	"default:\n  limit_usd: 5\n  window: lifetime\n",
	"default:\n  limit_usd: 1e308\n  limit_tokens: 1125899906842624\n",
	"agents:\n  backend:\n    limit_tokens: 500000000\n",
	"agents:\n  backend:\n    limit_usd: 1000000000\n    limit_tokens: 1125899906842624\n",
	"agents:\n  supervisor:\n    limit_usd: 5e-324\n",
	"agents:\n  supervisor:\n    limit_usd: 1e-300\n  backend:\n    limit_usd: 1e-310\n",
	"default:\n  limit_usd: 5e-324\n",
}

var sec330gProjects = []string{
	"",
	"supervisor:\n  agent: backend\n",
	"supervisor:\n  agent: librarian\n",
	"supervisor:\n  agent: watchdog\n",
	"supervisor:\n  agent: backend\nagent_budgets:\n  backend: 0.000001\n",
}

func TestSec330g_EvaluateStatusIsAlwaysFinite(t *testing.T) {
	bad := 0
	n := 0
	for pi, pol := range sec330gPolicies {
		dir := t.TempDir()
		appendLog(t, dir,
			ledgerLine("supervisor", octMid, "api", 70_000_000, 0, 0, 0, 12345.5),
			ledgerLine("librarian", octMid, "api", 70_000_000, 0, 0, 0, 12345.5),
			ledgerLine("backend", octMid, "api", 70_000_000, 0, 0, 0, 12345.5),
			ledgerLine("watchdog", octMid, "api", 70_000_000, 0, 0, 0, 12345.5))
		if pol != "" {
			if err := os.WriteFile(PolicyPath(dir), []byte(pol), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for ji, yml := range sec330gProjects {
			proj := ""
			if yml != "" {
				proj = t.TempDir()
				if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(yml), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, a := range []string{"supervisor", "librarian", "backend", "watchdog"} {
				n++
				st, _ := Evaluate(a, Options{StateDir: dir, Now: clock(octMid), Project: proj})
				nums := map[string]float64{
					"limit_usd": st.LimitUSD, "stop_usd": st.StopUSD, "spent_usd": st.SpentUSD,
					"pct": st.Pct, "tokens_pct": st.TokensPct,
				}
				for k, v := range nums {
					if math.IsInf(v, 0) || math.IsNaN(v) {
						bad++
						t.Errorf("P%d J%d %s: %s = %v", pi, ji, a, k, v)
					}
				}
				if st.LimitTokens < 0 || st.StopTokens < 0 || (st.LimitTokens > 0 && st.StopTokens <= 0) {
					bad++
					t.Errorf("P%d J%d %s: token limit %d stop %d", pi, ji, a, st.LimitTokens, st.StopTokens)
				}
				if _, err := json.Marshal(st); err != nil {
					bad++
					t.Errorf("P%d J%d %s: status does not marshal: %v", pi, ji, a, err)
				}
			}
		}
	}
	t.Logf("%d statuses, %d problems", n, bad)
}
