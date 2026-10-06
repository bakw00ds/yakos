package main

// Scratch probe for the PR #330 security review (final round): the `budget check
// --json` output the bash supervisor hook reads is never loosened by a project
// that names the agent as its supervisor. Not committed.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

type sec330eCLI struct {
	LimitUSD    float64 `json:"limit_usd"`
	StopUSD     float64 `json:"stop_usd"`
	LimitTokens int64   `json:"limit_tokens"`
	StopTokens  int64   `json:"stop_tokens"`
	Window      string  `json:"window"`
}

func (s sec330eCLI) String() string {
	return fmt.Sprintf("usd=%v/%v tok=%d/%d %s", s.LimitUSD, s.StopUSD, s.LimitTokens, s.StopTokens, s.Window)
}

func sec330eCheck(t *testing.T, state, agent, proj string) sec330eCLI {
	t.Helper()
	args := []string{"budget", "check", agent, "--json"}
	if proj != "" {
		args = append(args, "--project", proj)
	}
	_, out := runYakos(t, state, nil, args...)
	var s sec330eCLI
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") {
			if err := json.Unmarshal([]byte(l), &s); err != nil {
				t.Fatalf("bad JSON %q: %v", l, err)
			}
			return s
		}
	}
	t.Fatalf("no JSON in %q", out)
	return s
}

func sec330eCLILooser(base, got sec330eCLI) []string {
	var v []string
	if base.LimitUSD > 0 && (got.LimitUSD <= 0 || got.LimitUSD > base.LimitUSD+1e-9) {
		v = append(v, "usd limit raised")
	}
	if base.LimitUSD > 0 && (got.StopUSD <= 0 || got.StopUSD > base.StopUSD+1e-9) {
		v = append(v, "usd stop raised")
	}
	if base.LimitTokens > 0 && (got.LimitTokens <= 0 || got.LimitTokens > base.LimitTokens) {
		v = append(v, "token limit raised")
	}
	if base.LimitTokens > 0 && (got.StopTokens <= 0 || got.StopTokens > base.StopTokens) {
		v = append(v, "token stop raised")
	}
	if (base.LimitUSD > 0 || base.LimitTokens > 0) && base.Window == "lifetime" && got.Window != "lifetime" {
		v = append(v, "lifetime window turned "+got.Window)
	}
	return v
}

func TestSec330e_CLIBudgetCheckNeverLoosened(t *testing.T) {
	policies := []string{
		"",
		"default:\n  limit_usd: 5\n",
		"default:\n  limit_tokens: 1000000\n",
		"default:\n  window: lifetime\nagents:\n  supervisor:\n    window: monthly\n  backend:\n    limit_usd: 5\n",
		"agents:\n  backend:\n    limit_usd: 500\n",
		"agents:\n  librarian:\n    limit_tokens: 50000000\n",
	}
	agents := []string{"backend", "librarian", "watchdog"}
	fails := 0
	for pi, pol := range policies {
		state := t.TempDir()
		if pol != "" {
			if err := os.WriteFile(budget.PolicyPath(state), []byte(pol), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		sup := sec330eCheck(t, state, "supervisor", "")
		for _, a := range agents {
			base := sec330eCheck(t, state, a, "")
			proj := t.TempDir()
			if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: "+a+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			got := sec330eCheck(t, state, a, proj)
			own, vsSup := sec330eCLILooser(base, got), sec330eCLILooser(sup, got)
			if len(own)+len(vsSup) > 0 {
				fails++
				t.Errorf("P%d %s: `budget check --json` loosened by naming it supervisor: own %s, named %s, own:%v vs-sup:%v", pi, a, base, got, own, vsSup)
			}
			t.Logf("P%d %-9s own %-38s named %-38s own:%v vs-sup:%v", pi, a, base, got, own, vsSup)
		}
	}
	t.Logf("CLI cases with a loosening: %d of %d", fails, len(policies)*len(agents))
}
