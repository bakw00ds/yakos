// Package migrate is the Go port of cli/lib/migrate.sh (rank 21).
//
// It manages framework-version-stamped migrations against the sidecar
// schema-version files that are maintained by the kanban and memory
// packages (Decision A, go-port-decisions-2026-06-02.md).
//
// # Subcommands
//
//   - status              — show schema version for each known sidecar format
//   - up [<format>]       — apply pending migrations (no-op in Phase 1; all formats at v1)
//   - down [<format>]     — error in Phase 1; deferred to Phase 1.5
//
// # Sidecar locations
//
// Each format has a well-known sidecar file:
//
//	kanban  — <work-dir>/current/.kanban.schema-version   (canonical: kanban.CurrentSchemaVersion = "1")
//	memory  — <memory-dir>/.schema-version                (canonical: memory.SchemaVersion = "memory/v1")
//
// The work-dir is resolved from YAKOS_WORK_DIR or
// $HOME/agent-control/$YAKOS_PROJECT_NAME/work (matching paths.sh priority).
//
// The memory-dir is resolved from YAKOS_MEMORY_DIR or
// ~/.claude/projects/<encoded>/memory/ (the Claude project encoding).
//
// # Migration registry
//
// A static list of (format, fromVersion, toVersion, migrateFn) tuples. Phase 1
// ships with no actual migrations (every format is at v1). The registry exists so
// future sessions can add real migration steps without redesigning the plumbing.
//
// # Atomic writes
//
// Sidecar writes use temp-rename (Decision A / Q8).
//
// # Phase 1.5 note
//
// The `down` subcommand is intentionally not implemented. Rollback semantics
// require daemon-managed locking (Phase 2 §9) and are deferred.
package migrate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is "status", "up", or "down". Required.
	Subcommand string

	// Format restricts `up` to a single format (e.g. "kanban", "memory").
	// When empty, `up` processes all registered formats.
	Format string

	// WorkDir is the base work directory (resolves .kanban.schema-version).
	// When empty, resolved from YAKOS_WORK_DIR or HOME/agent-control/<project>/work.
	WorkDir string

	// MemoryDir is the memory directory (resolves .schema-version).
	// When empty, resolved from YAKOS_MEMORY_DIR or ~/.claude/projects/<encoded>/memory/.
	MemoryDir string

	// HomeDir is used for default path resolution. When empty, os.Getenv("HOME") is used.
	HomeDir string

	// DryRun prints what would be done without writing.
	DryRun bool

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Applied is the number of migrations applied (or would-be-applied in DryRun).
	Applied int

	// Skipped is the number of up-to-date formats skipped.
	Skipped int
}

// ---- migration registry -----------------------------------------------------

// migrationFn is the signature for a sidecar migration function.
// It receives the path to the sidecar file and returns the new content.
// The caller handles the atomic write; the function must not write directly.
type migrationFn func(sidecarPath string) (newContent string, err error)

// registration is one entry in the migration registry.
type registration struct {
	// Format identifies the sidecar owner (e.g. "kanban", "memory").
	Format string

	// FromVersion is the current version that triggers this migration.
	FromVersion string

	// ToVersion is the version that will be written after migration.
	ToVersion string

	// Migrate performs the migration; see migrationFn.
	Migrate migrationFn
}

// registry is the ordered list of known migrations.
// Phase 1: no actual migrations; all formats ship at their initial version.
// Add entries here as schema versions are introduced in future sessions.
var registry []registration // intentionally empty in Phase 1

// ---- sidecar descriptors ----------------------------------------------------

// sidecarDescriptor binds a human-readable format name to the functions needed
// to locate and read its sidecar file.
type sidecarDescriptor struct {
	// Name is the format identifier used in output and --format filtering.
	Name string

	// CurrentVersion is the expected current version (from the owning package constant).
	CurrentVersion string

	// SidecarPath returns the absolute path to the sidecar file given the resolved Config.
	SidecarPath func(cfg Config) string
}

// knownSidecars lists all sidecar formats Phase 1 tracks.
var knownSidecars = []sidecarDescriptor{
	{
		Name:           "kanban",
		CurrentVersion: "1",
		SidecarPath: func(cfg Config) string {
			return filepath.Join(cfg.WorkDir, "current", ".kanban.schema-version")
		},
	},
	{
		Name:           "memory",
		CurrentVersion: "memory/v1",
		SidecarPath: func(cfg Config) string {
			return filepath.Join(cfg.MemoryDir, ".schema-version")
		},
	},
}

// ---- Run --------------------------------------------------------------------

// Run executes the migrate subcommand described by cfg.
func Run(cfg Config) (Result, error) {
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

	// Resolve WorkDir.
	if cfg.WorkDir == "" {
		if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
			cfg.WorkDir = v
		} else if proj := os.Getenv("YAKOS_PROJECT_NAME"); proj != "" {
			cfg.WorkDir = filepath.Join(home, "agent-control", proj, "work")
		} else {
			cfg.WorkDir = filepath.Join(home, "agent-control")
		}
	}

	// Resolve MemoryDir.
	if cfg.MemoryDir == "" {
		if v := os.Getenv("YAKOS_MEMORY_DIR"); v != "" {
			cfg.MemoryDir = v
		} else {
			// Default: ~/.claude/projects/<encoded>/memory/
			// In Phase 1 we use a best-effort fallback since encoding varies.
			cfg.MemoryDir = filepath.Join(home, ".claude", "projects", "memory")
		}
	}

	switch cfg.Subcommand {
	case "status", "":
		return runStatus(cfg)
	case "up":
		return runUp(cfg)
	case "down":
		return Result{}, fmt.Errorf("migrate: `down` is deferred to Phase 1.5: rollback semantics require daemon-managed locking (Phase 2 §9); use YAKOS_IMPL=bash yakos migrate for rollback in the interim")
	default:
		return Result{}, fmt.Errorf("migrate: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp writes the --help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos migrate <subcommand> [<format>] [--dry-run]

Manage schema-version sidecar files for yakOS scratchpad formats.

Subcommands:
  status              Show the current schema version for each known format.
  up [<format>]       Apply any pending migrations. With no format, all
                      formats are checked. Phase 1 has no pending migrations
                      (all formats are at their initial version).
  down [<format>]     Rollback — deferred to Phase 1.5.

Known formats:
  kanban   .kanban.schema-version beside kanban.md (current version: "1")
  memory   .schema-version in the memory directory (current version: "memory/v1")

Flags:
  --dry-run           Print what WOULD be done without writing.
  --help, -h          This help.

Path resolution:
  WorkDir   YAKOS_WORK_DIR → $HOME/agent-control/$YAKOS_PROJECT_NAME/work
  MemoryDir YAKOS_MEMORY_DIR → ~/.claude/projects/memory/

Examples:
  yakos migrate status
  yakos migrate up
  yakos migrate up kanban
  yakos migrate up --dry-run
`)
}

// ---- status -----------------------------------------------------------------

func runStatus(cfg Config) (Result, error) {
	_, _ = fmt.Fprintln(cfg.Writer, "yakos migrate status")
	_, _ = fmt.Fprintln(cfg.Writer)

	for _, sd := range knownSidecars {
		p := sd.SidecarPath(cfg)
		version, err := readSidecarVersion(p)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.Writer, "  %-8s  ERROR reading sidecar: %v\n", sd.Name, err)
			continue
		}

		label := version
		if label == "" {
			label = "(absent — legacy, treated as current)"
		}

		upToDate := ""
		if version == "" || version == sd.CurrentVersion {
			upToDate = " [up-to-date]"
		} else {
			upToDate = " [pending migration → " + sd.CurrentVersion + "]"
		}

		_, _ = fmt.Fprintf(cfg.Writer, "  %-8s  %-20s  path: %s%s\n", sd.Name, label, p, upToDate)
	}

	_, _ = fmt.Fprintln(cfg.Writer)
	_, _ = fmt.Fprintf(cfg.Writer, "  Migration registry: %d registered migration(s) (Phase 1: none).\n", len(registry))
	return Result{}, nil
}

// ---- up ---------------------------------------------------------------------

func runUp(cfg Config) (Result, error) {
	var result Result

	targets := knownSidecars
	if cfg.Format != "" {
		targets = nil
		for _, sd := range knownSidecars {
			if sd.Name == cfg.Format {
				targets = append(targets, sd)
				break
			}
		}
		if len(targets) == 0 {
			return Result{}, fmt.Errorf("migrate up: unknown format %q (known: %s)",
				cfg.Format, knownFormatNames())
		}
	}

	for _, sd := range targets {
		p := sd.SidecarPath(cfg)
		version, err := readSidecarVersion(p)
		if err != nil {
			_, _ = fmt.Fprintf(cfg.ErrWriter, "migrate up: %s: cannot read sidecar: %v\n", sd.Name, err)
			continue
		}

		// Walk the registry for applicable migrations from this version.
		// In Phase 1 the registry is empty and this loop never executes.
		applied := 0
		for {
			step := findMigration(sd.Name, version)
			if step == nil {
				break // no more steps
			}

			if cfg.DryRun {
				_, _ = fmt.Fprintf(cfg.Writer, "  (dry-run) would migrate %s: %s → %s\n",
					sd.Name, step.FromVersion, step.ToVersion)
				version = step.ToVersion
				applied++
				result.Applied++
				continue
			}

			newContent, err := step.Migrate(p)
			if err != nil {
				return result, fmt.Errorf("migrate up: %s %s→%s: %w",
					sd.Name, step.FromVersion, step.ToVersion, err)
			}

			if err := atomicWriteSidecar(p, newContent); err != nil {
				return result, fmt.Errorf("migrate up: %s: write sidecar: %w", sd.Name, err)
			}

			_, _ = fmt.Fprintf(cfg.Writer, "  applied %s: %s → %s\n",
				sd.Name, step.FromVersion, step.ToVersion)

			version = step.ToVersion
			applied++
			result.Applied++
		}

		if applied == 0 {
			_, _ = fmt.Fprintf(cfg.Writer, "  %s: already at %s (no migrations pending)\n",
				sd.Name, sd.CurrentVersion)
			result.Skipped++
		}
	}

	if cfg.DryRun {
		_, _ = fmt.Fprintln(cfg.Writer, "(dry-run — no files written)")
	} else if result.Applied == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "All formats up-to-date; nothing to do.")
	} else {
		_, _ = fmt.Fprintf(cfg.Writer, "%d migration(s) applied.\n", result.Applied)
	}

	return result, nil
}

// ---- registry helpers -------------------------------------------------------

// findMigration returns the first registered migration for (format, fromVersion),
// or nil if none exists.
func findMigration(format, fromVersion string) *registration {
	for i := range registry {
		r := &registry[i]
		if r.Format == format && r.FromVersion == fromVersion {
			return r
		}
	}
	return nil
}

// RegisterMigration adds a migration step to the registry.
// Callers outside this package (e.g. future sessions) use this to extend the registry
// without editing the core file. Registrations are applied in the order they are added.
//
// Panics if a registration with the same (format, fromVersion) pair already exists,
// which catches duplicate registration bugs at init time.
func RegisterMigration(format, fromVersion, toVersion string, fn migrationFn) {
	for _, r := range registry {
		if r.Format == format && r.FromVersion == fromVersion {
			panic(fmt.Sprintf("migrate: duplicate registration for %s %s→%s", format, fromVersion, toVersion))
		}
	}
	registry = append(registry, registration{
		Format:      format,
		FromVersion: fromVersion,
		ToVersion:   toVersion,
		Migrate:     fn,
	})
}

// knownFormatNames returns a comma-separated list of known format names.
func knownFormatNames() string {
	names := make([]string, len(knownSidecars))
	for i, sd := range knownSidecars {
		names[i] = sd.Name
	}
	return strings.Join(names, ", ")
}

// ---- sidecar I/O ------------------------------------------------------------

// readSidecarVersion reads the version string from a sidecar file.
// Returns "" when the file does not exist (legacy; treated as current).
func readSidecarVersion(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// atomicWriteSidecar writes content to path via a temp-rename (Decision A / Q8).
func atomicWriteSidecar(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil { //nolint:gosec
		return fmt.Errorf("write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s → %s: %w", tmp, path, err)
	}
	return nil
}
