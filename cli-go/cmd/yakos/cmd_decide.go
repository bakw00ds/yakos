package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// Exit-code contract of `yakos decide` (ADR-0009, docs/decision-providers.md):
//
//	0  a typed answer was printed, OR the provider was unavailable and
//	   --shadow was given (stdout: {"answer":null,"reason":"<class>"})
//	1  usage error (bad flag, missing/extra surface); no decision attempted
//	3  the provider was unavailable, timed out, refused, or failed and
//	   --shadow was NOT given (stdout: {"answer":null,"reason":"<class>"})
//
// It NEVER exits 2. Exit 2 is the Claude Code hook "block" code, and no hook
// may block on a decide failure (fail-closed here means "take the existing
// deterministic path", never "stop the tool call"). A panic is converted to
// the unavailable path for the same reason: Go's own panic status is 2.
const (
	decideExitOK          = 0
	decideExitUsage       = 1
	decideExitUnavailable = 3
	decideMaxStdin        = 1 << 20
)

func printDecideHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos decide <surface> [--provider jev|mock|none] [--shadow] — ask a typed decision provider

Reads a JSON object (named state fields) on stdin, redacts it, asks the
question set lib/decisions/<surface>.yaml, and prints the typed answer as JSON.
A decision provider is not an agent runtime (ADR-0009): it answers fixed,
reviewed questions and returns probabilities, never text.

Flags:
    --provider <id>       jev | mock | none. Default: $YAKOS_DECISION_PROVIDER,
                          then decisions.provider in .yakos.yml, then none.
    --shadow              Advisory call: on any failure exit 0 with
                          {"answer":null,"reason":...} so a hook proceeds.
    --timeout <duration>  Overall deadline (default 1.5s; 10s with --shadow).
    --session <id>        Session id for the per-session call cap
                          (default $YAKOS_SESSION_ID, else "default").
    --state-file <path>   Read the state from a file instead of stdin.
    --sets-dir <dir>      Question-set directory (default <framework>/lib/decisions).
    --config <path>       .yakos.yml to read the decisions: block from
                          (default $CLAUDE_PROJECT_DIR/.yakos.yml, then ./.yakos.yml).

Credentials: jev reads TYPESAFE_API_KEY from the environment at call time.
It is never stored, logged, or printed. YAKOS_DECISION_DISABLE=1 turns every
provider off. YAKOS_DECISION_MOCK=<file|dir> feeds the mock provider.

Output (stdout):
    {"answer":{"<question>":{...}}, "surface":..., "schema_hash":..., ...}
    {"answer":null,"reason":"timeout|breaker_open|budget|no_key|disabled|..."}

Exit code:
    0   Answer printed, or provider unavailable with --shadow
    1   Usage error
    3   Provider unavailable/failed without --shadow (never 2: no hook may
        block on a decide failure)

Every call is appended to ~/.yakos-state/decision-log.ndjson (never the raw state).
`)
}

// runDecide is the `yakos decide` entry point.
func runDecide(yakosRoot string, args []string) {
	home := os.Getenv("HOME")
	os.Exit(decideMain(decideEnv{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Getenv: os.Getenv, YakosRoot: yakosRoot, Home: home,
	}, args))
}

// decideEnv carries every ambient dependency so tests can drive decideMain
// without a subprocess.
type decideEnv struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Getenv    func(string) string
	YakosRoot string
	Home      string
	// Provider, when non-nil, replaces provider construction (tests).
	Provider decision.Provider
	// StateDir overrides the state directory (tests); default statepath.Dir().
	StateDir string
}

func decideMain(env decideEnv, args []string) (code int) {
	shadow := false
	// A panic must not become Go's exit status 2 (the hook "block" code).
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(env.Stderr, "decide: internal error: %v\n", r)
			code = decideFail(env.Stdout, shadow, decision.ClassInternal)
		}
	}()

	var help bool
	var providerFlag, timeoutFlag, sessionFlag, stateFile, setsDir, configPath string
	fs := &cliflag.Set{Cmd: "decide", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--shadow", Kind: cliflag.Bool, Bool: &shadow},
		{Name: "--provider", Kind: cliflag.String, Str: &providerFlag, ValueDesc: "jev, mock, or none"},
		{Name: "--timeout", Kind: cliflag.String, Str: &timeoutFlag, ValueDesc: "a duration such as 1500ms"},
		{Name: "--session", Kind: cliflag.String, Str: &sessionFlag, ValueDesc: "a session id"},
		{Name: "--state-file", Kind: cliflag.String, Str: &stateFile, ValueDesc: "a path"},
		{Name: "--sets-dir", Kind: cliflag.String, Str: &setsDir, ValueDesc: "a directory"},
		{Name: "--config", Kind: cliflag.String, Str: &configPath, ValueDesc: "a path"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(env.Stderr, err)
		return decideExitUsage
	}
	if help {
		printDecideHelp(env.Stdout)
		return decideExitOK
	}
	if len(rest) != 1 || (len(rest[0]) > 0 && rest[0][0] == '-') {
		fmt.Fprintln(env.Stderr, "decide: expected exactly one <surface> (try --help)")
		return decideExitUsage
	}
	surface := rest[0]
	if !decision.ValidSurface(surface) {
		fmt.Fprintf(env.Stderr, "decide: invalid surface name %q (try --help)\n", surface)
		return decideExitUsage
	}
	var timeout time.Duration
	if timeoutFlag != "" {
		d, perr := time.ParseDuration(timeoutFlag)
		if perr != nil || d <= 0 {
			fmt.Fprintf(env.Stderr, "decide: invalid --timeout %q (try --help)\n", timeoutFlag)
			return decideExitUsage
		}
		timeout = d
	}
	if providerFlag != "" && providerFlag != decision.ProviderJev && providerFlag != decision.ProviderMock && providerFlag != decision.ProviderNone {
		fmt.Fprintf(env.Stderr, "decide: unknown provider %q (jev, mock, none)\n", providerFlag)
		return decideExitUsage
	}

	getenv := env.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	// Everything below is a decide FAILURE, not a usage error: the caller's
	// existing path runs (exit 0 with --shadow, otherwise 3).
	fail := func(class, format string, a ...any) int {
		if format != "" {
			fmt.Fprintf(env.Stderr, "decide: "+format+"\n", a...)
		}
		return decideFail(env.Stdout, shadow, class)
	}

	if decision.KillSwitch(getenv) {
		return fail(decision.ClassDisabled, "YAKOS_DECISION_DISABLE=1")
	}

	cfg, cerr := decision.LoadConfig(decideConfigPath(configPath, getenv))
	if cerr != nil {
		return fail(decision.ClassBadRequest, "config: %v", cerr)
	}
	name := providerFlag
	if name == "" {
		name = getenv(decision.EnvProvider)
	}
	if name == "" {
		name = cfg.Provider
	}
	if sc, ok := cfg.Surfaces[surface]; ok && sc.Mode == "off" {
		return fail(decision.ClassDisabled, "surface %s is off in .yakos.yml", surface)
	}

	if setsDir == "" {
		root := env.YakosRoot
		if r := getenv("YAKOS_ROOT"); r != "" {
			root = r
		}
		setsDir = filepath.Join(resolveLibRoot(root, env.Home, env.Stderr), "lib", "decisions")
	}
	set, serr := decision.LoadSet(setsDir, surface)
	if serr != nil {
		return fail(decision.ClassBadRequest, "question set: %v", serr)
	}

	state, rerr := readDecideState(env.Stdin, stateFile)
	if rerr != nil {
		return fail(decision.ClassBadRequest, "state: %v", rerr)
	}

	prov := env.Provider
	if prov == nil {
		stateDir := env.StateDir
		if stateDir == "" {
			stateDir = statepath.Dir()
		}
		paths := decision.StatePaths{Dir: stateDir}
		switch name {
		case decision.ProviderJev:
			prov = &decision.Jev{
				Getenv:  getenv,
				Breaker: decision.NewBreaker(paths.Breaker()),
				Budget:  decision.NewBudget(paths.Budget(), cfg.Budget.MaxCallsPerSession, cfg.Budget.MaxUSDPerDay),
			}
		case decision.ProviderMock:
			prov = &decision.Mock{Getenv: getenv}
		case decision.ProviderNone, "":
			prov = decision.NewNone()
		default:
			return fail(decision.ClassBadRequest, "unknown provider %q in configuration", name)
		}
	}

	mode := decision.ModePrefilter
	if shadow {
		mode = decision.ModeShadow
	}
	session := sessionFlag
	if session == "" {
		session = getenv("YAKOS_SESSION_ID")
	}
	if session == "" {
		session = "default"
	}
	logPath := ""
	if env.StateDir != "" {
		logPath = decision.StatePaths{Dir: env.StateDir}.Log()
	}
	eng := &decision.Engine{Provider: prov, Logger: decision.NewLogger(logPath), Egress: cfg.Egress}
	out := eng.Execute(context.Background(), set, state, mode, session, timeout)
	if out.Err != nil {
		return fail(out.Class, "%s", out.Err.Error())
	}

	res := out.Result
	payload := struct {
		Answer     map[string]decision.Answer `json:"answer"`
		Surface    string                     `json:"surface"`
		SchemaID   string                     `json:"schema_id"`
		SchemaHash string                     `json:"schema_hash"`
		Provider   string                     `json:"provider"`
		Model      string                     `json:"model"`
		Mode       string                     `json:"mode"`
		Usage      decision.Usage             `json:"usage"`
		CostUSD    float64                    `json:"cost_usd"`
		LatencyMS  int64                      `json:"latency_ms"`
	}{res.Answers, set.Surface, set.SchemaID, set.Hash, res.Provider, res.Model, mode, res.Usage, res.CostUSD, res.LatencyMS}
	b, _ := json.Marshal(payload)
	fmt.Fprintln(env.Stdout, string(b))
	return decideExitOK
}

// decideFail prints the null answer and returns the contract exit code.
func decideFail(stdout io.Writer, shadow bool, class string) int {
	b, _ := json.Marshal(struct {
		Answer *struct{} `json:"answer"`
		Reason string    `json:"reason"`
	}{nil, class})
	fmt.Fprintln(stdout, string(b))
	if shadow {
		return decideExitOK
	}
	return decideExitUnavailable
}

func decideConfigPath(flagVal string, getenv func(string) string) string {
	if flagVal != "" {
		return flagVal
	}
	if d := getenv("CLAUDE_PROJECT_DIR"); d != "" {
		return filepath.Join(d, ".yakos.yml")
	}
	return ".yakos.yml"
}

func readDecideState(stdin io.Reader, file string) (any, error) {
	var r io.Reader = stdin
	if file != "" && file != "-" {
		f, err := os.Open(file) //nolint:gosec // operator-supplied path
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	if r == nil {
		return nil, fmt.Errorf("no state on stdin")
	}
	data, err := io.ReadAll(io.LimitReader(r, decideMaxStdin+1))
	if err != nil {
		return nil, err
	}
	if len(data) > decideMaxStdin {
		return nil, fmt.Errorf("state exceeds %d bytes", decideMaxStdin)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("not valid JSON")
	}
	return v, nil
}
