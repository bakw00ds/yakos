package doctor

// budgets_refused_project_test.go: K-164 (sec-360 L2). A project .yakos.yml that
// the shared reader refuses (a symlink, over the cap) is read as absent, so its
// limits are off. Doctor says so, in fixed text with no path, and the production
// checklist no longer hangs on a FIFO.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/projfile"
)

func refusedBudgetProject(t *testing.T, kind string) string {
	t.Helper()
	dir := t.TempDir()
	switch kind {
	case "symlink":
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privileges on Windows")
		}
		real := budgetProjectDir(t, "agent_budgets:\n  backend: 5\n")
		if err := os.Symlink(filepath.Join(real, ".yakos.yml"), filepath.Join(dir, ".yakos.yml")); err != nil {
			t.Fatal(err)
		}
	case "oversized":
		body := "agent_budgets:\n  backend: 5\n#" + strings.Repeat("x", projfile.MaxBytes)
		if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestAgentBudgetsSaysWhenTheProjectFileWasRefused(t *testing.T) {
	for _, kind := range []string{"symlink", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			proj := refusedBudgetProject(t, kind)
			out, rep := budgetsSection(t, makeTmpHome(t), Config{BudgetProject: proj})
			if !strings.Contains(out, ".yakos.yml was not read") || rep.Warnings != 1 {
				t.Fatalf("no refusal finding (warnings %d):\n%s", rep.Warnings, out)
			}
			for _, want := range []string{"every project setting in it is ignored", "budget, models, injection_scan, decisions, supervisor-gate"} {
				if !strings.Contains(out, want) {
					t.Fatalf("the finding does not say all project settings are ignored (%q missing):\n%s", want, out)
				}
			}
			if strings.Contains(out, proj) || strings.Contains(out, "backend") {
				t.Fatalf("the finding names the path or the file's text:\n%s", out)
			}
		})
	}
	// A project with a normal file, or none, adds no line.
	for _, proj := range []string{budgetProjectDir(t, "agent_budgets:\n  backend: 5\n"), t.TempDir()} {
		if out, _ := budgetsSection(t, makeTmpHome(t), Config{BudgetProject: proj}); out != "" {
			t.Fatalf("unexpected output:\n%s", out)
		}
	}
}
