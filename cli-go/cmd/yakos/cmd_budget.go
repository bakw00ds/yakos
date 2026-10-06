package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/cliflag"
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
                          a project agent_budgets: entry): state, spend, limit.
                          Token columns appear when an agent has used tokens or has
                          a token limit. --by-project adds each agent's spend per project.
    set <agent> [<usd>] [--tokens <n>] [--window monthly|lifetime] [--max-model haiku|sonnet|opus|fable]
                          Set an agent's limits in ~/.yakos-state/budget-policy.yml.
                          <usd> is a dollar limit; it counts only runs billed per API
                          call, never a subscription or a local model.
                          --tokens is a token limit: the input, output and cache tokens of
                          every run, whatever it is billed (5000000, 500k, 1.5m, 2b).
                          Give <usd>, --tokens, or both. 0 turns a limit off
                          (including a built-in default).
                          --max-model also sets a model-tier ceiling for the agent
                          (a project cannot raise its cost with a dearer model).
    reset <agent>         Start the agent's current window over. Spend already
                          logged stops counting; the dispatch-log is untouched.
    check <agent> [--project <path>] [--json]
                          Pre-flight for hooks and scripts. Exit 0 when the agent
                          may run, 4 when it is in hard_stop. Never exits 2.
                          First line is machine-readable:
                          reason=<budget_off|budget_ok|budget_warning|budget_exhausted> state=... agent=...

Flags:
    --json                Machine-readable output.
    --window <w>          monthly (calendar month, local time; default) or lifetime.
    --by-project          status: list spend per project under each agent.
    --tokens <n>          set: token limit (see set).
    --max-model <tier>    set: model-tier ceiling applied at dispatch.
    --project <path>      Project whose .yakos.yml agent_budgets: may LOWER a limit.

States: ok, warning (default 80% of the limit), hard_stop (100% of either limit:
new dispatches are refused, exit 4; a run in flight is not killed). Agents have no
budget unless one is set, except supervisor ($100/month) and librarian ($40/month).
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
		window    = "monthly"
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
		if len(pos) == 2 {
			if usd == 0 {
				fmt.Printf("budget for %s turned off\n", pos[0])
			} else {
				fmt.Printf("budget for %s set to $%.2f (%s)\n", pos[0], usd, window)
			}
		}
		if tokensArg != "" {
			if tokens == 0 {
				fmt.Printf("token budget for %s turned off\n", pos[0])
			} else {
				fmt.Printf("token budget for %s set to %d tokens (%s)\n", pos[0], tokens, window)
			}
		}
	case "reset":
		if len(pos) != 1 {
			fmt.Fprintln(os.Stderr, "usage: yakos budget reset <agent>")
			os.Exit(1)
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
			code = 0
		}
	}()
	st, err := budget.Evaluate(agent, opts)
	if err != nil {
		fmt.Fprintf(stderr, "yakos budget check: %v (failing open)\n", err)
	}
	for _, w := range st.Warnings {
		fmt.Fprintf(stderr, "yakos budget: %s\n", w)
	}
	if asJSON {
		b, _ := json.Marshal(st)
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
	projLimits, _ := budget.ProjectLimits(opts.Project)
	var rows []budget.Status
	for _, a := range budget.AgentNamesWith(pol, projLimits) {
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
	// Tokens are the primary unit (K-136): when any agent has used tokens or has a
	// token limit, the table gains TOKENS and TOKEN LIMIT columns. Without any, the
	// table is exactly the dollar table it always was.
	showTokens := false
	for _, st := range rows {
		showTokens = showTokens || st.SpentTokens > 0 || st.LimitTokens > 0
	}
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	if showTokens {
		fmt.Fprintln(tw, "AGENT\tSTATE\tTOKENS\tTOKEN LIMIT\tSPENT\tLIMIT\tUSED\tWINDOW\tSOURCE")
	} else {
		fmt.Fprintln(tw, "AGENT\tSTATE\tSPENT\tLIMIT\tUSED\tWINDOW\tSOURCE")
	}
	for _, st := range rows {
		limit, used := "off", "-"
		if st.LimitUSD > 0 || st.LimitTokens > 0 {
			used = fmt.Sprintf("%.0f%%", st.Pct)
		}
		if st.LimitUSD > 0 {
			limit = fmt.Sprintf("$%.2f", st.LimitUSD)
		}
		if showTokens {
			tokLimit := "off"
			if st.LimitTokens > 0 {
				tokLimit = fmt.Sprintf("%d", st.LimitTokens)
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t$%.2f\t%s\t%s\t%s\t%s\n", st.Agent, st.State, st.SpentTokens, tokLimit, st.SpentUSD, limit, used, st.Window, st.Source)
		} else {
			fmt.Fprintf(tw, "%s\t%s\t$%.2f\t%s\t%s\t%s\t%s\n", st.Agent, st.State, st.SpentUSD, limit, used, st.Window, st.Source)
		}
		if byProject {
			for _, p := range st.Projects {
				if showTokens {
					fmt.Fprintf(tw, "  %s\t\t\t\t$%.2f\t\t\t\t\n", p.Project, p.SpentUSD)
				} else {
					fmt.Fprintf(tw, "  %s\t\t$%.2f\t\t\t\t\n", p.Project, p.SpentUSD)
				}
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
	out := clampDispatchModel(args, agent)
	if !hasModelFlag(out) {
		out = clampFrontmatterModel(out, agent, project, yakosRoot)
	}
	return out
}

func hasModelFlag(args []string) bool {
	for _, a := range args {
		if a == "--model" || strings.HasPrefix(a, "--model=") {
			return true
		}
	}
	return false
}

// clampFrontmatterModel covers a passthrough dispatch with no --model: the
// bash dispatch would resolve the agent's frontmatter model and pin the relay
// to it, bypassing the max_model ceiling. When the composed frontmatter model
// exceeds the ceiling, an explicit --model <ceiling> is appended (K-116).
func clampFrontmatterModel(args []string, agent, project, yakosRoot string) []string {
	if yakosRoot == "" {
		return args
	}
	roster, err := agentscompose.Compose(yakosRoot, project)
	if err != nil {
		return args
	}
	for _, a := range roster {
		if a.ID != agent || a.Model == "" {
			continue
		}
		clamped, note := budget.ClampModel(agent, a.Model, budget.Options{})
		if note == "" {
			return args
		}
		fmt.Fprintf(os.Stderr, "yakos budget: %s\n", note)
		return append(append([]string(nil), args...), "--model", clamped)
	}
	return args
}

// clampDispatchModel lowers an explicit --model on a passthrough dispatch to
// the agent's max_model ceiling (the bash dispatch has no ceiling of its own).
func clampDispatchModel(args []string, agent string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		model := ""
		switch {
		case out[i] == "--model" && i+1 < len(out):
			model = out[i+1]
		case strings.HasPrefix(out[i], "--model="):
			model = strings.TrimPrefix(out[i], "--model=")
		default:
			continue
		}
		clamped, note := budget.ClampModel(agent, model, budget.Options{})
		if note == "" {
			continue
		}
		fmt.Fprintf(os.Stderr, "yakos budget: %s\n", note)
		if out[i] == "--model" {
			out[i+1] = clamped
		} else {
			out[i] = "--model=" + clamped
		}
	}
	return out
}
