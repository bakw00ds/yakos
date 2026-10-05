package main

// binary_requirement_test.go — K-137: a test that needs the built bin/yakos must
// not skip quietly in CI. go-ci.yml runs `make build` before `go test` and says a
// missing binary FAILS under CI=true; on a developer machine it skips with a
// hint. The K-137 binary-driven tests share this one rule instead of each
// deciding for itself.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
// without ending itself: Skipf and Fatalf are recorded, and the helper's own
// return statements stop it (the real ones call runtime.Goexit).
type recordingTB struct {
	testing.TB
	skipped, failed bool
}

func (r *recordingTB) Helper()               {}
func (r *recordingTB) Skip(...any)           { r.skipped = true }
func (r *recordingTB) Skipf(string, ...any)  { r.skipped = true }
func (r *recordingTB) Fatalf(string, ...any) { r.failed = true }

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
		wantReturnsThePath bool
	}{
		{"missing under CI=true fails", missing, "true", false, true, false},
		{"missing on a developer machine skips", missing, "", true, false, false},
		{"missing with CI=false skips", missing, "false", true, false, false},
		{"present under CI=true runs", present, "true", false, false, true},
		{"present on a developer machine runs", present, "", false, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recordingTB{}
			got := requireBinaryAt(rec, c.bin, c.ci)
			if rec.skipped != c.wantSkip || rec.failed != c.wantFail {
				t.Errorf("skipped=%v failed=%v, want skipped=%v failed=%v", rec.skipped, rec.failed, c.wantSkip, c.wantFail)
			}
			if (got == c.bin) != c.wantReturnsThePath {
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
		rec := &recordingTB{}
		skipOrFailInCI(rec, c.ci, "needs a thing")
		if rec.failed != c.wantFail || rec.skipped == c.wantFail {
			t.Errorf("CI=%q: failed=%v skipped=%v, want failed=%v", c.ci, rec.failed, rec.skipped, c.wantFail)
		}
	}
}

// The two K-137 binary-driven helpers must use the shared rule, not a skip of their own.

func TestPolicyBinary_FailsInCIAndSkipsLocallyWhenTheBinaryIsMissing(t *testing.T) {
	t.Setenv("YAKOS_GO_BINARY", filepath.Join(t.TempDir(), "no-such-yakos"))
	for _, c := range []struct {
		ci       string
		wantFail bool
	}{{"true", true}, {"", false}} {
		t.Setenv("CI", c.ci)
		rec := &recordingTB{}
		_ = policyBinary(rec)
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
			rec := &recordingTB{}
			_ = twinSetup(rec)
			if rec.failed != ci.wantFail || rec.skipped == ci.wantFail {
				t.Errorf("%s, CI=%q: failed=%v skipped=%v, want failed=%v", c.name, ci.val, rec.failed, rec.skipped, ci.wantFail)
			}
		}
	}
}
