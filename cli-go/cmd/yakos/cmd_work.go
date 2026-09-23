package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/archive"
	"github.com/bakw00ds/yakos/internal/checkpoint"
	"github.com/bakw00ds/yakos/internal/compact"
	"github.com/bakw00ds/yakos/internal/peer"
	"github.com/bakw00ds/yakos/internal/planscore"
	"github.com/bakw00ds/yakos/internal/routing"
	"github.com/bakw00ds/yakos/internal/session"
	"github.com/bakw00ds/yakos/internal/supervise"
	"github.com/bakw00ds/yakos/internal/workclose"
)

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

	// Apply YAKOS_ROOT override from env, then cascade to materialized/embedded
	// lib.  model-routing list/show/promote read lib/agents; bare binary installs
	// would find nothing without the cascade.
	if envRoot := os.Getenv("YAKOS_ROOT"); envRoot != "" {
		cfg.YakosRoot = envRoot
	}
	cfg.YakosRoot = resolveLibRoot(cfg.YakosRoot, home, os.Stderr)

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
