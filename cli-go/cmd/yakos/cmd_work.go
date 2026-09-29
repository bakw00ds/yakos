package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/archive"
	"github.com/bakw00ds/yakos/internal/checkpoint"
	"github.com/bakw00ds/yakos/internal/cliflag"
	"github.com/bakw00ds/yakos/internal/compact"
	"github.com/bakw00ds/yakos/internal/peer"
	"github.com/bakw00ds/yakos/internal/planscore"
	"github.com/bakw00ds/yakos/internal/routing"
	"github.com/bakw00ds/yakos/internal/session"
	"github.com/bakw00ds/yakos/internal/supervise"
	"github.com/bakw00ds/yakos/internal/workclose"
)

// parseWorkFlags runs fs.Parse over args and exits 1 with the missing-value
// error on failure, exactly as each hand-rolled loop did. It returns the
// tokens Parse did not recognize (unknown flags and positionals, in argv
// order) for the caller's command-specific error text.
//
// Ordering caveat inherited from cliflag's two-phase design (see the
// runValidate comment in cmd_diag.go): a missing-value error, --help, or a
// value-validation error is now resolved before the unknown-flag /
// positional errors, where the old single-pass loops reported whichever
// bad token came first in argv. Only the choice among several simultaneous
// errors in one invocation can differ; each single-error argv is unchanged.
func parseWorkFlags(fs *cliflag.Set, args []string) []string {
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return rest
}

// flagValueUsedEquals reports whether the first occurrence of value val for
// flag name in args was spelled "name=val" (true) rather than "name val"
// (false). The old numeric-flag parsers worded their "not a positive
// integer" error differently for the two spellings; cliflag hands back only
// the value, so the spelling is recovered from argv to keep stderr
// byte-identical.
func flagValueUsedEquals(args []string, name, val string) bool {
	for i, a := range args {
		if a == name+"="+val {
			return true
		}
		if a == name && i+1 < len(args) && args[i+1] == val {
			return false
		}
	}
	return false
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

	help := false
	fs := &cliflag.Set{Cmd: "archive", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--auto-tag", Kind: cliflag.Bool, Bool: &autoTag},
		// Accepted for parity; the Go implementation is always non-interactive.
		{Name: "--yes", Aliases: []string{"-y"}, Kind: cliflag.Bool},
	}}
	left := parseWorkFlags(fs, args)
	if help {
		archive.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range left {
		if len(arg) > 0 && arg[0] == '-' {
			fmt.Fprintf(os.Stderr, "archive: unknown flag %q\n", arg)
			os.Exit(1)
		}
		if project == "" {
			project = arg
		} else if tag == "" {
			tag = arg
		} else {
			fmt.Fprintln(os.Stderr, "archive: too many positional args")
			os.Exit(1)
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

	// Resolve YAKOS_ROOT from env, then cascade to materialized/embedded lib.
	// archive reads lib/settings/hook-bypass.template.md; without this the
	// bare install emits a warning and skips the template restore.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

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
	help := false
	fs := &cliflag.Set{Cmd: "session", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
	}}
	left := parseWorkFlags(fs, args)
	if help {
		session.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range left {
		if len(arg) > 0 && arg[0] == '-' {
			fmt.Fprintf(os.Stderr, "session: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		}
		if project == "" {
			project = arg
		} else if id == "" {
			id = arg
		} else {
			fmt.Fprintln(os.Stderr, "session: too many positional args (try --help)")
			os.Exit(1)
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
		// cliflag also accepts "--auto=85" (the old loop treated it as a
		// positional and then failed); accepting the "=" form is intended.
		autoArg := ""
		fs := &cliflag.Set{Cmd: "compact threshold", Specs: []cliflag.Spec{
			{Name: "--auto", Kind: cliflag.String, Str: &autoArg, ValueDesc: "a value (e.g. --auto 85)"},
		}}
		positional := parseWorkFlags(fs, rest)
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
		var ageVals []string
		fs := &cliflag.Set{Cmd: "checkpoint clean", Specs: []cliflag.Spec{
			// Repeatable in the spec so every occurrence is validated in order,
			// as the old loop did; the last valid value wins.
			{Name: "--age", Kind: cliflag.StringSlice, Slice: &ageVals, ValueDesc: "a number (days)"},
		}}
		left := parseWorkFlags(fs, rest)
		for _, val := range ageVals {
			n := 0
			if _, err := fmt.Sscanf(val, "%d", &n); err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "checkpoint clean: --age value %q is not a positive integer\n", val)
				os.Exit(1)
			}
			cfg.CleanAgeDays = n
		}
		// clean takes no positionals: every leftover token, flag-shaped or not,
		// is reported as an unknown flag.
		for _, arg := range left {
			fmt.Fprintf(os.Stderr, "checkpoint clean: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
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
		var nVals []string
		fs := &cliflag.Set{Cmd: "supervise tail", Specs: []cliflag.Spec{
			{Name: "--watch", Aliases: []string{"-w"}, Kind: cliflag.Bool, Bool: &cfg.Watch},
			// Repeatable in the spec so every occurrence is validated in order,
			// as the old loop did; the last valid value wins.
			{Name: "--n", Kind: cliflag.StringSlice, Slice: &nVals, ValueDesc: "a value"},
		}}
		left := parseWorkFlags(fs, rest)
		for _, val := range nVals {
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "supervise tail: --n value %q is not a positive integer\n", val)
				os.Exit(1)
			}
			cfg.TailN = n
		}
		for _, arg := range left {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "supervise tail: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if cfg.Project == "" {
				cfg.Project = arg
			} else {
				fmt.Fprintln(os.Stderr, "supervise tail: too many positional args")
				os.Exit(1)
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
		fs := &cliflag.Set{Cmd: "supervise ack", Specs: []cliflag.Spec{
			{Name: "--note", Kind: cliflag.String, Str: &cfg.Note, ValueDesc: "a value"},
		}}
		left := parseWorkFlags(fs, rest)
		for _, arg := range left {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "supervise ack: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if cfg.Project == "" {
				cfg.Project = arg
			} else {
				fmt.Fprintln(os.Stderr, "supervise ack: too many positional args")
				os.Exit(1)
			}
		}

	case "ack-all":
		// Optional: [<project>] [--note "..."]
		fs := &cliflag.Set{Cmd: "supervise ack-all", Specs: []cliflag.Spec{
			{Name: "--note", Kind: cliflag.String, Str: &cfg.Note, ValueDesc: "a value"},
		}}
		left := parseWorkFlags(fs, rest)
		for _, arg := range left {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "supervise ack-all: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if cfg.Project == "" {
				cfg.Project = arg
			} else {
				fmt.Fprintln(os.Stderr, "supervise ack-all: too many positional args")
				os.Exit(1)
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
		help := false
		fs := &cliflag.Set{Cmd: "plan score show", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			planscore.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range left {
			fmt.Fprintf(os.Stderr, "plan score show: unknown option %q (try --help)\n", arg)
			os.Exit(1)
		}

	case "history":
		help := false
		var limitVals []string
		fs := &cliflag.Set{Cmd: "plan score history", Specs: []cliflag.Spec{
			{Name: "--project", Kind: cliflag.String, Str: &cfg.Project, ValueDesc: "a value"},
			// Repeatable in the spec so every occurrence is validated in order,
			// as the old loop did; the last valid value wins.
			{Name: "--limit", Kind: cliflag.StringSlice, Slice: &limitVals, ValueDesc: "a value"},
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			planscore.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, val := range limitVals {
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				// The old parser worded the error differently for the
				// "--limit=N" and "--limit N" spellings; keep both.
				label := "--limit"
				if flagValueUsedEquals(rest, "--limit", val) {
					label += " value"
				}
				fmt.Fprintf(os.Stderr, "plan score history: %s %q must be a positive integer\n", label, val)
				os.Exit(1)
			}
			cfg.Limit = n
		}
		for _, arg := range left {
			fmt.Fprintf(os.Stderr, "plan score history: unknown option %q (try --help)\n", arg)
			os.Exit(1)
		}

	case "override":
		if len(rest) == 0 || rest[0][0] == '-' {
			planscore.PrintHelp(os.Stderr)
			fmt.Fprintln(os.Stderr, "plan score override: <plan_id> required")
			os.Exit(1)
		}
		cfg.PlanID = rest[0]
		rest = rest[1:]
		help := false
		fs := &cliflag.Set{Cmd: "plan score override", Specs: []cliflag.Spec{
			{Name: "--reason", Kind: cliflag.String, Str: &cfg.Reason, ValueDesc: "a value"},
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			planscore.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range left {
			fmt.Fprintf(os.Stderr, "plan score override: unknown option %q (try --help)\n", arg)
			os.Exit(1)
		}

	case "correlate":
		// "--min-n=5" is now accepted: the old "--min-n=" branch compared an
		// 8-char literal to a 7-char slice, so it was dead code and the "="
		// form was always "unknown option"; accepting it is intended.
		var minNVals []string
		fs := &cliflag.Set{Cmd: "plan score correlate", Specs: []cliflag.Spec{
			{Name: "--project", Kind: cliflag.String, Str: &cfg.Project, ValueDesc: "a value"},
			{Name: "--since", Kind: cliflag.String, Str: &cfg.Since, ValueDesc: "a value"},
			// Repeatable so every occurrence is validated in order; last wins.
			{Name: "--min-n", Kind: cliflag.StringSlice, Slice: &minNVals, ValueDesc: "a value"},
		}}
		for _, arg := range parseWorkFlags(fs, rest) {
			if arg == "-h" || arg == "--help" {
				planscore.PrintHelp(os.Stdout)
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "plan score correlate: unknown option %q (try --help)\n", arg)
			os.Exit(1)
		}
		for _, val := range minNVals {
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				if flagValueUsedEquals(rest, "--min-n", val) {
					fmt.Fprintf(os.Stderr, "plan score correlate: --min-n value %q must be a positive integer\n", val)
				} else {
					fmt.Fprintf(os.Stderr, "plan score correlate: --min-n %q must be a positive integer\n", val)
				}
				os.Exit(1)
			}
			cfg.MinN = n
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

	help := false
	fs := &cliflag.Set{Cmd: "work close", Specs: []cliflag.Spec{
		{Name: "--plan-id", Kind: cliflag.String, Str: &cfg.PlanID, ValueDesc: "a value"},
		{Name: "--no-prompt", Kind: cliflag.Bool, Bool: &cfg.NoPrompt},
		{Name: "--project", Kind: cliflag.String, Str: &cfg.ProjectDir, ValueDesc: "a value"},
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
	}}
	left := parseWorkFlags(fs, args)
	if help {
		workclose.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range left {
		fmt.Fprintf(os.Stderr, "work close: unknown option %q (try --help)\n", arg)
		os.Exit(1)
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

	// Apply YAKOS_ROOT override from env, then cascade to materialized/embedded
	// lib.  model-routing list/show/promote read lib/agents; bare binary installs
	// would find nothing without the cascade.
	if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
		cfg.YakosRoot = envRoot
	}
	cfg.YakosRoot = resolveLibRoot(cfg.YakosRoot, home, os.Stderr)

	switch sub {
	case "eval":
		help := false
		var costVals []string
		var tiersVals []string
		fs := &cliflag.Set{Cmd: "model-routing eval", Specs: []cliflag.Spec{
			{Name: "--judge", Kind: cliflag.String, Str: &cfg.Judge, ValueDesc: "a value"},
			// Repeatable in the spec so every occurrence is validated in order,
			// as the old loop did; the last valid value wins.
			{Name: "--max-cost-usd", Kind: cliflag.StringSlice, Slice: &costVals, ValueDesc: "a value"},
			{Name: "--cases", Kind: cliflag.String, Str: &cfg.CasesGlob, ValueDesc: "a value"},
			{Name: "--tiers", Kind: cliflag.StringSlice, Slice: &tiersVals, ValueDesc: "a value"},
			{Name: "--include-fable", Kind: cliflag.Bool, Bool: &cfg.IncludeFable},
			{Name: "--project", Kind: cliflag.String, Str: &cfg.Project, ValueDesc: "a value"},
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			routing.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		// --tiers is comma-separated; the last occurrence wins.
		if len(tiersVals) > 0 {
			for _, t := range strings.Split(tiersVals[len(tiersVals)-1], ",") {
				cfg.Tiers = append(cfg.Tiers, strings.TrimSpace(t))
			}
		}
		for _, val := range costVals {
			v, err := strconv.ParseFloat(val, 64)
			if err != nil || v <= 0 {
				label := "--max-cost-usd"
				if flagValueUsedEquals(rest, "--max-cost-usd", val) {
					label += " value"
				}
				fmt.Fprintf(os.Stderr, "model-routing eval: %s %q must be a positive number\n", label, val)
				os.Exit(1)
			}
			cfg.MaxCostUSD = v
		}
		for _, arg := range left {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "model-routing eval: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if cfg.AgentID == "" {
				cfg.AgentID = arg
			} else {
				fmt.Fprintf(os.Stderr, "model-routing eval: unexpected argument %q\n", arg)
				os.Exit(1)
			}
		}

	case "list":
		help := false
		fs := &cliflag.Set{Cmd: "model-routing list", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			routing.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		// list takes no arguments at all: anything left, flag-shaped or not, is an error.
		for _, arg := range left {
			fmt.Fprintf(os.Stderr, "model-routing list: unexpected argument %q\n", arg)
			os.Exit(1)
		}

	case "show":
		help := false
		fs := &cliflag.Set{Cmd: "model-routing show", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			routing.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range left {
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
		help := false
		fs := &cliflag.Set{Cmd: "model-routing promote", Specs: []cliflag.Spec{
			{Name: "--global", Kind: cliflag.Bool, Bool: &cfg.Global},
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			routing.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range left {
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

	case "reject":
		help := false
		fs := &cliflag.Set{Cmd: "model-routing reject", Specs: []cliflag.Spec{
			{Name: "--note", Kind: cliflag.String, Str: &cfg.Note, ValueDesc: "a value"},
			{Name: "--force", Kind: cliflag.Bool, Bool: &cfg.Force},
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			routing.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range left {
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "model-routing reject: unknown flag %q\n", arg)
				os.Exit(1)
			}
			if cfg.AgentID == "" {
				cfg.AgentID = arg
			} else {
				fmt.Fprintf(os.Stderr, "model-routing reject: unexpected argument %q\n", arg)
				os.Exit(1)
			}
		}

	case "history":
		help := false
		fs := &cliflag.Set{Cmd: "model-routing history", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		left := parseWorkFlags(fs, rest)
		if help {
			routing.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range left {
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
