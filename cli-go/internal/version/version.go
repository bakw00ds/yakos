// Package version reads the root VERSION file and returns the version string
// used by the Go binary.
package version

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Version is set at build time by -ldflags "-X github.com/bakw00ds/yakos/internal/version.Version=<v>".
// When non-empty it takes precedence over the VERSION file on disk so that
// Go-only installs (no bash yakos tree, no VERSION file) can still report
// their version.  Dev builds that omit the flag leave this empty and fall
// back to the file.
var Version string

// suffix appended to every version string during the bash-to-Go transition.
// Remove this constant (and callers) once Go replaces bash entirely.
const goSuffix = "(go)"

// Read returns the version string, with " (go)" appended.
//
// Resolution order:
//  1. The compiled-in Version variable (set via -ldflags at release build time).
//     This path works on Go-only installs that ship no VERSION file.
//  2. The VERSION file at <yakosRoot>/VERSION.
//     This path works for source/bash-tree installs and dev builds where the
//     ldflags variable is not injected.
//
// yakosRoot is the absolute path to the repository root.  It is only
// consulted when the compiled-in variable is empty.
func Read(yakosRoot string) (string, error) {
	if v := strings.TrimSpace(Version); v != "" {
		return v + " " + goSuffix, nil
	}

	path := filepath.Join(yakosRoot, "VERSION")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("version: reading %s: %w", path, err)
	}
	v := strings.TrimSpace(string(data))
	if v == "" {
		return "", fmt.Errorf("version: %s is empty", path)
	}
	return v + " " + goSuffix, nil
}

// BinaryRoot returns the absolute path to the repository root, inferred from
// the location of the running executable. The Go binary lives at
// <repo-root>/bin/yakos (installed via make install or make build), so the
// root is three levels up from the binary.
//
// Callers that know the root explicitly (e.g. tests) should pass it directly
// to Read rather than using this helper.
func BinaryRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("version: resolving executable path: %w", err)
	}
	// exe → <repo>/bin/yakos  ⇒  root = exe/../..
	root := filepath.Dir(filepath.Dir(exe))
	return root, nil
}

// GoVersion returns the Go toolchain version string used to build the binary.
func GoVersion() string {
	return runtime.Version()
}
