package main

import (
	"bytes"
	"io"
	"regexp"

	"github.com/bakw00ds/yakos/internal/agent"
	"github.com/bakw00ds/yakos/internal/archive"
	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/checkpoint"
	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/compact"
	"github.com/bakw00ds/yakos/internal/completion"
	"github.com/bakw00ds/yakos/internal/consolecmd"
	"github.com/bakw00ds/yakos/internal/doctor"
	"github.com/bakw00ds/yakos/internal/envcfg"
	"github.com/bakw00ds/yakos/internal/githooks"
	"github.com/bakw00ds/yakos/internal/hooksinstall"
	"github.com/bakw00ds/yakos/internal/initialize"
	"github.com/bakw00ds/yakos/internal/install"
	"github.com/bakw00ds/yakos/internal/mcp"
	"github.com/bakw00ds/yakos/internal/memory"
	"github.com/bakw00ds/yakos/internal/metrics"
	"github.com/bakw00ds/yakos/internal/migrate"
	"github.com/bakw00ds/yakos/internal/mtlscmd"
	"github.com/bakw00ds/yakos/internal/peer"
	"github.com/bakw00ds/yakos/internal/planscore"
	"github.com/bakw00ds/yakos/internal/plugin"
	"github.com/bakw00ds/yakos/internal/quickstart"
	"github.com/bakw00ds/yakos/internal/retro"
	"github.com/bakw00ds/yakos/internal/routing"
	"github.com/bakw00ds/yakos/internal/session"
	"github.com/bakw00ds/yakos/internal/skill"
	"github.com/bakw00ds/yakos/internal/soul"
	"github.com/bakw00ds/yakos/internal/standards"
	internalstart "github.com/bakw00ds/yakos/internal/start"
	"github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/supervise"
	"github.com/bakw00ds/yakos/internal/teach"
	"github.com/bakw00ds/yakos/internal/telemetry"
	"github.com/bakw00ds/yakos/internal/uninstall"
	"github.com/bakw00ds/yakos/internal/workclose"
)

// flagAllow records one flag deliberately excluded from the help-vs-parser
// comparison for a command, with a reason a future reader can audit.
type flagAllow struct {
	Flag   string
	Reason string
}

// commandEntry is one row of commandRegistry: a top-level `yakos <name>`
// command's declared flag surface (Specs, aggregated across every
// subcommand that name routes to) and the help text a reader would see via
// `yakos <name> --help`.
//
// Specs is intentionally independent of any cliflag.Set actually used at
// runtime to parse args (only cmd_diag.go and cmd_integration.go use
// cliflag.Set for real parsing as of this PR; every other command still
// hand-parses its own argv). It exists purely so Names() has something to
// compare the command's --help text against. As more cmd_*.go files convert
// to cliflag in follow-up PRs (s6-structural-plan-2026-09-23.md §3.2 B2),
// their registry entries can be tightened to reference the live Set instead
// of a hand-maintained duplicate list.
type commandEntry struct {
	Name   string
	Specs  cliflag.Set
	HelpFn func(io.Writer)

	// AllowUndocumented lists flags Specs declares that HelpFn deliberately
	// does not mention. "-h" and "--help" are allowed for every command
	// implicitly (see helpParserGlobalAllow) and do not need to be repeated
	// here.
	AllowUndocumented []flagAllow

	// AllowUnparsed lists flag-shaped tokens HelpFn's text contains that
	// Specs does not declare — e.g. prose describing another CLI's flag
	// ("codex --dangerously-bypass-approvals-and-sandbox") rather than a
	// yakos flag.
	AllowUnparsed []flagAllow
}

// helpParserGlobalAllow is merged into every command's AllowUndocumented.
// "-h"/"--help" is accepted by essentially every parser in cmd/yakos, but
// most hand-written help bodies document it only via the top-level
// synopsis ("yakos <cmd> --help") rather than a repeated "-h, --help" bullet
// inside their own body — some do (archive, install, uninstall, ...), most
// don't. Requiring every command to spell it out would turn the diff test
// into churn on the single most universal, least interesting flag; the
// interesting drift this test exists to catch is a REAL flag (like
// runDoctor's --production, or runAuth's --all) silently missing from its
// help, not the help flag itself.
var helpParserGlobalAllow = []string{"-h", "--help"}

// flagTokenRE matches long-form flag tokens ("--foo", "--foo-bar") in
// free-form help text. Deliberately long-form only (no "-x" short forms):
// this is also the shape Set.Names() aliases are expected to use for the
// canonical Name field, and several existing help bodies use single-letter
// aliases (-s, -c, -y, -w) only in the usage synopsis without ever spelling
// them out in prose — see s6-structural-plan-2026-09-23.md §3.3. Short
// aliases are still real and still parsed (cliflag.Spec.Aliases carries
// them); they are just not part of this particular drift check, on the
// theory that documenting the long form is what matters and a short alias
// undocumented in prose is not the v0.50-class bug this test targets.
var flagTokenRE = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// extractFlagTokens returns every distinct long-form flag token
// (flagTokenRE matches) found in text, sorted.
func extractFlagTokens(text string) []string {
	matches := flagTokenRE.FindAllString(text, -1)
	seen := make(map[string]struct{}, len(matches))
	var out []string
	for _, m := range matches {
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	sortStringsLocal(out)
	return out
}

// sortStringsLocal is a tiny insertion sort, mirroring cliflag's own
// (unexported, so not reusable here) sortStrings — keeps this file
// dependency-free of "sort" for a handful of elements at a time and
// deterministic regardless of Go's sort implementation version, matching
// rule:cache-stability.
func sortStringsLocal(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}

// renderHelp runs fn against a buffer and returns the text. Used both to
// build help-text-derived Specs (helpDerivedSpecs, for commands whose flag
// parsing lives entirely inside an internal/<pkg> Run/ParseArgs function
// this registry does not otherwise audit) and by the diff test itself.
func renderHelp(fn func(io.Writer)) string {
	var buf bytes.Buffer
	fn(&buf)
	return buf.String()
}

// helpDerivedSpecs builds a cliflag.Set whose Specs are exactly the
// long-form flag tokens found in fn's own help output — so Names() can
// never drift from that text, by construction.
//
// This is used for commands whose real argv parsing happens entirely
// inside an internal/<pkg> function this PR does not independently audit
// (peer, soul, retro, telemetry, metrics — see s6-b2-cliflag-2026-09-23.md
// "Group B"): auditing each such package's hand-rolled parser was out of
// scope for this PR, so their help text is trusted as authoritative rather
// than independently re-derived from a second reading of their source.
func helpDerivedSpecs(name string, fn func(io.Writer)) cliflag.Set {
	toks := extractFlagTokens(renderHelp(fn))
	specs := make([]cliflag.Spec, len(toks))
	for i, t := range toks {
		specs[i] = cliflag.Spec{Name: t}
	}
	return cliflag.Set{Cmd: name, Specs: specs}
}

// s is a tiny constructor: a flat cliflag.Set from bare flag names (no
// Kind/ValueDesc — this registry only ever calls Names() on these, never
// Parse, so those fields are immaterial here).
func specSet(name string, flags ...string) cliflag.Set {
	specs := make([]cliflag.Spec, len(flags))
	for i, f := range flags {
		specs[i] = cliflag.Spec{Name: f}
	}
	return cliflag.Set{Cmd: name, Specs: specs}
}

// helpConsole and helpMTLS adapt consolecmd.Run / mtlscmd.Run (which take
// argv, not a bare io.Writer) into the func(io.Writer) HelpFn shape by
// invoking them with "--help".
func helpConsole(w io.Writer) { _ = consolecmd.Run([]string{"--help"}, w, io.Discard) }
func helpMTLS(w io.Writer)    { _ = mtlscmd.Run([]string{"--help"}, w, io.Discard) }

// commandRegistry is the explicit, source-ordered (alphabetical by Name)
// list of every `yakos <name>` command's declared flags and help text.
// Deliberately a slice, not a map, per rule:cache-stability: a map would
// need sorting before every use to stay deterministic, and this list feeds
// a test whose failure output should be stable run-to-run.
//
// Excluded: the always-native built-ins intercepted before the command
// switch even runs (--version, -v, --help, -h, help, go-port-status,
// main.go:230-243) — they take no flags and are answered before any
// command-specific parsing exists to audit. Also excluded: "agents", the
// plural alias for "agent" (identical Specs/HelpFn under the other name).
var commandRegistry = []commandEntry{
	{
		Name:   "agent",
		Specs:  specSet("agent", "--runtime", "--project", "--extends", "--role", "--domain", "--model", "--tools", "--force", "--json", "--format", "--out"),
		HelpFn: agent.PrintHelp,
		AllowUndocumented: []flagAllow{
			{Flag: "--format", Reason: "`agent docs --format`: the docs subcommand isn't described in agent.PrintHelp at all (internal/agent, not owned by this PR)"},
			{Flag: "--out", Reason: "`agent docs --out`: see --format above"},
		},
	},
	{
		Name:   "archive",
		Specs:  specSet("archive", "--auto-tag", "--yes"),
		HelpFn: archive.PrintHelp,
	},
	{
		Name:   "auth",
		Specs:  specSet("auth", "--as-default", "--all"),
		HelpFn: auth.PrintHelp,
		AllowUndocumented: []flagAllow{
			{Flag: "--all", Reason: "auth.PrintHelp (internal/auth) documents --as-default but never mentions --all; doc gap in a package this PR does not own"},
		},
	},
	{
		Name:   "checkpoint",
		Specs:  specSet("checkpoint", "--age"),
		HelpFn: checkpoint.PrintHelp,
		AllowUnparsed: []flagAllow{
			{Flag: "--fork-session", Reason: "checkpoint.PrintHelp's `restore <id> # resume via --fork-session` describes yakos start's flag, not a checkpoint flag"},
		},
	},
	{
		Name:   "compact",
		Specs:  specSet("compact", "--auto"),
		HelpFn: compact.PrintHelp,
	},
	{
		Name:   "completion",
		Specs:  specSet("completion"),
		HelpFn: completion.PrintHelp,
	},
	{
		Name:   "console",
		Specs:  specSet("console"),
		HelpFn: helpConsole,
	},
	{
		Name:   "cost",
		Specs:  specSet("cost", "--since", "--by", "--json", "--all-projects"),
		HelpFn: printCostHelp,
	},
	{
		Name:   "dispatch",
		Specs:  specSet("dispatch", "--runtime", "--model", "--project", "--timeout", "--eval-run-id", "--allow-root"),
		HelpFn: printDispatchHelp,
	},
	{
		Name:   "doctor",
		Specs:  specSet("doctor", "--probe-runtime", "--production", "--fix"),
		HelpFn: doctor.PrintHelp,
		AllowUndocumented: []flagAllow{
			{Flag: "--production", Reason: "doctor.PrintHelp (internal/doctor) documents --probe-runtime and --fix but never mentions --production; doc gap in a package this PR does not own"},
		},
	},
	{
		Name:   "env",
		Specs:  specSet("env"),
		HelpFn: envcfg.PrintHelp,
	},
	{
		Name:   "events",
		Specs:  specSet("events", "--ws-addr", "--topic", "--since"),
		HelpFn: printEventsHelp,
		AllowUnparsed: []flagAllow{
			{Flag: "--rotate-ws-token", Reason: "printEventsHelp's `Rotate the token with: yakos serve --rotate-ws-token` describes yakos serve's flag, not an events flag"},
		},
	},
	{
		Name:   "git-hooks",
		Specs:  specSet("git-hooks", "--force", "--promotion-gate"),
		HelpFn: githooks.PrintHelp,
		AllowUnparsed: []flagAllow{
			{Flag: "--no-verify", Reason: "githooks.PrintHelp's `git push --no-verify` describes git's own flag, not a yakos git-hooks flag"},
		},
	},
	{
		Name:   "hooks",
		Specs:  specSet("hooks", "--project", "--force", "--hooks-dir"),
		HelpFn: hooksinstall.PrintHelp,
		AllowUndocumented: []flagAllow{
			{Flag: "--force", Reason: "hooksinstall.PrintHelp (internal/hooksinstall) documents `install <runtime> --project <path>` but never mentions the also-accepted --force; doc gap in a package this PR does not own"},
		},
	},
	{
		Name:   "init",
		Specs:  specSet("init", "--project", "--template", "--force", "--with-gate", "--multi-dev", "--dry-run"),
		HelpFn: initialize.PrintHelp,
	},
	{
		Name:   "install",
		Specs:  specSet("install", "--force", "--dry-run"),
		HelpFn: install.PrintHelp,
	},
	{
		Name:   "kanban",
		Specs:  specSet("kanban", "--html", "--category", "--notes", "--port", "--host", "--no-open"),
		HelpFn: printKanbanHelp,
	},
	{
		Name:   "mcp",
		Specs:  specSet("mcp", "--project"),
		HelpFn: mcp.PrintHelp,
	},
	{
		Name:   "memory",
		Specs:  specSet("memory"),
		HelpFn: memory.PrintHelp,
	},
	{
		Name:   "metrics",
		HelpFn: metrics.PrintHelp,
		Specs:  helpDerivedSpecs("metrics", metrics.PrintHelp),
	},
	{
		Name:   "migrate",
		Specs:  specSet("migrate", "--dry-run"),
		HelpFn: migrate.PrintHelp,
	},
	{
		Name:   "model-routing",
		Specs:  specSet("model-routing", "--judge", "--max-cost-usd", "--cases", "--project", "--global", "--note", "--force"),
		HelpFn: routing.PrintHelp,
	},
	{
		Name:   "mtls",
		Specs:  specSet("mtls"),
		HelpFn: helpMTLS,
	},
	{
		Name:   "peer",
		HelpFn: peer.PrintHelp,
		Specs:  helpDerivedSpecs("peer", peer.PrintHelp),
	},
	{
		Name:   "plan",
		Specs:  specSet("plan", "--project", "--limit", "--reason", "--since", "--min-n"),
		HelpFn: planscore.PrintHelp,
	},
	{
		Name:   "plugin",
		Specs:  specSet("plugin", "--id", "--force"),
		HelpFn: plugin.PrintHelp,
	},
	{
		Name:   "quickstart",
		Specs:  specSet("quickstart", "--runtime", "--multi-dev", "--safe", "--allow-root", "--dry-run"),
		HelpFn: quickstart.PrintHelp,
	},
	{
		Name:   "refresh",
		Specs:  specSet("refresh", "--dry-run", "--all", "--project"),
		HelpFn: printRefreshHelp,
	},
	{
		Name:   "retro",
		HelpFn: retro.PrintHelp,
		Specs:  helpDerivedSpecs("retro", retro.PrintHelp),
	},
	{
		Name: "serve",
		Specs: specSet("serve", "--socket", "--pidfile", "--ws-addr", "--perf-addr", "--console-addr",
			"--console-bind", "--console-external-host", "--rotate-ws-token", "--rotate-perf-token",
			"--rotate-console-token", "--no-perf", "--no-console", "--console-bootstrap-cert",
			"--no-bootstrap-cert", "--console-allow-bash", "--console-structured-questions",
			"--share-terminal", "--ide-root", "--detach"),
		HelpFn: printServeHelp,
		AllowUndocumented: []flagAllow{
			{Flag: "--console-bootstrap-cert", Reason: "cmd_serve.go is off-limits for this PR (owned concurrently, per the dispatch brief); printServeHelp never mentions this real flag"},
			{Flag: "--no-bootstrap-cert", Reason: "see --console-bootstrap-cert above"},
			{Flag: "--console-allow-bash", Reason: "see --console-bootstrap-cert above"},
			{Flag: "--console-structured-questions", Reason: "see --console-bootstrap-cert above"},
		},
		AllowUnparsed: []flagAllow{
			{Flag: "--no-repl", Reason: "printServeHelp's auto-spawn note `(without --no-repl)` describes yakos start's flag, not a serve flag"},
		},
	},
	{
		Name:   "session",
		Specs:  specSet("session"),
		HelpFn: session.PrintHelp,
	},
	{
		Name:   "skill",
		Specs:  specSet("skill", "--review", "--global", "--project", "--reason"),
		HelpFn: skill.PrintHelp,
		AllowUndocumented: []flagAllow{
			{Flag: "--project", Reason: "skill.PrintHelp (internal/skill) documents `promote <slug> [--global]` but never mentions the also-accepted --project; doc gap in a package this PR does not own"},
		},
	},
	{
		Name:   "soul",
		HelpFn: soul.PrintHelp,
		Specs:  helpDerivedSpecs("soul", soul.PrintHelp),
	},
	{
		Name:   "standards",
		Specs:  specSet("standards"),
		HelpFn: standards.PrintHelp,
	},
	{
		Name: "start",
		Specs: specSet("start", "--runtime", "--safe", "--allow-root", "--no-agents", "--dry-run",
			"--print-agents", "--continue", "--fork-session", "--ide", "--bare", "--strict-mcp",
			"--no-repl", "--web", "--console-addr", "--ws-addr", "--perf-addr", "--networked",
			"--console-bind", "--console-external-host", "--ide-root", "--no-project-ide",
			"--resume", "--model", "--share-terminal", "--direct"),
		HelpFn: internalstart.PrintHelp,
		AllowUnparsed: []flagAllow{
			{Flag: "--dangerously-bypass-approvals-and-sandbox", Reason: "start.PrintHelp prose describing CODEX's own bypass flag, not a yakos start flag; cmd_start.go / internal/start are off-limits for this PR"},
			{Flag: "--approval-mode", Reason: "start.PrintHelp prose describing GEMINI's own approval-mode flag, not a yakos start flag"},
			{Flag: "--permission-mode", Reason: "start.PrintHelp prose describing CLAUDE's own permission-mode flag, not a yakos start flag"},
			{Flag: "--strict-mcp-config", Reason: "start.PrintHelp's `claude only — pass --strict-mcp-config` describes what --strict-mcp translates to for the claude runtime, not a second yakos start flag"},
		},
	},
	{
		Name:   "status",
		Specs:  specSet("status"),
		HelpFn: status.PrintHelp,
	},
	{
		Name:   "supervise",
		Specs:  specSet("supervise", "--watch", "--n", "--note"),
		HelpFn: supervise.PrintHelp,
		AllowUndocumented: []flagAllow{
			{Flag: "--n", Reason: "supervise.PrintHelp (internal/supervise) documents `tail [<project>] [--watch]` but never mentions the also-accepted --n; doc gap in a package this PR does not own"},
		},
	},
	{
		Name:   "teach",
		Specs:  specSet("teach", "--project", "--section", "--dry-run"),
		HelpFn: teach.PrintHelp,
		AllowUnparsed: []flagAllow{
			{Flag: "--extends", Reason: "teach.PrintHelp's `yakos agent new <name> --extends <framework-name>` note describes yakos agent's flag, not a teach flag"},
		},
	},
	{
		Name:   "team",
		Specs:  specSet("team", "--tag", "--yes"),
		HelpFn: printTeamHelp,
	},
	{
		Name:   "telemetry",
		HelpFn: telemetry.PrintHelp,
		Specs:  helpDerivedSpecs("telemetry", telemetry.PrintHelp),
	},
	{
		Name:   "uninstall",
		Specs:  specSet("uninstall", "--restore-settings", "--root", "--dry-run"),
		HelpFn: uninstall.PrintHelp,
	},
	{
		Name:   "update",
		Specs:  specSet("update", "--allow-non-ff", "--all", "--check", "--force", "--dry-run", "--binary", "--source"),
		HelpFn: printUpdateHelp,
		AllowUnparsed: []flagAllow{
			{Flag: "--ff-only", Reason: "printUpdateHelp's `git pull --ff-only in $YAKOS_ROOT` describes git's own flag, not a yakos update flag"},
		},
	},
	{
		Name:   "upgrade",
		Specs:  specSet("upgrade", "--force", "--dry-run", "--check"),
		HelpFn: printUpgradeHelp,
	},
	{
		Name:   "validate",
		Specs:  specSet("validate", "--all", "--strict"),
		HelpFn: printValidateHelp,
	},
	{
		Name:   "work",
		Specs:  specSet("work", "--plan-id", "--no-prompt", "--project"),
		HelpFn: workclose.PrintHelp,
	},
	{
		Name:   "workflow",
		Specs:  specSet("workflow", "--run-id", "--operator", "--prior-run-id", "--new-run-id"),
		HelpFn: printWorkflowHelp,
	},
}
