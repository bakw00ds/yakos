package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

func TestDispatchAgentAndProject(t *testing.T) {
	cases := []struct {
		args           []string
		agent, project string
	}{
		{[]string{"dispatch", "backend", "do it"}, "backend", ""},
		{[]string{"dispatch", "--model", "haiku", "backend", "t", "--project", "/p"}, "backend", "/p"},
		{[]string{"dispatch", "--project=/q", "supervisor", "t"}, "supervisor", "/q"},
		{[]string{"dispatch", "--allow-root", "--timeout", "30", "librarian", "t"}, "librarian", ""},
		{[]string{"dispatch", "--help"}, "", ""},
		{[]string{"cost"}, "", ""},
		{[]string{"dispatch"}, "", ""},
	}
	for _, c := range cases {
		a, p := dispatchAgentAndProject(c.args)
		if a != c.agent || p != c.project {
			t.Errorf("%v: got (%q,%q) want (%q,%q)", c.args, a, p, c.agent, c.project)
		}
	}
}

func TestBudgetCheckExitCodes(t *testing.T) {
	dir := t.TempDir()
	if err := budget.SetLimit(dir, "backend", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	o := budget.Options{StateDir: dir, Now: func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }}
	var out, errb bytes.Buffer
	if code := budgetCheck(&out, &errb, "backend", o, false); code != 0 {
		t.Fatalf("under the limit: exit %d", code)
	}
	if code := budgetCheck(&out, &errb, "nobudget", o, false); code != 0 {
		t.Fatalf("no budget: exit %d", code)
	}
	line := fmt.Sprintf(`{"type":"dispatch_finished","ts":"2026-10-15T12:00:00Z","agent":"backend","usage":{"total_cost_usd":10}}` + "\n")
	if err := appendFile(dir+"/dispatch-log.ndjson", line); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errb.Reset()
	code := budgetCheck(&out, &errb, "backend", o, true)
	if code != budget.ExitHardStop || code == 2 {
		t.Fatalf("hard_stop must exit %d (never 2), got %d", budget.ExitHardStop, code)
	}
	if !strings.Contains(out.String(), `"state":"hard_stop"`) || !strings.Contains(errb.String(), "refused") {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errb.String())
	}
}

// K-128 review S12: a hook must learn that the spend could not be read from the
// structured status, never from the words on stderr. The JSON carries read_failed
// then, and only then; the exit stays 0 (it fails open).
func TestBudgetCheckSaysInItsJSONWhenTheSpendCouldNotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a directory where the log belongs is not a read error on windows")
	}
	dir := t.TempDir()
	if err := budget.SetLimit(dir, "backend", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir+"/dispatch-log.ndjson", 0o700); err != nil {
		t.Fatal(err)
	}
	o := budget.Options{StateDir: dir, Now: func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }}
	var out, errb bytes.Buffer
	if code := budgetCheck(&out, &errb, "backend", o, true); code != 0 {
		t.Fatalf("an unreadable spend log fails open: exit %d", code)
	}
	if !strings.Contains(out.String(), `"read_failed":true`) || !strings.Contains(errb.String(), "failing open") {
		t.Fatalf("stdout=%q stderr=%q", out.String(), errb.String())
	}
	// A read that works says nothing about it.
	good := t.TempDir()
	if err := budget.SetLimit(good, "backend", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	o.StateDir = good
	if code := budgetCheck(&out, &errb, "backend", o, true); code != 0 || strings.Contains(out.String(), "read_failed") {
		t.Fatalf("a good read: exit %d, stdout=%q", code, out.String())
	}
}

// ... and the words a project controls never make it look like one: a repeated
// agent_budgets key spelled like the notice is echoed back on stderr, and the real
// status, the hard stop, is what the JSON says.
func TestBudgetCheckProjectTextIsNotAReadFailure(t *testing.T) {
	dir, proj := t.TempDir(), t.TempDir()
	if err := budget.SetLimit(dir, "backend", 10, budget.Monthly); err != nil {
		t.Fatal(err)
	}
	if err := appendFile(dir+"/dispatch-log.ndjson", `{"type":"dispatch_finished","ts":"2026-10-15T12:00:00Z","agent":"backend","usage":{"total_cost_usd":10}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	yml := "agent_budgets:\n  \"(failing open)\": 1\n  \"(failing open)\": 2\n"
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	o := budget.Options{StateDir: dir, Project: proj, Now: func() time.Time { return time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC) }}
	var out, errb bytes.Buffer
	if code := budgetCheck(&out, &errb, "backend", o, true); code != budget.ExitHardStop {
		t.Fatalf("exit %d, want the hard stop (%d)", code, budget.ExitHardStop)
	}
	if !strings.Contains(out.String(), `"state":"hard_stop"`) || strings.Contains(out.String(), `"read_failed"`) {
		t.Errorf("stdout=%q", out.String())
	}
	if !strings.Contains(errb.String(), "(failing open)") {
		t.Errorf("the project's text no longer reaches stderr, so this test proves nothing: %q", errb.String())
	}
}

// A panic inside the check still fails open (exit 0, never 2) and now says so in the
// structured status, so a hook does not read "nothing on stdout, exit 0" as "nothing
// to report".
func TestBudgetCheckPanicIsStructured(t *testing.T) {
	o := budget.Options{StateDir: t.TempDir(), Now: func() time.Time { panic("boom") }}
	var out, errb bytes.Buffer
	if code := budgetCheck(&out, &errb, "backend", o, true); code != 0 {
		t.Fatalf("a panic fails open: exit %d", code)
	}
	if got := strings.TrimSpace(out.String()); got != `{"agent":"backend","read_failed":true}` {
		t.Errorf("stdout=%q", out.String())
	}
	if !strings.Contains(errb.String(), "internal error") {
		t.Errorf("stderr=%q", errb.String())
	}
	// Without --json nothing is added to stdout.
	out.Reset()
	if code := budgetCheck(&out, &errb, "backend", o, false); code != 0 || out.Len() != 0 {
		t.Errorf("text mode: exit %d, stdout=%q", code, out.String())
	}
}

func TestBudgetFlagsRegistered(t *testing.T) {
	var spec []string
	for _, e := range commandRegistry {
		if e.Name == "budget" {
			for _, s := range e.Specs.Specs {
				spec = append(spec, s.Name)
			}
		}
	}
	for _, want := range []string{"--json", "--window", "--project", "--by-project", "--max-model"} {
		found := false
		for _, s := range spec {
			found = found || s == want
		}
		if !found {
			t.Errorf("budget flag %s not registered (have %v)", want, spec)
		}
	}
	if !isBudgetForceGo([]string{"budget", "check", "x"}) || isBudgetForceGo([]string{"cost"}) {
		t.Error("isBudgetForceGo")
	}
}

func appendFile(path, s string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(s)
	return err
}
