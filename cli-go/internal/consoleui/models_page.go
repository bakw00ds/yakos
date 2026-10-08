package consoleui

// models_page.go: the read side of the console's Models & Providers tab (K-153).
//
//	GET /api/models/overview   providers, catalog, aliases, budgets, evals, router
//	                           policy and the sensitive class, in one document
//	GET /api/models/explain    the router's dry-run decision for an agent (the
//	                           K-139b explain behind the tab's playground)
//	GET /api/router/policy     the router policy view (rules, pins, sha)
//
// Auth: RoleRead on all three; idempotent GETs, default rate-limit class. They
// change nothing: this slice has no browser write path. The policy files are edited
// with `yakos models ...` and `yakos router policy set`, which go through the one
// trusted writer and the audit line (the hardened browser writes are K-153b).
//
// What the responses never carry: a filesystem path, a credential or the value of
// an environment variable. Provider sign-in hints name variables (OPENAI_API_KEY)
// and gateway_classes name the variable a class sets; both are names. Every
// response is Cache-Control: no-store. Route metadata is for the operator and the
// ledger and never enters a prompt (rule:cache-stability).

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/budget"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/modelreg"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/router"
	"github.com/bakw00ds/yakos/internal/statepath"
)

const (
	probeCacheTTL = 15 * time.Second
	probeTimeout  = 3 * time.Second
	// evalTailBytes bounds how much of the dispatch log's end is scanned for eval
	// results; maxEvals bounds how many are returned.
	evalTailBytes = 1 << 20
	maxEvals      = 10
)

var (
	modelsAgentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	modelsClassRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

// modelsPage serves the Models tab. Its fields are replaced by tests.
type modelsPage struct {
	workspaceRoot, yakosRoot string
	stateDir                 func() string
	logPath                  func() string
	probe                    func(ctx context.Context, harness string) auth.ProbeResult
	cooling                  func(project, runtime string) (bool, time.Duration)
	now                      func() time.Time

	// w is the browser-write side (K-175); disabled unless Config.ModelWrites.
	w *modelsWriter

	mu     sync.Mutex
	probes map[string]probeEntry
}

type probeEntry struct {
	at  time.Time
	res auth.ProbeResult
}

func newModelsPage(workspaceRoot, yakosRoot string) *modelsPage {
	return &modelsPage{
		workspaceRoot: workspaceRoot, yakosRoot: yakosRoot,
		stateDir: modelreg.DefaultStateDir, logPath: statepath.DispatchLog,
		probe: auth.ProbeRuntime, cooling: dispatch.RuntimeCooling, now: time.Now,
		probes: map[string]probeEntry{},
		w:      &modelsWriter{},
	}
}

func writeModelsJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func modelsError(w http.ResponseWriter, status int, msg string) {
	writeModelsJSON(w, status, map[string]string{"error": msg})
}

func getOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

type providerView struct {
	Harness   string `json:"harness"`
	Provider  string `json:"provider"`
	Installed bool   `json:"installed"`
	SignedIn  bool   `json:"signed_in"`
	// Hint is the install or sign-in instruction; it names variables, not values.
	Hint            string `json:"hint,omitempty"`
	Cooling         bool   `json:"cooling"`
	CooldownSeconds int    `json:"cooldown_seconds"`
}

type budgetView struct {
	Agent       string  `json:"agent"`
	State       string  `json:"state"`
	Window      string  `json:"window"`
	LimitUSD    float64 `json:"limit_usd"`
	SpentUSD    float64 `json:"spent_usd"`
	LimitTokens int64   `json:"limit_tokens"`
	SpentTokens int64   `json:"spent_tokens"`
	Pct         float64 `json:"pct"`
	ReadFailed  bool    `json:"read_failed,omitempty"`
}

type evalView struct {
	RunID          string             `json:"run_id"`
	Agent          string             `json:"agent"`
	Ts             string             `json:"ts"`
	Partial        bool               `json:"partial"`
	TierPassRates  map[string]float64 `json:"tier_pass_rates"`
	TierMeanCosts  map[string]float64 `json:"tier_mean_costs"`
	CandidateTier  string             `json:"candidate_tier,omitempty"`
	CandidateFound bool               `json:"candidate_emitted"`
}

type aliasView struct {
	Alias string            `json:"alias"`
	By    map[string]string `json:"by"` // harness -> model id ("" is the harness default)
}

type overviewResponse struct {
	// WritesEnabled says the operator turned browser writes on (--console-model-writes);
	// CanWrite is that and the caller being an admin. The tab draws write controls
	// only for CanWrite.
	WritesEnabled bool `json:"writes_enabled"`
	CanWrite      bool `json:"can_write"`
	// PrivilegedHidden says router.allow_unsandboxed_runtimes was left out because
	// the caller is below admin.
	PrivilegedHidden bool                 `json:"privileged_hidden"`
	Providers        []providerView       `json:"providers"`
	Models           []modelreg.Entry     `json:"models"`
	Aliases          []aliasView          `json:"aliases"`
	Warnings         int                  `json:"registry_warnings"`
	Budgets          []budgetView         `json:"budgets"`
	Evals            []evalView           `json:"evals"`
	Router           router.PolicyView    `json:"router"`
	Sensitive        router.SensitiveView `json:"sensitive"`
}

func (m *modelsPage) probeOf(ctx context.Context, harness string) auth.ProbeResult {
	m.mu.Lock()
	e, ok := m.probes[harness]
	m.mu.Unlock()
	if ok && m.now().Sub(e.at) < probeCacheTTL {
		return e.res
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	res := m.probe(pctx, harness)
	m.mu.Lock()
	m.probes[harness] = probeEntry{at: m.now(), res: res}
	m.mu.Unlock()
	return res
}

func (m *modelsPage) handleOverview(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	state := m.stateDir()
	// Discovery results come from the on-disk cache only; this page never runs a
	// harness CLI to list models (`yakos models probe` does that).
	reg, err := modelreg.Load(modelreg.Options{
		StateDir: state, Project: m.workspaceRoot,
		Snapshots: modelreg.NewDiscoverer(modelreg.DiscovererConfig{StateDir: state}),
	})
	if err != nil {
		modelsError(w, http.StatusServiceUnavailable, "model registry unavailable")
		return
	}
	id := netid.IdentityFrom(r.Context())
	out := overviewResponse{
		WritesEnabled: m.w.enabled, CanWrite: m.w.enabled && id.Role.Allows(netid.RoleAdmin),
		PrivilegedHidden: !id.Role.Allows(netid.RoleAdmin),
		Providers:        []providerView{}, Models: []modelreg.Entry{}, Aliases: []aliasView{}, Budgets: []budgetView{},
		Warnings: len(reg.Warnings()), Router: router.ViewOf(state), Sensitive: router.Sensitive(),
	}
	for _, h := range modelreg.Harnesses {
		p := m.probeOf(r.Context(), h)
		v := providerView{Harness: h, Provider: modelreg.DefaultProvider[h], Installed: p.CLIPresent, SignedIn: p.Authed}
		switch {
		case !p.CLIPresent:
			v.Hint = p.CLIHint
		case !p.Authed:
			v.Hint = p.AuthHint
		}
		if cool, left := m.cooling(m.workspaceRoot, h); cool {
			v.Cooling, v.CooldownSeconds = true, int(left.Seconds())+1
		}
		out.Providers = append(out.Providers, v)
	}
	for _, e := range reg.Entries() {
		if modelreg.ValidID(e.ID) {
			out.Models = append(out.Models, e)
		}
	}
	for _, a := range modelreg.AliasNames {
		av := aliasView{Alias: a, By: map[string]string{}}
		for _, h := range modelreg.Harnesses {
			if id, ok := reg.ResolveAlias(h, a); ok {
				av.By[h] = id
			}
		}
		out.Aliases = append(out.Aliases, av)
	}
	out.Router = redactPolicy(out.Router, id)
	out.Budgets = m.budgets()
	out.Evals = m.evals()
	writeModelsJSON(w, http.StatusOK, out)
}

// budgets evaluates every agent with a limit. Only numbers and states leave: not
// the per-project spend (a project path) and not the status warnings (they can echo
// project text).
func (m *modelsPage) budgets() []budgetView {
	opts := budget.Options{Project: m.workspaceRoot}
	pol, _ := budget.LoadPolicy(opts.StateDirOrDefault())
	rows := []budgetView{}
	for _, a := range budget.AgentNamesForProject(pol, m.workspaceRoot) {
		st, _ := budget.Evaluate(a, opts)
		rows = append(rows, budgetView{
			Agent: st.Agent, State: string(st.State), Window: string(st.Window),
			LimitUSD: st.LimitUSD, SpentUSD: st.SpentUSD, LimitTokens: st.LimitTokens, SpentTokens: st.SpentTokens,
			Pct: st.Pct, ReadFailed: st.ReadFailed,
		})
	}
	return rows
}

// evals returns the most recent finished eval runs, newest first, read from the
// tail of the dispatch log. A log that cannot be read is no results.
func (m *modelsPage) evals() []evalView {
	out := []evalView{}
	path := m.logPath()
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return out
	}
	f, err := os.Open(path) //nolint:gosec // the daemon's own log, regular file checked above
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	if fi.Size() > evalTailBytes {
		if _, err := f.Seek(fi.Size()-evalTailBytes, io.SeekStart); err != nil {
			return out
		}
	}
	var all []evalView
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"eval_run_finished"`) {
			continue
		}
		var raw struct {
			Type    string             `json:"type"`
			Ts      string             `json:"ts"`
			RunID   string             `json:"run_id"`
			Agent   string             `json:"agent"`
			Partial bool               `json:"partial"`
			Rates   map[string]float64 `json:"tier_pass_rates"`
			Costs   map[string]float64 `json:"tier_mean_costs"`
			Emitted bool               `json:"candidate_emitted"`
			Tier    *string            `json:"candidate_tier"`
		}
		if json.Unmarshal(line, &raw) != nil || raw.Type != "eval_run_finished" || !modelsAgentRe.MatchString(raw.Agent) || boundedIdent(raw.RunID) == "" {
			continue // not an eval record this build wrote: skip it rather than show it half-trusted
		}
		ev := evalView{RunID: raw.RunID, Agent: raw.Agent, Ts: boundedIdent(raw.Ts), Partial: raw.Partial,
			TierPassRates: boundedKeys(raw.Rates), TierMeanCosts: boundedKeys(raw.Costs), CandidateFound: raw.Emitted}
		if raw.Tier != nil {
			ev.CandidateTier = boundedIdent(*raw.Tier)
		}
		all = append(all, ev)
	}
	for i := len(all) - 1; i >= 0 && len(out) < maxEvals; i-- {
		out = append(out, all[i])
	}
	return out
}

// boundedKeys drops the entries whose key is not a short printable identifier
// (a tier name); the log is a file the console does not own.
func boundedKeys(in map[string]float64) map[string]float64 {
	if in == nil {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		if boundedIdent(k) != "" {
			out[k] = v
		}
	}
	return out
}

// boundedIdent returns s when it is a short printable identifier, else "".
func boundedIdent(s string) string {
	if len(s) > 64 {
		return ""
	}
	for _, c := range s {
		if c < 0x20 || c > 0x7e {
			return ""
		}
	}
	return s
}

func (m *modelsPage) handlePolicy(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	writeModelsJSON(w, http.StatusOK, redactPolicy(router.ViewOf(m.stateDir()), netid.IdentityFrom(r.Context())))
}

// redactPolicy hides the runtimes the operator allowed to run unsandboxed from
// everyone below admin: which harnesses may run without a sandbox is a map for an
// attacker and not something a read-only role needs (K-175).
func redactPolicy(v router.PolicyView, id netid.Identity) router.PolicyView {
	if !id.Role.Allows(netid.RoleAdmin) {
		v.AllowUnsandboxedRuntimes = []string{}
	}
	return v
}

// handleExplain is the playground: GET /api/models/explain?agent=A[&class=C]
// [&task_bytes=N]. A dry run (dispatch.Explain): nothing starts, no ledger row is
// written, no conversation is pinned.
func (m *modelsPage) handleExplain(w http.ResponseWriter, r *http.Request) {
	if !getOnly(w, r) {
		return
	}
	q := r.URL.Query()
	agent, class := q.Get("agent"), q.Get("class")
	if !modelsAgentRe.MatchString(agent) {
		modelsError(w, http.StatusBadRequest, "invalid agent name")
		return
	}
	if class != "" {
		if !modelsClassRe.MatchString(class) {
			modelsError(w, http.StatusBadRequest, "invalid class")
			return
		}
		known := false
		for _, c := range router.KnownClasses(m.stateDir()) {
			known = known || c == class
		}
		// "sensitive" is declarable (the router keeps it for a request however it
		// is set); dispatch.Explain below is the same dry run the CLI uses.
		known = known || class == router.ClassSensitive
		if !known {
			modelsError(w, http.StatusBadRequest, "unknown class")
			return
		}
	}
	var taskBytes int64
	if s := q.Get("task_bytes"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 || n > 1<<40 {
			modelsError(w, http.StatusBadRequest, "invalid task_bytes")
			return
		}
		taskBytes = n
	}
	if m.workspaceRoot == "" {
		modelsError(w, http.StatusServiceUnavailable, "no workspace")
		return
	}
	d, err := explainDecision(r.Context(), dispatch.ExplainQuery{
		YakosRoot: m.yakosRoot, Project: m.workspaceRoot, Agent: agent, Class: class, TaskBytes: taskBytes,
	})
	if err != nil {
		// The message can name a path; send a fixed one.
		if strings.Contains(err.Error(), "not found in composed set") {
			modelsError(w, http.StatusNotFound, "unknown agent")
			return
		}
		modelsError(w, http.StatusUnprocessableEntity, "no route could be decided")
		return
	}
	b, err := router.ExplainJSON(router.ExplainView{Agent: agent, Decision: d})
	if err != nil {
		modelsError(w, http.StatusInternalServerError, "cannot encode the result")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

// explainDecision is dispatch.Explain; tests replace it.
var explainDecision = dispatch.Explain
