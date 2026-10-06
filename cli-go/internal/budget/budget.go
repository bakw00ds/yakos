package budget

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// State is an agent's budget state.
type State string

const (
	StateOff      State = "off"       // no limit configured
	StateOK       State = "ok"        // under the warning threshold
	StateWarning  State = "warning"   // at or past the warning threshold
	StateHardStop State = "hard_stop" // at or past 100%: new dispatches refused
)

// ExitHardStop is the exit code of `yakos dispatch` and `yakos budget check`
// when an agent is in hard_stop. It is never 2: exit 2 is the Claude Code hook
// "block" code and a budget refusal must never block a tool call.
const ExitHardStop = 4

// Options locate the state and fix the clock for tests.
type Options struct {
	StateDir string           // default statepath.Dir()
	Project  string           // project whose .yakos.yml may lower the limit
	Now      func() time.Time // default time.Now
}

// StateDirOrDefault returns the state directory in effect.
func (o Options) StateDirOrDefault() string { return o.dir() }

func (o Options) dir() string {
	if o.StateDir != "" {
		return o.StateDir
	}
	return statepath.Dir()
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Status is the evaluated budget of one agent.
type Status struct {
	Agent     string  `json:"agent"`
	State     State   `json:"state"`
	Window    Window  `json:"window"`
	WindowKey string  `json:"window_key"`
	LimitUSD  float64 `json:"limit_usd"`
	// StopUSD is where `yakos dispatch` refuses: the limit, or for the
	// supervisor twice it (state hard_stop at the limit refuses routine hook
	// launches only; see builtinStopFactor).
	StopUSD  float64 `json:"stop_usd"`
	SpentUSD float64 `json:"spent_usd"`
	// Token budget (K-136), additive. LimitTokens is 0 (and omitted) when the
	// agent has no token limit; StopTokens is where `yakos dispatch` refuses
	// (the limit times the stop factor); SpentTokens is every reported token of
	// every run in the window, whatever its billing, and is reported even when no
	// token limit is set. TokensPct is the share of the token limit used.
	LimitTokens int64   `json:"limit_tokens,omitempty"`
	StopTokens  int64   `json:"stop_tokens,omitempty"`
	SpentTokens int64   `json:"spent_tokens,omitempty"`
	TokensPct   float64 `json:"tokens_pct,omitempty"`
	// Pct is the share of the limit used: the dollar limit's, the token
	// limit's, or the larger of the two when an agent has both.
	Pct       float64  `json:"pct"`
	WarnPct   int      `json:"warn_pct"`
	Source    string   `json:"source"`
	RollsOver string   `json:"rolls_over,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	// ReadFailed is true when the spend could not be read (an unreadable dispatch
	// log). The state, spend and percentage then describe an empty ledger, "ok,
	// nothing spent", which is not a measurement, and callers fail open. It is set
	// from the error itself and never from text: stderr and Warnings carry strings a
	// project controls (a repeated agent_budgets key is echoed back in the YAML
	// error), so a hook must rely on this field and not on the words it finds there.
	ReadFailed bool `json:"read_failed,omitempty"`
	// Reason is a stable machine-readable code: budget_off, budget_ok,
	// budget_warning or budget_exhausted. Hooks and scripts match on it.
	Reason string `json:"reason"`
	// Projects is spend per project in the current window, largest first
	// (top 10), from the project recorded on each dispatch_finished event.
	Projects []ProjectSpend `json:"projects,omitempty"`
}

// ProjectSpend is one project's share of an agent's spend in the window.
type ProjectSpend struct {
	Project  string  `json:"project"`
	SpentUSD float64 `json:"spent_usd"`
}

// Stable reason codes (Status.Reason).
const (
	ReasonOff       = "budget_off"
	ReasonOK        = "budget_ok"
	ReasonWarning   = "budget_warning"
	ReasonExhausted = "budget_exhausted"
)

func reasonFor(s State) string {
	switch s {
	case StateHardStop:
		return ReasonExhausted
	case StateWarning:
		return ReasonWarning
	case StateOK:
		return ReasonOK
	}
	return ReasonOff
}

// Refused reports whether new dispatches for this agent must be refused.
func (s Status) Refused() bool { return s.State == StateHardStop }

// usdTripped and tokensTripped say which limit has reached its dispatch stop. A
// limit that is not configured never trips.
func (s Status) usdTripped() bool {
	return s.LimitUSD > 0 && s.SpentUSD+1e-9 >= s.StopUSD
}

func (s Status) tokensTripped() bool {
	return s.LimitTokens > 0 && s.SpentTokens >= s.StopTokens
}

// spentDesc is "$3.00 of $5.00", "1,200 of 1,000 tokens", or both joined by
// "and", for the limits the agent has. A dollar-only agent reads exactly as it
// did before token limits existed.
func (s Status) spentDesc() string {
	var parts []string
	if s.LimitUSD > 0 || s.LimitTokens <= 0 {
		parts = append(parts, fmt.Sprintf("$%.2f of $%.2f", s.SpentUSD, s.LimitUSD))
	}
	if s.LimitTokens > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s tokens", commas(s.SpentTokens), commas(s.LimitTokens)))
	}
	return strings.Join(parts, " and ")
}

// commas formats n with thousands separators.
func commas(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// Message is a one-line human description.
func (s Status) Message() string {
	switch s.State {
	case StateOff:
		return fmt.Sprintf("%s: no budget", s.Agent)
	case StateHardStop:
		return fmt.Sprintf("%s: HARD STOP, %s (%s) spent", s.Agent, s.spentDesc(), s.Window)
	case StateWarning:
		return fmt.Sprintf("%s: warning, %s (%s) spent, %.0f%%", s.Agent, s.spentDesc(), s.Window, s.Pct)
	}
	return fmt.Sprintf("%s: ok, %s (%s) spent, %.0f%%", s.Agent, s.spentDesc(), s.Window, s.Pct)
}

// RefusedError is returned by Enforce for an agent in hard_stop.
type RefusedError struct{ Status Status }

func (e *RefusedError) Error() string {
	s := e.Status
	msg := fmt.Sprintf("budget: dispatch refused: agent %q has spent $%.2f of its $%.2f %s budget", s.Agent, s.SpentUSD, s.LimitUSD, s.Window)
	if s.StopUSD > s.LimitUSD {
		// The supervisor's dispatch stop is 2x its limit (high-risk exemption ceiling).
		msg = fmt.Sprintf("budget: dispatch refused: agent %q exceeded 2x the $%.2f %s supervisor budget (high-risk exemption ceiling, $%.2f spent)", s.Agent, s.LimitUSD, s.Window, s.SpentUSD)
	}
	hints := []string{fmt.Sprintf("raise it with `yakos budget set %s <usd>`", s.Agent), fmt.Sprintf("run `yakos budget reset %s`", s.Agent)}
	if s.LimitTokens > 0 {
		// An agent with a token limit (K-136): say what was used, and which limit
		// to raise. A dollar limit that did not trip is not blamed.
		used := fmt.Sprintf("used %s of its %s tokens", commas(s.SpentTokens), commas(s.LimitTokens))
		switch {
		case s.LimitUSD > 0 && s.usdTripped() && s.tokensTripped():
			msg = fmt.Sprintf("budget: dispatch refused: agent %q has spent $%.2f of its $%.2f and %s (%s budget)", s.Agent, s.SpentUSD, s.LimitUSD, used, s.Window)
			hints[0] = fmt.Sprintf("raise them with `yakos budget set %s <usd> --tokens <n>`", s.Agent)
		case s.LimitUSD > 0 && s.usdTripped():
			// Only the dollar limit is exhausted: the message above already says so.
		default:
			msg = fmt.Sprintf("budget: dispatch refused: agent %q has %s (%s token budget)", s.Agent, used, s.Window)
			hints[0] = fmt.Sprintf("raise it with `yakos budget set %s --tokens <n>`", s.Agent)
		}
	}
	if s.RollsOver != "" {
		hints = append(hints, "or wait for the window to roll over on "+s.RollsOver)
	}
	return msg + "; " + strings.Join(hints, ", ")
}

// IsRefused reports whether err is a hard-stop refusal.
func IsRefused(err error) bool {
	var r *RefusedError
	return errors.As(err, &r)
}

// Evaluate computes the status of agent. A policy file problem is reported in
// Status.Warnings and the built-in defaults still apply. A spend I/O error is
// returned with a StateOK status: the budget is a cost guard, so callers fail
// open on it rather than stop all dispatching.
func Evaluate(agent string, o Options) (Status, error) {
	dir := o.dir()
	now := o.now()
	pol, perr := LoadPolicy(dir)
	var projectUSD *float64
	var warns []string
	if perr != nil {
		warns = append(warns, perr.Error())
	}
	if pl, w := ProjectLimits(o.Project); w != "" {
		warns = append(warns, w)
	} else if v, ok := pl[agent]; ok {
		projectUSD = &v
	}
	lim := Resolve(agent, pol, projectUSD)
	st := Status{
		Agent: agent, State: StateOff, Window: lim.Window, WindowKey: WindowKey(lim.Window, now),
		LimitUSD: lim.USD, StopUSD: lim.USD * lim.StopFactor, WarnPct: lim.WarnPct, Source: lim.Source, Warnings: append(warns, lim.Warnings...),
		LimitTokens: lim.Tokens, StopTokens: int64(float64(lim.Tokens) * lim.StopFactor),
	}
	st.Reason = ReasonOff
	if lim.USD <= 0 && lim.Tokens <= 0 {
		return st, nil
	}
	if next := NextWindowStart(lim.Window, now); !next.IsZero() {
		st.RollsOver = next.Format("2006-01-02")
	}
	st.State = StateOK
	st.Reason = ReasonOK
	agg, err := refresh(dir)
	if err != nil {
		st.ReadFailed = true
		return st, fmt.Errorf("budget: reading spend: %w", err)
	}
	spent := agg.spend(agent, lim.Window, st.WindowKey)
	spentTok := agg.tokens(agent, lim.Window, st.WindowKey).Total()
	resets, rerr := readResets(dir)
	if rerr != nil {
		st.Warnings = append(st.Warnings, rerr.Error())
	}
	if r, ok := resets[agent]; ok && r.Window == string(lim.Window) && r.Key == st.WindowKey {
		spent -= r.USD
		spentTok -= r.Tokens
	}
	spent = math.Max(0, spent)
	if spentTok < 0 {
		spentTok = 0
	}
	st.SpentUSD = spent
	st.SpentTokens = spentTok
	// Each configured limit has its own share used; the agent's is the larger,
	// and it is at hard_stop when EITHER limit is reached (K-136).
	hard := false
	if lim.USD > 0 {
		st.Pct = spent / lim.USD * 100
		hard = spent+1e-9 >= lim.USD
	}
	if lim.Tokens > 0 {
		st.TokensPct = float64(spentTok) / float64(lim.Tokens) * 100
		st.Pct = math.Max(st.Pct, st.TokensPct)
		hard = hard || spentTok >= lim.Tokens
	}
	switch {
	case hard:
		st.State = StateHardStop
	case st.Pct >= float64(lim.WarnPct):
		st.State = StateWarning
	}
	st.Reason = reasonFor(st.State)
	for p, v := range agg.projectSpend(agent, lim.Window, st.WindowKey) {
		st.Projects = append(st.Projects, ProjectSpend{Project: p, SpentUSD: v})
	}
	sort.Slice(st.Projects, func(i, j int) bool {
		if st.Projects[i].SpentUSD != st.Projects[j].SpentUSD {
			return st.Projects[i].SpentUSD > st.Projects[j].SpentUSD
		}
		return st.Projects[i].Project < st.Projects[j].Project
	})
	if len(st.Projects) > 10 {
		st.Projects = st.Projects[:10]
	}
	return st, nil
}

// Enforce is the dispatch pre-flight: it returns a *RefusedError when agent is
// in hard_stop and past its dispatch stop (StopUSD for a dollar limit,
// StopTokens for a token limit; either one refuses), nil otherwise. Every other
// failure (unreadable log, untrusted policy file) fails open; the returned
// Status carries the details.
func Enforce(agent string, o Options) (Status, error) {
	st, err := Evaluate(agent, o)
	if st.State == StateHardStop && (st.usdTripped() || st.tokensTripped()) {
		return st, &RefusedError{Status: st}
	}
	return st, err
}

// Reset starts agent's current window over: spend already recorded in it no
// longer counts. It does not delete anything from the dispatch-log.
func Reset(agent string, o Options) (Status, error) {
	if err := ValidateAgent(agent); err != nil {
		return Status{}, err
	}
	dir := o.dir()
	now := o.now()
	pol, _ := LoadPolicy(dir)
	lim := Resolve(agent, pol, nil)
	key := WindowKey(lim.Window, now)
	if err := resetLocked(dir, agent, lim.Window, key, now); err != nil {
		return Status{}, err
	}
	return Evaluate(agent, o)
}

// resetLocked records the reset baseline under the budget lock.
func resetLocked(dir, agent string, w Window, key string, now time.Time) error {
	unlock, err := acquire(dir, 5*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	agg, err := refreshLocked(dir)
	if err != nil {
		return err
	}
	resets, _ := readResets(dir) // an untrusted file is replaced by the write below
	resets[agent] = resetRec{Window: string(w), Key: key, USD: agg.spend(agent, w, key), Tokens: agg.tokens(agent, w, key).Total(), At: now.UTC().Format(time.RFC3339)}
	return writeJSON(dir, resetsFileName, resets)
}
