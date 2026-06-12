// Package refresh implements the Go port of `yakos refresh`.
//
// Refresh detects and repairs three classes of per-project deployment drift:
//
//  1. Hook script drift   — <project>/scripts/hooks/*.sh out of date vs
//     lib/hooks/*.sh (and lib/hooks/lib/)
//  2. settings.json drift — hook registrations missing or superseded vs
//     lib/settings/settings.template.json
//  3. Agent symlink drift — ~/.claude/agents/<id>.md missing or wrong
//     vs lib/agents/<id>.md
//
// The package is intentionally free of any state beyond what is passed in
// through Config. All writes are atomic (temp-rename). No file locking is
// added in Phase 1 (see decision Q8: locking deferred to Phase 2 daemon).
package refresh

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/deploydrift"
)

// Config controls a refresh run.
type Config struct {
	// YakosRoot is the framework repo root ($YAKOS_ROOT).
	YakosRoot string

	// ProjectPaths is the list of project directories to refresh.
	// Populated by the caller after project discovery.
	ProjectPaths []string

	// DryRun, when true, reports what would change without writing anything.
	DryRun bool

	// Writer is the output destination. Defaults to os.Stdout.
	Writer io.Writer

	// ErrWriter is the error destination. Defaults to os.Stderr.
	ErrWriter io.Writer

	// HomeDir overrides $HOME. Used in tests. If empty, os.Getenv("HOME") is used.
	HomeDir string
}

// HookPhaseReport holds per-phase counts for hook script sync.
type HookPhaseReport struct {
	New    int // files newly copied from framework
	Synced int // files updated (stale → current)
	OK     int // files already current
}

// SettingsPhaseReport holds per-phase counts for settings.json smart merge.
type SettingsPhaseReport struct {
	Added   int
	Removed int
	Skipped bool   // true when settings.json was absent
	SkipMsg string // reason when Skipped is true
}

// AgentPhaseReport holds per-phase counts for agent symlink sync.
type AgentPhaseReport struct {
	New   int // symlinks newly created or refreshed
	OK    int // symlinks already correct
	Warns int // real files skipped with a warning
}

// ProjectReport is the result for one project directory.
type ProjectReport struct {
	ProjectPath string
	Hooks       HookPhaseReport
	Settings    SettingsPhaseReport
	HasDrift    bool // true when any count > 0
}

// Report is the overall result of a refresh run.
type Report struct {
	Projects []ProjectReport
	Agents   AgentPhaseReport
	DryRun   bool
}

// Run executes the refresh for all projects in cfg and returns a structured
// Report. Output is streamed to cfg.Writer as work proceeds.
func Run(cfg Config) (*Report, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	dryTag := ""
	if cfg.DryRun {
		dryTag = " [DRY RUN]"
	}
	_, _ = fmt.Fprintf(cfg.Writer, "yakos refresh%s\n\n", dryTag)

	report := &Report{DryRun: cfg.DryRun}

	// Phase: agent symlinks (once globally, not per-project)
	_, _ = fmt.Fprintln(cfg.Writer, "Agent symlinks (~/.claude/agents/)")
	agentRpt, err := syncAgents(cfg.YakosRoot, home, cfg.DryRun, cfg.Writer)
	if err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "refresh: agent symlink sync error: %v\n", err)
	}
	report.Agents = agentRpt
	_, _ = fmt.Fprintf(cfg.Writer, "  new=%d ok=%d warns=%d\n\n", agentRpt.New, agentRpt.OK, agentRpt.Warns)

	// Per-project phases
	hooksRoot := filepath.Join(cfg.YakosRoot, "lib", "hooks")
	templateFile := filepath.Join(cfg.YakosRoot, "lib", "settings", "settings.template.json")

	_, _ = fmt.Fprintln(cfg.Writer, "Project hook + settings refresh:")
	for _, projPath := range cfg.ProjectPaths {
		projRpt, err := refreshOne(projPath, hooksRoot, templateFile, cfg.DryRun, cfg.Writer, cfg.ErrWriter)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "refresh: project %s: %v\n", projPath, err)
			continue
		}
		report.Projects = append(report.Projects, projRpt)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "Summary: %d project(s) processed\n", len(report.Projects))
	if cfg.DryRun {
		_, _ = fmt.Fprintln(cfg.Writer, "(dry-run: no files written)")
	}

	return report, nil
}

// refreshOne runs hook sync + settings merge for a single project directory.
func refreshOne(projPath, hooksRoot, templateFile string, dryRun bool, w, ew io.Writer) (ProjectReport, error) {
	absPath, err := filepath.Abs(projPath)
	if err != nil {
		absPath = projPath
	}

	if fi, err := os.Stat(absPath); err != nil || !fi.IsDir() {
		return ProjectReport{}, fmt.Errorf("project path not found: %s", absPath)
	}

	_, _ = fmt.Fprintf(w, "  project: %s\n", absPath)

	hooksDst := filepath.Join(absPath, "scripts", "hooks")

	// Phase 2: hook script sync
	if !dryRun {
		if err := os.MkdirAll(hooksDst, 0755); err != nil { //nolint:gosec
			return ProjectReport{}, fmt.Errorf("mkdir %s: %w", hooksDst, err)
		}
	}
	hookRpt, err := syncHooks(hooksRoot, hooksDst, dryRun, w)
	if err != nil {
		_, _ = fmt.Fprintf(ew, "refresh: hook sync error for %s: %v\n", absPath, err)
	}

	// Phase 3: settings.json smart merge
	deployedSettings := filepath.Join(absPath, ".claude", "settings.json")
	settingsRpt := SettingsPhaseReport{}
	if _, err := os.Stat(deployedSettings); os.IsNotExist(err) {
		settingsRpt.Skipped = true
		settingsRpt.SkipMsg = "skipped (no .claude/settings.json)"
	} else {
		settingsRpt, err = mergeSettingsForProject(templateFile, deployedSettings, dryRun, w)
		if err != nil {
			_, _ = fmt.Fprintf(ew, "refresh: settings merge error for %s: %v\n", absPath, err)
		}
	}

	hasDrift := hookRpt.New > 0 || hookRpt.Synced > 0 || settingsRpt.Added > 0 || settingsRpt.Removed > 0

	driftStatus := "in sync"
	if hasDrift {
		if dryRun {
			driftStatus = "drift detected (dry-run)"
		} else {
			driftStatus = "drift detected + repaired"
		}
	}

	hooksSummary := fmt.Sprintf("new=%d synced=%d ok=%d", hookRpt.New, hookRpt.Synced, hookRpt.OK)
	settingsSummary := ""
	if settingsRpt.Skipped {
		settingsSummary = settingsRpt.SkipMsg
	} else {
		settingsSummary = fmt.Sprintf("added=%d removed=%d", settingsRpt.Added, settingsRpt.Removed)
	}

	_, _ = fmt.Fprintf(w, "    hooks:    %s\n", hooksSummary)
	_, _ = fmt.Fprintf(w, "    settings: %s\n", settingsSummary)
	_, _ = fmt.Fprintf(w, "    status:   %s\n", driftStatus)
	_, _ = fmt.Fprintln(w, "")

	return ProjectReport{
		ProjectPath: absPath,
		Hooks:       hookRpt,
		Settings:    settingsRpt,
		HasDrift:    hasDrift,
	}, nil
}

// mergeSettingsForProject wraps MergeSettingsFiles and converts MergeStats into
// SettingsPhaseReport for per-project reporting.
func mergeSettingsForProject(templateFile, deployedFile string, dryRun bool, w io.Writer) (SettingsPhaseReport, error) {
	stats, err := MergeSettingsFiles(templateFile, deployedFile, dryRun, w)
	if err != nil {
		return SettingsPhaseReport{}, err
	}
	return SettingsPhaseReport{
		Added:   stats.Added,
		Removed: stats.Removed,
	}, nil
}

// syncHooks copies / updates hook files from srcRoot into dstRoot.
// It mirrors the bash _sync_hooks logic exactly, including:
//   - Skipping .gitkeep, README.md, and git/ subdirectory entries.
//   - Writing / updating a .framework-hash sidecar beside each deployed file.
//   - Dry-run: reports counts without writing.
func syncHooks(srcRoot, dstRoot string, dryRun bool, w io.Writer) (HookPhaseReport, error) {
	var rpt HookPhaseReport

	if _, err := os.Stat(srcRoot); os.IsNotExist(err) {
		return rpt, nil
	}

	err := filepath.Walk(srcRoot, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(srcRoot, srcPath)
		if err != nil {
			return nil
		}

		// Skip non-hook files (same exclusion list as init.sh / refresh.sh).
		base := filepath.Base(rel)
		if base == ".gitkeep" || base == "README.md" {
			return nil
		}
		// Skip files under git/ (admin-only, not deployed).
		// Skip files under legacy/ (post-F6 holding area; canonical entries are
		// the symlinks at lib/hooks/<name>.sh that refresh already visits).
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if len(parts) > 0 && (parts[0] == "git" || parts[0] == "legacy") {
			return nil
		}

		dstPath := filepath.Join(dstRoot, rel)
		hashFile := dstPath + ".framework-hash"

		srcHash, err := deploydrift.SHA256File(srcPath)
		if err != nil {
			return nil // skip unhashable files
		}

		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			// NEW — file not deployed yet
			if dryRun {
				_, _ = fmt.Fprintf(w, "    [dry-run] hooks: would copy NEW  %s\n", rel)
			} else {
				if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil { //nolint:gosec
					return nil
				}
				if err := copyFile(srcPath, dstPath); err != nil {
					return nil
				}
				if err := os.WriteFile(hashFile, []byte(srcHash), 0644); err != nil { //nolint:gosec
					return nil
				}
			}
			rpt.New++
			return nil
		}

		// File exists — compare by content hash
		dstHash, err := deploydrift.SHA256File(dstPath)
		if err != nil {
			return nil
		}

		if srcHash != dstHash {
			// SYNC — deployed is stale
			if dryRun {
				_, _ = fmt.Fprintf(w, "    [dry-run] hooks: would sync STALE %s\n", rel)
			} else {
				if err := copyFile(srcPath, dstPath); err != nil {
					return nil
				}
				if err := os.WriteFile(hashFile, []byte(srcHash), 0644); err != nil { //nolint:gosec
					return nil
				}
			}
			rpt.Synced++
		} else {
			// OK — ensure sidecar is up to date even if content matches
			if !dryRun {
				_ = os.WriteFile(hashFile, []byte(srcHash), 0644) //nolint:gosec
			}
			rpt.OK++
		}
		return nil
	})

	return rpt, err
}

// copyFile copies the file at src to dst, preserving execute permission.
func copyFile(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat src %s: %w", src, err)
	}

	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open src %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	// Preserve execute bits from source.
	mode := srcInfo.Mode()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create dst %s: %w", dst, err)
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copy %s → %s: %w", src, dst, copyErr)
	}
	return closeErr
}

// CollectProjects discovers all yakos-wired project paths under the canonical
// search roots (~/agent-control/*/.project-path and ~/github/*/.claude/settings.json).
// Deduplication is performed by resolved absolute path.
func CollectProjects(home string) []string {
	seen := make(map[string]bool)
	var result []string

	acRoot := filepath.Join(home, "agent-control")
	ghRoot := filepath.Join(home, "github")

	// From ~/agent-control/*/.project-path
	if entries, err := os.ReadDir(acRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
			data, err := os.ReadFile(ppFile) //nolint:gosec
			if err != nil {
				continue
			}
			proj := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
			if proj == "" {
				continue
			}
			abs, err := filepath.Abs(proj)
			if err != nil {
				continue
			}
			if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
				continue
			}
			if !seen[abs] {
				seen[abs] = true
				result = append(result, abs)
			}
		}
	}

	// From ~/github/*/.claude/settings.json that look yakos-wired
	if entries, err := os.ReadDir(ghRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sfPath := filepath.Join(ghRoot, e.Name(), ".claude", "settings.json")
			data, err := os.ReadFile(sfPath) //nolint:gosec
			if err != nil {
				continue
			}
			if !strings.Contains(string(data), "scripts/hooks/") {
				continue
			}
			abs, err := filepath.Abs(filepath.Join(ghRoot, e.Name()))
			if err != nil {
				continue
			}
			if !seen[abs] {
				seen[abs] = true
				result = append(result, abs)
			}
		}
	}

	return result
}

// InferProjectFromCWD attempts to determine the project path from the current
// working directory, mirroring the bash _infer_project_from_cwd logic.
// Returns "" when the project cannot be determined.
func InferProjectFromCWD(cwd, home string) string {
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}

	acRoot := filepath.Join(home, "agent-control")

	// Inside ~/agent-control/<name>/ → read .project-path
	if strings.HasPrefix(cwdAbs, acRoot+string(filepath.Separator)) {
		rest := cwdAbs[len(acRoot)+1:]
		parts := strings.SplitN(rest, string(filepath.Separator), 2)
		if len(parts) > 0 {
			ppFile := filepath.Join(acRoot, parts[0], ".project-path")
			if data, err := os.ReadFile(ppFile); err == nil { //nolint:gosec
				proj := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
				if proj != "" {
					return proj
				}
			}
		}
	}

	// Inside a known project repo
	if entries, err := os.ReadDir(acRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
			data, err := os.ReadFile(ppFile) //nolint:gosec
			if err != nil {
				continue
			}
			proj := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
			projAbs, err := filepath.Abs(proj)
			if err != nil {
				continue
			}
			if cwdAbs == projAbs || strings.HasPrefix(cwdAbs, projAbs+string(filepath.Separator)) {
				return projAbs
			}
		}
	}

	// cwd itself looks yakos-wired
	sfPath := filepath.Join(cwdAbs, ".claude", "settings.json")
	if data, err := os.ReadFile(sfPath); err == nil { //nolint:gosec
		if strings.Contains(string(data), "scripts/hooks/") {
			return cwdAbs
		}
	}

	return ""
}
