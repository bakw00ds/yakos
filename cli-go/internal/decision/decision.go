// Package decision is yakOS's typed decision-provider abstraction (ADR-0009).
//
// A decision provider answers a fixed, reviewed set of typed questions
// (Choice, Score, Noul) about a redacted, named-field JSON state and returns
// probabilities, never text. It is NOT an agent runtime: it has no session,
// no prompt, and no tools, so no agent can be routed to it.
//
// Invariants enforced here (not by callers):
//
//   - Fail-closed. Every failure is a typed *Error; callers fall back to the
//     existing deterministic path. Nothing in this package ever panics a hook
//     into a blocking exit.
//   - Egress control. State passes through Sanitize (redact.go) inside
//     Execute before any provider sees it.
//   - Out of band. Nothing here touches a prompt, system-prompt prefix, agent
//     roster, or always-loaded rule (rule:cache-stability). Answers go to the
//     decision log and to the calling hook only.
package decision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// Modes a decision point can run in.
const (
	ModeShadow    = "shadow"
	ModePrefilter = "prefilter"
)

// Provider names.
const (
	ProviderJev  = "jev"
	ProviderMock = "mock"
	ProviderNone = "none"
)

// Deadlines (ADR-0009 §4). A hook may run the call synchronously, so BOTH
// modes default to 1.5 s (shadow is meant to be backgrounded by the hook, but
// the bound must not depend on that). An explicit --timeout may raise it, never
// beyond MaxTimeout.
const (
	PrefilterTimeout = 1500 * time.Millisecond
	ShadowTimeout    = PrefilterTimeout
	MaxTimeout       = 10 * time.Second
)

// Error classes recorded in the decision log and printed as the "reason".
const (
	ClassDisabled    = "disabled"
	ClassNoKey       = "no_key"
	ClassBreakerOpen = "breaker_open"
	ClassBudget      = "budget"
	ClassTimeout     = "timeout"
	ClassNetwork     = "network"
	ClassHTTP401     = "http_401"
	ClassHTTP422     = "http_422"
	ClassHTTP429     = "http_429"
	ClassHTTP529     = "http_529"
	ClassHTTP5xx     = "http_5xx"
	ClassHTTPOther   = "http_other"
	ClassMalformed   = "malformed"
	ClassOversize    = "oversize"
	ClassBadRequest  = "bad_request"
	ClassInternal    = "internal"
)

// Error is the only error type providers return. Class is a stable,
// low-cardinality token safe to log; Err is detail that never contains the
// API key or request state.
type Error struct {
	Class string
	Err   error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return e.Class
	}
	return e.Class + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

func newErr(class string, format string, a ...any) *Error {
	return &Error{Class: class, Err: fmt.Errorf(format, a...)}
}

// ErrorClass returns the class of err, or ClassInternal for a foreign error,
// or "" for nil.
func ErrorClass(err error) string {
	if err == nil {
		return ""
	}
	var de *Error
	if errors.As(err, &de) {
		return de.Class
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassTimeout
	}
	return ClassInternal
}

// Question is one typed question. Provider-neutral: the JSON tags match the
// TypeSafe wire shape (https://docs.typesafe.ai/api) but nothing here names a
// vendor. Criteria is map[string]string (choice), []string (score), or
// map[string]string with keys true/false (noul).
type Question struct {
	Type         string `json:"type" yaml:"type"`
	Instructions string `json:"instructions" yaml:"instructions"`
	Criteria     any    `json:"criteria,omitempty" yaml:"criteria,omitempty"`
}

// Request is one decision call. State must already be sanitized; Execute
// guarantees that, providers must not be handed raw state by other callers
// (the jev client additionally re-checks its size).
type Request struct {
	Surface    string
	SchemaID   string
	SchemaHash string // sha256 of the question-set file; logged, never sent
	Model      string // pinned version, e.g. "jev-1.13.0"
	State      any
	Questions  map[string]Question
	Mode       string
	Timeout    time.Duration
	Session    string
}

// Answer mirrors the three TypeSafe answer shapes. Pointers keep "absent"
// distinct from zero (a Noul of 0.0 is a real answer; Noul has no confidence).
type Answer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
}

// Usage is the token usage the provider reported.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Result is a successful decision.
type Result struct {
	Provider  string            `json:"provider"`
	Model     string            `json:"model"` // model the provider says it served
	Answers   map[string]Answer `json:"answers"`
	Usage     Usage             `json:"usage"`
	CostUSD   float64           `json:"cost_usd"`
	LatencyMS int64             `json:"latency_ms"`
}

// Provider is the provider-neutral interface.
type Provider interface {
	Name() string
	// Available reports (cheaply, without a network call) whether Decide can
	// be attempted: key present, breaker closed, budget left, not disabled.
	Available(ctx context.Context) error
	Decide(ctx context.Context, req Request) (*Result, error)
}

// none is the disabled provider: every caller takes its existing path.
type none struct{}

func (none) Name() string { return ProviderNone }
func (none) Available(context.Context) error {
	return newErr(ClassDisabled, "decision provider is none")
}
func (none) Decide(context.Context, Request) (*Result, error) {
	return nil, newErr(ClassDisabled, "decision provider is none")
}

// NewNone returns the disabled provider.
func NewNone() Provider { return none{} }

// ---- configuration ----------------------------------------------------------

// SurfaceConfig is one entry of decisions.surfaces.
type SurfaceConfig struct {
	Mode          string  `yaml:"mode"`
	MinConfidence float64 `yaml:"min_confidence"`
}

// BudgetConfig is decisions.budget.
type BudgetConfig struct {
	MaxCallsPerSession int     `yaml:"max_calls_per_session"`
	MaxUSDPerDay       float64 `yaml:"max_usd_per_day"`
}

// EgressConfig is decisions.egress.
type EgressConfig struct {
	Level      string   `yaml:"level"`
	NeverPaths []string `yaml:"never_paths"`
}

// Config is the `decisions:` block of .yakos.yml. It never holds a credential.
type Config struct {
	// Provider is what the project file says. A project file can only turn a
	// provider OFF (provider: none); it can never turn one on. See
	// ResolveProvider.
	Provider string `yaml:"provider"`
	// ProviderSet is true when the file carries an explicit provider value.
	ProviderSet bool                     `yaml:"-"`
	Model       string                   `yaml:"model"`
	DefaultMode string                   `yaml:"default_mode"`
	Surfaces    map[string]SurfaceConfig `yaml:"surfaces"`
	Budget      BudgetConfig             `yaml:"budget"`
	Egress      EgressConfig             `yaml:"egress"`
}

// Defaults documented in ADR-0009.
const (
	DefaultMaxCallsPerSession = 2000
	DefaultMaxUSDPerDay       = 1.00
)

// DefaultNeverPaths are always merged into the operator's never_paths.
var DefaultNeverPaths = []string{
	"**/.env*", ".env*", "**/*.pem", "*.pem", "**/*.key", "*.key",
	"**/credentials/**", "credentials/**", "**/secrets/**", "secrets/**",
	"**/id_rsa*", "**/id_ed25519*",
	// Credential FILES (not directories): ~/.aws/credentials, netrc, git,
	// pgpass, npm and docker client credentials.
	"**/.aws/credentials", ".aws/credentials", "**/.netrc", ".netrc",
	"**/.git-credentials", ".git-credentials", "**/.pgpass", ".pgpass",
	"**/.npmrc", ".npmrc", "**/.docker/config.json", ".docker/config.json",
}

// DefaultConfig is what an absent decisions: block means: provider none.
func DefaultConfig() Config {
	return Config{
		Provider:    ProviderNone,
		Model:       PinnedModel,
		DefaultMode: ModeShadow,
		// Budget and Egress are left zero on purpose: callers apply Tighten
		// with the user-level Policy, which supplies the defaults and is the
		// ceiling a project can only tighten.
	}
}

// LoadConfig reads the `decisions:` block from a .yakos.yml at path. A
// missing file or absent block yields DefaultConfig. Zero-valued fields are
// filled from the defaults. A malformed file is an error (callers treat it as
// "provider unavailable", never as "enabled").
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	var doc struct {
		Decisions *Config `yaml:"decisions"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return DefaultConfig(), fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Decisions == nil {
		return cfg, nil
	}
	got := *doc.Decisions
	def := DefaultConfig()
	got.ProviderSet = got.Provider != ""
	if got.Provider == "" {
		got.Provider = def.Provider
	}
	if got.Model == "" {
		got.Model = def.Model
	}
	if got.DefaultMode == "" {
		got.DefaultMode = def.DefaultMode
	}
	return got, nil
}

// ---- policy: project config may only tighten ---------------------------------

// PolicyFileName is the user-level ceiling, kept in the state directory (which
// is owner-only and outside any repository), so a cloned project cannot write
// it. The defaults below apply when it is absent.
const PolicyFileName = "decision-policy.yml"

// Policy is the user-level ceiling for budget and egress.
type Policy struct {
	// Provider is the user-level switch that turns a provider on ("jev", "mock").
	// It lives here, outside any repository, because a cloned project must not
	// be able to start sending data to a third party by itself.
	Provider string       `yaml:"provider"`
	Budget   BudgetConfig `yaml:"budget"`
	Egress   EgressConfig `yaml:"egress"`
}

// DefaultPolicy is the documented ceiling: 2000 calls, $1/day, strict egress.
func DefaultPolicy() Policy {
	return Policy{
		Budget: BudgetConfig{MaxCallsPerSession: DefaultMaxCallsPerSession, MaxUSDPerDay: DefaultMaxUSDPerDay},
		Egress: EgressConfig{Level: EgressStrict},
	}
}

// LoadPolicy reads the user-level policy file; a missing file is the default.
// The operator loosens the ceiling here, never in a project's .yakos.yml.
func LoadPolicy(path string) (Policy, error) {
	p := DefaultPolicy()
	data, err := os.ReadFile(path) //nolint:gosec // state-dir file
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return DefaultPolicy(), err
	}
	var got Policy
	if err := yaml.Unmarshal(data, &got); err != nil {
		return DefaultPolicy(), fmt.Errorf("parse %s: %w", path, err)
	}
	if got.Budget.MaxCallsPerSession > 0 {
		p.Budget.MaxCallsPerSession = got.Budget.MaxCallsPerSession
	}
	if got.Budget.MaxUSDPerDay > 0 {
		p.Budget.MaxUSDPerDay = got.Budget.MaxUSDPerDay
	}
	if got.Egress.Level != "" {
		p.Egress.Level = got.Egress.Level
	}
	p.Egress.NeverPaths = got.Egress.NeverPaths
	p.Provider = strings.TrimSpace(got.Provider)
	return p, nil
}

// ResolveProvider picks the provider for one call. Enabling one is a USER
// decision: --provider, then $YAKOS_DECISION_PROVIDER, then `provider:` in the
// user-level policy file (~/.yakos-state/decision-policy.yml). A project
// .yakos.yml may only veto: an explicit `provider: none` there turns the
// user-level policy switch off for that project (an explicit flag or env var
// in the shell still wins). Any other value
// in the project file is ignored and reported in warning, because a cloned
// repository must not be able to start egress on its own.
func ResolveProvider(flag string, getenv func(string) string, project Config, policy Policy) (name, warning string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if flag != "" {
		return flag, ""
	}
	if project.ProviderSet && project.Provider != "" && project.Provider != ProviderNone {
		warning = fmt.Sprintf("decisions.provider %q in the project .yakos.yml is ignored: a project file cannot enable a provider (set %s, or provider: in ~/.yakos-state/%s)", project.Provider, EnvProvider, PolicyFileName)
	}
	if v := getenv(EnvProvider); v != "" {
		return v, warning // an explicit choice in this shell
	}
	if project.ProviderSet && project.Provider == ProviderNone {
		return ProviderNone, "" // the project opts out of the user-level switch
	}
	if policy.Provider != "" {
		return policy.Provider, warning
	}
	return ProviderNone, warning
}

// Tighten returns cfg with budget and egress clamped to the policy: caps are
// the minimum of the two, the egress level is the more restrictive, and
// never_paths are the union. A project can only make things stricter.
func Tighten(cfg Config, p Policy) Config {
	if cfg.Budget.MaxCallsPerSession <= 0 || cfg.Budget.MaxCallsPerSession > p.Budget.MaxCallsPerSession {
		cfg.Budget.MaxCallsPerSession = p.Budget.MaxCallsPerSession
	}
	if cfg.Budget.MaxUSDPerDay <= 0 || cfg.Budget.MaxUSDPerDay > p.Budget.MaxUSDPerDay {
		cfg.Budget.MaxUSDPerDay = p.Budget.MaxUSDPerDay
	}
	if EgressRank(cfg.Egress.Level) > EgressRank(p.Egress.Level) || cfg.Egress.Level == "" {
		cfg.Egress.Level = p.Egress.Level
	}
	if EgressRank(cfg.Egress.Level) == 0 {
		cfg.Egress.Level = EgressStrict // normalises unknown levels
	}
	seen := map[string]bool{}
	var never []string
	for _, x := range append(append([]string{}, p.Egress.NeverPaths...), cfg.Egress.NeverPaths...) {
		if !seen[x] {
			seen[x] = true
			never = append(never, x)
		}
	}
	cfg.Egress.NeverPaths = never
	return cfg
}

// ---- state-dir files --------------------------------------------------------

const (
	LogFileName      = "decision-log.ndjson"
	BreakerFileName  = "decision-breaker.json"
	BudgetFileName   = "decision-budget.json"
	PromotionsName   = "decision-promotions.ndjson"
	killSwitchEnvVar = "YAKOS_DECISION_DISABLE"
	// EnvMock selects the mock provider's fixture (file or directory).
	EnvMock = "YAKOS_DECISION_MOCK"
	// EnvProvider overrides decisions.provider.
	EnvProvider = "YAKOS_DECISION_PROVIDER"
)

// StatePaths resolves the decision state files under dir (statepath.Dir()
// when empty). They live beside, never inside, the dispatch-log.
type StatePaths struct{ Dir string }

func (s StatePaths) dir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return statepath.Dir()
}
func (s StatePaths) Log() string        { return filepath.Join(s.dir(), LogFileName) }
func (s StatePaths) Breaker() string    { return filepath.Join(s.dir(), BreakerFileName) }
func (s StatePaths) Budget() string     { return filepath.Join(s.dir(), BudgetFileName) }
func (s StatePaths) Promotions() string { return filepath.Join(s.dir(), PromotionsName) }
func (s StatePaths) Policy() string     { return filepath.Join(s.dir(), PolicyFileName) }

// KillSwitch reports whether YAKOS_DECISION_DISABLE=1 (checked before
// anything else).
func KillSwitch(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv(killSwitchEnvVar) == "1"
}

// ---- orchestration ----------------------------------------------------------

// Engine wires a provider to sanitisation and logging.
type Engine struct {
	Provider Provider
	Logger   *Logger // nil disables logging
	Egress   EgressConfig
	Now      func() time.Time
	// LocalVerdict / LocalTrigger, when set, are copied into the decision-log
	// record (shadow-vs-local comparison). They never reach the provider.
	LocalVerdict string
	LocalTrigger string
	// Tag is copied into the record (see Record.Tag).
	Tag string
}

// Outcome is what Execute returns. Exactly one of Result / Err is set.
type Outcome struct {
	Result *Result
	Err    error
	Class  string // ErrorClass(Err); "" on success
}

// Execute runs one decision for a loaded question set: it drops non-allowlisted
// state fields, redacts, size-caps, calls the provider, applies the model pin
// from the set, and appends one decision-log record. It never returns the raw
// state and never logs it.
func (e *Engine) Execute(ctx context.Context, set *QuestionSet, state any, mode, session string, timeout time.Duration) Outcome {
	now := e.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	if mode != ModeShadow && mode != ModePrefilter {
		mode = ModePrefilter
	}
	if timeout <= 0 {
		timeout = PrefilterTimeout
		if mode == ModeShadow {
			timeout = ShadowTimeout
		}
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}

	rec := Record{
		Type: "decision", TS: start.UTC().Format(time.RFC3339Nano), ID: newID(),
		Surface: set.Surface, SchemaID: set.SchemaID, SchemaHash: set.Hash,
		Provider: e.Provider.Name(), Model: set.Model, Mode: mode, Session: session,
		LocalVerdict: e.LocalVerdict, LocalTrigger: e.LocalTrigger, Tag: e.Tag,
	}
	finish := func(res *Result, err error) Outcome {
		rec.LatencyMS = now().Sub(start).Milliseconds()
		if err != nil {
			rec.ErrorClass = ErrorClass(err)
			rec.Status = rec.ErrorClass
		} else {
			rec.Status = "ok"
			rec.Model = res.Model
			rec.InputTokens, rec.OutputTokens = res.Usage.InputTokens, res.Usage.OutputTokens
			rec.CostUSD = res.CostUSD
			rec.Answers = SummarizeAnswers(res.Answers)
		}
		if e.Logger != nil {
			_ = e.Logger.Append(rec) // logging must never change the outcome
		}
		return Outcome{Result: res, Err: err, Class: rec.ErrorClass}
	}

	san, stats, err := Sanitize(state, SanitizeOptions{
		Level:         e.Egress.Level,
		NeverPaths:    e.Egress.NeverPaths,
		AllowedFields: set.StateFields,
		MaxBytes:      set.MaxStateBytes,
	})
	rec.Redactions, rec.StateBytes = stats.Redactions, stats.Bytes
	if err != nil {
		return finish(nil, err)
	}

	// Hard deadline for every provider, including one that ignores its
	// context: the call runs in its own goroutine and Execute stops waiting at
	// the deadline. (An abandoned call finishes or dies with the process; the
	// caller is a short-lived CLI.)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := Request{
		Surface: set.Surface, SchemaID: set.SchemaID, SchemaHash: set.Hash,
		Model: set.Model, State: san, Questions: set.Questions,
		Mode: mode, Timeout: timeout, Session: session,
	}
	type decided struct {
		res *Result
		err error
	}
	ch := make(chan decided, 1) // buffered: an abandoned call never leaks blocked on send
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- decided{err: newErr(ClassInternal, "provider panic")}
			}
		}()
		res, err := e.Provider.Decide(ctx, req)
		ch <- decided{res, err}
	}()
	select {
	case d := <-ch:
		if d.err != nil {
			return finish(nil, d.err)
		}
		return finish(d.res, nil)
	case <-ctx.Done():
		return finish(nil, newErr(ClassTimeout, "deadline exceeded"))
	}
}

func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}
