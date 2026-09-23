package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/bakw00ds/yakos/internal/passthrough"
	"github.com/bakw00ds/yakos/internal/version"
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
	{Name: "init", Since: "0.46.0", Desc: "Bootstrap a new yakOS-wired project", Notes: "full feature parity with cli/lib/init.sh; hook copy advisory printed (bash refresh handles hooks); --with-gate/--multi-dev not ported in Phase 1 (base project still written, but the command exits non-zero for either flag)"},
	{Name: "install", Since: "0.47.0", Desc: "Install yakOS framework files into a project", Notes: "full feature parity with cli/lib/install.sh; --force/--dry-run supported; per-file symlinks into ~/.claude/{agents,skills,rules,playbooks}; launcher symlink at ~/.local/bin/yakos; settings.json env merge"},
	{Name: "uninstall", Since: "0.48.0", Desc: "Remove yakOS symlinks and launcher", Notes: "full feature parity with cli/lib/uninstall.sh; removes YakOS-owned symlinks + launcher + pointer; --restore-settings/--root/--dry-run; partial-uninstall log+continue"},
	{Name: "start", Since: "0.49.0", Desc: "Start a yakOS session (preflight + exec runtime)", Notes: "full feature parity with cli/lib/start.sh; preflight banner + audit-log; exec deferred to runtime CLI; --dry-run/--print-agents/--safe/--allow-root/passthrough flags supported"},
	{Name: "update", Since: "0.50.0", Desc: "Pull latest yakOS and refresh all projects", Notes: "full feature parity with cli/lib/update.sh; git pull --ff-only + per-project refresh via refresh.CollectProjects + refresh.Run; --allow-non-ff/--all/--dry-run supported"},
	{Name: "upgrade", Since: "0.76.1", Desc: "Download latest release and fully re-provision", Notes: "binary-install upgrade: selfupdate.Apply (GitHub release fetch + SHA-256 + atomic replace) then install.Run re-provision (re-materialize embedded lib, re-point ~/.claude symlinks, re-merge settings); --force/--dry-run/--check flags; idempotent"},
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
	{Name: "mtls", Since: "0.76.0", Desc: "Manage mTLS client certs for the networked console", Notes: "issue-client/list-clients/show-ca/set-role subcommands; hand-off bundle (crt+key+ca.crt); atomic roles.json writes at 0600 (LOW-1 compatible); auto-issue bootstrap cert on first networked start; --no-bootstrap-cert to disable; ADR-0004"},
}

type portedCommand struct {
	Name  string
	Since string // version in which Go impl shipped
	Desc  string // one-liner shown in `yakos help` (≤60 chars)
	Notes string
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
			"init", "install", "uninstall", "update", "upgrade", "quickstart",
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
			"serve", "console", "kanban", "status", "session", "events", "mtls",
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
	"console":   "Manage console users and bootstrap tokens (run on daemon host)",
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
