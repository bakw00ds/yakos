package main

// Probe for the PR #330 security review (final round): `budget check --json`, the
// line the bash supervisor hook reads, is always one parseable JSON object with
// numeric amounts, whatever the trusted policy and the project file say. If it is
// missing, the bash gate fails open.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
)

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
	missing := 0
	for pi, pol := range policies {
		state := t.TempDir()
		for _, a := range []string{"supervisor", "librarian", "backend"} {
			writeTokenLog(t, state, a, 70_000_000, 0)
		}
		if pol != "" {
			if err := os.WriteFile(budget.PolicyPath(state), []byte(pol), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for ji, yml := range projects {
			args := func(a string) []string { return []string{"budget", "check", a, "--json"} }
			proj := ""
			if yml != "" {
				proj = t.TempDir()
				if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(yml), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for _, a := range []string{"supervisor", "librarian", "backend"} {
				av := args(a)
				if proj != "" {
					av = append(av, "--project", proj)
				}
				code, out := runYakos(t, state, nil, av...)
				var obj map[string]any
				found := false
				for _, l := range strings.Split(out, "\n") {
					if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") {
						if json.Unmarshal([]byte(l), &obj) == nil {
							found = true
						}
						break
					}
				}
				_, numOK := obj["limit_usd"].(float64)
				_, stOK := obj["state"].(string)
				if !found || !numOK || !stOK {
					missing++
					t.Errorf("P%d J%d %s: exit %d, no usable JSON line (the bash gate would fail open): %q", pi, ji, a, code, out)
				}
			}
		}
	}
	t.Logf("calls without a usable JSON line: %d", missing)
}
