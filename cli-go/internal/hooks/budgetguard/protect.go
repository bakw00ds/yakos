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
//
// Known evasions, pre-existing and shared by every phrase below (budget set
// included): variable indirection, $'..' quoting, a renamed symlink or glob
// path to the binary, base64 | sh, xargs, a function or alias wrapper, and
// $(...). The text match cannot see through any of them. A command that
// merely QUOTES a writer phrase (echo, grep, a commit message) is blocked
// too; put such text in a file with the Write tool. Only a backslash-newline
// continuation is joined before matching (K-176), as the shell itself does.
// The audit actor label of a write is advisory, not a boundary.
var (
	budgetCmdRE   = regexp.MustCompile(`yakos[[:space:]]+budget[[:space:]]+(set|reset)([[:space:]]|$)`)
	dispatchSupRE = regexp.MustCompile(`yakos[[:space:]]+dispatch[[:space:]]+(--?[A-Za-z-]+([[:space:]]+[^-[:space:]][^[:space:]]*)?[[:space:]]+)*supervisor([[:space:]]|$)`)
	// K-176: the K-153 trusted policy writers. The audit line of a write names
	// the OS user, so an agent's change would look like the operator's. Read
	// only models list|show|probe and router policy get|explain stay open.
	modelsWriteRE = regexp.MustCompile(`yakos[^[:space:]]*[[:space:]]+models[[:space:]]+(enable|disable|alias|pin|pricing)([[:space:]]|$)`)
	routerSetRE   = regexp.MustCompile(`yakos[^[:space:]]*[[:space:]]+router[[:space:]]+policy[[:space:]]+set([[:space:]]|$)`)
	// K-176 (sec-362 F1): enable pins a workflow sha and turns on cron and
	// webhook triggers, i.e. persistent unattended agent runs.
	flowsSchedRE  = regexp.MustCompile(`yakos[^[:space:]]*[[:space:]]+flows[[:space:]]+schedule[[:space:]]+(enable|disable)([[:space:]]|$)`)
	budgetFilesRE = regexp.MustCompile(`(?i)budget-(policy\.yml|spend\.json|resets\.json)|budget\.lock|dispatch-log[^[:space:]/]*\.ndjson|router-policy\.yml|model-registry\.yml`)
	budgetBaseRE  = regexp.MustCompile(`(?i)^(budget-(policy\.yml|spend\.json|resets\.json)|budget\.lock|dispatch-log[^[:space:]/]*\.ndjson)$`)
	// K-176: the two K-153 policy files, matched case-insensitively (a
	// case-insensitive filesystem opens a mixed-case name as the real file) in
	// Write/Edit base names and, via budgetFilesRE, in Bash text.
	policyBaseRE = regexp.MustCompile(`(?i)^(router-policy\.yml|model-registry\.yml)$`)
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
		// Backslash-newline continuations are joined first (the shell removes
		// both characters), so `yakos \<nl> models enable` is one line.
		cmd := normalizer.Replace(strings.ReplaceAll(hookio.ToolInputString(in, "command"), "\\\n", ""))
		if anyLine(budgetCmdRE, cmd) {
			return "yakos budget set|reset"
		}
		// The supervisor's dispatch stop is 2x its limit, so a same-user
		// `yakos dispatch supervisor` could spend between 1x and 2x. The hook
		// launches it itself (not through a tool call); agents may not.
		if anyLine(dispatchSupRE, cmd) {
			return "yakos dispatch supervisor"
		}
		if anyLine(modelsWriteRE, cmd) {
			return "yakos models enable|disable|alias|pin|pricing"
		}
		if anyLine(routerSetRE, cmd) {
			return "yakos router policy set"
		}
		if anyLine(flowsSchedRE, cmd) {
			return "yakos flows schedule enable|disable"
		}
		if anyLine(budgetFilesRE, cmd) {
			if !strings.Contains(strings.TrimRight(cmd, "\n"), "\n") && readOnlyRE.MatchString(strings.TrimRight(cmd, "\n")) {
				return ""
			}
			return "budget state file"
		}
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		base := filepath.Base(filepath.ToSlash(hookio.ToolFilePath(in)))
		if budgetBaseRE.MatchString(base) {
			return "budget state file"
		}
		if policyBaseRE.MatchString(base) {
			return "routing policy file"
		}
	}
	return ""
}
