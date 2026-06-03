// Package plugin is the Go port of cli/lib/plugin.sh (rank 22).
//
// It manages external runtime adapters installed under ~/.yakos/plugins/.
// Each plugin is a directory containing a runtime.sh that implements the
// yakOS runtime contract (documented in cli/lib/runtimes/README.md and
// docs/plugin-spec.md).
//
// # Subcommands
//
//   - list                   — list installed plugins (id, version, status)
//   - install <source>       — install from a git URL or local directory path
//   - remove <id>            — remove an installed plugin
//   - validate <dir>         — validate a plugin directory without installing
//   - status                 — alias for list (matches bash plugin.sh list behaviour)
//   - register <name> <dir>  — validate + symlink a local dir as a named plugin
//
// # Plugin directory layout
//
//	~/.yakos/plugins/<id>/
//	  runtime.sh    — required; implements the runtime contract
//	  VERSION       — optional; printed by list
//
// The built-in runtimes claude, codex, and gemini are reserved IDs. Attempting
// to install or remove them is an error matching the bash behaviour.
//
// # Validation
//
// plugin_validate in the bash source soft-checks that runtime.sh exists and
// that each of the eight expected yk_rt_<id>_<fn>() function headers is
// present. The Go port mirrors this exactly.
//
// # Install sources
//
// A source is classified as a git URL if it begins with "http" or "git@";
// everything else is treated as a local directory path. Git installs require
// git(1) on PATH. Local installs use os.CopyFS / filepath.Walk.
//
// # Idempotency note
//
// install with --force removes the destination before copying/cloning. Without
// --force, a pre-existing destination is an error matching bash behaviour.
package plugin

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: list, install, remove, validate, status, register.
	// Required.
	Subcommand string

	// Source is the git URL or local directory path for install.
	Source string

	// ID is the plugin id override (--id flag for install; positional for
	// remove/register).
	ID string

	// Dir is the local directory path for validate and register.
	Dir string

	// Force allows overwriting an existing plugin (--force for install).
	Force bool

	// PluginsDir overrides the default ~/.yakos/plugins directory.
	// When empty, resolved from HOME.
	PluginsDir string

	// HomeDir overrides os.Getenv("HOME") for path resolution in tests.
	HomeDir string

	// GitExecFn is the function used to run "git clone". Defaults to the real
	// git binary. Injectable for tests.
	GitExecFn func(args ...string) error

	// Writer receives normal output. Default: os.Stdout.
	Writer io.Writer

	// ErrWriter receives error/advisory output. Default: os.Stderr.
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Installed is the plugin id that was installed (install/register only).
	Installed string

	// Removed is the plugin id that was removed (remove only).
	Removed string

	// Plugins is the list of plugins found (list/status only).
	Plugins []PluginInfo
}

// PluginInfo describes one installed plugin.
type PluginInfo struct {
	// ID is the plugin's directory name under ~/.yakos/plugins/.
	ID string

	// Version is the content of the VERSION file, or "(unversioned)" when
	// the file is absent. "(broken)" when runtime.sh is missing.
	Version string

	// Valid is true when runtime.sh exists.
	Valid bool
}

// builtins are the reserved plugin ids that shadow built-in runtimes.
var builtins = []string{"claude", "codex", "gemini"}

// requiredFunctions is the set of per-runtime function name suffixes that
// plugin_validate checks for in runtime.sh.
var requiredFunctions = []string{
	"id", "check_cli", "check_auth", "capabilities",
	"materialize_agents", "cleanup_agents", "launch", "dispatch",
}

// ---- Run --------------------------------------------------------------------

// Run executes the plugin subcommand described by cfg.
func Run(cfg Config) (Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.GitExecFn == nil {
		cfg.GitExecFn = defaultGitExec
	}

	pluginsDir := cfg.PluginsDir
	if pluginsDir == "" {
		home := cfg.HomeDir
		if home == "" {
			home = os.Getenv("HOME")
		}
		if home == "" {
			home = "/tmp"
		}
		pluginsDir = filepath.Join(home, ".yakos", "plugins")
	}

	switch cfg.Subcommand {
	case "list", "status", "":
		return runList(cfg, pluginsDir)
	case "install":
		return runInstall(cfg, pluginsDir)
	case "remove":
		return runRemove(cfg, pluginsDir)
	case "validate":
		return runValidate(cfg)
	case "register":
		return runRegister(cfg, pluginsDir)
	default:
		return Result{}, fmt.Errorf("plugin: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp writes the --help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos plugin <subcommand> [args...]

Subcommands:
  install <source> [--id <id>] [--force]
                       Install a runtime plugin from <source>:
                         - git URL: https://... or git@...
                         - local path: a directory containing runtime.sh
                       --id overrides the inferred plugin id (default:
                       basename of source).
                       --force overwrites an existing plugin.

  list                 List installed plugins (also: status).

  remove <id>          Remove the plugin at ~/.yakos/plugins/<id>/.

  validate <dir>       Validate a plugin directory without installing it.
                       Checks for runtime.sh and the expected function
                       headers. Exits 0 on success.

  register <name> <dir>
                       Validate and register a local directory as a named
                       plugin. Creates a copy under ~/.yakos/plugins/<name>/.
                       Equivalent to: yakos plugin install <dir> --id <name>

  status               Alias for list.

Plugins live at ~/.yakos/plugins/<id>/runtime.sh.
The runtime.sh contract is documented in
  cli/lib/runtimes/README.md
and  docs/plugin-spec.md.

Examples:
  yakos plugin install https://github.com/user/yakos-cursor-runtime
  yakos plugin install ~/code/my-cursor-adapter --id cursor
  yakos plugin list
  yakos plugin validate ~/code/my-cursor-adapter
  yakos plugin register cursor ~/code/my-cursor-adapter
  yakos plugin remove cursor
`)
}

// ---- list / status ----------------------------------------------------------

func runList(cfg Config, pluginsDir string) (Result, error) {
	entries, err := os.ReadDir(pluginsDir)
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintf(cfg.Writer, "no plugins installed (dir %s does not exist)\n", pluginsDir)
		return Result{}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("plugin list: read dir %s: %w", pluginsDir, err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos plugins (%s):\n", pluginsDir)

	var plugins []PluginInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		dir := filepath.Join(pluginsDir, id)
		runtimePath := filepath.Join(dir, "runtime.sh")
		_, rErr := os.Stat(runtimePath)
		if rErr != nil {
			// Broken plugin: no runtime.sh.
			_, _ = fmt.Fprintf(cfg.Writer, "  %-20s (no runtime.sh — broken plugin)\n", id)
			plugins = append(plugins, PluginInfo{ID: id, Version: "(broken)", Valid: false})
			continue
		}
		ver := version(dir)
		_, _ = fmt.Fprintf(cfg.Writer, "  %-20s %s\n", id, ver)
		plugins = append(plugins, PluginInfo{ID: id, Version: ver, Valid: true})
	}

	if len(plugins) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "  (none)")
	}

	return Result{Plugins: plugins}, nil
}

// version reads the VERSION file from dir, returning "(unversioned)" if absent.
func version(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "VERSION")) //nolint:gosec
	if err != nil {
		return "(unversioned)"
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "(unversioned)"
	}
	return v
}

// ---- install ----------------------------------------------------------------

func runInstall(cfg Config, pluginsDir string) (Result, error) {
	if cfg.Source == "" {
		return Result{}, fmt.Errorf("plugin install: <source> required (try --help)")
	}

	if err := os.MkdirAll(pluginsDir, 0755); err != nil { //nolint:gosec
		return Result{}, fmt.Errorf("plugin install: mkdir %s: %w", pluginsDir, err)
	}

	id := cfg.ID
	if id == "" {
		id = inferID(cfg.Source)
	}

	if err := validateID(id); err != nil {
		return Result{}, fmt.Errorf("plugin install: %w", err)
	}
	if isBuiltin(id) {
		return Result{}, fmt.Errorf("plugin install: %q shadows a built-in runtime; pass --id <other>", id)
	}

	dest := filepath.Join(pluginsDir, id)
	if _, err := os.Stat(dest); err == nil {
		if !cfg.Force {
			return Result{}, fmt.Errorf("plugin install: %s already exists (use --force to overwrite)", dest)
		}
		if err := os.RemoveAll(dest); err != nil {
			return Result{}, fmt.Errorf("plugin install: remove %s: %w", dest, err)
		}
	}

	if isGitURL(cfg.Source) {
		if err := cfg.GitExecFn("clone", "--depth", "1", cfg.Source, dest); err != nil {
			return Result{}, fmt.Errorf("plugin install: git clone failed: %w", err)
		}
	} else {
		if fi, err := os.Stat(cfg.Source); err != nil || !fi.IsDir() {
			return Result{}, fmt.Errorf("plugin install: local path %q is not a directory", cfg.Source)
		}
		if err := copyDir(cfg.Source, dest); err != nil {
			return Result{}, fmt.Errorf("plugin install: copy %s → %s: %w", cfg.Source, dest, err)
		}
	}

	// Validate after install; roll back on failure.
	if err := validatePlugin(id, dest, cfg.ErrWriter); err != nil {
		_, _ = fmt.Fprintln(cfg.ErrWriter, "plugin install: validation failed; rolling back")
		_ = os.RemoveAll(dest)
		return Result{}, err
	}

	// Make runtime.sh executable (best-effort; matches bash behaviour).
	_ = os.Chmod(filepath.Join(dest, "runtime.sh"), 0755) //nolint:gosec

	_, _ = fmt.Fprintf(cfg.Writer, "installed plugin: %s at %s\n", id, dest)
	_, _ = fmt.Fprintf(cfg.Writer, "  Use via: yakos start --runtime %s  /  yakos dispatch <agent> ... --runtime %s\n", id, id)
	_, _ = fmt.Fprintf(cfg.Writer, "  Or set in agent frontmatter: runtime: %s\n", id)

	return Result{Installed: id}, nil
}

// ---- remove -----------------------------------------------------------------

func runRemove(cfg Config, pluginsDir string) (Result, error) {
	id := cfg.ID
	if id == "" {
		return Result{}, fmt.Errorf("plugin remove: <id> required (try --help)")
	}
	if isBuiltin(id) {
		return Result{}, fmt.Errorf("plugin remove: %q is a built-in, cannot remove", id)
	}

	dest := filepath.Join(pluginsDir, id)
	if _, err := os.Stat(dest); os.IsNotExist(err) {
		return Result{}, fmt.Errorf("plugin remove: %s not found", dest)
	}
	if err := os.RemoveAll(dest); err != nil {
		return Result{}, fmt.Errorf("plugin remove: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "removed plugin: %s\n", id)
	return Result{Removed: id}, nil
}

// ---- validate ---------------------------------------------------------------

func runValidate(cfg Config) (Result, error) {
	dir := cfg.Dir
	if dir == "" {
		return Result{}, fmt.Errorf("plugin validate: <dir> required (try --help)")
	}

	// Infer id from dir name when not provided.
	id := cfg.ID
	if id == "" {
		id = filepath.Base(dir)
	}

	if err := validatePlugin(id, dir, cfg.ErrWriter); err != nil {
		return Result{}, err
	}

	_, _ = fmt.Fprintf(cfg.Writer, "plugin %q: valid\n", id)
	return Result{}, nil
}

// ---- register ---------------------------------------------------------------

// runRegister validates a local dir and installs it as id in pluginsDir.
// It is sugar for: yakos plugin install <dir> --id <name>
func runRegister(cfg Config, pluginsDir string) (Result, error) {
	if cfg.ID == "" {
		return Result{}, fmt.Errorf("plugin register: <name> required (try --help)")
	}
	if cfg.Dir == "" {
		return Result{}, fmt.Errorf("plugin register: <dir> required (try --help)")
	}

	installCfg := cfg
	installCfg.Subcommand = "install"
	installCfg.Source = cfg.Dir
	// ID already set.
	return runInstall(installCfg, pluginsDir)
}

// ---- validation helpers -----------------------------------------------------

// validatePlugin checks that dir/runtime.sh exists and contains all required
// function headers. Mirrors plugin_validate() in plugin.sh exactly.
//
// The function-header check is a grep-based soft check: it looks for a line
// matching ^[[:space:]]*yk_rt_<id>_<fn>[[:space:]]*( in runtime.sh.
// A syntax error or missing function only surfaces on use (lazy sourcing).
func validatePlugin(id, dir string, errW io.Writer) error {
	runtimePath := filepath.Join(dir, "runtime.sh")
	if _, err := os.Stat(runtimePath); os.IsNotExist(err) {
		return fmt.Errorf("plugin %q: missing required runtime.sh", id)
	}

	data, err := os.ReadFile(runtimePath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("plugin %q: cannot read runtime.sh: %w", id, err)
	}
	content := string(data)

	missing := 0
	for _, fn := range requiredFunctions {
		funcName := fmt.Sprintf("yk_rt_%s_%s", id, fn)
		// Pattern: ^[[:space:]]*yk_rt_<id>_<fn>[[:space:]]*(
		pattern := `(?m)^\s*` + regexp.QuoteMeta(funcName) + `\s*\(`
		matched, _ := regexp.MatchString(pattern, content)
		if !matched {
			_, _ = fmt.Fprintf(errW, "plugin %q: missing %s() in runtime.sh\n", id, funcName)
			missing++
		}
	}

	if missing > 0 {
		return fmt.Errorf("plugin %q: validation failed (%d missing function(s))", id, missing)
	}
	return nil
}

// ---- ID helpers -------------------------------------------------------------

// validIDPattern matches IDs composed of alphanumerics, hyphens, and underscores.
var validIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateID returns an error when id contains invalid characters.
func validateID(id string) error {
	if !validIDPattern.MatchString(id) {
		return fmt.Errorf("id %q must be alphanumeric (-_)", id)
	}
	return nil
}

// isBuiltin reports whether id names a built-in runtime.
func isBuiltin(id string) bool {
	for _, b := range builtins {
		if id == b {
			return true
		}
	}
	return false
}

// isGitURL reports whether source is a git URL (http/https or git@).
func isGitURL(source string) bool {
	return strings.HasPrefix(source, "http") || strings.HasPrefix(source, "git@")
}

// inferID derives the plugin id from the install source.
// Mirrors the bash logic:
//
//	*.git → strip .git suffix from basename
//	http*/git@* → basename, strip .git
//	other → basename
//
// After basename extraction the "yakos-runtime-" and "yakos-" prefixes are
// stripped (matching the bash ID="${ID#yakos-runtime-}" strip).
func inferID(source string) string {
	base := filepath.Base(source)
	// Strip .git suffix.
	base = strings.TrimSuffix(base, ".git")
	// Strip yakos-runtime- then yakos- prefix.
	base = strings.TrimPrefix(base, "yakos-runtime-")
	base = strings.TrimPrefix(base, "yakos-")
	return base
}

// ---- file-copy helper -------------------------------------------------------

// copyDir copies src recursively to dst, creating directories as needed.
// Mirrors the bash `cp -R` behaviour.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, fi.Mode())
		}
		return copyFile(path, target, fi.Mode())
	})
}

// copyFile copies a single file from src to dst preserving permissions.
func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// ---- git helper -------------------------------------------------------------

// defaultGitExec runs the real "git" binary with args.
func defaultGitExec(args ...string) error {
	cmd := exec.Command("git", args...) //nolint:gosec
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}
