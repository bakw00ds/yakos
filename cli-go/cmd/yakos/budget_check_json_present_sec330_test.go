package main

// Probe for the PR #330 security review (final round): `budget check --json`, the
// line the bash supervisor hook reads, is always one parseable JSON object with
// numeric amounts, whatever the trusted policy and the project file say. If it is
// missing, the bash gate fails open.
//
// Adopted from sec-330's probe with the one change the final spec asks for (item 8):
// the 63 combinations run in process through budgetCheck, the function the CLI's main
// calls, reading stdout alone as the hook does, and two real-binary cases keep the argv
// and exit-code contract. The 63 binary runs took about 5 s here and about 30 s on the
// macOS runner, where this package has no time to spare.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

// firstJSONObject returns the first line of out that is a JSON object, decoded, and whether
// there was one.
func firstJSONObject(out string) (map[string]any, bool) {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") {
			var obj map[string]any
			if json.Unmarshal([]byte(l), &obj) == nil {
				return obj, true
			}
			return nil, false
		}
	}
	return nil, false
}

// usableStatusLine reports whether obj is what the bash gate needs: a numeric limit_usd and a
// string state.
func usableStatusLine(obj map[string]any) bool {
	_, numOK := obj["limit_usd"].(float64)
	_, stOK := obj["state"].(string)
	return numOK && stOK
}

func TestSec330g_BudgetCheckJSONAlwaysPresent(t *testing.T) {
	policies := []string{
		"",
		"agents:\n  supervisor:\n    limit_usd: 1e308\n",
		"agents:\n  supervisor:\n    limit_usd: .inf\n",
		"agents:\n  supervisor:\n    limit_tokens: 99999999999999999\n",
		"agents:\n  librarian:\n    limit_usd: 0\n    limit_tokens: 0\n",
		"default:\n  limit_usd: 1e308\n",
		"agents:\n  backend:\n    limit_tokens: 500000000\n",
	}
	projects := []string{"", "supervisor:\n  agent: backend\n", "supervisor:\n  agent: librarian\n"}
	agents := []string{"supervisor", "librarian", "backend"}
	missing := 0
	for pi, pol := range policies {
		state := t.TempDir()
		for _, a := range agents {
			writeTokenLog(t, state, a, 70_000_000, 0)
		}
		if pol != "" {
			if err := os.WriteFile(budget.PolicyPath(state), []byte(pol), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for ji, yml := range projects {
			proj := ""
			if yml != "" {
				proj = t.TempDir()
				if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(yml), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, a := range agents {
				var out, errb bytes.Buffer
				code := budgetCheck(&out, &errb, a, budget.Options{StateDir: state, Project: proj}, true)
				obj, found := firstJSONObject(out.String())
				if !found || !usableStatusLine(obj) {
					missing++
					t.Errorf("P%d J%d %s: exit %d, no usable JSON line on stdout (the bash gate would fail open): %q", pi, ji, a, code, out.String())
				}
			}
		}
	}
	t.Logf("calls without a usable JSON line: %d", missing)
}

// The same contract through the real binary's argv, for the two most hostile combinations: the
// line is on stdout or stderr-merged output as one object, and the exit is the hard stop's (4) or
// 0, never 2 (a hook's block code).
func TestSec330g_BudgetCheckJSONThroughTheBinary(t *testing.T) {
	for name, c := range map[string]struct {
		policy, project, agent string
	}{
		"an absurd dollar limit, a project naming backend": {"agents:\n  supervisor:\n    limit_usd: 1e308\n", "supervisor:\n  agent: backend\n", "backend"},
		"an infinite dollar limit on the supervisor":       {"agents:\n  supervisor:\n    limit_usd: .inf\n", "", "supervisor"},
	} {
		t.Run(name, func(t *testing.T) {
			state := t.TempDir()
			writeTokenLog(t, state, c.agent, 70_000_000, 0)
			if err := os.WriteFile(budget.PolicyPath(state), []byte(c.policy), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"budget", "check", c.agent, "--json"}
			if c.project != "" {
				proj := t.TempDir()
				if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(c.project), 0o644); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--project", proj)
			}
			code, out := runYakos(t, state, nil, args...)
			if code != 0 && code != budget.ExitHardStop {
				t.Errorf("exit %d, want 0 or the hard stop's %d, never 2: %q", code, budget.ExitHardStop, out)
			}
			if obj, found := firstJSONObject(out); !found || !usableStatusLine(obj) {
				t.Errorf("no usable JSON line (the bash gate would fail open): %q", out)
			}
		})
	}
}
