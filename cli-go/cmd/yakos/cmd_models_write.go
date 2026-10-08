package main

// cmd_models_write.go: the writers behind `yakos models enable|disable|alias|
// pin|pricing` and `yakos router policy get|set` (K-153).
//
// Every write goes through the one trusted writer of its file (modelreg's overlay
// functions, routerpolicy.Edit; both over statepath.EditYAML: trust-checked read,
// owner-only 0600 atomic rename) and appends a config_changed line to the
// dispatch log through dispatch.Account (operator, file, sha before and after).
// None of them prints a path.
//
// Trust: the target is statepath.TrustedDir, so an absolute $HOME is trusted as
// given, including one inside a project directory (K-176, sec-356 Q1). Readers
// resolve the same $HOME, so a project that controls it controls both sides;
// tightening only the writers would write a file the readers never read. These
// commands are an audit and correctness layer, not a boundary against code that
// can set the caller's environment. The boundary against an agent running them
// is the budget-guard hook (lib/hooks/legacy/budget-guard.sh and its Go twin,
// K-176), which refuses `models enable|disable|alias|pin|pricing` and `router
// policy set` from a tool call.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/policywrite"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// maxPolicyInput bounds a rules file read for `router policy set`.
const maxPolicyInput = 256 << 10

// cliOperatorID names the actor of a CLI write in the audit line: the OS user.
func cliOperatorID() string {
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return "cli"
	}
	name := u.Username
	if i := strings.LastIndexByte(name, '\\'); i >= 0 { // DOMAIN\user on Windows
		name = name[i+1:]
	}
	return name
}

// callerIdentity reads who is calling from the environment (K-176): the agent
// (YAKOS_AGENT_TYPE, set by dispatch), the session id Claude Code gives its
// tools, and whether any agent-context marker is present. The environment is
// the caller's own, so this labels the audit line; it is not a boundary. The
// boundary is the budget-guard hook, which refuses these commands to an agent.
func callerIdentity() (agent, session, actor string) {
	agent = os.Getenv("YAKOS_AGENT_TYPE")
	for _, k := range []string{"CLAUDE_SESSION_ID", "CLAUDE_CODE_SESSION_ID"} {
		if v := os.Getenv(k); v != "" {
			session = v
			break
		}
	}
	actor = "operator"
	if agent != "" || session != "" || os.Getenv("CLAUDECODE") != "" || os.Getenv("CLAUDE_PROJECT_DIR") != "" {
		actor = "agent"
	}
	return agent, session, actor
}

// openAudit opens (and flock-holds) the dispatch log in the trusted state
// directory BEFORE a policy write, so a write that could not be recorded is
// refused rather than made unaudited. The log is always the one in stateDir (the
// home state directory); YAKOS_DISPATCH_LOG, which a project can set, is ignored.
func openAudit(stderr io.Writer, stateDir string) *dispatch.ConfigAudit {
	agent, session, actor := callerIdentity()
	au, err := dispatch.OpenConfigAudit(dispatch.Request{OperatorID: cliOperatorID(), Surface: dispatch.SurfaceCLI, AgentName: agent, SessionID: session}, stateDir)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "yakos: the dispatch log cannot be opened, so the change was not made")
		return nil
	}
	au.Actor = actor
	return au
}

// auditWrite records a policy write through the already-open log. The write has
// happened; a failure here is reported (exit 1) so the operator knows.
func auditWrite(stderr io.Writer, au *dispatch.ConfigAudit, file, action string, res statepath.EditResult) bool {
	err := au.Record(dispatch.ConfigChange{
		File: file, Action: action, SHABefore: res.SHABefore, SHAAfter: res.SHAAfter, Surface: dispatch.SurfaceCLI, Actor: au.Actor,
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "yakos: the change was written but could not be recorded in the dispatch log")
		return false
	}
	return true
}

// reportWrite prints the one-line result of a write and audits it.
func reportWrite(stdout, stderr io.Writer, au *dispatch.ConfigAudit, file, action, what string, res statepath.EditResult) int {
	if !res.Changed {
		_, _ = fmt.Fprintf(stdout, "unchanged: %s (%s)\n", what, file)
		return 0
	}
	if !auditWrite(stderr, au, file, action, res) {
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "ok: %s (%s %s)\n", what, file, res)
	return 0
}

// modelsWriteArgs are the flags of the write subcommands.
type modelsWriteArgs struct {
	input, output, cacheRead, cacheWrite, billing, runtime string
	clear                                                  bool
}

// errAuditFailed marks a write whose audit line failed; reportWrite has already
// said so on stderr.
var errAuditFailed = errors.New("audit failed")

// recorder reports and audits each change as policywrite makes it.
func recorder(stdout, stderr io.Writer, sub string, au *dispatch.ConfigAudit) policywrite.Recorder {
	return func(c policywrite.Change) error {
		if c.Note != "" {
			_, _ = fmt.Fprintf(stderr, "models %s: note: %s\n", sub, c.Note)
		}
		if reportWrite(stdout, stderr, au, c.File, c.Action, c.What, c.Res) != 0 {
			return errAuditFailed
		}
		return nil
	}
}

func modelsWrite(stdout, stderr io.Writer, env modelsEnv, sub string, pos []string, a modelsWriteArgs, project string) int {
	fail := func(format string, args ...any) int {
		_, _ = fmt.Fprintf(stderr, "models %s: %s\n", sub, fmt.Sprintf(format, args...))
		return 1
	}
	if env.stateDir == "" {
		return fail("no home directory, so no overlay to write")
	}
	reg, err := loadModelRegistry(stderr, env, project, env.discoverer(env.stateDir, 0))
	if err != nil {
		return fail("the model registry could not be loaded")
	}
	au := openAudit(stderr, env.stateDir)
	if au == nil {
		return 1
	}
	defer au.Close()
	rec := recorder(stdout, stderr, sub, au)
	var werr error
	switch sub {
	case "enable", "disable":
		if len(pos) != 1 {
			return fail("usage: yakos models %s <id>", sub)
		}
		werr = policywrite.SetEnabled(env.stateDir, reg, pos[0], sub == "enable", rec)
	case "alias":
		if len(pos) != 3 {
			return fail("usage: yakos models alias <%s> <codex|agy> <id|default>", strings.Join(modelreg.AliasNames, "|"))
		}
		id := pos[2]
		if id == "default" {
			id = ""
		}
		werr = policywrite.SetAlias(env.stateDir, reg, pos[0], pos[1], id, rec)
	case "pricing":
		if len(pos) != 1 {
			return fail("usage: yakos models pricing <id> --input <usd> --output <usd> [--cache-read <usd>] [--cache-write <usd>] [--billing api|subscription|local] | --clear")
		}
		werr = policywrite.SetPricing(env.stateDir, reg, pos[0], policywrite.PricingArgs{
			Input: a.input, Output: a.output, CacheRead: a.cacheRead, CacheWrite: a.cacheWrite, Billing: a.billing, Clear: a.clear,
		}, rec)
	default: // pin
		if len(pos) < 1 || len(pos) > 2 || (a.clear && len(pos) != 1) || (!a.clear && len(pos) != 2) {
			return fail("usage: yakos models pin <agent> <id> [--runtime <name>] | yakos models pin <agent> --clear")
		}
		id := ""
		if len(pos) == 2 {
			id = pos[1]
		}
		werr = policywrite.SetPin(env.stateDir, reg, pos[0], id, a.runtime, a.clear, rec)
	}
	switch {
	case werr == nil:
		return 0
	case errors.Is(werr, errAuditFailed):
		return 1
	}
	return fail("%v", werr)
}

// routerPolicy runs `yakos router policy get|set`.
func routerPolicy(stdout, stderr io.Writer, env explainEnv, args []string) int {
	var (
		help, asJSON bool
		rulesFile    string
	)
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	fs := policyFlagSet(&help, &asJSON, &rulesFile)
	pos, err := fs.Parse(args[min(1, len(args)):])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return explainExitUsage
	}
	if help || sub == "" || sub == "--help" || sub == "-h" {
		printRouterHelp(stdout)
		return 0
	}
	state := env.stateDir()
	switch {
	case sub == "get" && len(pos) == 0:
		return routerPolicyGet(stdout, stderr, state, asJSON)
	case sub == "set" && len(pos) == 0 && rulesFile != "":
		return routerPolicySet(stdout, stderr, state, rulesFile)
	}
	_, _ = fmt.Fprintln(stderr, "usage: yakos router policy get [--json] | yakos router policy set --rules-file <file|->")
	return explainExitUsage
}

func routerPolicyGet(stdout, stderr io.Writer, state string, asJSON bool) int {
	v := router.ViewOf(state)
	if asJSON {
		return writeJSON(stdout, stderr, v)
	}
	sha := v.SHA
	if sha == "" {
		sha = "(no trusted policy file)"
	}
	_, _ = fmt.Fprintf(stdout, "policy_sha: %s\n", sha)
	_, _ = fmt.Fprintf(stdout, "allow_unsandboxed_runtimes: [%s]\n", strings.Join(v.AllowUnsandboxedRuntimes, " "))
	_, _ = fmt.Fprintf(stdout, "hooks_endpoint: %t\nopenai_endpoint: %t\n", v.HooksEndpoint, v.OpenAIEndpoint)
	for _, r := range v.Rules {
		_, _ = fmt.Fprintf(stdout, "%s: %s\n", r.ID, sanitizeForTerminal(ruleLine(r)))
	}
	for _, p := range v.Pins {
		_, _ = fmt.Fprintf(stdout, "pin: %s -> %s/%s\n", sanitizeForTerminal(p.Agent), sanitizeForTerminal(p.Runtime), sanitizeForTerminal(p.Model))
	}
	for _, w := range v.Warnings {
		_, _ = fmt.Fprintf(stderr, "router policy: %s\n", sanitizeForTerminal(w))
	}
	return 0
}

func routerPolicySet(stdout, stderr io.Writer, state, rulesFile string) int {
	if state == "" {
		_, _ = fmt.Fprintln(stderr, "router policy set: no home directory, so no policy to write")
		return explainExitFail
	}
	data, err := readBounded(rulesFile, maxPolicyInput)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "router policy set: --rules-file: %v\n", err)
		return explainExitUsage
	}
	au := openAudit(stderr, state)
	if au == nil {
		return explainExitFail
	}
	defer au.Close()
	if err := policywrite.SetRules(state, data, nil, recorder(stdout, stderr, "policy set", au)); err != nil {
		if !errors.Is(err, errAuditFailed) {
			_, _ = fmt.Fprintf(stderr, "router policy set: %s\n", sanitizeForTerminal(err.Error()))
		}
		return explainExitFail
	}
	return 0
}

// readBounded reads at most max bytes of a regular file, or of stdin for "-". A
// FIFO or device named by mistake is refused, not read.
func readBounded(path string, max int64) ([]byte, error) {
	var r io.Reader = os.Stdin
	if path != "-" {
		fi, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("cannot stat the file")
		}
		if !fi.Mode().IsRegular() {
			return nil, fmt.Errorf("not a regular file")
		}
		f, err := os.Open(path) //nolint:gosec // operator-named file, regular, bounded below
		if err != nil {
			return nil, fmt.Errorf("cannot open the file")
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read the file")
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("larger than %d bytes", max)
	}
	return data, nil
}

func policyFlagSet(help, asJSON *bool, rulesFile *string) *cliflag.Set {
	return &cliflag.Set{Cmd: "router policy", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: help},
		{Name: "--json", Kind: cliflag.Bool, Bool: asJSON},
		{Name: "--rules-file", Kind: cliflag.String, Str: rulesFile, ValueDesc: "a path or -"},
	}}
}

// ruleLine renders a rule on one line: what it matches, then what it does.
func ruleLine(r router.Rule) string {
	var m, a []string
	add := func(dst *[]string, k, v string) {
		if v != "" {
			*dst = append(*dst, k+"="+v)
		}
	}
	add(&m, "class", r.Match.Class)
	add(&m, "agent", r.Match.Agent)
	add(&m, "domain", r.Match.Domain)
	if r.Match.TaskBytesGT > 0 {
		add(&m, "task_bytes_gt", strconv.FormatInt(r.Match.TaskBytesGT, 10))
	}
	add(&m, "tags", strings.Join(r.Match.Tags, ","))
	add(&a, "runtime", r.Action.Runtime)
	add(&a, "model", r.Action.Model)
	add(&a, "fallbacks", strings.Join(r.Action.Fallbacks, ","))
	line := "when " + orAny(strings.Join(m, " ")) + " -> " + strings.Join(a, " ")
	if r.OverridePins {
		line += " (overrides pins)"
	}
	return line
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}
