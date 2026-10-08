package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/projectcfg"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// Exit-code contract of `yakos budget` (docs/budgets.md):
//
//	0  ok (also: warning, no budget, or a budget read failure, which fails open)
//	1  usage error or a failed set/reset
//	4  `budget check` only: the agent is in hard_stop
//
// It never exits 2: exit 2 is the Claude Code hook "block" code, and a budget
// refusal must never block a tool call (the supervisor hook stays fail-open).
func printBudgetHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos budget <status|set|reset|check> — per-agent token and dollar budgets with a hard stop

Subcommands:
    status [--json] [--by-project] [--project <path>]
                          One row per agent with a budget (user-level, built-in, or
                          a project agent_budgets: entry): state, tokens used and
                          their limit, then dollars spent and their limit.
                          --by-project adds each agent's spend per project.
    set <agent> [<usd>] [--tokens <n>] [--window monthly|lifetime] [--max-model haiku|sonnet|opus|fable]
                          Set an agent's limits in ~/.yakos-state/budget-policy.yml.
                          <usd> is a dollar limit; it counts only runs billed per API
                          call, never a subscription or a local model.
                          --tokens is a token limit: the input, output and cache tokens of
                          every run, whatever it is billed (5000000, 500k, 1.5m, 2b).
                          Give <usd>, --tokens, or both. 0 turns a limit off, including
                          a built-in default. The supervisor and librarian have a built-in
                          dollar limit AND a built-in token limit; their budget is off only
                          when both are 0.
                          --max-model also sets a model-tier ceiling for the agent
                          (a project cannot raise its cost with a dearer model).
    reset <agent> [--project <path>]
                          Start the agent's current window over. Spend already
                          logged stops counting; the dispatch-log is untouched.
    check <agent> [--project <path>] [--json]
                          Pre-flight for hooks and scripts. Exit 0 when the agent
                          may run, 4 when it is in hard_stop. Never exits 2.
                          First line is machine-readable:
                          reason=<budget_off|budget_ok|budget_warning|budget_exhausted> state=... agent=...

Flags:
    --json                Machine-readable output.
    --window <w>          monthly (calendar month, local time) or lifetime. Without
                          it, set keeps the agent's current window (its own entry's,
                          else the policy default's, else monthly): the dollar and
                          token limits share one window, so changing one never
                          changes the other's.
    --by-project          status: list spend per project under each agent.
    --tokens <n>          set: token limit (see set).
    --max-model <tier>    set: model-tier ceiling applied at dispatch.
    --project <path>      Project whose .yakos.yml agent_budgets: may LOWER a limit, and
                          whose supervisor: agent: name keeps the supervisor's budget.

States: ok, warning (default 80% of the limit), hard_stop (100% of either limit:
new dispatches are refused, exit 4; a run in flight is not killed). Agents have no
budget unless one is set, except supervisor ($100 and 33M tokens per month) and
librarian ($40 and 13M tokens per month).
See docs/budgets.md.
`)
}

func runBudget(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printBudgetHelp(os.Stdout)
		if len(args) == 0 {
			os.Exit(1)
		}
		return
	}
	sub, rest := args[0], args[1:]
	var (
		help      bool
		asJSON    bool
		byProject bool
		window    string // set: "" keeps the agent's current window
		maxModel  string
		project   string
		tokensArg string
	)
	specs := []cliflag.Spec{{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help}}
	switch sub {
	case "status":
		specs = append(specs, cliflag.Spec{Name: "--json", Kind: cliflag.Bool, Bool: &asJSON},
			cliflag.Spec{Name: "--by-project", Kind: cliflag.Bool, Bool: &byProject},
			cliflag.Spec{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"})
	case "set":
		specs = append(specs, cliflag.Spec{Name: "--window", Kind: cliflag.String, Str: &window, ValueDesc: "monthly or lifetime"},
			cliflag.Spec{Name: "--tokens", Kind: cliflag.String, Str: &tokensArg, ValueDesc: "a token count"},
			cliflag.Spec{Name: "--max-model", Kind: cliflag.String, Str: &maxModel, ValueDesc: "a model tier"})
	case "reset":
		specs = append(specs, cliflag.Spec{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"})
	case "check":
		specs = append(specs, cliflag.Spec{Name: "--json", Kind: cliflag.Bool, Bool: &asJSON},
			cliflag.Spec{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"})
	default:
		fmt.Fprintf(os.Stderr, "budget: unknown subcommand %q (status | set | reset | check)\n", sub)
		os.Exit(1)
	}
	fs := &cliflag.Set{Cmd: "budget " + sub, Specs: specs}
	pos, err := fs.Parse(rest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		printBudgetHelp(os.Stdout)
		return
	}
	for _, p := range pos {
		if strings.HasPrefix(p, "-") {
			fmt.Fprintf(os.Stderr, "budget %s: unknown flag %q\n", sub, p)
			os.Exit(1)
		}
	}
	opts := budget.Options{Project: project}
	switch sub {
	case "status":
		if len(pos) != 0 {
			fmt.Fprintln(os.Stderr, "budget status: unexpected argument")
			os.Exit(1)
		}
		if opts.Project == "" {
			opts.Project, _ = os.Getwd() // a project agent_budgets: entry shows in status
		}
		budgetStatus(os.Stdout, opts, asJSON, byProject)
	case "set":
		// <agent> [<usd>] [--tokens <n>]: at least one limit is required.
		if len(pos) < 1 || len(pos) > 2 || (len(pos) == 1 && tokensArg == "") {
			fmt.Fprintln(os.Stderr, "usage: yakos budget set <agent> [<usd>] [--tokens <n>] [--window monthly|lifetime]")
			os.Exit(1)
		}
		var (
			usd    float64
			tokens int64
		)
		if len(pos) == 2 {
			var err error
			usd, err = strconv.ParseFloat(strings.TrimPrefix(pos[1], "$"), 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "budget set: %q is not a dollar amount\n", pos[1])
				os.Exit(1)
			}
		}
		if tokensArg != "" {
			var err error
			tokens, err = parseTokenCount(tokensArg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "budget set: %v\n", err)
				os.Exit(1)
			}
		}
		if len(pos) == 2 {
			if err := budget.SetLimit(opts.StateDirOrDefault(), pos[0], usd, budget.Window(window)); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		if tokensArg != "" {
			if err := budget.SetTokenLimit(opts.StateDirOrDefault(), pos[0], tokens, budget.Window(window)); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		if maxModel != "" {
			if err := budget.SetMaxModel(opts.StateDirOrDefault(), pos[0], maxModel); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("model ceiling for %s set to %s\n", pos[0], maxModel)
		}
		// Say which window the limit now counts in: the one just given, or the
		// agent's current one that a set without --window kept.
		shownWindow := window
		if shownWindow == "" {
			pol, _ := budget.LoadPolicy(opts.StateDirOrDefault())
			shownWindow = string(budget.Resolve(pos[0], pol, nil).Window)
		}
		if len(pos) == 2 {
			if usd == 0 {
				fmt.Printf("dollar budget for %s turned off\n", pos[0])
			} else {
				fmt.Printf("budget for %s set to $%.2f (%s)\n", pos[0], usd, shownWindow)
			}
		}
		if tokensArg != "" {
			if tokens == 0 {
				fmt.Printf("token budget for %s turned off\n", pos[0])
			} else {
				fmt.Printf("token budget for %s set to %d tokens (%s)\n", pos[0], tokens, shownWindow)
			}
		}
		// A dollar limit turned off while a token limit remains (the supervisor and
		// librarian carry a built-in one) leaves the agent budgeted: say so, so
		// "set <agent> 0" is not mistaken for "no budget".
		if len(pos) == 2 && usd == 0 && tokensArg == "" {
			pol, _ := budget.LoadPolicy(opts.StateDirOrDefault())
			if lim := budget.Resolve(pos[0], pol, nil); lim.Tokens > 0 {
				fmt.Printf("note: %s still has a token budget of %d tokens (%s); turn it off too with: yakos budget set %s --tokens 0\n", pos[0], lim.Tokens, lim.Window, pos[0])
			}
		}
	case "reset":
		if len(pos) != 1 {
			fmt.Fprintln(os.Stderr, "usage: yakos budget reset <agent> [--project <path>]")
			os.Exit(1)
		}
		if opts.Project == "" {
			// As for status: the project in the working directory, whose
			// supervisor: agent: name keeps the supervisor's budget.
			opts.Project, _ = os.Getwd()
		}
		st, err := budget.Reset(pos[0], opts)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("budget window for %s restarted (%s)\n", pos[0], st.Message())
	case "check":
		if len(pos) != 1 {
			fmt.Fprintln(os.Stderr, "usage: yakos budget check <agent> [--project <path>] [--json]")
			os.Exit(1)
		}
		os.Exit(budgetCheck(os.Stdout, os.Stderr, pos[0], opts, asJSON))
	}
}

// budgetCheck is the hook-callable pre-flight. It returns the exit code and
// never 2, even on a panic.
func budgetCheck(stdout, stderr io.Writer, agent string, opts budget.Options, asJSON bool) (code int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "yakos budget check: internal error: %v (failing open)\n", r)
			// Say so in the structured status too: a hook must not read "nothing on
			// stdout, exit 0" as "nothing to report" (it never reads stderr).
			if asJSON {
				b, _ := json.Marshal(map[string]any{"agent": agent, "read_failed": true})
				fmt.Fprintln(stdout, string(b))
			}
			code = 0
		}
	}()
	st, err := budget.Evaluate(agent, opts)
	return reportCheck(stdout, stderr, agent, st, err, asJSON)
}

// reportCheck prints the answer of a budget check and returns its exit code: 0, or
// budget.ExitHardStop for a refused agent, never 2. It takes the evaluated status so
// that a status json cannot encode can be tested: Evaluate keeps every amount and stop
// finite (a limit above the bound is ignored, an off unit is 0), so no real status is
// one, but if one ever were, the line must still be a JSON object. A hook reads stdout
// only, and an empty line is what it takes for "no budget": the bash supervisor gate
// fails open on it, with no decision and no finding (sec-330, final round of #330).
func reportCheck(stdout, stderr io.Writer, agent string, st budget.Status, err error, asJSON bool) int {
	if err != nil {
		fmt.Fprintf(stderr, "yakos budget check: %v (failing open)\n", err)
	}
	for _, w := range st.Warnings {
		fmt.Fprintf(stderr, "yakos budget: %s\n", w)
	}
	if asJSON {
		b, merr := json.Marshal(st)
		if merr != nil {
			// A status that cannot be encoded is a failed read, as a panic is: the same
			// {"agent", "read_failed": true} object, and the same exit 0 (it fails open).
			fmt.Fprintf(stderr, "yakos budget check: encoding the status: %v (failing open)\n", merr)
			b, _ = json.Marshal(map[string]any{"agent": agent, "read_failed": true})
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintln(stdout, string(b))
	} else {
		line := fmt.Sprintf("reason=%s state=%s agent=%s spent_usd=%.2f limit_usd=%.2f window=%s", st.Reason, st.State, st.Agent, st.SpentUSD, st.LimitUSD, st.Window)
		if st.LimitTokens > 0 {
			// Only an agent with a token limit gains these two keys, so the line
			// stays as it was for every other agent.
			line += fmt.Sprintf(" spent_tokens=%d limit_tokens=%d", st.SpentTokens, st.LimitTokens)
		}
		fmt.Fprintln(stdout, line)
		fmt.Fprintln(stdout, st.Message())
	}
	if st.Refused() {
		fmt.Fprintln(stderr, (&budget.RefusedError{Status: st}).Error())
		return budget.ExitHardStop
	}
	return 0
}

func budgetStatus(w io.Writer, opts budget.Options, asJSON, byProject bool) {
	pol, perr := budget.LoadPolicy(opts.StateDirOrDefault())
	if perr != nil {
		fmt.Fprintf(os.Stderr, "yakos budget: %v\n", perr)
	}
	var rows []budget.Status
	for _, a := range budget.AgentNamesForProject(pol, opts.Project) {
		st, err := budget.Evaluate(a, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "yakos budget: %s: %v\n", a, err)
		}
		rows = append(rows, st)
	}
	if asJSON {
		b, _ := json.MarshalIndent(rows, "", "  ")
		fmt.Fprintln(w, string(b))
		return
	}
	// Tokens are the primary unit (K-136), so they lead: TOKENS and TOKEN LIMIT come
	// before the dollar columns. (The supervisor and librarian always have a token
	// limit, so the columns are always meaningful.)
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tSTATE\tTOKENS\tTOKEN LIMIT\tSPENT\tLIMIT\tUSED\tWINDOW\tSOURCE")
	for _, st := range rows {
		limit, tokLimit, used := "off", "off", "-"
		if st.LimitUSD > 0 || st.LimitTokens > 0 {
			used = fmt.Sprintf("%.0f%%", st.Pct)
		}
		if st.LimitUSD > 0 {
			limit = fmt.Sprintf("$%.2f", st.LimitUSD)
		}
		if st.LimitTokens > 0 {
			tokLimit = fmt.Sprintf("%d", st.LimitTokens)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t$%.2f\t%s\t%s\t%s\t%s\n", st.Agent, st.State, st.SpentTokens, tokLimit, st.SpentUSD, limit, used, st.Window, st.Source)
		if byProject {
			for _, p := range st.Projects {
				fmt.Fprintf(tw, "  %s\t\t\t\t$%.2f\t\t\t\t\n", p.Project, p.SpentUSD)
			}
		}
	}
	_ = tw.Flush()
}

// parseTokenCount reads a token count: digits, optionally with a decimal point
// and a k, m or b suffix (thousand, million, billion), such as 5000000, 500k,
// 1.5m or 2b. It rejects anything negative, empty, or past the budget package's
// bound.
func parseTokenCount(s string) (int64, error) {
	orig := s
	s = strings.ToLower(strings.TrimSpace(s))
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1e3, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1e6, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "b"):
		mult, s = 1e9, strings.TrimSuffix(s, "b")
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f*mult > float64(int64(1)<<50) {
		return 0, fmt.Errorf("%q is not a token count (use a number such as 5000000, 500k, 1.5m or 2b)", orig)
	}
	return int64(f*mult + 0.5), nil
}

// dispatchValueFlags are the `yakos dispatch` flags that consume the next
// argument, needed to find the agent among the positionals.
var dispatchValueFlags = map[string]bool{"--runtime": true, "--model": true, "--project": true, "--timeout": true, "--eval-run-id": true}

// dispatchAgentAndProject extracts the agent name and --project from the argv
// of a `yakos dispatch` invocation without validating it (the real parser runs
// later). It returns "" for the agent when none is found.
func dispatchAgentAndProject(args []string) (agent, project string) {
	if len(args) == 0 || args[0] != "dispatch" {
		return "", ""
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--help" || a == "-h":
			return "", ""
		case strings.HasPrefix(a, "--") && strings.Contains(a, "="):
			if k, v, _ := strings.Cut(a, "="); k == "--project" {
				project = v
			}
		case dispatchValueFlags[a]:
			if i+1 < len(rest) {
				if a == "--project" {
					project = rest[i+1]
				}
				i++
			}
		case strings.HasPrefix(a, "-"):
		default:
			if agent == "" {
				agent = a
			}
		}
	}
	return agent, project
}

// budgetGateBeforePassthrough applies the dollar-budget pre-flight to a
// `yakos dispatch` that is about to be handed to the bash implementation. A
// hard_stop exits 4 with the refusal; every other outcome falls through.
func budgetGateBeforePassthrough(args []string, yakosRoot string) []string {
	agent, project := dispatchAgentAndProject(args)
	if agent == "" {
		return args
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	st, err := budget.Enforce(agent, budget.Options{Project: project})
	if budget.IsRefused(err) {
		fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
		os.Exit(budget.ExitHardStop)
	}
	if st.State == budget.StateWarning {
		fmt.Fprintf(os.Stderr, "yakos budget: %s\n", st.Message())
	}
	out, err := passthroughClamped(args, agent, project, yakosRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(budget.ExitHardStop)
	}
	if budget.MaxModel(agent, budget.Options{Project: project}) != "" {
		_ = os.Setenv(noFallbackEnv, "1")
	}
	return out
}

// passthroughClamped applies the agent's max_model ceiling to a passthrough
// dispatch's argv: it lowers a model above the ceiling and refuses one the
// registry cannot rank, as the Go-native dispatch does. The project is passed so
// an agent the project names as its supervisor keeps the supervisor's ceiling.
//
// The bash relay resolves the agent file, the runtime and its own argv itself,
// so ranking alone cannot bound what it runs (a symlinked agent, an id:
// collision, `--runtime auto`, a flag value that looks like a flag, a fallback
// chain naming codex). Under a ceiling the dispatch therefore
//
//   - is refused when the runtime it resolves to is not claude: bash cannot pin a
//     codex or agy model, and the Go-native dispatcher refuses an unranked one;
//   - gets `--model <ranked>` and then `--runtime claude` appended LAST, whatever
//     the argv already says, because the bash parser lets the last one win; and
//   - runs with its runtime-fallback lists switched off (YAKOS_DISPATCH_NO_FALLBACK,
//     read by dispatch.sh), so an absent claude is an error, not codex.
//
// Bash then runs nothing dearer than what was ranked, whatever file it read
// (K-168).
func passthroughClamped(args []string, agent, project, yakosRoot string) ([]string, error) {
	ceiling := budget.MaxModel(agent, budget.Options{Project: project})
	if ceiling == "" {
		return args, nil
	}
	a := rosterAgent(yakosRoot, project, agent)
	rt := passthroughRuntimeFor(args, a, project)
	if rt != "claude" {
		return nil, fmt.Errorf("dispatch: agent %s has max_model %s and resolves to runtime %q; the bash dispatch cannot pin a %s model, so it is refused (use the Go dispatcher, or unset the runtime)", agent, ceiling, rt, rt)
	}
	model, _ := argvFlag(args, "--model")
	if model == "" {
		model = bashEffectiveModel(a)
	}
	pin, note, err := dispatch.EnforceModelCeiling(rt, agent, ceiling, model)
	if err != nil {
		return nil, err
	}
	if !runtime.ValidateTier(pin) {
		return nil, fmt.Errorf("dispatch: model %q for agent %s is not a tier the bash dispatch accepts, so max_model %s cannot be pinned and it is refused", pin, agent, ceiling)
	}
	if note != "" {
		fmt.Fprintf(os.Stderr, "yakos budget: %s\n", note)
	}
	out := append(append([]string(nil), args...), "--model", pin, "--runtime", "claude")
	return out, nil
}

// noFallbackEnv is read by cli/lib/dispatch.sh: set to 1, the runtime chain is the
// one runtime it was given, with no frontmatter or project fallbacks.
const noFallbackEnv = "YAKOS_DISPATCH_NO_FALLBACK"

// rosterAgent is the composed agent named id, or the zero agent when the roster
// cannot be read or has none (the bash dispatch then falls back to its defaults).
func rosterAgent(yakosRoot, project, id string) agentscompose.ComposedAgent {
	if yakosRoot == "" {
		return agentscompose.ComposedAgent{}
	}
	roster, err := agentscompose.Compose(yakosRoot, project)
	if err != nil {
		return agentscompose.ComposedAgent{}
	}
	for _, a := range roster {
		if a.ID == id {
			return a
		}
	}
	return agentscompose.ComposedAgent{}
}

// argvFlag is the value the bash dispatch's parser gives flag (--runtime,
// --model, ...): it walks the argv as dispatch.sh does, so a flag-looking string
// that is the VALUE of another flag (`--eval-run-id --runtime=claude`) is not
// read as a flag, and the last occurrence wins. ok is false when none is given.
func argvFlag(args []string, flag string) (val string, ok bool) {
	if len(args) == 0 || args[0] != "dispatch" {
		return "", false
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case dispatchValueFlags[a]:
			if i+1 < len(rest) {
				if a == flag {
					val, ok = rest[i+1], true
				}
				i++
			}
		case strings.HasPrefix(a, "--") && strings.Contains(a, "="):
			if k, v, _ := strings.Cut(a, "="); k == flag {
				val, ok = v, true
			}
		}
	}
	return val, ok
}

// argvRuntime is the --runtime the bash parser reads from the argv, or "".
func argvRuntime(args []string) string {
	v, _ := argvFlag(args, "--runtime")
	return v
}

// passthroughRuntimeFor resolves the runtime the bash dispatch would pick, in its
// order: --runtime, the agent's frontmatter runtime:, .yakos.yml (per-domain, then
// default-runtime), YAKOS_RUNTIME, the state default, claude. A ceiling ranks the
// model on that runtime's column, never on claude's for a codex agent (K-168).
func passthroughRuntimeFor(args []string, a agentscompose.ComposedAgent, project string) string {
	// "auto" is no runtime to the bash parser; it resolves like an absent one.
	if rt := argvRuntime(args); rt != "" && rt != "auto" {
		return rt
	}
	if rt := strings.TrimSpace(a.Runtime); rt != "" {
		return rt
	}
	cfg, _ := projectcfg.Load(project)
	if rt, _ := cfg.RuntimeFor(a.Domain); rt != "" {
		return rt
	}
	if rt := strings.TrimSpace(os.Getenv("YAKOS_RUNTIME")); rt != "" {
		return rt
	}
	if d := modelreg.DefaultStateDir(); d != "" {
		if data, err := os.ReadFile(filepath.Join(d, "default-runtime")); err == nil {
			f := strings.Fields(string(data))
			if len(f) > 0 && runtimeNameRe.MatchString(f[0]) {
				return f[0]
			}
		}
	}
	return "claude"
}

var runtimeNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// bashDefaultModel is the tier cli/lib/dispatch.sh runs when neither the
// frontmatter nor a promoted policy names one (MODEL_RESOLVED's fallback).
const bashDefaultModel = "sonnet"

// bashEffectiveModel is the model the bash dispatch would run for a without a
// --model: model-policy (a promoted tier) over model, each alias-resolved, else
// the fixed bashDefaultModel. It mirrors dispatch.sh's resolution, so the clamp
// ranks what the relay will actually use rather than the composed model alone.
func bashEffectiveModel(a agentscompose.ComposedAgent) string {
	if p := strings.TrimSpace(a.ModelPolicy); p != "" {
		return runtime.ResolveAlias(p)
	}
	m := strings.TrimSpace(a.ModelRaw)
	if m == "" {
		m = a.Model
	}
	if m == "" {
		return bashDefaultModel
	}
	return runtime.ResolveAlias(m)
}
