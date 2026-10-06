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
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/decision"
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
func SetLimit(stateDir, agent string, usd float64, w Window) error {
	if err := ValidateAgent(agent); err != nil {
		return err
	}
	if math.IsNaN(usd) || math.IsInf(usd, 0) || usd < 0 {
		return fmt.Errorf("budget: limit must be a non-negative number of dollars")
	}
	if w != Monthly && w != Lifetime {
		return fmt.Errorf("budget: window must be %s or %s", Monthly, Lifetime)
	}
	return updatePolicy(stateDir, func(p *Policy) {
		prev := p.Agents[agent]
		prev.LimitUSD = &usd
		prev.Window = string(w)
		p.Agents[agent] = prev
	})
}

// maxTokenLimit bounds a configured token limit; a larger number is a typo.
const maxTokenLimit = int64(1) << 50

// SetTokenLimit records a token limit for agent in the policy file. tokens 0
// turns the token limit off. The window is the agent's one window: it also
// applies to the agent's dollar limit. The read-modify-write runs under the
// budget lock so parallel sets never lose an update.
func SetTokenLimit(stateDir, agent string, tokens int64, w Window) error {
	if err := ValidateAgent(agent); err != nil {
		return err
	}
	if tokens < 0 || tokens > maxTokenLimit {
		return fmt.Errorf("budget: token limit must be between 0 and %d", maxTokenLimit)
	}
	if w != Monthly && w != Lifetime {
		return fmt.Errorf("budget: window must be %s or %s", Monthly, Lifetime)
	}
	return updatePolicy(stateDir, func(p *Policy) {
		prev := p.Agents[agent]
		prev.LimitTokens = &tokens
		prev.Window = string(w)
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

// ClampModel returns model lowered to agent's configured max_model ceiling,
// plus a note when it changed. A model outside the four tiers (a full model
// id) or an agent with no ceiling is returned unchanged.
func ClampModel(agent, model string, o Options) (string, string) {
	pol, _ := LoadPolicy(o.dir()) // an unusable policy still leaves the built-in ceiling
	ceil := pol.Agents[agent].MaxModel
	if ceil == "" {
		ceil = builtinMaxModel[agent]
	}
	if ceil == "" || modelRank[ceil] == 0 || modelRank[model] == 0 || modelRank[model] <= modelRank[ceil] {
		return model, ""
	}
	return ceil, fmt.Sprintf("model %q lowered to %q: agent %s has max_model %s (budget-policy.yml or built-in)", model, ceil, agent, ceil)
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

// Limit is the effective limit for one agent.
type Limit struct {
	USD      float64 // 0 = off
	Tokens   int64   // 0 = off
	Window   Window
	WarnPct  int
	Source   string // builtin | policy | policy-default | project | none
	Warnings []string
	// StopFactor scales the dispatch-level stop: dispatch refuses at
	// StopFactor x USD (1 for every agent but the supervisor). It scales Tokens
	// the same way.
	StopFactor float64
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
	l := Limit{Window: Monthly, WarnPct: DefaultWarnPct, Source: "none", StopFactor: 1}
	if f, ok := builtinStopFactor[agent]; ok {
		l.StopFactor = f
	}
	apply := func(a AgentLimit, src string) {
		if a.WarnPct > 0 && a.WarnPct <= 100 {
			l.WarnPct = a.WarnPct
		}
		l.Window = parseWindow(a.Window, l.Window)
		if a.LimitUSD != nil {
			v := *a.LimitUSD
			if math.IsNaN(v) || v < 0 {
				v = 0
			}
			l.USD = v
			l.Source = src
		}
		if a.LimitTokens != nil {
			v := *a.LimitTokens
			if v < 0 || v > maxTokenLimit {
				v = 0
			}
			l.Tokens = v
			if l.Source == "none" {
				l.Source = src
			}
		}
	}
	// Global default first so agent-level values override it field by field.
	apply(p.Default, "policy-default")
	if v, ok := builtinLimits[agent]; ok {
		// A built-in is more specific than the global default.
		l.USD, l.Source = v, "builtin"
	}
	if a, ok := p.Agents[agent]; ok {
		apply(a, "policy")
	}
	if projectUSD != nil {
		pv := *projectUSD
		switch {
		case math.IsNaN(pv) || pv <= 0:
			l.Warnings = append(l.Warnings, fmt.Sprintf("project agent_budgets.%s=%v ignored: a project cannot disable a budget", agent, pv))
		case l.USD == 0:
			l.USD, l.Source = pv, "project" // off -> limited is a tightening
		case pv < l.USD:
			l.USD, l.Source = pv, "project"
		case pv > l.USD:
			l.Warnings = append(l.Warnings, fmt.Sprintf("project agent_budgets.%s=$%.2f ignored: a project cannot raise the user-level limit of $%.2f", agent, pv, l.USD))
		}
	}
	return l
}

// ProjectLimits reads the `agent_budgets:` map from <project>/.yakos.yml. A
// missing file or key yields nil; a malformed one yields a warning string.
func ProjectLimits(project string) (map[string]float64, string) {
	if project == "" {
		return nil, ""
	}
	data, err := os.ReadFile(filepath.Join(project, ".yakos.yml")) //nolint:gosec
	if err != nil {
		return nil, ""
	}
	var doc struct {
		AgentBudgets map[string]float64 `yaml:"agent_budgets"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Sprintf(".yakos.yml agent_budgets ignored: %v", err)
	}
	return doc.AgentBudgets, ""
}

// AgentNames returns every agent that has a limit configured (policy or
// built-in), sorted.
func AgentNames(p Policy) []string { return AgentNamesWith(p, nil) }

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
