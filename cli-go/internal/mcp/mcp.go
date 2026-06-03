// Package mcp implements the Go port of `yakos mcp`.
//
// Phase 1 scope: READ-ONLY config management for the yakos-dispatch MCP
// server registration in a project's .mcp.json (or the global
// ~/.claude/mcp.json).
//
// The native MCP server itself is Phase 2 (Q3 design decision); this package
// only manages the JSON config that tells Claude Code where to find the server.
//
// Subcommands:
//
//	install   [--project <path>]   Add/refresh the yakos-dispatch entry.
//	uninstall [--project <path>]   Remove the yakos-dispatch entry.
//	status    [--project <path>]   Show whether the entry is present.
//	probe                          Verify the 'mcp' Python package is importable.
//
// MCP config file location:
//
//	Unix/macOS: <project>/.mcp.json          (project-scoped)
//	Windows:    %APPDATA%/claude/mcp.json    (global; per planner Q3 recommendation)
//
// Note: all JSON reads and writes are done with encoding/json + atomic
// temp-rename (Q8 compliance). No external jq dependency.
package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ServerName is the key used for the yakos-dispatch entry in mcpServers.
const ServerName = "yakos-dispatch"

// Config controls a mcp Run invocation.
type Config struct {
	// Subcommand is one of: install, uninstall, status, probe.
	Subcommand string

	// Project is the project directory whose .mcp.json to manage.
	// When empty, the current working directory is used (resolved via CWD()).
	Project string

	// YakosRoot is the framework repo root (YAKOS_ROOT). Required for
	// install so the server script path can be constructed.
	YakosRoot string

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error output destination. Defaults to os.Stderr.
	ErrWriter io.Writer

	// CWD is injected for tests. When empty, os.Getwd() is used.
	CWD func() (string, error)

	// LookPath is injected for tests to simulate Python availability.
	// When nil, exec.LookPath is used.
	LookPath func(string) (string, error)

	// ExecPython is injected for tests to simulate `python3 -c 'import mcp'`.
	// Returns nil when the package is importable; non-nil otherwise.
	ExecPython func() error

	// Now is injected for tests. When nil, time.Now is used.
	Now func() time.Time
}

// Result is the structured outcome of a mcp Run invocation.
type Result struct {
	Subcommand string
	// Project is the resolved absolute project path.
	Project string
	// MCPFile is the resolved .mcp.json path.
	MCPFile string
	// EntryPresent reports whether the yakos-dispatch entry existed after the call.
	EntryPresent bool
	// ServerScriptPresent reports whether the server script was found (install/status).
	ServerScriptPresent bool
	// PythonMCPImportable reports whether `python3 -c 'import mcp'` succeeded (probe/status).
	PythonMCPImportable bool
}

// Run executes the mcp subcommand described by cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	lookPath := cfg.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	execPython := cfg.ExecPython
	if execPython == nil {
		execPython = defaultExecPython
	}
	cwdFn := cfg.CWD
	if cwdFn == nil {
		cwdFn = os.Getwd
	}

	r := &runner{
		cfg:        cfg,
		w:          cfg.Writer,
		now:        now,
		lookPath:   lookPath,
		execPython: execPython,
		cwdFn:      cwdFn,
	}

	switch cfg.Subcommand {
	case "install":
		return r.runInstall()
	case "uninstall":
		return r.runUninstall()
	case "status":
		return r.runStatus()
	case "probe":
		return r.runProbe()
	case "", "-h", "--help", "help":
		PrintHelp(cfg.Writer)
		return &Result{Subcommand: cfg.Subcommand}, nil
	default:
		return nil, fmt.Errorf("mcp: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp prints the mcp help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos mcp <subcommand> [args...]

Manage the yakOS MCP server registration in a project's .mcp.json.

Subcommands:
  install [--project <path>]     Add the yakos-dispatch entry to the
                                 project's .mcp.json (creates the file
                                 if absent; merges other servers).
                                 Default project: cwd.
  uninstall [--project <path>]   Remove the yakos-dispatch entry.
  status [--project <path>]      Show whether the entry is present.
  probe                          Verify the 'mcp' Python package is
                                 installed (the server needs it).

The MCP server itself lives at:
  cli/lib/mcp/yakos-mcp-server.py

Tools exposed once installed (visible to Claude Code in the session):
  dispatch_codex, dispatch_agy, dispatch_claude_sdk,
  dispatch_antigravity_sdk, dispatch_claude
  continue_codex, continue_agy, continue_claude_sdk, continue_claude
  (antigravity-sdk continue intentionally absent — SDK lacks cross-
  process resume support at v0.31)

Examples:
  yakos mcp install                                     # cwd
  yakos mcp install --project ~/code/myapp
  yakos mcp status
  yakos mcp uninstall

After install, restart the Claude Code session in that project and
the yakos-dispatch tools become available to the lead.

See docs/mcp-integration.md for the full guide.
`)
}

// ---- runner -----------------------------------------------------------------

type runner struct {
	cfg        Config
	w          io.Writer
	now        func() time.Time
	lookPath   func(string) (string, error)
	execPython func() error
	cwdFn      func() (string, error)
}

// resolveProject returns the absolute project directory path.
func (r *runner) resolveProject() (string, error) {
	p := r.cfg.Project
	if p == "" {
		cwd, err := r.cwdFn()
		if err != nil {
			return "", fmt.Errorf("mcp: could not determine current directory: %w", err)
		}
		return cwd, nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("mcp: cannot resolve project path %q: %w", p, err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("mcp: --project %q does not exist", p)
	}
	return abs, nil
}

// serverScript returns the path to the yakos-mcp-server.py.
func (r *runner) serverScript() string {
	return filepath.Join(r.cfg.YakosRoot, "cli", "lib", "mcp", "yakos-mcp-server.py")
}

// isoTimestamp returns a UTC timestamp suitable for backup file names.
func (r *runner) isoTimestamp() string {
	t := r.now().UTC()
	return fmt.Sprintf("%04d%02d%02dT%02d%02d%02dZ",
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second())
}

// ---- install ----------------------------------------------------------------

// mcpEntry is the JSON shape of a single mcpServers entry.
type mcpEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// mcpDoc is the top-level .mcp.json structure.
type mcpDoc struct {
	MCPServers map[string]mcpEntry `json:"mcpServers"`
}

func (r *runner) runInstall() (*Result, error) {
	project, err := r.resolveProject()
	if err != nil {
		return nil, err
	}

	mcpFile := filepath.Join(project, ".mcp.json")
	script := r.serverScript()

	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("mcp install: server script missing at %s", script)
	}

	entry := mcpEntry{
		Command: "python3",
		Args:    []string{script},
		Env:     map[string]string{"YAKOS_ROOT": r.cfg.YakosRoot},
	}

	var doc mcpDoc
	if _, err := os.Stat(mcpFile); os.IsNotExist(err) {
		// Create fresh .mcp.json.
		doc = mcpDoc{MCPServers: map[string]mcpEntry{ServerName: entry}}
		if err := writeJSON(mcpFile, doc); err != nil {
			return nil, fmt.Errorf("mcp install: could not write %s: %w", mcpFile, err)
		}
		_, _ = fmt.Fprintf(r.w, "wrote %s with %s entry\n", mcpFile, ServerName)
		_, _ = fmt.Fprintf(r.w, "restart your Claude Code session in %s to load it\n", project)
		return &Result{
			Subcommand:          "install",
			Project:             project,
			MCPFile:             mcpFile,
			EntryPresent:        true,
			ServerScriptPresent: true,
		}, nil
	}

	// Parse existing JSON.
	data, err := os.ReadFile(mcpFile) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("mcp install: could not read %s: %w", mcpFile, err)
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("mcp install: existing %s is not valid JSON; refusing to overwrite", mcpFile)
	}

	// Backup first.
	bak := mcpFile + ".yakos-bak-" + r.isoTimestamp()
	if err := copyFile(mcpFile, bak); err != nil {
		return nil, fmt.Errorf("mcp install: could not create backup: %w", err)
	}

	// Decode, merge, write.
	if err := json.Unmarshal(data, &doc); err != nil {
		doc = mcpDoc{}
	}
	if doc.MCPServers == nil {
		doc.MCPServers = make(map[string]mcpEntry)
	}
	doc.MCPServers[ServerName] = entry

	if err := writeJSON(mcpFile, doc); err != nil {
		return nil, fmt.Errorf("mcp install: merge failed; backup at %s: %w", bak, err)
	}

	_, _ = fmt.Fprintf(r.w, "updated %s (added/refreshed %s entry)\n", mcpFile, ServerName)
	_, _ = fmt.Fprintf(r.w, "backup at %s\n", bak)
	_, _ = fmt.Fprintf(r.w, "restart your Claude Code session in %s to load it\n", project)

	return &Result{
		Subcommand:          "install",
		Project:             project,
		MCPFile:             mcpFile,
		EntryPresent:        true,
		ServerScriptPresent: true,
	}, nil
}

// ---- uninstall --------------------------------------------------------------

func (r *runner) runUninstall() (*Result, error) {
	project, err := r.resolveProject()
	if err != nil {
		return nil, err
	}
	mcpFile := filepath.Join(project, ".mcp.json")

	if _, err := os.Stat(mcpFile); os.IsNotExist(err) {
		_, _ = fmt.Fprintf(r.w, "mcp uninstall: %s not present (nothing to do)\n", mcpFile)
		return &Result{
			Subcommand:   "uninstall",
			Project:      project,
			MCPFile:      mcpFile,
			EntryPresent: false,
		}, nil
	}

	data, err := os.ReadFile(mcpFile) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("mcp uninstall: could not read %s: %w", mcpFile, err)
	}

	var doc mcpDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("mcp uninstall: %s is not valid JSON", mcpFile)
	}

	if _, ok := doc.MCPServers[ServerName]; !ok {
		_, _ = fmt.Fprintf(r.w, "mcp uninstall: %s entry not present in %s\n", ServerName, mcpFile)
		return &Result{
			Subcommand:   "uninstall",
			Project:      project,
			MCPFile:      mcpFile,
			EntryPresent: false,
		}, nil
	}

	// Backup.
	bak := mcpFile + ".yakos-bak-" + r.isoTimestamp()
	if err := copyFile(mcpFile, bak); err != nil {
		return nil, fmt.Errorf("mcp uninstall: could not create backup: %w", err)
	}

	delete(doc.MCPServers, ServerName)
	if err := writeJSON(mcpFile, doc); err != nil {
		return nil, fmt.Errorf("mcp uninstall: rewrite failed; backup at %s: %w", bak, err)
	}

	_, _ = fmt.Fprintf(r.w, "removed %s from %s\n", ServerName, mcpFile)
	_, _ = fmt.Fprintf(r.w, "backup at %s\n", bak)

	return &Result{
		Subcommand:   "uninstall",
		Project:      project,
		MCPFile:      mcpFile,
		EntryPresent: false,
	}, nil
}

// ---- status -----------------------------------------------------------------

func (r *runner) runStatus() (*Result, error) {
	project, err := r.resolveProject()
	if err != nil {
		return nil, err
	}
	mcpFile := filepath.Join(project, ".mcp.json")
	script := r.serverScript()

	scriptPresent := false
	if _, err := os.Stat(script); err == nil {
		scriptPresent = true
	}

	scriptPresence := "(MISSING)"
	if scriptPresent {
		scriptPresence = "(present)"
	}

	_, _ = fmt.Fprintf(r.w, "yakos mcp status — project: %s\n", project)
	_, _ = fmt.Fprintf(r.w, "  server script: %s %s\n", script, scriptPresence)
	_, _ = fmt.Fprintf(r.w, "  .mcp.json:     %s\n", mcpFile)

	entryPresent := false
	if _, err := os.Stat(mcpFile); os.IsNotExist(err) {
		_, _ = fmt.Fprintf(r.w, "    not present — run 'yakos mcp install'\n")
	} else {
		data, rerr := os.ReadFile(mcpFile) //nolint:gosec
		if rerr != nil {
			_, _ = fmt.Fprintf(r.w, "    could not read: %v\n", rerr)
		} else {
			var doc mcpDoc
			if err := json.Unmarshal(data, &doc); err != nil {
				_, _ = fmt.Fprintf(r.w, "    not valid JSON\n")
			} else if _, ok := doc.MCPServers[ServerName]; ok {
				entryPresent = true
				_, _ = fmt.Fprintf(r.w, "    %s entry: PRESENT\n", ServerName)
				_, _ = fmt.Fprintf(r.w, "    --- entry ---\n")
				entry := doc.MCPServers[ServerName]
				enc, _ := json.MarshalIndent(entry, "    ", "  ")
				_, _ = fmt.Fprintf(r.w, "    %s\n", string(enc))
			} else {
				_, _ = fmt.Fprintf(r.w, "    %s entry: NOT INSTALLED (other servers may be present)\n", ServerName)
			}
		}
	}

	_, _ = fmt.Fprintln(r.w)
	_, _ = fmt.Fprintln(r.w, "  mcp python package:")
	pythonOK := r.execPython() == nil
	if pythonOK {
		_, _ = fmt.Fprintln(r.w, "    OK (importable)")
	} else {
		_, _ = fmt.Fprintln(r.w, "    MISSING — run 'pip install mcp'")
	}

	return &Result{
		Subcommand:          "status",
		Project:             project,
		MCPFile:             mcpFile,
		EntryPresent:        entryPresent,
		ServerScriptPresent: scriptPresent,
		PythonMCPImportable: pythonOK,
	}, nil
}

// ---- probe ------------------------------------------------------------------

func (r *runner) runProbe() (*Result, error) {
	ok := r.execPython() == nil
	if ok {
		_, _ = fmt.Fprintln(r.w, "yakos mcp probe: OK — 'mcp' package importable")
	} else {
		_, _ = fmt.Fprint(r.w, `yakos mcp probe: FAIL — 'mcp' Python package not installed.

Install it with:
  pip install mcp

The yakOS MCP server (cli/lib/mcp/yakos-mcp-server.py) needs this
package to talk to Claude Code via the Model Context Protocol.
`)
	}
	return &Result{
		Subcommand:          "probe",
		PythonMCPImportable: ok,
	}, nil
}

// ---- helpers ----------------------------------------------------------------

// defaultExecPython runs `python3 -c 'import mcp'` and reports whether it
// succeeded. This is the real implementation; tests inject ExecPython to avoid
// needing a real python3 installation.
func defaultExecPython() error {
	cmd := exec.Command("python3", "-c", "import mcp") //nolint:gosec
	return cmd.Run()
}

// GlobalMCPFile returns the conventional global MCP config path.
// On Windows this is %APPDATA%/claude/mcp.json (per Q3 planner recommendation).
// On Unix/macOS it is ~/.claude/mcp.json.
func GlobalMCPFile(homeDir string) string {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		return filepath.Join(appData, "claude", "mcp.json")
	}
	return filepath.Join(homeDir, ".claude", "mcp.json")
}

// writeJSON atomically writes v as pretty-printed JSON to path
// (temp-rename per Q8 discipline).
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// copyFile copies src to dst (for backups).
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644) //nolint:gosec
}

// ParseArgs parses the flags for install/uninstall/status subcommands.
// Returns the project override (empty string = use CWD).
// Returns an error for unknown flags.
func ParseArgs(sub string, args []string) (project string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--project":
			i++
			if i >= len(args) {
				return "", fmt.Errorf("mcp %s: --project requires a path", sub)
			}
			project = args[i]
		case strings.HasPrefix(arg, "--project="):
			project = arg[len("--project="):]
		case strings.HasPrefix(arg, "-"):
			return "", fmt.Errorf("mcp %s: unknown flag %q", sub, arg)
		default:
			return "", fmt.Errorf("mcp %s: unexpected arg %q", sub, arg)
		}
	}
	return project, nil
}
