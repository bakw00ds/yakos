// Package soul is the Go port of cli/lib/soul.sh (rank 24).
//
// A soul is the operator's personal identity/preferences file. yakOS reads it
// at session start and prepends it to the lead agent's system prompt (not
// specialists). Two layers are supported:
//
//   - global:  ~/.yakos-state/soul/global.md — same across projects
//   - project: ~/.yakos-state/soul/<project-slug>.md — per-project layer
//
// # Synopsis
//
//	yakos soul show    [global|project]
//	yakos soul edit    [global|project]
//	yakos soul history [global|project]
//	yakos soul revert  <version> [global|project]
//	yakos soul pending
//	yakos soul approve <slug>    (not yet implemented in M1)
//	yakos soul reject  <slug>    (not yet implemented in M1)
//
// # File layout
//
//	~/.yakos-state/soul/global.md
//	~/.yakos-state/soul/<project-slug>.md
//	~/.yakos-state/soul/history/<layer>-<iso>.md
//
// # Atomic writes
//
// All writes use temp-rename (Q8 / Decision A) to avoid partial writes.
package soul

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: show, edit, history, revert, pending, approve, reject.
	Subcommand string

	// Args holds positional arguments after the subcommand.
	Args []string

	// HomeDir overrides os.Getenv("HOME") for path resolution.
	// When empty, os.Getenv("HOME") is used.
	HomeDir string

	// YakosRoot is the absolute path to the yakos repo root (for template lookup).
	// When empty, the template fallback is used.
	YakosRoot string

	// ProjectDir overrides the project-slug resolution when set.
	// Useful for tests that need a known project directory.
	ProjectDir string

	// Now overrides time.Now() for snapshot timestamps. Tests inject a fixed time.
	Now time.Time

	// EditorFn overrides the editor invocation. When nil, the real EDITOR is used.
	// Signature: func(path string) error
	EditorFn func(string) error

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand is the subcommand that was executed.
	Subcommand string

	// Layer is the layer that was targeted (global|project|"").
	Layer string

	// File is the absolute path of the soul file touched (may be "" when the
	// file does not exist and subcommand is show).
	File string

	// SnapshotFile is the snapshot created before a mutating operation. Empty
	// when no snapshot was created.
	SnapshotFile string

	// PendingEdits lists the ## edit: headings found in soul-proposed-edits.md.
	// Non-nil only for the "pending" subcommand.
	PendingEdits []string
}

// ---- entry point ------------------------------------------------------------

// Run executes the soul command according to cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}

	soulDir := filepath.Join(home, ".yakos-state", "soul")
	historyDir := filepath.Join(soulDir, "history")

	switch cfg.Subcommand {
	case "show":
		return runShow(cfg, soulDir)
	case "edit":
		return runEdit(cfg, soulDir, historyDir, home)
	case "history":
		return runHistory(cfg, historyDir)
	case "revert":
		return runRevert(cfg, soulDir, historyDir)
	case "pending":
		return runPending(cfg, home)
	case "approve":
		return runApprove(cfg)
	case "reject":
		return runReject(cfg)
	default:
		return nil, fmt.Errorf("soul: unknown subcommand %q (try 'yakos soul help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos soul — operator-personal soul files (loaded by lead at session start)

  yakos soul show [global|project]              # print current
  yakos soul edit [global|project]              # open in $EDITOR (creates from template if absent)
  yakos soul history [global|project]           # list version snapshots
  yakos soul revert <version> [global|project]  # revert to snapshot
  yakos soul pending                            # list librarian-proposed edits in current session
  yakos soul approve <edit-slug>                # apply pending edit (M1+ when librarian dispatched)
  yakos soul reject <edit-slug>                 # discard pending edit

Soul files live at ~/.yakos-state/soul/{global,<project-slug>}.md
Splicing into the lead's system prompt happens at session start
(see cli/lib/agents-compose.sh::yk_agents_apply_soul).
`)
}

// ---- subcommand implementations ---------------------------------------------

func runShow(cfg Config, soulDir string) (*Result, error) {
	layer := layerArg(cfg.Args, "global")
	file, err := resolveLayerFile(cfg, soulDir, layer)
	if err != nil {
		return nil, err
	}

	res := &Result{Subcommand: "show", Layer: layer, File: file}

	data, err := os.ReadFile(file) //nolint:gosec
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintf(cfg.Writer,
			"soul: no soul file at %s (run 'yakos soul edit %s' to create)\n", file, layer)
		res.File = ""
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("soul: reading %s: %w", file, err)
	}

	_, _ = cfg.Writer.Write(data)
	return res, nil
}

func runEdit(cfg Config, soulDir, historyDir, home string) (*Result, error) {
	layer := layerArg(cfg.Args, "global")
	file, err := resolveLayerFile(cfg, soulDir, layer)
	if err != nil {
		return nil, err
	}

	res := &Result{Subcommand: "edit", Layer: layer, File: file}

	if err := os.MkdirAll(soulDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("soul: mkdir %s: %w", soulDir, err)
	}

	// Snapshot before edit so revert works (mirrors bash: snapshot only when file exists).
	snap, err := snapshot(file, layer, historyDir, cfg.Now)
	if err != nil {
		return nil, err
	}
	res.SnapshotFile = snap

	// Seed from template if file does not exist.
	if _, statErr := os.Stat(file); os.IsNotExist(statErr) {
		if err := seedSoulFile(cfg, file, layer, home); err != nil {
			return nil, err
		}
	}

	// Open in editor.
	if cfg.EditorFn != nil {
		if err := cfg.EditorFn(file); err != nil {
			return nil, fmt.Errorf("soul: editor: %w", err)
		}
	} else {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = os.Getenv("VISUAL")
		}
		if editor == "" {
			editor = "vi"
		}
		// We run the editor by delegating to exec; in the Go port this requires
		// spawning a child process.
		if err := runEditor(editor, file); err != nil {
			return nil, fmt.Errorf("soul: editor: %w", err)
		}
	}

	_, _ = fmt.Fprintf(cfg.Writer, "soul: edited %s\n", file)
	return res, nil
}

func runHistory(cfg Config, historyDir string) (*Result, error) {
	layer := layerArg(cfg.Args, "global")
	res := &Result{Subcommand: "history", Layer: layer}

	if _, err := os.Stat(historyDir); os.IsNotExist(err) {
		_, _ = fmt.Fprintln(cfg.Writer, "(no history)")
		return res, nil
	}

	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return nil, fmt.Errorf("soul: reading history dir: %w", err)
	}

	prefix := layer + "-"
	suffix := ".md"
	var found []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			found = append(found, filepath.Join(historyDir, name))
		}
	}

	if len(found) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "(no history)")
		return res, nil
	}

	sort.Strings(found)

	for _, snapPath := range found {
		base := filepath.Base(snapPath)
		// Strip "<layer>-" prefix and ".md" suffix to get the timestamp.
		ts := strings.TrimPrefix(base, prefix)
		ts = strings.TrimSuffix(ts, suffix)

		fi, err := os.Stat(snapPath)
		size := int64(0)
		if err == nil {
			size = fi.Size()
		}
		_, _ = fmt.Fprintf(cfg.Writer, "  %s  (%d bytes)\n", ts, size)
	}

	return res, nil
}

func runRevert(cfg Config, soulDir, historyDir string) (*Result, error) {
	if len(cfg.Args) == 0 {
		return nil, fmt.Errorf("soul: revert requires a <version> argument")
	}
	version := cfg.Args[0]
	layer := "global"
	if len(cfg.Args) >= 2 {
		layer = cfg.Args[1]
	}

	file, err := resolveLayerFile(cfg, soulDir, layer)
	if err != nil {
		return nil, err
	}

	snap := filepath.Join(historyDir, layer+"-"+version+".md")
	if _, err := os.Stat(snap); err != nil {
		return nil, fmt.Errorf("soul: no snapshot '%s-%s.md' in %s", layer, version, historyDir)
	}

	res := &Result{Subcommand: "revert", Layer: layer, File: file}

	// Snapshot current before reverting.
	currentSnap, err := snapshot(file, layer, historyDir, cfg.Now)
	if err != nil {
		return nil, err
	}
	res.SnapshotFile = currentSnap

	// Read snapshot data.
	data, err := os.ReadFile(snap) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("soul: reading snapshot: %w", err)
	}

	// Atomic write of reverted content.
	if err := atomicWrite(file, string(data)); err != nil {
		return nil, fmt.Errorf("soul: writing reverted file: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "soul: reverted %s to %s\n", layer, version)
	return res, nil
}

func runPending(cfg Config, home string) (*Result, error) {
	res := &Result{Subcommand: "pending"}

	// Resolve project slug.
	slug := resolveProjectSlug(cfg, home)
	if slug == "" {
		return nil, fmt.Errorf("soul: no active project session detected")
	}

	pendingFile := filepath.Join(home, "agent-control", slug, "work", "current", "soul-proposed-edits.md")
	data, err := os.ReadFile(pendingFile) //nolint:gosec
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintln(cfg.Writer, "(no pending soul-edit proposals)")
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("soul: reading pending file: %w", err)
	}

	content := string(data)
	var edits []string
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## edit: ") {
			edits = append(edits, line)
		}
	}

	if len(edits) == 0 {
		_, _ = fmt.Fprintf(cfg.Writer, "(no pending edits in %s)\n", pendingFile)
	} else {
		for _, e := range edits {
			_, _ = fmt.Fprintln(cfg.Writer, e)
		}
	}

	res.PendingEdits = edits
	return res, nil
}

func runApprove(cfg Config) (*Result, error) {
	_, _ = fmt.Fprintln(cfg.Writer, "soul approve: not yet implemented (M1 ships read/edit/history; approve/reject deferred to when librarian is dispatched)")
	_, _ = fmt.Fprintln(cfg.Writer, "      proposed-edit format: see librarian agent + framework-internal-plan.md §3.4")
	return &Result{Subcommand: "approve"}, nil
}

func runReject(cfg Config) (*Result, error) {
	_, _ = fmt.Fprintln(cfg.Writer, "soul reject: not yet implemented (same status as 'soul approve' above)")
	return &Result{Subcommand: "reject"}, nil
}

// ---- helpers ----------------------------------------------------------------

// layerArg returns the first arg if present, otherwise def.
func layerArg(args []string, def string) string {
	if len(args) > 0 {
		return args[0]
	}
	return def
}

// resolveLayerFile maps a layer name to its soul file path.
func resolveLayerFile(cfg Config, soulDir, layer string) (string, error) {
	switch layer {
	case "global":
		return filepath.Join(soulDir, "global.md"), nil
	case "project":
		home := cfg.HomeDir
		if home == "" {
			home = os.Getenv("HOME")
		}
		slug := resolveProjectSlug(cfg, home)
		if slug == "" {
			return "", fmt.Errorf("soul: cannot resolve project layer (no active project detected)")
		}
		return filepath.Join(soulDir, slug+".md"), nil
	default:
		return "", fmt.Errorf("soul: unknown layer %q (use 'global' or 'project')", layer)
	}
}

// resolveProjectSlug determines the current project slug.
// It checks cfg.ProjectDir first, then YAKOS_PROJECT_DIR env, then walks
// ~/agent-control/*/project-path files.
func resolveProjectSlug(cfg Config, home string) string {
	// Config override for tests.
	if cfg.ProjectDir != "" {
		return filepath.Base(cfg.ProjectDir)
	}

	// Environment variable.
	if v := os.Getenv("YAKOS_PROJECT_DIR"); v != "" {
		return filepath.Base(v)
	}

	if home == "" {
		return ""
	}

	// Walk ~/agent-control/*/project-path.
	acRoot := filepath.Join(home, "agent-control")
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return ""
	}

	cwd, _ := os.Getwd()
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
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
		if p == "" {
			continue
		}
		pr, err := filepath.EvalSymlinks(p)
		if err != nil {
			pr = p
		}
		if cwd == pr || strings.HasPrefix(cwd, pr+string(filepath.Separator)) {
			return e.Name()
		}
	}

	return ""
}

// snapshot copies file to historyDir/<layer>-<ts>.md and returns the snapshot path.
// Returns "" (no error) when the source file does not exist yet.
func snapshot(file, layer, historyDir string, now time.Time) (string, error) {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return "", nil
	}

	if err := os.MkdirAll(historyDir, 0755); err != nil { //nolint:gosec
		return "", fmt.Errorf("soul: mkdir history: %w", err)
	}

	ts := now.UTC().Format("2006-01-02T15:04:05Z")
	snapPath := filepath.Join(historyDir, layer+"-"+ts+".md")

	data, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		return "", fmt.Errorf("soul: reading file for snapshot: %w", err)
	}

	if err := atomicWrite(snapPath, string(data)); err != nil {
		return "", fmt.Errorf("soul: writing snapshot: %w", err)
	}

	return snapPath, nil
}

// seedSoulFile creates the soul file from template (or a bare default when the
// template is absent).
func seedSoulFile(cfg Config, file, layer, home string) error {
	content := bareDefaultContent(layer, home)

	// Try to read the template.
	if cfg.YakosRoot != "" {
		tmplPath := filepath.Join(cfg.YakosRoot, "lib", "settings", "soul.template.md")
		data, err := os.ReadFile(tmplPath) //nolint:gosec
		if err == nil {
			user := os.Getenv("USER")
			if user == "" {
				user = os.Getenv("USERNAME")
			}
			if user == "" {
				user = "operator"
			}
			ts := cfg.Now.UTC().Format("2006-01-02T15:04:05Z")
			content = string(data)
			content = strings.ReplaceAll(content, "{{layer}}", layer)
			content = strings.ReplaceAll(content, "{{ts}}", ts)
			content = strings.ReplaceAll(content, "{{user}}", user)
		}
	}

	return atomicWrite(file, content)
}

// bareDefaultContent returns a minimal soul file body when the template is unavailable.
func bareDefaultContent(layer, home string) string {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	if user == "" {
		if home != "" {
			user = filepath.Base(home)
		} else {
			user = "operator"
		}
	}
	return fmt.Sprintf("# Soul (%s) — %s\n\n## About me\n\n## Voice / tone preferences\n\n## Recurring concerns\n\n## Stack expertise\n\n", layer, user)
}

// runEditor spawns editor as a child process with the given file.
func runEditor(editor, file string) error {
	cmd := exec.Command(editor, file) //nolint:gosec
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// atomicWrite writes content to path using a temp-file + rename (Q8 / Decision A).
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".yakos-soul-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}
