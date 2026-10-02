package budgetguard

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

// K-119 F4: dollar budgets (yakos budget) are an operator control. An agent
// must not lift its own stop with `yakos budget set|reset` or by editing the
// budget state files. This twin mirrors the block at the top of
// lib/hooks/budget-guard.sh byte for byte in its decisions; there is
// deliberately no hook-bypass.md scope, because an agent can write that file.
// It is a speed bump, not a sandbox: same-user code can always evade it.
var (
	budgetCmdRE   = regexp.MustCompile(`yakos[[:space:]]+budget[[:space:]]+(set|reset)([[:space:]]|$)`)
	budgetFilesRE = regexp.MustCompile(`budget-(policy\.yml|spend\.json|resets\.json)|budget\.lock|dispatch-log[^[:space:]/]*\.ndjson`)
	budgetBaseRE  = regexp.MustCompile(`^(budget-(policy\.yml|spend\.json|resets\.json)|budget\.lock|dispatch-log[^[:space:]/]*\.ndjson)$`)
	// readOnlyRE is the only exemption: a single-line read command with no
	// shell metacharacters.
	readOnlyRE = regexp.MustCompile("^[[:space:]]*(cat|head|tail|less|more|ls|stat|wc|grep|jq|file)[[:space:]][^;&|><$`()]*$")
)

var normalizer = strings.NewReplacer(`"`, "", `'`, "", `\`, "")

// anyLine reports whether re matches at least one line of s (grep semantics).
func anyLine(re *regexp.Regexp, s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// protectedBudgetOp returns a non-empty match description when the tool call
// would change dollar budgets.
func protectedBudgetOp(in hooktype.HookInput) string {
	switch in.Tool {
	case "Bash":
		// Normalise: drop quotes and backslashes so `yakos budget "set"` and
		// `re\set` match (mirrors the tr in budget-guard.sh). Variable
		// indirection and $(...) are out of reach of text matching.
		cmd := normalizer.Replace(hookio.ToolInputString(in, "command"))
		if anyLine(budgetCmdRE, cmd) {
			return "yakos budget set|reset"
		}
		if anyLine(budgetFilesRE, cmd) {
			if !strings.Contains(strings.TrimRight(cmd, "\n"), "\n") && readOnlyRE.MatchString(strings.TrimRight(cmd, "\n")) {
				return ""
			}
			return "budget state file"
		}
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		if budgetBaseRE.MatchString(filepath.Base(filepath.ToSlash(hookio.ToolFilePath(in)))) {
			return "budget state file"
		}
	}
	return ""
}
