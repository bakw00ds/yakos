package team

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// RunArchiveBash shells out to cli/lib/archive.sh using the same env-var
// wiring that the bash `yakos` entry-point uses.  This is the production
// implementation of ArchiveFn for as long as `yakos archive` has not been
// ported to Go (rank 10 in the port plan).
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
