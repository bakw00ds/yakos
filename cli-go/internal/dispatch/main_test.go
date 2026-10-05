package dispatch

import (
	"os"
	"testing"
)

// TestMain makes runtime resolution hermetic for the whole test binary.
//
// Resolution (resolve.go) asks the machine two questions: is the runtime's CLI
// installed and signed in, and did the operator set a default runtime in the
// state directory. The answers differ between a developer laptop (claude, codex
// and agy installed, a default-runtime file) and CI (none of it), and most of
// this package's tests are about something else. So by default every runtime
// counts as available and no state default is set. Tests that exercise the
// probe or the state default install their own via withProbe / withStateDefault.
func TestMain(m *testing.M) {
	runtimeProbe = func(string) probeResult { return probeResult{OK: true} }
	stateDefaultRuntime = func() string { return "" }
	os.Exit(m.Run())
}

// withProbe installs a probe for one test.
func withProbe(t *testing.T, fn func(name string) probeResult) {
	t.Helper()
	orig := runtimeProbe
	runtimeProbe = fn
	t.Cleanup(func() { runtimeProbe = orig })
}

// withStateDefault sets the state-dir default runtime for one test.
func withStateDefault(t *testing.T, name string) {
	t.Helper()
	orig := stateDefaultRuntime
	stateDefaultRuntime = func() string { return name }
	t.Cleanup(func() { stateDefaultRuntime = orig })
}
