package main

// binary_requirement_test.go — K-137: a test that needs the built bin/yakos must
// not skip quietly in CI. go-ci.yml runs `make build` before `go test` (except on
// Windows) and says a missing binary FAILS under CI=true; on a developer machine
// it skips with a hint. The K-137 binary-driven tests share this one rule instead
// of each deciding for itself.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// testGOOS is the OS the binary-driven tests believe they run on. A test
// overrides it to exercise the Windows rule on any host.
var testGOOS = runtime.GOOS

// skipOnWindows skips a binary-driven test on Windows, in CI too: go-ci.yml
// builds bin/yakos before go test only where runner.os is not Windows, so there
// the binary is never present and "fail when it is missing" would fail every
// such test.
func skipOnWindows(t testing.TB) {
	t.Helper()
	if testGOOS == "windows" {
		t.Skip("binary-driven tests do not run on Windows: go-ci.yml builds no binary there before go test")
	}
}

// skipOrFailInCI skips the test, or fails it when ci is "true" (the value GitHub
// Actions sets): a precondition the CI workflow guarantees has to be a failure
// there, or a broken build step turns every such test into a quiet skip.
func skipOrFailInCI(t testing.TB, ci, format string, args ...any) {
	t.Helper()
	if ci == "true" {
		t.Fatalf(format+" (CI must not skip this: go-ci.yml builds bin/yakos before go test)", args...)
		return
	}
	t.Skipf(format, args...)
}

// requireBinaryAt returns bin when it exists. When it is missing the test fails
// under CI=true and skips otherwise.
func requireBinaryAt(t testing.TB, bin, ci string) string {
	t.Helper()
	if _, err := os.Stat(bin); err != nil {
		skipOrFailInCI(t, ci, "Go yakos binary not found at %q (run make build): %v", bin, err)
		return ""
	}
	return bin
}

// recordingTB lets a test watch what a helper does to the test it is given
// without ending itself. Like the real one, Skip and Fatal stop the goroutine
// they are called on, so use record to run a helper against it.
type recordingTB struct {
	testing.TB
	skipped, failed bool
}

func (r *recordingTB) Helper()               {}
func (r *recordingTB) Skip(...any)           { r.skipped = true; runtime.Goexit() }
func (r *recordingTB) Skipf(string, ...any)  { r.skipped = true; runtime.Goexit() }
func (r *recordingTB) Fatalf(string, ...any) { r.failed = true; runtime.Goexit() }

// record runs fn against a fresh recordingTB on a goroutine of its own, as the
// testing package runs a test, so a Skip or Fatal inside fn stops fn where it is.
func record(fn func(t testing.TB)) *recordingTB {
	rec := &recordingTB{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(rec)
	}()
	<-done
	return rec
}

func TestRequireBinaryAt_FailsUnderCIAndSkipsElsewhere(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "yakos")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "no-such-yakos")

	cases := []struct {
		name, bin, ci      string
		wantSkip, wantFail bool
		wantTheBinaryBack  bool
	}{
		{"missing under CI=true fails", missing, "true", false, true, false},
		{"missing on a developer machine skips", missing, "", true, false, false},
		{"missing with CI=false skips", missing, "false", true, false, false},
		{"present under CI=true runs", present, "true", false, false, true},
		{"present on a developer machine runs", present, "", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			rec := record(func(tb testing.TB) { got = requireBinaryAt(tb, c.bin, c.ci) })
			if rec.skipped != c.wantSkip || rec.failed != c.wantFail {
				t.Errorf("skipped=%v failed=%v, want skipped=%v failed=%v", rec.skipped, rec.failed, c.wantSkip, c.wantFail)
			}
			if (got == c.bin) != c.wantTheBinaryBack {
				t.Errorf("returned %q, want the path back only when the binary exists", got)
			}
		})
	}
}

func TestSkipOrFailInCI_OnlyTheExactCIValueFails(t *testing.T) {
	for _, c := range []struct {
		ci       string
		wantFail bool
	}{{"true", true}, {"", false}, {"false", false}, {"1", false}} {
		rec := record(func(tb testing.TB) { skipOrFailInCI(tb, c.ci, "needs a thing") })
		if rec.failed != c.wantFail || rec.skipped == c.wantFail {
			t.Errorf("CI=%q: failed=%v skipped=%v, want failed=%v", c.ci, rec.failed, rec.skipped, c.wantFail)
		}
	}
}

// The two K-137 binary-driven helpers must use the shared rule, not a skip of their own.

func TestPolicyBinary_SkipsOnWindowsEvenInCI(t *testing.T) {
	// go-ci.yml builds no binary on Windows before go test, so a missing binary there is
	// expected and must not become a failure. testGOOS makes the rule testable on any host.
	old := testGOOS
	testGOOS = "windows"
	t.Cleanup(func() { testGOOS = old })
	t.Setenv("YAKOS_GO_BINARY", filepath.Join(t.TempDir(), "no-such-yakos"))
	t.Setenv("CI", "true")
	rec := record(func(tb testing.TB) { _ = policyBinary(tb) })
	if !rec.skipped || rec.failed {
		t.Errorf("on Windows in CI: skipped=%v failed=%v, want skipped and not failed", rec.skipped, rec.failed)
	}
}

func TestPolicyBinary_FailsInCIAndSkipsLocallyWhenTheBinaryIsMissing(t *testing.T) {
	old := testGOOS
	testGOOS = "linux" // the rule under test applies off Windows, whatever the host
	t.Cleanup(func() { testGOOS = old })
	t.Setenv("YAKOS_GO_BINARY", filepath.Join(t.TempDir(), "no-such-yakos"))
	for _, c := range []struct {
		ci       string
		wantFail bool
	}{{"true", true}, {"", false}} {
		t.Setenv("CI", c.ci)
		rec := record(func(tb testing.TB) { _ = policyBinary(tb) })
		if rec.failed != c.wantFail || rec.skipped == c.wantFail {
			t.Errorf("CI=%q: failed=%v skipped=%v, want failed=%v", c.ci, rec.failed, rec.skipped, c.wantFail)
		}
	}
}

func TestTwinSetup_FailsInCIAndSkipsLocallyWithoutTheBinaryOrTheBashTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("twinSetup skips on Windows before it looks for anything")
	}
	root := t.TempDir()
	withBinary := filepath.Join(root, "bin", "yakos") // exists, but no cli/yakos tree beside it
	if err := os.MkdirAll(filepath.Dir(withBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(withBinary, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, bin string
	}{
		{"no binary", filepath.Join(root, "bin", "no-such-yakos")},
		{"binary but no bash tree", withBinary},
	} {
		t.Setenv("YAKOS_GO_BINARY", c.bin)
		for _, ci := range []struct {
			val      string
			wantFail bool
		}{{"true", true}, {"", false}} {
			t.Setenv("CI", ci.val)
			rec := record(func(tb testing.TB) { _ = twinSetup(tb) })
			if rec.failed != ci.wantFail || rec.skipped == ci.wantFail {
				t.Errorf("%s, CI=%q: failed=%v skipped=%v, want failed=%v", c.name, ci.val, rec.failed, rec.skipped, ci.wantFail)
			}
		}
	}
}
