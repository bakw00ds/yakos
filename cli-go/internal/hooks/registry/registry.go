// Package registry is the explicit, sorted list of Tier-0 Go-native hooks
// `yakos hook run <name>` can dispatch to.
//
// Per rule:cache-stability ("non-deterministic ordering... map/dict
// iteration order... unsorted roster compose... Same inputs must serialize
// to the same bytes every time"), the registry is an explicit slice
// literal in name order, never a map iterated for Names() or any other
// output. Names() sorts defensively anyway so a future accidental
// reordering of the slice literal cannot silently change output order.
package registry

import (
	"context"
	"sort"

	"github.com/bakw00ds/yakos/internal/hooks/autocompacttrigger"
	"github.com/bakw00ds/yakos/internal/hooks/budgetguard"
	"github.com/bakw00ds/yakos/internal/hooks/contextinject"
	"github.com/bakw00ds/yakos/internal/hooks/contextthreshold"
	"github.com/bakw00ds/yakos/internal/hooks/cyclecounter"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/mailboxmirror"
	"github.com/bakw00ds/yakos/internal/hooks/outputinjectionscan"
	"github.com/bakw00ds/yakos/internal/hooks/pathallowlist"
	"github.com/bakw00ds/yakos/internal/hooks/pathlog"
	"github.com/bakw00ds/yakos/internal/hooks/peerclaim"
	"github.com/bakw00ds/yakos/internal/hooks/peerclaimconfirm"
	"github.com/bakw00ds/yakos/internal/hooks/planoutcomecapture"
	"github.com/bakw00ds/yakos/internal/hooks/planqualitygate"
	"github.com/bakw00ds/yakos/internal/hooks/retrodispatch"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
	"github.com/bakw00ds/yakos/internal/hooks/sessionendcheck"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorackgate"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorgate"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
	"github.com/bakw00ds/yakos/internal/hooks/taskcompletedispatch"
	"github.com/bakw00ds/yakos/internal/hooks/taskdependencygate"
	"github.com/bakw00ds/yakos/internal/hooks/teamlifecycle"
)

// Hook is the runner.Hook interface, re-exported here so callers that only
// import registry don't also need to import runner. (registry cannot import
// runner directly without an import cycle: runner would need to import
// registry to offer a "run by name" convenience, which it deliberately does
// not — dispatch-by-name lives in cmd/yakos/cmd_hook.go via this package.)
type Hook interface {
	Name() string
	Run(ctx context.Context, in hooktype.HookInput) (hooktype.HookOutput, error)
}

// Config carries every constructor parameter any registered hook needs.
// Not every hook uses every field; a hook whose constructor takes fewer
// arguments simply ignores the rest.
type Config struct {
	// WorkCurrentDir is the absolute path to work/current/ for the active
	// session. Used by every hook.
	WorkCurrentDir string

	// ProjectDir is the project root (bash's $CLAUDE_PROJECT_DIR). Used by
	// hooks that read project-local config such as .yakos.yml or
	// work/current/hook-bypass.md relative to the project rather than
	// work/current/ alone.
	ProjectDir string

	// StateDir is the yakOS state directory (bash's ~/.yakos-state/),
	// used by retro-dispatch for its PID file.
	StateDir string
}

// Entry describes one registered hook.
type Entry struct {
	// Name is the canonical hook name, matching lib/hooks/<Name>.sh minus
	// the extension (e.g. "path-log").
	Name string

	// FailClosed marks a hook that can BLOCK (exit 2) and therefore must
	// fail closed on degraded input (undecodable stdin, missing work dir),
	// mirroring lib/hooks/lib/hook-input.sh's HOOK_FAIL_CLOSED contract.
	// Telemetry-only hooks (path-log, mailbox-mirror, team-lifecycle,
	// session-end-check, and the telemetry branches of
	// output-injection-scan / task-dependency-gate /
	// task-complete-dispatch) leave this false and fail open, per the
	// bash README's "no-block policy for telemetry hooks".
	FailClosed bool

	// GoReady marks a hook whose Go output has been brought to parity with
	// its bash counterpart (verified by tests/run-hook-parity.sh) and is
	// therefore safe for `yakos refresh --hooks-impl=go` to register as
	// `yakos hook run <name>` instead of the .sh script. False means the
	// hook still runs but is not yet parity-verified; refresh in `go` mode
	// keeps writing the bash command for it until A-2 closes the gap
	// (S-6 structural plan §2.5).
	GoReady bool

	// New constructs the Hook for this entry from Config.
	New func(cfg Config) Hook
}

// entries is the explicit, name-sorted registry. Keep it sorted — Names()
// depends on it defensively, but a reviewer scanning this literal should
// see sorted order directly (rule:cache-stability).
var entries = []Entry{
	{
		Name:       "auto-compact-trigger",
		FailClosed: false,
		New:        func(cfg Config) Hook { return autocompacttrigger.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "budget-guard",
		FailClosed: true,
		New:        func(cfg Config) Hook { return budgetguard.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "context-inject",
		FailClosed: false,
		New:        func(cfg Config) Hook { return contextinject.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "context-threshold",
		FailClosed: false,
		New:        func(cfg Config) Hook { return contextthreshold.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "cycle-counter",
		FailClosed: false,
		New:        func(cfg Config) Hook { return cyclecounter.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "mailbox-mirror",
		FailClosed: false,
		New:        func(cfg Config) Hook { return mailboxmirror.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "output-injection-scan",
		FailClosed: false,
		New:        func(cfg Config) Hook { return outputinjectionscan.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "path-allowlist",
		FailClosed: true,
		New:        func(cfg Config) Hook { return pathallowlist.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "path-log",
		FailClosed: false,
		GoReady:    true,
		New:        func(cfg Config) Hook { return pathlog.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "peer-claim",
		FailClosed: true,
		New:        func(cfg Config) Hook { return peerclaim.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "peer-claim-confirm",
		FailClosed: false,
		New:        func(cfg Config) Hook { return peerclaimconfirm.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "plan-outcome-capture",
		FailClosed: false,
		New:        func(cfg Config) Hook { return planoutcomecapture.New() },
	},
	{
		Name:       "plan-quality-gate",
		FailClosed: false,
		New:        func(cfg Config) Hook { return planqualitygate.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "retro-dispatch",
		FailClosed: false,
		New:        func(cfg Config) Hook { return retrodispatch.New(cfg.WorkCurrentDir, cfg.StateDir) },
	},
	{
		Name:       "secret-scan",
		FailClosed: true,
		New:        func(cfg Config) Hook { return secretscan.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "session-end-check",
		FailClosed: false,
		New:        func(cfg Config) Hook { return sessionendcheck.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "supervisor-ack-gate",
		FailClosed: true,
		New:        func(cfg Config) Hook { return supervisorackgate.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "supervisor-gate",
		FailClosed: true,
		New:        func(cfg Config) Hook { return supervisorgate.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "supervisor-stream",
		FailClosed: false,
		New:        func(cfg Config) Hook { return supervisorstream.New(cfg.WorkCurrentDir, cfg.ProjectDir) },
	},
	{
		Name:       "task-complete-dispatch",
		FailClosed: false,
		New:        func(cfg Config) Hook { return taskcompletedispatch.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "task-dependency-gate",
		FailClosed: false,
		New:        func(cfg Config) Hook { return taskdependencygate.New(cfg.WorkCurrentDir) },
	},
	{
		Name:       "team-lifecycle",
		FailClosed: false,
		New:        func(cfg Config) Hook { return teamlifecycle.New(cfg.WorkCurrentDir) },
	},
}

// Names returns every registered hook name, sorted ascending.
func Names() []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}

// All returns every registered Entry, sorted by Name.
func All() []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup finds the entry for name and constructs its Hook via cfg. The
// second return value is the Entry (so callers can inspect FailClosed /
// GoReady without a second lookup); ok is false when name is not
// registered.
func Lookup(name string, cfg Config) (Hook, Entry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e.New(cfg), e, true
		}
	}
	return nil, Entry{}, false
}

// MustHaveEntries panics if entries is empty — a defensive check for
// init-time wiring bugs, exercised by TestEntriesNonEmpty rather than
// called in production code paths.
func MustHaveEntries() {
	if len(entries) == 0 {
		panic("registry: no hooks registered")
	}
}
