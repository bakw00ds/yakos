package main

import (
	"bytes"
	"fmt"
	"os"
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
