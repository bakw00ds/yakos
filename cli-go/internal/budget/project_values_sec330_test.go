package budget

// Probe for the PR #330 security review (spec item 7): a project's agent_budgets:
// value is validated like a policy value. Whatever a project writes, the Status stays
// finite, marshals, and is never looser than without the project file. Not committed.

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestSec330j_ProjectValuesKeepStatusFinite(t *testing.T) {
	values := []string{".inf", "-.inf", ".nan", "1e308", "1000000001", "5e-324", "0.001", "-1", "0", "3"}
	policies := []string{"", "agents:\n  backend:\n    limit_usd: 50\n"}
	for pi, pol := range policies {
		dir := t.TempDir()
		appendLog(t, dir,
			ledgerLine("backend", octMid, "api", 1000, 0, 0, 0, 0.5),
			ledgerLine("watchdog", octMid, "api", 1000, 0, 0, 0, 0.5),
			ledgerLine("supervisor", octMid, "api", 1000, 0, 0, 0, 0.5))
		if pol != "" {
			if err := os.WriteFile(PolicyPath(dir), []byte(pol), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, v := range values {
			for _, c := range []struct{ agent, yml string }{
				{"backend", "agent_budgets:\n  backend: " + v + "\n"},
				{"watchdog", "supervisor:\n  agent: watchdog\nagent_budgets:\n  watchdog: " + v + "\n"},
				{"supervisor", "agent_budgets:\n  supervisor: " + v + "\n"},
			} {
				proj := t.TempDir()
				if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(c.yml), 0o644); err != nil {
					t.Fatal(err)
				}
				base, _ := Evaluate(c.agent, Options{StateDir: dir, Now: clock(octMid)})
				st, _ := Evaluate(c.agent, Options{StateDir: dir, Now: clock(octMid), Project: proj})
				for k, x := range map[string]float64{"limit_usd": st.LimitUSD, "stop_usd": st.StopUSD, "pct": st.Pct, "tokens_pct": st.TokensPct} {
					if math.IsInf(x, 0) || math.IsNaN(x) {
						t.Errorf("P%d %s agent_budgets=%s: %s = %v", pi, c.agent, v, k, x)
					}
				}
				if _, err := json.Marshal(st); err != nil {
					t.Errorf("P%d %s agent_budgets=%s: status does not marshal: %v", pi, c.agent, v, err)
				}
				// Never looser than without the project file (a project may only lower).
				if base.LimitUSD > 0 && (st.LimitUSD <= 0 || st.LimitUSD > base.LimitUSD+1e-9) {
					t.Errorf("P%d %s agent_budgets=%s: dollar limit loosened from %v to %v", pi, c.agent, v, base.LimitUSD, st.LimitUSD)
				}
				t.Logf("P%d %-10s agent_budgets=%-11s base usd=%v -> usd=%v stop=%v warns=%d", pi, c.agent, v, base.LimitUSD, st.LimitUSD, st.StopUSD, len(st.Warnings))
			}
		}
	}
}
