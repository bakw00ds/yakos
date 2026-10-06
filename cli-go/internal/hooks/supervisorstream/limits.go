package supervisorstream

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// optInt is a whole-number config value that decodes leniently: only a plain
// decimal scalar is accepted (no quotes, sign, hex or underscores); anything
// else leaves ok false and never fails the surrounding block, so one bad key
// no longer resets the rest of supervisor: to defaults. Bash twin: _ss_int.
type optInt struct {
	ok bool
	v  int
}

var decimalRe = regexp.MustCompile(`^[0-9]+$`)

func (o *optInt) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Style == 0 {
		if v, ok := parseDecimal(n.Value); ok {
			o.ok, o.v = true, v
		}
	}
	return nil
}

// parseDecimal accepts digits only; more than 9 digits clamps (never overflows).
func parseDecimal(s string) (int, bool) {
	if !decimalRe.MatchString(s) {
		return 0, false
	}
	if len(s) > 9 {
		return 999999999, true
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

// resolveModel maps the semantic aliases `yakos dispatch` knows to tiers and
// reports a name that is still not a tier (bad), falling back to haiku:
// dispatch dies on an unknown tier. Bash twin: the sup_model case blocks.
func resolveModel(m string) (model, bad string) {
	switch m {
	case "cheap":
		m = "haiku"
	case "balanced":
		m = "sonnet"
	case "best", "reasoning":
		m = "opus"
	case "frontier":
		m = "fable"
	}
	switch m {
	case "haiku", "sonnet", "opus", "fable":
		return m, ""
	}
	return "haiku", m
}

// limits are the effective launch-gate settings for one hook run.
type limits struct {
	cap, interval, deadline, backoffMin int
	// ceil is the high-risk launch ceiling: 3x the TRUSTED cap (policy or
	// default), never reduced by a project value. 0 means unlimited.
	ceil int
	// bud is the supervisor's dollar-budget position (K-119), evaluated
	// in-process at launch time. Zero value = no budget in force.
	bud     budgetGate
	ignored []string // project values refused because they reduce supervision
	invalid []string // values below their minimum, replaced by the default
}

// policyFileName is the user-level, trusted file that may LOOSEN the limits.
const policyFileName = "supervisor-policy.yml"

type policyFile struct {
	MaxLaunches    optInt `yaml:"max_launches_per_session"`
	MinInterval    optInt `yaml:"min_launch_interval_s"`
	RunDeadline    optInt `yaml:"run_deadline_s"`
	SessionBackoff optInt `yaml:"session_limit_backoff_min"`
}

// strictness says which direction of change means MORE supervision.
type strictness int

const (
	stricterUp   strictness = iota // a larger value is stricter (deadline)
	stricterDown                   // a smaller value is stricter (interval, backoff)
	stricterUp0                    // larger, or 0 (unlimited), is stricter (cap)
)

// limitTable is the one definition of the per-key limits. A project
// .yakos.yml may only move a limit in the stricter direction (more
// supervision); the trusted user-level policy may move it either way. The
// bash twin mirrors it in the comment above _ss_limit.
//
//	key                        default      range       project may
//	max_launches_per_session   30           0..10000    raise it, or 0
//	min_launch_interval_s      120          0..86400    lower it
//	run_deadline_s             240/480/600  floor..3600 lengthen it (floor 30 s)
//	session_limit_backoff_min  30           0..1440     lower it
var limitTable = []struct {
	key    string
	lo, hi int
	dir    strictness
}{
	{"max_launches_per_session", 0, 10000, stricterUp0},
	{"min_launch_interval_s", 0, 86400, stricterDown},
	{"run_deadline_s", 0, 3600, stricterUp},
	{"session_limit_backoff_min", 0, 1440, stricterDown},
}

func tableRow(key string) (lo, hi int, dir strictness) {
	for _, r := range limitTable {
		if r.key == key {
			return r.lo, r.hi, r.dir
		}
	}
	return 0, 0, stricterUp
}

// loadPolicy reads ~/.yakos-state/supervisor-policy.yml when it is trusted
// (regular file, ours, not group/world writable): the same bar as the decision
// policy. Bash twin: _ss_policy_trusted + _ss_limit.
func loadPolicy() policyFile {
	var pf policyFile
	path := filepath.Join(statepath.Dir(), policyFileName)
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return pf
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o022 != 0 {
		return pf
	}
	if !ownedByCurrentUser(fi) {
		return pf
	}
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return pf
	}
	_ = yaml.Unmarshal(data, &pf)
	return pf
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// pick resolves one key: base = trusted policy value (else def); the project
// value applies only when stricter. floor > 0 makes smaller values invalid.
func pick(key string, def int, user, proj optInt, floor int, ignored, invalid *[]string) (val, base int) {
	lo, hi, dir := tableRow(key)
	base = def
	if user.ok {
		if user.v >= floor {
			base = clampInt(user.v, lo, hi)
		} else {
			*invalid = append(*invalid, key)
		}
	}
	if !proj.ok {
		return base, base
	}
	if proj.v < floor {
		*invalid = append(*invalid, key)
		return base, base
	}
	pv := clampInt(proj.v, lo, hi)
	better := false
	switch dir {
	case stricterUp:
		better = pv > base
	case stricterDown:
		better = pv < base
	case stricterUp0:
		better = base > 0 && (pv == 0 || pv > base)
	}
	if better {
		return pv, base
	}
	if pv != base {
		*ignored = append(*ignored, key)
	}
	return base, base
}

// minDeadline is the smallest accepted run deadline: a smaller one only kills
// runs. Env YAKOS_SUPERVISOR_MIN_DEADLINE_S (operator/test knob) overrides 30.
func minDeadline(env map[string]string) int {
	v := env["YAKOS_SUPERVISOR_MIN_DEADLINE_S"]
	if v == "" {
		v = os.Getenv("YAKOS_SUPERVISOR_MIN_DEADLINE_S")
	}
	if n, ok := parseDecimal(v); ok {
		return n
	}
	return 30
}

// resolveLimits computes the gate limits. The deadline default scales with the
// model tier (haiku 240 s, sonnet 480 s, opus/fable 600 s).
func resolveLimits(cfg *supervisorConfig, model string, env map[string]string) limits {
	defDeadline := 240
	switch model {
	case "sonnet":
		defDeadline = 480
	case "opus", "fable":
		defDeadline = 600
	}
	var proj supervisorConfig
	if cfg != nil {
		proj = *cfg
	}
	pol := loadPolicy()
	var ig, inv []string
	l := limits{}
	var capBase int
	l.cap, capBase = pick("max_launches_per_session", 30, pol.MaxLaunches, proj.MaxLaunches, 0, &ig, &inv)
	l.ceil = capBase * 3
	l.interval, _ = pick("min_launch_interval_s", 120, pol.MinInterval, proj.MinInterval, 0, &ig, &inv)
	l.deadline, _ = pick("run_deadline_s", defDeadline, pol.RunDeadline, proj.RunDeadline, minDeadline(env), &ig, &inv)
	l.backoffMin, _ = pick("session_limit_backoff_min", 30, pol.SessionBackoff, proj.SessionBackoff, 0, &ig, &inv)
	l.ignored, l.invalid = ig, inv
	return l
}

// budgetGate is the supervisor budget as the launch gate sees it. hard is
// state hard_stop (spent >= limit); over is spent >= the dispatch stop (2x the
// limit), the ceiling for high-risk launches. Both false when the budget is
// off or the read failed (fail open). cause is empty unless the read failed
// (K-128, S3): then it says why and the gate writes one WARN naming it. A budget
// that is merely off is not a failure. Bash twin: _ss_budget.
type budgetGate struct {
	hard, over bool
	state      string
	spent      float64
	limit      float64
	stop       float64
	cause      string
}

// denyReason is why allowLaunch refused ("" means allow).
type denyReason string

const (
	denyNone     denyReason = ""
	denyBackoff  denyReason = "backoff"
	denyCeiling  denyReason = "ceiling"
	denyCap      denyReason = "cap"
	denyInterval denyReason = "interval"
	// denyBudget: routine launch refused at the supervisor's hard_stop.
	denyBudget denyReason = "budget"
	// denyBudgetCeiling: high-risk launch refused past 2x the limit.
	denyBudgetCeiling denyReason = "budget-ceiling"
)

// allowLaunch is the single gate decision. High-risk launches bypass the cap
// and the interval up to a ceiling of 3x the cap; routine launches count
// against the cap and wait out the interval; an account session limit pauses
// both. The #316 dollar budget plugs in here and exempts high-risk the same
// way: routine launches are refused at the supervisor's hard_stop, high-risk
// ones run on up to 2x the limit (decided here, in-process: no env var or flag
// carries the exemption). Bash twin: _ss_allow.
func allowLaunch(st runState, lim limits, high bool, now int64) denyReason {
	if st.backoff > now {
		return denyBackoff
	}
	if high {
		if lim.ceil > 0 && st.hlaunches >= lim.ceil {
			return denyCeiling
		}
		if lim.bud.over {
			return denyBudgetCeiling
		}
		return denyNone
	}
	if lim.bud.hard {
		return denyBudget
	}
	if lim.cap > 0 && st.launches >= lim.cap {
		return denyCap
	}
	if lim.interval > 0 && now-st.last < int64(lim.interval) {
		return denyInterval
	}
	return denyNone
}
