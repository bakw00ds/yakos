// Package paritytest provides a framework for comparing Go subcommand output
// against the bash baseline. Each Phase 1 subcommand port uses this to verify
// byte-for-byte (or schema-equivalent) parity with the bash yakos.
//
// # Overview
//
// A [Case] describes one parity scenario: which args to pass, what env vars
// to set, how to populate a scratch workdir, and how to compare the resulting
// stdout/stderr/exit code between bash yakos and Go yakos.
//
// [Run] executes both binaries and asserts the comparison mode succeeds.
// [Capture] re-runs only bash yakos and writes golden files that [Run] can
// later compare against.
//
// # Binary resolution
//
// Bash yakos: $YAKOS_BASH_BINARY (default: /Users/tw/github/yakOS/cli/yakos)
// Go yakos:   $YAKOS_GO_BINARY   (default: ./bin/yakos relative to the repo root)
//
// # Golden files
//
// Golden files live under testdata/golden/<case-name>.{stdout,stderr,exit}
// relative to the calling test's source package. Re-generate them with:
//
//	go test ./... -update-goldens
//
// CI always runs in comparison mode (no -update-goldens flag).
package paritytest

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// UpdateGoldens is true when the -update-goldens flag is passed.
// Test binaries register this flag at init time; CI should never set it.
var UpdateGoldens = flag.Bool("update-goldens", false,
	"re-capture bash baseline golden files instead of comparing")

// CompareMode describes how a stream is compared between bash and Go.
type CompareMode int

const (
	// CompareExact requires byte-for-byte identical output.
	CompareExact CompareMode = iota

	// CompareJSONL performs line-by-line JSON comparison.
	// Fields listed in [Case].JSONLIgnoreFields are stripped before compare.
	CompareJSONL

	// CompareRegex asserts that the bash output matches the given regex.
	// The Go output must also match the same regex.
	// Use this when both outputs are expected to contain a dynamic value
	// (timestamp, pid) that follows a known format.
	CompareRegex

	// CompareGolden compares the Go output against a previously captured
	// baseline file. The baseline is written by [Capture] (or Run when
	// -update-goldens is set). The path is resolved relative to the
	// test file's directory.
	CompareGolden

	// CompareIgnore skips comparison for this stream.
	CompareIgnore
)

// Case is one parity test scenario.
type Case struct {
	// Name is the human-readable label used in test output and golden filenames.
	// Example: "validate-clean-agent"
	Name string

	// Args are the command-line arguments passed to both bash and Go yakos.
	Args []string

	// Env contains extra environment variables set for both invocations.
	// These are merged with the current process environment; use empty string
	// values to unset variables via the exec.Cmd.Env mechanism (not supported
	// directly — callers must use "KEY=" to clear on POSIX; prefer explicit
	// sets only).
	Env map[string]string

	// WorkdirSetup is called with a fresh temporary directory before both
	// invocations. Use it to write fixture files. The workdir is the working
	// directory for both binaries.
	// If nil, the temporary directory is left empty.
	WorkdirSetup func(t testing.TB, dir string)

	// Stdin is optional bytes to send to both binaries on stdin.
	Stdin []byte

	// StdoutCompare controls how stdout is compared.
	StdoutCompare CompareMode

	// StderrCompare controls how stderr is compared.
	// Defaults to CompareIgnore when zero (uncommon to assert stderr exactly).
	StderrCompare CompareMode

	// ExitCodeMatch, when true, requires the Go binary to exit with the same
	// code as bash. Almost always true.
	ExitCodeMatch bool

	// StdoutRegex is used when StdoutCompare == CompareRegex. The bash output
	// must match; the Go output must also match. They need not be identical.
	StdoutRegex string

	// StderrRegex is used when StderrCompare == CompareRegex.
	StderrRegex string

	// GoldenDir is the directory under which golden files are stored.
	// Resolved relative to the calling test's source directory if relative.
	// Defaults to "testdata/golden" relative to the calling test's source file.
	GoldenDir string

	// JSONLIgnoreFields lists JSON field names whose values are stripped
	// before JSONL comparison. Use for timestamps, pids, or other
	// per-invocation values that are expected to differ.
	JSONLIgnoreFields []string

	// StdoutTransformBash is an optional hook that transforms the bash stdout
	// before comparison. Use it to normalize known differences (e.g., strip a
	// version suffix) without changing the CompareMode for the whole stream.
	StdoutTransformBash func([]byte) []byte

	// StdoutTransformGo is the corresponding transform for Go stdout.
	StdoutTransformGo func([]byte) []byte
}

// result holds the captured output of one binary invocation.
type result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Run executes c against both bash and Go yakos and reports any differences.
// If -update-goldens is set it delegates to Capture instead.
func Run(t *testing.T, c Case) {
	t.Helper()

	if *UpdateGoldens {
		Capture(t, c)
		return
	}

	bashBin := bashBinary()
	goBin := goBinary()

	workdir := setupWorkdir(t, c)

	bashResult := invoke(t, "bash", bashBin, c.Args, c.Env, c.Stdin, workdir)
	goResult := invoke(t, "go", goBin, c.Args, c.Env, c.Stdin, workdir)

	compareResultsTB(t, c, bashResult, goResult, callerGoldenDir(t, c))
}

// Capture runs only bash yakos and writes its output to golden files.
// Called by Run when -update-goldens is set, or directly to seed baselines.
func Capture(t *testing.T, c Case) {
	t.Helper()

	bashBin := bashBinary()
	workdir := setupWorkdir(t, c)
	r := invoke(t, "bash", bashBin, c.Args, c.Env, c.Stdin, workdir)

	goldenDir := callerGoldenDir(t, c)
	if err := os.MkdirAll(goldenDir, 0755); err != nil {
		t.Fatalf("paritytest: creating golden dir %s: %v", goldenDir, err)
	}

	base := goldenBase(goldenDir, c.Name)
	mustWriteGolden(t, base+".stdout", r.Stdout)
	mustWriteGolden(t, base+".stderr", r.Stderr)
	mustWriteGolden(t, base+".exit", []byte(strconv.Itoa(r.ExitCode)+"\n"))
}

// MakeFixtureProject creates a temporary directory with the given file layout
// and returns its absolute path. The map keys are relative file paths; the
// values are file contents. Parent directories are created automatically.
// The directory and all its contents are cleaned up by t.Cleanup.
func MakeFixtureProject(t testing.TB, layout map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range layout {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatalf("paritytest: MakeFixtureProject mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			t.Fatalf("paritytest: MakeFixtureProject write %s: %v", abs, err)
		}
	}
	return dir
}

// ---- internal helpers -------------------------------------------------------

// bashBinary returns the bash yakos binary path from $YAKOS_BASH_BINARY or
// the default hard-coded location relative to the repo root.
func bashBinary() string {
	if v := os.Getenv("YAKOS_BASH_BINARY"); v != "" {
		return v
	}
	return "/Users/tw/github/yakOS/cli/yakos"
}

// goBinary returns the Go yakos binary path from $YAKOS_GO_BINARY or the
// default location derived from the repo root (cli-go/../bin/yakos).
func goBinary() string {
	if v := os.Getenv("YAKOS_GO_BINARY"); v != "" {
		return v
	}
	// Default: locate the repo root relative to this source file and return
	// <repo-root>/bin/yakos.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		// Fallback: relative path from cwd.
		return "./bin/yakos"
	}
	// thisFile = <repo>/cli-go/internal/paritytest/paritytest.go
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	return filepath.Join(repoRoot, "bin", "yakos")
}

// setupWorkdir creates a temp dir and runs c.WorkdirSetup in it if non-nil.
func setupWorkdir(t testing.TB, c Case) string {
	t.Helper()
	dir := t.TempDir()
	if c.WorkdirSetup != nil {
		c.WorkdirSetup(t, dir)
	}
	return dir
}

// invoke runs the binary at binPath with args/env/stdin in workdir,
// capturing stdout and stderr into buffers.
// Invocation errors that are not exit-code errors are reported as fatal.
func invoke(t testing.TB, label, binPath string, args []string, env map[string]string, stdin []byte, workdir string) result {
	t.Helper()

	// Validate binary exists before running.
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("paritytest: %s binary not found at %q: %v\n"+
			"Set YAKOS_BASH_BINARY / YAKOS_GO_BINARY to override the path.",
			label, binPath, err)
	}

	cmd := exec.Command(binPath, args...) //nolint:gosec // controlled test paths
	cmd.Dir = workdir

	// Build env: inherit current env, then apply overrides.
	// For Go binary invocations, inject YAKOS_IMPL=go so that the binary uses
	// Go-native routing. Without this, the guard in main() proxies everything to
	// bash, defeating the parity comparison. Callers can override via Case.Env.
	base := os.Environ()
	merged := make([]string, 0, len(base)+len(env)+1)
	merged = append(merged, base...)
	if label == "go" {
		merged = append(merged, "YAKOS_IMPL=go")
	}
	for k, v := range env {
		merged = append(merged, k+"="+v)
	}
	cmd.Env = merged

	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	exitCode := 0
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("paritytest: invoking %s yakos (%s): %v", label, binPath, err)
		}
	}

	return result{
		Stdout:   outBuf.Bytes(),
		Stderr:   errBuf.Bytes(),
		ExitCode: exitCode,
	}
}

// compareResultsTB applies the comparison knobs in c to bashRes and goRes,
// reporting failures via t. Uses testing.TB so unit tests can pass a stub.
func compareResultsTB(t testing.TB, c Case, bashRes, goRes result, goldenDir string) {
	t.Helper()

	// --- exit code ---
	if c.ExitCodeMatch && bashRes.ExitCode != goRes.ExitCode {
		t.Errorf("paritytest %q: exit code mismatch: bash=%d go=%d",
			c.Name, bashRes.ExitCode, goRes.ExitCode)
	}

	// --- stdout ---
	bashOut := bashRes.Stdout
	goOut := goRes.Stdout
	if c.StdoutTransformBash != nil {
		bashOut = c.StdoutTransformBash(bashOut)
	}
	if c.StdoutTransformGo != nil {
		goOut = c.StdoutTransformGo(goOut)
	}
	compareStream(t, c, "stdout", c.StdoutCompare, c.StdoutRegex, bashOut, goOut, goldenDir)

	// --- stderr ---
	compareStream(t, c, "stderr", c.StderrCompare, c.StderrRegex, bashRes.Stderr, goRes.Stderr, goldenDir)
}

// compareStream compares one output stream according to mode.
func compareStream(
	t testing.TB,
	c Case,
	streamName string,
	mode CompareMode,
	regexPat string,
	bashData, goData []byte,
	goldenDir string,
) {
	t.Helper()

	switch mode {
	case CompareIgnore:
		// nothing

	case CompareExact:
		if !bytes.Equal(bashData, goData) {
			t.Errorf("paritytest %q: %s mismatch\n  bash: %s\n  go:   %s\n  diff:\n%s",
				c.Name, streamName,
				formatBytes(bashData), formatBytes(goData),
				diffBytes(streamName, bashData, goData))
		}

	case CompareJSONL:
		if err := compareJSONL(bashData, goData, c.JSONLIgnoreFields); err != nil {
			t.Errorf("paritytest %q: %s JSONL mismatch: %v", c.Name, streamName, err)
		}

	case CompareRegex:
		if regexPat == "" {
			t.Errorf("paritytest %q: %s CompareRegex mode requires a non-empty regex pattern", c.Name, streamName)
			return
		}
		re, err := regexp.Compile(regexPat)
		if err != nil {
			t.Fatalf("paritytest %q: %s invalid regex %q: %v", c.Name, streamName, regexPat, err)
		}
		if !re.Match(bashData) {
			t.Errorf("paritytest %q: bash %s does not match regex %q\n  bash output: %s",
				c.Name, streamName, regexPat, formatBytes(bashData))
		}
		if !re.Match(goData) {
			t.Errorf("paritytest %q: go %s does not match regex %q\n  go output: %s",
				c.Name, streamName, regexPat, formatBytes(goData))
		}

	case CompareGolden:
		base := goldenBase(goldenDir, c.Name)
		goldenPath := base + "." + streamName
		golden, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Errorf("paritytest %q: reading golden file %s: %v\n"+
				"  Run with -update-goldens to capture the baseline.",
				c.Name, goldenPath, err)
			return
		}
		if !bytes.Equal(golden, goData) {
			t.Errorf("paritytest %q: go %s differs from golden %s\n  diff:\n%s",
				c.Name, streamName, goldenPath, diffBytes(goldenPath, golden, goData))
		}
	}
}

// compareJSONL compares two JSONL byte slices line by line.
// Fields in ignoreFields are deleted from each parsed object before comparison.
func compareJSONL(a, b []byte, ignoreFields []string) error {
	aLines := nonEmptyLines(a)
	bLines := nonEmptyLines(b)
	if len(aLines) != len(bLines) {
		return fmt.Errorf("line count: bash=%d go=%d", len(aLines), len(bLines))
	}
	for i, al := range aLines {
		bl := bLines[i]
		aObj, err := parseAndStrip(al, ignoreFields)
		if err != nil {
			return fmt.Errorf("line %d (bash): %w", i+1, err)
		}
		bObj, err := parseAndStrip(bl, ignoreFields)
		if err != nil {
			return fmt.Errorf("line %d (go): %w", i+1, err)
		}
		aJSON, _ := json.Marshal(aObj)
		bJSON, _ := json.Marshal(bObj)
		if !bytes.Equal(aJSON, bJSON) {
			return fmt.Errorf("line %d mismatch:\n  bash: %s\n  go:   %s", i+1, aJSON, bJSON)
		}
	}
	return nil
}

// parseAndStrip unmarshals a JSON line and deletes any keys in ignoreFields.
func parseAndStrip(line string, ignoreFields []string) (map[string]any, error) {
	var obj map[string]any
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return nil, fmt.Errorf("JSON parse: %w (input: %q)", err, line)
	}
	for _, f := range ignoreFields {
		delete(obj, f)
	}
	return obj, nil
}

// nonEmptyLines splits b on newlines and returns non-empty lines.
func nonEmptyLines(b []byte) []string {
	raw := strings.Split(string(b), "\n")
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// callerGoldenDir returns the resolved golden directory for c.
// If c.GoldenDir is set it is returned (resolved relative to the calling
// test's source file). Otherwise "testdata/golden" beside the test source.
func callerGoldenDir(t *testing.T, c Case) string {
	t.Helper()
	// Walk up the call stack to find the first frame outside this package.
	// Skip frames 0–N until we're past paritytest package frames.
	var callerFile string
	for skip := 1; skip < 20; skip++ {
		_, file, _, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		if !strings.Contains(file, "/paritytest/") {
			callerFile = file
			break
		}
	}
	if callerFile == "" {
		t.Log("paritytest: could not determine caller file; using current dir for goldens")
		callerFile = "."
	}

	base := filepath.Dir(callerFile)
	if c.GoldenDir != "" {
		if filepath.IsAbs(c.GoldenDir) {
			return c.GoldenDir
		}
		return filepath.Join(base, c.GoldenDir)
	}
	return filepath.Join(base, "testdata", "golden")
}

// goldenBase returns the path prefix for golden files (without stream extension).
func goldenBase(dir, name string) string {
	// Sanitize name to be filesystem-safe.
	safe := strings.NewReplacer(
		"/", "-",
		" ", "-",
		":", "-",
	).Replace(name)
	return filepath.Join(dir, safe)
}

// mustWriteGolden writes data to path atomically (temp + rename).
func mustWriteGolden(t testing.TB, path string, data []byte) {
	t.Helper()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		t.Fatalf("paritytest: writing golden %s: %v", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("paritytest: renaming golden %s: %v", path, err)
	}
}

// formatBytes returns a human-readable representation of b, truncated.
func formatBytes(b []byte) string {
	const maxLen = 200
	s := string(b)
	if len(s) > maxLen {
		return s[:maxLen] + "...(truncated)"
	}
	return s
}

// diffBytes produces a simple line-diff between a and b for error output.
func diffBytes(label string, a, b []byte) string {
	aLines := strings.Split(string(a), "\n")
	bLines := strings.Split(string(b), "\n")

	var sb strings.Builder
	maxLen := len(aLines)
	if len(bLines) > maxLen {
		maxLen = len(bLines)
	}
	for i := 0; i < maxLen; i++ {
		al, bl := "", ""
		if i < len(aLines) {
			al = aLines[i]
		}
		if i < len(bLines) {
			bl = bLines[i]
		}
		if al != bl {
			fmt.Fprintf(&sb, "  line %d %s\n    -: %q\n    +: %q\n", i+1, label, al, bl)
		}
	}
	if sb.Len() == 0 {
		return "  (no line differences found — may be whitespace or encoding)"
	}
	return sb.String()
}
