package main

// cmd_hook_degraded_test.go — K-100 item 5: the `yakos hook run` entrypoint's
// posture on undecodable stdin must follow the registry's FailClosed flag, and
// that flag must agree with the bash hooks' HOOK_FAIL_CLOSED=1 declaration.
//
// Accepted divergence (pinned in tests/run-hook-parity.sh): after the WARN, bash
// continues into a non-blocking hook's body with EMPTY input, which for four
// hooks (plan-outcome-capture, session-end-check, task-complete-dispatch,
// task-dependency-gate) appends a log or telemetry record built from empty fields. Go exits 0
// straight after the WARN. Neither blocks, neither writes stdout, and both
// print the identical WARN line.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/registry"
)

var failClosedDecl = regexp.MustCompile(`(?m)^HOOK_FAIL_CLOSED=1\b`)

// Every hook that declares HOOK_FAIL_CLOSED=1 in bash must be FailClosed in the
// registry, and every FailClosed registry entry must be declared in bash — in
// both directions, so neither side can silently weaken a gate.
func TestRegistryFailClosedMatchesBashDeclaration(t *testing.T) {
	hooksDir := filepath.Join("..", "..", "..", "lib", "hooks")
	seen := map[string]bool{}
	for _, e := range registry.All() {
		data, err := os.ReadFile(filepath.Join(hooksDir, e.Name+".sh")) //nolint:gosec
		if err != nil {
			if os.IsNotExist(err) {
				continue // Go-only hook, no bash twin
			}
			t.Fatal(err)
		}
		seen[e.Name] = true
		if bash := failClosedDecl.Match(data); bash != e.FailClosed {
			t.Errorf("%s: bash HOOK_FAIL_CLOSED=1 is %v but registry FailClosed is %v", e.Name, bash, e.FailClosed)
		}
	}
	// Any bash hook that declares fail-closed but is missing from the registry.
	files, _ := filepath.Glob(filepath.Join(hooksDir, "*.sh"))
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".sh")
		data, _ := os.ReadFile(f) //nolint:gosec
		if failClosedDecl.Match(data) && !seen[name] {
			t.Errorf("%s declares HOOK_FAIL_CLOSED=1 in bash but has no registry entry", name)
		}
	}
}

func TestHookRunDegradedStdinFollowsRegistryFlag(t *testing.T) {
	bin := hooksImplBinary(t)
	stdins := map[string]string{
		"garbage":    "not json{",
		"empty":      "",
		"array":      `[1,2]`,
		"bare-null":  `null`,
		"bare-quote": `"x"`,
	}
	for _, e := range registry.All() {
		for label, stdin := range stdins {
			t.Run(e.Name+"/"+label, func(t *testing.T) {
				home := t.TempDir()
				proj := t.TempDir()
				work := filepath.Join(proj, "work", "current")
				if err := os.MkdirAll(filepath.Join(work, "logs"), 0o755); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(bin, "hook", "run", e.Name) //nolint:gosec
				cmd.Env = hooksImplEnv(home, work, proj)
				cmd.Stdin = strings.NewReader(stdin)
				var so, se bytes.Buffer
				cmd.Stdout, cmd.Stderr = &so, &se
				code := 0
				if err := cmd.Run(); err != nil {
					ee, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatal(err)
					}
					code = ee.ExitCode()
				}
				if e.FailClosed {
					if code != 2 || !strings.Contains(se.String(), e.Name+": BLOCKED") {
						t.Fatalf("fail-closed hook: exit=%d stderr=%q, want exit 2 + BLOCKED", code, se.String())
					}
					return
				}
				if code != 0 || so.Len() != 0 || !strings.HasPrefix(se.String(), e.Name+": WARN") {
					t.Fatalf("non-blocking hook: exit=%d stdout=%q stderr=%q, want exit 0, no stdout, WARN", code, so.String(), se.String())
				}
			})
		}
	}
}
