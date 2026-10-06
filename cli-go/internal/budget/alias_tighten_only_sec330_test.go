package budget

// Scratch probe for the PR #330 security review (final round): no .yakos.yml
// content may raise an agent's limit or stop, or turn its lifetime window monthly,
// and the agent a project names as its supervisor may never run looser than the
// supervisor itself. Not committed.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sec330eLim struct {
	usd, stopUSD float64
	tok, stopTok int64
	window       Window
}

func (l sec330eLim) String() string {
	return fmt.Sprintf("usd=%v/%v tok=%d/%d %s", l.usd, l.stopUSD, l.tok, l.stopTok, l.window)
}

func sec330eEval(t *testing.T, dir, agent, proj string) sec330eLim {
	t.Helper()
	st, _ := Evaluate(agent, Options{StateDir: dir, Now: clock(octMid), Project: proj})
	return sec330eLim{usd: st.LimitUSD, stopUSD: st.StopUSD, tok: st.LimitTokens, stopTok: st.StopTokens, window: st.Window}
}

// looser lists the ways got is looser than base: a limit raised or removed, a
// stop raised or removed, or a lifetime window turned monthly.
func sec330eLooser(base, got sec330eLim) []string {
	var v []string
	if base.usd > 0 && (got.usd <= 0 || got.usd > base.usd+1e-9) {
		v = append(v, "usd limit raised")
	}
	if base.usd > 0 && (got.stopUSD <= 0 || got.stopUSD > base.stopUSD+1e-9) {
		v = append(v, "usd stop raised")
	}
	if base.tok > 0 && (got.tok <= 0 || got.tok > base.tok) {
		v = append(v, "token limit raised")
	}
	if base.tok > 0 && (got.stopTok <= 0 || got.stopTok > base.stopTok) {
		v = append(v, "token stop raised")
	}
	if (base.usd > 0 || base.tok > 0) && base.window == Lifetime && got.window != Lifetime {
		v = append(v, "lifetime window turned "+string(got.window))
	}
	return v
}

var sec330ePolicies = []string{
	"",
	"default:\n  limit_usd: 5\n",
	"default:\n  limit_tokens: 1000000\n",
	"default:\n  limit_usd: 5\n  limit_tokens: 1000000\n  window: lifetime\n",
	"agents:\n  backend:\n    limit_usd: 500\n",
	"agents:\n  backend:\n    limit_usd: 0\n    limit_tokens: 0\n",
	"agents:\n  librarian:\n    limit_usd: 0\n    limit_tokens: 0\n",
	"agents:\n  supervisor:\n    limit_usd: 1000\n    limit_tokens: 100000000\n",
	"agents:\n  supervisor:\n    limit_usd: 0\n    limit_tokens: 0\n",
	"default:\n  window: lifetime\nagents:\n  supervisor:\n    window: monthly\n  backend:\n    limit_usd: 5\n",
	"default:\n  window: lifetime\nagents:\n  supervisor:\n    limit_usd: 100\n    window: monthly\n",
	"agents:\n  backend:\n    limit_usd: 5\n    window: lifetime\n  supervisor:\n    window: monthly\n",
	"default:\n  limit_usd: 5\n  limit_tokens: 1000000\nagents:\n  supervisor:\n    limit_usd: 1000\n    limit_tokens: 999999999\n",
	"agents:\n  lead:\n    limit_tokens: 2000000\n    window: lifetime\n",
	"agents:\n  librarian:\n    limit_tokens: 50000000\n",
}

func sec330eProjects(agent string) map[string]string {
	return map[string]string{
		"names it":         "supervisor:\n  agent: " + agent + "\n",
		"scan-only":        "supervisor:\n  enabled: true\nother:\n  agent: " + agent + "\n",
		"malformed":        "supervisor:\n\tagent: " + agent + "\n",
		"names+raise usd":  "supervisor:\n  agent: " + agent + "\nagent_budgets:\n  " + agent + ": 1000000\n",
		"names+zero usd":   "supervisor:\n  agent: " + agent + "\nagent_budgets:\n  " + agent + ": 0\n",
		"agent_budgets lo": "agent_budgets:\n  " + agent + ": 0.5\n",
		"quoted name":      "supervisor:\n  agent: \"" + agent + "\"\n",
		"twice":            "supervisor:\n  agent: " + agent + "\nsupervisor:\n  agent: lead\n",
	}
}

func TestSec330e_NoProjectFileLoosensABudget(t *testing.T) {
	agents := []string{"backend", "librarian", "watchdog", "lead"}
	var fails []string
	cases := 0
	for pi, pol := range sec330ePolicies {
		dir := t.TempDir()
		if pol != "" {
			if err := os.WriteFile(PolicyPath(dir), []byte(pol), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for _, a := range agents {
			base := sec330eEval(t, dir, a, "")
			for name, yml := range sec330eProjects(a) {
				proj := t.TempDir()
				if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(yml), 0o644); err != nil {
					t.Fatal(err)
				}
				got := sec330eEval(t, dir, a, proj)
				cases++
				// 1. The agent's own budget never loosens.
				if v := sec330eLooser(base, got); len(v) > 0 {
					fails = append(fails, fmt.Sprintf("P%d %s %q: own %s -> %s: %s", pi, a, name, base, got, strings.Join(v, ", ")))
				}
				// 2. A renamed supervisor never runs looser than the supervisor.
				if len(readProjectConfig(proj).supervisor) > 0 && readProjectConfig(proj).aliasFor(a) != "" {
					sup := sec330eEval(t, dir, "supervisor", proj)
					if v := sec330eLooser(sup, got); len(v) > 0 {
						fails = append(fails, fmt.Sprintf("P%d %s %q: vs supervisor %s -> %s: %s", pi, a, name, sup, got, strings.Join(v, ", ")))
					}
				}
			}
		}
	}
	t.Logf("%d cases, %d loosenings", cases, len(fails))
	for _, f := range fails {
		t.Errorf("a project file loosened a budget: %s", f)
	}
}
