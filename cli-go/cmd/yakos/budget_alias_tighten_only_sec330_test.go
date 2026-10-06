package main

// Probe from the PR #330 security review (final round): the `budget check --json`
// output the bash supervisor hook reads is never loosened by a project that names
// the agent as its supervisor.
//
// Adopted from sec-330's probe with the one change the final spec asks for (item 8): the
// 42 checks run in process through budgetCheck, the function the CLI's main calls,
// reading stdout alone as the hook does, and two real-binary cases keep the argv contract
// (--project) and the exit-code contract (4 at the combined stop). The 42 binary runs took
// about 2.4 s here and about 30 s on the macOS runner, where this package has no time to spare.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// sec330eParse decodes the first JSON line of out.
func sec330eParse(t *testing.T, out string) sec330eCLI {
	t.Helper()
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

// sec330eCheck is `budget check <agent> --json [--project proj]` in process: stdout alone, as
// the bash hook reads it.
func sec330eCheck(t *testing.T, state, agent, proj string) sec330eCLI {
	t.Helper()
	var out, errb bytes.Buffer
	budgetCheck(&out, &errb, agent, budget.Options{StateDir: state, Project: proj}, true)
	return sec330eParse(t, out.String())
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

// The argv contract, through the real binary: --project is read, and the line the binary prints is
// the tuple the in-process check gives for the same inputs, which is the operator's $5 and its stop
// of $5, not the supervisor's $100 with a stop of $200.
func TestSec330e_CLIThroughTheBinaryReadsTheProject(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(budget.PolicyPath(state), []byte("default:\n  limit_usd: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: backend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out := runYakos(t, state, nil, "budget", "check", "backend", "--json", "--project", proj)
	got := sec330eParse(t, out)
	if want := sec330eCheck(t, state, "backend", proj); got != want {
		t.Errorf("the binary printed %s, the in-process check %s", got, want)
	}
	if got.LimitUSD != 5 || got.StopUSD != 5 {
		t.Errorf("naming the agent as the supervisor must not raise the operator's $5 and its stop: %s", got)
	}
}

// The exit-code contract, through the real binary: past the combined stop the check exits 4, never
// 2, and still prints the status line.
func TestSec330e_CLIExitsFourAtTheCombinedStop(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(budget.PolicyPath(state), []byte("agents:\n  backend:\n    limit_usd: 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":%q,"agent":"backend","runtime":"claude","billing":"api","usage":{"input_tokens":10,"output_tokens":0,"total_cost_usd":6}}`+"\n", time.Now().UTC().Format(time.RFC3339))
	if err := appendFile(filepath.Join(state, "dispatch-log.ndjson"), line); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("supervisor:\n  agent: backend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := runYakos(t, state, nil, "budget", "check", "backend", "--json", "--project", proj)
	if code != budget.ExitHardStop {
		t.Fatalf("exit %d, want the hard stop's %d (never 2): %s", code, budget.ExitHardStop, out)
	}
	if got := sec330eParse(t, out); got.LimitUSD != 5 || got.StopUSD != 5 {
		t.Errorf("the status line past the stop: %s", got)
	}
}
