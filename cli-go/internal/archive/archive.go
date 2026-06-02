// Package archive is the Go port of cli/lib/archive.sh.
//
// It implements `yakos archive <project> <tag> [--auto-tag] [--yes]`:
//  1. Validate <project> exists under ~/agent-control/.
//  2. Validate <tag> (alphanumeric + dots/hyphens/underscores; no slashes or spaces).
//  3. Read work/current/hook-bypass.md; abort if any active entries have expired.
//  4. Move work/current/ → work/archive/<tag>/ (atomic: os.Rename; falls back to
//     copy+remove when src and dst are on different filesystems).
//  5. Recreate work/current/{logs,artifacts,reports}/ empty.
//  6. Restore hook-bypass.md template and an empty session-history NDJSON in fresh current/.
//  7. Append an archive entry to work/sessions.ndjson.
//  8. Print a summary.
//
// Worktree cleanup: explicitly NOT in scope per git-hygiene rule §Worktree.
// "Cleanup happens at archive time — yakos archive does NOT clean worktrees;
// that's manual still in v0.1." This implementation honours the same caveat.
package archive

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// validTagRe matches tag strings that consist only of alphanumeric characters,
// dots, hyphens, and underscores — identical to the case-pattern check in
// archive.sh: *[!A-Za-z0-9._-]*)
var validTagRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// Config carries everything Run needs.
// All fields except YakosRoot, Project, and Tag have usable zero values.
type Config struct {
	// YakosRoot is the absolute path to the yakOS framework root (required).
	// Used to find lib/settings/hook-bypass.template.md.
	YakosRoot string

	// Project is the agent-control project name (required).
	Project string

	// Tag is the archive tag (required). Alphanumeric + dots/hyphens/underscores.
	Tag string

	// AutoTag, when true, appends a numeric suffix on conflict instead of failing.
	// Mirrors --auto-tag in archive.sh.
	AutoTag bool

	// HomeDir overrides $HOME for resolving ~/agent-control/.
	// Defaults to os.Getenv("HOME") when empty.
	HomeDir string

	// Now is injected for tests; zero value uses time.Now().
	Now time.Time

	// Writer is where progress/status messages are printed (default: os.Stdout).
	Writer io.Writer

	// ErrWriter is where error messages are printed (default: os.Stderr).
	ErrWriter io.Writer
}

// Result is the outcome of a successful Run call.
type Result struct {
	// Project is the project that was archived.
	Project string

	// Tag is the tag that was used (may have a suffix appended by AutoTag).
	Tag string

	// DestDir is the absolute path to the archive destination directory.
	DestDir string

	// SessLogPath is the absolute path to the sessions.ndjson ledger entry was appended to.
	SessLogPath string

	// Size is the pre-move disk usage of work/current/ as reported by du (may be "unknown").
	Size string
}

// Run executes the archive operation and returns a structured Result.
// Output is streamed to cfg.Writer as work proceeds.
//
// Errors are returned (not printed); the caller prints and exits.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}

	// ---- resolve home --------------------------------------------------------
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	// ---- validate project ----------------------------------------------------
	if cfg.Project == "" {
		return nil, fmt.Errorf("archive: missing <project>")
	}
	controlDir := filepath.Join(home, "agent-control", cfg.Project)
	if _, err := os.Stat(controlDir); err != nil {
		return nil, fmt.Errorf("archive: project %q not found at %s (run 'yakos init' first)", cfg.Project, controlDir)
	}

	// ---- validate tag --------------------------------------------------------
	if cfg.Tag == "" {
		return nil, fmt.Errorf("archive: missing <tag>")
	}
	if !validTagRe.MatchString(cfg.Tag) {
		return nil, fmt.Errorf("archive: <tag> may only contain alphanumeric, dot, hyphen, underscore: got %q", cfg.Tag)
	}

	// ---- resolve work / current / archive directories -----------------------
	workDir := workDirFor(cfg.Project, home)
	currentDir := filepath.Join(workDir, "current")
	archiveBase := filepath.Join(workDir, "archive")

	if _, err := os.Stat(currentDir); err != nil {
		return nil, fmt.Errorf("archive: %s does not exist (nothing to archive)", currentDir)
	}

	// ---- auto-tag conflict resolution ----------------------------------------
	tag := cfg.Tag
	destDir := filepath.Join(archiveBase, tag)
	if _, err := os.Stat(destDir); err == nil {
		// Destination already exists.
		if !cfg.AutoTag {
			return nil, fmt.Errorf("archive: %s already exists; pick a different tag or use --auto-tag", destDir)
		}
		suffix := 2
		for {
			candidate := fmt.Sprintf("%s-%d", destDir, suffix)
			if _, statErr := os.Stat(candidate); os.IsNotExist(statErr) {
				destDir = candidate
				tag = fmt.Sprintf("%s-%d", tag, suffix)
				break
			}
			suffix++
		}
	}

	// ---- expired-bypass check ------------------------------------------------
	bypassFile := filepath.Join(currentDir, "hook-bypass.md")
	if err := checkExpiredBypasses(bypassFile); err != nil {
		return nil, err
	}

	// ---- size summary (before move, for the ledger) -------------------------
	size := diskUsage(currentDir)

	// ---- move + recreate ----------------------------------------------------
	if err := os.MkdirAll(archiveBase, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("archive: mkdir %s: %w", archiveBase, err)
	}

	// os.Rename is atomic on POSIX when src and dst are on the same filesystem.
	// If they are on different filesystems, fall back to a copy+remove.
	if err := atomicMove(currentDir, destDir); err != nil {
		return nil, fmt.Errorf("archive: move %s → %s: %w", currentDir, destDir, err)
	}

	// Recreate empty subdirs in fresh work/current/.
	for _, sub := range []string{"logs", "artifacts", "reports"} {
		dir := filepath.Join(currentDir, sub)
		if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
			return nil, fmt.Errorf("archive: mkdir %s: %w", dir, err)
		}
	}

	// Restore the hook-bypass template in fresh current/.
	newBypassFile := filepath.Join(currentDir, "hook-bypass.md")
	templateSrc := filepath.Join(cfg.YakosRoot, "lib", "settings", "hook-bypass.template.md")
	if err := copyFile(templateSrc, newBypassFile); err != nil {
		// Non-fatal: warn but do not abort — the archive already moved.
		_, _ = fmt.Fprintf(cfg.ErrWriter, "archive: warning: could not restore hook-bypass.md template: %v\n", err)
	}

	// Recreate empty session-history NDJSON in fresh current/.
	sessionHistoryFile := filepath.Join(currentDir, ".session-started-history.ndjson")
	if err := os.WriteFile(sessionHistoryFile, nil, 0644); err != nil { //nolint:gosec
		_, _ = fmt.Fprintf(cfg.ErrWriter, "archive: warning: could not create session-history file: %v\n", err)
	}

	// ---- append session ledger entry ----------------------------------------
	sessLogPath := filepath.Join(workDir, "sessions.ndjson")
	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ts := now.UTC().Format(time.RFC3339)

	entry := map[string]string{
		"ts":      ts,
		"action":  "archive",
		"project": cfg.Project,
		"tag":     tag,
		"dest":    destDir,
		"size":    size,
	}
	if err := appendNDJSON(sessLogPath, entry); err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "archive: warning: could not write sessions.ndjson: %v\n", err)
	}

	// ---- summary ------------------------------------------------------------
	_, _ = fmt.Fprintf(cfg.Writer, "\nArchived %q work/current/ → work/archive/%s/\n", cfg.Project, tag)
	_, _ = fmt.Fprintf(cfg.Writer, "  size:    %s\n", size)
	_, _ = fmt.Fprintf(cfg.Writer, "  source:  %s (now empty)\n", currentDir)
	_, _ = fmt.Fprintf(cfg.Writer, "  dest:    %s\n", destDir)
	_, _ = fmt.Fprintf(cfg.Writer, "  ledger:  %s\n", sessLogPath)

	return &Result{
		Project:     cfg.Project,
		Tag:         tag,
		DestDir:     destDir,
		SessLogPath: sessLogPath,
		Size:        size,
	}, nil
}

// PrintHelp writes the --help text for `yakos archive`, matching archive.sh usage().
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos archive <project> <tag> [--auto-tag] [--yes]

Roll work/current/ into work/archive/<tag>/ for a project under
~/agent-control/. Refuses to archive while expired hook-bypass entries
remain.

Arguments:
  <project>      Project name (subdir of ~/agent-control/)
  <tag>          Archive tag (alphanumeric + dots/hyphens)

Options:
  --auto-tag     Treat <tag> as a hint; on conflict, append a suffix.
                 Used internally by 'yakos team restart'.
  --yes          Skip confirmation prompts.
  --help, -h     Print this help.
`)
}

// ---- path resolution --------------------------------------------------------

// workDirFor mirrors yakos_work_dir() from paths.sh.
// Priority:
//  1. YAKOS_WORK_DIR env
//  2. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR
//  3. $HOME/agent-control/<project>/work (canonical)
func workDirFor(project, home string) string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return v
	}
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		if pd := os.Getenv("CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work")
		}
	}
	return filepath.Join(home, "agent-control", project, "work")
}

// ---- expired-bypass check ---------------------------------------------------

// bypassEntry holds the parsed id and expiry string from hook-bypass.md.
type bypassEntry struct {
	id      string
	expires string
}

// checkExpiredBypasses reads hook-bypass.md and returns an error listing any
// expired entries. Mirrors the awk + jq logic from archive.sh.
//
// If the file does not exist, the check passes (no bypasses = no expiry issues).
func checkExpiredBypasses(bypassFile string) error {
	f, err := os.Open(bypassFile) //nolint:gosec
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("archive: reading hook-bypass.md: %w", err)
	}
	defer func() { _ = f.Close() }()

	entries := parseActiveBypassEntries(f)

	now := time.Now().UTC()
	var expired []string
	for _, e := range entries {
		t, parseErr := time.Parse(time.RFC3339, e.expires)
		if parseErr != nil {
			// Unparseable expiry → treat as expired (mirrors bash "parse_error" branch).
			expired = append(expired, fmt.Sprintf("  - bypass:%s (unparseable Expires: %s)", e.id, e.expires))
			continue
		}
		if t.Before(now) {
			expired = append(expired, fmt.Sprintf("  - bypass:%s (expired %s)", e.id, e.expires))
		}
	}

	if len(expired) > 0 {
		return fmt.Errorf("archive: expired bypass entries must be removed before archiving:\n%s\n\nEdit %s and re-run",
			strings.Join(expired, "\n"), bypassFile)
	}
	return nil
}

// parseActiveBypassEntries reads hook-bypass.md and returns entries found
// under the "## Active entries" section.
//
// It mirrors the awk script in archive.sh:
//   - Wait for a line matching "## Active entries".
//   - Then collect "## bypass:<id>" headings.
//   - Then collect "**Expires:**" lines and pair them with the preceding heading.
func parseActiveBypassEntries(r io.Reader) []bypassEntry {
	var entries []bypassEntry
	active := false
	currentID := ""

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if !active {
			// Look for "## Active entries" (case-sensitive, same as bash).
			if trimmed == "## Active entries" {
				active = true
			}
			continue
		}

		// Under Active entries: look for bypass headings.
		if strings.HasPrefix(trimmed, "## bypass:") {
			currentID = strings.TrimSpace(strings.TrimPrefix(trimmed, "## bypass:"))
			continue
		}

		// Look for Expires fields.
		if strings.HasPrefix(trimmed, "**Expires:**") && currentID != "" {
			exp := strings.TrimSpace(strings.TrimPrefix(trimmed, "**Expires:**"))
			// Take only the first space-separated token (date portion).
			if idx := strings.IndexByte(exp, ' '); idx >= 0 {
				exp = exp[:idx]
			}
			if exp != "" {
				entries = append(entries, bypassEntry{id: currentID, expires: exp})
			}
		}
	}
	return entries
}

// ---- filesystem helpers -----------------------------------------------------

// atomicMove moves src to dst atomically using os.Rename. If Rename fails
// (cross-device), it falls back to a recursive copy followed by removal of src.
func atomicMove(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	// Cross-device fallback: copy then remove.
	if err := copyDir(src, dst); err != nil {
		return fmt.Errorf("copy fallback: %w", err)
	}
	return os.RemoveAll(src)
}

// copyDir recursively copies src directory to dst.
// dst must not exist before this call.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

// copyFile copies a single file from src to dst using a temp-rename pattern to
// avoid torn writes.
func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	fi, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}

	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode()) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("copy %s → %s: %w", src, tmp, err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s → %s: %w", tmp, dst, err)
	}
	return nil
}

// diskUsage returns a human-readable size string for dir, mirroring
// `du -sh`. Returns "unknown" if du is unavailable or fails.
func diskUsage(dir string) string {
	// We walk the directory and sum file sizes, then format with a unit suffix
	// matching common du output (K, M, G). This avoids any external dependency.
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return formatSize(total)
}

// formatSize returns a du-sh–style human readable string.
func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1fG", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1fM", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1fK", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// appendNDJSON marshals entry to JSON and appends it as a newline-terminated
// NDJSON record to path. Creates the file if it does not exist.
// Uses append-only O_APPEND semantics for crash-safety.
func appendNDJSON(path string, entry map[string]string) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
