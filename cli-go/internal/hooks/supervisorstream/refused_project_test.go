package supervisorstream_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/projfile"
)

// sec-360 H1: a .yakos.yml the budget package refuses (a symlink, or over 1 MiB)
// is ABSENT to the hook too. If the hook still read the name `backend` from it, the
// supervisor would run as an agent with no built-in budget and the budget package,
// which sees no such name, would call it unlimited.
const renamesToBackend = "supervisor:\n  score_every_n_calls: 1\n  agent: backend\n  model: opus\n"

func refusedProject(t *testing.T, kind string) string {
	t.Helper()
	proj := t.TempDir()
	switch kind {
	case "oversized":
		body := renamesToBackend + "#" + strings.Repeat("x", projfile.MaxBytes)
		writeYAML(t, proj, body)
	case "symlink":
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on Windows")
		}
		real := t.TempDir()
		writeYAML(t, real, renamesToBackend)
		if err := os.Symlink(filepath.Join(real, ".yakos.yml"), filepath.Join(proj, ".yakos.yml")); err != nil {
			t.Fatal(err)
		}
	}
	return proj
}

func TestRefusedProjectFileCannotRenameTheSupervisor(t *testing.T) {
	for _, kind := range []string{"symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
			work, proj := t.TempDir(), refusedProject(t, kind)
			rec := &recorder{}
			h := newHook(work, proj)
			h.Launch = rec.launch
			env := map[string]string{"YAKOS_CLI": "/fake/yakos"}
			// The refused file cannot lower score_every_n_calls either: the default is 10.
			out := riskEdit(t, h, env)
			for i := 1; i < 10; i++ {
				riskEdit(t, h, env)
			}

			if len(rec.specs) != 1 {
				t.Fatalf("launches = %d, want 1", len(rec.specs))
			}
			args := rec.specs[0].Args
			if len(args) < 2 || args[0] != "dispatch" {
				t.Fatalf("args = %v", args)
			}
			agent := args[1]
			if agent != "supervisor" {
				t.Fatalf("the supervisor ran as %q: a refused file renamed it", agent)
			}
			// That name has a budget the budget package knows.
			st, err := budget.Evaluate(agent, budget.Options{StateDir: os.Getenv("YAKOS_DISPATCH_LOG"), Project: proj})
			if err != nil || st.State == budget.StateOff || st.LimitUSD != 100 {
				t.Fatalf("budget for the launched agent: %+v %v", st, err)
			}
			// The refusal is surfaced, without the project's path.
			e := string(out.Stderr)
			if !strings.Contains(e, ".yakos.yml ignored:") || strings.Contains(e, proj) {
				t.Fatalf("stderr = %q", e)
			}
		})
	}
}
