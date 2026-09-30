package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
//	   (also `decide promote` failures: it is an operator command)
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
                          then provider: in ~/.yakos-state/decision-policy.yml,
                          then none. A project .yakos.yml can only turn the
                          provider off (provider: none), never on.
    --shadow              Advisory call: on any failure exit 0 with
                          {"answer":null,"reason":...} so a hook proceeds.
    --timeout <duration>  Overall deadline (default 1.5s in both modes; max 10s).
    --session <id>        Session id for the per-session call cap
                          (default $YAKOS_SESSION_ID, else "default").
    --state-file <path>   Read the state from a file instead of stdin.
    --consume-state-file  Delete the --state-file once it has been read. Only a
                          shadow-state-*.json regular file of yours directly in
                          the state directory is ever deleted; any other path is
                          read and left alone. Used by hooks that hand the state
                          over in a private temp file.
    --sets-dir <dir>      Question-set directory (default <framework>/lib/decisions).
    --tag <label>         Label the call as not real traffic (e.g. smoke); compare
                          leaves tagged records out by default.
    --local <verdict>     pass | escalate: what the caller's own deterministic
                          heuristic decided for this event. Recorded in the
                          decision log beside the answer, never sent to the
                          provider (used by the supervisor shadow hook).
    --local-trigger <k>   Trigger kind behind an escalate verdict (logged only).
    --config <path>       .yakos.yml to read the decisions: block from
                          (default $CLAUDE_PROJECT_DIR/.yakos.yml, then ./.yakos.yml).
                          A project may only TIGHTEN the user-level ceiling in
                          ~/.yakos-state/decision-policy.yml (default: 2000
                          calls, $1/day, strict egress).

yakos decide compare <surface> [--json] [--log <path>] [--sets-dir <dir>]
             [--session <id>] [--exclude-session <id,id>] [--since <dur|time>]
             [--include-mock] [--include-tagged]
    Reads the decision log and prints how often the shadow verdict agreed with
    the local heuristic recorded beside it (shadow-mode records for the surface's
    current question-set hash only), plus fail-open counts, latency and cost.
    Mock-provider and --tag'ged (smoke) records are not counted unless asked, so
    the 200-decision sample gate reflects real traffic. --since takes a duration
    such as 72h or an RFC 3339 time. This is the evidence that gates promotion
    out of shadow mode.

yakos decide promote <surface> --report <eval report>
    Operator command: records a verified promotion for the surface's current
    question-set hash (required before a set may declare may_block: true).

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
	var providerFlag, timeoutFlag, sessionFlag, stateFile, setsDir, configPath, reportPath string
	var localVerdict, localTrigger, logFlag string
	var asJSON, consumeState, includeMock, includeTagged bool
	var tagFlag, excludeSessions, sinceFlag string
	fs := &cliflag.Set{Cmd: "decide", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--shadow", Kind: cliflag.Bool, Bool: &shadow},
		{Name: "--provider", Kind: cliflag.String, Str: &providerFlag, ValueDesc: "jev, mock, or none"},
		{Name: "--timeout", Kind: cliflag.String, Str: &timeoutFlag, ValueDesc: "a duration such as 1500ms"},
		{Name: "--session", Kind: cliflag.String, Str: &sessionFlag, ValueDesc: "a session id"},
		{Name: "--state-file", Kind: cliflag.String, Str: &stateFile, ValueDesc: "a path"},
		{Name: "--sets-dir", Kind: cliflag.String, Str: &setsDir, ValueDesc: "a directory"},
		{Name: "--config", Kind: cliflag.String, Str: &configPath, ValueDesc: "a path"},
		{Name: "--tag", Kind: cliflag.String, Str: &tagFlag, ValueDesc: "a label"},
		{Name: "--exclude-session", Kind: cliflag.String, Str: &excludeSessions, ValueDesc: "session ids, comma separated"},
		{Name: "--since", Kind: cliflag.String, Str: &sinceFlag, ValueDesc: "a duration or RFC 3339 time"},
		{Name: "--include-mock", Kind: cliflag.Bool, Bool: &includeMock},
		{Name: "--include-tagged", Kind: cliflag.Bool, Bool: &includeTagged},
		{Name: "--consume-state-file", Kind: cliflag.Bool, Bool: &consumeState},
		{Name: "--local", Kind: cliflag.String, Str: &localVerdict, ValueDesc: "pass or escalate"},
		{Name: "--local-trigger", Kind: cliflag.String, Str: &localTrigger, ValueDesc: "a trigger kind"},
		{Name: "--log", Kind: cliflag.String, Str: &logFlag, ValueDesc: "a decision-log path"},
		{Name: "--json", Kind: cliflag.Bool, Bool: &asJSON},
		{Name: "--report", Kind: cliflag.String, Str: &reportPath, ValueDesc: "an eval report path"},
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
	if len(rest) == 2 && rest[0] == "promote" {
		return decidePromote(env, rest[1], reportPath, setsDir)
	}
	if len(rest) == 2 && rest[0] == "compare" {
		opts := decision.CompareOptions{Session: sessionFlag, IncludeMock: includeMock, IncludeTagged: includeTagged}
		for _, x := range strings.Split(excludeSessions, ",") {
			if x = strings.TrimSpace(x); x != "" {
				opts.ExcludeSessions = append(opts.ExcludeSessions, x)
			}
		}
		if sinceFlag != "" {
			t, perr := parseSince(sinceFlag, time.Now())
			if perr != nil {
				fmt.Fprintf(env.Stderr, "decide compare: invalid --since %q (a duration such as 72h, or an RFC 3339 time)\n", sinceFlag)
				return decideExitUsage
			}
			opts.Since = t
		}
		return decideCompare(env, rest[1], logFlag, setsDir, asJSON, opts)
	}
	if localVerdict != "" && !decision.ValidLocalVerdict(localVerdict) {
		fmt.Fprintf(env.Stderr, "decide: invalid --local %q (pass or escalate)\n", localVerdict)
		return decideExitUsage
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
	stateDir := env.StateDir
	if stateDir == "" {
		stateDir = statepath.Dir()
	}
	// A handed-over state file holds raw, unredacted text: remove it on EVERY
	// exit from here on (surface off, config or question-set error, disabled,
	// provider failure, success). Usage errors above return before this point
	// and delete nothing. consumeStateFile only ever deletes the hook's own
	// temp file.
	if consumeState && stateFile != "" && stateFile != "-" {
		defer consumeStateFile(stateFile, stateDir, env.Stderr)
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

	paths := decision.StatePaths{Dir: stateDir}
	cfg, cerr := decision.LoadConfig(decideConfigPath(configPath, getenv))
	if cerr != nil {
		return fail(decision.ClassBadRequest, "config: %v", cerr)
	}
	// A project's .yakos.yml may only tighten the user-level policy.
	policy, perr := decision.LoadPolicy(paths.Policy())
	if perr != nil {
		fmt.Fprintf(env.Stderr, "decide: %v; using the default policy\n", perr)
	}
	cfg = decision.Tighten(cfg, policy)
	name, pwarn := decision.ResolveProvider(providerFlag, getenv, cfg, policy)
	if pwarn != "" {
		fmt.Fprintf(env.Stderr, "decide: %s\n", pwarn)
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
	eng := &decision.Engine{Provider: prov, Logger: decision.NewLogger(logPath), Egress: cfg.Egress,
		LocalVerdict: localVerdict, LocalTrigger: sanitizeTrigger(localTrigger), Tag: sanitizeTrigger(tagFlag)}
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
// decidePromote implements `yakos decide promote <surface> --report <file>`:
// an operator command that records a verified promotion for the surface's
// current question-set hash. It never exits 2.
func decidePromote(env decideEnv, surface, report, setsDir string) int {
	if !decision.ValidSurface(surface) || report == "" {
		fmt.Fprintln(env.Stderr, "decide promote: usage: yakos decide promote <surface> --report <eval report path>")
		return decideExitUsage
	}
	getenv := env.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if setsDir == "" {
		root := env.YakosRoot
		if r := getenv("YAKOS_ROOT"); r != "" {
			root = r
		}
		setsDir = filepath.Join(resolveLibRoot(root, env.Home, env.Stderr), "lib", "decisions")
	}
	set, err := decision.LoadSet(setsDir, surface)
	if err != nil {
		fmt.Fprintf(env.Stderr, "decide promote: %v\n", err)
		return decideExitUsage
	}
	stateDir := env.StateDir
	if stateDir == "" {
		stateDir = statepath.Dir()
	}
	p, err := decision.RecordPromotion(decision.StatePaths{Dir: stateDir}.Promotions(), surface, set.Hash, report, time.Now())
	if err != nil {
		fmt.Fprintf(env.Stderr, "decide promote: %v\n", err)
		return decideExitUsage
	}
	b, _ := json.Marshal(p)
	fmt.Fprintln(env.Stdout, string(b))
	return decideExitOK
}

// decideCompare implements `yakos decide compare <surface>`: shadow-vs-local
// agreement from the decision log. Read-only; never calls a provider.
func decideCompare(env decideEnv, surface, logPath, setsDir string, asJSON bool, opts decision.CompareOptions) int {
	if !decision.ValidSurface(surface) {
		fmt.Fprintf(env.Stderr, "decide compare: invalid surface name %q\n", surface)
		return decideExitUsage
	}
	getenv := env.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if setsDir == "" {
		root := env.YakosRoot
		if r := getenv("YAKOS_ROOT"); r != "" {
			root = r
		}
		setsDir = filepath.Join(resolveLibRoot(root, env.Home, env.Stderr), "lib", "decisions")
	}
	set, err := decision.LoadSet(setsDir, surface)
	if err != nil {
		fmt.Fprintf(env.Stderr, "decide compare: %v\n", err)
		return decideExitUsage
	}
	if logPath == "" {
		stateDir := env.StateDir
		if stateDir == "" {
			stateDir = statepath.Dir()
		}
		logPath = decision.StatePaths{Dir: stateDir}.Log()
	}
	rep, err := decision.Compare(logPath, set, opts)
	if err != nil {
		fmt.Fprintf(env.Stderr, "decide compare: %v\n", err)
		return decideExitUsage
	}
	if asJSON {
		b, _ := json.Marshal(rep)
		fmt.Fprintln(env.Stdout, string(b))
		return decideExitOK
	}
	rep.WriteText(env.Stdout)
	return decideExitOK
}

// consumeStateFile deletes a state file a hook handed over.
// It only ever deletes the hook's own temp files: directly inside the state
// directory, named shadow-state-*.json, a regular file (never a symlink) owned
// by the current user. Anything else is left alone and reported.
func consumeStateFile(path, stateDir string, stderr io.Writer) {
	clean := filepath.Clean(path)
	if ok, _ := filepath.Match("shadow-state-*.json", filepath.Base(clean)); !ok || filepath.Dir(clean) != filepath.Clean(stateDir) {
		fmt.Fprintf(stderr, "decide: --consume-state-file: %s is not a hook state file in %s; not deleted\n", path, stateDir)
		return
	}
	fi, err := os.Lstat(clean)
	if err != nil || !fi.Mode().IsRegular() || !decision.OwnedByCurrentUser(fi) {
		fmt.Fprintf(stderr, "decide: --consume-state-file: %s is not a regular file of yours; not deleted\n", path)
		return
	}
	_ = os.Remove(clean)
}

// parseSince accepts a duration back from now (72h) or an RFC 3339 instant.
func parseSince(v string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	return time.Parse(time.RFC3339, v)
}

// sanitizeTrigger keeps the logged trigger kind to a short token: the log is
// for aggregation, not for free text.
func sanitizeTrigger(s string) string {
	if len(s) > 40 {
		s = s[:40]
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

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
