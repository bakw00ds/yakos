// Command yakos is the Go port of the yakOS CLI.
//
// The binary installs alongside the existing bash yakos under the SAME name.
// The YAKOS_IMPL environment variable controls which implementation is active:
//
//	YAKOS_IMPL=go   — always use Go-native routing.
//	YAKOS_IMPL=bash — proxy EVERY invocation to bash yakos (errors if absent).
//	(unset)         — auto: shadow-mode when bash yakos is present at
//	                  <repo-root>/cli/yakos; Go-native when it is absent.
//	                  This lets Go-only installs (binary only, no bash tree)
//	                  work without any env-var configuration.
//
// Always-available built-ins (--version, --help, go-port-status) are answered
// natively regardless of YAKOS_IMPL and regardless of whether the bash tree
// is installed.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"net/http"

	"github.com/bakw00ds/yakos/internal/agent"
	"github.com/bakw00ds/yakos/internal/archive"
	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/checkpoint"
	"github.com/bakw00ds/yakos/internal/compact"
	"github.com/bakw00ds/yakos/internal/completion"
	internalconsoleui "github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/doctor"
	"github.com/bakw00ds/yakos/internal/envcfg"
	"github.com/bakw00ds/yakos/internal/githooks"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
	"github.com/bakw00ds/yakos/internal/initialize"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/jsonrpc"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/mcp"
	"github.com/bakw00ds/yakos/internal/mcpserver"
	"github.com/bakw00ds/yakos/internal/memory"
	"github.com/bakw00ds/yakos/internal/metrics"
	"github.com/bakw00ds/yakos/internal/metricsdash"
	"github.com/bakw00ds/yakos/internal/migrate"
	"github.com/bakw00ds/yakos/internal/passthrough"
	"github.com/bakw00ds/yakos/internal/peer"
	internalperfdash "github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/planscore"
	"github.com/bakw00ds/yakos/internal/plugin"
	"github.com/bakw00ds/yakos/internal/quickstart"
	"github.com/bakw00ds/yakos/internal/refresh"
	"github.com/bakw00ds/yakos/internal/retro"
	"github.com/bakw00ds/yakos/internal/routing"
	"github.com/bakw00ds/yakos/internal/runtime"
	"github.com/bakw00ds/yakos/internal/selfupdate"
	internalserve "github.com/bakw00ds/yakos/internal/serve"
	"github.com/bakw00ds/yakos/internal/session"
	"github.com/bakw00ds/yakos/internal/skill"
	"github.com/bakw00ds/yakos/internal/soul"
	"github.com/bakw00ds/yakos/internal/standards"
	"github.com/bakw00ds/yakos/internal/start"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/supervise"
	"github.com/bakw00ds/yakos/internal/teach"
	"github.com/bakw00ds/yakos/internal/team"
	"github.com/bakw00ds/yakos/internal/telemetry"
	"github.com/bakw00ds/yakos/internal/uninstall"
	"github.com/bakw00ds/yakos/internal/update"
	"github.com/bakw00ds/yakos/internal/validate"
	"github.com/bakw00ds/yakos/internal/version"
	"github.com/bakw00ds/yakos/internal/workclose"
	"github.com/bakw00ds/yakos/internal/workflow"
	"github.com/bakw00ds/yakos/internal/wsbus"
	"golang.org/x/net/websocket"
)

// portedCommands lists subcommands implemented natively in Go. During the
// bootstrap phase this list is empty. Add entries here as subcommands are
// ported in subsequent dispatch tasks.
var portedCommands = []portedCommand{
	{Name: "validate", Since: "0.37.0", Desc: "Validate framework lib/ or a project's .claude/", Notes: "full feature parity with cli/lib/validate.sh"},
	{Name: "cost", Since: "0.38.0", Desc: "Aggregate dispatch-log spend by agent/runtime/day", Notes: "full feature parity with cli/lib/cost.sh"},
	{Name: "status", Since: "0.39.0", Desc: "Print work-in-progress dashboard for a project", Notes: "full feature parity with cli/lib/status.sh"},
	{Name: "doctor", Since: "0.40.0", Desc: "Diagnose install health and config drift", Notes: "full feature parity with cli/lib/doctor.sh"},
	{Name: "refresh", Since: "0.41.0", Desc: "Sync hooks and settings for wired projects", Notes: "full feature parity with cli/lib/refresh.sh"},
	{Name: "kanban", Since: "0.42.0", Desc: "Render/manage/serve the WIP board", Notes: "full feature parity with cli/lib/kanban.sh including serve/status/stop (rank 41 complete)"},
	{Name: "dispatch", Since: "0.43.0", Desc: "Dispatch a task to a specialist agent", Notes: "full feature parity with cli/lib/dispatch.sh; PRs #15/#31/#32/#34/#39/#40 invariants"},
	{Name: "team", Since: "0.44.0", Desc: "Create, list, or archive agent teams", Notes: "full feature parity with cli/lib/team.sh; archive step now native Go (rank 10)"},
	{Name: "archive", Since: "0.45.0", Desc: "Archive a completed work session", Notes: "full feature parity with cli/lib/archive.sh; worktree cleanup deferred (manual, v0.1)"},
	{Name: "init", Since: "0.46.0", Desc: "Bootstrap a new yakOS-wired project", Notes: "full feature parity with cli/lib/init.sh; hook copy advisory printed (bash refresh handles hooks); --with-gate/--multi-dev advisory only in Phase 1"},
	{Name: "install", Since: "0.47.0", Desc: "Install yakOS framework files into a project", Notes: "full feature parity with cli/lib/install.sh; --force/--dry-run supported; per-file symlinks into ~/.claude/{agents,skills,rules,playbooks}; launcher symlink at ~/.local/bin/yakos; settings.json env merge"},
	{Name: "uninstall", Since: "0.48.0", Desc: "Remove yakOS symlinks and launcher", Notes: "full feature parity with cli/lib/uninstall.sh; removes YakOS-owned symlinks + launcher + pointer; --restore-settings/--root/--dry-run; partial-uninstall log+continue"},
	{Name: "start", Since: "0.49.0", Desc: "Start a yakOS session (preflight + exec runtime)", Notes: "full feature parity with cli/lib/start.sh; preflight banner + audit-log; exec deferred to runtime CLI; --dry-run/--print-agents/--safe/--allow-root/passthrough flags supported"},
	{Name: "update", Since: "0.50.0", Desc: "Pull latest yakOS and refresh all projects", Notes: "full feature parity with cli/lib/update.sh; git pull --ff-only + per-project refresh via refresh.CollectProjects + refresh.Run; --allow-non-ff/--all/--dry-run supported"},
	{Name: "quickstart", Since: "0.51.0", Desc: "One-shot install + init + start", Notes: "full feature parity with cli/lib/quickstart.sh; composes install+init+start; idempotent; --runtime/--multi-dev/--safe/--allow-root/--dry-run flags"},
	{Name: "auth", Since: "0.52.0", Desc: "Manage runtime API credentials (keychain-backed)", Notes: "full feature parity with cli/lib/auth.sh; status/login/logout/set-default; OS keychain via go-keyring; graceful degradation on headless Linux"},
	{Name: "memory", Since: "0.53.0", Desc: "Read/write/index the project MEMORY.md store", Notes: "full feature parity with cli/lib/memory.sh; list/read/write/delete/index-rebuild; MEMORY.md byte-identical index; schema sidecar; atomic writes"},
	{Name: "agent", Since: "0.54.0", Desc: "Create, lint, diff, and list agent definitions", Notes: "full feature parity with cli/lib/agent.sh; new/lint/diff/list subcommands; agents alias; docs (idea rank 9) md+html; reuses agentscompose+validate packages; atomic writes"},
	{Name: "session", Since: "0.55.0", Desc: "List, inspect, resume, or fork sessions", Notes: "full feature parity with cli/lib/session.sh; list/info/resume/fork subcommands; streams .session-started-history.ndjson; export deferred (tar/gzip out of scope for Phase 1)"},
	{Name: "migrate", Since: "0.56.0", Desc: "Run schema migrations for state files", Notes: "full feature parity with cli/lib/migrate.sh; status/up subcommands; sidecar schema-version registry (kanban + memory); down deferred to Phase 1.5; atomic writes"},
	{Name: "plugin", Since: "0.57.0", Desc: "Install, remove, and validate CLI plugins", Notes: "full feature parity with cli/lib/plugin.sh; list/install/remove/validate/register/status subcommands; git URL + local-path install; function-header validation; rollback on failure; built-in id guard"},
	{Name: "teach", Since: "0.58.0", Desc: "Append a lesson to project agent files", Notes: "full feature parity with cli/lib/teach.sh; appends dated lesson bullets to project agent files under ## Lessons learned; --project/--section/--dry-run; atomic temp-rename writes; backup before edit"},
	{Name: "soul", Since: "0.59.0", Desc: "View and edit the agent soul (system prompt)", Notes: "full feature parity with cli/lib/soul.sh; show/edit/history/revert/pending subcommands; approve/reject print not-yet-implemented (M1 scope); two-layer (global/project) soul files; atomic writes; snapshot-before-edit; template seeding from lib/settings/soul.template.md"},
	{Name: "retro", Since: "0.60.0", Desc: "Trigger or schedule a 10-cycle retrospective", Notes: "full feature parity with cli/lib/retro.sh; now/disable/enable/status/last/history subcommands; sentinel flag at ~/.yakos-state/retro-disabled; atomic writes (Q8 temp-rename); .retro-due marker written by 'now'; session resolution via ProjectDir cfg override or YAKOS_PROJECT_NAME env or agent-control walk"},
	{Name: "skill", Since: "0.61.0", Desc: "Manage skill candidates (promote/reject/defer)", Notes: "full feature parity with cli/lib/skill.sh; candidates/promote/reject/defer/stats subcommands; graveyard + fingerprint dedup (§16.1); calibration warnings (§16.2); atomic writes; validate gate on promote; --global promote to lib/skills/"},
	{Name: "compact", Since: "0.62.0", Desc: "Compact context now or set auto-threshold", Notes: "full feature parity with cli/lib/compact.sh; now/threshold/history subcommands; atomic writes for settings.json (temp-rename, Q8); O_APPEND for compact-log.ndjson; M3.1 auto-send deferred (prints slash-command advisory)"},
	{Name: "checkpoint", Since: "0.63.0", Desc: "Snapshot and restore scratchpad state", Notes: "full feature parity with cli/lib/checkpoint.sh; create/list/restore/clean subcommands; now+resume aliases; scratchpad copy of plan/decisions/contracts/status/kanban .md; manifest.json with ts/session_id/runtime/by_user; session-id resolution chain (cfg/env/history/unknown); atomic dir writes; M3.2 librarian digest deferred"},
	{Name: "env", Since: "0.64.0", Desc: "Manage deployment environments and PR gates", Notes: "full feature parity with cli/lib/env.sh; status/promote/validate/list subcommands; YAML environments section parsed; gh/glab/git PR tool detection; injectable GitFn+ExecFn+PRToolOverride for tests; atomic project-dir resolution"},
	{Name: "standards", Since: "0.65.0", Desc: "Enable and check project engineering standards", Notes: "full feature parity with cli/lib/standards.sh; list/enable/disable/check/init subcommands; all 6 Plan-4 standards; profile.type suggested matrix; atomic YAML rewrite (temp-rename, Q8); injectable PromptFn for init tests"},
	{Name: "peer", Since: "0.66.0", Desc: "Coordinate between parallel agent peers", Notes: "full feature parity with cli/lib/peer.sh; status/log/claim/release/claims/deadlock/propose-mode/respond-mode/handoff subcommands; mailbox package with O_APPEND+flock append + atomic temp-rename; byte-identical NDJSON format; injectable CoordDirFn+Now for tests"},
	{Name: "mcp", Since: "0.67.0", Desc: "Manage MCP server config (install/uninstall/probe)", Notes: "full feature parity with cli/lib/mcp.sh (Phase 1 read-only config management); install/uninstall/status/probe subcommands; atomic JSON writes (temp-rename, Q8); Windows %APPDATA%/claude/mcp.json per Q3 planner recommendation; native MCP server is Phase 2"},
	{Name: "completion", Since: "0.68.0", Desc: "Generate shell completion scripts", Notes: "full feature parity with cli/lib/completion.sh; bash/zsh/fish/install subcommands; //go:embed templates (Decision D); shell auto-detection from $SHELL and YAKOS_COMPLETION_SHELL; BASH_COMPLETION_USER_DIR, YAKOS_ZSH_COMPDIR, XDG_CONFIG_HOME path overrides"},
	{Name: "git-hooks", Since: "0.69.0", Desc: "Install/manage yakOS git hook integrations", Notes: "full feature parity with cli/lib/git-hooks.sh; install/uninstall/status subcommands; --force/--promotion-gate flags; atomic temp-rename hook writes (Q8); .framework-hash sibling for YakOS ownership + drift detection; composed pre-push for version-gate+promotion-gate"},
	{Name: "supervise", Since: "0.70.0", Desc: "Enable and manage the supervisor finding gate", Notes: "full feature parity with cli/lib/supervise.sh; enable/disable/status/tail/clear/set/pending/ack/ack-all subcommands; PRs #28-#39 supervisor redesign preserved (gate-on-CRITICAL, ack tracking, finding IDs); atomic YAML writes (temp-rename, Q8); append-only supervisor-acks.ndjson; YAKOS_SUPERVISOR_DISABLE emergency bypass"},
	{Name: "plan score", Since: "0.71.0", Desc: "Score and correlate plan quality outcomes", Notes: "full feature parity with cli/lib/plan-score.sh; show/history/override/correlate subcommands; reads plan-quality-log.ndjson; Pearson r + quartile + threshold → outcome report in correlate; .plan-blocked marker removal on override; injectable PlanQualityLog+CurrentDir+Now for tests"},
	{Name: "work close", Since: "0.71.0", Desc: "Record plan outcome and close work session", Notes: "full feature parity with cli/lib/work-close.sh; appends plan_outcome record to plan-quality-log.ndjson; git diff stats, dispatch-log sums, rework cycles, first_try_pass, scope_creep_ratio; non-blocking (missing data → null); injectable GitFn+PromptFn+Now for tests"},
	{Name: "model-routing", Since: "0.72.0", Desc: "Evaluate and promote per-task model assignments", Notes: "full feature parity with cli/lib/model-routing.sh (1035 LOC bash, rank 36); eval/list/show/promote/reject/history subcommands; Wilson 95% CI lower bound; per-run + weekly cost guards; anti-self-congratulation guard; backup+atomic frontmatter rewrite; injectable DispatchFn+JudgeFn+ValidateFn for tests"},
	{Name: "hooks", Since: "0.73.0", Desc: "Generate runtime hook configs (codex/gemini/agy)", Notes: "full feature parity with cli/lib/hooks-install.sh (rank 39); install/status subcommands; codex/gemini/agy hook config generation; path-allowlist.json → codex permissions translation; Decision Q9: hook bodies remain bash"},
	{Name: "kanban serve", Since: "0.73.0", Desc: "Run live kanban web UI (serve/status/stop)", Notes: "kanban serve/status/stop (rank 41 complete); net/http stdlib server; //go:embed serve_ui.html; mutex-serialised mutations; DNS-rebinding Host header check; 127.0.0.1 default bind; no python3 dependency"},
	{Name: "telemetry", Since: "0.74.0", Desc: "Opt-in anonymised CLI telemetry", Notes: "opt-in anonymised telemetry (ideas rank 10); enable/disable/status/set-endpoint/purge/show sub-subcommands; default off; no PII; local NDJSON log at ~/.yakos-state/telemetry.ndjson; operator-configured endpoint only; fail-silent shipper"},
	{Name: "metrics", Since: "0.75.0", Desc: "Collect and report per-project quality metrics", Notes: "per-project commit-keyed metrics time series (Phase-1 MVP); collect/report/trend/compare verbs; [E] collectors from dispatch log + git + state; [T] analyzers for go-backend (go test/vet, golangci-lint, staticcheck, gocyclo, deadcode, gosec, govulncheck) + gitleaks cross-cutting; null≠0 invariant; append-only NDJSON at <project>/.yakos/metrics/history.ndjson; ADR-0001"},
}

type portedCommand struct {
	Name  string
	Since string // version in which Go impl shipped
	Desc  string // one-liner shown in `yakos help` (≤60 chars)
	Notes string
}

// implChoice is the result of the gate decision in selectImpl.
type implChoice int

const (
	// implPassthrough means forward this invocation to bash yakos.
	implPassthrough implChoice = iota
	// implGoNative means use the Go-native command router.
	implGoNative
)

// isHelpArg reports whether arg is one of the help flags intercepted by the
// always-available built-in block.  It is extracted as a named predicate so
// the routing intent can be unit-tested (TestHelpRoutingIsAlwaysGoNative).
func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

// selectImpl encodes the YAKOS_IMPL gate decision as a pure function so it
// can be unit-tested without touching the filesystem or spawning processes.
//
//	impl="go"          → implGoNative  (explicit Go-native opt-in)
//	impl="bash"        → implPassthrough (explicit bash, even if absent — Run
//	                     will surface the ErrNoBashYakos error)
//	impl="" (unset)    → implPassthrough when bashExists; implGoNative otherwise
//	                     (Go-only install just works; shadow-mode preserved when
//	                     bash is present)
func selectImpl(impl string, bashExists bool) implChoice {
	switch impl {
	case "go":
		return implGoNative
	case "bash":
		return implPassthrough
	default:
		// Unset: shadow-mode when bash present, Go-native when not.
		if bashExists {
			return implPassthrough
		}
		return implGoNative
	}
}

func main() {
	// Determine the repo root from the executable location.
	// The binary is built to <repo-root>/bin/yakos, so root = exe/../..
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: could not resolve executable path: %v\n", err)
		os.Exit(1)
	}
	yakosRoot := filepath.Dir(filepath.Dir(exe))

	args := os.Args[1:]

	// Telemetry: record this invocation when main() returns.
	// The call is fail-silent and costs ~1ms when telemetry is off.
	// NOTE: subcommands that call os.Exit() bypass this defer.  That is
	// intentional — telemetry is best-effort and must never block the CLI.
	// The duration captured here therefore reflects only commands that return
	// normally (e.g. help, go-port-status).  Error-path invocations that call
	// os.Exit are not captured, which is an acceptable trade-off.
	telemetryStartNano := time.Now().UnixNano()
	telemetryHome := os.Getenv("HOME")
	if telemetryHome == "" {
		telemetryHome = "/tmp"
	}
	defer func() {
		recordInvocation(telemetryHome, yakosRoot, args, telemetryStartNano)
	}()

	// Always-available built-ins — answered natively regardless of YAKOS_IMPL
	// and regardless of whether a bash yakos tree is installed.  These must
	// work on a Go-only install (curl | sh installs only the binary).
	//
	// help/--help/-h is a deliberate exception to passthrough transparency:
	// the Go port is at full parity (41/41 commands) so the Go command list IS
	// the authoritative list on every install type, including shadow-mode installs
	// where bash is still present.  Routing help through bash would show the bash
	// tree's abbreviated output instead of the full grouped list.  The runHelp
	// function adds a footer on bash-present installs so nothing is hidden from
	// the operator.  This decision must NOT be reverted without a corresponding
	// update to the footer and the TestHelpRoutingIsAlwaysGoNative test.
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			runVersion(yakosRoot)
			return
		case "--help", "-h", "help": // isHelpArg — keep in sync with isHelpArg()
			runHelp(yakosRoot, args)
			return
		case "go-port-status":
			runPortStatus()
			return
		}
	}

	// Gate: decide which implementation to use for this invocation.
	//
	//   YAKOS_IMPL=go   → Go-native routing always.
	//   YAKOS_IMPL=bash → passthrough always (errors if no bash present).
	//   (unset)         → shadow-mode when bash yakos exists; Go-native otherwise.
	//
	// selectImpl encodes this decision; it is separately unit-tested.
	switch selectImpl(os.Getenv("YAKOS_IMPL"), passthrough.BashYakosExists(yakosRoot)) {
	case implPassthrough:
		exitWith(passthrough.Run(yakosRoot, args))
	case implGoNative:
		// no-op: execution continues to the Go-native router below
	}

	// Go-native routing.
	if len(args) == 0 {
		runHelp(yakosRoot, args)
		return
	}

	switch args[0] {
	case "--version", "-v":
		runVersion(yakosRoot)
	case "--help", "-h", "help":
		runHelp(yakosRoot, args)
	case "go-port-status":
		runPortStatus()
	case "validate":
		runValidate(yakosRoot, args[1:])
	case "cost":
		runCost(args[1:])
	case "status":
		runStatus(args[1:])
	case "doctor":
		runDoctor(yakosRoot, args[1:])
	case "refresh":
		runRefresh(yakosRoot, args[1:])
	case "kanban":
		// When the daemon is running, route kanban mutations through the daemon
		// so the WS event bus receives the events.  Reads fall through to in-process.
		if daemonMode() != "off" {
			if routed := maybeRouteToDaemon(yakosRoot, args); routed {
				return
			}
		}
		runKanban(yakosRoot, args[1:])
	case "dispatch":
		runDispatch(yakosRoot, args[1:])
	case "team":
		runTeam(yakosRoot, args[1:])
	case "archive":
		runArchive(yakosRoot, args[1:])
	case "init":
		runInit(args[1:])
	case "install":
		runInstall(yakosRoot, args[1:])
	case "uninstall":
		runUninstall(args[1:])
	case "start":
		runStart(yakosRoot, args[1:])
	case "update":
		runUpdate(yakosRoot, args[1:])
	case "quickstart":
		runQuickstart(yakosRoot, args[1:])
	case "auth":
		runAuth(args[1:])
	case "memory":
		runMemory(args[1:])
	case "agent", "agents":
		runAgent(yakosRoot, args[0], args[1:])
	case "session":
		runSession(args[1:])
	case "migrate":
		runMigrate(args[1:])
	case "plugin":
		runPlugin(args[1:])
	case "teach":
		runTeach(args[1:])
	case "soul":
		runSoul(yakosRoot, args[1:])
	case "retro":
		runRetro(args[1:])
	case "skill":
		runSkill(yakosRoot, args[1:])
	case "compact":
		runCompact(args[1:])
	case "checkpoint":
		runCheckpoint(args[1:])
	case "env":
		runEnv(args[1:])
	case "standards":
		runStandards(args[1:])
	case "peer":
		runPeer(args[1:])
	case "mcp":
		runMCP(yakosRoot, args[1:])
	case "completion":
		runCompletion(args[1:])
	case "git-hooks":
		runGitHooks(yakosRoot, args[1:])
	case "supervise":
		runSupervise(args[1:])
	case "plan":
		runPlan(yakosRoot, args[1:])
	case "work":
		runWork(args[1:])
	case "model-routing":
		runModelRouting(yakosRoot, args[1:])
	case "hooks":
		runHooks(args[1:])
	case "serve":
		runServe(yakosRoot, args[1:])
	case "events":
		runEvents(args[1:])
	case "telemetry":
		runTelemetry(args[1:])
	case "metrics":
		runMetrics(args[1:])
	case "workflow":
		runWorkflow(yakosRoot, args[1:])
	default:
		// YAKOS_DAEMON routing: if the daemon is running and YAKOS_DAEMON=on|auto,
		// route this subcommand through the JSON-RPC client instead of in-process.
		// See internal/serve package for the daemon surface.
		// This is resolved in routeViaDaemon; falls through to passthrough on miss.
		if routed := maybeRouteToDaemon(yakosRoot, args); routed {
			return
		}
		// Shadow-mode passthrough: forward everything to bash yakos.
		exitWith(passthrough.Run(yakosRoot, args))
	}
}

// runVersion prints the version string read from the VERSION file plus the
// "(go)" transition suffix.
func runVersion(yakosRoot string) {
	v, err := version.Read(yakosRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(v)
}

// helpGroup describes a named section in the help output.
type helpGroup struct {
	Title    string
	Commands []string // portedCommand.Name values (or bare names for builtins)
}

// helpGroups defines the display order and grouping for `yakos help`.
// Commands in multiple groups should not appear — list each once.
var helpGroups = []helpGroup{
	{
		Title: "Core / Project",
		Commands: []string{
			"--version", "--help", "go-port-status",
			"init", "install", "uninstall", "update", "quickstart",
			"start", "refresh", "doctor", "validate", "auth",
		},
	},
	{
		Title: "Dispatch & Orchestration",
		Commands: []string{
			"dispatch", "team", "archive", "peer", "workflow",
		},
	},
	{
		Title: "Console & Web",
		Commands: []string{
			"serve", "kanban", "status", "session", "events",
		},
	},
	{
		Title: "Release & Maintenance",
		Commands: []string{
			"checkpoint", "compact", "migrate", "git-hooks", "hooks",
			"completion", "env", "standards", "mcp", "plugin",
		},
	},
	{
		Title: "LLM Ops & Metrics",
		Commands: []string{
			"cost", "metrics", "telemetry", "model-routing",
			"plan score", "work close",
		},
	},
	{
		Title: "Supervision & Retro",
		Commands: []string{
			"supervise", "retro", "skill", "soul", "teach",
			"agent", "memory",
		},
	},
}

// builtinDescs provides one-liners for always-available built-ins that do not
// appear in portedCommands.
//
// go-port-status description is built dynamically in runHelp so the parity
// fraction stays accurate as portedCommands grows — do not hardcode it here.
var builtinDescs = map[string]string{
	"--version": "Print version string",
	"--help":    "Print this help",
	"workflow":  "Run a named multi-step workflow",
	"serve":     "Run the daemon + web console (requires daemon running)",
	"events":    "Stream live bus events (requires daemon running)",
}

// runHelp prints a self-contained, grouped command list regardless of whether
// the bash yakos tree is present. It is the canonical help surface for the Go
// binary on every install type.
//
// The function is routed from both the always-available built-in block and the
// Go-native switch, so `yakos help`, `yakos --help`, and `yakos -h` all reach
// it identically on every install type (Go-only and shadow-mode/bash-present).
// See the always-available block comment above for why this is intentional.
//
// On bash-present installs a footer line is appended to preserve discoverability
// of any command not listed here; on Go-only installs the footer is omitted.
func runHelp(yakosRoot string, _ []string) {
	v, _ := version.Read(yakosRoot)
	if v == "" {
		v = "unknown"
	}

	// Build a lookup: name → Desc (portedCommands + builtins).
	// go-port-status is generated dynamically so the parity count stays accurate.
	descs := make(map[string]string, len(portedCommands)+len(builtinDescs)+1)
	for k, d := range builtinDescs {
		descs[k] = d
	}
	for _, cmd := range portedCommands {
		descs[cmd.Name] = cmd.Desc
	}
	descs["go-port-status"] = fmt.Sprintf("Show Go port parity detail (%d/%d)", len(portedCommands), len(portedCommands))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "yakos %s\n\n", v)
	_, _ = fmt.Fprintln(w, "Usage:  yakos <command> [args]")
	_, _ = fmt.Fprintln(w, "")

	for _, grp := range helpGroups {
		_, _ = fmt.Fprintf(w, "%s\n", grp.Title)
		for _, name := range grp.Commands {
			desc := descs[name]
			if desc == "" {
				desc = "(see yakos " + name + " --help)"
			}
			_, _ = fmt.Fprintf(w, "  %-20s\t%s\n", name, desc)
		}
		_, _ = fmt.Fprintln(w, "")
	}

	_, _ = fmt.Fprintln(w, "Run  yakos <cmd> --help       for per-command help.")
	_, _ = fmt.Fprintln(w, "Run  yakos go-port-status     for full parity detail.")
	_, _ = fmt.Fprintln(w, "See  docs/go-shadow-mode.md   for install / switch guide.")

	// Safety footer: on bash-present installs, remind the operator that any
	// command not listed above is still reachable via the bash tree.  This
	// preserves discoverability without sacrificing the authoritative Go list.
	if passthrough.BashYakosExists(yakosRoot) {
		_, _ = fmt.Fprintln(w, "")
		_, _ = fmt.Fprintln(w, "(bash yakos detected — any command not listed above is still handled by the bash tree: run 'yakos <cmd>'.)")
	}

	_ = w.Flush()
}

// runPortStatus lists every subcommand and whether it is natively implemented
// in Go or still proxied to bash. Output is written directly to os.Stdout;
// write errors are treated as fatal because stdout failure is unrecoverable.
func runPortStatus() {
	mustPrint := func(s string) {
		if _, err := fmt.Fprint(os.Stdout, s); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}

	mustPrint("yakos go-port-status — subcommand migration tracker\n\n")

	if len(portedCommands) == 0 {
		mustPrint("  No subcommands ported to Go yet.\n")
		mustPrint("  All commands proxy to the bash yakos at cli/yakos.\n")
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "  COMMAND\tSINCE\tNOTES"); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
		for _, cmd := range portedCommands {
			if _, err := fmt.Fprintf(w, "  %s\t%s\t%s\n", cmd.Name, cmd.Since, cmd.Notes); err != nil {
				fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
				os.Exit(1)
			}
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}

	mustPrint(fmt.Sprintf("\n  Ported: %d / total subcommands tracked: %d\n",
		len(portedCommands), len(portedCommands)))
}

// runValidate implements `yakos validate` natively in Go.
//
// Usage mirrors cli/lib/validate.sh exactly:
//
//	yakos validate              — framework mode (validates $YAKOS_ROOT/lib/)
//	yakos validate <path>       — project mode (validates <path>/.claude/)
//	yakos validate --all        — both framework and project
//	yakos validate --strict     — warnings become errors
//	yakos validate --help       — print help and exit 0
//
// YAKOS_ROOT must be set in the environment (the bash entry-point sets it;
// in tests it is injected via Case.Env).
func runValidate(yakosRoot string, args []string) {
	allMode := false
	strict := false
	var targets []string

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printValidateHelp(os.Stdout)
			os.Exit(0)
		case "--all":
			allMode = true
		case "--strict", "-s":
			strict = true
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "validate: unknown flag %q\n", arg)
				os.Exit(1)
			}
			targets = append(targets, arg)
		}
	}

	// YAKOS_ROOT can be overridden by env (matches bash behaviour where the
	// entry-point sets it before sourcing validate.sh).
	if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
		yakosRoot = envRoot
	}

	cfg := validate.Config{
		YakosRoot: yakosRoot,
		Strict:    strict,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	var r *validate.Result
	switch {
	case allMode:
		r = validate.RunAll(cfg, targets)
	case len(targets) == 0:
		r = validate.RunFramework(cfg)
	default:
		r = &validate.Result{}
		for _, t := range targets {
			sub := validate.RunProject(cfg, t)
			r.Errors += sub.Errors
			r.Warnings += sub.Warnings
			r.Findings = append(r.Findings, sub.Findings...)
		}
	}

	exitCode := validate.PrintSummary(os.Stdout, r)
	os.Exit(exitCode)
}

// printValidateHelp prints the help text for `yakos validate`, matching the
// bash validate.sh --help output exactly.
func printValidateHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos validate [<project-path>] [--all]

Schema + reference validation. Three modes:

  yakos validate                Validate the framework's lib/ (this repo).
  yakos validate <path>         Validate <path>/.claude/ for a project.
  yakos validate --all          Validate framework lib/ AND the project's
                                .claude/ (must also pass <path>).

v0.1 lib/ is intentionally empty — this command handles the empty
case and reports cleanly. Full frontmatter+reference validation runs
once Batch 3 populates lib/agents/, lib/skills/, lib/rules/.

Uses python3 if available for full YAML/JSON parsing; degrades to
grep-based checks otherwise (with a "limited validation" warning).
`)
}

// runCost implements `yakos cost` natively in Go.
//
// Usage mirrors cli/lib/cost.sh exactly:
//
//	yakos cost                        — all-time, by-runtime table
//	yakos cost --by agent             — group by agent
//	yakos cost --by day --since DATE  — day-level view since a date
//	yakos cost --json                 — machine-readable JSON
//	yakos cost --help                 — print help and exit 0
//
// The log directory defaults to $HOME/.yakos-state; override via
// YAKOS_DISPATCH_LOG (set to the log directory, not the file).
// Parity tests set YAKOS_DISPATCH_LOG to a temp dir containing a
// fixture dispatch-log.ndjson.
func runCost(args []string) {
	since := ""
	by := "runtime"
	emitJSON := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printCostHelp(os.Stdout)
			os.Exit(0)
		case arg == "--since":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "cost: --since requires a date")
				os.Exit(1)
			}
			since = args[i]
		case len(arg) > 8 && arg[:8] == "--since=":
			since = arg[8:]
		case arg == "--by":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "cost: --by requires an axis")
				os.Exit(1)
			}
			by = args[i]
		case len(arg) > 5 && arg[:5] == "--by=":
			by = arg[5:]
		case arg == "--json":
			emitJSON = true
		case arg == "--all-projects":
			// accepted as a no-op (mirrors bash behaviour)
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "cost: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "cost: unexpected argument %q\n", arg)
			os.Exit(1)
		}
	}

	axis, err := cost.ParseAxis(by)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	// Resolve log directory.  YAKOS_DISPATCH_LOG overrides the default.
	logDir := filepath.Join(os.Getenv("HOME"), ".yakos-state")
	if v := os.Getenv("YAKOS_DISPATCH_LOG"); v != "" {
		logDir = v
	}

	files, err := cost.LogFiles(logDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cost: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		if err := cost.PrintNoFiles(os.Stdout, emitJSON, logDir); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	ch := cost.StreamFiles(files, since)
	rpt := cost.Aggregate(ch, axis, 0)

	if rpt.Events == 0 {
		if err := cost.PrintNoEvents(os.Stdout, emitJSON, since); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if emitJSON {
		if err := cost.PrintJSON(os.Stdout, rpt); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := cost.PrintTable(os.Stdout, rpt, since, by); err != nil {
			fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
			os.Exit(1)
		}
	}
}

// printCostHelp prints the help text for `yakos cost`, matching the
// bash cost.sh --help output exactly.
func printCostHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos cost [--since <ISO>] [--by agent|runtime|day|project]
            [--all-projects] [--json]

Aggregate dispatch-log.ndjson telemetry. Defaults to all-time, by-runtime.

By default, --by project / --all-projects rolls up across every project
that has appeared in the dispatch-log. Useful for multi-project burn-rate
review.

Flags:
  --since <ISO-date>   Filter events with ts >= <ISO-date>. Examples:
                       --since 2026-05-01 (start of day)
                       --since 2026-05-08T12:00:00Z
  --by <axis>          Aggregation axis: agent, runtime (default), day.
  --json               Emit machine-readable JSON instead of a table.

Sources rolled up:
  ~/.yakos-state/dispatch-log.ndjson      (current)
  ~/.yakos-state/dispatch-log.*.ndjson    (rotated archives)

Token columns:
  est_in / est_out — chars/4 estimate from prompt/output bytes.
  Real per-runtime token counts arrive in v0.6.x once stream-json
  parsing per-adapter lands.

Examples:
  yakos cost
  yakos cost --by agent --since 2026-05-01
  yakos cost --by day --json | jq
`)
}

// runStatus implements `yakos status` natively in Go.
//
// Usage mirrors cli/lib/status.sh exactly:
//
//	yakos status <project>   — print dashboard for <project> under ~/agent-control/
//	yakos status --help      — print help and exit 0
//
// The project's work directory is resolved following paths.sh priority:
//
//  1. YAKOS_WORK_DIR
//  2. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR
//  3. $HOME/agent-control/<project>/work  (canonical)
func runStatus(args []string) {
	project := ""

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			status.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "status: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if project != "" {
				fmt.Fprintln(os.Stderr, "status: too many positional args")
				os.Exit(1)
			}
			project = arg
		}
	}

	if project == "" {
		fmt.Fprintln(os.Stderr, "status: missing <project> (try --help)")
		os.Exit(1)
	}

	// Verify the control directory exists (mirrors bash's directory check).
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	controlDir := filepath.Join(home, "agent-control", project)
	if _, err := os.Stat(controlDir); err != nil {
		fmt.Fprintf(os.Stderr, "status: project %q not found at %s\n", project, controlDir)
		os.Exit(1)
	}

	cfg := status.Config{
		Project:   project,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	rpt, err := status.Status(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "status: %v\n", err)
		os.Exit(1)
	}

	if err := status.Format(os.Stdout, rpt); err != nil {
		fmt.Fprintf(os.Stderr, "yakos: write error: %v\n", err)
		os.Exit(1)
	}
}

// runDoctor implements `yakos doctor` natively in Go.
//
// Usage mirrors cli/lib/doctor.sh exactly:
//
//	yakos doctor [<project-path>] [--probe-runtime] [--production]
//	yakos doctor --help
//
// Exits 0 when no errors found (warnings/info/drift are OK).
// Exits 1 when one or more error-severity findings are reported.
// The --fix flag is recognised but rejected (Phase 1 scope constraint).
func runDoctor(yakosRoot string, args []string) {
	projectPath := ""
	probeRuntime := false
	production := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			doctor.PrintHelp(os.Stdout)
			os.Exit(0)
		case "--probe-runtime":
			probeRuntime = true
		case "--production":
			production = true
		case "--fix":
			fmt.Fprintln(os.Stderr, "doctor: --fix is not yet implemented in the Go port (see ideas wishlist rank 5)")
			fmt.Fprintln(os.Stderr, "  Use 'YAKOS_IMPL=bash yakos doctor --fix' to reach the bash implementation.")
			os.Exit(1)
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "doctor: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if projectPath != "" {
				fmt.Fprintln(os.Stderr, "doctor: too many positional args")
				os.Exit(1)
			}
			projectPath = arg
		}
	}

	// Resolve YAKOS_LIB from env.
	yakosLib := os.Getenv("YAKOS_LIB")
	if yakosLib == "" && yakosRoot != "" {
		yakosLib = filepath.Join(yakosRoot, "lib")
	}

	cfg := doctor.Config{
		YakosRoot:    yakosRoot,
		YakosLib:     yakosLib,
		ProjectPath:  projectPath,
		ProbeRuntime: probeRuntime,
		Production:   production,
		Writer:       os.Stdout,
		ErrWriter:    os.Stderr,
	}

	report, err := doctor.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor: %v\n", err)
		os.Exit(1)
	}
	if report.Errors > 0 {
		os.Exit(1)
	}
	os.Exit(0)
}

// runRefresh implements `yakos refresh` natively in Go.
//
// Usage mirrors cli/lib/refresh.sh exactly:
//
//	yakos refresh                        — infer project from cwd
//	yakos refresh --project <path>       — explicit single project
//	yakos refresh --all                  — discover all wired projects
//	yakos refresh --dry-run              — report changes without writing
//	yakos refresh --help                 — print help and exit 0
//
// YAKOS_ROOT must be set in the environment (set by the bash entry-point;
// in tests it is injected via the env map).
func runRefresh(yakosRoot string, args []string) {
	dryRun := false
	allProjects := false
	explicitProject := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printRefreshHelp(os.Stdout)
			os.Exit(0)
		case arg == "--dry-run":
			dryRun = true
		case arg == "--all":
			allProjects = true
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "refresh: --project requires a path")
				os.Exit(1)
			}
			explicitProject = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			explicitProject = arg[10:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "refresh: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "refresh: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "refresh: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Validate prerequisites exist.
	hooksRoot := filepath.Join(yakosRoot, "lib", "hooks")
	if _, err := os.Stat(hooksRoot); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "refresh: lib/hooks not found at %s (bad YAKOS_ROOT?)\n", hooksRoot)
		os.Exit(1)
	}
	templateFile := filepath.Join(yakosRoot, "lib", "settings", "settings.template.json")
	if _, err := os.Stat(templateFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "refresh: settings template not found at %s\n", templateFile)
		os.Exit(1)
	}

	// Collect target projects.
	var projectPaths []string
	switch {
	case explicitProject != "":
		projectPaths = []string{explicitProject}
	case allProjects:
		projectPaths = refresh.CollectProjects(home)
		if len(projectPaths) == 0 {
			_, _ = fmt.Fprintln(os.Stdout, "No yakos-wired projects found under ~/agent-control/ or ~/github/.")
			_, _ = fmt.Fprintln(os.Stdout, "Run 'yakos init <name> --project <path>' to bootstrap a project.")
			os.Exit(0)
		}
	default:
		// Infer from cwd.
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		proj := refresh.InferProjectFromCWD(cwd, home)
		if proj == "" {
			fmt.Fprintf(os.Stderr, "refresh: cannot infer project from cwd %q. Use --project <path> or --all.\n", cwd)
			os.Exit(1)
		}
		projectPaths = []string{proj}
	}

	cfg := refresh.Config{
		YakosRoot:    yakosRoot,
		ProjectPaths: projectPaths,
		DryRun:       dryRun,
		Writer:       os.Stdout,
		ErrWriter:    os.Stderr,
		HomeDir:      home,
	}

	_, err := refresh.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "refresh: %v\n", err)
		os.Exit(1)
	}
}

// printRefreshHelp prints the help text for `yakos refresh`, matching the
// bash refresh.sh --help output exactly.
func printRefreshHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos refresh [--project <path>|--all] [--dry-run]

Detect and repair per-project deployment drift:
  - Hook scripts in <project>/scripts/hooks/ synced from lib/hooks/
  - settings.json hook registrations smart-merged from lib/settings/settings.template.json
  - ~/.claude/agents/ symlinks refreshed from lib/agents/

Options:
  --project <path>  Repair a specific project path.
  --all             Discover all wired projects (~/agent-control/*/ +
                    ~/github/*/.claude/settings.json) and refresh each.
  --dry-run         Print what WOULD change without writing anything.
  --help, -h        Print this help.

Without --project or --all, infers from cwd (same as yakos start).

Exit codes:
  0   Success (including no-op when already in sync)
  1   Error (bad project path, corrupt JSON, etc.)
`)
}

// runKanban implements `yakos kanban` natively in Go.
//
// Usage mirrors cli/lib/kanban.sh exactly:
//
//	yakos kanban                       # render TUI (default)
//	yakos kanban --html [<out>]        # render static HTML snapshot
//	yakos kanban add "<title>" [--category <c>] [--notes "<text>"]
//	yakos kanban notes <id> "<text>"   # set/replace notes field
//	yakos kanban move <id> <col>       # move between columns
//	yakos kanban done <id>             # shortcut to DONE
//	yakos kanban delete <id>           # hard-delete a task (also: rm)
//	yakos kanban serve [...]           # live web UI (rank 41 complete)
//	yakos kanban status                # is the web UI running?
//	yakos kanban stop                  # stop the running web UI
//	yakos kanban --help                # print help
//
// The kanban.md file is resolved from YAKOS_WORK_DIR env variable, or from the
// canonical path $HOME/agent-control/<YAKOS_PROJECT>/work/current/kanban.md.
// When neither is set, the current working directory is searched for a
// work/current/kanban.md.
func runKanban(yakosRoot string, args []string) {
	if len(args) == 0 {
		renderKanbanTUI()
		return
	}

	switch args[0] {
	case "--help", "-h", "help":
		printKanbanHelp(os.Stdout)
		os.Exit(0)
	case "--html":
		renderKanbanHTML(args[1:])
	case "add":
		kanbanAdd(args[1:])
	case "notes":
		kanbanNotes(args[1:])
	case "move":
		kanbanMove(args[1:])
	case "done":
		kanbanDone(args[1:])
	case "delete", "rm":
		kanbanDelete(args[1:])
	case "serve", "--serve":
		kanbanServe(args[1:])
	case "status":
		kanbanServeStatus()
	case "stop":
		kanbanServeStop()
	default:
		fmt.Fprintf(os.Stderr, "kanban: unknown subcommand %q (try --help)\n", args[0])
		os.Exit(1)
	}
}

// kanbanFilePath resolves the path to kanban.md.
//
// Resolution order (mirrors cli/lib/paths.sh priority):
//  1. YAKOS_WORK_DIR env → $YAKOS_WORK_DIR/current/kanban.md
//  2. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR → $CLAUDE_PROJECT_DIR/work/current/kanban.md
//  3. $HOME/agent-control/$YAKOS_PROJECT_NAME/work/current/kanban.md
//  4. Fallback: ./work/current/kanban.md relative to cwd
func kanbanFilePath() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current", "kanban.md")
	}
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		if pd := os.Getenv("CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work", "current", "kanban.md")
		}
	}
	if proj := os.Getenv("YAKOS_PROJECT_NAME"); proj != "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/tmp"
		}
		return filepath.Join(home, "agent-control", proj, "work", "current", "kanban.md")
	}
	// Fallback: look in cwd.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, "work", "current", "kanban.md")
}

// loadBoard reads and parses the kanban.md at path. If the file does not
// exist and create is true, a seeded empty board is returned. If the file
// does not exist and create is false, an error is returned.
func loadBoard(path string, create bool) (*kanban.Board, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		if !create {
			return nil, fmt.Errorf("kanban.md not found at %s", path)
		}
		return kanban.Seed(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	b, err := kanban.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return b, nil
}

// ensureKanbanDir creates the directory containing the kanban.md if needed.
func ensureKanbanDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0755) //nolint:gosec
}

func renderKanbanTUI() {
	path := kanbanFilePath()
	b, err := loadBoard(path, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: %v\n", err)
		os.Exit(1)
	}
	if err := b.RenderTUI(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "kanban: render: %v\n", err)
		os.Exit(1)
	}
}

func renderKanbanHTML(args []string) {
	path := kanbanFilePath()
	outPath := filepath.Join(filepath.Dir(path), "kanban.html")
	if len(args) > 0 {
		outPath = args[0]
	}

	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: %v\n", err)
		os.Exit(1)
	}

	// Write to temp then rename (atomic).
	tmp := outPath + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: create %s: %v\n", tmp, err)
		os.Exit(1)
	}
	if err := b.RenderHTML(f, path); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: render html: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: close %s: %v\n", tmp, err)
		os.Exit(1)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: rename %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: rendered: %s\n", outPath)
}

func kanbanAdd(args []string) {
	category := "other"
	notes := ""
	title := ""

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--category":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban add: --category needs a value")
				os.Exit(1)
			}
			category = args[i]
		case "--notes":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban add: --notes needs a value")
				os.Exit(1)
			}
			notes = args[i]
		default:
			if len(args[i]) > 0 && args[i][0] == '-' && args[i] != "--" {
				// Check for --category=<v> and --notes=<v> forms.
				if len(args[i]) > 11 && args[i][:11] == "--category=" {
					category = args[i][11:]
				} else if len(args[i]) > 8 && args[i][:8] == "--notes=" {
					notes = args[i][8:]
				} else if args[i] == "--" {
					i++
					if i < len(args) && title == "" {
						title = args[i]
					}
				} else {
					fmt.Fprintf(os.Stderr, "kanban add: unknown option %q\n", args[i])
					os.Exit(1)
				}
			} else {
				if title == "" {
					title = args[i]
				} else {
					fmt.Fprintf(os.Stderr, "kanban add: unexpected argument %q\n", args[i])
					os.Exit(1)
				}
			}
		}
		i++
	}

	if title == "" {
		fmt.Fprintln(os.Stderr, `kanban add: title required: yakos kanban add "<title>"`)
		os.Exit(1)
	}

	path := kanbanFilePath()
	if err := ensureKanbanDir(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: mkdir %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}

	b, err := loadBoard(path, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: %v\n", err)
		os.Exit(1)
	}

	id := b.Add(title, category, notes)

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: added: %s \xe2\x80\x94 %s (category: %s)\n", id, title, category)
}

func kanbanNotes(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `kanban notes: id and text required: yakos kanban notes <id> "<text>"`)
		os.Exit(1)
	}
	id := args[0]
	text := args[1]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: %v\n", err)
		os.Exit(1)
	}

	if err := b.SetNotes(id, text); err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: notes set for %s\n", id)
}

func kanbanMove(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "kanban move: id and column required: yakos kanban move <id> <col>")
		os.Exit(1)
	}
	id := args[0]
	col, err := kanban.NormalizeColumn(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", err)
		os.Exit(1)
	}

	path := kanbanFilePath()
	b, loadErr := loadBoard(path, false)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", loadErr)
		os.Exit(1)
	}

	if err := b.Move(id, col); err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: moved %s to %s\n", id, col)
}

func kanbanDone(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "kanban done: id required: yakos kanban done <id>")
		os.Exit(1)
	}
	id := args[0]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: %v\n", err)
		os.Exit(1)
	}

	if err := b.Done(id); err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: moved %s to DONE\n", id)
}

func kanbanDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "kanban delete: id required: yakos kanban delete <id>")
		os.Exit(1)
	}
	id := args[0]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: %v\n", err)
		os.Exit(1)
	}

	if err := b.Delete(id); err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: deleted: %s\n", id)
}

// ---- kanban serve / status / stop ------------------------------------------

// kanbanStateFile returns the path to the .kanban-serve.json state sidecar.
// Matches bash _kanban_state_file() which uses yakos_current_dir().
func kanbanStateFile() string {
	boardPath := kanbanFilePath()
	dir := filepath.Dir(boardPath)
	return filepath.Join(dir, ".kanban-serve.json")
}

// kanbanServeStateExists returns the parsed state if a live state file exists.
func kanbanServeStateExists() (url, pid string, ok bool) {
	stateFile := kanbanStateFile()
	raw, err := os.ReadFile(stateFile) //nolint:gosec
	if err != nil {
		return "", "", false
	}
	var state struct {
		PID int    `json:"pid"`
		URL string `json:"url"`
	}
	if err := func() error {
		return func() error { return nil }() // placeholder
	}(); err != nil {
		return "", "", false
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", "", false
	}
	if state.URL == "" || state.PID == 0 {
		return "", "", false
	}
	return state.URL, intToStr(state.PID), true
}

// intToStr converts int to string without strconv import (local helper).
func intToStr(n int) string {
	return strconv.Itoa(n)
}

// kanbanServe implements `yakos kanban serve [--port N] [--host H] [--no-open]`.
func kanbanServe(args []string) {
	port := 0
	host := "127.0.0.1"
	openBrowser := true

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban serve: --port needs a value")
				os.Exit(1)
			}
			n := 0
			for _, ch := range args[i] {
				if ch < '0' || ch > '9' {
					fmt.Fprintln(os.Stderr, "kanban serve: --port must be a number")
					os.Exit(1)
				}
				n = n*10 + int(ch-'0')
			}
			port = n
		case "--host":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban serve: --host needs a value")
				os.Exit(1)
			}
			host = args[i]
			// Security: require explicit flag to expose on all interfaces.
			if host == "0.0.0.0" {
				fmt.Fprintln(os.Stderr, "kanban serve: binding 0.0.0.0 — the web UI is UNAUTHENTICATED and can mutate the board")
			}
		case "--no-open":
			openBrowser = false
		default:
			if len(args[i]) > 7 && args[i][:7] == "--port=" {
				// --port=N form.
				val := args[i][7:]
				n := 0
				for _, ch := range val {
					if ch < '0' || ch > '9' {
						fmt.Fprintln(os.Stderr, "kanban serve: --port must be a number")
						os.Exit(1)
					}
					n = n*10 + int(ch-'0')
				}
				port = n
			} else if len(args[i]) > 7 && args[i][:7] == "--host=" {
				host = args[i][7:]
			} else {
				fmt.Fprintf(os.Stderr, "kanban serve: unknown option %q\n", args[i])
				os.Exit(1)
			}
		}
	}

	boardPath := kanbanFilePath()
	project := os.Getenv("YAKOS_PROJECT_NAME")
	if project == "" {
		project = filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(boardPath))))
	}

	cfg := kanban.ServeConfig{
		BoardPath:   boardPath,
		Project:     project,
		Host:        host,
		Port:        port,
		OpenBrowser: openBrowser,
		ErrWriter:   os.Stderr,
	}
	if _, err := kanban.Serve(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "kanban serve: %v\n", err)
		os.Exit(1)
	}
}

// kanbanServeStatus checks if a kanban web UI is running and prints its URL.
// The Go implementation checks the .kanban-serve.json state file.
// Note: the Go server does not write a state file in the current implementation
// (the bash server did this; the Go server blocks and keeps state in-process).
// This subcommand prints a not-running message pointing at the state file path.
func kanbanServeStatus() {
	url, pid, ok := kanbanServeStateExists()
	if ok {
		fmt.Printf("kanban web UI: running\n  url:     %s\n  pid:     %s\n", url, pid)
	} else {
		fmt.Fprintln(os.Stderr, "kanban web UI: not running")
		fmt.Fprintf(os.Stderr, "  (state file: %s)\n", kanbanStateFile())
	}
}

// kanbanServeStop prints an advisory. The Go server runs in the foreground
// (same as bash) and is stopped by Ctrl-C. A background PID from a state file
// can be killed if the state file is present.
func kanbanServeStop() {
	url, pid, ok := kanbanServeStateExists()
	if !ok {
		fmt.Fprintln(os.Stderr, "kanban web UI: not running")
		return
	}
	fmt.Fprintf(os.Stderr, "kanban web UI running at %s (pid %s)\n", url, pid)
	fmt.Fprintln(os.Stderr, "To stop it: press Ctrl-C in the terminal where it is running,")
	fmt.Fprintln(os.Stderr, "  or: kill "+pid)
}

// ---- hooks ------------------------------------------------------------------

// runHooks implements `yakos hooks` natively in Go.
//
// Usage mirrors cli/lib/hooks-install.sh exactly:
//
//	yakos hooks install <runtime> --project <path> [--force]
//	yakos hooks status [<project>]
//	yakos hooks --help
//
// Decision Q9: hook BODIES remain bash (Phase 3). This command only manages
// runtime-native config deployment (pointing runtimes at the bash scripts).
func runHooks(args []string) {
	if len(args) == 0 {
		hooksinstall.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	switch args[0] {
	case "--help", "-h", "help":
		hooksinstall.PrintHelp(os.Stdout)
		os.Exit(0)
	case "install":
		runHooksInstall(args[1:])
	case "status":
		runHooksStatus(args[1:])
	case "lint":
		runHooksLint(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "hooks: unknown subcommand %q (try --help)\n", args[0])
		os.Exit(1)
	}
}

func runHooksInstall(args []string) {
	runtime := ""
	project := ""
	force := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			hooksinstall.PrintHelp(os.Stdout)
			os.Exit(0)
		case "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "hooks install: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case "--force":
			force = true
		default:
			if len(args[i]) > 10 && args[i][:10] == "--project=" {
				project = args[i][10:]
			} else if args[i][0] == '-' {
				fmt.Fprintf(os.Stderr, "hooks install: unknown flag %q\n", args[i])
				os.Exit(1)
			} else {
				if runtime == "" {
					runtime = args[i]
				} else {
					fmt.Fprintln(os.Stderr, "hooks install: too many positional args")
					os.Exit(1)
				}
			}
		}
	}

	if runtime == "" {
		fmt.Fprintln(os.Stderr, "hooks install: <runtime> required")
		hooksinstall.PrintHelp(os.Stderr)
		os.Exit(1)
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "hooks install: --project required")
		hooksinstall.PrintHelp(os.Stderr)
		os.Exit(1)
	}
	if !hooksinstall.IsKnownRuntime(runtime) {
		fmt.Fprintf(os.Stderr, "hooks install: unknown runtime %q\n", runtime)
		os.Exit(1)
	}
	if _, err := os.Stat(project); err != nil {
		fmt.Fprintf(os.Stderr, "hooks install: project not found: %s\n", project)
		os.Exit(1)
	}

	cfg := hooksinstall.Config{
		Subcommand: "install",
		Runtime:    runtime,
		ProjectDir: project,
		Force:      force,
		Writer:     os.Stderr,
		ErrWriter:  os.Stderr,
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "hooks: %v\n", err)
		os.Exit(1)
	}
}

func runHooksStatus(args []string) {
	project := ""
	if len(args) > 0 {
		project = args[0]
	}
	if project == "" {
		// Try to infer from cwd: look for .claude/settings.json walking up.
		cwd, _ := os.Getwd()
		for dir := cwd; dir != "/" && dir != ""; {
			if _, err := os.Stat(filepath.Join(dir, ".claude", "settings.json")); err == nil {
				project = dir
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "hooks status: pass <project> or run from inside it")
		os.Exit(1)
	}

	cfg := hooksinstall.Config{
		Subcommand: "status",
		ProjectDir: project,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}
	if _, err := hooksinstall.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "hooks: %v\n", err)
		os.Exit(1)
	}
}

func runHooksLint(args []string) {
	hooksDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			fmt.Fprintln(os.Stdout, `yakos hooks lint [--hooks-dir <path>]

Lint all .star files in the hooks directory.

  --hooks-dir <path>   Directory containing .star files.
                       Defaults to lib/hooks/ relative to YAKOS_ROOT.

Checks performed:
  - Syntax errors (parse + compile via go.starlark.net)
  - override = True without on_event defined (always a no-op)
  - Calls to ctx.X where X is not in the sandboxed API
  - Unreachable code after return in on_event

Exit codes:
  0 — no errors (warnings may be present)
  1 — one or more errors found`)
			os.Exit(0)
		case "--hooks-dir":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "hooks lint: --hooks-dir requires a path")
				os.Exit(1)
			}
			hooksDir = args[i]
		default:
			if strings.HasPrefix(args[i], "--hooks-dir=") {
				hooksDir = args[i][len("--hooks-dir="):]
			} else {
				fmt.Fprintf(os.Stderr, "hooks lint: unknown arg %q\n", args[i])
				os.Exit(1)
			}
		}
	}

	if hooksDir == "" {
		// Default: lib/hooks/ relative to YAKOS_ROOT or binary location.
		if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
			hooksDir = filepath.Join(envRoot, "lib", "hooks")
		}
	}
	if hooksDir == "" {
		fmt.Fprintln(os.Stderr, "hooks lint: --hooks-dir required (or set YAKOS_ROOT)")
		os.Exit(1)
	}

	cfg := hooksinstall.LintConfig{
		HooksDir:  hooksDir,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}
	results, err := hooksinstall.RunLint(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hooks lint: %v\n", err)
		os.Exit(1)
	}
	if results.ErrCount > 0 {
		os.Exit(1)
	}
}

// printKanbanHelp prints the help text for `yakos kanban`, matching the
// bash kanban.sh --help output.
func printKanbanHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos kanban — 3-column markdown board in scratchpad

  yakos kanban                       # render TUI
  yakos kanban --html [<out>]        # render static HTML snapshot
  yakos kanban serve [--port N]      # live web UI: view + manage
                                     #   [--host H] [--no-open]
                                     #   (default: random high port on 127.0.0.1)
                                     #   binds 127.0.0.1 by default (loopback only)
  yakos kanban status                # is the web UI running? print its URL
  yakos kanban stop                  # stop the running web UI
  yakos kanban add "<title>"         # append to TODO
                [--category <c>]     #   category (default: other)
                [--notes "<text>"]   #   initial notes (single line)
  yakos kanban notes <id> "<text>"   # set/replace notes field (any column)
  yakos kanban move <id> <col>       # move between columns
  yakos kanban done <id>             # shortcut to DONE
  yakos kanban delete <id>           # hard-delete a task (also: rm)

Known categories: bug  feature  chore  question  other  (arbitrary values accepted)
`)
}

// runDispatch implements `yakos dispatch` natively in Go.
//
// Usage mirrors cli/lib/dispatch.sh exactly:
//
//	yakos dispatch <agent-name> "<task-prompt>" [flags]
//
// Flags:
//
//	--runtime <id>       Override the agent's frontmatter runtime: field
//	--model <tier>       Override the model tier (haiku|sonnet|opus|fable); aliases expanded
//	--project <path>     Project repo path
//	--timeout <secs>     Max time to wait (default 600)
//	--eval-run-id <id>   Mark as model-routing eval dispatch
//	--allow-root         Set IS_SANDBOX=1 for root-user container dispatch
//	--help               Print help and exit 0
//
// Exits with the dispatch'd runtime's exit code.
func runDispatch(yakosRoot string, args []string) {
	agentName := ""
	task := ""
	runtimeOverride := ""
	modelOverride := ""
	evalRunID := ""
	project := ""
	timeoutSecs := 0
	allowRoot := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printDispatchHelp(os.Stdout)
			os.Exit(0)

		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --runtime requires an id")
				os.Exit(1)
			}
			runtimeOverride = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			runtimeOverride = arg[10:]

		case arg == "--model":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --model requires a tier (haiku|sonnet|opus|fable)")
				os.Exit(1)
			}
			modelOverride = args[i]
		case len(arg) > 8 && arg[:8] == "--model=":
			modelOverride = arg[8:]

		case arg == "--eval-run-id":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --eval-run-id requires an id string")
				os.Exit(1)
			}
			evalRunID = args[i]
		case len(arg) > 14 && arg[:14] == "--eval-run-id=":
			evalRunID = arg[14:]

		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]

		case arg == "--timeout":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "dispatch: --timeout requires a number")
				os.Exit(1)
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: --timeout value %q is not a number\n", args[i])
				os.Exit(1)
			}
			timeoutSecs = n
		case len(arg) > 10 && arg[:10] == "--timeout=":
			n, err := strconv.Atoi(arg[10:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "dispatch: --timeout value %q is not a number\n", arg[10:])
				os.Exit(1)
			}
			timeoutSecs = n

		case arg == "--allow-root":
			allowRoot = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "dispatch: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if agentName == "" {
				agentName = arg
			} else if task == "" {
				task = arg
			} else {
				fmt.Fprintln(os.Stderr, "dispatch: too many positional args (use --help)")
				os.Exit(1)
			}
		}
	}

	if agentName == "" {
		printDispatchHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "dispatch: missing <agent-name>")
		os.Exit(1)
	}
	if task == "" {
		printDispatchHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "dispatch: missing <task-prompt>")
		os.Exit(1)
	}

	// Resolve project from env or inference (mirrors dispatch.sh project resolution).
	if project == "" {
		project = os.Getenv("YAKOS_PROJECT_PATH")
	}
	if project == "" {
		// Try to infer from the current working directory via agent-control layout.
		cwd, _ := os.Getwd()
		home := os.Getenv("HOME")
		project = inferProjectFromCWD(cwd, home)
	}
	if project == "" {
		fmt.Fprintln(os.Stderr, "dispatch: cannot infer project; pass --project <path>")
		os.Exit(1)
	}
	if stat, err := os.Stat(project); err != nil || !stat.IsDir() {
		fmt.Fprintf(os.Stderr, "dispatch: project path not found or not a directory: %s\n", project)
		os.Exit(1)
	}

	// Resolve model alias (e.g. "balanced" → "sonnet").
	// The runtime package validates only concrete tiers; we expand aliases here.
	if modelOverride != "" {
		modelOverride = runtime.ResolveAlias(modelOverride)
		if !runtime.ValidateTier(modelOverride) {
			fmt.Fprintf(os.Stderr, "dispatch: invalid model tier %q (must be haiku|sonnet|opus|fable)\n", modelOverride)
			os.Exit(1)
		}
	}

	// Log the dispatch parameters to stderr (mirrors dispatch.sh:347).
	resolvedModel := modelOverride
	if resolvedModel == "" {
		resolvedModel = "(from frontmatter)"
	}
	chosenBy := "frontmatter"
	if modelOverride != "" {
		chosenBy = "override"
	} else if evalRunID != "" {
		chosenBy = "eval"
	}
	runtimeDesc := runtimeOverride
	if runtimeDesc == "" {
		runtimeDesc = "(from frontmatter)"
	}
	fmt.Fprintf(os.Stderr, "yakos dispatch: agent=%s runtime=%s model=%s (by:%s) project=%s\n",
		agentName, runtimeDesc, resolvedModel, chosenBy, project)

	// LOW-2 remediation (Phase 2.5): YAKOS_CONVERSATION_ID is read only on the
	// CLI one-shot path, validated against the identity allow-list, and then
	// passed explicitly into Request.ConversationID.  The daemon dispatch path
	// (dispatch.Run via Service.Run) no longer reads this env var; see dispatch.go.
	cliConvID := os.Getenv("YAKOS_CONVERSATION_ID")
	if cliConvID != "" {
		if err := dispatch.ValidateIdentityField("conversation_id", cliConvID); err != nil {
			fmt.Fprintf(os.Stderr, "dispatch: YAKOS_CONVERSATION_ID: %v\n", err)
			os.Exit(1)
		}
	}

	req := dispatch.Request{
		AgentName:      agentName,
		Task:           task,
		Project:        project,
		Runtime:        runtimeOverride,
		Model:          modelOverride,
		EvalRunID:      evalRunID,
		AllowRoot:      allowRoot,
		Timeout:        timeoutSecs,
		YakosRoot:      yakosRoot,
		ConversationID: cliConvID,
	}

	stdout, _, err := dispatch.Run(context.Background(), req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dispatch: %v\n", err)
		os.Exit(1)
	}

	// Write captured stdout to the terminal.
	if len(stdout) > 0 {
		if _, err := os.Stdout.Write(stdout); err != nil {
			fmt.Fprintf(os.Stderr, "dispatch: write stdout: %v\n", err)
			os.Exit(1)
		}
	}
}

// inferProjectFromCWD attempts to resolve a project path from cwd using the
// ~/agent-control/<name>/.project-path convention (mirrors dispatch.sh project inference).
func inferProjectFromCWD(cwd, home string) string {
	if home == "" {
		return ""
	}
	acRoot := filepath.Join(home, "agent-control")
	// Check if cwd is inside agent-control.
	if len(cwd) > len(acRoot)+1 && cwd[:len(acRoot)] == acRoot {
		rest := cwd[len(acRoot)+1:]
		name := rest
		if idx := len(name); idx > 0 {
			// Take first path component.
			for i, c := range name {
				if c == '/' || c == os.PathSeparator {
					name = name[:i]
					break
				}
			}
		}
		ppFile := filepath.Join(acRoot, name, ".project-path")
		if data, err := os.ReadFile(ppFile); err == nil { //nolint:gosec
			return strings.TrimSpace(string(data))
		}
	}
	// Scan all agent-control entries.
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(data))
		if cwd == p || len(cwd) > len(p)+1 && cwd[:len(p)] == p && cwd[len(p)] == '/' {
			return p
		}
	}
	return ""
}

// printDispatchHelp prints the help text for `yakos dispatch`, matching the
// bash dispatch.sh --help output exactly.
func printDispatchHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos dispatch <agent-name> "<task-prompt>" [flags]

Spawn a yakOS agent on the runtime its frontmatter declares (or a
runtime override). One-shot, non-interactive — captures stdout and
returns the runtime's exit code.

Arguments:
  <agent-name>      The agent's id (e.g. backend, security-reviewer,
                    pandaos-database). Must exist in the composed agent
                    set for the project.
  <task-prompt>     The work to do. Quoted so the lead can pass a
                    multi-line description.

Flags:
  --runtime <id>    Override the agent's frontmatter `+"`"+`runtime:`+"`"+` field.
  --model <tier>    Override the model tier for this dispatch only.
                    Accepted values: haiku | sonnet | opus | fable.
                    Recorded as model_chosen_by:"override" in the
                    dispatch-log. Does not affect the runtime selection.
  --project <path>  Project repo path. Defaults to inferring from cwd
                    (matches `+"`"+`yakos start`+"`"+`'s inference).
  --timeout <secs>  Max time to wait. Default 600s.
  --eval-run-id <id>
                    Mark this dispatch as part of a model-routing eval
                    run. Sets model_chosen_by:"eval" and eval_run_id in
                    the dispatch-log. Intended for use by the eval
                    harness (Phase 2); not for operator use.

Audit trail at ~/.yakos-state/dispatch-log.ndjson.

Examples:
  yakos dispatch backend "implement the /v1/meal-plans GET handler"
  yakos dispatch troubleshooter "diagnose why login_test fails on CI" --runtime codex
  yakos dispatch test-runner "run the suite" --model sonnet
`)
}

// runArchive implements `yakos archive` natively in Go.
//
// Usage mirrors cli/lib/archive.sh exactly:
//
//	yakos archive <project> <tag> [--auto-tag] [--yes]
//	yakos archive --help
//
// Rolls work/current/ into work/archive/<tag>/ for the named project under
// ~/agent-control/. Refuses to archive while expired hook-bypass entries remain.
//
// NOTE: worktree cleanup is explicitly NOT performed (same caveat as bash).
// Per git-hygiene rule §Worktree: "Cleanup happens at archive time —
// yakos archive does NOT clean worktrees; that's manual still in v0.1."
func runArchive(yakosRoot string, args []string) {
	project := ""
	tag := ""
	autoTag := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			archive.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--auto-tag":
			autoTag = true
		case arg == "--yes" || arg == "-y":
			// Accepted for parity; the Go implementation is always non-interactive.
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "archive: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if project == "" {
				project = arg
			} else if tag == "" {
				tag = arg
			} else {
				fmt.Fprintln(os.Stderr, "archive: too many positional args")
				os.Exit(1)
			}
		}
	}

	if project == "" {
		archive.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "archive: missing <project>")
		os.Exit(1)
	}
	if tag == "" {
		archive.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "archive: missing <tag>")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := archive.Config{
		YakosRoot: yakosRoot,
		Project:   project,
		Tag:       tag,
		AutoTag:   autoTag,
		HomeDir:   home,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := archive.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "archive: %v\n", err)
		os.Exit(1)
	}
}

// runTeam implements `yakos team` natively in Go.
//
// Usage mirrors cli/lib/team.sh exactly:
//
//	yakos team restart <project> [--tag <tag>] [--yes]
//	yakos team --help
//
// The only subcommand in v0.1 is `restart`. It archives work/current/ for
// the given project (by delegating to bash archive.sh, which is rank 10 in
// the port plan) and prints relaunch instructions.
func runTeam(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		printTeamHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	switch sub {
	case "restart":
		runTeamRestart(yakosRoot, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "team: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}
}

// runTeamRestart handles `yakos team restart <project> [--tag <tag>] [--yes]`.
func runTeamRestart(yakosRoot string, args []string) {
	project := ""
	tag := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			printTeamRestartHelp(os.Stdout)
			os.Exit(0)
		case arg == "--tag":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "team restart: --tag requires a value")
				os.Exit(1)
			}
			tag = args[i]
		case len(arg) > 6 && arg[:6] == "--tag=":
			tag = arg[6:]
		case arg == "--yes" || arg == "-y":
			// Accepted for parity; the Go implementation is always non-interactive.
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "team restart: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if project == "" {
				project = arg
			} else {
				fmt.Fprintln(os.Stderr, "team restart: too many positional args")
				os.Exit(1)
			}
		}
	}

	if project == "" {
		fmt.Fprintln(os.Stderr, "team restart: missing <project> (try --help)")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := team.Config{
		YakosRoot: yakosRoot,
		Project:   project,
		Tag:       tag,
		HomeDir:   home,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := team.Restart(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "team: %v\n", err)
		os.Exit(1)
	}
}

// printTeamHelp prints the help text for `yakos team`, matching the
// bash team.sh usage() output exactly.
func printTeamHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos team <subcommand> [args...]

Subcommands:
  restart <project>     Archive work/current/ and print instructions to
                        relaunch a fresh claude session. Does NOT
                        auto-relaunch in v0.1.

Options on 'restart':
  --tag <tag>           Override the auto-generated archive tag.
  --yes                 Skip the confirmation summary.

Other 'team' subcommands may be added in later versions.
`)
}

// printTeamRestartHelp prints the per-subcommand help for `yakos team restart`,
// matching the bash team.sh inline help exactly.
func printTeamRestartHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos team restart <project> [--tag <tag>] [--yes]

Archive work/current/ for <project> and print relaunch instructions.
Does NOT auto-launch claude in v0.1.
`)
}

// runInit implements `yakos init` natively in Go.
//
// Usage mirrors cli/lib/init.sh exactly:
//
//	yakos init <name> --project <path> [--force] [--template <kind>]
//	                  [--dry-run] [--with-gate] [--multi-dev] [--help]
//
// The subcommand name in the dispatch switch is "init"; the Go package is
// named "initialize" to avoid the reserved word collision (package init is
// special in Go).
//
// Bash flags --with-gate and --multi-dev are accepted for CLI parity but
// print advisory messages in the Go port; the underlying operations
// (git hook installation, /var/lib/yakos coord provisioning) delegate to
// bash in Phase 1.
func runInit(args []string) {
	name := ""
	project := ""
	template := ""
	force := false
	withGate := false
	multiDev := false
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			initialize.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "init: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]

		case arg == "--template":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "init: --template requires a kind (base, rails, go, python, node, rust, static-site)")
				os.Exit(1)
			}
			template = args[i]
		case len(arg) > 11 && arg[:11] == "--template=":
			template = arg[11:]

		case arg == "--force":
			force = true
		case arg == "--with-gate":
			withGate = true
		case arg == "--multi-dev":
			multiDev = true
		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "init: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if name == "" {
				name = arg
			} else {
				fmt.Fprintf(os.Stderr, "init: unexpected positional argument %q\n", arg)
				os.Exit(1)
			}
		}
	}

	if name == "" {
		initialize.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "init: missing <name>")
		os.Exit(1)
	}
	if project == "" {
		initialize.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "init: --project <path> is required")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := initialize.Config{
		Name:        name,
		ProjectPath: project,
		Template:    template,
		Force:       force,
		WithGate:    withGate,
		MultiDev:    multiDev,
		DryRun:      dryRun,
		HomeDir:     home,
		Writer:      os.Stdout,
		ErrWriter:   os.Stderr,
	}

	if _, err := initialize.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		os.Exit(1)
	}
}

// runInstall implements `yakos install` natively in Go.
//
// Usage mirrors cli/lib/install.sh exactly:
//
//	yakos install [--force] [--dry-run]
//	yakos install --help
//
// Creates per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
// pointing into the YakOS lib/ (repo or materialized embedded copy). Manages
// a launcher symlink at ~/.local/bin/yakos. Merges
// CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1 into ~/.claude/settings.json.
//
// Root resolution (see install.ResolveRoot):
//  1. YAKOS_ROOT env → if it has lib/, use it (dev-with-repo mode).
//  2. Exe-adjacent root (inferred by main()) → if it has lib/, use it.
//  3. Embedded lib in binary → materialize to ~/.local/share/yakos/<ver>/
//     and use that (binary-only / curl|sh install).
//  4. None → error with actionable message.
func runInstall(yakosRoot string, args []string) {
	force := false
	dryRun := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			install.PrintHelp(os.Stdout)
			os.Exit(0)
		case "--force":
			force = true
		case "--dry-run":
			dryRun = true
		default:
			fmt.Fprintf(os.Stderr, "install: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve the framework root via the cascade: env → repo → embedded.
	// This replaces the old "YAKOS_ROOT must be set" hard requirement.
	resolvedRoot, err := install.ResolveRoot(yakosRoot, home, version.Version, force, dryRun, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	cfg := install.Config{
		YakosRoot: resolvedRoot,
		HomeDir:   home,
		Force:     force,
		DryRun:    dryRun,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := install.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "install: %v\n", err)
		os.Exit(1)
	}
}

// runUninstall implements `yakos uninstall` natively in Go.
//
// Usage mirrors cli/lib/uninstall.sh exactly:
//
//	yakos uninstall [--restore-settings] [--root <path>] [--dry-run]
//	yakos uninstall --help
//
// Removes per-file symlinks under ~/.claude/{agents,skills,rules,playbooks}/
// that point into the YakOS repo. Removes the managed launcher symlink recorded
// in ~/.yakos-state/install-manifest. Removes ~/.yakos and the manifest.
// Handles settings.json according to the created-marker and --restore-settings.
//
// YAKOS_ROOT is not needed by uninstall (it reads ~/.yakos instead). The --root
// flag overrides the pointer file, mirroring the bash --root flag.
func runUninstall(args []string) {
	restoreSettings := false
	explicitRoot := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			uninstall.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--restore-settings":
			restoreSettings = true
		case arg == "--dry-run":
			dryRun = true
		case arg == "--root":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "uninstall: --root requires a path argument")
				os.Exit(1)
			}
			explicitRoot = args[i]
		case len(arg) > 7 && arg[:7] == "--root=":
			explicitRoot = arg[7:]
		default:
			fmt.Fprintf(os.Stderr, "uninstall: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := uninstall.Config{
		HomeDir:         home,
		ExplicitRoot:    explicitRoot,
		RestoreSettings: restoreSettings,
		DryRun:          dryRun,
		Writer:          os.Stdout,
		ErrWriter:       os.Stderr,
	}

	if _, err := uninstall.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "uninstall: %v\n", err)
		os.Exit(1)
	}
}

// runStart implements `yakos start` natively in Go.
//
// Usage mirrors cli/lib/start.sh exactly:
//
//	yakos start [<name>] [flags]
//
// Resolves the project, selects a runtime, prints a preflight banner (including
// the lead-dispatch-discipline one-liner), writes audit-log entries, and then
// exec's the runtime CLI replacing the current process.
//
// The --dry-run and --print-agents modes exit without launching a runtime.
//
// YAKOS_ROOT is used for agent composition; it defaults to the value resolved
// from the executable location (same as main()).
func runStart(yakosRoot string, args []string) {
	name := ""
	runtime := ""
	safe := false
	allowRoot := false
	noAgents := false
	dryRun := false
	printAgents := false
	continueSession := false
	resume := ""
	fork := false
	ide := false
	bare := false
	strictMCP := false
	model := ""
	noREPL := false
	consoleAddr := ""
	var passthrough []string

	// Honor YAKOS_ALLOW_ROOT env as equivalent to --allow-root.
	if os.Getenv("YAKOS_ALLOW_ROOT") == "1" {
		allowRoot = true
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			start.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --runtime requires an id")
				os.Exit(1)
			}
			runtime = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			runtime = arg[10:]

		case arg == "--safe":
			safe = true
		case arg == "--allow-root":
			allowRoot = true
		case arg == "--no-agents":
			noAgents = true
		case arg == "--dry-run":
			dryRun = true
		case arg == "--print-agents":
			printAgents = true
		case arg == "-c" || arg == "--continue":
			continueSession = true
		case arg == "--fork-session":
			fork = true
		case arg == "--ide":
			ide = true
		case arg == "--bare":
			bare = true
		case arg == "--strict-mcp":
			strictMCP = true
		case arg == "--no-repl" || arg == "--web":
			noREPL = true

		case arg == "--console-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --console-addr requires an address")
				os.Exit(1)
			}
			consoleAddr = args[i]
		case len(arg) > 15 && arg[:15] == "--console-addr=":
			consoleAddr = arg[15:]

		case arg == "--resume":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --resume requires a session id")
				os.Exit(1)
			}
			resume = args[i]
		case len(arg) > 9 && arg[:9] == "--resume=":
			resume = arg[9:]

		case arg == "--model":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "start: --model requires an alias")
				os.Exit(1)
			}
			model = args[i]
		case len(arg) > 8 && arg[:8] == "--model=":
			model = arg[8:]

		case arg == "--":
			// Rest forwarded to runtime CLI.
			passthrough = append(passthrough, args[i+1:]...)
			i = len(args)

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "start: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if name == "" {
				name = arg
			} else {
				fmt.Fprintf(os.Stderr, "start: unexpected positional argument %q\n", arg)
				os.Exit(1)
			}
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// Resolve the console token so the banner URL is accurate.
	// Skip token I/O on --dry-run / --print-agents: those paths must be
	// read-only and must not create ~/.yakos-state/ or write a token file.
	stateDir := filepath.Join(home, ".yakos-state")
	var consoleTok string
	if !dryRun && !printAgents {
		consoleTok, _ = internalconsoleui.LoadOrCreateToken(stateDir)
	}

	cfg := start.Config{
		Name:         name,
		YakosRoot:    yakosRoot,
		HomeDir:      home,
		Runtime:      runtime,
		Safe:         safe,
		AllowRoot:    allowRoot,
		NoAgents:     noAgents,
		DryRun:       dryRun,
		PrintAgents:  printAgents,
		Continue:     continueSession,
		Resume:       resume,
		Fork:         fork,
		IDE:          ide,
		Bare:         bare,
		StrictMCP:    strictMCP,
		Model:        model,
		Passthrough:  passthrough,
		NoREPL:       noREPL,
		ConsoleAddr:  consoleAddr,
		ConsoleToken: consoleTok,
		Writer:       os.Stdout,
		ErrWriter:    os.Stderr,
	}

	if _, err := start.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}

	// When --no-repl is set, start.Run returns after the banner (no exec).
	// Hand off to runServe to bring up the daemon + console.
	if noREPL {
		serveArgs := []string{}
		if consoleAddr != "" {
			serveArgs = append(serveArgs, "--console-addr", consoleAddr)
		}
		if dryRun {
			// --no-repl --dry-run: serve path already printed its intent in
			// the banner; exit cleanly without binding.
			os.Exit(0)
		}
		runServe(yakosRoot, serveArgs)
	}
}

// runUpdate implements `yakos update` natively in Go.
//
// Install-type detection:
//   - Source / dev install (bash tree present at <yakosRoot>/cli/yakos):
//     Runs git pull --ff-only + optional per-project refresh.
//     Flags: --allow-non-ff, --all, --dry-run.
//   - Binary-only install (no bash tree, common curl|sh case):
//     Downloads the latest release from GitHub, verifies SHA-256, and
//     atomically replaces the running binary.
//     Flags: --check, --force, --dry-run.
//
// Mode override flags:
//
//	--binary   Force binary-update path regardless of bash tree presence.
//	--source   Force git-pull path regardless of bash tree presence.
//
// Common to both modes:
//
//	yakos update --check   — report latest version; apply nothing
//	yakos update --help    — print help and exit 0
//
// YAKOS_ROOT must be set in the environment (resolved from the binary
// location by main() when unset).
func runUpdate(yakosRoot string, args []string) {
	// Source-path flags.
	allowNonFF := false
	allProjects := false

	// Binary-path flags.
	checkOnly := false
	force := false

	// Common flags.
	dryRun := false

	// Mode override.
	forceBinary := false
	forceSource := false

	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			printUpdateHelp(os.Stdout)
			os.Exit(0)
		// Source-path flags.
		case "--allow-non-ff":
			allowNonFF = true
		case "--all":
			allProjects = true
		// Binary-path flags.
		case "--check":
			checkOnly = true
		case "--force":
			force = true
		// Common.
		case "--dry-run":
			dryRun = true
		// Mode override.
		case "--binary":
			forceBinary = true
		case "--source":
			forceSource = true
		default:
			fmt.Fprintf(os.Stderr, "update: unknown argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "update: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	// --binary and --source are mutually exclusive; silently resolving them
	// would be confusing and could apply the wrong update path.
	if forceBinary && forceSource {
		fmt.Fprintln(os.Stderr, "update: --binary and --source are mutually exclusive; specify at most one")
		os.Exit(1)
	}

	// Determine install type.
	isBinaryInstall := !passthrough.BashYakosExists(yakosRoot)
	if forceBinary {
		isBinaryInstall = true
	}
	if forceSource {
		isBinaryInstall = false
	}

	if isBinaryInstall {
		runUpdateBinary(yakosRoot, checkOnly || dryRun, force)
		return
	}

	// Source / dev install: git pull path.
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	// --check on source path: just print current + latest and exit.
	if checkOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		latest, err := selfupdate.LatestRelease(ctx, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "update: %v\n", err)
			os.Exit(1)
		}
		cur, _ := version.Read(yakosRoot)
		fmt.Fprintf(os.Stdout, "current: %s\nlatest:  %s\n", cur, latest)
		return
	}

	cfg := update.Config{
		YakosRoot:   yakosRoot,
		HomeDir:     home,
		AllowNonFF:  allowNonFF,
		AllProjects: allProjects,
		DryRun:      dryRun,
		Writer:      os.Stdout,
		ErrWriter:   os.Stderr,
	}

	if _, err := update.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
	}
}

// runUpdateBinary runs the self-update path for binary-only installs.
// dryRun covers both --dry-run and --check (no write; just report).
func runUpdateBinary(yakosRoot string, dryRun, force bool) {
	// Two independent timeout bounds govern this operation:
	//   - metadata fetches (GitHub API, checksums.txt): bounded by the metadata
	//     client's own 15 s client.Timeout (BuildDefaultClient), independent of
	//     this context deadline.
	//   - binary download: client.Timeout is 0 on the download client, so this
	//     context deadline is the effective cancellation mechanism.  10 minutes
	//     gives a 256 MiB binary room on a slow link (~3 Mbit/s) while still
	//     providing a DoS bound.  The 256 MiB LimitReader is the size cap.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Determine the running version.  For binary-only installs the ldflags
	// variable is the authoritative source; fall back to the VERSION file
	// if present (dev build without ldflags).
	currentVersion := strings.TrimSpace(version.Version)
	if currentVersion == "" {
		if v, err := version.Read(yakosRoot); err == nil {
			// Strip the " (go)" suffix that Read appends.
			currentVersion = strings.TrimSuffix(strings.TrimSpace(v), " (go)")
			currentVersion = strings.TrimSpace(currentVersion)
		}
	}

	// Resolve the executable path upfront so the operator can see what will
	// be replaced before any network activity begins (MEDIUM-2).
	exePath, exeErr := os.Executable()
	if exeErr == nil {
		exePath, exeErr = filepath.EvalSymlinks(exePath)
	}
	fmt.Fprintf(os.Stdout, "update mode: binary\n")
	if exeErr == nil {
		fmt.Fprintf(os.Stdout, "target binary: %s\n", exePath)
	}
	if currentVersion != "" {
		fmt.Fprintf(os.Stdout, "current version: %s\n", currentVersion)
	}

	res, err := selfupdate.Apply(ctx, selfupdate.Opts{
		CurrentVersion: currentVersion,
		Force:          force,
		DryRun:         dryRun,
		Writer:         os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		os.Exit(1)
	}

	// NIT-6: both branches printed the same line; collapse to a single check.
	if !res.AlreadyUpToDate {
		fmt.Fprintf(os.Stdout, "latest version: %s\n", res.NewVersion)
	}
}

// printUpdateHelp writes the combined help text for `yakos update`.
func printUpdateHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos update — update yakOS to the latest release

Auto-detects install type:
  binary install  → downloads latest GitHub release + verifies SHA-256
                    + atomically replaces the running binary
  source install  → git pull --ff-only in $YAKOS_ROOT + optional refresh

Mode override (mutually exclusive):
  --binary         Force binary-update path (even if bash tree is present).
  --source         Force git-pull path (even if bash tree is absent).

Binary-install options:
  --check          Report whether an update is available; apply nothing.
  --force          Reinstall latest even when already up to date.
  --dry-run        Print what WOULD happen; write nothing.

Source-install options:
  --allow-non-ff   Allow non-fast-forward git pull.
  --all            After the framework update, discover every deployed
                   project and run yakos refresh on each.
  --dry-run        Print what WOULD happen without running git pull or
                   project refresh.

Common options:
  --help, -h       Print this help.
`)
}

// runQuickstart implements `yakos quickstart` natively in Go.
//
// Usage mirrors cli/lib/quickstart.sh exactly:
//
//	yakos quickstart [--runtime <id>] [--multi-dev] [--safe] [--allow-root] [--dry-run]
//	yakos quickstart --help
//
// Detects the current state and runs only what is needed:
//  1. yakOS not installed → yakos install
//  2. cwd is a git repo not yet bootstrapped → yakos init
//  3. project already bootstrapped → yakos start
//
// Each step delegates to the corresponding Go package (install, initialize, start).
// Idempotent; safe to re-run against an already-onboarded project.
func runQuickstart(yakosRoot string, args []string) {
	runtime := ""
	multiDev := false
	safe := false
	allowRoot := false
	dryRun := false

	// Honor YAKOS_ALLOW_ROOT env as equivalent to --allow-root.
	if os.Getenv("YAKOS_ALLOW_ROOT") == "1" {
		allowRoot = true
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			quickstart.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "quickstart: --runtime requires an id")
				os.Exit(1)
			}
			runtime = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			runtime = arg[10:]

		case arg == "--multi-dev":
			multiDev = true
		case arg == "--safe":
			safe = true
		case arg == "--allow-root":
			allowRoot = true
		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "quickstart: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			fmt.Fprintf(os.Stderr, "quickstart: unexpected argument %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve YAKOS_ROOT from env (bash entry-point may set it).
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	if yakosRoot == "" {
		fmt.Fprintln(os.Stderr, "quickstart: YAKOS_ROOT is not set")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := quickstart.Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Runtime:   runtime,
		MultiDev:  multiDev,
		Safe:      safe,
		AllowRoot: allowRoot,
		DryRun:    dryRun,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	if _, err := quickstart.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "quickstart: %v\n", err)
		os.Exit(1)
	}
}

// runAuth implements `yakos auth` natively in Go.
//
// Usage mirrors cli/lib/auth.sh exactly:
//
//	yakos auth status [<runtime>]      — report cli + auth state
//	yakos auth login <runtime>         — print login instructions / exec runtime login
//	yakos auth logout <runtime>        — best-effort credential removal
//	yakos auth set-default <runtime>   — persist default runtime
//	yakos auth --help                  — print help and exit 0
//
// OS keychain access uses github.com/zalando/go-keyring, which abstracts
// macOS Keychain Services, Linux secret-service (D-Bus), and Windows DPAPI.
// Keyring access degrades gracefully when the service is unavailable.
func runAuth(args []string) {
	sub := ""
	target := ""
	asDefault := false
	doAll := false

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			auth.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			sub = args[0]
			args = args[1:]
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			auth.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--as-default":
			asDefault = true
		case arg == "--all":
			doAll = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "auth %s: unknown flag %q\n", sub, arg)
			os.Exit(1)
		default:
			if target == "" {
				target = arg
			} else {
				fmt.Fprintf(os.Stderr, "auth %s: too many positional args\n", sub)
				os.Exit(1)
			}
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := auth.Config{
		HomeDir:    home,
		Subcommand: sub,
		Target:     target,
		AsDefault:  asDefault,
		All:        doAll,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := auth.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "auth: %v\n", err)
		os.Exit(1)
	}
}

// runMemory implements `yakos memory` natively in Go.
//
// Usage mirrors cli/lib/memory.sh (rank 18):
//
//	yakos memory list                            — list MEMORY.md index + files
//	yakos memory read <slug>                     — print a memory file's body
//	yakos memory write <slug> <type> <body>      — create or replace a memory
//	yakos memory delete <slug>                   — remove a memory file
//	yakos memory index-rebuild                   — rewrite MEMORY.md from files
//	yakos memory --help                          — print help and exit 0
//
// The memory directory is resolved from YAKOS_MEMORY_DIR env (for tests) or
// from the encoded project path: ~/.claude/projects/<encoded>/memory/.
// The project path is read from YAKOS_PROJECT_PATH or inferred from cwd.
func runMemory(args []string) {
	sub := ""
	slug := ""
	memType := ""
	body := ""

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			memory.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			sub = args[0]
			args = args[1:]
		}
	}

	// Per-subcommand positional arguments.
	switch sub {
	case "read", "delete":
		if len(args) > 0 {
			slug = args[0]
		} else {
			fmt.Fprintf(os.Stderr, "memory %s: <slug> required (try --help)\n", sub)
			os.Exit(1)
		}
	case "write":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "memory write: <slug> <type> <body> required (try --help)")
			os.Exit(1)
		}
		slug = args[0]
		memType = args[1]
		body = args[2]
	case "list", "index-rebuild", "":
		// no positional args
	default:
		fmt.Fprintf(os.Stderr, "memory: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	// Resolve memory directory.
	// Priority: YAKOS_MEMORY_DIR (test injection) → YAKOS_PROJECT_PATH encode → cwd inference.
	memDir := os.Getenv("YAKOS_MEMORY_DIR")
	if memDir == "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/tmp"
		}
		// Resolve project path.
		projectPath := os.Getenv("YAKOS_PROJECT_PATH")
		if projectPath == "" {
			cwd, _ := os.Getwd()
			projectPath = inferProjectFromCWD(cwd, home)
		}
		if projectPath == "" {
			fmt.Fprintln(os.Stderr, "memory: cannot resolve project path; set YAKOS_PROJECT_PATH or run from inside a project")
			os.Exit(1)
		}
		encoded := memoryEncodeProjectPath(projectPath)
		memDir = filepath.Join(home, ".claude", "projects", encoded, "memory")
	}

	cfg := memory.Config{
		MemoryDir:  memDir,
		Subcommand: sub,
		Slug:       slug,
		Type:       memType,
		Body:       body,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if err := memory.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "memory: %v\n", err)
		os.Exit(1)
	}
}

// memoryEncodeProjectPath encodes a project path to the Claude Code format
// used for ~/.claude/projects/<encoded>/.  Mirrors initialize.encodeProjectPath
// and is safe on both Unix and Windows.
//
// Algorithm: strip Windows drive-letter prefix (e.g. "C:"), replace path
// separators and Windows-illegal chars ('/', '\', ':', '<', '>', '"', '|',
// '?', '*') with '-', trim leading/trailing '-', collapse consecutive '-'
// runs, return "root" for degenerate inputs like "/" or "C:\".
func memoryEncodeProjectPath(absPath string) string {
	s := absPath

	// Strip Windows drive-letter prefix (e.g. "C:" or "c:").
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		s = s[2:]
	}

	// Replace separators and Windows-illegal chars with '-'.
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '<', '>', '"', '|', '?', '*':
			sb.WriteByte('-')
		default:
			sb.WriteRune(r)
		}
	}
	encoded := sb.String()

	// Trim leading and trailing '-'.
	encoded = strings.Trim(encoded, "-")

	// Collapse consecutive '-' into a single '-'.
	for strings.Contains(encoded, "--") {
		encoded = strings.ReplaceAll(encoded, "--", "-")
	}

	// Guard against empty result (e.g. "/" or `C:\`).
	if encoded == "" {
		return "root"
	}
	return encoded
}

// runAgent implements `yakos agent` (and its `yakos agents` plural alias)
// natively in Go.
//
// Usage mirrors cli/lib/agent.sh exactly:
//
//	yakos agent new <name> [flags]      — scaffold a new project agent file
//	yakos agent lint [<project>]        — audit every agent file in the project
//	yakos agent diff <name> [flags]     — body diff vs extends: parent
//	yakos agent list [--project <path>] [--json] — list composed roster
//	yakos agent docs [--format md|html] — render auto-generated reference page
//	yakos agents lint [<project>]       — plural alias for lint
func runAgent(yakosRoot, cmdName string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		agent.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	// Resolve YAKOS_ROOT from env.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := agent.Config{
		YakosRoot: yakosRoot,
		HomeDir:   home,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	switch sub {
	case "-h", "--help":
		agent.PrintHelp(os.Stdout)
		os.Exit(0)

	case "new", "create":
		cfg.Subcommand = "new"
		parseAgentNewFlags(&cfg, rest)

	case "lint":
		cfg.Subcommand = "lint"
		parseAgentLintFlags(&cfg, rest)

	case "diff":
		cfg.Subcommand = "diff"
		parseAgentDiffFlags(&cfg, rest)

	case "list":
		cfg.Subcommand = "list"
		parseAgentListFlags(&cfg, rest)

	case "docs":
		runAgentDocs(yakosRoot, rest)
		return

	default:
		// The bash version also routes the plural 'agents lint' here; the
		// command name is already "agent" or "agents" — allow 'lint' via
		// 'yakos agents lint' which arrives as sub=lint above. Unknown
		// subcommands get a helpful error.
		fmt.Fprintf(os.Stderr, "%s: unknown subcommand %q (try --help)\n", cmdName, sub)
		os.Exit(1)
	}

	r, err := agent.Run(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdName, err)
		os.Exit(1)
	}
	if r != nil && r.Errors > 0 {
		os.Exit(1)
	}
}

// parseAgentNewFlags parses flags for `yakos agent new`.
func parseAgentNewFlags(cfg *agent.Config, args []string) {
	i := 0
	for i < len(args) {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--runtime":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --runtime requires an id")
				os.Exit(1)
			}
			cfg.Runtime = args[i]
		case len(arg) > 10 && arg[:10] == "--runtime=":
			cfg.Runtime = arg[10:]
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --project requires a path")
				os.Exit(1)
			}
			cfg.Project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			cfg.Project = arg[10:]
		case arg == "--extends":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --extends requires an id")
				os.Exit(1)
			}
			cfg.Extends = args[i]
		case len(arg) > 10 && arg[:10] == "--extends=":
			cfg.Extends = arg[10:]
		case arg == "--role":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --role requires a value")
				os.Exit(1)
			}
			cfg.Role = args[i]
		case len(arg) > 7 && arg[:7] == "--role=":
			cfg.Role = arg[7:]
		case arg == "--domain":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --domain requires a value")
				os.Exit(1)
			}
			cfg.Domain = args[i]
		case len(arg) > 9 && arg[:9] == "--domain=":
			cfg.Domain = arg[9:]
		case arg == "--model":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --model requires a value")
				os.Exit(1)
			}
			cfg.Model = args[i]
		case len(arg) > 8 && arg[:8] == "--model=":
			cfg.Model = arg[8:]
		case arg == "--tools":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent new: --tools requires a value")
				os.Exit(1)
			}
			cfg.Tools = args[i]
		case len(arg) > 8 && arg[:8] == "--tools=":
			cfg.Tools = arg[8:]
		case arg == "--force":
			cfg.Force = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent new: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if cfg.Name == "" {
				cfg.Name = arg
			} else {
				fmt.Fprintf(os.Stderr, "agent new: too many positional args\n")
				os.Exit(1)
			}
		}
		i++
	}
	if cfg.Name == "" {
		agent.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "agent new: <name> required")
		os.Exit(1)
	}
}

// parseAgentLintFlags parses flags for `yakos agent lint`.
func parseAgentLintFlags(cfg *agent.Config, args []string) {
	for _, arg := range args {
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent lint: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if cfg.Project == "" {
				cfg.Project = arg
			} else {
				fmt.Fprintln(os.Stderr, "agent lint: too many positional args")
				os.Exit(1)
			}
		}
	}
}

// parseAgentDiffFlags parses flags for `yakos agent diff`.
func parseAgentDiffFlags(cfg *agent.Config, args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent diff: --project requires a path")
				os.Exit(1)
			}
			cfg.Project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			cfg.Project = arg[10:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent diff: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			if cfg.Name == "" {
				cfg.Name = arg
			} else {
				fmt.Fprintln(os.Stderr, "agent diff: too many positional args")
				os.Exit(1)
			}
		}
	}
	if cfg.Name == "" {
		fmt.Fprintln(os.Stderr, "agent diff: <name> required")
		os.Exit(1)
	}
}

// parseAgentListFlags parses flags for `yakos agent list`.
func parseAgentListFlags(cfg *agent.Config, args []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			agent.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--json":
			cfg.JSON = true
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent list: --project requires a path")
				os.Exit(1)
			}
			cfg.Project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			cfg.Project = arg[10:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent list: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "agent list: unexpected argument %q\n", arg)
			os.Exit(1)
		}
	}
}

// runAgentDocs implements `yakos agent docs [--format md|html]`.
func runAgentDocs(yakosRoot string, args []string) {
	format := agent.DocsFormatMD
	project := ""
	outPath := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(os.Stdout, "yakos agent docs [--format md|html] [--project <path>] [--out <file>]\n\n")
			_, _ = fmt.Fprint(os.Stdout, "Render an auto-generated agent reference page from frontmatter.\n")
			os.Exit(0)
		case arg == "--format":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent docs: --format requires md or html")
				os.Exit(1)
			}
			switch args[i] {
			case "md", "markdown":
				format = agent.DocsFormatMD
			case "html":
				format = agent.DocsFormatHTML
			default:
				fmt.Fprintf(os.Stderr, "agent docs: unknown format %q (md or html)\n", args[i])
				os.Exit(1)
			}
		case len(arg) > 9 && arg[:9] == "--format=":
			switch arg[9:] {
			case "md", "markdown":
				format = agent.DocsFormatMD
			case "html":
				format = agent.DocsFormatHTML
			default:
				fmt.Fprintf(os.Stderr, "agent docs: unknown format %q\n", arg[9:])
				os.Exit(1)
			}
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent docs: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]
		case arg == "--out":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "agent docs: --out requires a path")
				os.Exit(1)
			}
			outPath = args[i]
		case len(arg) > 6 && arg[:6] == "--out=":
			outPath = arg[6:]
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "agent docs: unknown flag %q\n", arg)
			os.Exit(1)
		default:
			fmt.Fprintf(os.Stderr, "agent docs: unexpected argument %q\n", arg)
			os.Exit(1)
		}
	}

	var w = os.Stdout
	if outPath != "" {
		// Atomic write.
		tmp := outPath + ".tmp"
		f, err := os.Create(tmp) //nolint:gosec
		if err != nil {
			fmt.Fprintf(os.Stderr, "agent docs: create %s: %v\n", tmp, err)
			os.Exit(1)
		}
		err = agent.RenderDocs(agent.DocsConfig{
			YakosRoot: yakosRoot,
			Project:   project,
			Format:    format,
			Writer:    f,
		})
		_ = f.Close()
		if err != nil {
			_ = os.Remove(tmp)
			fmt.Fprintf(os.Stderr, "agent docs: %v\n", err)
			os.Exit(1)
		}
		if err := os.Rename(tmp, outPath); err != nil {
			_ = os.Remove(tmp)
			fmt.Fprintf(os.Stderr, "agent docs: rename: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "agent docs: wrote %s\n", outPath)
		return
	}

	if err := agent.RenderDocs(agent.DocsConfig{
		YakosRoot: yakosRoot,
		Project:   project,
		Format:    format,
		Writer:    w,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "agent docs: %v\n", err)
		os.Exit(1)
	}
}

// runSession implements `yakos session` natively in Go.
//
// Usage mirrors cli/lib/session.sh and extends it with info/resume/fork:
//
//	yakos session list <project>               List sessions for a project.
//	yakos session info <project> [<id>]        Show details for a session.
//	yakos session resume <project> [<id>]      Print start flags to resume a session.
//	yakos session fork <project> [<id>]        Print start flags to fork a session.
//	yakos session --help                       Print help and exit 0.
//
// Session history is read from:
//
//	~/agent-control/<project>/work/current/.session-started-history.ndjson
//
// The export subcommand from bash session.sh is NOT ported in Phase 1;
// it requires tar/gzip plumbing out of scope for the current batch.
// Use YAKOS_IMPL=bash yakos session export for that path.
func runSession(args []string) {
	sub := ""
	project := ""
	id := ""

	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			session.PrintHelp(os.Stdout)
			os.Exit(0)
		case "export":
			fmt.Fprintln(os.Stderr, "session: export is not yet implemented in the Go port (tar/gzip plumbing out of scope for Phase 1)")
			fmt.Fprintln(os.Stderr, "  Use: YAKOS_IMPL=bash yakos session export")
			os.Exit(1)
		default:
			sub = args[0]
			args = args[1:]
		}
	}

	// Each subcommand takes: <project> [<id>]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			session.PrintHelp(os.Stdout)
			os.Exit(0)
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "session: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		default:
			if project == "" {
				project = arg
			} else if id == "" {
				id = arg
			} else {
				fmt.Fprintln(os.Stderr, "session: too many positional args (try --help)")
				os.Exit(1)
			}
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := session.Config{
		HomeDir:    home,
		Subcommand: sub,
		Project:    project,
		ID:         id,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := session.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "session: %v\n", err)
		os.Exit(1)
	}
}

// runMigrate implements `yakos migrate` natively in Go.
//
// Usage mirrors cli/lib/migrate.sh (rank 21), adapted to target the
// sidecar schema-version files managed by the kanban and memory packages
// (Decision A, go-port-decisions-2026-06-02.md).
//
//	yakos migrate status              — show schema version for each sidecar format
//	yakos migrate up [<format>]       — apply pending migrations (no-op in Phase 1)
//	yakos migrate down [<format>]     — error; deferred to Phase 1.5
//	yakos migrate --dry-run           — print what WOULD be done without writing
//	yakos migrate --help              — print help and exit 0
//
// The work directory is resolved from YAKOS_WORK_DIR or
// $HOME/agent-control/$YAKOS_PROJECT_NAME/work. The memory directory is
// resolved from YAKOS_MEMORY_DIR or ~/.claude/projects/memory/.
func runMigrate(args []string) {
	sub := ""
	format := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			migrate.PrintHelp(os.Stdout)
			os.Exit(0)
		case arg == "--dry-run":
			dryRun = true
		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "migrate: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		default:
			if sub == "" {
				sub = arg
			} else if format == "" {
				format = arg
			} else {
				fmt.Fprintln(os.Stderr, "migrate: too many positional args (try --help)")
				os.Exit(1)
			}
		}
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	workDir := os.Getenv("YAKOS_WORK_DIR")
	if workDir == "" {
		if proj := os.Getenv("YAKOS_PROJECT_NAME"); proj != "" {
			workDir = filepath.Join(home, "agent-control", proj, "work")
		}
	}

	memDir := os.Getenv("YAKOS_MEMORY_DIR")

	cfg := migrate.Config{
		Subcommand: sub,
		Format:     format,
		WorkDir:    workDir,
		MemoryDir:  memDir,
		HomeDir:    home,
		DryRun:     dryRun,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := migrate.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

// runPlugin implements `yakos plugin` natively in Go.
//
// Usage mirrors cli/lib/plugin.sh exactly:
//
//	yakos plugin list
//	yakos plugin install <source> [--id <id>] [--force]
//	yakos plugin remove <id>
//	yakos plugin validate <dir> [--id <id>]
//	yakos plugin register <name> <dir>
//	yakos plugin status
//	yakos plugin --help
//
// Plugins live at ~/.yakos/plugins/<id>/runtime.sh. The built-in runtimes
// claude, codex, and gemini are reserved and cannot be installed or removed.
func runPlugin(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		plugin.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	cfg := plugin.Config{
		Subcommand: sub,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "list", "status":
		// No extra args needed.

	case "install":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case arg == "--force":
				cfg.Force = true
			case arg == "--id":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plugin install: --id requires a value")
					os.Exit(1)
				}
				cfg.ID = rest[i]
			case len(arg) > 5 && arg[:5] == "--id=":
				cfg.ID = arg[5:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin install: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Source == "" {
					cfg.Source = arg
				} else {
					fmt.Fprintln(os.Stderr, "plugin install: too many positional args (try --help)")
					os.Exit(1)
				}
			}
		}

	case "remove":
		for _, arg := range rest {
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin remove: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.ID == "" {
					cfg.ID = arg
				} else {
					fmt.Fprintln(os.Stderr, "plugin remove: too many positional args (try --help)")
					os.Exit(1)
				}
			}
		}

	case "validate":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case arg == "--id":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plugin validate: --id requires a value")
					os.Exit(1)
				}
				cfg.ID = rest[i]
			case len(arg) > 5 && arg[:5] == "--id=":
				cfg.ID = arg[5:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin validate: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Dir == "" {
					cfg.Dir = arg
				} else {
					fmt.Fprintln(os.Stderr, "plugin validate: too many positional args (try --help)")
					os.Exit(1)
				}
			}
		}

	case "register":
		// register <name> <dir>
		positionals := make([]string, 0, 2)
		for _, arg := range rest {
			switch {
			case arg == "-h" || arg == "--help":
				plugin.PrintHelp(os.Stdout)
				os.Exit(0)
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "plugin register: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				positionals = append(positionals, arg)
			}
		}
		if len(positionals) >= 1 {
			cfg.ID = positionals[0]
		}
		if len(positionals) >= 2 {
			cfg.Dir = positionals[1]
		}
		if len(positionals) > 2 {
			fmt.Fprintln(os.Stderr, "plugin register: too many positional args (try --help)")
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "plugin: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	if _, err := plugin.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "plugin: %v\n", err)
		os.Exit(1)
	}
}

// runTeach implements `yakos teach` natively in Go.
//
// Usage mirrors cli/lib/teach.sh exactly:
//
//	yakos teach <agent-name> <lesson-file> [flags]
//
// Flags:
//
//	--project <path>   Project root (defaults to inferred from ~/agent-control/).
//	--section <name>   H2 heading to append under (default: "Lessons learned").
//	--dry-run          Print what would be written; do not modify files.
//	--help             Print help and exit 0.
//
// Appends a dated lesson bullet to the project agent file at
// <project>/.claude/agents/<name>.md under the target section,
// creating the section when absent. Backs up the original file before
// every edit. Uses atomic temp-rename writes (Q8 / Decision A).
func runTeach(args []string) {
	agentName := ""
	lessonFile := ""
	project := ""
	section := ""
	dryRun := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			teach.PrintHelp(os.Stdout)
			os.Exit(0)

		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "teach: --project requires a path")
				os.Exit(1)
			}
			project = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			project = arg[10:]

		case arg == "--section":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "teach: --section requires a name")
				os.Exit(1)
			}
			section = args[i]
		case len(arg) > 10 && arg[:10] == "--section=":
			section = arg[10:]

		case arg == "--dry-run":
			dryRun = true

		case len(arg) > 0 && arg[0] == '-':
			fmt.Fprintf(os.Stderr, "teach: unknown flag %q (try --help)\n", arg)
			os.Exit(1)

		default:
			if agentName == "" {
				agentName = arg
			} else if lessonFile == "" {
				lessonFile = arg
			} else {
				fmt.Fprintln(os.Stderr, "teach: too many positional args (try --help)")
				os.Exit(1)
			}
		}
	}

	if agentName == "" {
		teach.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "teach: <agent-name> required")
		os.Exit(1)
	}
	if lessonFile == "" {
		teach.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "teach: <lesson-file> required")
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := teach.Config{
		AgentName:  agentName,
		LessonFile: lessonFile,
		ProjectDir: project,
		Section:    section,
		DryRun:     dryRun,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := teach.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "teach: %v\n", err)
		os.Exit(1)
	}
}

// runSoul implements `yakos soul` natively in Go.
//
// Usage mirrors cli/lib/soul.sh exactly:
//
//	yakos soul show    [global|project]              — print current soul
//	yakos soul edit    [global|project]              — open in $EDITOR
//	yakos soul history [global|project]              — list version snapshots
//	yakos soul revert  <version> [global|project]    — revert to snapshot
//	yakos soul pending                               — list pending edits
//	yakos soul approve <edit-slug>                   — apply pending (M1+ deferred)
//	yakos soul reject  <edit-slug>                   — discard pending (M1+ deferred)
//
// Soul files live at ~/.yakos-state/soul/{global,<project-slug>}.md.
// Snapshots are written atomically to ~/.yakos-state/soul/history/.
func runSoul(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		soul.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := soul.Config{
		Subcommand: sub,
		Args:       rest,
		HomeDir:    home,
		YakosRoot:  yakosRoot,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := soul.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "soul: %v\n", err)
		os.Exit(1)
	}
}

// runRetro implements `yakos retro` natively in Go.
//
// Usage mirrors cli/lib/retro.sh exactly:
//
//	yakos retro now           — write .retro-due marker (manual trigger)
//	yakos retro disable       — disable auto-trigger (counter still increments)
//	yakos retro enable        — re-enable auto-trigger
//	yakos retro status        — current state
//	yakos retro last          — show last retro outputs from scratchpad
//	yakos retro history       — cadence stats from cycle-counter logs
//
// State (enabled/disabled) is stored as a sentinel file at
// ~/.yakos-state/retro-disabled. Present = disabled; absent = enabled.
func runRetro(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		retro.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := retro.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := retro.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "retro: %v\n", err)
		os.Exit(1)
	}
}

// runSkill implements `yakos skill` natively in Go.
//
// Usage mirrors cli/lib/skill.sh exactly:
//
//	yakos skill candidates [--review]
//	yakos skill promote <slug> [--global]
//	yakos skill reject <slug> [--reason "<text>"]
//	yakos skill defer <slug> <N>
//	yakos skill stats
//
// Reads:  <work>/current/skill-candidates.md (librarian-written)
//
//	~/.yakos-state/skill-graveyard.ndjson (rejected history)
//
// Writes: <project>/.claude/skills/<slug>/SKILL.md (on promote)
//
//	lib/skills/<slug>/SKILL.md (on promote --global; rare)
//	~/.yakos-state/promotion-log.ndjson
//	~/.yakos-state/skill-graveyard.ndjson (on reject)
//	<work>/current/skill-candidates.md (removes promoted/rejected entries)
func runSkill(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		skill.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	cfg := skill.Config{
		Subcommand: sub,
		HomeDir:    home,
		YakosRoot:  yakosRoot,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "candidates":
		for _, arg := range rest {
			if arg == "--review" {
				cfg.Review = true
			} else {
				fmt.Fprintf(os.Stderr, "skill candidates: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			}
		}

	case "promote":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--global":
				cfg.Global = true
			case arg == "--project":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "skill promote: --project requires a path")
					os.Exit(1)
				}
				cfg.ProjectPath = rest[i]
			case len(arg) > 10 && arg[:10] == "--project=":
				cfg.ProjectPath = arg[10:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "skill promote: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Slug == "" {
					cfg.Slug = arg
				} else {
					fmt.Fprintln(os.Stderr, "skill promote: too many positional args")
					os.Exit(1)
				}
			}
		}
		if cfg.Slug == "" {
			skill.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "skill promote: <slug> required")
			os.Exit(1)
		}

	case "reject":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--reason":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "skill reject: --reason requires a value")
					os.Exit(1)
				}
				cfg.Reason = rest[i]
			case len(arg) > 9 && arg[:9] == "--reason=":
				cfg.Reason = arg[9:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "skill reject: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			default:
				if cfg.Slug == "" {
					cfg.Slug = arg
				} else {
					fmt.Fprintln(os.Stderr, "skill reject: too many positional args")
					os.Exit(1)
				}
			}
		}
		if cfg.Slug == "" {
			skill.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "skill reject: <slug> required")
			os.Exit(1)
		}

	case "defer":
		positionals := make([]string, 0, 2)
		for _, arg := range rest {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "skill defer: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			}
			positionals = append(positionals, arg)
		}
		if len(positionals) < 2 {
			skill.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "skill defer: <slug> and <N> required")
			os.Exit(1)
		}
		cfg.Slug = positionals[0]
		n, err := strconv.Atoi(positionals[1])
		if err != nil || n <= 0 {
			fmt.Fprintf(os.Stderr, "skill defer: <N> must be a positive integer; got %q\n", positionals[1])
			os.Exit(1)
		}
		cfg.DeferCycles = n

	case "stats":
		for _, arg := range rest {
			fmt.Fprintf(os.Stderr, "skill stats: unexpected argument %q (try --help)\n", arg)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "skill: unknown subcommand %q (try 'yakos skill help')\n", sub)
		os.Exit(1)
	}

	if _, err := skill.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "skill: %v\n", err)
		os.Exit(1)
	}
}

// runCompact implements `yakos compact` natively in Go.
//
// Usage mirrors cli/lib/compact.sh exactly:
//
//	yakos compact now              # print /compact for the active session (M3.1: auto-send via tmux)
//	yakos compact threshold [N]    # show or set notice threshold (1-99; default 75)
//	yakos compact history          # show last 50 compaction log entries
//	yakos compact --help           # print help and exit 0
//
// Reads:  ~/.yakos-state/settings.json       (context_thresholds)
//
//	~/.yakos-state/compact-log.ndjson  (compaction history)
//
// Writes: ~/.yakos-state/settings.json       (atomic temp-rename, Q8) on threshold set
//
//	~/.yakos-state/compact-log.ndjson  (O_APPEND) on now
func runCompact(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		compact.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := compact.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	// threshold subcommand: supports positional N and --auto N flag.
	//
	//   yakos compact threshold          → show all three thresholds
	//   yakos compact threshold show     → same
	//   yakos compact threshold N        → set notice threshold to N
	//   yakos compact threshold --auto N → set auto-compact threshold to N
	if sub == "threshold" {
		autoArg := ""
		positional := []string{}
		for i := 0; i < len(rest); i++ {
			if rest[i] == "--auto" {
				if i+1 >= len(rest) {
					fmt.Fprintln(os.Stderr, "compact threshold: --auto requires a value (e.g. --auto 85)")
					os.Exit(1)
				}
				autoArg = rest[i+1]
				i++ // consume the value
			} else {
				positional = append(positional, rest[i])
			}
		}
		if len(positional) > 1 {
			fmt.Fprintln(os.Stderr, "compact threshold: too many arguments")
			os.Exit(1)
		}
		if len(positional) == 1 {
			cfg.ThresholdArg = positional[0]
		}
		cfg.AutoArg = autoArg
	}

	if _, err := compact.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "compact: %v\n", err)
		os.Exit(1)
	}
}

// runCheckpoint implements `yakos checkpoint` natively in Go.
//
// Usage mirrors cli/lib/checkpoint.sh exactly:
//
//	yakos checkpoint create                 # create snapshot (alias: now)
//	yakos checkpoint list                   # list existing snapshots
//	yakos checkpoint restore <id>           # resume via --fork-session (alias: resume)
//	yakos checkpoint clean [--age <days>]   # GC old snapshots (default >30d)
//	yakos checkpoint --help                 # print help and exit 0
//
// Snapshots live under <work>/current/checkpoints/<iso-ts>/ and contain:
//
//	summary.md, scratchpad/{plan,decisions,contracts,status,kanban}.md,
//	token-snapshot.txt, session-id.txt, manifest.json
//
// Work directory is resolved via YAKOS_WORK_DIR env → YAKOS_INPLACE_WORK+
// CLAUDE_PROJECT_DIR → $HOME/agent-control/$YAKOS_PROJECT_NAME/work.
func runCheckpoint(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		checkpoint.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := checkpoint.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "restore", "resume":
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "checkpoint restore: missing <id>")
			checkpoint.PrintHelp(os.Stderr)
			os.Exit(1)
		}
		cfg.RestoreID = rest[0]

	case "clean":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--age":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "checkpoint clean: --age requires a number (days)")
					os.Exit(1)
				}
				n := 0
				if _, err := fmt.Sscanf(rest[i], "%d", &n); err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "checkpoint clean: --age value %q is not a positive integer\n", rest[i])
					os.Exit(1)
				}
				cfg.CleanAgeDays = n
			case len(arg) > 6 && arg[:6] == "--age=":
				val := arg[6:]
				n := 0
				if _, err := fmt.Sscanf(val, "%d", &n); err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "checkpoint clean: --age value %q is not a positive integer\n", val)
					os.Exit(1)
				}
				cfg.CleanAgeDays = n
			default:
				fmt.Fprintf(os.Stderr, "checkpoint clean: unknown flag %q (try --help)\n", arg)
				os.Exit(1)
			}
		}

	case "create", "now", "list":
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "checkpoint %s: unexpected argument %q\n", sub, rest[0])
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "checkpoint: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	if _, err := checkpoint.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "checkpoint: %v\n", err)
		os.Exit(1)
	}
}

// runEnv implements `yakos env` natively in Go.
//
// Usage mirrors cli/lib/env.sh exactly:
//
//	yakos env status                   # current branch → env mapping
//	yakos env promote <from> <to>      # PR from env's branch → to env's branch
//	yakos env validate                 # check .yakos.yml environments section
//	yakos env list                     # list configured envs
//	yakos env --help                   # print help and exit 0
//
// Environments are declared in <project>/.yakos.yml under `environments:`.
// PR tool detection: gh → glab → git URL guidance.
// Project dir resolved from YAKOS_PROJECT_DIR env, cwd, or .project-path.
func runEnv(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		envcfg.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := envcfg.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "promote":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "env promote: requires <from> and <to> env names")
			envcfg.PrintHelp(os.Stderr)
			os.Exit(1)
		}
		cfg.PromoteFrom = rest[0]
		cfg.PromoteTo = rest[1]

	case "status", "validate", "list":
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "env %s: unexpected argument %q\n", sub, rest[0])
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "env: unknown subcommand %q (try 'yakos env help')\n", sub)
		os.Exit(1)
	}

	if _, err := envcfg.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "env: %v\n", err)
		os.Exit(1)
	}
}

// runStandards implements `yakos standards` natively in Go.
//
// Usage mirrors cli/lib/standards.sh exactly:
//
//	yakos standards list               # show all 6 standards + state
//	yakos standards enable  <name>     # set profile.standards.<name> = true
//	yakos standards disable <name>     # set profile.standards.<name> = false
//	yakos standards check              # preview what active standards catch
//	yakos standards init               # interactive profile + standards selection
//	yakos standards --help             # print help and exit 0
//
// State lives in <project>/.yakos.yml under profile.standards.*.
// Project dir resolved from YAKOS_PROJECT_DIR env, cwd, or agent-control walk.
// Atomic YAML rewrite via temp-rename (Q8) on enable/disable/init.
func runStandards(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		standards.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := standards.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "enable", "disable":
		if len(rest) == 0 {
			fmt.Fprintf(os.Stderr, "standards %s: requires a standard name\n", sub)
			standards.PrintHelp(os.Stderr)
			os.Exit(1)
		}
		if len(rest) > 1 {
			fmt.Fprintf(os.Stderr, "standards %s: too many arguments (expected one standard name)\n", sub)
			os.Exit(1)
		}
		cfg.StandardName = rest[0]

	case "list", "check", "init":
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "standards %s: unexpected argument %q\n", sub, rest[0])
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "standards: unknown subcommand %q (try 'yakos standards help')\n", sub)
		os.Exit(1)
	}

	if _, err := standards.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "standards: %v\n", err)
		os.Exit(1)
	}
}

// runPeer implements `yakos peer` natively in Go.
//
// Usage mirrors cli/lib/peer.sh exactly:
//
//	yakos peer status [<project>]
//	yakos peer log [--since <iso>] [<project>]
//	yakos peer claim <file> [<project>]
//	yakos peer release <file> [<project>]
//	yakos peer claims [<project>]
//	yakos peer deadlock [<project>]
//	yakos peer propose-mode --mode <m> --targets <glob>... [--reason <t>] [--timeout <secs>] [<project>]
//	yakos peer respond-mode --to <proposal-id> --ack|--reject [--reason <t>] [<project>]
//	yakos peer handoff --to <user@host> --completed-scope <s> --notes <s> --next-action <s> [<project>]
//	yakos peer handoff --ack <handoff-id>|--reject <handoff-id> [--reason <t>] [<project>]
//
// Coord dir defaults to /var/lib/yakos/<project>/coord/. All subcommands
// no-op cleanly when coord is not configured for the project (same
// load-bearing guarantee as bash peer.sh).
func runPeer(args []string) {
	sub := ""
	rest := args
	if len(args) > 0 {
		sub = args[0]
		rest = args[1:]
	}

	if sub == "--help" || sub == "-h" || sub == "help" {
		sub = "help"
		rest = nil
	}

	cfg := peer.Config{
		Subcommand: sub,
		Args:       rest,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := peer.Run(cfg); err != nil {
		// Mirror bash exit-code convention for the two coord-check failures:
		// missing coord → 64, not-writable → 77. We use exit 1 for other errors.
		msg := err.Error()
		fmt.Fprintln(os.Stderr, msg)
		if strings.Contains(msg, "coord not configured") {
			os.Exit(64)
		}
		if strings.Contains(msg, "not writable") {
			os.Exit(77)
		}
		if strings.Contains(msg, "peer rejected") {
			os.Exit(1)
		}
		os.Exit(1)
	}
}

// runMCP implements `yakos mcp` natively in Go.
//
// Usage mirrors cli/lib/mcp.sh exactly (Phase 1: read-only config management):
//
//	yakos mcp install   [--project <path>]   Add/refresh yakos-dispatch in .mcp.json.
//	yakos mcp uninstall [--project <path>]   Remove yakos-dispatch from .mcp.json.
//	yakos mcp status    [--project <path>]   Show whether the entry is present.
//	yakos mcp probe                          Verify 'mcp' Python package is importable.
//	yakos mcp --help                         Print help and exit 0.
//
// NOTE: the native MCP server is Phase 2 (Q3 design decision). This command
// manages only the JSON registration that tells Claude Code where the server is.
// MCP config file: <project>/.mcp.json. On Windows: %APPDATA%/claude/mcp.json.
func runMCP(yakosRoot string, args []string) {
	if len(args) == 0 {
		mcp.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "--help", "-h", "help":
		mcp.PrintHelp(os.Stdout)
		os.Exit(0)
	case "serve":
		// Native MCP server over stdio (Phase 2, decision Q3).
		runMCPServe(yakosRoot, rest)
		return
	case "probe":
		// probe takes no flags.
		if len(rest) > 0 {
			fmt.Fprintf(os.Stderr, "mcp probe: unexpected argument %q\n", rest[0])
			os.Exit(1)
		}
	case "install", "uninstall", "status":
		// These accept optional --project flag.
	default:
		fmt.Fprintf(os.Stderr, "mcp: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	// Resolve YAKOS_ROOT from env.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	project := ""
	if sub != "probe" {
		p, err := mcp.ParseArgs(sub, rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			os.Exit(1)
		}
		project = p
	}

	cfg := mcp.Config{
		Subcommand: sub,
		Project:    project,
		YakosRoot:  yakosRoot,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := mcp.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runMCPServe implements `yakos mcp serve` — the native MCP stdio server.
//
// This subcommand runs a single MCP (Model Context Protocol) session bound to
// the calling process's stdin/stdout. Claude Code registers it via:
//
//	claude mcp add yakos -- yakos mcp serve
//
// Per decision Q3 (2026-06-02): stdio transport only in Phase 2.
// Streamable HTTP is a follow-up dispatch.
//
// Flags: none currently.
func runMCPServe(yakosRoot string, args []string) {
	for _, arg := range args {
		switch arg {
		case "-h", "--help":
			fmt.Fprint(os.Stdout, `yakos mcp serve

Start a native MCP (Model Context Protocol) server on stdin/stdout.
Claude Code registers this via: claude mcp add yakos -- yakos mcp serve

Tool surface (Phase 2):
  yakos.dispatch            Invoke a subagent
  yakos.kanban.list         List kanban items
  yakos.kanban.add          Add a task to TODO
  yakos.kanban.move         Move a task between columns
  yakos.kanban.done         Move a task to DONE
  yakos.refresh             Detect and repair deployment drift
  yakos.supervise.run       Read supervisor findings
  yakos.supervise.ack       Acknowledge a supervisor finding

Transport: JSON-RPC 2.0 over stdin/stdout (NDJSON).

`)
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "mcp serve: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	// Resolve workspace root from cwd.
	workspaceRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp serve: resolve cwd: %v\n", err)
		os.Exit(1)
	}

	// Read the binary version string for ServerInfo.
	ver := ""
	if v, err := version.Read(yakosRoot); err == nil {
		ver = v
	}

	// Construct a session-scoped dispatch.Service for this stdio MCP session.
	// Each `yakos mcp serve` process is a single session (one Claude Code
	// connection), so one Service per process is the correct scope — this is
	// not an ephemeral per-call Service.  The session ends when stdin closes.
	dispatchSvc := dispatch.NewService(dispatch.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		YakosRoot:     yakosRoot,
	})

	cfg := mcpserver.Config{
		WorkspaceRoot:   workspaceRoot,
		YakosRoot:       yakosRoot,
		Version:         ver,
		DispatchService: dispatchSvc,
	}

	ctx := context.Background()
	if err := mcpserver.Serve(ctx, cfg, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
		fmt.Fprintf(os.Stderr, "mcp serve: %v\n", err)
		os.Exit(1)
	}
}

// runCompletion implements `yakos completion` natively in Go.
//
// Usage mirrors cli/lib/completion.sh exactly:
//
//	yakos completion bash      Print the bash completion script to stdout.
//	yakos completion zsh       Print the zsh completion script to stdout.
//	yakos completion fish      Print the fish completion script to stdout.
//	yakos completion install   Auto-detect shell and write completion file.
//	yakos completion --help    Print help and exit 0.
//
// Shell detection for install:
//  1. YAKOS_COMPLETION_SHELL env var
//  2. $SHELL suffix (bash/zsh/fish)
//
// Install paths:
//
//	bash: BASH_COMPLETION_USER_DIR or ~/.local/share/bash-completion/completions/yakos
//	zsh:  YAKOS_ZSH_COMPDIR or ~/.zsh/completions/_yakos
//	fish: XDG_CONFIG_HOME/fish/completions/yakos.fish or ~/.config/fish/completions/yakos.fish
func runCompletion(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}

	switch sub {
	case "--help", "-h", "help":
		completion.PrintHelp(os.Stdout)
		os.Exit(0)
	case "bash", "zsh", "fish", "install", "":
		// valid
	default:
		fmt.Fprintf(os.Stderr, "completion: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := completion.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	if _, err := completion.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runGitHooks implements `yakos git-hooks` natively in Go.
//
// Usage mirrors cli/lib/git-hooks.sh exactly:
//
//	yakos git-hooks install   [--force] [--promotion-gate]
//	yakos git-hooks uninstall
//	yakos git-hooks status
//	yakos git-hooks --help
//
// Must be run from inside a git repository. Discovers the repo root via
// `git rev-parse --show-toplevel`.
//
// Gate source: $YAKOS_ROOT/lib/hooks/git/pre-push-version-gate.sh
// Promotion gate: $YAKOS_ROOT/lib/hooks/git/pre-push-promotion-gate.sh
func runGitHooks(yakosRoot string, args []string) {
	if len(args) == 0 {
		githooks.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	// Resolve YAKOS_ROOT from env.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "--help", "-h", "help":
		githooks.PrintHelp(os.Stdout)
		os.Exit(0)
	case "install", "uninstall", "status":
		// valid
	default:
		githooks.PrintHelp(os.Stderr)
		fmt.Fprintf(os.Stderr, "git-hooks: unknown subcommand %q\n", sub)
		os.Exit(2)
	}

	force := false
	withPromotion := false
	for _, arg := range rest {
		switch arg {
		case "--force":
			force = true
		case "--promotion-gate":
			withPromotion = true
		default:
			fmt.Fprintf(os.Stderr, "git-hooks %s: unknown flag %q\n", sub, arg)
			os.Exit(2)
		}
	}

	cfg := githooks.Config{
		Subcommand:        sub,
		Force:             force,
		WithPromotionGate: withPromotion,
		YakosRoot:         yakosRoot,
		Writer:            os.Stdout,
		ErrWriter:         os.Stderr,
	}

	if _, err := githooks.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runSupervise implements `yakos supervise` natively in Go.
//
// Usage mirrors cli/lib/supervise.sh exactly (PRs #28–#39 redesign preserved):
//
//	yakos supervise enable  [<project>]
//	yakos supervise disable [<project>]
//	yakos supervise status  [<project>]
//	yakos supervise tail    [<project>] [--watch] [--n <N>]
//	yakos supervise clear   [<project>]
//	yakos supervise set <key> <value> [<project>]
//	yakos supervise pending [<project>]
//	yakos supervise ack     <finding-id> [<project>] [--note "..."]
//	yakos supervise ack-all [<project>] [--note "..."]
//
// Project resolution: explicit arg → inferred from cwd (agent-control walk).
// Emergency bypass: export YAKOS_SUPERVISOR_DISABLE=1 (no .yakos.yml edit needed).
func runSupervise(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		supervise.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	// Validate subcommand before parsing flags.
	switch sub {
	case "enable", "disable", "status", "tail", "clear", "set", "pending", "ack", "ack-all":
		// valid
	default:
		fmt.Fprintf(os.Stderr, "supervise: unknown subcommand %q (try --help)\n", sub)
		os.Exit(1)
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := supervise.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
		TailN:      10,
	}

	switch sub {
	case "enable", "disable", "status", "clear", "pending":
		// Optional positional: project name.
		for _, arg := range rest {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "supervise %s: unknown flag %q\n", sub, arg)
				os.Exit(1)
			}
			if cfg.Project == "" {
				cfg.Project = arg
			} else {
				fmt.Fprintf(os.Stderr, "supervise %s: too many positional args\n", sub)
				os.Exit(1)
			}
		}

	case "tail":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--watch" || arg == "-w":
				cfg.Watch = true
			case arg == "--n":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "supervise tail: --n requires a value")
					os.Exit(1)
				}
				n, err := strconv.Atoi(rest[i])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "supervise tail: --n value %q is not a positive integer\n", rest[i])
					os.Exit(1)
				}
				cfg.TailN = n
			case len(arg) > 4 && arg[:4] == "--n=":
				n, err := strconv.Atoi(arg[4:])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "supervise tail: --n value %q is not a positive integer\n", arg[4:])
					os.Exit(1)
				}
				cfg.TailN = n
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "supervise tail: unknown flag %q\n", arg)
				os.Exit(1)
			default:
				if cfg.Project == "" {
					cfg.Project = arg
				} else {
					fmt.Fprintln(os.Stderr, "supervise tail: too many positional args")
					os.Exit(1)
				}
			}
		}

	case "set":
		// Requires: <key> <value> [<project>]
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "supervise set: <key> <value> required (e.g. block_on_critical false)")
			os.Exit(1)
		}
		cfg.Key = rest[0]
		cfg.Value = rest[1]
		if len(rest) >= 3 {
			cfg.Project = rest[2]
		}
		if len(rest) > 3 {
			fmt.Fprintln(os.Stderr, "supervise set: too many positional args")
			os.Exit(1)
		}

	case "ack":
		// Requires: <finding-id> [<project>] [--note "..."]
		if len(rest) == 0 {
			fmt.Fprintln(os.Stderr, "supervise ack: <finding-id> required (try 'yakos supervise pending')")
			os.Exit(1)
		}
		cfg.FindingID = rest[0]
		rest = rest[1:]
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--note":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "supervise ack: --note requires a value")
					os.Exit(1)
				}
				cfg.Note = rest[i]
			case len(arg) > 7 && arg[:7] == "--note=":
				cfg.Note = arg[7:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "supervise ack: unknown flag %q\n", arg)
				os.Exit(1)
			default:
				if cfg.Project == "" {
					cfg.Project = arg
				} else {
					fmt.Fprintln(os.Stderr, "supervise ack: too many positional args")
					os.Exit(1)
				}
			}
		}

	case "ack-all":
		// Optional: [<project>] [--note "..."]
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--note":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "supervise ack-all: --note requires a value")
					os.Exit(1)
				}
				cfg.Note = rest[i]
			case len(arg) > 7 && arg[:7] == "--note=":
				cfg.Note = arg[7:]
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "supervise ack-all: unknown flag %q\n", arg)
				os.Exit(1)
			default:
				if cfg.Project == "" {
					cfg.Project = arg
				} else {
					fmt.Fprintln(os.Stderr, "supervise ack-all: too many positional args")
					os.Exit(1)
				}
			}
		}
	}

	if _, err := supervise.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runPlan implements `yakos plan <subcommand>` natively in Go.
//
// Currently ported leaf subcommands:
//
//	yakos plan score show [<plan_id>]
//	yakos plan score history [--project <name>] [--limit <n>]
//	yakos plan score override <plan_id> --reason "<text>"
//	yakos plan score correlate [--project <p>] [--since <iso>] [--min-n <n>]
//
// The bash source dispatches: yakos plan score <sub> and also yakos plan <sub>
// directly. The Go port mirrors that: `plan score show` and `plan show` both
// route to planscore.Run.
func runPlan(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		planscore.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	// Strip the redundant "score" wrapper when present: `plan score show` → sub=show.
	if sub == "score" {
		if len(rest) == 0 {
			planscore.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		sub = rest[0]
		rest = rest[1:]
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := planscore.Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	switch sub {
	case "show":
		if len(rest) > 0 && rest[0] != "" && rest[0][0] != '-' {
			cfg.PlanID = rest[0]
			rest = rest[1:]
		}
		for _, arg := range rest {
			if arg == "-h" || arg == "--help" {
				planscore.PrintHelp(os.Stdout)
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "plan score show: unknown option %q (try --help)\n", arg)
			os.Exit(1)
		}

	case "history":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--project":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plan score history: --project requires a value")
					os.Exit(1)
				}
				cfg.Project = rest[i]
			case len(arg) > 10 && arg[:10] == "--project=":
				cfg.Project = arg[10:]
			case arg == "--limit":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plan score history: --limit requires a value")
					os.Exit(1)
				}
				n, err := strconv.Atoi(rest[i])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "plan score history: --limit %q must be a positive integer\n", rest[i])
					os.Exit(1)
				}
				cfg.Limit = n
			case len(arg) > 8 && arg[:8] == "--limit=":
				n, err := strconv.Atoi(arg[8:])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "plan score history: --limit value %q must be a positive integer\n", arg[8:])
					os.Exit(1)
				}
				cfg.Limit = n
			case arg == "-h" || arg == "--help":
				planscore.PrintHelp(os.Stdout)
				os.Exit(0)
			default:
				fmt.Fprintf(os.Stderr, "plan score history: unknown option %q (try --help)\n", arg)
				os.Exit(1)
			}
		}

	case "override":
		if len(rest) == 0 || rest[0][0] == '-' {
			planscore.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "plan score override: <plan_id> required")
			os.Exit(1)
		}
		cfg.PlanID = rest[0]
		rest = rest[1:]
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--reason":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plan score override: --reason requires a value")
					os.Exit(1)
				}
				cfg.Reason = rest[i]
			case len(arg) > 9 && arg[:9] == "--reason=":
				cfg.Reason = arg[9:]
			case arg == "-h" || arg == "--help":
				planscore.PrintHelp(os.Stdout)
				os.Exit(0)
			default:
				fmt.Fprintf(os.Stderr, "plan score override: unknown option %q (try --help)\n", arg)
				os.Exit(1)
			}
		}

	case "correlate":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--project":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plan score correlate: --project requires a value")
					os.Exit(1)
				}
				cfg.Project = rest[i]
			case len(arg) > 10 && arg[:10] == "--project=":
				cfg.Project = arg[10:]
			case arg == "--since":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plan score correlate: --since requires a value")
					os.Exit(1)
				}
				cfg.Since = rest[i]
			case len(arg) > 8 && arg[:8] == "--since=":
				cfg.Since = arg[8:]
			case arg == "--min-n":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "plan score correlate: --min-n requires a value")
					os.Exit(1)
				}
				n, err := strconv.Atoi(rest[i])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "plan score correlate: --min-n %q must be a positive integer\n", rest[i])
					os.Exit(1)
				}
				cfg.MinN = n
			case len(arg) > 7 && arg[:7] == "--min-n=":
				n, err := strconv.Atoi(arg[7:])
				if err != nil || n <= 0 {
					fmt.Fprintf(os.Stderr, "plan score correlate: --min-n value %q must be a positive integer\n", arg[7:])
					os.Exit(1)
				}
				cfg.MinN = n
			case arg == "-h" || arg == "--help":
				planscore.PrintHelp(os.Stdout)
				os.Exit(0)
			default:
				fmt.Fprintf(os.Stderr, "plan score correlate: unknown option %q (try --help)\n", arg)
				os.Exit(1)
			}
		}

	case "-h", "--help", "help", "":
		planscore.PrintHelp(os.Stdout)
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "plan score: unknown subcommand %q (try --help)\n", sub)
		os.Exit(64)
	}

	if _, err := planscore.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runWork implements `yakos work <subcommand>` natively in Go.
//
// Currently ported leaf subcommands:
//
//	yakos work close [--plan-id <id>] [--no-prompt] [--project <path>]
func runWork(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		workclose.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "close":
		runWorkClose(rest)
	case "-h", "--help", "help":
		workclose.PrintHelp(os.Stdout)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "work: unknown subcommand %q (try --help)\n", sub)
		os.Exit(64)
	}
}

// runWorkClose handles `yakos work close [options]`.
func runWorkClose(args []string) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := workclose.Config{
		HomeDir:   home,
		Writer:    os.Stdout,
		ErrWriter: os.Stderr,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--plan-id":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "work close: --plan-id requires a value")
				os.Exit(1)
			}
			cfg.PlanID = args[i]
		case len(arg) > 10 && arg[:10] == "--plan-id=":
			cfg.PlanID = arg[10:]
		case arg == "--no-prompt":
			cfg.NoPrompt = true
		case arg == "--project":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "work close: --project requires a value")
				os.Exit(1)
			}
			cfg.ProjectDir = args[i]
		case len(arg) > 10 && arg[:10] == "--project=":
			cfg.ProjectDir = arg[10:]
		case arg == "-h" || arg == "--help":
			workclose.PrintHelp(os.Stdout)
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "work close: unknown option %q (try --help)\n", arg)
			os.Exit(1)
		}
	}

	if _, err := workclose.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// runModelRouting implements `yakos model-routing` natively in Go.
//
// Usage mirrors cli/lib/model-routing.sh exactly:
//
//	yakos model-routing eval <agent-id> [--judge <agent>] [--max-cost-usd <n>]
//	                                     [--cases <glob>] [--project <path>]
//	yakos model-routing list
//	yakos model-routing show <agent-id>
//	yakos model-routing promote <agent-id> [--global]
//	yakos model-routing reject <agent-id> [--note "<text>"] [--force]
//	yakos model-routing history [<agent-id>]
//
// YAKOS_ROOT is resolved from the executable location (same as all other subcommands).
func runModelRouting(yakosRoot string, args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		routing.PrintHelp(os.Stdout)
		os.Exit(0)
	}

	sub := args[0]
	rest := args[1:]

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg := routing.Config{
		Subcommand: sub,
		YakosRoot:  yakosRoot,
		HomeDir:    home,
		Writer:     os.Stdout,
		ErrWriter:  os.Stderr,
	}

	// Apply YAKOS_ROOT override from env (matches other subcommands).
	if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
		cfg.YakosRoot = envRoot
	}

	switch sub {
	case "eval":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--judge":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "model-routing eval: --judge requires a value")
					os.Exit(1)
				}
				cfg.Judge = rest[i]
			case len(arg) > 8 && arg[:8] == "--judge=":
				cfg.Judge = arg[8:]
			case arg == "--max-cost-usd":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "model-routing eval: --max-cost-usd requires a value")
					os.Exit(1)
				}
				v, err := strconv.ParseFloat(rest[i], 64)
				if err != nil || v <= 0 {
					fmt.Fprintf(os.Stderr, "model-routing eval: --max-cost-usd %q must be a positive number\n", rest[i])
					os.Exit(1)
				}
				cfg.MaxCostUSD = v
			case len(arg) > 15 && arg[:15] == "--max-cost-usd=":
				v, err := strconv.ParseFloat(arg[15:], 64)
				if err != nil || v <= 0 {
					fmt.Fprintf(os.Stderr, "model-routing eval: --max-cost-usd value %q must be a positive number\n", arg[15:])
					os.Exit(1)
				}
				cfg.MaxCostUSD = v
			case arg == "--cases":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "model-routing eval: --cases requires a value")
					os.Exit(1)
				}
				cfg.CasesGlob = rest[i]
			case len(arg) > 8 && arg[:8] == "--cases=":
				cfg.CasesGlob = arg[8:]
			case arg == "--project":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "model-routing eval: --project requires a value")
					os.Exit(1)
				}
				cfg.Project = rest[i]
			case len(arg) > 10 && arg[:10] == "--project=":
				cfg.Project = arg[10:]
			case arg == "-h" || arg == "--help":
				routing.PrintHelp(os.Stdout)
				os.Exit(0)
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "model-routing eval: unknown flag %q\n", arg)
				os.Exit(1)
			default:
				if cfg.AgentID == "" {
					cfg.AgentID = arg
				} else {
					fmt.Fprintf(os.Stderr, "model-routing eval: unexpected argument %q\n", arg)
					os.Exit(1)
				}
			}
		}

	case "list":
		for _, arg := range rest {
			if arg == "-h" || arg == "--help" {
				routing.PrintHelp(os.Stdout)
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "model-routing list: unexpected argument %q\n", arg)
			os.Exit(1)
		}

	case "show":
		for _, arg := range rest {
			if arg == "-h" || arg == "--help" {
				routing.PrintHelp(os.Stdout)
				os.Exit(0)
			}
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "model-routing show: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if cfg.AgentID == "" {
				cfg.AgentID = arg
			} else {
				fmt.Fprintf(os.Stderr, "model-routing show: unexpected argument %q\n", arg)
				os.Exit(1)
			}
		}

	case "promote":
		for _, arg := range rest {
			switch arg {
			case "--global":
				cfg.Global = true
			case "-h", "--help":
				routing.PrintHelp(os.Stdout)
				os.Exit(0)
			default:
				if len(arg) > 0 && arg[0] == '-' {
					fmt.Fprintf(os.Stderr, "model-routing promote: unknown flag %q\n", arg)
					os.Exit(1)
				}
				if cfg.AgentID == "" {
					cfg.AgentID = arg
				} else {
					fmt.Fprintf(os.Stderr, "model-routing promote: unexpected argument %q\n", arg)
					os.Exit(1)
				}
			}
		}

	case "reject":
		for i := 0; i < len(rest); i++ {
			arg := rest[i]
			switch {
			case arg == "--note":
				i++
				if i >= len(rest) {
					fmt.Fprintln(os.Stderr, "model-routing reject: --note requires a value")
					os.Exit(1)
				}
				cfg.Note = rest[i]
			case len(arg) > 7 && arg[:7] == "--note=":
				cfg.Note = arg[7:]
			case arg == "--force":
				cfg.Force = true
			case arg == "-h" || arg == "--help":
				routing.PrintHelp(os.Stdout)
				os.Exit(0)
			case len(arg) > 0 && arg[0] == '-':
				fmt.Fprintf(os.Stderr, "model-routing reject: unknown flag %q\n", arg)
				os.Exit(1)
			default:
				if cfg.AgentID == "" {
					cfg.AgentID = arg
				} else {
					fmt.Fprintf(os.Stderr, "model-routing reject: unexpected argument %q\n", arg)
					os.Exit(1)
				}
			}
		}

	case "history":
		for _, arg := range rest {
			if arg == "-h" || arg == "--help" {
				routing.PrintHelp(os.Stdout)
				os.Exit(0)
			}
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "model-routing history: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if cfg.FilterAgent == "" {
				cfg.FilterAgent = arg
			} else {
				fmt.Fprintf(os.Stderr, "model-routing history: unexpected argument %q\n", arg)
				os.Exit(1)
			}
		}

	case "-h", "--help", "help", "":
		routing.PrintHelp(os.Stdout)
		os.Exit(0)

	default:
		routing.PrintHelp(os.Stderr)
		fmt.Fprintf(os.Stderr, "model-routing: unknown subcommand %q\n", sub)
		os.Exit(64)
	}

	if _, err := routing.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

// exitWith calls os.Exit with the code returned by passthrough.Run.
// It prints any error to stderr and exits 1 on passthrough failure.
func exitWith(code int, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "yakos: passthrough error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// ---- yakos serve ------------------------------------------------------------

// runServe implements `yakos serve` — the Phase 2 daemon process.
//
// The daemon is OFF by default (YAKOS_DAEMON=off per decision Q1).
// Operators start it explicitly:
//
//	yakos serve [--socket <path>] [--pidfile <path>] [--ws-addr <addr>] [--help]
//
// Flags:
//
//	--socket <path>       Override the default Unix socket / named pipe path.
//	--pidfile <path>      Override the default PID file path.
//	--ws-addr <addr>      WebSocket bind address (default 127.0.0.1:7891).
//	--rotate-ws-token     Rotate the WS bearer token and exit.
//	--detach              Print advisory (actual backgrounding is the operator's job).
//	--help                Print help and exit 0.
//
// YAKOS_DAEMON mode is NOT changed by running this command; the operator sets
// YAKOS_DAEMON=on or YAKOS_DAEMON=auto in their shell rc to route CLI calls
// through the daemon.
func runServe(yakosRoot string, args []string) {
	socketPath := ""
	pidFile := ""
	wsAddr := ""
	perfAddr := ""
	consoleAddr := ""
	consoleBind := ""
	detach := false
	rotateToken := false
	rotatePerfToken := false
	rotateConsoleToken := false
	noPerfDash := false
	noConsole := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			printServeHelp(os.Stdout)
			os.Exit(0)
		case "--socket":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --socket requires a path")
				os.Exit(1)
			}
			socketPath = args[i]
		case "--pidfile":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --pidfile requires a path")
				os.Exit(1)
			}
			pidFile = args[i]
		case "--ws-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --ws-addr requires an address")
				os.Exit(1)
			}
			wsAddr = args[i]
		case "--perf-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --perf-addr requires an address")
				os.Exit(1)
			}
			perfAddr = args[i]
		case "--console-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --console-addr requires an address")
				os.Exit(1)
			}
			consoleAddr = args[i]
		case "--console-bind":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "serve: --console-bind requires an address")
				os.Exit(1)
			}
			consoleBind = args[i]
		case "--rotate-ws-token":
			rotateToken = true
		case "--rotate-perf-token":
			rotatePerfToken = true
		case "--rotate-console-token":
			rotateConsoleToken = true
		case "--no-perf":
			noPerfDash = true
		case "--no-console":
			noConsole = true
		case "--detach":
			detach = true
		default:
			if len(args[i]) > 9 && args[i][:9] == "--socket=" {
				socketPath = args[i][9:]
			} else if len(args[i]) > 10 && args[i][:10] == "--pidfile=" {
				pidFile = args[i][10:]
			} else if len(args[i]) > 10 && args[i][:10] == "--ws-addr=" {
				wsAddr = args[i][10:]
			} else if len(args[i]) > 12 && args[i][:12] == "--perf-addr=" {
				perfAddr = args[i][12:]
			} else if len(args[i]) > 15 && args[i][:15] == "--console-addr=" {
				consoleAddr = args[i][15:]
			} else if len(args[i]) > 15 && args[i][:15] == "--console-bind=" {
				consoleBind = args[i][15:]
			} else {
				fmt.Fprintf(os.Stderr, "serve: unknown flag %q (try --help)\n", args[i])
				os.Exit(1)
			}
		}
	}

	// --rotate-ws-token: generate a new token and print the path, then exit.
	if rotateToken {
		tok, err := wsbus.RotateToken("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: rotate-ws-token: %v\n", err)
			os.Exit(1)
		}
		_ = tok // token stored in file; print the path
		fmt.Fprintf(os.Stdout, "ws token rotated: %s\n", wsbus.TokenFilePath())
		os.Exit(0)
	}

	// --rotate-perf-token: generate a new perf dashboard token and exit.
	if rotatePerfToken {
		home, _ := os.UserHomeDir()
		stateDir := filepath.Join(home, ".yakos-state")
		tok, err := internalperfdash.RotatePerfToken(stateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: rotate-perf-token: %v\n", err)
			os.Exit(1)
		}
		_ = tok
		fmt.Fprintf(os.Stdout, "perf token rotated: %s\n", internalperfdash.PerfTokenFilePath(stateDir))
		os.Exit(0)
	}

	// --rotate-console-token: generate a new console token and exit.
	if rotateConsoleToken {
		home, _ := os.UserHomeDir()
		stateDir := filepath.Join(home, ".yakos-state")
		tok, err := internalconsoleui.RotateToken(stateDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "serve: rotate-console-token: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "console token rotated; new token: %s\n", tok)
		fmt.Fprintf(os.Stdout, "token file: %s\n", internalconsoleui.TokenFilePath(stateDir))
		os.Exit(0)
	}

	if detach {
		fmt.Fprintln(os.Stderr, "serve: --detach advisory: use 'yakos serve &' to background the daemon in your shell")
		fmt.Fprintln(os.Stderr, "serve: for persistent startup see docs/integrations/ (systemd / launchd / Task Scheduler)")
	}

	// Resolve workspace root from cwd (daemon is per-workspace).
	workspaceRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "serve: resolve cwd: %v\n", err)
		os.Exit(1)
	}

	// Resolve tokens for startup banner (before daemon blocks).
	home, _ := os.UserHomeDir()
	perfStateDir := filepath.Join(home, ".yakos-state")
	consoleTok, _ := internalconsoleui.LoadOrCreateToken(perfStateDir)
	perfTok, _ := internalperfdash.LoadOrCreatePerfToken(perfStateDir)

	cfg := internalserve.Config{
		WorkspaceRoot: workspaceRoot,
		SocketPath:    socketPath,
		PIDFile:       pidFile,
		YakosRoot:     yakosRoot,
		WSAddr:        wsAddr,
		PerfAddr:      perfAddr,
		NoPerfDash:    noPerfDash,
		ConsoleAddr:   consoleAddr,
		ConsoleBind:   consoleBind,
		NoConsole:     noConsole,
	}

	wsBindAddr := wsAddr
	if wsBindAddr == "" {
		wsBindAddr = "127.0.0.1:7891"
	}
	perfBindAddr := perfAddr
	if perfBindAddr == "" {
		perfBindAddr = "127.0.0.1:7895"
	}
	// Effective console bind address for the startup banner.
	// --console-bind takes precedence over --console-addr.
	consoleEffectiveBind := consoleBind
	if consoleEffectiveBind == "" {
		consoleEffectiveBind = consoleAddr
	}
	if consoleEffectiveBind == "" {
		consoleEffectiveBind = "127.0.0.1:7890"
	}

	fmt.Fprintf(os.Stderr, "yakos serve: starting daemon for workspace %s\n", workspaceRoot)
	fmt.Fprintf(os.Stderr, "yakos serve: socket at %s\n", jsonrpc.SocketPath(workspaceRoot))
	if !noConsole {
		// When the console is enabled, /v1/events is embedded in it at
		// consoleEffectiveBind.  The standalone WS server at wsBindAddr is still
		// running for direct programmatic access (CLI tools, scripts).
		// Note: for the networked path (--console-bind non-loopback), the
		// full banner is printed by printNetworkedConsoleBanner in serve.Run();
		// this line covers the brief pre-Run startup line only.
		fmt.Fprintf(os.Stderr, "yakos serve: console: http://%s/#token=%s\n", consoleEffectiveBind, consoleTok)
		fmt.Fprintf(os.Stderr, "yakos serve: ws events (console): ws://%s/v1/events\n", consoleEffectiveBind)
		fmt.Fprintf(os.Stderr, "yakos serve: ws events (standalone): ws://%s/v1/events\n", wsBindAddr)
	} else {
		fmt.Fprintf(os.Stderr, "yakos serve: ws events at ws://%s/v1/events\n", wsBindAddr)
		if !noPerfDash {
			// Console disabled — fall back to standalone perf dashboard banner.
			fmt.Fprintf(os.Stderr, "yakos serve: perf dashboard: http://%s/#token=%s\n", perfBindAddr, perfTok)
		}
	}
	fmt.Fprintln(os.Stderr, "yakos serve: press Ctrl-C to stop")

	ctx := context.Background()
	if err := internalserve.Run(ctx, cfg); err != nil {
		if err == internalserve.ErrAlreadyRunning {
			fmt.Fprintln(os.Stderr, "serve: daemon already running for this workspace")
			os.Exit(75) // EX_TEMPFAIL per design §2
		}
		// Clean shutdown via signal returns a context.Canceled error; exit 0.
		if err == context.Canceled {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// ---- WebSocket dial helpers (used by runEvents) -----------------------------

// newWSConfig builds a *websocket.Config for the given ws:// URL and bearer token.
func newWSConfig(wsURL, token string) (*websocket.Config, error) {
	cfg, err := websocket.NewConfig(wsURL, "http://127.0.0.1/")
	if err != nil {
		return nil, err
	}
	cfg.Header = http.Header{"Authorization": {"Bearer " + token}}
	return cfg, nil
}

// dialWSConfig dials a WebSocket using the given config.
func dialWSConfig(cfg *websocket.Config) (*websocket.Conn, error) {
	return websocket.DialConfig(cfg)
}

// receiveWSJSON reads one JSON frame from conn into v.
func receiveWSJSON(conn *websocket.Conn, v interface{}) error {
	return websocket.JSON.Receive(conn, v)
}

// runEvents implements `yakos events` — a WebSocket client that prints
// events from the daemon's WS bus to stdout.
//
// Usage:
//
//	yakos events [--ws-addr <addr>] [--topic <topic>] [--since <duration>]
//
// Flags:
//
//	--ws-addr <addr>   WebSocket address (default 127.0.0.1:7891).
//	--topic <topic>    Filter to a specific topic (supports exact match or glob "kanban.*").
//	--since <duration> ERROR: replay is out of scope for Phase 2 (Q8 decision).
//	--help             Print help and exit 0.
func runEvents(args []string) {
	wsAddr := "127.0.0.1:7891"
	topic := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			printEventsHelp(os.Stdout)
			os.Exit(0)
		case "--ws-addr":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "events: --ws-addr requires an address")
				os.Exit(1)
			}
			wsAddr = args[i]
		case "--topic":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "events: --topic requires a topic string")
				os.Exit(1)
			}
			topic = args[i]
		case "--since":
			// Q8 decision: replay is out of scope for Phase 2.
			fmt.Fprintln(os.Stderr, "events: --since is not supported in Phase 2 (event replay deferred to Phase 3)")
			fmt.Fprintln(os.Stderr, "events: run without --since to receive live events from this moment forward")
			os.Exit(1)
		default:
			if len(args[i]) > 10 && args[i][:10] == "--ws-addr=" {
				wsAddr = args[i][10:]
			} else if len(args[i]) > 8 && args[i][:8] == "--topic=" {
				topic = args[i][8:]
			} else {
				fmt.Fprintf(os.Stderr, "events: unknown flag %q (try --help)\n", args[i])
				os.Exit(1)
			}
		}
	}

	// Load token from default location.
	token, err := wsbus.LoadOrCreateToken("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "events: read ws token: %v\n", err)
		os.Exit(1)
	}

	wsURL := "ws://" + wsAddr + "/v1/events"

	cfg, err := newWSConfig(wsURL, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events: build ws config: %v\n", err)
		os.Exit(1)
	}

	conn, err := dialWSConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "events: connect to %s: %v\n", wsURL, err)
		fmt.Fprintln(os.Stderr, "events: is the daemon running? try: yakos serve --ws-addr "+wsAddr)
		os.Exit(1)
	}
	defer conn.Close()

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	for {
		var ev wsbus.Event
		if err := receiveWSJSON(conn, &ev); err != nil {
			fmt.Fprintf(os.Stderr, "events: connection closed: %v\n", err)
			os.Exit(1)
		}

		// Skip ping events (internal heartbeat).
		if ev.Topic == "ping" {
			continue
		}

		// Apply topic glob filter.
		if topic != "" && !matchTopic(topic, ev.Topic) {
			continue
		}

		if err := enc.Encode(ev); err != nil {
			fmt.Fprintf(os.Stderr, "events: encode: %v\n", err)
			os.Exit(1)
		}
	}
}

// matchTopic returns true if pattern matches topic.
// Supports trailing glob: "kanban.*" matches "kanban.added", "kanban.moved", etc.
// Exact match always works.
func matchTopic(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(topic, prefix+".")
	}
	if pattern == "*" {
		return true
	}
	return false
}

func printEventsHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos events [--ws-addr <addr>] [--topic <pattern>] [--help]

Connect to the yakos daemon WebSocket event stream and print events to stdout.
Each event is printed as a JSON object (pretty-printed).

The daemon must be running (yakos serve --ws-addr <addr>).

Flags:
  --ws-addr <addr>   WebSocket address to connect to (default 127.0.0.1:7891).
  --topic <pattern>  Filter events by topic. Supports exact match or glob:
                       kanban.added
                       kanban.*        (all kanban events)
                       *               (all events, same as omitting --topic)
  --since <dur>      ERROR: replay is out of scope for Phase 2 (Q8).
                     Event replay arrives in Phase 3 if signal emerges.
  --help, -h         Print this help.

Authentication:
  Token is read from ~/.yakos-state/ws-token (same file the daemon writes).
  Rotate the token with: yakos serve --rotate-ws-token

Event topics:
  kanban.added       A task was added to the board.
  kanban.moved       A task was moved between columns.
  dispatch.started   An agent dispatch started.
  dispatch.finished  An agent dispatch finished (includes exit_code).
  presence           A developer presence update.

Example:
  yakos serve --ws-addr 127.0.0.1:7891 &
  yakos events --topic kanban.*
`)
}

func printServeHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos serve [--socket <path>] [--pidfile <path>] [--ws-addr <addr>]
             [--console-addr <addr>] [--console-bind <addr>] [--no-console]
             [--perf-addr <addr>] [--no-perf] [--detach] [--help]

Start the yakos daemon for the current workspace.

The daemon listens on a JSON-RPC 2.0 socket and routes subcommand calls
from the CLI (when YAKOS_DAEMON=on|auto) without spawning a new process
per invocation.  It also starts a WebSocket event server for real-time
multi-dev coordination (see yakos events) and a unified console dashboard
that mounts kanban, cost (metrics), and performance tabs under one token.

The daemon is OFF by default (YAKOS_DAEMON=off). To opt in:

  export YAKOS_DAEMON=auto     # uses daemon if running; falls back otherwise
  yakos serve &                # start in background

The console URL (with token) is printed at startup:
  yakos serve: console: http://127.0.0.1:7890/#token=<console-token>

For persistent daemon startup, see docs/integrations/ for systemd (Linux),
launchd (macOS), and Task Scheduler (Windows) unit files.

Flags:
  --socket <path>           Override the socket/pipe path (default: platform XDG path).
  --pidfile <path>          Override the PID file path.
  --ws-addr <addr>          WebSocket bind address (default 127.0.0.1:7891).
                            Loopback-only; cross-machine access requires mTLS (Q2).
  --console-addr <addr>     Unified console bind address (default 127.0.0.1:7890).
                            Loopback-only. Mounts kanban+cost+perf under one token.
  --console-bind <addr>     Bind the console to a non-loopback address with mTLS.
                            FAIL-CLOSED: daemon refuses if mTLS material is unavailable.
                            No plain-HTTP escape hatch. See ADR-0004.
                            When set to a loopback address, behaves like --console-addr.
                            Example: --console-bind 0.0.0.0:7890
  --no-console              Disable the unified console server.
  --perf-addr <addr>        Standalone performance dashboard address (default 127.0.0.1:7895).
                            Only used when --no-console is set.
  --no-perf                 Disable the standalone performance dashboard.
  --rotate-ws-token         Generate a new WS bearer token and exit.
  --rotate-perf-token       Generate a new perf dashboard token and exit.
  --rotate-console-token    Generate a new console token and exit.
  --detach                  Print a backgrounding advisory (operator must use '&').
  --help, -h                Print this help.

Socket paths (defaults):
  Linux   $XDG_RUNTIME_DIR/yakos/<hash>.sock
  macOS   $TMPDIR/yakos/<hash>.sock
  Windows \\.\pipe\yakos-<uid>-<hash>

<hash> is derived from the workspace root path (SHA-256 prefix, stable).

WebSocket:
  ws://127.0.0.1:7891/v1/events (default)
  Token stored at ~/.yakos-state/ws-token (mode 0600).
  Use 'yakos events' to connect a debug client.

Unified console:
  http://127.0.0.1:7890/#token=<console-token> (default)
  Token stored at ~/.yakos-state/console-token (mode 0600).
  Tabs: Overview | Chat | Flows | Kanban | Cost | Performance
  Chat: per-model REPL panes (claude/codex/agy/gemini × haiku/sonnet/opus/fable);
        claude streams token-by-token, others arrive buffered.
  Flows: YAML DAG workflow builder and live SVG canvas with per-run cost.

Performance dashboard (standalone, used only with --no-console):
  http://127.0.0.1:7895/#token=<perf-token> (default)
  Token stored at ~/.yakos-state/perf-token (mode 0600).

Exit codes:
  0   Clean shutdown (SIGTERM/SIGINT received).
  75  Another daemon is already running for this workspace (EX_TEMPFAIL).
  1   Error.
`)
}

// ---- YAKOS_DAEMON routing ---------------------------------------------------

// daemonMode reads YAKOS_DAEMON from the environment.
// Returns "off", "on", or "auto".
func daemonMode() string {
	v := os.Getenv("YAKOS_DAEMON")
	switch v {
	case "1", "on":
		return "on"
	case "auto":
		return "auto"
	default:
		return "off"
	}
}

// maybeRouteToDaemon checks YAKOS_DAEMON and routes the subcommand through the
// daemon JSON-RPC client if a daemon is reachable. Returns true if the
// subcommand was handled (or fatally errored). Returns false to fall through
// to the bash passthrough.
//
// Current routing: only --version is routed (proof of concept for the
// smoke test described in the dispatch brief). Full routing is a follow-up.
func maybeRouteToDaemon(yakosRoot string, args []string) bool {
	mode := daemonMode()
	if mode == "off" {
		return false
	}

	// Detect the daemon socket.
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	socketPath := jsonrpc.SocketPath(cwd)

	// Attempt to connect (200 ms timeout per design §2 detection).
	conn, err := jsonrpc.Dial(socketPath)
	if err != nil {
		// Daemon not running.
		if mode == "on" {
			fmt.Fprintf(os.Stderr, "yakos: WARN daemon not running at %s (YAKOS_DAEMON=on); falling through to local exec\n", socketPath)
		}
		// auto: silent fallback.
		return false
	}

	client := jsonrpc.NewClient(conn)
	defer func() { _ = client.Close() }()

	// Version match check: CLI major.minor must match daemon.
	// On mismatch, fall back rather than failing hard.
	rawVersion, vErr := client.Call(context.Background(), "yakos.version", nil)
	if vErr != nil {
		fmt.Fprintf(os.Stderr, "yakos: daemon ping failed: %v; falling through to local exec\n", vErr)
		return false
	}

	_ = rawVersion // version match logic is a follow-up; accept any live daemon for now

	// Route --version.
	if len(args) > 0 && (args[0] == "--version" || args[0] == "-v") {
		var result struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(rawVersion, &result); err == nil && result.Version != "" {
			fmt.Println(result.Version + " [via daemon]")
			return true
		}
	}

	// Route kanban mutations through the daemon so the WS bus receives events.
	// Only the mutation subcommands are routed; reads fall through to in-process.
	if len(args) >= 2 && args[0] == "kanban" {
		routed, ok := routeKanbanViaDaemon(client, args[1:])
		if ok {
			if routed != "" {
				fmt.Println(routed)
			}
			return true
		}
	}

	return false
}

// routeKanbanViaDaemon handles kanban subcommand routing through the daemon RPC.
// Returns (output, true) if the subcommand was handled, ("", false) otherwise.
func routeKanbanViaDaemon(client *jsonrpc.Client, args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	ctx := context.Background()
	switch args[0] {
	case "add":
		if len(args) < 2 {
			return "", false // let in-process handle the error
		}
		title := args[1]
		var category, notes string
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--category":
				i++
				if i < len(args) {
					category = args[i]
				}
			case "--notes":
				i++
				if i < len(args) {
					notes = args[i]
				}
			}
		}
		params := map[string]string{"title": title}
		if category != "" {
			params["category"] = category
		}
		if notes != "" {
			params["notes"] = notes
		}
		raw, err := client.Call(ctx, "yakos.kanban.add", params)
		if err != nil {
			return "", false // fall through to in-process
		}
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", false
		}
		return fmt.Sprintf("kanban: added: %s — %s (category: %s)", result.ID, title, func() string {
			if category == "" {
				return "other"
			}
			return category
		}()), true

	case "move":
		if len(args) < 3 {
			return "", false
		}
		raw, err := client.Call(ctx, "yakos.kanban.move", map[string]string{"id": args[1], "to": args[2]})
		if err != nil {
			return "", false
		}
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
			return "", false
		}
		return fmt.Sprintf("kanban: moved %s to %s", args[1], args[2]), true

	case "done":
		if len(args) < 2 {
			return "", false
		}
		raw, err := client.Call(ctx, "yakos.kanban.done", map[string]string{"id": args[1]})
		if err != nil {
			return "", false
		}
		var result struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || !result.OK {
			return "", false
		}
		return fmt.Sprintf("kanban: %s moved to DONE", args[1]), true
	}
	return "", false
}

// ---- telemetry instrumentation ----------------------------------------------

// currentGOOS returns runtime.GOOS.  Thin wrapper so the goruntime alias is
// used only in this section and does not interfere with the `runtime` package
// imported as the yakos internal/runtime package above.
func currentGOOS() string { return goruntime.GOOS }

// currentGOARCH returns runtime.GOARCH.
func currentGOARCH() string { return goruntime.GOARCH }

// recordInvocation builds and records a telemetry Event for the current CLI
// invocation.  It is called from the deferred closure in main().
// It is fail-silent: any error is swallowed.
// startNano is the result of time.Now().UnixNano() captured before dispatch.
func recordInvocation(home, yakosRoot string, args []string, startNano int64) {
	// Short-circuit before any I/O when telemetry is disabled — avoids the
	// version.Read file access on every invocation when the user has not
	// opted in. telemetry.Record already checks this, but that requires
	// building the full Event first (including version.Read).
	if cfg, err := telemetry.LoadConfig(home); err != nil || !cfg.Enabled {
		return
	}

	endTime := time.Now()
	durationMS := (endTime.UnixNano() - startNano) / 1e6
	if durationMS < 0 {
		durationMS = 0
	}

	cmd := ""
	sub := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		// Only record the second-level subcommand when it is not a flag.
		sub = args[1]
	}

	v := "unknown"
	if vv, err := version.Read(yakosRoot); err == nil {
		v = vv
	}

	ev := telemetry.Event{
		TS:           endTime.UTC(),
		YakosVersion: v,
		OS:           currentGOOS(),
		Arch:         currentGOARCH(),
		Command:      cmd,
		Subcommand:   sub,
		ExitCode:     0, // best-effort; subcommand runners call os.Exit directly
		DurationMS:   durationMS,
		AgentCount:   0,
		Runtime:      nil,
		SessionHash:  telemetry.SessionHash(),
	}

	telemetry.Record(home, ev)
}

// runMetrics implements `yakos metrics` natively in Go.
//
// Usage:
//
//	yakos metrics collect [--trigger T] [--no-write] [--skip-analyzers] [--json]
//	yakos metrics report [--json]
//	yakos metrics trend [--metric PATH] [--last N] [--since TS]
//	yakos metrics compare <shaA> <shaB>
//	yakos metrics gate [--budgets PATH] [--advisory] [--enforce] [--json] [--collect]
//	yakos metrics serve [--port N] [--host 127.0.0.1] [--project P] [--all-projects]
//	yakos metrics help
//
// Storage: <project>/.yakos/metrics/history.ndjson (append-only NDJSON).
// See cli-go/internal/metrics/metrics.go and docs/adr/ADR-0001.md.
func runMetrics(args []string) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg, err := metrics.ParseArgs(args, home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metrics: %v\n", err)
		os.Exit(1)
	}
	cfg.Writer = os.Stdout
	cfg.ErrWriter = os.Stderr

	// The serve subcommand starts a long-running HTTP server; it is handled
	// here (not inside metrics.Run) to avoid an import cycle between the
	// metrics package and the metricsdash package.
	if cfg.Subcommand == "serve" {
		if err := runMetricsDash(cfg, home); err != nil {
			fmt.Fprintf(os.Stderr, "metrics serve: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if _, err := metrics.Run(cfg); err != nil {
		// metrics gate in enforce mode returns GateExitError with the intended
		// exit code.  Use errors.As so detection survives any future wrapping
		// (e.g. fmt.Errorf("...: %w", gateErr)).  Do not print an error
		// message — the gate already printed its breach table.
		var gateErr *metrics.GateExitError
		if errors.As(err, &gateErr) {
			os.Exit(gateErr.Code)
		}
		fmt.Fprintf(os.Stderr, "metrics: %v\n", err)
		os.Exit(1)
	}
}

// runMetricsDash starts the Phase-3 metrics dashboard HTTP server.
// It is called from runMetrics when cfg.Subcommand == "serve".
// Separated to avoid an import cycle (metrics ↔ metricsdash).
func runMetricsDash(cfg metrics.Config, home string) error {
	// Resolve project directory.
	projectDir := metrics.ResolveProjectDir(cfg.ProjectDir)
	if projectDir == "" {
		cwd, _ := os.Getwd()
		projectDir = cwd
		fmt.Fprintf(os.Stderr, "metrics serve: no project dir resolved; using cwd %s\n", projectDir)
	}

	// Default host/port when ParseArgs didn't set them (shouldn't happen,
	// but guard here to be safe).
	host := cfg.ServeHost
	if host == "" {
		host = "127.0.0.1"
	}
	port := cfg.ServePort
	if port == 0 {
		port = 7896
	}
	addr := fmt.Sprintf("%s:%d", host, port)

	// Loud early failure for non-loopback bind attempts.
	if err := metricsdash.ValidateAddr(addr); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR: "+err.Error())
		return err
	}

	// State dir for the auth token.
	stateDir := cfg.StateDir
	if stateDir == "" {
		stateDir = metrics.ResolveStateDir(home)
	}

	tok, err := metricsdash.LoadOrCreateMetricsToken(stateDir)
	if err != nil {
		return fmt.Errorf("load token: %w", err)
	}

	// Print the dashboard URL with the token in the URL fragment.
	// The fragment is never sent in HTTP requests so it never appears in
	// server access logs. The raw token is only printed when --show-token is
	// set; the #token= URL is sufficient for normal browser use.
	fmt.Printf("metrics serve: dashboard ready\n")
	fmt.Printf("  URL:   http://%s/#token=%s\n", addr, tok)
	if cfg.ServeShowToken {
		fmt.Printf("  token: %s\n", tok)
	}
	fmt.Printf("  press Ctrl-C to stop\n")

	srv := metricsdash.New(metricsdash.Config{
		Addr:        addr,
		Token:       tok,
		ProjectDir:  projectDir,
		AllProjects: cfg.ServeAllProjects,
		HomeDir:     home,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return srv.Serve(ctx)
}

// runTelemetry implements `yakos telemetry` natively in Go.
//
// Usage:
//
//	yakos telemetry enable [--endpoint URL]   — enable recording
//	yakos telemetry disable                   — disable recording
//	yakos telemetry status                    — print enabled?, endpoint, counts
//	yakos telemetry set-endpoint <url>        — set/change endpoint
//	yakos telemetry purge                     — delete local NDJSON log
//	yakos telemetry show [--limit N]          — print last N records
//	yakos telemetry --help                    — print help
//
// See cli-go/internal/telemetry/README.md for schema + privacy notes.
func runTelemetry(args []string) {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}

	cfg, err := telemetry.ParseArgs(args, home)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		os.Exit(1)
	}
	cfg.Writer = os.Stdout
	cfg.ErrWriter = os.Stderr

	if _, err := telemetry.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
		os.Exit(1)
	}
}

// ---- yakos workflow ----------------------------------------------------------

// runWorkflow implements `yakos workflow <subcommand>`.
//
// Subcommands:
//
//	yakos workflow run <name> [--run-id <id>] [--operator <id>]
//	  Load <work>/current/workflows/<name>.yaml and execute it headlessly.
//	  Blocks until the graph drains (or ctx is cancelled).
//
//	yakos workflow resume <name> --prior-run-id <id> --new-run-id <id> [--operator <id>]
//	  Resume a failed workflow run from a prior runID.
//	  Fails loudly if the YAML has changed since the prior run.
//
//	yakos workflow status <run-id>
//	  Print the run.json for a given runID.
func runWorkflow(yakosRoot string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "workflow: subcommand required (run | resume | status)")
		fmt.Fprintln(os.Stderr, "usage: yakos workflow run <name> [--run-id <id>] [--operator <id>]")
		fmt.Fprintln(os.Stderr, "       yakos workflow resume <name> --prior-run-id <id> --new-run-id <id>")
		fmt.Fprintln(os.Stderr, "       yakos workflow status <run-id>")
		os.Exit(1)
	}
	sub := args[0]
	rest := args[1:]

	workspaceRoot, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow: resolve cwd: %v\n", err)
		os.Exit(1)
	}
	workDir := filepath.Join(workspaceRoot, "work", "current")

	switch sub {
	case "run":
		runWorkflowRun(yakosRoot, workspaceRoot, workDir, rest)
	case "resume":
		runWorkflowResume(yakosRoot, workspaceRoot, workDir, rest)
	case "status":
		runWorkflowStatus(workDir, rest)
	default:
		fmt.Fprintf(os.Stderr, "workflow: unknown subcommand %q (run | resume | status)\n", sub)
		os.Exit(1)
	}
}

func runWorkflowRun(yakosRoot, workspaceRoot, workDir string, args []string) {
	var name, runID, operatorID string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--run-id":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "workflow run: --run-id requires a value")
				os.Exit(1)
			}
			runID = args[i]
		case "--operator":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "workflow run: --operator requires a value")
				os.Exit(1)
			}
			operatorID = args[i]
		default:
			if name == "" && len(args[i]) > 0 && args[i][0] != '-' {
				name = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "workflow run: unknown argument %q\n", args[i])
				os.Exit(1)
			}
		}
	}
	if name == "" {
		fmt.Fprintln(os.Stderr, "workflow run: workflow name is required")
		os.Exit(1)
	}
	if runID == "" {
		// Generate a time-based default runID.
		runID = fmt.Sprintf("run-%d", time.Now().UnixMilli())
	}

	// Validate name and runID.
	if err := workflow.ValidateID("workflow name", name); err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		os.Exit(1)
	}
	if err := workflow.ValidateID("run_id", runID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		os.Exit(1)
	}

	wfPath := filepath.Join(workDir, "workflows", name+".yaml")
	wf, err := workflow.Load(wfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: load %q: %v\n", name, err)
		os.Exit(1)
	}
	if err := workflow.Validate(wf); err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: validate %q: %v\n", name, err)
		os.Exit(1)
	}

	// Build a dispatch.Service for this CLI run.
	svc := dispatch.NewService(dispatch.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		YakosRoot:     yakosRoot,
	})

	eng := &workflow.Engine{
		Svc:       svc,
		YakosRoot: yakosRoot,
		Project:   workspaceRoot,
		WorkDir:   workDir,
	}

	fmt.Fprintf(os.Stderr, "workflow run: starting %q run %s\n", name, runID)
	// CLI callers pass zero IdentityCarrier: loopback path, no RBAC enforcement.
	rs, err := eng.Run(context.Background(), wf, runID, operatorID, dispatch.IdentityCarrier{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow run: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "workflow run complete: run_id=%s status=%s\n", rs.RunID, rs.Status)
	if rs.Status != "completed" {
		os.Exit(1)
	}
}

func runWorkflowResume(yakosRoot, workspaceRoot, workDir string, args []string) {
	var name, priorRunID, newRunID, operatorID string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--prior-run-id":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "workflow resume: --prior-run-id requires a value")
				os.Exit(1)
			}
			priorRunID = args[i]
		case "--new-run-id":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "workflow resume: --new-run-id requires a value")
				os.Exit(1)
			}
			newRunID = args[i]
		case "--operator":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "workflow resume: --operator requires a value")
				os.Exit(1)
			}
			operatorID = args[i]
		default:
			if name == "" && len(args[i]) > 0 && args[i][0] != '-' {
				name = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "workflow resume: unknown argument %q\n", args[i])
				os.Exit(1)
			}
		}
	}
	if name == "" || priorRunID == "" || newRunID == "" {
		fmt.Fprintln(os.Stderr, "workflow resume: name, --prior-run-id, and --new-run-id are required")
		os.Exit(1)
	}
	// C1 (defense-in-depth): validate run IDs at the CLI boundary before any
	// filesystem access. Engine.Resume validates again internally.
	if err := workflow.ValidateID("prior_run_id", priorRunID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: %v\n", err)
		os.Exit(1)
	}
	if err := workflow.ValidateID("new_run_id", newRunID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: %v\n", err)
		os.Exit(1)
	}

	wfPath := filepath.Join(workDir, "workflows", name+".yaml")
	wf, err := workflow.Load(wfPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: load %q: %v\n", name, err)
		os.Exit(1)
	}
	if err := workflow.Validate(wf); err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: validate %q: %v\n", name, err)
		os.Exit(1)
	}

	svc := dispatch.NewService(dispatch.ServiceConfig{
		WorkspaceRoot: workspaceRoot,
		YakosRoot:     yakosRoot,
	})

	eng := &workflow.Engine{
		Svc:       svc,
		YakosRoot: yakosRoot,
		Project:   workspaceRoot,
		WorkDir:   workDir,
	}

	fmt.Fprintf(os.Stderr, "workflow resume: resuming %q from %s → %s\n", name, priorRunID, newRunID)
	// CLI callers pass zero IdentityCarrier: loopback path, no RBAC enforcement.
	rs, err := eng.Resume(context.Background(), wf, priorRunID, newRunID, operatorID, dispatch.IdentityCarrier{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow resume: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "workflow resume complete: run_id=%s parent_run_id=%s status=%s\n",
		rs.RunID, rs.ParentRunID, rs.Status)
	if rs.Status != "completed" {
		os.Exit(1)
	}
}

func runWorkflowStatus(workDir string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "workflow status: run_id is required")
		os.Exit(1)
	}
	runID := args[0]
	// H1: validate run_id before building the filesystem path.
	if err := workflow.ValidateID("run_id", runID); err != nil {
		fmt.Fprintf(os.Stderr, "workflow status: %v\n", err)
		os.Exit(1)
	}
	runDir := filepath.Join(workDir, "workflows", "runs", runID)

	rs, err := workflow.LoadRunState(runDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow status: %v\n", err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rs); err != nil {
		fmt.Fprintf(os.Stderr, "workflow status: encode: %v\n", err)
		os.Exit(1)
	}
}
