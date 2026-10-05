package dispatch

// resolve.go contains shared agent and runtime resolution helpers used by
// both the one-shot (dispatch.go / Run) and streaming (stream.go / RunStream)
// paths.
//
// Keeping resolution in one place prevents the two paths from drifting — the
// original bug (PR #203 fixed Run but not RunStream) was caused by duplicate
// resolution blocks.  Any future change to resolution logic only needs to land
// here.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/projectcfg"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// resolveAgent finds the target agent for the given name from the composed
// roster.  Resolution order:
//
//	a) Exact match in the composed roster (specialist agent from lib/agents/*.md
//	   or <project>/.claude/agents/*.md).
//	b) If the name equals a known runtime identifier (claude/codex/agy)
//	   and is absent from the roster, synthesize a generic catch-all agent for
//	   that runtime.  This allows `dispatch claude "..."` and the console chat
//	   pane's default agent="claude" to work without requiring a claude.md file
//	   in lib/agents/.
//	c) Genuinely unknown name → error (unchanged behaviour).
//
// yakosRoot and project are included in the error message for debuggability.
func resolveAgent(roster []agentscompose.ComposedAgent, name, yakosRoot, project string) (*agentscompose.ComposedAgent, error) {
	for i := range roster {
		if roster[i].ID == name {
			return &roster[i], nil
		}
	}
	if agentscompose.IsKnownRuntime(name) {
		generic := agentscompose.GenericAgentForRuntime(name)
		return &generic, nil
	}
	return nil, fmt.Errorf("dispatch: agent %q not found in composed set (yakosRoot=%s, project=%s)",
		name, yakosRoot, project)
}

// ValidateAgentName checks whether name resolves to a known agent or a bare
// runtime catch-all (claude/codex/agy).  It is the pre-flight check
// used by the chat dispatch handler to return 400 before the 202 when the
// caller supplies an agent name that will never succeed.
//
// Resolution order mirrors resolveAgent:
//
//  a) Exact match in the composed roster.
//  b) Bare known-runtime name (catch-all).
//  c) Neither → returns a non-nil error with a message safe to surface to the
//     caller (no roster path leakage).
//
// When yakosRoot is empty the roster cannot be composed; in that case only
// the known-runtime catch-all (b) is checked.  If the name is also not a
// known runtime, nil is returned (validation is skipped rather than failing
// closed) so that callers without a yakosRoot configured do not break.
// This matches the behaviour of RunStream itself: with no yakosRoot, Compose
// returns an empty roster and only known-runtime names succeed.
func ValidateAgentName(name, yakosRoot, project string) error {
	if yakosRoot != "" {
		roster, err := agentscompose.Compose(yakosRoot, project)
		if err != nil {
			// Compose failure (e.g. unreadable agents dir): fall through to the
			// pass-through path below so a bare "claude" still works when the
			// roster directory is absent.  A genuinely unknown name passes through
			// — validation cannot proceed without a roster.
			roster = nil
		}
		if roster != nil {
			_, resolveErr := resolveAgent(roster, name, yakosRoot, project)
			if resolveErr == nil {
				return nil // found in roster or known-runtime catch-all
			}
			// resolveAgent only errors on case (c): name unknown.
			return fmt.Errorf("unknown agent %q; not in roster and not a known runtime", name)
		}
	}
	// yakosRoot empty or roster compose failed: known-runtime is always valid;
	// anything else passes through (cannot validate without a roster).
	if agentscompose.IsKnownRuntime(name) {
		return nil
	}
	// No roster available: skip validation (cannot determine validity).
	return nil
}

// ---- runtime resolution (K-132) ---------------------------------------------
//
// The Go dispatcher used to pick a runtime from only an explicit override or an
// agent named after a runtime, falling back to claude, so an agent's
// `runtime:` pin, the project's .yakos.yml and the operator's default were all
// ignored (K-127). The chain below is the port of cli/lib/dispatch.sh
// (278-331) and cli/lib/project-config.sh (yk_pcfg_resolve_runtime), highest
// precedence first:
//
//  1. an explicit runtime (--runtime, Params.Runtime, a console pane set to a
//     specific runtime)                                         "override"
//  2. the agent's name, when it is a runtime and the agent has no pin of its
//     own (`dispatch codex "..."`)                              "agent-name"
//  3. the agent's frontmatter `runtime:`                        "frontmatter"
//  4. .yakos.yml per-domain[<agent domain>]                     "per-domain"
//  5. .yakos.yml default-runtime                                "project-default"
//  6. YAKOS_RUNTIME, supplied by the CLI one-shot path only     "env"
//  7. ~/.yakos-state/default-runtime                            "state-default"
//  8. claude                                                    "default"
//
// The candidate chain is that choice, then the agent's runtime-fallback, then
// .yakos.yml default-fallback. The first candidate that exists in the adapter
// registry, has its CLI on PATH and looks signed in wins ("fallback" when it is
// not the first). If none passes, dispatch fails fast and names why each was
// skipped, instead of starting a process that cannot work.
//
// An EXPLICIT choice (rules 1 and 2: the operator named the runtime) does not
// fall back. It is operator intent, including intent about where the task is
// sent, and answering from another vendor instead would be the silent switch
// the operator reported. If the named runtime cannot run, dispatch fails with an
// ExplicitRuntimeError naming the runtime, why, and the fallbacks it did not
// use. The CLI's --runtime-fallback opts in (Request.RuntimeFallbackOptIn).
// DELIBERATE DIVERGENCE from cli/lib/dispatch.sh, which walks the fallback lists
// for an explicit --runtime too (K-143 encodes it in the parity matrix).
// Pins (rule 3) and project defaults (rules 4 to 8) keep walking the lists, as
// bash does.
//
// The daemon never reads YAKOS_RUNTIME: only the CLI path passes it, in
// Request.RuntimeEnvDefault, and it ranks below the project file exactly where
// bash reads the variable.

// Values of Result.RuntimeChosenBy and the dispatch-log runtime_chosen_by field.
const (
	RuntimeByOverride       = "override"
	RuntimeByAgentName      = "agent-name"
	RuntimeByFrontmatter    = "frontmatter"
	RuntimeByPerDomain      = "per-domain"
	RuntimeByProjectDefault = "project-default"
	RuntimeByEnv            = "env"
	RuntimeByStateDefault   = "state-default"
	RuntimeByDefault        = "default"
	RuntimeByFallback       = "fallback"
)

// autoRuntime is the explicit spelling of "no override" (the console runtime
// select's `auto`).
const autoRuntime = "auto"

// SkippedRuntime is a chain candidate that was passed over, and why.
type SkippedRuntime struct {
	Runtime string
	Reason  string
}

// RuntimeChoice is the outcome of runtime resolution.
type RuntimeChoice struct {
	// Runtime is the adapter that will run the dispatch.
	Runtime string
	// ChosenBy says which rule picked it (the RuntimeBy* constants).
	ChosenBy string
	// FallbackFrom is the preferred runtime that could not be used; set only
	// when ChosenBy is RuntimeByFallback.
	FallbackFrom string
	// Skipped lists every candidate passed over before Runtime, in chain order.
	Skipped []SkippedRuntime
}

// RouteQuery asks which runtime an agent would run on.
type RouteQuery struct {
	YakosRoot string
	Project   string
	Agent     string
	// Override is an explicit runtime; "" and "auto" mean none.
	Override string
	// EnvDefault is the ambient YAKOS_RUNTIME value. Only the CLI one-shot path
	// sets it; every daemon transport leaves it empty.
	EnvDefault string
	// FallbackOptIn are the runtimes the operator listed to fall back to (the
	// CLI's --runtime-fallback). Only the CLI sets it.
	FallbackOptIn []string
}

// probeResult is the outcome of checking one chain candidate.
type probeResult struct {
	OK     bool
	Reason string
}

// runtimeProbe decides whether a candidate can run now. It ends when ctx does.
// Tests replace it.
var runtimeProbe = defaultRuntimeProbe

// stateDefaultRuntime reads ~/.yakos-state/default-runtime (the preference
// `yakos auth set-default` writes) and returns it with a warning when the file
// exists but is not trusted (see auth.ReadDefaultRuntime). Tests replace it.
var stateDefaultRuntime = func() (name, warning string) { return auth.ReadDefaultRuntime(statepath.Dir()) }

// routeLog receives the one-line notices the resolver prints: a fallback, an
// ignored default, a broken .yakos.yml. Tests replace it.
var routeLog io.Writer = os.Stderr

// defaultRuntimeProbe is the production probe: the adapter must exist, its CLI
// must be on PATH, and the runtime must look signed in (auth.ProbeRuntime).
// agy.Available is PATH-only, so without the sign-in half a signed-out agy
// would be selected and then fail (D13). The answer is reused for probeTTL.
func defaultRuntimeProbe(ctx context.Context, name string) probeResult {
	if r, ok := cachedProbe(name); ok {
		return r
	}
	r := probeOnce(ctx, name)
	if ctx.Err() == nil { // an answer cut short by a cancel is not an answer
		storeProbe(name, r)
	}
	return r
}

// probeOnce asks the machine, uncached. Tests replace it to count the asks.
var probeOnce = probeMachine

func probeMachine(ctx context.Context, name string) probeResult {
	adapter, err := runtime.Resolve(name)
	if err != nil {
		return probeResult{Reason: unsupportedReasonFor(name)}
	}
	p := auth.ProbeRuntime(ctx, name)
	switch {
	case !p.CLIPresent:
		reason := "CLI not found on PATH"
		if p.CLIHint != "" {
			reason += "; " + p.CLIHint
		}
		return probeResult{Reason: reason}
	case !p.Authed:
		reason := "not signed in"
		if p.AuthHint != "" {
			reason += "; " + p.AuthHint
		}
		if p.Note != "" {
			reason += " (" + p.Note + ")"
		}
		return probeResult{Reason: reason}
	case !adapter.Available(ctx):
		return probeResult{Reason: "the adapter reports it unavailable"}
	}
	return probeResult{OK: true}
}

// probeTTL is how long a runtime's probe answer is reused. What the probe reads
// (PATH, the environment, a few files and, for agy, the OS keyring) does not
// change between the turns of a conversation, and the keyring read can be slow,
// so every dispatch of a long-lived daemon should not pay for it again. The
// price is that a daemon notices an install or a sign-in up to this long after
// it happens; a one-shot CLI process never reuses an answer. Tests set it to 0.
//
// An answer is only reused while the process environment is the same (PATH,
// HOME, CODEX_HOME and every credential variable the probe reads are part of
// it): a daemon's environment does not change, so that costs nothing there, and
// a change of it, which only a test or an operator's os.Setenv makes, is never
// answered from the past.
var probeTTL = 30 * time.Second

// probeClock is the clock the cache reads. Tests replace it.
var probeClock = time.Now

type cachedProbeEntry struct {
	res probeResult
	at  time.Time
}

var probeCache = struct {
	sync.Mutex
	m map[string]cachedProbeEntry
}{m: make(map[string]cachedProbeEntry)}

// probeKey names a cache entry: the runtime plus a digest of the environment.
func probeKey(name string) string {
	env := os.Environ()
	sort.Strings(env)
	h := sha256.New()
	for _, e := range env {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	return name + "\x00" + hex.EncodeToString(h.Sum(nil))
}

func cachedProbe(name string) (probeResult, bool) {
	if probeTTL <= 0 {
		return probeResult{}, false
	}
	key := probeKey(name)
	probeCache.Lock()
	defer probeCache.Unlock()
	e, ok := probeCache.m[key]
	if !ok || probeClock().Sub(e.at) >= probeTTL {
		return probeResult{}, false
	}
	return e.res, true
}

func storeProbe(name string, r probeResult) {
	if probeTTL <= 0 {
		return
	}
	key := probeKey(name)
	probeCache.Lock()
	probeCache.m[key] = cachedProbeEntry{res: r, at: probeClock()}
	probeCache.Unlock()
}

// resetProbeCache forgets every cached answer (tests).
func resetProbeCache() {
	probeCache.Lock()
	probeCache.m = make(map[string]cachedProbeEntry)
	probeCache.Unlock()
}

// unsupportedReason is why a runtime id that parses but has no Go adapter
// (claude-sdk, antigravity-sdk, plugin ids) is skipped.
const unsupportedReason = "not supported by the Go dispatcher (bash only)"

// unsupportedReasonFor is unsupportedReason, with the migration hint for the
// one runtime that used to be supported and was removed.
func unsupportedReasonFor(name string) string {
	if name == "gemini" {
		return "gemini was removed; use agy"
	}
	return unsupportedReason
}

// chainInput is everything the chain needs, already loaded, so the resolver
// itself is a pure function of its inputs.
type chainInput struct {
	override     string // explicit runtime; "" = none
	agentName    string
	agent        *agentscompose.ComposedAgent // nil when the roster is unavailable
	project      projectcfg.Config
	envDefault   string
	stateDefault string
	// optIn are fallbacks the operator listed for this dispatch (the CLI's
	// --runtime-fallback). An explicit runtime uses only these; any other
	// choice tries them after the agent's and the project's own lists.
	optIn []string
}

type candidate struct{ name, by string }

// explicit reports whether the operator named this runtime: an override, or a
// bare runtime name used as the agent (`yakos dispatch codex "..."`).
func (c candidate) explicit() bool {
	return c.by == RuntimeByOverride || c.by == RuntimeByAgentName
}

func supportedRuntime(name string) bool {
	_, err := runtime.Resolve(name)
	return err == nil
}

// preferred picks the first runtime by the precedence list above. The three
// ambient defaults (project default-runtime, env, state file) are skipped when
// they name a runtime this dispatcher cannot run, so a stale default can never
// make an agent that never asked for it undispatchable; the second return is
// the notices to log for that.
func preferred(in chainInput) (candidate, []string) {
	var notes []string
	agentRuntime, domain := "", ""
	if in.agent != nil {
		agentRuntime, domain = in.agent.Runtime, in.agent.Domain
	}
	switch {
	case in.override != "":
		return candidate{in.override, RuntimeByOverride}, nil
	case agentRuntime == "" && agentscompose.IsKnownRuntime(in.agentName):
		return candidate{in.agentName, RuntimeByAgentName}, nil
	case agentRuntime != "":
		return candidate{agentRuntime, RuntimeByFrontmatter}, nil
	}
	if name, src := in.project.RuntimeFor(domain); name != "" {
		by := RuntimeByProjectDefault
		if src == projectcfg.SourcePerDomain {
			by = RuntimeByPerDomain
		}
		// A per-domain rule is a deliberate choice for this agent: honour it
		// as a pin. Only the project-wide default is skipped when unusable.
		if by == RuntimeByPerDomain || supportedRuntime(name) {
			return candidate{name, by}, nil
		}
		notes = append(notes, fmt.Sprintf("ignoring default-runtime %q in .yakos.yml: %s", name, unsupportedReasonFor(name)))
	}
	if in.envDefault != "" {
		if supportedRuntime(in.envDefault) {
			return candidate{in.envDefault, RuntimeByEnv}, notes
		}
		notes = append(notes, fmt.Sprintf("ignoring YAKOS_RUNTIME %q: %s", in.envDefault, unsupportedReasonFor(in.envDefault)))
	}
	if in.stateDefault != "" {
		if supportedRuntime(in.stateDefault) {
			return candidate{in.stateDefault, RuntimeByStateDefault}, notes
		}
		notes = append(notes, fmt.Sprintf("ignoring default-runtime %q in the state dir: %s", in.stateDefault, unsupportedReasonFor(in.stateDefault)))
	}
	return candidate{"claude", RuntimeByDefault}, notes
}

// buildChain returns the ordered, de-duplicated candidates. For a runtime the
// operator named it is that runtime and then only the fallbacks the operator
// listed for this dispatch (optIn), if any. Otherwise it is the preferred
// runtime, then the agent's runtime-fallback, then the project default-fallback,
// then optIn.
func buildChain(in chainInput) ([]candidate, []string) {
	first, notes := preferred(in)
	chain := []candidate{first}
	add := func(name string) {
		for _, c := range chain {
			if c.name == name {
				return
			}
		}
		chain = append(chain, candidate{name, RuntimeByFallback})
	}
	if !first.explicit() {
		if in.agent != nil {
			for _, f := range in.agent.RuntimeFallback {
				add(f)
			}
		}
		for _, f := range in.project.DefaultFallback {
			add(f)
		}
	}
	for _, f := range in.optIn {
		add(f)
	}
	return chain, notes
}

// implicitFallbacks are the runtimes the agent's runtime-fallback and the
// project's default-fallback name, in order and without repeats, minus the
// runtime already chosen. For an explicit choice they are the ones NOT used.
func implicitFallbacks(in chainInput, chosen string) []string {
	var out []string
	add := func(name string) {
		if name == chosen {
			return
		}
		for _, o := range out {
			if o == name {
				return
			}
		}
		out = append(out, name)
	}
	if in.agent != nil {
		for _, f := range in.agent.RuntimeFallback {
			add(f)
		}
	}
	for _, f := range in.project.DefaultFallback {
		add(f)
	}
	return out
}

// chooseRuntime walks the chain. probe == nil skips the availability check
// (used to learn the preferred runtime without touching the machine). A
// cancelled ctx ends the walk with its error.
func chooseRuntime(ctx context.Context, in chainInput, probe func(context.Context, string) probeResult) (RuntimeChoice, []string, error) {
	chain, notes := buildChain(in)
	var choice RuntimeChoice
	for i, c := range chain {
		if !supportedRuntime(c.name) {
			// An explicit override that names no runtime is an error, not a
			// reason to quietly run something else.
			if c.by == RuntimeByOverride {
				_, err := runtime.Resolve(c.name)
				return choice, notes, fmt.Errorf("dispatch: %w", err)
			}
			choice.Skipped = append(choice.Skipped, SkippedRuntime{c.name, unsupportedReasonFor(c.name)})
			continue
		}
		if probe != nil {
			if p := probe(ctx, c.name); !p.OK {
				// A probe cut short by a cancel says "no" because it was
				// stopped, not because the runtime is unusable: end the walk
				// instead of reporting that as a skipped runtime.
				if err := ctx.Err(); err != nil {
					return choice, notes, err
				}
				choice.Skipped = append(choice.Skipped, SkippedRuntime{c.name, p.Reason})
				continue
			}
		}
		choice.Runtime, choice.ChosenBy = c.name, c.by
		if i > 0 {
			choice.ChosenBy, choice.FallbackFrom = RuntimeByFallback, chain[0].name
		}
		return choice, notes, nil
	}
	if chain[0].explicit() && len(choice.Skipped) > 0 {
		return choice, notes, &ExplicitRuntimeError{
			Runtime:   choice.Skipped[0].Runtime,
			Reason:    choice.Skipped[0].Reason,
			AlsoTried: choice.Skipped[1:],
			NotUsed:   implicitFallbacks(in, chain[0].name),
		}
	}
	return choice, notes, noRuntimeError(in.agentName, choice.Skipped)
}

// ExplicitRuntimeError is the failure of a runtime the operator named: with
// --runtime, a console pane's runtime, the runtime parameter of an API call or
// MCP tool, or a bare runtime name used as the agent. Such a choice does not
// fall back to another vendor behind the operator's back; this error says what
// stopped it and what was deliberately not used. Only the CLI can opt in
// (--runtime-fallback), so the hint for that is the CLI's to add.
type ExplicitRuntimeError struct {
	// Runtime is the runtime that was named, and Reason why it cannot run.
	Runtime string
	Reason  string
	// AlsoTried are fallbacks the operator opted into that were skipped too.
	AlsoTried []SkippedRuntime
	// NotUsed are the runtimes of the agent's runtime-fallback and the project's
	// default-fallback, which an explicit runtime does not use.
	NotUsed []string
}

func (e *ExplicitRuntimeError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dispatch: runtime %s was requested explicitly but cannot run: %s", e.Runtime, e.Reason)
	for _, sk := range e.AlsoTried {
		fmt.Fprintf(&b, "; %s: %s", sk.Runtime, sk.Reason)
	}
	if len(e.NotUsed) > 0 {
		fmt.Fprintf(&b, ". Not falling back to %s: an explicit runtime does not use the agent's or the project's fallback list",
			strings.Join(e.NotUsed, ", "))
	}
	return b.String()
}

// RunnableFallbacks are the runtimes in NotUsed that this dispatcher can run, in
// order: the ones the CLI's --runtime-fallback accepts. An agent's or a
// project's fallback list may name a bash-only runtime (claude-sdk), which the
// flag rejects as unknown, so a hint built from NotUsed could suggest a command
// that fails.
func (e *ExplicitRuntimeError) RunnableFallbacks() []string {
	var out []string
	for _, name := range e.NotUsed {
		if supportedRuntime(name) {
			out = append(out, name)
		}
	}
	return out
}

// AsExplicitRuntimeError reports whether err is (or wraps) an
// *ExplicitRuntimeError, and returns it.
func AsExplicitRuntimeError(err error) (*ExplicitRuntimeError, bool) {
	var e *ExplicitRuntimeError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// noRuntimeError is the fail-fast error when nothing in the chain can run.
func noRuntimeError(agent string, skipped []SkippedRuntime) error {
	if len(skipped) == 0 {
		return fmt.Errorf("dispatch: no runtime available for agent %q", agent)
	}
	parts := make([]string, len(skipped))
	for i, sk := range skipped {
		parts[i] = sk.Runtime + ": " + sk.Reason
	}
	return fmt.Errorf("dispatch: no runtime available for agent %q: %s. "+
		"Add a runtime-fallback to the agent or default-fallback to .yakos.yml to fall back automatically, or pass --runtime",
		agent, strings.Join(parts, "; "))
}

// loadChainInput gathers the inputs for one agent. The project file and the
// state default are read here, once per resolution. A broken .yakos.yml is
// reported (logWarnings) only by the resolution that precedes real work, so a
// handler that merely asks "what would run?" does not repeat the warning on
// every request.
func loadChainInput(agent *agentscompose.ComposedAgent, agentName, project, override, envDefault string, optIn []string, logWarnings bool) chainInput {
	pcfg, warns := projectcfg.Load(project)
	stateDefault, stateWarn := stateDefaultRuntime()
	if stateWarn != "" {
		warns = append(warns, stateWarn)
	}
	if logWarnings {
		for _, w := range warns {
			fmt.Fprintf(routeLog, "yakos dispatch: %s\n", w)
		}
	}
	override = strings.TrimSpace(override)
	if override == autoRuntime {
		override = ""
	}
	return chainInput{
		override:     override,
		agentName:    agentName,
		agent:        agent,
		project:      pcfg,
		envDefault:   strings.TrimSpace(envDefault),
		stateDefault: stateDefault,
		optIn:        optIn,
	}
}

// agentForQuery finds the agent a RouteQuery names: the roster entry, the
// generic catch-all for a runtime name, or nil when there is no roster or no
// such agent (resolution then proceeds from the defaults).
func agentForQuery(q RouteQuery) *agentscompose.ComposedAgent {
	if q.YakosRoot != "" {
		if roster, err := agentscompose.Compose(q.YakosRoot, q.Project); err == nil {
			if a, err := resolveAgent(roster, q.Agent, q.YakosRoot, q.Project); err == nil {
				return a
			}
		}
	}
	if agentscompose.IsKnownRuntime(q.Agent) {
		g := agentscompose.GenericAgentForRuntime(q.Agent)
		return &g
	}
	return nil
}

// ResolveRuntime resolves the runtime exactly as Run and RunStream will,
// including the availability and sign-in checks and the fallback chain. The CLI
// uses it to print the runtime it is about to dispatch to. It prints nothing
// about fallbacks itself.
func ResolveRuntime(ctx context.Context, q RouteQuery) (RuntimeChoice, error) {
	in := loadChainInput(agentForQuery(q), q.Agent, q.Project, q.Override, q.EnvDefault, q.FallbackOptIn, false)
	choice, _, err := chooseRuntime(ctx, in, runtimeProbe)
	return choice, err
}

// PreferredRuntime returns the runtime a request would run on if every runtime
// were installed and signed in: the first supported entry of the chain, with no
// check of the machine. Handlers use it to validate a request (a model is valid
// per runtime) before the work is queued; the real choice, with probing and
// fallback, is made again by Run and RunStream.
func PreferredRuntime(q RouteQuery) (RuntimeChoice, error) {
	in := loadChainInput(agentForQuery(q), q.Agent, q.Project, q.Override, q.EnvDefault, q.FallbackOptIn, false)
	choice, _, err := chooseRuntime(context.Background(), in, nil)
	return choice, err
}

// RosterRuntimes reports the runtime each roster agent is headed for when
// everything is available: its pin if it has one, else the project and state
// defaults, else claude. It reads the project file and state default once. It
// is for display (the skills popover); a pin is reported as written, even one
// this dispatcher cannot run.
func RosterRuntimes(roster []agentscompose.ComposedAgent, project string) map[string]string {
	in := loadChainInput(nil, "", project, "", "", nil, false)
	out := make(map[string]string, len(roster))
	for i := range roster {
		in.agent, in.agentName = &roster[i], roster[i].ID
		first, _ := preferred(in)
		out[roster[i].ID] = first.name
	}
	return out
}

// ---- model resolution (K-132) -------------------------------------------------

// modelCheck is the verdict on an explicitly requested model for one runtime.
type modelCheck struct {
	// resolved is the model id to use; "" means the harness default (no model
	// flag).
	resolved string
	// ok is false when the request cannot be honoured on this runtime.
	ok bool
	// unmapped is true when the request was an alias the table has no entry for
	// on this runtime. It is ok, resolves to "", and the caller warns once.
	unmapped bool
}

// checkModel resolves and validates an explicitly requested model (an alias or
// an id) for runtime rt. It prints nothing, so a handler can call it before the
// work is queued.
//
//   - claude: the four tiers, after alias expansion (ValidateTier).
//   - codex, agy: an alias, expanded through that runtime's column (an alias
//     with no mapping is "use the harness default", not an error), or a model
//     id in the safe alphabet. A bare Claude tier (haiku, sonnet, opus, fable)
//     is refused: it matches the id alphabet but names no codex or agy model, so
//     passing it through would only fail later inside the CLI. An alias is the
//     portable spelling.
func checkModel(rt, model string) modelCheck {
	if rt == "claude" {
		r := runtime.ResolveAlias(model)
		return modelCheck{resolved: r, ok: runtime.ValidateTier(r)}
	}
	if runtime.IsClaudeTier(model) {
		return modelCheck{}
	}
	if runtime.IsAlias(model) {
		id, found := runtime.AliasModelFor(rt, model)
		if !found {
			return modelCheck{ok: true, unmapped: true}
		}
		return modelCheck{resolved: id, ok: runtime.ValidateModelFor(rt, id) && !runtime.IsClaudeTier(id)}
	}
	return modelCheck{resolved: model, ok: runtime.ValidateModelFor(rt, model)}
}

// CheckModelOverride reports whether an explicitly requested model is acceptable
// on runtime rt and what it resolves to (see checkModel; "" with ok true means
// the harness default). Callers that validate a request before queueing it use
// it; Run and RunStream apply the same rule.
func CheckModelOverride(rt, model string) (resolved string, ok bool) {
	c := checkModel(rt, model)
	return c.resolved, c.ok
}

// invalidModelError is the error for an explicit model that does not fit rt.
// The claude text is unchanged from before runtimes had their own models.
func invalidModelError(rt, model string) error {
	if rt == "claude" {
		return fmt.Errorf("dispatch: invalid model tier %q (must be %s)", model, runtime.ModelHint(rt))
	}
	return fmt.Errorf("dispatch: invalid model %q for runtime %s (want %s)", model, rt, runtime.ModelHint(rt))
}

// warnUnmapped prints the one-line notice for an alias with no mapping.
func warnUnmapped(alias, rt string) {
	fmt.Fprintf(routeLog, "yakos dispatch: WARN: alias %s has no %s mapping; using harness default\n", alias, rt)
}

// modelChoice is the model a dispatch will use.
type modelChoice struct {
	model    string // "" = no model flag; the harness picks
	chosenBy string // override | eval | frontmatter
	explicit bool   // true when a pin put a model here (never for a default)
}

// agentModelFor is the model an agent's frontmatter pins for runtime rt, or ""
// when it pins none that means anything there. For claude that is the resolved
// tier (ComposedAgent.Model). For any other runtime it is ModelRaw: a
// semantic alias is expanded through that runtime's column (an alias with no
// mapping warns once and yields ""), and an id passes through when it is safe.
// A bare Claude tier name (opus) means nothing to codex or agy and is ignored,
// as a non-Claude id has always been ignored on claude.
func agentModelFor(rt string, a *agentscompose.ComposedAgent) string {
	if a == nil {
		return ""
	}
	if rt == "claude" {
		return a.Model
	}
	raw := a.ModelRaw
	if raw == "" || runtime.IsClaudeTier(raw) {
		return ""
	}
	c := checkModel(rt, raw)
	if c.unmapped {
		warnUnmapped(raw, rt)
	}
	if !c.ok {
		return ""
	}
	return c.resolved
}

// resolveModel applies the precedence override > agent frontmatter > the
// runtime's default, per runtime. Only a pin (override or frontmatter) puts a
// model on a codex or agy command line; their default is no model at all.
// viaFallback is true when rt was reached by falling back: an explicit model
// that does not fit the fallback runtime is then dropped (with a notice) and the
// choice continues with the agent's own pin for that runtime and then its
// default, rather than failing a request whose runtime was already changed
// under it.
func resolveModel(rt string, viaFallback bool, override, evalRunID string, a *agentscompose.ComposedAgent) (modelChoice, error) {
	mc := modelChoice{chosenBy: "frontmatter"}
	if evalRunID != "" {
		mc.chosenBy = "eval"
	}
	if override != "" {
		c := checkModel(rt, override)
		if c.ok {
			if c.unmapped {
				warnUnmapped(override, rt)
			}
			mc.model, mc.chosenBy, mc.explicit = c.resolved, "override", c.resolved != ""
			return mc, nil
		}
		if !viaFallback {
			return modelChoice{}, invalidModelError(rt, override)
		}
		fmt.Fprintf(routeLog, "yakos dispatch: model %q does not fit fallback runtime %s; ignoring it\n", override, rt)
	}
	if m := agentModelFor(rt, a); m != "" {
		mc.model, mc.explicit = m, true
		return mc, nil
	}
	mc.model = runtime.DefaultModelFor(rt)
	return mc, nil
}

// ---- the shared routing step --------------------------------------------------

// routeInput is what Run and RunStream hand to routeDispatch.
type routeInput struct {
	YakosRoot, Project, Agent string
	RuntimeOverride           string
	RuntimeEnvDefault         string
	RuntimeFallbackOptIn      []string
	ModelOverride             string
	EvalRunID                 string
}

// routed is the result of routing one dispatch.
type routed struct {
	Agent           *agentscompose.ComposedAgent
	Adapter         runtime.Adapter
	Runtime         string
	RuntimeChosenBy string
	FallbackFrom    string
	Model           string
	ModelChosenBy   string
	// ModelExplicit is true when the model came from the caller, the agent's
	// frontmatter or a budget clamp, as opposed to the runtime default. Only
	// then does a chat adapter pass the model to its CLI.
	ModelExplicit bool
}

// routeDispatch is the one place that turns (agent, overrides, project state)
// into (agent, runtime, model). Run and RunStream both call it, so the one-shot
// and streaming paths cannot drift apart (the PR #203 class of bug).
func routeDispatch(ctx context.Context, in routeInput) (*routed, error) {
	roster, err := agentscompose.Compose(in.YakosRoot, in.Project)
	if err != nil {
		return nil, fmt.Errorf("dispatch: compose agents: %w", err)
	}
	// See resolveAgent for the order (specialist, generic runtime, error).
	agent, err := resolveAgent(roster, in.Agent, in.YakosRoot, in.Project)
	if err != nil {
		return nil, err
	}

	ci := loadChainInput(agent, in.Agent, in.Project, in.RuntimeOverride, in.RuntimeEnvDefault, in.RuntimeFallbackOptIn, true)
	choice, notes, err := chooseRuntime(ctx, ci, runtimeProbe)
	for _, n := range notes {
		fmt.Fprintf(routeLog, "yakos dispatch: %s\n", n)
	}
	if err != nil {
		return nil, err
	}
	if choice.ChosenBy == RuntimeByFallback {
		fmt.Fprintf(routeLog, "yakos dispatch: preferred runtime unavailable [%s]; falling back to '%s'\n",
			skippedSummary(choice.Skipped), choice.Runtime)
	}
	adapter, err := runtime.Resolve(choice.Runtime)
	if err != nil {
		return nil, fmt.Errorf("dispatch: %w", err)
	}

	mc, err := resolveModel(choice.Runtime, choice.ChosenBy == RuntimeByFallback, in.ModelOverride, in.EvalRunID, agent)
	if err != nil {
		return nil, err
	}

	// A user-level max_model ceiling (K-119) lowers a dearer model, whether it
	// came from a project's supervisor.model or the agent's frontmatter. The
	// ceiling is expressed in Claude tiers, so it applies to claude only.
	if choice.Runtime == "claude" {
		if clamped, note := budget.ClampModel(in.Agent, mc.model, budget.Options{}); note != "" {
			mc.model, mc.explicit = clamped, true
			fmt.Fprintf(os.Stderr, "yakos budget: %s\n", note)
		}
	}

	return &routed{
		Agent:           agent,
		Adapter:         adapter,
		Runtime:         choice.Runtime,
		RuntimeChosenBy: choice.ChosenBy,
		FallbackFrom:    choice.FallbackFrom,
		Model:           mc.model,
		ModelChosenBy:   mc.chosenBy,
		ModelExplicit:   mc.explicit,
	}, nil
}

// skippedSummary renders "agy: not signed in; run: yakos auth login agy, ...".
func skippedSummary(skipped []SkippedRuntime) string {
	parts := make([]string, len(skipped))
	for i, sk := range skipped {
		parts[i] = sk.Runtime + ": " + sk.Reason
	}
	return strings.Join(parts, ", ")
}

// ParseRuntimeList parses a comma-separated list of runtime ids (the value of
// the CLI's --runtime-fallback). Every entry must be a runtime this dispatcher
// can run; the empty string is the empty list.
func ParseRuntimeList(raw string) ([]string, error) {
	var out []string
	for _, f := range strings.Split(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, err := runtime.Resolve(f); err != nil {
			return nil, err
		}
		dup := false
		for _, o := range out {
			dup = dup || o == f
		}
		if !dup {
			out = append(out, f)
		}
	}
	return out, nil
}
