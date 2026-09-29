package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/agent"
	"github.com/bakw00ds/yakos/internal/cliflag"
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

// Ordering note (applies to every function converted in this file): like the
// cmd_diag.go conversions, cliflag.Set.Parse separates recognized flags from
// unrecognized tokens before either is acted on. A missing-value error is
// therefore reported before an unknown-flag / extra-positional error that
// appeared earlier in argv, and -h/--help wins over both. The exit code and
// the text of each individual message are unchanged. See runValidate in
// cmd_diag.go for the general rule.

// parseAgentNewFlags parses flags for `yakos agent new`.
func parseAgentNewFlags(cfg *agent.Config, args []string) {
	help := false
	fs := &cliflag.Set{Cmd: "agent new", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--runtime", Kind: cliflag.String, Str: &cfg.Runtime, ValueDesc: "an id"},
		{Name: "--project", Kind: cliflag.String, Str: &cfg.Project, ValueDesc: "a path"},
		{Name: "--extends", Kind: cliflag.String, Str: &cfg.Extends, ValueDesc: "an id"},
		{Name: "--role", Kind: cliflag.String, Str: &cfg.Role, ValueDesc: "a value"},
		{Name: "--domain", Kind: cliflag.String, Str: &cfg.Domain, ValueDesc: "a value"},
		{Name: "--model", Kind: cliflag.String, Str: &cfg.Model, ValueDesc: "a value"},
		{Name: "--tools", Kind: cliflag.String, Str: &cfg.Tools, ValueDesc: "a value"},
		{Name: "--force", Kind: cliflag.Bool, Bool: &cfg.Force},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		agent.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range rest {
		switch {
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
	}
	if cfg.Name == "" {
		agent.PrintHelp(os.Stderr)
		fmt.Fprintln(os.Stderr, "agent new: <name> required")
		os.Exit(1)
	}
}

// parseAgentLintFlags parses flags for `yakos agent lint`.
func parseAgentLintFlags(cfg *agent.Config, args []string) {
	help := false
	fs := &cliflag.Set{Cmd: "agent lint", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		agent.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range rest {
		switch {
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
	help := false
	fs := &cliflag.Set{Cmd: "agent diff", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--project", Kind: cliflag.String, Str: &cfg.Project, ValueDesc: "a path"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		agent.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range rest {
		switch {
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
	help := false
	fs := &cliflag.Set{Cmd: "agent list", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--json", Kind: cliflag.Bool, Bool: &cfg.JSON},
		{Name: "--project", Kind: cliflag.String, Str: &cfg.Project, ValueDesc: "a path"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		agent.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range rest {
		if len(arg) > 0 && arg[0] == '-' {
			fmt.Fprintf(os.Stderr, "agent list: unknown flag %q\n", arg)
		} else {
			fmt.Fprintf(os.Stderr, "agent list: unexpected argument %q\n", arg)
		}
		os.Exit(1)
	}
}

// agentDocsFormatForms replays args the way cliflag consumed them and
// reports, for each --format occurrence in order, whether it was the
// "--format=<v>" spelling (true) or "--format <v>" (false). The old
// hand-rolled parser worded the invalid-format error differently for the
// two spellings, and cliflag does not expose which spelling matched.
func agentDocsFormatForms(args []string) []bool {
	var forms []bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--format":
			forms = append(forms, false)
			i++
		case a == "--project" || a == "--out":
			i++
		case len(a) > 9 && a[:9] == "--format=":
			forms = append(forms, true)
		}
	}
	return forms
}

// runAgentDocs implements `yakos agent docs [--format md|html]`.
func runAgentDocs(yakosRoot string, args []string) {
	format := agent.DocsFormatMD
	project := ""
	outPath := ""

	help := false
	var formats []string
	fs := &cliflag.Set{Cmd: "agent docs", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--format", Kind: cliflag.StringSlice, Slice: &formats, ValueDesc: "md or html"},
		{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"},
		{Name: "--out", Kind: cliflag.String, Str: &outPath, ValueDesc: "a path"},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		_, _ = fmt.Fprint(os.Stdout, "yakos agent docs [--format md|html] [--project <path>] [--out <file>]\n\n")
		_, _ = fmt.Fprint(os.Stdout, "Render an auto-generated agent reference page from frontmatter.\n")
		os.Exit(0)
	}
	// Every --format occurrence is validated (first invalid one exits); the
	// last valid one wins. The two spellings keep their historical wording.
	forms := agentDocsFormatForms(args)
	for n, f := range formats {
		switch f {
		case "md", "markdown":
			format = agent.DocsFormatMD
		case "html":
			format = agent.DocsFormatHTML
		default:
			if n < len(forms) && forms[n] {
				fmt.Fprintf(os.Stderr, "agent docs: unknown format %q\n", f)
			} else {
				fmt.Fprintf(os.Stderr, "agent docs: unknown format %q (md or html)\n", f)
			}
			os.Exit(1)
		}
	}
	for _, arg := range rest {
		if len(arg) > 0 && arg[0] == '-' {
			fmt.Fprintf(os.Stderr, "agent docs: unknown flag %q\n", arg)
		} else {
			fmt.Fprintf(os.Stderr, "agent docs: unexpected argument %q\n", arg)
		}
		os.Exit(1)
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
		help := false
		fs := &cliflag.Set{Cmd: "plugin install", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
			{Name: "--force", Kind: cliflag.Bool, Bool: &cfg.Force},
			{Name: "--id", Kind: cliflag.String, Str: &cfg.ID, ValueDesc: "a value"},
		}}
		pos, err := fs.Parse(rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if help {
			plugin.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range pos {
			switch {
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
		help := false
		fs := &cliflag.Set{Cmd: "plugin remove", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		pos, _ := fs.Parse(rest) // no value-taking flags, so Parse cannot fail
		if help {
			plugin.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range pos {
			switch {
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
		help := false
		fs := &cliflag.Set{Cmd: "plugin validate", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
			{Name: "--id", Kind: cliflag.String, Str: &cfg.ID, ValueDesc: "a value"},
		}}
		pos, err := fs.Parse(rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if help {
			plugin.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		for _, arg := range pos {
			switch {
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
		help := false
		fs := &cliflag.Set{Cmd: "plugin register", Specs: []cliflag.Spec{
			{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		}}
		pos, _ := fs.Parse(rest) // no value-taking flags, so Parse cannot fail
		if help {
			plugin.PrintHelp(os.Stdout)
			os.Exit(0)
		}
		positionals := make([]string, 0, 2)
		for _, arg := range pos {
			switch {
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

	help := false
	fs := &cliflag.Set{Cmd: "teach", Specs: []cliflag.Spec{
		{Name: "--help", Aliases: []string{"-h"}, Kind: cliflag.Bool, Bool: &help},
		{Name: "--project", Kind: cliflag.String, Str: &project, ValueDesc: "a path"},
		{Name: "--section", Kind: cliflag.String, Str: &section, ValueDesc: "a name"},
		{Name: "--dry-run", Kind: cliflag.Bool, Bool: &dryRun},
	}}
	rest, err := fs.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if help {
		teach.PrintHelp(os.Stdout)
		os.Exit(0)
	}
	for _, arg := range rest {
		switch {
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
// State (enabled/disabled) is stored in
// ~/.yakos-state/settings.json as .retro.auto_dispatch (null/absent = enabled).
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
		fs := &cliflag.Set{Cmd: "skill candidates", Specs: []cliflag.Spec{
			{Name: "--review", Kind: cliflag.Bool, Bool: &cfg.Review},
		}}
		pos, _ := fs.Parse(rest) // no value-taking flags, so Parse cannot fail
		// Everything that is not --review is rejected, flag-shaped or not
		// (this subcommand has never accepted -h or positionals).
		for _, arg := range pos {
			fmt.Fprintf(os.Stderr, "skill candidates: unknown flag %q (try --help)\n", arg)
			os.Exit(1)
		}

	case "promote":
		fs := &cliflag.Set{Cmd: "skill promote", Specs: []cliflag.Spec{
			{Name: "--global", Kind: cliflag.Bool, Bool: &cfg.Global},
			{Name: "--project", Kind: cliflag.String, Str: &cfg.ProjectPath, ValueDesc: "a path"},
		}}
		pos, err := fs.Parse(rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, arg := range pos {
			switch {
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
		fs := &cliflag.Set{Cmd: "skill reject", Specs: []cliflag.Spec{
			{Name: "--reason", Kind: cliflag.String, Str: &cfg.Reason, ValueDesc: "a value"},
		}}
		pos, err := fs.Parse(rest)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, arg := range pos {
			switch {
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
