package main

// cmd_router.go: `yakos router explain` and the shared explain path behind
// `yakos dispatch --explain` (K-139b). Both ask dispatch.Explain what the router
// would decide: a dry run that starts nothing, writes no ledger row, pins no
// conversation and prints no notice. The router has no bash twin (K-143), so
// `yakos router` is always Go-native, like `yakos models`.

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/policywrite"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	internalstart "github.com/bakw00ds/yakos/internal/start"
)

// isRouterForceGo reports whether this invocation is `yakos router ...`. The
// router has no bash equivalent, so shadow mode must not hand it to the bash CLI.
func isRouterForceGo(args []string) bool {
	return len(args) > 0 && args[0] == "router"
}

var (
	// explainAgentRe is the shape of an agent name taken from argv.
	explainAgentRe = policywrite.AgentRe
	// explainClassRe is the shape of a route class taken from argv.
	explainClassRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

// Exit codes of `yakos router explain`: 0 ok, 1 the route could not be decided
// (no runtime can run), 2 a usage error (bad flag, bad agent name, unknown
// agent, unknown class). It is not a hook, so 2 here is only a usage error.
const (
	explainExitFail  = 1
	explainExitUsage = 2
)

func printRouterHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos router <explain|policy> — show how the router would route a dispatch, and its policy

yakos router explain <agent> [flags]

Subcommands:
    explain <agent> [--task-file F] [--class C] [--project DIR] [--json]
                          Print the runtime and model the router would pick for
                          the agent, the provider, the rule that decided it (R0 is
                          the default chain, R1..R6 the policy file's rules), the
                          runtime chain, the reason, the runtime it fell back
                          from, the route class and the policy sha. Nothing is
                          started, no ledger row is written and no conversation is
                          pinned.

    policy get [--json]   Show the router policy: its sha, the rules (R1..R6), the per-agent
                          pins, and whether running a harness without its sandbox and the
                          hooks and OpenAI endpoints are allowed.
    policy set --rules-file <file|->
                          Replace the rules: list from a YAML list (at most 6 rules, each
                          checked as the router reads it). Nothing else in the file is
                          touched; the privileged keys are edited by hand. The write is
                          atomic, owner-only and recorded in the dispatch log.

Flags:
    --rules-file <file|-> policy set: the YAML list of rules.
    --task-file F         Use the size of this regular file as the task size (for
                          task_bytes_gt rules). The file is not read.
    --class C             Route class to match rules against. A Claude Code request
                          class (subagent, opus, sonnet, haiku, fable) also prints
                          the env aliases gateway_classes would set.
    --project DIR         Project whose .yakos.yml applies (default: inferred, as
                          for dispatch, else the working directory).
    --json                Machine-readable output.

The same output for one dispatch: yakos dispatch --explain <agent> [task] [flags].
Exit codes: 0 ok, 1 no route could be decided, 2 usage error (including an
unknown agent or class). See docs/routing.md.
`)
}

func runRouter(yakosRoot string, args []string) {
	os.Exit(routerMain(yakosRoot, args, os.Stdout, os.Stderr, defaultExplainEnv()))
}

// explainEnv is what an explain reads from its surroundings, so tests can drive
// it without the machine's environment, state directory or sign-ins.
type explainEnv struct {
	explain  func(context.Context, dispatch.ExplainQuery) (router.RouteDecision, error)
	stateDir func() string
	environ  func() []string
	cwd      func() (string, error)
}

func defaultExplainEnv() explainEnv {
	return explainEnv{
		explain:  dispatch.Explain,
		stateDir: routerpolicy.StateDir,
		environ:  os.Environ,
		cwd:      os.Getwd,
	}
}

func routerMain(yakosRoot string, args []string, stdout, stderr io.Writer, env explainEnv) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printRouterHelp(stdout)
		if len(args) == 0 {
			return explainExitUsage
		}
		return 0
	}
	if args[0] == "policy" {
		return routerPolicy(stdout, stderr, env, args[1:])
	}
	if args[0] != "explain" {
		_, _ = fmt.Fprintf(stderr, "router: unknown subcommand %q (explain | policy)\n", sanitizeForTerminal(args[0]))
		return explainExitUsage
	}
	var (
		help     bool
		asJSON   bool
		taskFile string
		class    string
		project  string
	)
	fs := &cliflag.Set{Cmd: "router explain", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--json", Kind: cliflag.Bool, Bool: &asJSON},
		{Name: "--task-file", Kind: cliflag.String, Str: &taskFile, ValueDesc: "a path"},
		{Name: "--class", Kind: cliflag.String, Str: &class, ValueDesc: "a route class"},
		{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"},
	}}
	pos, err := fs.Parse(args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return explainExitUsage
	}
	if help {
		printRouterHelp(stdout)
		return 0
	}
	if len(pos) != 1 {
		_, _ = fmt.Fprintln(stderr, "router explain: expected exactly one <agent> (try --help)")
		return explainExitUsage
	}
	var taskBytes int64
	if taskFile != "" {
		n, terr := taskFileSize(taskFile)
		if terr != nil {
			_, _ = fmt.Fprintf(stderr, "router explain: --task-file: %v\n", terr)
			return explainExitUsage
		}
		taskBytes = n
	}
	root := explainLibRoot(yakosRoot)
	proj, perr := explainProject(project, env.cwd)
	if perr != nil {
		_, _ = fmt.Fprintf(stderr, "router explain: %v\n", perr)
		return explainExitUsage
	}
	return explainRun(stdout, stderr, env, explainArgs{
		YakosRoot: root, Project: proj, Agent: pos[0], Class: class,
		TaskBytes: taskBytes, JSON: asJSON,
		ConversationID: os.Getenv("YAKOS_CONVERSATION_ID"),
	}, "router explain")
}

// taskFileSize returns the size of a regular file without opening it, so a FIFO
// or device named by mistake cannot block or stream.
func taskFileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("cannot stat the file")
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("not a regular file")
	}
	return fi.Size(), nil
}

// explainLibRoot resolves the yakOS lib root the way dispatch does.
func explainLibRoot(yakosRoot string) string {
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	return resolveLibRoot(yakosRoot, home, io.Discard)
}

// explainProject resolves the project as dispatch does (flag, YAKOS_PROJECT_PATH,
// the agent-control layout) and, because an explain only reads, falls back to the
// working directory.
func explainProject(project string, cwd func() (string, error)) (string, error) {
	if project == "" {
		project = os.Getenv("YAKOS_PROJECT_PATH")
	}
	wd, _ := cwd()
	if project == "" {
		project = inferProjectFromCWD(wd, os.Getenv("HOME"))
	}
	if project == "" {
		project = wd
	}
	if st, err := os.Stat(project); project == "" || err != nil || !st.IsDir() {
		return "", fmt.Errorf("project path not found or not a directory")
	}
	return project, nil
}

// explainArgs is one explain request after the command has parsed its flags.
type explainArgs struct {
	YakosRoot, Project, Agent, Class string
	TaskBytes                        int64
	Runtime, Model, EvalRunID        string
	RuntimeEnvDefault                string
	RuntimeFallbackOptIn             []string
	ConversationID                   string
	JSON                             bool
}

// explainRun validates the request, asks the router and prints the answer. cmd
// prefixes errors ("router explain" or "dispatch").
func explainRun(stdout, stderr io.Writer, env explainEnv, a explainArgs, cmd string) int {
	if !explainAgentRe.MatchString(a.Agent) {
		_, _ = fmt.Fprintf(stderr, "%s: invalid agent name\n", cmd)
		return explainExitUsage
	}
	if a.Class != "" {
		if !explainClassRe.MatchString(a.Class) {
			_, _ = fmt.Fprintf(stderr, "%s: invalid class (letters, digits, . _ : -)\n", cmd)
			return explainExitUsage
		}
		known := knownExplainClasses(env.stateDir())
		if !containsString(known, a.Class) {
			_, _ = fmt.Fprintf(stderr, "%s: unknown class %q (known: %s)\n", cmd, a.Class, strings.Join(known, ", "))
			return explainExitUsage
		}
	}
	rt := a.Runtime
	if rt == "auto" {
		rt = ""
	}
	d, err := env.explain(context.Background(), dispatch.ExplainQuery{
		YakosRoot: a.YakosRoot, Project: a.Project, Agent: a.Agent,
		Runtime: rt, RuntimeEnvDefault: a.RuntimeEnvDefault, RuntimeFallbackOptIn: a.RuntimeFallbackOptIn,
		Model: a.Model, EvalRunID: a.EvalRunID, Class: a.Class, TaskBytes: a.TaskBytes,
		ConversationID: a.ConversationID,
	})
	if err != nil {
		msg := strings.TrimPrefix(dispatch.PrefixedMessage(err), "dispatch: ")
		code := explainExitFail
		// An unknown agent is a usage error, and its message names no path.
		if i := strings.Index(msg, " not found in composed set"); i >= 0 {
			msg = msg[:i+len(" not found in composed set")]
			code = explainExitUsage
		}
		_, _ = fmt.Fprintf(stderr, "%s: %s\n", cmd, sanitizeForTerminal(msg))
		return code
	}
	v := router.ExplainView{Agent: a.Agent, Decision: d, JevShadow: jevShadowState(env, a.Project)}
	if rt != "" {
		v.Overrides = append(v.Overrides, "runtime")
	}
	if a.Model != "" {
		v.Overrides = append(v.Overrides, "model")
	}
	if routerpolicyKnowsClass(a.Class) {
		v.EnvClass = a.Class
		v.Env = gatewayEnvFor(env, a.Class)
	}
	if a.JSON {
		b, jerr := router.ExplainJSON(v)
		if jerr != nil {
			_, _ = fmt.Fprintf(stderr, "%s: cannot encode the result\n", cmd)
			return explainExitFail
		}
		_, _ = stdout.Write(b)
		return 0
	}
	router.WriteExplain(stdout, v)
	return 0
}

// jevShadowState is "on" or "off": whether the K-177 Jev routing shadow would
// send task text for a dispatch in project. It reads the same trusted policy and
// project opt-outs the dispatcher does.
func jevShadowState(env explainEnv, project string) string {
	if project == "" && env.cwd != nil {
		project, _ = env.cwd()
	}
	getenv := func(k string) string {
		for _, kv := range env.environ() {
			if strings.HasPrefix(kv, k+"=") {
				return kv[len(k)+1:]
			}
		}
		return ""
	}
	if decision.ResolveRoutingShadow(env.stateDir(), project, getenv).Enabled {
		return "on"
	}
	return "off"
}

func routerpolicyKnowsClass(c string) bool {
	return c != "" && routerpolicy.EnvNameFor(c) != ""
}

// gatewayEnvFor returns the alias gateway_classes would set for class, using the
// same resolution `yakos start` applies at launch.
func gatewayEnvFor(env explainEnv, class string) []router.EnvAlias {
	m := map[string]string{}
	for _, kv := range env.environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	g := internalstart.GatewayAliasesFor(env.stateDir(), m)
	out := []router.EnvAlias{}
	for _, c := range g.Set {
		if c.Class == class {
			out = append(out, router.EnvAlias{Name: c.EnvName, Model: c.Model, Class: c.Class})
		}
	}
	for _, c := range g.Overridden {
		if c.Class == class {
			out = append(out, router.EnvAlias{Name: c.EnvName, Model: c.Model, Class: c.Class, Overridden: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// knownExplainClasses lists, sorted, the classes `--class` accepts.
func knownExplainClasses(stateDir string) []string { return router.KnownClasses(stateDir) }

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
