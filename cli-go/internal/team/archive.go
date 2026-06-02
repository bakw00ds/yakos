package team

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bakw00ds/yakos/internal/archive"
)

// RunArchive is the production ArchiveFn used by Restart.
//
// When YAKOS_IMPL=go it calls archive.Run directly (native Go implementation,
// rank 10 in the port plan). Otherwise it falls back to RunArchiveBash which
// shells out to cli/lib/archive.sh — preserving bash-parity during the
// transition period.
//
// The native path always passes AutoTag=true to mirror the --auto-tag flag
// that team restart uses, matching the previous RunArchiveBash call.
func RunArchive(yakosRoot, project, tag string) error {
	if os.Getenv("YAKOS_IMPL") == "go" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/tmp"
		}
		cfg := archive.Config{
			YakosRoot: yakosRoot,
			Project:   project,
			Tag:       tag,
			AutoTag:   true,
			HomeDir:   home,
			Writer:    os.Stdout,
			ErrWriter: os.Stderr,
		}
		if _, err := archive.Run(cfg); err != nil {
			return fmt.Errorf("archive: %w", err)
		}
		return nil
	}
	return RunArchiveBash(yakosRoot, project, tag)
}

// RunArchiveBash shells out to cli/lib/archive.sh using the same env-var
// wiring that the bash `yakos` entry-point uses.  This is retained as a
// fallback for YAKOS_IMPL != "go" during the Phase 1 transition period.
//
// It runs:
//
//	bash <yakosRoot>/cli/lib/archive.sh <project> <tag> --auto-tag --yes
//
// with YAKOS_ROOT and YAKOS_LIB set in the subprocess environment.
//
// stdout and stderr are forwarded to the calling process's standard streams
// so that the user sees the archive progress output in real time.
func RunArchiveBash(yakosRoot, project, tag string) error {
	archiveScript := filepath.Join(yakosRoot, "cli", "lib", "archive.sh")
	if _, err := os.Stat(archiveScript); err != nil {
		return fmt.Errorf("archive.sh not found at %s: %w", archiveScript, err)
	}

	cmd := exec.Command("bash", archiveScript, project, tag, "--auto-tag", "--yes") //nolint:gosec
	cmd.Env = append(os.Environ(),
		"YAKOS_ROOT="+yakosRoot,
		"YAKOS_LIB="+filepath.Join(yakosRoot, "lib"),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("archive.sh: %w", err)
	}
	return nil
}
