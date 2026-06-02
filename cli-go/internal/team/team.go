// Package team is the Go port of cli/lib/team.sh.
//
// It implements the `yakos team` lifecycle subcommands. In v0.1, only one
// subcommand exists: `yakos team restart <project>`.
//
// restart <project>:
//  1. Derive an archive tag from the current UTC time (auto-<ISO-8601>),
//     unless --tag overrides it.
//  2. Delegate to `yakos archive <project> <tag> --auto-tag --yes` (still
//     bash in Phase 1; archive is rank 10 in the port plan).
//  3. Print relaunch instructions.
//
// The package intentionally has no direct dependency on the archive package
// (not yet ported). Archiving is delegated via passthrough.RunArchive, which
// shells out to the bash archive.sh through the existing passthrough shim.
package team

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Config carries everything Restart needs to do its work.
// All fields have usable zero values except YakosRoot and Project.
type Config struct {
	// YakosRoot is the absolute path to the yakOS framework root (required).
	YakosRoot string

	// Project is the agent-control project name (required).
	Project string

	// Tag is the explicit archive tag. When empty, an auto-tag is derived
	// from the current time: "auto-<YYYY-MM-DDTHH-MM-SS>Z".
	Tag string

	// HomeDir is the user home directory for resolving ~/agent-control/.
	// Defaults to os.Getenv("HOME") when empty.
	HomeDir string

	// Now is injected for tests; zero value uses time.Now().
	Now time.Time

	// Writer is where progress/status messages are printed (default: os.Stdout).
	Writer io.Writer

	// ErrWriter is where error messages are printed (default: os.Stderr).
	ErrWriter io.Writer

	// ArchiveFn is the function called to perform the archive step.
	// The default implementation shells out to bash archive.sh via the
	// yakOS passthrough mechanism.  Tests inject a mock here so that no
	// real filesystem mutation occurs.
	//
	// Signature: archive(yakosRoot, project, tag string) error
	ArchiveFn func(yakosRoot, project, tag string) error
}

// RestartResult is the outcome of a successful Restart call.
type RestartResult struct {
	// Project is the project that was restarted.
	Project string

	// Tag is the archive tag that was used.
	Tag string

	// ProjectPath is the value read from .project-path (may be empty).
	ProjectPath string
}

// Restart implements `yakos team restart <project>`.
//
// It:
//  1. Validates that the project control directory exists.
//  2. Derives an archive tag (auto-generated or from Config.Tag).
//  3. Calls cfg.ArchiveFn to archive work/current/.
//  4. Prints progress messages and relaunch instructions to cfg.Writer.
//
// The function returns an error only when the preconditions fail (missing
// project) or when the archive step fails. Output messages are written
// directly to cfg.Writer so callers don't need to format them.
func Restart(cfg Config) (*RestartResult, error) {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	// ErrWriter is reserved for future use (e.g. verbose mode warnings).
	// Currently all user-facing output goes to Writer. Silence the linter.
	_ = cfg.ErrWriter

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	controlDir := filepath.Join(home, "agent-control", cfg.Project)
	if _, err := os.Stat(controlDir); err != nil {
		return nil, fmt.Errorf("team restart: project %q not found at %s", cfg.Project, controlDir)
	}

	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tag := cfg.Tag
	if tag == "" {
		tag = isoTag(now)
	}

	_, _ = fmt.Fprintf(w, "Restarting team for %q...\n", cfg.Project)
	_, _ = fmt.Fprintf(w, "  Auto-tag: %s\n", tag)

	archiveFn := cfg.ArchiveFn
	if archiveFn == nil {
		archiveFn = defaultArchive
	}
	if err := archiveFn(cfg.YakosRoot, cfg.Project, tag); err != nil {
		return nil, fmt.Errorf("team restart: archive failed: %w", err)
	}

	// Best-effort: read project repo path from .project-path marker.
	projectPath := "<path-to-project-repo>"
	ppFile := filepath.Join(controlDir, ".project-path")
	if data, err := os.ReadFile(ppFile); err == nil { //nolint:gosec
		if s := trimNewline(string(data)); s != "" {
			projectPath = s
		}
	}

	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Team archived. To start a fresh session:")
	_, _ = fmt.Fprintf(w, "  yakos start %s\n", cfg.Project)
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintf(w, "(Or run bare 'yakos' from %s or %s — auto-detects.)\n",
		controlDir, projectPath)

	return &RestartResult{
		Project:     cfg.Project,
		Tag:         tag,
		ProjectPath: projectPath,
	}, nil
}

// isoTag returns the auto-generated archive tag for the given time.
// Format: "auto-<YYYY-MM-DDTHH-MM-SS>Z" (colons replaced with hyphens so
// the tag is safe as a directory name on all platforms — mirrors the bash
// ct_iso_utc helper which uses date -u +%Y%m%dT%H%M%SZ).
func isoTag(t time.Time) string {
	// Matches bash ct_iso_utc: YYYYMMDDTHHMMSSz
	return "auto-" + t.UTC().Format("20060102T150405Z")
}

// trimNewline strips a trailing newline from s.
func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// defaultArchive is the production ArchiveFn: it shells out to the bash
// archive.sh through the existing passthrough shim.
//
// It constructs the equivalent of:
//
//	YAKOS_ROOT=<root> YAKOS_LIB=<root>/lib bash <root>/cli/lib/archive.sh <project> <tag> --auto-tag --yes
func defaultArchive(yakosRoot, project, tag string) error {
	return RunArchiveBash(yakosRoot, project, tag)
}
