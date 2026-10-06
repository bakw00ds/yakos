package dispatch

import (
	"context"
	"os"
	"testing"

	rt "github.com/bakw00ds/yakos/internal/runtime"
)

// testAliasTable is the alias table these tests run against, so none of them
// depends on the model ids in lib/settings/model-aliases.json (vendors rename
// models). claude maps as in production; codex has no mapping (an empty entry
// means "use the harness default"); agy has recognisable fake ids.
func testAliasTable() map[string]map[string]string {
	t := map[string]map[string]string{}
	for alias, tier := range map[string]string{"cheap": "haiku", "balanced": "sonnet", "best": "opus", "reasoning": "opus", "frontier": "fable"} {
		t[alias] = map[string]string{"claude": tier, "codex": "", "agy": "agy-" + alias + "-x"}
	}
	return t
}

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
	runtimeProbe = func(context.Context, string) probeResult { return probeResult{OK: true} }
	stateDefaultRuntime = func() (string, string) { return "", "" }
	probeTTL = 0 // no answer is reused between tests
	rt.SetAliasTableForTest(testAliasTable())
	os.Exit(m.Run())
}

// withProbe installs a probe for one test.
func withProbe(t *testing.T, fn func(name string) probeResult) {
	t.Helper()
	withProbeCtx(t, func(_ context.Context, name string) probeResult { return fn(name) })
}

// withProbeCtx installs a probe that sees the dispatch's context.
func withProbeCtx(t *testing.T, fn func(ctx context.Context, name string) probeResult) {
	t.Helper()
	orig := runtimeProbe
	runtimeProbe = fn
	t.Cleanup(func() { runtimeProbe = orig })
}

// withStateDefault sets the state-dir default runtime for one test.
func withStateDefault(t *testing.T, name string) {
	t.Helper()
	withStateDefaultWarn(t, name, "")
}

// withStateDefaultWarn is withStateDefault with a warning the reader reports for
// a file it refused.
func withStateDefaultWarn(t *testing.T, name, warning string) {
	t.Helper()
	orig := stateDefaultRuntime
	stateDefaultRuntime = func() (string, string) { return name, warning }
	t.Cleanup(func() { stateDefaultRuntime = orig })
}
