// Package budget implements per-agent budgets with a hard stop (K-119, K-136).
//
// An agent gets a token limit, a dollar limit, or both, over a window (calendar
// month in local time, or lifetime). Tokens are the primary unit (K-136): the
// aggregate sums the reported input, output and cache tokens of every run, for
// every model, and `limit_tokens` trips on that total whatever the run was billed.
// Dollars are summed from the dispatch-log cost fields but count only for runs
// billed per API call (cost.CountsAsSpend): a subscription or local run's
// figure is never spend, so `limit_usd` does not move for it. Below the warning
// percentage the agent is "ok", from it "warning", and at 100% of EITHER limit
// the agent is in "hard_stop" and new dispatches are refused until the limit is
// raised, the operator runs `yakos budget reset <agent>`, or the window rolls
// over. A run already in flight is never killed.
//
// The policy lives in the user-level state directory, never in a project: a
// project .yakos.yml may only LOWER a limit (same trust rule as the decision
// provider policy, ADR-0009).
package budget

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// PolicyFileName is the user-level budget policy, kept in the state directory.
const PolicyFileName = "budget-policy.yml"

// Window is the accounting window of a limit.
type Window string

const (
	Monthly  Window = "monthly"
	Lifetime Window = "lifetime"
)

// DefaultWarnPct is the warning threshold when none is configured.
const DefaultWarnPct = 80

// Built-in default limits (USD per month) for the two agents that dominate
// observed spend. Basis (dispatch-log, efficiency audit 2026-10-01):
//
//	supervisor: $58 in Sep 2026 after the haiku switch (107 calls); $800-900 per
//	            month before it. $100/month is about 1.7x the current rate.
//	librarian:  $22 in May, $159 in Jun (36% of runs failed); none since. $40
//	            is about 1.8x the healthy May figure.
//
// An operator overrides either in budget-policy.yml (limit_usd: 0 turns it off).
var builtinLimits = map[string]float64{
	"supervisor": 100,
	"librarian":  40,
}

// Built-in token limits (tokens per month, K-136), the same two agents. They
// exist so the two built-in budgets still trip for an operator on a subscription,
// whose runs cost no dollars (cost.CountsAsSpend) and so never move limit_usd. A
// token limit counts every run of the agent whatever its billing.
//
// Each is the dollar ceiling above converted at the Sonnet reference rate of $3
// per million tokens (efficiency audit 2026-10-01: sonnet $3 input and $15 output
// per million tokens, cache reads at 0.1x, cache writes at about 1.25x), rounded
// down to a whole million:
//
//	supervisor: $100 / ($3 per 1M tokens) = 33.3M tokens  ->  33,000,000
//	librarian:  $40  / ($3 per 1M tokens) = 13.3M tokens  ->  13,000,000
//
// Observed on the operator's dispatch-log (all four token kinds summed, cost as
// the CLI reported it): the supervisor's blended cost was $2.76 to $3.83 per
// million tokens in May, June, Sep and Oct 2026, so the conversion holds for it
// (Sep, a healthy month, used 15.1M tokens for $58; May and June used 263M and
// 292M). The librarian ran on a dearer model, $7.77 and $10.34 per million tokens
// in May and June, so its token ceiling is looser than its dollar ceiling (May,
// healthy, used 2.9M tokens; June, the runaway, 15.4M). TestBuiltinTokenLimits_
// FollowTheDollarCeilings keeps the numbers tied to this arithmetic.
//
// An operator overrides either in budget-policy.yml (limit_tokens: 0 turns it
// off; the dollar limit is separate).
var builtinTokenLimits = map[string]int64{
	"supervisor": 33_000_000,
	"librarian":  13_000_000,
}

// builtinTokenRateUSDPerMTok is the reference rate the built-in token limits are
// derived from: the Sonnet fresh-input price, dollars per million tokens.
const builtinTokenRateUSDPerMTok = 3.0

// builtinStopFactor lets an agent run past its limit up to factor x limit
// before `yakos dispatch` refuses it. Only the supervisor has one: the
// supervisor-stream hook refuses ROUTINE launches at 1x (state hard_stop) but
// keeps launching HIGH-risk ones, so the dispatch it forks must not refuse
// them between 1x and the ceiling. No flag or env var carries the exemption:
// the dispatch-level stop is simply later for this agent.
var builtinStopFactor = map[string]float64{"supervisor": 2}

// BuiltinLimit returns the built-in default monthly limit for agent, if any.
func BuiltinLimit(agent string) (float64, bool) {
	v, ok := builtinLimits[agent]
	return v, ok
}

// BuiltinTokenLimit returns the built-in default monthly token limit for agent,
// if any.
func BuiltinTokenLimit(agent string) (int64, bool) {
	v, ok := builtinTokenLimits[agent]
	return v, ok
}

// AgentLimit is one limit entry. LimitUSD and LimitTokens are pointers so an
// explicit 0 (off) differs from "not set".
type AgentLimit struct {
	LimitUSD *float64 `yaml:"limit_usd,omitempty"`
	// LimitTokens caps the tokens an agent may use in the window: fresh input,
	// output, cache reads and cache writes of every run, whatever its billing. It
	// trips exactly like LimitUSD (warning at warn_pct, hard stop at 100%). It
	// shares the entry's Window with LimitUSD. A project cannot set one; a limit
	// in this user-level file is the only way to have one.
	LimitTokens *int64 `yaml:"limit_tokens,omitempty"`
	Window      string `yaml:"window,omitempty"`
	WarnPct     int    `yaml:"warn_pct,omitempty"`
	// MaxModel is an optional model-tier ceiling (haiku < sonnet < opus <
	// fable) applied at dispatch, so a project cannot raise an agent's cost
	// by naming a dearer model (K-119 F2).
	MaxModel string `yaml:"max_model,omitempty"`

	// tokensIgnored says why a limit_tokens value in the file could not be read as a
	// token limit (not a whole number, too large for 64 bits, not a number at all);
	// "" when there was none or it was fine. Resolve reports it as a warning and
	// keeps the limit the value would have replaced. Set only by UnmarshalYAML.
	tokensIgnored string
}

// UnmarshalYAML reads one policy entry. limit_tokens is read as a YAML node and
// checked here rather than decoded straight into an int64, because yaml.v3 decodes
// 1500000.5 into an int64 as 1500000 without an error, and a value that is not a
// whole number of tokens must be ignored with a warning, not silently truncated into
// a limit the operator did not write. A value that cannot be read as a number leaves
// the limit unset and records why (tokensIgnored); the rest of the entry, and of the
// file, is read as usual.
func (a *AgentLimit) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		LimitUSD    *float64  `yaml:"limit_usd"`
		LimitTokens yaml.Node `yaml:"limit_tokens"`
		Window      string    `yaml:"window"`
		WarnPct     int       `yaml:"warn_pct"`
		MaxModel    string    `yaml:"max_model"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	*a = AgentLimit{LimitUSD: raw.LimitUSD, Window: raw.Window, WarnPct: raw.WarnPct, MaxModel: raw.MaxModel}
	a.LimitTokens, a.tokensIgnored = tokenLimitFromNode(&raw.LimitTokens)
	return nil
}

// tokenLimitFromNode reads a limit_tokens node: nil and "" when the key is absent or
// null, the whole number it holds, or nil and the reason it cannot be a token count.
// Range (negative, above the bound) is checked where limits are resolved.
func tokenLimitFromNode(n *yaml.Node) (*int64, string) {
	if n == nil || n.Kind == 0 {
		return nil, ""
	}
	if n.Kind != yaml.ScalarNode {
		return nil, "is not a number"
	}
	switch n.ShortTag() {
	case "!!null":
		return nil, ""
	case "!!int":
		var v int64
		if err := n.Decode(&v); err != nil {
			return nil, fmt.Sprintf("%s does not fit in a 64-bit integer", n.Value)
		}
		return &v, ""
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, "is not a number"
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return nil, fmt.Sprintf("%v is not a whole number of tokens", f)
		}
		if f < -9e18 || f > 9e18 {
			return nil, fmt.Sprintf("%v does not fit in a 64-bit integer", f)
		}
		v := int64(f)
		return &v, ""
	}
	return nil, "is not a number"
}

// Policy is the user-level budget policy file.
type Policy struct {
	Default AgentLimit            `yaml:"default,omitempty"`
	Agents  map[string]AgentLimit `yaml:"agents,omitempty"`
}

// ErrUntrustedPolicy marks a budget state file (policy, spend cache, resets)
// ignored for being a symlink, not a regular file, or having the wrong
// ownership or mode.
var ErrUntrustedPolicy = errors.New("budget file ignored")

// PolicyPath returns the policy file path inside stateDir.
func PolicyPath(stateDir string) string { return filepath.Join(stateDir, PolicyFileName) }

func trustCheck(path string) (os.FileInfo, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s is a symlink", ErrUntrustedPolicy, path)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrUntrustedPolicy, path)
	}
	if !decision.OwnedByCurrentUser(fi) {
		return nil, fmt.Errorf("%w: %s is owned by another user", ErrUntrustedPolicy, path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("%w: %s is group or world writable (chmod 600)", ErrUntrustedPolicy, path)
	}
	return fi, nil
}

// LoadPolicy reads the policy file. A missing file is an empty policy. An
// untrusted or unparsable file yields an empty policy plus an error the caller
// reports (the built-in defaults still apply, so a bad file never disables
// the supervisor and librarian limits).
func LoadPolicy(stateDir string) (Policy, error) {
	path := PolicyPath(stateDir)
	if _, err := trustCheck(path); err != nil {
		if os.IsNotExist(err) {
			return Policy{}, nil
		}
		return Policy{}, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // state-dir file
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Policy{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return p, nil
}

// SavePolicy writes the policy atomically with mode 0600.
func SavePolicy(stateDir string, p Policy) error {
	if err := statepath.SecureDir(stateDir); err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	header := "# yakOS per-agent budgets (K-119, K-136): limit_usd and limit_tokens. Edit with `yakos budget set`.\n"
	return atomicWrite(PolicyPath(stateDir), append([]byte(header), data...))
}

// SetLimit records a limit for agent in the policy file. usd 0 turns the
// limit off for that agent (overriding a built-in default). The read-modify-
// write runs under the budget lock so parallel sets never lose an update.
//
// w is the agent's window. The empty window keeps the agent's current one (its
// policy entry's, else the global default's, else monthly): the dollar and token
// limits share one window, so changing one must not silently change what the
// other means, for example turn a lifetime cap into one that re-opens every month.
func SetLimit(stateDir, agent string, usd float64, w Window) error {
	if err := ValidateAgent(agent); err != nil {
		return err
	}
	if !validLimitUSD(usd) {
		return fmt.Errorf("budget: limit must be 0 (off) or a number of dollars from %v to %v", minLimitUSD, maxLimitUSD)
	}
	if w != "" && w != Monthly && w != Lifetime {
		return fmt.Errorf("budget: window must be %s or %s", Monthly, Lifetime)
	}
	return updatePolicy(stateDir, func(p *Policy) {
		prev := p.Agents[agent]
		prev.LimitUSD = &usd
		prev.Window = string(windowOrCurrent(*p, agent, w))
		p.Agents[agent] = prev
	})
}

// windowOrCurrent returns w, or, when w is empty, the window agent counts in now
// (its policy entry's, else the global default's, else monthly). The set
// functions store the result explicitly, as they always have, so a policy file
// written by this version reads the same under an older one.
func windowOrCurrent(p Policy, agent string, w Window) Window {
	if w != "" {
		return w
	}
	return Resolve(agent, p, nil).Window
}

// maxTokenLimit bounds a configured token limit; a larger number is a typo.
const maxTokenLimit = int64(1) << 50

// SetTokenLimit records a token limit for agent in the policy file. tokens 0
// turns the token limit off. The window is the agent's one window: it also
// applies to the agent's dollar limit, so the empty window keeps the agent's
// current one (see SetLimit) rather than resetting it to monthly. The
// read-modify-write runs under the budget lock so parallel sets never lose an
// update.
func SetTokenLimit(stateDir, agent string, tokens int64, w Window) error {
	if err := ValidateAgent(agent); err != nil {
		return err
	}
	if tokens < 0 || tokens > maxTokenLimit {
		return fmt.Errorf("budget: token limit must be between 0 and %d", maxTokenLimit)
	}
	if w != "" && w != Monthly && w != Lifetime {
		return fmt.Errorf("budget: window must be %s or %s", Monthly, Lifetime)
	}
	return updatePolicy(stateDir, func(p *Policy) {
		prev := p.Agents[agent]
		prev.LimitTokens = &tokens
		prev.Window = string(windowOrCurrent(*p, agent, w))
		p.Agents[agent] = prev
	})
}

// modelRank orders the model tiers by cost.
var modelRank = map[string]int{"haiku": 1, "sonnet": 2, "opus": 3, "fable": 4}

// SetMaxModel records a model-tier ceiling for agent ("" removes it).
func SetMaxModel(stateDir, agent, tier string) error {
	if err := ValidateAgent(agent); err != nil {
		return err
	}
	if tier != "" && modelRank[tier] == 0 {
		return fmt.Errorf("budget: max model must be haiku, sonnet, opus or fable")
	}
	return updatePolicy(stateDir, func(p *Policy) {
		prev := p.Agents[agent]
		prev.MaxModel = tier
		p.Agents[agent] = prev
	})
}

// updatePolicy applies fn to the trusted policy under the budget lock.
func updatePolicy(stateDir string, fn func(*Policy)) error {
	if err := statepath.SecureDir(stateDir); err != nil {
		return err
	}
	unlock, err := acquire(stateDir, 5*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	if _, err := trustCheck(PolicyPath(stateDir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	p, err := LoadPolicy(stateDir)
	if err != nil {
		return err
	}
	if p.Agents == nil {
		p.Agents = map[string]AgentLimit{}
	}
	fn(&p)
	return SavePolicy(stateDir, p)
}

// builtinMaxModel are built-in model ceilings. The supervisor is capped at
// sonnet so a project's `supervisor.model: opus` cannot drain the budget every
// project shares. Only the user-level policy can change it (`budget set
// supervisor <usd> --max-model fable` lifts it).
var builtinMaxModel = map[string]string{"supervisor": "sonnet"}

// tierCeiling returns agent's own max_model ceiling in pol: its policy entry,
// else the built-in. A word that is not one of the four tiers is no ceiling.
func tierCeiling(pol Policy, agent string) string {
	ceil := pol.Agents[agent].MaxModel
	if ceil == "" {
		ceil = builtinMaxModel[agent]
	}
	if modelRank[ceil] == 0 {
		return ""
	}
	return ceil
}

// MaxModel returns the max_model ceiling in force for agent: its own, and, when
// o.Project names agent as its supervisor, the lower of that and the
// supervisor's. A project that renames its supervisor (`supervisor: agent:
// watchdog`) therefore keeps the supervisor's sonnet ceiling, the same rule that
// keeps its budget (see effective). It is "" when there is none. The ceiling is a
// Claude tier word; modelreg reads it for any harness.
func MaxModel(agent string, o Options) string {
	pol, _ := LoadPolicy(o.dir()) // an unusable policy still leaves the built-in ceiling
	ceil := tierCeiling(pol, agent)
	if sup := readProjectConfig(o.Project).aliasFor(agent); sup != "" {
		if c := tierCeiling(pol, sup); c != "" && (ceil == "" || modelRank[c] < modelRank[ceil]) {
			ceil = c
		}
	}
	return ceil
}

// claudeRegistry is the embedded catalog with no overlay and no project. The
// claude column cannot be remapped by an overlay, so it ranks the four tiers the
// same everywhere, and ClampModel needs nothing more.
var claudeRegistry = sync.OnceValues(func() (*modelreg.Registry, error) {
	return modelreg.Load(modelreg.Options{})
})

// ClampModel returns model lowered to agent's configured max_model ceiling,
// plus a note when it changed. A model outside the four tiers (a full model
// id) or an agent with no ceiling is returned unchanged. The ordering is
// modelreg's (Registry.Clamp on the claude harness), the one ordering the
// dispatcher applies to every harness (see dispatch.enforceCeiling, which refuses
// where this leaves a model alone).
func ClampModel(agent, model string, o Options) (string, string) {
	ceil := MaxModel(agent, o)
	if ceil == "" {
		return model, ""
	}
	reg, err := claudeRegistry()
	if err != nil {
		return model, ""
	}
	clamped, lowered := reg.Clamp("claude", model, ceil)
	if !lowered {
		return model, ""
	}
	return clamped, fmt.Sprintf("model %q lowered to %q: agent %s has max_model %s (budget-policy.yml or built-in)", model, clamped, agent, ceil)
}

// ValidateAgent rejects names that could not be a dispatch agent name.
func ValidateAgent(agent string) error {
	if agent == "" || len(agent) > 128 {
		return fmt.Errorf("budget: agent name is required (max 128 chars)")
	}
	for _, r := range agent {
		ok := r == '-' || r == '_' || r == '.' || r == ':' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return fmt.Errorf("budget: invalid agent name %q", agent)
		}
	}
	return nil
}

// Limit is the effective limit for one agent: one tuple on the agent's one spend
// counter.
type Limit struct {
	USD      float64 // 0 = off
	Tokens   int64   // 0 = off
	Window   Window
	WarnPct  int
	Source   string // builtin | policy | policy-default | project | none
	Warnings []string
	// StopFactor is the factor the dispatch-level stop is derived from: dispatch
	// refuses at StopFactor x the limit (1 for every agent but the supervisor).
	StopFactor float64
	// StopUSD and StopTokens are where dispatch refuses, in absolute terms, one per
	// unit (0 when the unit has no limit). For an agent resolved on its own they are
	// the limit times StopFactor. They are stored, not derived from StopFactor, because
	// the agent a project names as its supervisor combines two tuples (see tighter),
	// and each unit's stop is the smaller of the two sides' absolute stops: a single
	// factor cannot express "this side's amount with that side's stop".
	StopUSD    float64
	StopTokens int64
}

func parseWindow(s string, fallback Window) Window {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "monthly":
		return Monthly
	case "lifetime":
		return Lifetime
	}
	return fallback
}

// Resolve computes the effective limit for agent. Order: an explicit agent
// entry in the policy, then the built-in default, then the policy's global
// default, else off. projectUSD is the project's requested limit (nil when
// the project sets none) and can only lower the result.
func Resolve(agent string, p Policy, projectUSD *float64) Limit {
	return resolve(agent, p, projectUSD)
}

// maxLimitUSD bounds a configured dollar limit. A larger number is a typo, and a
// stop of twice it must stay finite (1e308 doubled is an infinite stop).
const maxLimitUSD = 1e9

// minLimitUSD is the smallest positive dollar limit, one cent. A smaller positive number
// is a typo too, and an unsafe one: spend divided by 5e-324, which is a number in range,
// is an infinite share, which json cannot encode. It is out of range like a negative
// limit: ignored with a warning in the user-level policy and in a project, refused by
// SetLimit. 0 is not below it: 0 is how a limit is turned off.
const minLimitUSD = 0.01

// validLimitUSD reports whether v is a dollar limit that the policy, a project or
// `budget set` may give: 0 (off), or a finite number from minLimitUSD to maxLimitUSD.
func validLimitUSD(v float64) bool {
	return v == 0 || (v >= minLimitUSD && v <= maxLimitUSD)
}

func resolve(agent string, p Policy, projectUSD *float64) Limit {
	l := Limit{Window: Monthly, WarnPct: DefaultWarnPct, Source: "none", StopFactor: 1}
	if f, ok := builtinStopFactor[agent]; ok {
		l.StopFactor = f
	}
	// apply layers one policy entry (name is "default" or an agent) on l. A limit that
	// is out of range is IGNORED with a warning, and the limit it would have replaced
	// (a built-in, or the global default's) stays: a value that cannot be a limit is a
	// typo or a corrupt edit, not the operator turning the budget off, and turning a
	// built-in off by accident would silently remove the supervisor's and librarian's
	// backstop. Out of range is a dollar limit that is negative, NaN, infinite, above
	// maxLimitUSD or positive but below minLimitUSD, and a token limit that is negative,
	// not a whole number, not a number at all or above maxTokenLimit. 0 is how a limit is
	// turned off, on purpose.
	apply := func(name string, a AgentLimit, src string) {
		if a.WarnPct > 0 && a.WarnPct <= 100 {
			l.WarnPct = a.WarnPct
		}
		l.Window = parseWindow(a.Window, l.Window)
		if a.LimitUSD != nil {
			if v := *a.LimitUSD; !validLimitUSD(v) {
				l.Warnings = append(l.Warnings, fmt.Sprintf("policy limit_usd for %s ignored: %v is not 0 (off) or a finite number of dollars from %v to %v; the limit it would have replaced stays (0 turns a limit off)", name, v, minLimitUSD, maxLimitUSD))
			} else {
				l.USD = v
				l.Source = src
			}
		}
		if a.tokensIgnored != "" {
			l.Warnings = append(l.Warnings, fmt.Sprintf("policy limit_tokens for %s ignored: %s; the limit it would have replaced stays (0 turns a limit off)", name, a.tokensIgnored))
		}
		if a.LimitTokens != nil {
			if v := *a.LimitTokens; v < 0 || v > maxTokenLimit {
				l.Warnings = append(l.Warnings, fmt.Sprintf("policy limit_tokens for %s ignored: %d is outside 0 to %d; the limit it would have replaced stays (0 turns a limit off)", name, v, maxTokenLimit))
			} else {
				l.Tokens = v
				if l.Source == "none" {
					l.Source = src
				}
			}
		}
	}
	// Global default first so agent-level values override it field by field.
	apply("default", p.Default, "policy-default")
	if v, ok := builtinLimits[agent]; ok {
		// A built-in is more specific than the global default.
		l.USD, l.Source = v, "builtin"
	}
	if v, ok := builtinTokenLimits[agent]; ok {
		// So is a built-in token limit (K-136); it keeps the budget tripping for
		// a subscription operator, whose runs never move the dollar limit.
		l.Tokens, l.Source = v, "builtin"
	}
	if a, ok := p.Agents[agent]; ok {
		apply(agent, a, "policy")
	}
	if projectUSD != nil {
		pv := *projectUSD
		switch {
		case math.IsNaN(pv) || pv <= 0:
			l.Warnings = append(l.Warnings, fmt.Sprintf("project agent_budgets.%s=%v ignored: a project cannot disable a budget", agent, pv))
		case !validLimitUSD(pv):
			// Validated exactly like a policy value: an infinite, huge or absurdly small
			// number is not applied, not even to an agent the operator left unlimited (then
			// nothing else bounds it, and it would be a limit or a stop json cannot encode).
			// Only the number and the validated agent name are echoed, never the project's text.
			l.Warnings = append(l.Warnings, fmt.Sprintf("project agent_budgets.%s=%v ignored: it is not a finite number of dollars from %v to %v", agent, pv, minLimitUSD, maxLimitUSD))
		case l.USD == 0:
			l.USD, l.Source = pv, "project" // off -> limited is a tightening
		case pv < l.USD:
			l.USD, l.Source = pv, "project"
		case pv > l.USD:
			l.Warnings = append(l.Warnings, fmt.Sprintf("project agent_budgets.%s=$%.2f ignored: a project cannot raise the user-level limit of $%.2f", agent, pv, l.USD))
		}
	}
	l.StopUSD = l.USD * l.StopFactor
	l.StopTokens = int64(float64(l.Tokens) * l.StopFactor)
	return l
}

// effective is the limit Evaluate and Reset use for agent: resolve, and, when the
// project names agent as its supervisor, combined with the supervisor's own limit
// (see tighter). A project's agent_budgets: lowers either side's dollar limit.
func effective(agent string, p Policy, cfg projectConfig) Limit {
	own := resolve(agent, p, cfg.projectUSD(agent))
	if cfg.aliasFor(agent) == "" {
		return own
	}
	return tighter(own, resolve(supervisorAgent, p, cfg.projectUSD(supervisorAgent)))
}

// tighter combines the limit of an agent a project names as its supervisor (own: its
// operator entry, else the policy default, else its own built-in, with its own window
// and stop) with the supervisor's limit (sup: the operator's supervisor entry, else the
// built-in, with its window and stop) into ONE limit on the agent's one spend counter.
// The project file is the attacker in this model, so the result is never looser than
// either side, and so never looser than checking both separately:
//
//   - per unit (dollars, tokens) the smaller amount;
//   - per unit the smaller ABSOLUTE stop, min(own amount x own factor, sup amount x sup
//     factor), not the stop of whichever side has the smaller amount (an own 50M limit
//     with a 50M stop beside the supervisor's 33M with a 66M stop is 33M with a 50M
//     stop, not 33M with 66M);
//   - the window is lifetime if either side that has a limit, in either unit, is
//     lifetime, and monthly when every side that has a limit is monthly. A side with no
//     limit at all counts nothing, so its window does not: an unlimited agent whose
//     lifetime window comes from the policy default, beside a supervisor an entry
//     makes monthly, is monthly. When neither side has a limit there is nothing to count
//     and the agent's own window is kept;
//   - a unit that is off or unlimited on one side counts as infinite there, so only an
//     agent that would otherwise be unlimited in a unit gains the supervisor's limit.
//
// It can be stricter than checking both separately: an own $200 lifetime limit beside
// the supervisor's $100 monthly one becomes $100 lifetime.
func tighter(own, sup Limit) Limit {
	out := own
	out.USD = tighterF(own.USD, sup.USD)
	out.StopUSD = tighterF(own.StopUSD, sup.StopUSD)
	out.Tokens = tighterI(own.Tokens, sup.Tokens)
	out.StopTokens = tighterI(own.StopTokens, sup.StopTokens)
	ownLimited, supLimited := own.USD > 0 || own.Tokens > 0, sup.USD > 0 || sup.Tokens > 0
	switch {
	case !ownLimited && !supLimited:
		out.Window = own.Window // nothing is counted: keep the agent's own
	case (ownLimited && own.Window == Lifetime) || (supLimited && sup.Window == Lifetime):
		out.Window = Lifetime
	default:
		out.Window = Monthly
	}
	if sup.WarnPct < out.WarnPct {
		out.WarnPct = sup.WarnPct // warn at the earlier of the two
	}
	if sup.StopFactor < out.StopFactor {
		out.StopFactor = sup.StopFactor
	}
	if own.Source == "none" {
		out.Source = sup.Source
	}
	out.Warnings = append([]string(nil), own.Warnings...)
	for _, w := range sup.Warnings {
		dup := false
		for _, o := range out.Warnings {
			dup = dup || o == w
		}
		if !dup {
			out.Warnings = append(out.Warnings, w)
		}
	}
	return out
}

// tighterF and tighterI are the smaller of two amounts where 0 means off (unlimited,
// infinite): 0 only when both are.
func tighterF(a, b float64) float64 {
	switch {
	case a <= 0:
		return b
	case b <= 0 || a < b:
		return a
	}
	return b
}

func tighterI(a, b int64) int64 {
	switch {
	case a <= 0:
		return b
	case b <= 0 || a < b:
		return a
	}
	return b
}

// projectConfig is what the budget reads from <project>/.yakos.yml: the
// agent_budgets: map, and the names the project gives its supervisor agent.
type projectConfig struct {
	limits     map[string]float64 // agent_budgets:
	warn       string             // set when the file could not be parsed
	supervisor []string           // supervisor: agent: (see readProjectConfig)
}

// supervisorAgent is the one agent whose budget is built in: the supervisor.
const supervisorAgent = "supervisor"

// projectUSD is the dollar limit the project's agent_budgets: asks for agent, or nil.
func (c projectConfig) projectUSD(agent string) *float64 {
	if v, ok := c.limits[agent]; ok {
		return &v
	}
	return nil
}

// aliasFor returns "supervisor" when agent is a name the project gives its supervisor
// (and is not "supervisor" itself), else "". Such an agent is budgeted at the stricter
// of its own limit and the supervisor's (see tighter), so naming an agent the supervisor
// can only add limits to it, never raise one.
func (c projectConfig) aliasFor(agent string) string {
	if agent == supervisorAgent {
		return ""
	}
	for _, n := range c.supervisor {
		if n == agent {
			return supervisorAgent
		}
	}
	return ""
}

func (c *projectConfig) addSupervisor(name string) {
	if ValidateAgent(name) != nil {
		return
	}
	for _, n := range c.supervisor {
		if n == name {
			return
		}
	}
	c.supervisor = append(c.supervisor, name)
}

// readProjectConfig reads <project>/.yakos.yml. A missing file or key yields the
// zero config; a malformed file sets warn and no limits.
//
// The supervisor's agent name is a project setting: the supervisor hook launches
// `yakos dispatch <name>` and its budget is keyed on that name, so a project that
// renames its supervisor would otherwise leave it without the supervisor's
// built-in budget. Both hook twins read the name, and they read it differently:
// the Go twin as YAML (supervisor.agent), the bash twin with a line scan (the
// first agent: line within 20 lines after a supervisor: line). Every name either
// of them can arrive at is returned, so a file the two disagree about cannot
// pick a name the budget does not know.
func readProjectConfig(project string) projectConfig {
	var c projectConfig
	if project == "" {
		return c
	}
	data, err := os.ReadFile(filepath.Join(project, ".yakos.yml")) //nolint:gosec
	if err != nil {
		return c
	}
	var doc struct {
		AgentBudgets map[string]float64 `yaml:"agent_budgets"`
		// Supervisor is decoded loosely: a project that writes `supervisor: true`
		// must not lose its agent_budgets to a decode error.
		Supervisor any `yaml:"supervisor"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		c.warn = fmt.Sprintf(".yakos.yml agent_budgets ignored: %v", err)
	} else {
		c.limits = doc.AgentBudgets
		if m, ok := doc.Supervisor.(map[string]any); ok {
			if name, ok := m["agent"].(string); ok {
				c.addSupervisor(strings.TrimSpace(name))
			}
		}
	}
	c.addSupervisor(scanSupervisorAgent(data))
	return c
}

// scanSupervisorAgent reads the supervisor's agent name the way the bash hook does:
//
//	grep -A 20 '^[[:space:]]*supervisor:' | grep -E '^[[:space:]]*agent:[[:space:]]*' |
//	  head -1 | awk -F: '{print $2}' | tr -d '[:space:]'
//
// that is, the first agent: line within 20 lines after a supervisor: line, its
// text between the first and the second colon, with every space removed. It is ""
// when there is none, or the first one is empty (and the hook keeps its default).
func scanSupervisorAgent(data []byte) string {
	lines := strings.Split(string(data), "\n")
	inContext := make([]bool, len(lines))
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimLeft(l, " \t\r\f\v"), "supervisor:") {
			for j := i; j <= i+20 && j < len(lines); j++ {
				inContext[j] = true
			}
		}
	}
	for i, l := range lines {
		if !inContext[i] || !strings.HasPrefix(strings.TrimLeft(l, " \t\r\f\v"), "agent:") {
			continue
		}
		fields := strings.Split(l, ":")
		if len(fields) < 2 {
			return ""
		}
		return strings.Join(strings.Fields(fields[1]), "")
	}
	return ""
}

// ProjectLimits reads the `agent_budgets:` map from <project>/.yakos.yml. A
// missing file or key yields nil; a malformed one yields a warning string.
func ProjectLimits(project string) (map[string]float64, string) {
	c := readProjectConfig(project)
	return c.limits, c.warn
}

// ProjectSupervisorAgents returns the agent names <project>/.yakos.yml gives the
// supervisor (supervisor: agent:), valid names only, in the order found. The
// supervisor hook runs under such a name, so its budget is the supervisor's.
func ProjectSupervisorAgents(project string) []string {
	return readProjectConfig(project).supervisor
}

// AgentNames returns every agent that has a limit configured (policy or
// built-in), sorted.
func AgentNames(p Policy) []string { return AgentNamesWith(p, nil) }

// AgentNamesForProject is AgentNamesWith over the project's own agent_budgets:,
// plus the agent the project names as its supervisor, which has the supervisor's
// budget under whatever name it gives it.
func AgentNamesForProject(p Policy, project string) []string {
	c := readProjectConfig(project)
	names := AgentNamesWith(p, c.limits)
	have := make(map[string]bool, len(names))
	for _, n := range names {
		have[n] = true
	}
	for _, n := range c.supervisor {
		if !have[n] {
			names = append(names, n)
			have[n] = true
		}
	}
	sort.Strings(names)
	return names
}

// AgentNamesWith is AgentNames plus the agents a project limits through
// agent_budgets: (K-119 review: an agent limited only by its project must
// still show in status and doctor).
func AgentNamesWith(p Policy, project map[string]float64) []string {
	seen := map[string]bool{}
	for a := range project {
		if ValidateAgent(a) == nil {
			seen[a] = true
		}
	}
	for a := range builtinLimits {
		seen[a] = true
	}
	for a := range p.Agents {
		seen[a] = true
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
