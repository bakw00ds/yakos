package paritytest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Unit tests for compareJSONL
// ---------------------------------------------------------------------------

func TestCompareJSONL_Identical(t *testing.T) {
	t.Parallel()
	a := []byte(`{"level":"info","msg":"done","ts":1717000000}` + "\n")
	b := []byte(`{"level":"info","msg":"done","ts":1717000000}` + "\n")
	if err := compareJSONL(a, b, nil); err != nil {
		t.Errorf("identical JSONL should pass; got: %v", err)
	}
}

func TestCompareJSONL_IgnoreTimestamp(t *testing.T) {
	t.Parallel()
	// ts values differ; they should be stripped before compare.
	a := []byte(`{"level":"info","msg":"done","ts":1717000000}` + "\n")
	b := []byte(`{"level":"info","msg":"done","ts":9999999999}` + "\n")
	if err := compareJSONL(a, b, []string{"ts"}); err != nil {
		t.Errorf("JSONL with ignored ts should pass; got: %v", err)
	}
}

func TestCompareJSONL_IgnoreMultipleFields(t *testing.T) {
	t.Parallel()
	a := []byte(`{"level":"info","msg":"dispatch","ts":1000,"pid":1234}` + "\n")
	b := []byte(`{"level":"info","msg":"dispatch","ts":2000,"pid":5678}` + "\n")
	if err := compareJSONL(a, b, []string{"ts", "pid"}); err != nil {
		t.Errorf("JSONL with ignored ts+pid should pass; got: %v", err)
	}
}

func TestCompareJSONL_MismatchAfterStrip(t *testing.T) {
	t.Parallel()
	// Even after stripping ts, msg differs — should fail.
	a := []byte(`{"level":"info","msg":"done","ts":1000}` + "\n")
	b := []byte(`{"level":"info","msg":"error","ts":1000}` + "\n")
	if err := compareJSONL(a, b, []string{"ts"}); err == nil {
		t.Error("expected mismatch on differing msg field; got nil error")
	}
}

func TestCompareJSONL_DifferentLineCount(t *testing.T) {
	t.Parallel()
	a := []byte(`{"msg":"a"}` + "\n" + `{"msg":"b"}` + "\n")
	b := []byte(`{"msg":"a"}` + "\n")
	if err := compareJSONL(a, b, nil); err == nil {
		t.Error("expected line count mismatch error; got nil")
	} else if !strings.Contains(err.Error(), "line count") {
		t.Errorf("error should mention 'line count'; got: %v", err)
	}
}

func TestCompareJSONL_InvalidJSON(t *testing.T) {
	t.Parallel()
	a := []byte(`not-json` + "\n")
	b := []byte(`{"msg":"a"}` + "\n")
	if err := compareJSONL(a, b, nil); err == nil {
		t.Error("expected parse error for invalid JSON; got nil")
	}
}

func TestCompareJSONL_EmptyInput(t *testing.T) {
	t.Parallel()
	if err := compareJSONL([]byte{}, []byte{}, nil); err != nil {
		t.Errorf("empty JSONL should pass; got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Unit tests for Capture (golden file writing)
// ---------------------------------------------------------------------------

func TestCapture_WritesGoldenFiles(t *testing.T) {
	// Cannot run in parallel because it uses a flag-driven path.
	// Capture creates golden files; verify they exist with expected contents.

	goldenDir := t.TempDir()

	// We invoke capture internals directly since we can't run a real binary
	// in a unit test. Simulate what Capture does by calling mustWriteGolden.
	base := goldenBase(goldenDir, "test-case")
	mustWriteGolden(t, base+".stdout", []byte("hello\n"))
	mustWriteGolden(t, base+".stderr", []byte(""))
	mustWriteGolden(t, base+".exit", []byte("0\n"))

	// Verify each file exists and has expected content.
	files := map[string]string{
		base + ".stdout": "hello\n",
		base + ".stderr": "",
		base + ".exit":   "0\n",
	}
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("golden file %s missing: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("golden file %s: got %q; want %q", path, got, want)
		}
	}
}

func TestCapture_AtomicWrite(t *testing.T) {
	t.Parallel()
	// Verifies mustWriteGolden leaves no .tmp file behind on success.
	dir := t.TempDir()
	path := filepath.Join(dir, "output.stdout")
	mustWriteGolden(t, path, []byte("content\n"))

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful write")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written golden: %v", err)
	}
	if string(data) != "content\n" {
		t.Errorf("golden content: got %q; want %q", data, "content\n")
	}
}

// ---------------------------------------------------------------------------
// Unit tests for MakeFixtureProject
// ---------------------------------------------------------------------------

func TestMakeFixtureProject_CreatesFiles(t *testing.T) {
	t.Parallel()
	layout := map[string]string{
		"CLAUDE.md":          "# Project\n",
		"subdir/agent.md":    "name: test-agent\n",
		"subdir/nested/file": "data",
	}
	dir := MakeFixtureProject(t, layout)

	for rel, want := range layout {
		path := filepath.Join(dir, rel)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("fixture file %s missing: %v", rel, err)
			continue
		}
		if string(got) != want {
			t.Errorf("fixture file %s: got %q; want %q", rel, got, want)
		}
	}
}

func TestMakeFixtureProject_EmptyLayout(t *testing.T) {
	t.Parallel()
	dir := MakeFixtureProject(t, map[string]string{})
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("fixture dir missing: %v", err)
	}
	if !fi.IsDir() {
		t.Error("fixture dir is not a directory")
	}
}

// ---------------------------------------------------------------------------
// Unit tests for helper functions
// ---------------------------------------------------------------------------

func TestNonEmptyLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input []byte
		want  int
	}{
		{[]byte("a\nb\nc\n"), 3},
		{[]byte("a\n\nb\n"), 2},
		{[]byte(""), 0},
		{[]byte("\n\n\n"), 0},
		{[]byte("single"), 1},
	}
	for _, tc := range tests {
		got := nonEmptyLines(tc.input)
		if len(got) != tc.want {
			t.Errorf("nonEmptyLines(%q): got %d lines; want %d", tc.input, len(got), tc.want)
		}
	}
}

func TestGoldenBase_Sanitizes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want string
	}{
		{"simple", "simple"},
		{"with spaces", "with-spaces"},
		{"with/slash", "with-slash"},
		{"with:colon", "with-colon"},
	}
	for _, tc := range tests {
		got := filepath.Base(goldenBase("/dir", tc.name))
		if got != tc.want {
			t.Errorf("goldenBase name=%q: got %q; want %q", tc.name, got, tc.want)
		}
	}
}

func TestFormatBytes_Truncates(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 300)
	out := formatBytes([]byte(long))
	if !strings.HasSuffix(out, "...(truncated)") {
		t.Errorf("expected truncation suffix; got %q", out[:50])
	}
}

func TestFormatBytes_ShortPassthrough(t *testing.T) {
	t.Parallel()
	short := "hello"
	out := formatBytes([]byte(short))
	if out != short {
		t.Errorf("short string should pass through unchanged; got %q", out)
	}
}

func TestDiffBytes_NoDifferences(t *testing.T) {
	t.Parallel()
	a := []byte("line1\nline2\n")
	result := diffBytes("test", a, a)
	// When identical, should say no differences.
	if !strings.Contains(result, "no line differences") {
		t.Errorf("expected 'no line differences'; got: %q", result)
	}
}

func TestDiffBytes_ReportsDifference(t *testing.T) {
	t.Parallel()
	a := []byte("line1\nlineA\n")
	b := []byte("line1\nlineB\n")
	result := diffBytes("test", a, b)
	if !strings.Contains(result, "lineA") || !strings.Contains(result, "lineB") {
		t.Errorf("diff should contain both values; got: %q", result)
	}
}

// ---------------------------------------------------------------------------
// Integration test: fake "echo" command parity via CompareExact
// ---------------------------------------------------------------------------

// TestIntegration_EchoParity verifies the harness end-to-end by using the
// system "echo" command as a stand-in for both bash and Go yakos.
// Both invocations produce identical output, so CompareExact must pass.
//
// This does NOT invoke real yakos binaries — it uses the invoke() helper
// directly with the same binary for both "bash" and "go" sides.
func TestIntegration_EchoParity(t *testing.T) {
	t.Parallel()

	echoBin, err := findEcho()
	if err != nil {
		t.Skipf("skipping echo integration test: %v", err)
	}

	dir := t.TempDir()
	bashRes := invoke(t, "bash", echoBin, []string{"hello world"}, nil, nil, dir)
	goRes := invoke(t, "go", echoBin, []string{"hello world"}, nil, nil, dir)

	if bashRes.ExitCode != 0 || goRes.ExitCode != 0 {
		t.Errorf("echo exited non-zero: bash=%d go=%d", bashRes.ExitCode, goRes.ExitCode)
	}
	if string(bashRes.Stdout) != string(goRes.Stdout) {
		t.Errorf("echo stdout mismatch: bash=%q go=%q", bashRes.Stdout, goRes.Stdout)
	}
}

// TestIntegration_MismatchReported verifies that when outputs differ,
// compareResults reports the discrepancy through the testing.T.
func TestIntegration_MismatchReported(t *testing.T) {
	// Cannot be parallel: we use a sub-test to capture the failure.
	fakeT := &captureT{}

	c := Case{
		Name:          "mismatch-test",
		StdoutCompare: CompareExact,
		ExitCodeMatch: true,
	}

	bashRes := result{Stdout: []byte("bash output\n"), ExitCode: 0}
	goRes := result{Stdout: []byte("go output\n"), ExitCode: 0}

	compareResultsTB(fakeT, c, bashRes, goRes, t.TempDir())

	if !fakeT.hadError {
		t.Error("expected compareResults to report an error for mismatched output")
	}
}

// TestIntegration_MissingBinaryFatal verifies that invoke fatals with a clear
// message when the binary path does not exist.
func TestIntegration_MissingBinaryFatal(t *testing.T) {
	t.Parallel()

	fakeT := &captureT{}
	invoke(fakeT, "bash", "/nonexistent/binary/that/does/not/exist", nil, nil, nil, t.TempDir())

	if !fakeT.hadFatal {
		t.Error("expected invoke to fatal for missing binary")
	}
	// The message may say "not found" (from os.Stat path) or "no such file or
	// directory" (from exec.Command path). Either is acceptable — the key
	// invariant is that it fatals rather than panics or silently succeeding.
	hasPath := strings.Contains(fakeT.lastMsg, "/nonexistent/binary")
	if !hasPath {
		t.Errorf("fatal message should reference the binary path; got: %q", fakeT.lastMsg)
	}
}

// findEcho returns the path to the echo binary.
func findEcho() (string, error) {
	// Try common locations.
	for _, p := range []string{"/bin/echo", "/usr/bin/echo"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// ---------------------------------------------------------------------------
// captureT — minimal testing.TB implementation for asserting fatal/error calls
// ---------------------------------------------------------------------------

// captureT is a minimal testing.TB that records whether Errorf/Fatalf was called.
// Used to test that the harness itself calls t.Fatal / t.Error correctly.
type captureT struct {
	testing.TB
	hadError bool
	hadFatal bool
	lastMsg  string
}

func (c *captureT) Helper()                           {}
func (c *captureT) Log(args ...any)                   {}
func (c *captureT) Logf(format string, args ...any)   {}
func (c *captureT) TempDir() string                   { return os.TempDir() }
func (c *captureT) Skipf(format string, args ...any)  {}
func (c *captureT) SkipNow()                          {}
func (c *captureT) Skip(args ...any)                  {}

func (c *captureT) Errorf(format string, args ...any) {
	c.hadError = true
	c.lastMsg = fmt.Sprintf(format, args...)
}

func (c *captureT) Fatalf(format string, args ...any) {
	c.hadFatal = true
	c.lastMsg = fmt.Sprintf(format, args...)
	// Don't actually exit — just record.
}

func (c *captureT) Error(args ...any) {
	c.hadError = true
}

func (c *captureT) Fatal(args ...any) {
	c.hadFatal = true
}

// Cleanup calls f immediately (no deferred cleanup in the stub).
func (c *captureT) Cleanup(f func()) { f() }
