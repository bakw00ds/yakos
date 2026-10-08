package main

// budget_set_audit_test.go: `yakos budget set` records a config_changed line like
// the models and router writers do (K-175; it wrote none before). The subprocess
// runs against a scratch HOME, so the policy and the log are the test's own.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// runInHome runs the router with args against home. YAKOS_DISPATCH_LOG points at a
// different directory: the audit line must ignore it.
func runInHome(t *testing.T, home, override string, args ...string) (int, string) {
	t.Helper()
	b, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetHelperMain$")
	cmd.Env = []string{
		"YAKOS_TEST_MAIN_ARGS=" + string(b), "YAKOS_DISPATCH_LOG=" + override,
		"HOME=" + home, "USERPROFILE=" + home, "PATH=" + t.TempDir(),
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String()
}

func configLines(t *testing.T, state string) []map[string]any {
	t.Helper()
	b, _ := os.ReadFile(statepath.DispatchLogIn(state))
	var out []map[string]any
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.Contains(ln, `"config_changed"`) {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("line %q: %v", ln, err)
		}
		out = append(out, m)
	}
	return out
}

func fileSHA(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestBudgetSetWritesAConfigChangedLine(t *testing.T) {
	home, override := t.TempDir(), t.TempDir()
	state := filepath.Join(home, ".yakos-state") // where the audit goes
	policy := budget.PolicyPath(override)        // where YAKOS_DISPATCH_LOG sends the policy

	if code, out := runInHome(t, home, override, "budget", "set", "backend", "12"); code != 0 {
		t.Fatalf("set: %d %s", code, out)
	}
	lines := configLines(t, state)
	if len(lines) != 1 {
		t.Fatalf("%d config_changed lines, want 1: %v", len(lines), lines)
	}
	l := lines[0]
	if l["file"] != "budget-policy.yml" || l["action"] != "budget.set" || l["surface"] != "cli" ||
		l["policy_sha_before"] != "" || l["policy_sha_after"] != fileSHA(t, policy) || l["operator_id"] == "" {
		t.Errorf("line = %v", l)
	}
	// The override directory (a project can set it) gets the policy but no audit line.
	entries, _ := os.ReadDir(override)
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(override, e.Name()))
		if strings.Contains(string(b), "config_changed") {
			t.Errorf("audit line in the override directory: %s", e.Name())
		}
	}

	// A second change cites the first one's sha; a repeat of the same value
	// changes nothing and records nothing.
	first := fileSHA(t, policy)
	if code, out := runInHome(t, home, override, "budget", "set", "backend", "13", "--tokens", "5000"); code != 0 {
		t.Fatalf("second set: %d %s", code, out)
	}
	lines = configLines(t, state)
	if len(lines) != 2 || lines[1]["policy_sha_before"] != first || lines[1]["policy_sha_after"] != fileSHA(t, policy) {
		t.Fatalf("second line: %v", lines)
	}
	if code, out := runInHome(t, home, override, "budget", "set", "backend", "13", "--tokens", "5000"); code != 0 {
		t.Fatalf("repeat: %d %s", code, out)
	}
	if n := len(configLines(t, state)); n != 2 {
		t.Errorf("an unchanged set wrote a line: %d lines", n)
	}
	// Reading writes no line.
	if code, out := runInHome(t, home, override, "budget", "status"); code != 0 {
		t.Fatalf("status: %d %s", code, out)
	}
	if n := len(configLines(t, state)); n != 2 {
		t.Errorf("status wrote a line: %d lines", n)
	}
}

// A change that cannot be recorded is not made.
func TestBudgetSetRefusedWhenTheLogCannotBeOpened(t *testing.T) {
	home, override := t.TempDir(), t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(statepath.DispatchLogIn(state), 0o700); err != nil { // a directory where the log should be
		t.Fatal(err)
	}
	code, out := runInHome(t, home, override, "budget", "set", "backend", "12")
	if code != 1 || !strings.Contains(out, "so the change was not made") {
		t.Errorf("set with an unopenable log = %d %q", code, out)
	}
	if _, err := os.Stat(budget.PolicyPath(override)); err == nil {
		t.Error("the policy was written although the change could not be recorded")
	}
}

// A set with a bad part writes none of it: the dollar limit does not land when
// the --max-model beside it is refused, and nothing is recorded.
func TestBudgetSetWithABadPartWritesNothing(t *testing.T) {
	home, override := t.TempDir(), t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	code, _ := runInHome(t, home, override, "budget", "set", "backend", "12", "--max-model", "no-such-tier")
	if code == 0 {
		t.Fatal("a bad --max-model was accepted")
	}
	if _, err := os.Stat(budget.PolicyPath(override)); err == nil {
		t.Error("the dollar limit was written although the ceiling was refused")
	}
	if n := len(configLines(t, state)); n != 0 {
		t.Errorf("%d audit lines for a refused set", n)
	}
}

// A bad request is refused for its own reason before the audit log is touched:
// the floor test must name the floor even when the log cannot be opened.
func TestBudgetSetValidatesBeforeOpeningTheLog(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(statepath.DispatchLogIn(state), 0o700); err != nil { // unopenable log
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"budget", "set", "backend", "0.009"},
		{"budget", "set", "backend", "--tokens", "-5"},
		{"budget", "set", "backend", "5", "--window", "weekly"},
		{"budget", "set", "backend", "5", "--max-model", "no-such-tier"},
	} {
		code, out := runInHome(t, home, t.TempDir(), args...)
		if code != 1 || strings.Contains(out, "dispatch log") || !strings.Contains(out, "budget") {
			t.Errorf("%v = %d %q; want the validation error, not the log's", args, code, out)
		}
	}
}

// A fresh scratch state: no .yakos-state directory and no log yet. The audit
// opens (creating both), the write lands and one line is recorded.
func TestBudgetSetAuditWorksOnAFreshStateDir(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, ".yakos-state")
	if _, err := os.Stat(state); err == nil {
		t.Fatal("fixture is not fresh")
	}
	override := t.TempDir()
	if code, out := runInHome(t, home, override, "budget", "set", "backend", "--tokens", "5m"); code != 0 {
		t.Fatalf("set on a fresh state dir: %d %s", code, out)
	}
	if n := len(configLines(t, state)); n != 1 {
		t.Errorf("%d config_changed lines on a fresh state dir, want 1", n)
	}
}
