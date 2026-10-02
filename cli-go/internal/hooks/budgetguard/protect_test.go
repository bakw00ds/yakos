package budgetguard_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/budgetguard"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

func protectInput(tool string, ti map[string]any) hooktype.HookInput {
	return hooktype.HookInput{Tool: tool, Payload: map[string]any{"session_id": "s", "tool_input": ti}, Env: map[string]string{}}
}

func TestProtectsBudgetState(t *testing.T) {
	cases := []struct {
		name  string
		tool  string
		ti    map[string]any
		block bool
	}{
		{"set", "Bash", map[string]any{"command": "yakos budget set supervisor 9999"}, true},
		{"reset with path and env", "Bash", map[string]any{"command": "cd x && YAKOS_IMPL=go ./bin/yakos budget reset supervisor"}, true},
		{"set on a later line", "Bash", map[string]any{"command": "echo hi\nyakos budget set a 1"}, true},
		{"status is fine", "Bash", map[string]any{"command": "yakos budget status --json"}, false},
		{"check is fine", "Bash", map[string]any{"command": "yakos budget check supervisor"}, false},
		{"sed on policy", "Bash", map[string]any{"command": "sed -i s/100/9999/ ~/.yakos-state/budget-policy.yml"}, true},
		{"redirect into resets", "Bash", map[string]any{"command": "echo {} > ~/.yakos-state/budget-resets.json"}, true},
		{"rm lock", "Bash", map[string]any{"command": "rm -f ~/.yakos-state/budget.lock"}, true},
		{"cat is fine", "Bash", map[string]any{"command": "cat ~/.yakos-state/budget-spend.json"}, false},
		{"cat piped to tee is not exempt", "Bash", map[string]any{"command": "cat x | tee ~/.yakos-state/budget-policy.yml"}, true},
		{"multi-line cat then rm", "Bash", map[string]any{"command": "cat ~/.yakos-state/budget-policy.yml\nrm ~/.yakos-state/budget-policy.yml"}, true},
		{"write policy", "Write", map[string]any{"file_path": "/home/u/.yakos-state/budget-policy.yml"}, true},
		{"edit spend", "Edit", map[string]any{"file_path": "/home/u/.yakos-state/budget-spend.json"}, true},
		{"write other file", "Write", map[string]any{"file_path": "/home/u/notes/budget.md"}, false},
		{"read tool untouched", "Read", map[string]any{"file_path": "/home/u/.yakos-state/budget-policy.yml"}, false},
		{"quoted subcommand", "Bash", map[string]any{"command": `yakos budget "set" supervisor 1`}, true},
		{"single-quoted reset", "Bash", map[string]any{"command": `yakos budget 'reset' supervisor`}, true},
		{"backslash in reset", "Bash", map[string]any{"command": `yakos budget re\set supervisor`}, true},
		{"quoted yakos", "Bash", map[string]any{"command": `"yakos" budget set a 1`}, true},
		{"truncate dispatch log", "Bash", map[string]any{"command": ": > ~/.yakos-state/dispatch-log.ndjson"}, true},
		{"glob dispatch logs", "Bash", map[string]any{"command": "rm ~/.yakos-state/dispatch-log*.ndjson"}, true},
		{"quoted dispatch log redirect", "Bash", map[string]any{"command": `echo > "$HOME/.yakos-state/dispatch-log-2026.ndjson"`}, true},
		{"grep dispatch log is fine", "Bash", map[string]any{"command": "grep supervisor ~/.yakos-state/dispatch-log.ndjson"}, false},
		{"edit dispatch log", "Edit", map[string]any{"file_path": "/home/u/.yakos-state/dispatch-log.ndjson"}, true},
		{"write rotated dispatch log", "Write", map[string]any{"file_path": "/home/u/.yakos-state/dispatch-log-2026-09.ndjson"}, true},
		{"unrelated bash", "Bash", map[string]any{"command": "ls -la"}, false},
	}
	for _, c := range cases {
		// No .yakos.yml and no work dir: the rule must not depend on config.
		h := &budgetguard.Hook{}
		out, err := h.Run(context.Background(), protectInput(c.tool, c.ti))
		if err != nil {
			t.Fatal(err)
		}
		if (out.ExitCode == 2) != c.block {
			t.Errorf("%s: exit %d, want block=%v", c.name, out.ExitCode, c.block)
		}
		if c.block && !strings.Contains(string(out.Stderr), "operator control") {
			t.Errorf("%s: stderr %q", c.name, out.Stderr)
		}
	}
}

func TestProtectionIgnoresBypassAndKeepsEmergencyDisable(t *testing.T) {
	work := t.TempDir()
	h := &budgetguard.Hook{WorkCurrentDir: work}
	// A hook-bypass.md entry an agent could write must not unlock it.
	_ = writeFile(work+"/hook-bypass.md", "## bypass:x\n\n**Hook:** budget\n**Scope:** *\n**Reason:** agent wrote this\n")
	in := protectInput("Bash", map[string]any{"command": "yakos budget reset supervisor"})
	if out, _ := h.Run(context.Background(), in); out.ExitCode != 2 {
		t.Fatalf("bypass file must not unlock the rule, exit %d", out.ExitCode)
	}
	in.Env["YAKOS_BUDGET_DISABLE"] = "1"
	if out, _ := h.Run(context.Background(), in); out.ExitCode != 0 {
		t.Fatalf("the operator's emergency env still disables the guard, exit %d", out.ExitCode)
	}
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o644) }
