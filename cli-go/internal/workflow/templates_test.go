package workflow_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/workflow"
)

// Every shipped template loads and validates, uses the router for its nodes,
// and any cron it declares parses.
func TestShippedTemplatesValidate(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "lib", "workflows", "templates")
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil || len(files) < 2 {
		t.Fatalf("templates in %s: %v, %v", dir, files, err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		wf, err := workflow.Load(f)
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		if err := workflow.Validate(wf); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		if want := strings.TrimSuffix(filepath.Base(f), ".yaml"); wf.Name != want {
			t.Errorf("%s: name %q does not match the file", f, wf.Name)
		}
		for _, n := range wf.Nodes {
			if n.Runtime != workflow.Auto {
				t.Errorf("%s node %s: runtime %q, want auto", f, n.ID, n.Runtime)
			}
		}
		seen[wf.Name] = true
		data, _ := os.ReadFile(f)
		if !strings.HasPrefix(string(data), "# ") {
			t.Errorf("%s: first line must be a '# description' comment", f)
		}
	}
	for _, want := range []string{"pr-review-multi-model", "nightly-review"} {
		if !seen[want] {
			t.Errorf("template %s missing", want)
		}
	}
}

func TestTriggerValidation(t *testing.T) {
	base := "version: 1\nname: x\nnodes:\n  - id: a\n    agent: r\n    prompt: p\n    output_limit: 10\n"
	for tr, ok := range map[string]bool{
		"triggers:\n  cron: \"*/5 * * * *\"\n":                  true,
		"triggers:\n  cron: \"nonsense\"\n":                     false,
		"triggers:\n  webhook:\n    secret_env: YAKOS_MY_SECRET\n":    true,
		"triggers:\n  webhook:\n    secret_env: lower\n":        false,
		"triggers:\n  webhook:\n    secret_env: \"A;rm -rf\"\n": false,
		"triggers:\n  webhook: {}\n":                            false,
	} {
		p := filepath.Join(t.TempDir(), "w.yaml")
		_ = os.WriteFile(p, []byte(base+tr), 0o600)
		wf, err := workflow.Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := workflow.Validate(wf) == nil; got != ok {
			t.Errorf("%q: valid=%v, want %v", tr, got, ok)
		}
	}
}
