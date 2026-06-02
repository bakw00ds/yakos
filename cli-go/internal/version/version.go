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

// suffix appended to every version string during the bash-to-Go transition.
// Remove this constant (and callers) once Go replaces bash entirely.
const goSuffix = "(go)"

// Read returns the version string from the VERSION file located at yakosRoot,
// with " (go)" appended. yakosRoot is the absolute path to the repository root
// (the directory that contains the VERSION file).
func Read(yakosRoot string) (string, error) {
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
