// version_parity_test.go demonstrates the paritytest harness for the simplest
// possible subcommand: --version.
//
// How this test works:
//
//  1. Both bash yakos and Go yakos are invoked with the "--version" arg.
//  2. bash produces:  "yakos 0.36.0.0"      (no suffix)
//     Go produces:    "0.36.0.0 (go)"        (different format, "(go)" suffix)
//  3. Because the output formats legitimately differ during Phase 1 shadow mode,
//     we use StdoutTransform hooks to normalize both sides to the bare version
//     string before comparing — asserting they agree on the version number
//     even though the formatting differs.
//  4. Both must exit 0.
//
// This file is the canonical reference for how every future Phase 1 port writes
// its parity test. Copy the structure; adapt the comparison mode and transforms
// for each subcommand's parity contract.
//
// To re-capture the bash golden baseline:
//
//	cd cli-go && go test ./cmd/yakos/... -run TestVersionParity -update-goldens -v
//
// To run just this parity test:
//
//	cd cli-go && go test ./cmd/yakos/... -run TestVersionParity -v
package main

import (
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/paritytest"
)

// versionNumRE matches the numeric version in both output formats:
//
//	"yakos 0.36.0.0"   →  "0.36.0.0"
//	"0.36.0.0 (go)"    →  "0.36.0.0"
var versionNumRE = regexp.MustCompile(`\d+\.\d+\.\d+\.\d+|\d+\.\d+\.\d+`)

// extractVersionNumber strips formatting from a --version output line,
// returning just the version number followed by a newline.
// Used as a StdoutTransform hook so both bash ("yakos 0.36.0.0") and Go
// ("0.36.0.0 (go)") are normalized to "0.36.0.0\n" before comparison.
func extractVersionNumber(b []byte) []byte {
	m := versionNumRE.Find(bytes.TrimSpace(b))
	if m == nil {
		return b // unchanged — comparison will fail with a clear message
	}
	return append(m, '\n')
}

// TestVersionParity is the worked-example parity test for "yakos --version".
//
// Bash output format:  "yakos <version>"   (e.g. "yakos 0.36.0.0")
// Go output format:    "<version> (go)"    (e.g. "0.36.0.0 (go)")
//
// The test normalizes both sides to the bare version number before comparing,
// asserting version number agreement without demanding format identity.
// Both must exit 0.
func TestVersionParity(t *testing.T) {
	skipIfBinariesMissing(t)

	paritytest.Run(t, paritytest.Case{
		Name:          "version-baseline",
		Args:          []string{"--version"},
		StdoutCompare: paritytest.CompareGolden,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,

		// Normalize both sides to the bare version number before compare.
		// This lets bash emit "yakos 0.36.0.0" and Go emit "0.36.0.0 (go)"
		// while still asserting they agree on the version value.
		StdoutTransformBash: extractVersionNumber,
		StdoutTransformGo:   extractVersionNumber,

		// GoldenDir is relative to this file. Golden files are tracked in git
		// so CI can compare without running bash at golden-capture time.
		GoldenDir: "testdata/golden",
	})
}

// TestVersionParity_ExitZero explicitly asserts both binaries exit 0 for
// --version, independent of output content. This protects against an
// implementation that prints the right text but exits non-zero.
func TestVersionParity_ExitZero(t *testing.T) {
	skipIfBinariesMissing(t)

	paritytest.Run(t, paritytest.Case{
		Name:          "version-exit-zero",
		Args:          []string{"--version"},
		StdoutCompare: paritytest.CompareIgnore,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// TestVersionParity_VersionFormat verifies that both outputs contain a
// recognizable version number. Uses CompareRegex so neither output is
// required to be identical — both must just match the pattern.
func TestVersionParity_VersionFormat(t *testing.T) {
	skipIfBinariesMissing(t)

	paritytest.Run(t, paritytest.Case{
		Name:          "version-format",
		Args:          []string{"--version"},
		StdoutCompare: paritytest.CompareRegex,
		StdoutRegex:   `\d+\.\d+`,
		StderrCompare: paritytest.CompareIgnore,
		ExitCodeMatch: true,
	})
}

// TestVersionParity_GoSuffix asserts that the Go binary appends the "(go)"
// transition suffix. This validates the shadow-mode distinguisher without
// requiring the bash binary to be present.
func TestVersionParity_GoSuffix(t *testing.T) {
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (build with `make build`): %v", goBin, err)
	}

	out, err := captureOutput(goBin, []string{"--version"})
	if err != nil {
		t.Fatalf("running Go yakos --version: %v", err)
	}
	if !strings.Contains(out, "(go)") {
		t.Errorf("Go yakos --version should contain '(go)'; got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// skipIfBinariesMissing skips t if either the bash or Go binary is absent.
// This keeps parity tests from failing in environments where only one binary
// is present (e.g., a Go-only CI step that hasn't installed bash yakos).
func skipIfBinariesMissing(t *testing.T) {
	t.Helper()
	goBin := resolveGoBinary()
	if _, err := os.Stat(goBin); err != nil {
		t.Skipf("Go yakos binary not found at %q (build with `make build`): %v", goBin, err)
	}
	bashBin := resolveBashBinary()
	if _, err := os.Stat(bashBin); err != nil {
		t.Skipf("bash yakos binary not found at %q: %v", bashBin, err)
	}
}

// resolveGoBinary returns the Go binary path, preferring $YAKOS_GO_BINARY.
func resolveGoBinary() string {
	if v := os.Getenv("YAKOS_GO_BINARY"); v != "" {
		return v
	}
	return repoRoot(new(testing.T)) + "/bin/yakos"
}

// resolveBashBinary returns the bash binary path, preferring $YAKOS_BASH_BINARY.
func resolveBashBinary() string {
	if v := os.Getenv("YAKOS_BASH_BINARY"); v != "" {
		return v
	}
	return "/Users/tw/github/yakOS/cli/yakos"
}

// captureOutput runs bin with args and returns combined stdout as a string.
// When bin is the Go binary, YAKOS_IMPL=go is injected so Go-native routing
// is active. (Without it the guard in main() proxies to bash, which would
// produce bash-format output and defeat tests asserting Go-specific behavior.)
func captureOutput(bin string, args []string) (string, error) {
	cmd := exec.Command(bin, args...) //nolint:gosec
	cmd.Env = append(os.Environ(), "YAKOS_IMPL=go")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "", err
		}
	}
	return out.String(), nil
}
