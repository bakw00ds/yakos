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
	"encoding/json"
	"fmt"
	"github.com/bakw00ds/yakos/internal/binver"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/deploydrift"
)

// ResolveApply converts a transport-level "apply" flag into the DryRun value
// for Config.
//
// SECURITY (round-2 review R5/R19, M2 follow-up): yakos.refresh rewrites
// hook scripts, settings.json, and agent symlinks across every project
// under $HOME/agent-control. Round 1 fixed this fail-safe default on the
// MCP transport only (via a *bool dryRun field defaulting to dry-run when
// omitted); JSON-RPC (yakos.refresh.run), gRPC (Refresh.Run), and REST
// (currently unrouted, but inherits the bug the day it is wired) all still
// had `DryRun bool json:"dry_run,omitempty"`, whose Go zero value (false,
// indistinguishable from an omitted field) meant the field being merely
// ABSENT from the request already applied changes.
//
// The fix standardizes every transport on a single "apply" boolean instead
// of "dry_run": the wire field's Go zero value (false) now means dry-run by
// construction, for every transport, with no *bool/pointer machinery
// needed. Every transport MUST decode into an "apply" field and call this
// function — never invert a locally-named "dryRun bool" field, which
// reintroduces the exact zero-value bug this closes. There is intentionally
// no transport-specific variation: same inputs, same result, everywhere.
func ResolveApply(apply bool) bool {
	return !apply
}

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

	// HooksImpl is the explicit --hooks-impl choice. Empty means "not given":
	// each project uses its persisted hooks_impl from .yakos.yml, else bash.
	// A non-empty value overrides and (outside dry-run) re-persists.
	HooksImpl HooksImpl

	// YakosBinary overrides the absolute yakos path embedded in Go-form hook
	// commands. Empty means the running binary (os.Executable, symlinks
	// evaluated). Tests set it for determinism.
	YakosBinary string

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
	Rules       RulesPhaseReport
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

	// SECURITY / K-91b test-safety net: under `go test`, refuse to apply
	// (write) against a project path that is YakosRoot itself, or a git
	// worktree of it. This is not a production behavior change — a real
	// operator passing YakosRoot as one of its own ProjectPaths is not a
	// scenario refresh needs to support — but it turns a class of test bug
	// (a test that hardcodes the real repo checkout as WorkspaceRoot /
	// ProjectPaths, then a later edit or a dry-run-default regression flips
	// DryRun to false) from "silently rewrites the real checked-out repo's
	// hook scripts and settings.json" into a loud, immediate failure
	// instead. See work/current/reports/
	// scripts-hooks-drift-diag-2026-09-23.md §4 for the incident shape this
	// guards against (methods_expansion_test.go's
	// TestMethod_RefreshRun_DryRunReturnsOutput was exactly this pattern,
	// safe only because it happened to hardcode apply:false).
	if !cfg.DryRun && testing.Testing() {
		for _, p := range cfg.ProjectPaths {
			if isFrameworkSelf(p, cfg.YakosRoot) {
				return nil, fmt.Errorf("refresh: refusing apply=true against project path %q under go test — it is YakosRoot (%q) or a worktree of it; this looks like a test using the real repo checkout as a write target, use t.TempDir() for the project path instead", p, cfg.YakosRoot)
			}
		}
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

	// Resolve and validate every project's hooks impl BEFORE any write, so a
	// fail-closed refusal leaves the whole tree untouched.
	templateFile := filepath.Join(cfg.YakosRoot, "lib", "settings", "settings.template.json")
	impls, err := resolveProjectImpls(cfg, templateFile)
	if err != nil {
		return nil, err
	}

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

	_, _ = fmt.Fprintln(cfg.Writer, "Project hook + settings refresh:")
	for _, projPath := range cfg.ProjectPaths {
		projRpt, err := refreshOne(projPath, hooksRoot, templateFile, cfg.DryRun, impls[projPath], cfg.Writer, cfg.ErrWriter)
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
func refreshOne(projPath, hooksRoot, templateFile string, dryRun bool, ri resolvedImpl, w, ew io.Writer) (ProjectReport, error) {
	absPath, err := filepath.Abs(projPath)
	if err != nil {
		absPath = projPath
	}

	if fi, err := os.Stat(absPath); err != nil || !fi.IsDir() {
		return ProjectReport{}, fmt.Errorf("project path not found: %s", absPath)
	}

	_, _ = fmt.Fprintf(w, "  project: %s\n", absPath)
	_, _ = fmt.Fprintf(w, "    hooks-impl: %s (%s)\n", ri.impl, ri.source)
	if ri.persist && !dryRun {
		if err := PersistHooksImpl(absPath, ri.impl); err != nil {
			return ProjectReport{}, fmt.Errorf("persisting hooks_impl: %w", err)
		}
	}

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
		settingsRpt, err = mergeSettingsForProject(templateFile, deployedSettings, dryRun, ri.impl, ri.bin, w)
		if err != nil {
			_, _ = fmt.Fprintf(ew, "refresh: settings merge error for %s: %v\n", absPath, err)
		}
	}

	// Phase 3b: default-on auto-compaction window (K-118).
	compactStatus, compactChanged := "", false
	if !settingsRpt.Skipped {
		var cerr error
		compactStatus, compactChanged, cerr = applyAutoCompact(absPath, deployedSettings, dryRun)
		if cerr != nil {
			_, _ = fmt.Fprintf(ew, "refresh: auto-compact error for %s: %v\n", absPath, cerr)
			compactStatus = ""
		}
	}

	// Phase 4 (K-116): specialist rules into the project's .claude/rules/.
	// Symlinks must never target a deletable worktree: redirect to the
	// canonical checkout exactly as the agent symlinks do.
	rulesRoot, rerr := resolveAgentsSourceRoot(filepath.Dir(filepath.Dir(hooksRoot)), io.Discard)
	var rulesRpt RulesPhaseReport
	if rerr == nil {
		rulesRpt, rerr = syncProjectRules(rulesRoot, absPath, dryRun, w)
	}
	if rerr != nil {
		_, _ = fmt.Fprintf(ew, "refresh: rules sync error for %s: %v\n", absPath, rerr)
	}

	hasDrift := rulesRpt.New > 0 || hookRpt.New > 0 || hookRpt.Synced > 0 || settingsRpt.Added > 0 || settingsRpt.Removed > 0 || compactChanged

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
	if compactStatus != "" {
		_, _ = fmt.Fprintf(w, "    auto-compact-window: %s\n", compactStatus)
	}
	_, _ = fmt.Fprintf(w, "    rules:    new=%d ok=%d warns=%d\n", rulesRpt.New, rulesRpt.OK, rulesRpt.Warns)
	_, _ = fmt.Fprintf(w, "    status:   %s\n", driftStatus)
	_, _ = fmt.Fprintln(w, "")

	return ProjectReport{
		ProjectPath: absPath,
		Hooks:       hookRpt,
		Settings:    settingsRpt,
		Rules:       rulesRpt,
		HasDrift:    hasDrift,
	}, nil
}

// mergeSettingsForProject wraps MergeSettingsFiles and converts MergeStats into
// SettingsPhaseReport for per-project reporting.
func mergeSettingsForProject(templateFile, deployedFile string, dryRun bool, impl HooksImpl, bin string, w io.Writer) (SettingsPhaseReport, error) {
	stats, err := MergeSettingsFilesImpl(templateFile, deployedFile, dryRun, w, impl, bin)
	if err != nil {
		return SettingsPhaseReport{}, err
	}
	return SettingsPhaseReport{
		Added:   stats.Added,
		Removed: stats.Removed,
	}, nil
}

// syncHooks copies / updates hook files from srcRoot into dstRoot.
// This is the canonical layout algorithm; cli/lib/refresh.sh _sync_hooks
// mirrors it exactly (K-94) and must be changed in lockstep. It includes:
//   - Skipping .gitkeep, README.md, and git/ subdirectory entries.
//   - Never recreating legacy/ under dstRoot: legacy/<name>.sh deploys flat.
//   - Writing / updating a .framework-hash sidecar beside each deployed file.
//   - Dry-run: reports counts without writing.
//
// Symlink / materialized-install handling: on a dev-repo install, top-level
// lib/hooks/<name>.sh entries are symlinks to legacy/<name>.sh; filepath.Walk
// follows them so they are processed correctly.  On a bare binary install
// (materialized embedded lib), go:embed does not follow symlinks, so only
// lib/hooks/legacy/<name>.sh exists — the top-level <name>.sh symlinks are
// absent.  To cover both cases this function runs two passes:
//
//  1. Walk srcRoot, skipping git/ and legacy/ as before.  Track the base
//     names of all .sh files processed so the second pass can deduplicate.
//  2. Walk srcRoot/legacy/, copy each <name>.sh to dstRoot/<name>.sh (flat,
//     no legacy/ prefix) only when it was NOT already covered in pass 1.
//
// This ensures that on a bare install the legacy/ real files are copied to
// the project as flat <name>.sh hooks, matching what the symlinks would have
// produced on a full repo install.
func syncHooks(srcRoot, dstRoot string, dryRun bool, w io.Writer) (HookPhaseReport, error) {
	var rpt HookPhaseReport

	if _, err := os.Stat(srcRoot); os.IsNotExist(err) {
		return rpt, nil
	}

	// processedRels tracks the destination-relative paths pass-1 already
	// handled, so pass-2 (legacy/) can skip a hook whose flat destination
	// was already written. Keyed by relative path, not basename: a helper
	// such as lib/x.sh must not suppress a distinct legacy/x.sh (K-94).
	processedRels := make(map[string]bool)

	// syncOne performs the copy-or-update logic for a single (src, dst, rel) triple.
	syncOne := func(srcPath, dstPath, rel string) {
		hashFile := dstPath + ".framework-hash"

		srcHash, err := deploydrift.SHA256File(srcPath)
		if err != nil {
			return // skip unhashable files
		}

		if _, err := os.Stat(dstPath); os.IsNotExist(err) {
			// NEW — file not deployed yet
			if dryRun {
				_, _ = fmt.Fprintf(w, "    [dry-run] hooks: would copy NEW  %s\n", rel)
			} else {
				if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil { //nolint:gosec
					return
				}
				if err := copyFile(srcPath, dstPath); err != nil {
					return
				}
				if err := os.WriteFile(hashFile, []byte(srcHash+"\n"), 0644); err != nil { //nolint:gosec
					return
				}
			}
			rpt.New++
			return
		}

		// File exists — compare by content hash
		dstHash, err := deploydrift.SHA256File(dstPath)
		if err != nil {
			return
		}

		if srcHash != dstHash {
			// SYNC — deployed is stale
			if dryRun {
				_, _ = fmt.Fprintf(w, "    [dry-run] hooks: would sync STALE %s\n", rel)
			} else {
				if err := copyFile(srcPath, dstPath); err != nil {
					return
				}
				if err := os.WriteFile(hashFile, []byte(srcHash+"\n"), 0644); err != nil { //nolint:gosec
					return
				}
			}
			rpt.Synced++
		} else {
			// OK — ensure sidecar is up to date even if content matches, and
			// that the script is executable (content-identical but 0644 is a
			// silent fail-open: exit 126 is non-blocking).
			if fi, serr := os.Stat(dstPath); serr == nil && runtimeGOOS != "windows" && fi.Mode().Perm()&0o111 != 0o111 {
				if dryRun {
					_, _ = fmt.Fprintf(w, "    [dry-run] hooks: would chmod 0755 %s (not executable)\n", rel)
				} else {
					_ = os.Chmod(dstPath, hookFileMode) //nolint:gosec
				}
			}
			if !dryRun {
				_ = os.WriteFile(hashFile, []byte(srcHash+"\n"), 0644) //nolint:gosec
			}
			rpt.OK++
		}
	}

	// Pass 1: walk the top-level srcRoot, skip git/ and legacy/.
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
		// Skip files under legacy/ — handled by pass 2 when not covered here.
		parts := strings.SplitN(rel, string(filepath.Separator), 2)
		if len(parts) > 0 && (parts[0] == "git" || parts[0] == "legacy") {
			return nil
		}

		dstPath := filepath.Join(dstRoot, rel)
		syncOne(srcPath, dstPath, rel)
		// Track the destination-relative path so pass-2 can skip it.
		processedRels[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return rpt, err
	}

	// Pass 2: walk srcRoot/legacy/ and copy <name>.sh to dstRoot/<name>.sh for
	// any file not already covered by pass 1.  This handles materialized installs
	// where go:embed did not include the top-level symlinks.
	legacyRoot := filepath.Join(srcRoot, "legacy")
	if fi, statErr := os.Stat(legacyRoot); statErr == nil && fi.IsDir() {
		_ = filepath.Walk(legacyRoot, func(srcPath string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			base := filepath.Base(srcPath)
			if base == ".gitkeep" || base == "README.md" {
				return nil
			}
			// Only .sh files need to be promoted to the flat dst.
			if !strings.HasSuffix(base, ".sh") {
				return nil
			}
			// Skip if pass-1 already handled the flat destination.
			if processedRels[base] {
				return nil
			}
			// Copy legacy/<name>.sh → dstRoot/<name>.sh (flat, no subdir).
			dstPath := filepath.Join(dstRoot, base)
			syncOne(srcPath, dstPath, base)
			return nil
		})
	}

	pruneLegacyMirror(dstRoot, dryRun, w)

	return rpt, nil
}

// pruneLegacyMirror is a one-shot cleanup for projects refreshed under the
// old layout, which mirrored lib/hooks/legacy/ as scripts/hooks/legacy/. If
// that subdirectory exists and every file in it has a flat counterpart
// (same basename) directly under dstRoot, it is a stale orphan and is
// removed, logging one line. If any file lacks a counterpart the directory
// is left alone (it may hold something operator-owned).
// cli/lib/refresh.sh _prune_legacy_mirror mirrors this exactly.
func pruneLegacyMirror(dstRoot string, dryRun bool, w io.Writer) {
	legacy := filepath.Join(dstRoot, "legacy")
	fi, err := os.Lstat(legacy)
	if err != nil || !fi.IsDir() {
		return
	}
	count := 0
	allCovered := true
	_ = filepath.Walk(legacy, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		count++
		if _, statErr := os.Stat(filepath.Join(dstRoot, filepath.Base(p))); statErr != nil {
			allCovered = false
		}
		return nil
	})
	if !allCovered {
		return
	}
	if dryRun {
		_, _ = fmt.Fprintf(w, "    [dry-run] hooks: would remove orphan legacy/ subdir (%d files, all have flat counterparts)\n", count)
		return
	}
	if err := os.RemoveAll(legacy); err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "    [hooks] removed orphan legacy/ subdir (%d files, all have flat counterparts)\n", count)
}

// hookFileMode is forced on every deployed hook script regardless of the
// source mode. A non-executable hook exits 126, which Claude Code treats as
// non-blocking, so a 0644 source (or a stale 0644 destination) would silently
// disable the gate.
const hookFileMode os.FileMode = 0o755

var runtimeGOOS = runtime.GOOS

// copyFile copies the file at src to dst and forces hookFileMode.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open src %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, hookFileMode) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create dst %s: %w", dst, err)
	}

	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copy %s → %s: %w", src, dst, copyErr)
	}
	if closeErr != nil {
		return closeErr
	}
	// OpenFile's mode applies only on create; fix an existing 0644 file too.
	return os.Chmod(dst, hookFileMode) //nolint:gosec
}

// CollectProjects discovers all yakos-wired project paths under the canonical
// search roots (~/agent-control/*/.project-path and ~/github/*/.claude/settings.json).
// Deduplication is performed by resolved absolute path.
//
// SECURITY / K-91a: the framework's own repo checkout commonly lives under
// ~/github/<name> and its own .claude/settings.json legitimately mentions
// "scripts/hooks/" (it is itself a yakos-wired project for tooling
// purposes) — so without exclusion it is indistinguishable from any
// consumer project and gets swept by scope:"all" refresh, overwriting its
// tracked scripts/hooks/ mirror from whatever stale $YAKOS_ROOT the caller
// resolved to. This is the exact mechanism the framework repo self-sweep
// incident traced back to (see work/current/reports/
// scripts-hooks-drift-diag-2026-09-23.md §1/§3). CollectProjects therefore
// self-excludes using $YAKOS_ROOT read from the environment, which is the
// reproducible trigger the diagnosis identified. Callers that already hold
// a resolved YakosRoot value (which may differ from the environment, e.g.
// an explicitly configured daemon) should prefer CollectProjectsExcluding
// for a precise exclusion instead of relying on this env-var fallback.
func CollectProjects(home string) []string {
	return CollectProjectsExcluding(home, os.Getenv("YAKOS_ROOT"))
}

// CollectProjectsExcluding is CollectProjects, additionally excluding
// yakosRoot itself and any git worktree of it from the result. Pass "" for
// yakosRoot to disable exclusion (equivalent to the historical, unsafe
// CollectProjects behavior — callers should only do this deliberately).
//
// Exclusion is by os.SameFile on each candidate's resolved toplevel, never
// raw string/path comparison, so it is correct on case-insensitive
// filesystems where two differently-cased paths can alias the same inode
// (see the YAKOS_ROOT / YAKOS_LIB aliasing note in cache-stability
// discipline), and by comparing `git rev-parse --git-common-dir` so a
// worktree of yakosRoot is caught even though its own toplevel is a
// different directory than yakosRoot's.
func CollectProjectsExcluding(home, yakosRoot string) []string {
	seen := make(map[string]bool)
	var result []string

	acRoot := filepath.Join(home, "agent-control")
	ghRoot := filepath.Join(home, "github")

	add := func(abs string) {
		if seen[abs] {
			return
		}
		if isFrameworkSelf(abs, yakosRoot) {
			_, _ = fmt.Fprintf(os.Stderr, "refresh: skipping %s — it is the framework's own repo (or a worktree of it)\n", abs)
			seen[abs] = true // don't re-evaluate on a later duplicate encounter
			return
		}
		seen[abs] = true
		result = append(result, abs)
	}

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
			add(abs)
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
			add(abs)
		}
	}

	return result
}

// IsFrameworkSelf reports whether candidate is yakosRoot itself, or a git
// worktree of it. It is the exported form of the same check
// CollectProjectsExcluding applies to every filesystem-discovered
// candidate, for callers that need to test a single already-known path —
// e.g. a WorkspaceRoot used as a refresh project fallback when scope:"all"
// discovery finds nothing — rather than filter a discovered list.
// yakosRoot == "" always returns false (exclusion disabled).
func IsFrameworkSelf(candidate, yakosRoot string) bool {
	return isFrameworkSelf(candidate, yakosRoot)
}

// isFrameworkSelf reports whether candidate is yakosRoot itself, or a git
// worktree of it. yakosRoot == "" always returns false (exclusion
// disabled).
func isFrameworkSelf(candidate, yakosRoot string) bool {
	if yakosRoot == "" {
		return false
	}
	rootInfo, err := os.Stat(yakosRoot)
	if err != nil {
		return false
	}
	candInfo, err := os.Stat(candidate)
	if err != nil {
		return false
	}
	if os.SameFile(rootInfo, candInfo) {
		return true
	}

	// Not the same directory by inode — check whether candidate is a git
	// worktree of yakosRoot by comparing each path's --git-common-dir
	// (a worktree's --git-common-dir resolves to the SAME physical .git
	// directory as the main checkout's, from any worktree path).
	rootCommon, rootOK := gitCommonDir(yakosRoot)
	candCommon, candOK := gitCommonDir(candidate)
	if !rootOK || !candOK {
		return false
	}
	rootCommonInfo, err := os.Stat(rootCommon)
	if err != nil {
		return false
	}
	candCommonInfo, err := os.Stat(candCommon)
	if err != nil {
		return false
	}
	return os.SameFile(rootCommonInfo, candCommonInfo)
}

// gitCommonDir resolves `git -C dir rev-parse --git-common-dir` to an
// absolute path. Returns ok=false if dir is not inside a git repo, or git
// is unavailable.
func gitCommonDir(dir string) (path string, ok bool) {
	return gitRevParsePath(dir, "--git-common-dir")
}

// gitDir resolves `git -C dir rev-parse --git-dir` to an absolute path.
// For a git worktree this differs from gitCommonDir (it points at the
// worktree's own <main-repo>/.git/worktrees/<name> admin directory); for
// the main checkout the two are identical. Returns ok=false if dir is not
// inside a git repo, or git is unavailable.
func gitDir(dir string) (path string, ok bool) {
	return gitRevParsePath(dir, "--git-dir")
}

// gitRevParsePath runs `git -C dir rev-parse <flag>` and resolves the
// result to an absolute path. Returns ok=false if dir is not inside a git
// repo, or git is unavailable.
func gitRevParsePath(dir, flag string) (path string, ok bool) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", flag)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	return abs, true
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

// resolvedImpl is one project's effective hooks impl and where it came from.
type resolvedImpl struct {
	impl    HooksImpl
	source  string // "flag", "persisted", or "default"
	persist bool   // write hooks_impl to .yakos.yml (flag given)
	bin     string // absolute yakos path for Go-form commands (go/hybrid)
}

// resolveProjectImpls decides each project's impl (flag > persisted >
// DefaultHooksImpl) and validates go/hybrid against the Go registry. Any failure aborts
// the run before a byte is written.
func resolveProjectImpls(cfg Config, templateFile string) (map[string]resolvedImpl, error) {
	out := make(map[string]resolvedImpl, len(cfg.ProjectPaths))
	var tmpl map[string]any
	loaded := false
	warnedTemp, warnedGo := false, false
	for _, p := range cfg.ProjectPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		// An invalid auto_compact_window aborts the run before a byte is
		// written, like an invalid hooks_impl, so it cannot hide behind an
		// "in sync" report and a zero exit.
		if _, err := readAutoCompactSetting(abs); err != nil {
			return nil, err
		}
		ri := resolvedImpl{impl: DefaultHooksImpl, source: "default"}
		if cfg.HooksImpl != "" {
			impl, err := ParseHooksImpl(string(cfg.HooksImpl))
			if err != nil {
				return nil, err
			}
			ri = resolvedImpl{impl: impl, source: "flag", persist: true}
		} else if impl, found, err := ReadPersistedHooksImpl(abs); err != nil {
			return nil, err
		} else if found {
			ri = resolvedImpl{impl: impl, source: "persisted"}
		}
		if ri.impl != HooksImplBash {
			if !loaded {
				data, err := os.ReadFile(templateFile) //nolint:gosec
				if err == nil {
					err = json.Unmarshal(data, &tmpl)
				}
				if err != nil {
					if ri.source == "default" {
						// The default is best-effort: the settings phase reports a
						// bad template itself. Only an explicit choice fails here.
						out[p] = resolvedImpl{impl: HooksImplBash, source: "default; template unreadable, keeping bash"}
						continue
					}
					return nil, fmt.Errorf("reading template %s: %w", templateFile, err)
				}
				loaded = true
			}
			bin := cfg.YakosBinary
			if bin == "" {
				var berr error
				if bin, berr = runningBinary(); berr != nil {
					return nil, fmt.Errorf("resolving the yakos binary path for Go hook commands: %w", berr)
				}
			}
			ri.bin = bin
			if ok, detail := pinnedBinarySupportsHooks(bin); !ok {
				// A binary older than 0.60.0.0 reads "--impl" as a hook name and
				// exits 0, so pinned gates would silently stop enforcing.
				_, _ = fmt.Fprintf(cfg.ErrWriter, "refresh: warning: %s cannot run `hook run --impl` (%s; needs %s or newer); keeping bash hooks. Install a current yakos and re-run refresh.\n", bin, detail, binver.MinHookRun)
				ri = resolvedImpl{impl: HooksImplBash, source: ri.source + "; yakos binary too old for Go hooks, keeping bash"}
				out[p] = ri
				continue
			}
			if ri.source == "default" && ephemeralBinary(bin) {
				// Nothing asked for Go hooks, so never pin a path that may
				// vanish (temp dir, worktree build): stay on bash.
				ri = resolvedImpl{impl: HooksImplBash, source: "default; yakos binary at " + bin + " looks temporary, keeping bash"}
				out[p] = ri
				continue
			}
			if !warnedTemp && ephemeralBinary(bin) {
				warnedTemp = true
				_, _ = fmt.Fprintf(cfg.ErrWriter, "refresh: warning: Go hook commands will point at %s, which looks temporary (temp dir or worktree); re-run refresh from an installed yakos\n", bin)
			}
			if !warnedGo && ri.impl == HooksImplGo {
				warnedGo = true
				if names := nonGoReadyIn(tmpl); len(names) > 0 {
					_, _ = fmt.Fprintf(cfg.ErrWriter, "refresh: warning: --hooks-impl go also switches %d hook(s) not yet marked GoReady (parity-unverified): %s\n", len(names), strings.Join(names, ", "))
				}
			}
			if err := ValidateHooksImpl(ri.impl, tmpl); err != nil {
				return nil, fmt.Errorf("%s: %w", abs, err)
			}
		}
		out[p] = ri
	}
	return out, nil
}

// pinnedBinarySupportsHooks checks that bin can run `hook run --impl`. The
// running binary is current by construction, and a path that does not exist
// yet cannot be probed (doctor warns about it); only an existing binary that
// is too old or unreadable is refused.
var pinnedBinarySupportsHooks = func(bin string) (bool, string) {
	if self, err := runningBinary(); err == nil && self == bin {
		return true, ""
	}
	fi, err := os.Stat(bin)
	if err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return true, ""
	}
	return binver.SupportsHookRun(bin)
}
