package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/agent"
	"github.com/bakw00ds/yakos/internal/memory"
	"github.com/bakw00ds/yakos/internal/plugin"
	"github.com/bakw00ds/yakos/internal/retro"
	"github.com/bakw00ds/yakos/internal/skill"
	"github.com/bakw00ds/yakos/internal/soul"
	"github.com/bakw00ds/yakos/internal/standards"
	"github.com/bakw00ds/yakos/internal/teach"
)

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

	// Resolve the effective lib root via the cascade: on-disk → materialized →
	// embedded auto-materialize.  Bare binary installs (no YAKOS_ROOT set, no
	// cloned repo) need this so agent list/diff/docs see the framework agents
	// rather than silently returning 0.
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

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

	// Resolve YAKOS_ROOT from env, then cascade to materialized/embedded lib.
	// soul seed reads lib/settings/soul.template.md; without this the bare
	// install silently falls back to a built-in default instead of the shipped
	// template.
	if r := os.Getenv("YAKOS_ROOT"); r != "" {
		yakosRoot = r
	}
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

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
	// Cascade to materialized/embedded lib so skill promote --global can
	// locate lib/skills/ on a bare binary install.
	yakosRoot = resolveLibRoot(yakosRoot, home, os.Stderr)

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
