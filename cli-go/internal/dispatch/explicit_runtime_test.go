package dispatch

// explicit_runtime_test.go: a runtime the operator NAMES does not fall back to
// another vendor behind their back (K-132 decision 14:45Z). The tests drive the
// real routing step (routeDispatch, so the real chain builder and chooser) and
// the real Run, RunStream and Service.Run, with only the machine probe
// replaced.
//
// This is a DELIBERATE DIVERGENCE from cli/lib/dispatch.sh, which walks the
// fallback lists for an explicit --runtime too. K-143 (parity matrix) must
// encode it as intended, not as a bug to port back.

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// down makes the named runtimes unavailable and everything else fine.
func down(t *testing.T, reasons map[string]string) {
	t.Helper()
	withProbe(t, func(name string) probeResult {
		if r, ok := reasons[name]; ok {
			return probeResult{Reason: r}
		}
		return probeResult{OK: true}
	})
}

const (
	notInstalled = "CLI not found on PATH; install: npm install -g @openai/codex"
	signedOut    = "not signed in; run: yakos auth login codex"
)

func asExplicit(t *testing.T, err error) *ExplicitRuntimeError {
	t.Helper()
	ee, ok := AsExplicitRuntimeError(err)
	if !ok {
		t.Fatalf("err = %v (%T), want an *ExplicitRuntimeError", err, err)
	}
	return ee
}

func TestRoute_ExplicitRuntimeFailsFast(t *testing.T) {
	root := routingRoot(t)
	cases := []struct {
		name        string
		yml         string
		agent       string
		mut         func(*routeInput)
		wantRuntime string
		wantNotUsed []string
	}{
		{"override, project default-fallback", "default-fallback: [claude]\n", "plain",
			func(in *routeInput) { in.RuntimeOverride = "codex" }, "codex", []string{"claude"}},
		// pinned-fb is pinned to agy with runtime-fallback [codex, claude]: its own
		// list (minus the runtime named) comes first, then the project's.
		{"override, agent runtime-fallback and project default-fallback", "default-fallback: [agy]\n", "pinned-fb",
			func(in *routeInput) { in.RuntimeOverride = "codex" }, "codex", []string{"claude", "agy"}},
		{"override, nothing to fall back to", "", "plain",
			func(in *routeInput) { in.RuntimeOverride = "codex" }, "codex", nil},
		{"a bare runtime name as the agent", "default-fallback: [claude]\n", "codex",
			nil, "codex", []string{"claude"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logbuf := captureRouteLog(t)
			down(t, map[string]string{"codex": signedOut})
			got, err := route(t, root, projectWithYML(t, tc.yml), tc.agent, tc.mut)
			if got != nil {
				t.Fatalf("routed to %s by %s; an explicit runtime that cannot run must not run anything", got.Runtime, got.RuntimeChosenBy)
			}
			ee := asExplicit(t, err)
			if ee.Runtime != tc.wantRuntime || ee.Reason != signedOut {
				t.Errorf("runtime/reason = %q/%q", ee.Runtime, ee.Reason)
			}
			if strings.Join(ee.NotUsed, ",") != strings.Join(tc.wantNotUsed, ",") {
				t.Errorf("NotUsed = %v, want %v", ee.NotUsed, tc.wantNotUsed)
			}
			msg := err.Error()
			for _, want := range []string{"codex", "requested explicitly", "not signed in", "yakos auth login codex"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q does not mention %q", msg, want)
				}
			}
			for _, nu := range tc.wantNotUsed {
				if !strings.Contains(msg, nu) {
					t.Errorf("error %q does not name the unused fallback %q", msg, nu)
				}
			}
			if len(tc.wantNotUsed) == 0 && strings.Contains(msg, "Not falling back") {
				t.Errorf("error %q talks about fallbacks when there are none", msg)
			}
			if strings.Contains(logbuf.String(), "falling back") {
				t.Errorf("it fell back: %q", logbuf.String())
			}
		})
	}
}

// The reason says what is wrong, not just that it is: not installed versus not
// signed in.
func TestRoute_ExplicitRuntimeErrorNamesTheReason(t *testing.T) {
	root := routingRoot(t)
	captureRouteLog(t)
	for _, reason := range []string{notInstalled, signedOut} {
		down(t, map[string]string{"agy": reason})
		_, err := route(t, root, projectWithYML(t, ""), "plain", func(in *routeInput) { in.RuntimeOverride = "agy" })
		ee := asExplicit(t, err)
		if ee.Reason != reason || !strings.Contains(err.Error(), reason) {
			t.Errorf("reason %q is not in %q", reason, err)
		}
	}
}

// What still falls back, as bash does: a frontmatter pin, a project default,
// and "auto" (which is no override at all).
func TestRoute_ImplicitChoicesStillFallBack(t *testing.T) {
	root := routingRoot(t)
	cases := []struct {
		name     string
		yml      string
		agent    string
		mut      func(*routeInput)
		wantRT   string
		wantFrom string
	}{
		{"frontmatter pin", "", "pinned-fb", nil, "claude", "agy"}, // agy, then codex (also down), then claude
		{"project default-runtime", "default-runtime: codex\ndefault-fallback: [claude]\n", "plain", nil, "claude", "codex"},
		{"per-domain", "per-domain:\n  misc: codex\ndefault-fallback: [claude]\n", "plain", nil, "claude", "codex"},
		{"auto", "default-runtime: codex\ndefault-fallback: [claude]\n", "plain", func(in *routeInput) { in.RuntimeOverride = "auto" }, "claude", "codex"},
		{"env default", "default-fallback: [claude]\n", "plain", func(in *routeInput) { in.RuntimeEnvDefault = "codex" }, "claude", "codex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureRouteLog(t)
			down(t, map[string]string{"agy": signedOut, "codex": signedOut})
			got, err := route(t, root, projectWithYML(t, tc.yml), tc.agent, tc.mut)
			if err != nil || got.Runtime != tc.wantRT || got.RuntimeChosenBy != RuntimeByFallback || got.FallbackFrom != tc.wantFrom {
				t.Errorf("got %+v %v, want %s by fallback from %s", got, err, tc.wantRT, tc.wantFrom)
			}
		})
	}
}

// --runtime-fallback is the operator's opt-in: the list they give is used, and
// only that list, for a runtime they named.
func TestRoute_ExplicitRuntimeOptIn(t *testing.T) {
	root := routingRoot(t)

	t.Run("falls back to the listed runtime and says so", func(t *testing.T) {
		logbuf := captureRouteLog(t)
		down(t, map[string]string{"codex": signedOut})
		got, err := route(t, root, projectWithYML(t, ""), "plain", func(in *routeInput) {
			in.RuntimeOverride, in.RuntimeFallbackOptIn = "codex", []string{"claude"}
		})
		if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByFallback || got.FallbackFrom != "codex" {
			t.Fatalf("got %+v %v, want claude by fallback from codex", got, err)
		}
		if !strings.Contains(logbuf.String(), "falling back to 'claude'") {
			t.Errorf("the fallback was not announced: %q", logbuf.String())
		}
	})

	t.Run("the project and agent lists are still not used", func(t *testing.T) {
		captureRouteLog(t)
		// codex and claude are down; agy is up and sits in the project's list,
		// but the operator listed only claude.
		down(t, map[string]string{"codex": signedOut, "claude": notInstalled})
		_, err := route(t, root, projectWithYML(t, "default-fallback: [agy]\n"), "plain", func(in *routeInput) {
			in.RuntimeOverride, in.RuntimeFallbackOptIn = "codex", []string{"claude"}
		})
		ee := asExplicit(t, err)
		if len(ee.AlsoTried) != 1 || ee.AlsoTried[0].Runtime != "claude" {
			t.Errorf("AlsoTried = %+v, want the listed claude only", ee.AlsoTried)
		}
		if !strings.Contains(err.Error(), "claude: "+notInstalled) {
			t.Errorf("error %q does not say why the listed fallback failed", err)
		}
		if strings.Join(ee.NotUsed, ",") != "agy" {
			t.Errorf("NotUsed = %v, want the project's agy", ee.NotUsed)
		}
	})

	t.Run("a pinned agent tries its own lists first, then the opt-in", func(t *testing.T) {
		captureRouteLog(t)
		// pinned-fb: agy, falling back to codex then claude. Only the extra
		// runtime named on the command line works... but all three are in the
		// registry, so exercise the order with a probe that records it.
		var order []string
		withProbe(t, func(name string) probeResult {
			order = append(order, name)
			return probeResult{Reason: "down"}
		})
		_, err := route(t, root, projectWithYML(t, ""), "pinned-fb", func(in *routeInput) { in.RuntimeFallbackOptIn = []string{"claude", "codex"} })
		if err == nil {
			t.Fatal("every runtime is down: want an error")
		}
		if got := strings.Join(order, ","); got != "agy,codex,claude" {
			t.Errorf("probe order = %s, want agy,codex,claude (the opt-in adds nothing already in the chain)", got)
		}
	})
}

// The typed error carries what the CLI needs for its hint.
func TestExplicitRuntimeError_FormatsOneLine(t *testing.T) {
	e := &ExplicitRuntimeError{Runtime: "codex", Reason: signedOut, NotUsed: []string{"claude", "agy"}}
	want := "dispatch: runtime codex was requested explicitly but cannot run: " + signedOut +
		". Not falling back to claude, agy: an explicit runtime does not use the agent's or the project's fallback list"
	if got := e.Error(); got != want {
		t.Errorf("Error() =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(e.Error(), "\n") {
		t.Error("the error spans lines")
	}
	wrapped := errors.Join(errors.New("outer"), e)
	if got, ok := AsExplicitRuntimeError(wrapped); !ok || got != e {
		t.Error("AsExplicitRuntimeError does not see through wrapping")
	}
	if _, ok := AsExplicitRuntimeError(errors.New("other")); ok {
		t.Error("an unrelated error was taken for an ExplicitRuntimeError")
	}
}

// The CLI's opt-in hint names only runtimes --runtime-fallback accepts. A fallback
// list can also name a bash-only runtime (claude-sdk) or a removed one (gemini).
func TestExplicitRuntimeError_RunnableFallbacks(t *testing.T) {
	e := &ExplicitRuntimeError{Runtime: "codex", Reason: signedOut, NotUsed: []string{"claude-sdk", "claude", "gemini", "agy", "antigravity-sdk"}}
	if got := strings.Join(e.RunnableFallbacks(), ","); got != "claude,agy" {
		t.Errorf("RunnableFallbacks = %q, want claude,agy", got)
	}
	// Everything it suggests is something the flag takes.
	if _, err := ParseRuntimeList(strings.Join(e.RunnableFallbacks(), ",")); err != nil {
		t.Errorf("the suggested list is rejected by --runtime-fallback: %v", err)
	}
	// The message still says what was configured and not used.
	if !strings.Contains(e.Error(), "claude-sdk, claude, gemini") {
		t.Errorf("the error should list every unused fallback: %s", e)
	}
	if none := (&ExplicitRuntimeError{NotUsed: []string{"claude-sdk"}}).RunnableFallbacks(); len(none) != 0 {
		t.Errorf("RunnableFallbacks = %v, want none", none)
	}
}

func TestParseRuntimeList(t *testing.T) {
	good := map[string][]string{
		"":                  nil,
		"claude":            {"claude"},
		"claude,codex":      {"claude", "codex"},
		" codex , agy ":     {"codex", "agy"},
		"claude,claude,agy": {"claude", "agy"},
		",claude,":          {"claude"},
	}
	for in, want := range good {
		got, err := ParseRuntimeList(in)
		if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("ParseRuntimeList(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"gemini", "nope", "claude,gemini", "claude-sdk", "--runtime"} {
		if _, err := ParseRuntimeList(bad); err == nil {
			t.Errorf("ParseRuntimeList(%q): want an error", bad)
		}
	}
}

// ---- through the real entry points ---------------------------------------------

// One-shot, MCP, JSON-RPC and REST all reach dispatch through Service.Run (and
// the CLI through Run): an explicit runtime that cannot run starts no process.
func TestServiceRun_ExplicitRuntimeStartsNothing(t *testing.T) {
	root := routingRoot(t)
	rec := fakeCLIs(t)
	isolatedLogDir(t)
	captureRouteLog(t)
	down(t, map[string]string{"codex": signedOut})
	svc := NewService(ServiceConfig{YakosRoot: root, WorkspaceRoot: projectWithYML(t, "default-fallback: [claude]\n")})

	_, _, err := svc.Run(context.Background(), Params{Agent: "plain", Task: "hi", Runtime: "codex"})
	asExplicit(t, err)
	if got := invoked(t, rec); len(got) != 0 {
		t.Errorf("executed %v; nothing may run when an explicit runtime is unavailable", got)
	}
}

func TestRun_ExplicitRuntimeOptInExecutesTheFallback(t *testing.T) {
	root := routingRoot(t)
	rec := fakeCLIs(t)
	logDir := isolatedLogDir(t)
	captureRouteLog(t)
	down(t, map[string]string{"codex": signedOut})

	_, res, err := Run(context.Background(), Request{
		AgentName: "plain", Task: "hi", Project: t.TempDir(), YakosRoot: root,
		Runtime: "codex", RuntimeFallbackOptIn: []string{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := invoked(t, rec); strings.Join(got, ",") != "claude" {
		t.Errorf("executed %v, want only claude", got)
	}
	if res.RuntimeChosenBy != RuntimeByFallback || res.FallbackFrom != "codex" {
		t.Errorf("result routing = %q/%q", res.RuntimeChosenBy, res.FallbackFrom)
	}
	events := readDispatchLog(t, logDir)
	fin := events[len(events)-1]
	assertField(t, fin, "runtime_chosen_by", "fallback")
	assertField(t, fin, "fallback_from", "codex")
}

// The console's streaming path: a pane set to a specific runtime fails with the
// error instead of answering from another vendor.
func TestRunStream_ExplicitRuntimeFailsFast(t *testing.T) {
	logDir := isolatedLogDir(t)
	root := routingRoot(t)
	captureRouteLog(t)
	down(t, map[string]string{"codex": signedOut})
	svc := NewService(ServiceConfig{YakosRoot: root, WorkspaceRoot: logDir, OperatorID: "test-op"})

	ran := false
	withStreamRunFn(func(context.Context, Request, runtime.Adapter, runtime.ChatDispatchRequest, func(StreamChunk)) (Result, error) {
		ran = true
		return Result{}, nil
	}, func() {
		_, err := svc.RunStream(context.Background(), Params{Agent: "plain", Task: "hi", Project: projectWithYML(t, "default-fallback: [claude]\n"), Runtime: "codex"}, func(StreamChunk) {})
		asExplicit(t, err)
	})
	if ran {
		t.Error("the stream ran although the pane's explicit runtime cannot run")
	}
}

// An explicit runtime that works is untouched by all of this.
func TestRoute_ExplicitRuntimeThatWorksIsChosen(t *testing.T) {
	root := routingRoot(t)
	captureRouteLog(t)
	got, err := route(t, root, projectWithYML(t, "default-fallback: [claude]\n"), "plain", func(in *routeInput) { in.RuntimeOverride = "codex" })
	if err != nil || got.Runtime != "codex" || got.RuntimeChosenBy != RuntimeByOverride || got.FallbackFrom != "" {
		t.Errorf("got %+v %v, want codex by override", got, err)
	}
}

// ---- the probe is cancellable and its answers are reused for a while ---------

func TestRoute_CancelEndsAProbeInFlight(t *testing.T) {
	root := routingRoot(t)
	captureRouteLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	withProbeCtx(t, func(ctx context.Context, name string) probeResult {
		close(started)
		<-ctx.Done() // a keyring read waiting on an unlock prompt
		return probeResult{Reason: "cancelled"}
	})
	go func() { <-started; cancel() }()
	done := make(chan error, 1)
	go func() {
		_, err := routeDispatch(ctx, routeInput{YakosRoot: root, Project: projectWithYML(t, ""), Agent: "plain", RuntimeOverride: "codex"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled (not a runtime reported unavailable)", err)
		}
		if _, ok := AsExplicitRuntimeError(err); ok {
			t.Error("a cancelled probe was reported as an unavailable runtime")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("routing did not end when the context was cancelled")
	}
}

func TestDefaultRuntimeProbe_ReusesAnswersForTheTTL(t *testing.T) {
	origOnce, origTTL, origNeg, origClock := probeOnce, probeTTL, probeNegativeTTL, probeClock
	t.Cleanup(func() {
		probeOnce, probeTTL, probeNegativeTTL, probeClock = origOnce, origTTL, origNeg, origClock
		resetProbeCache()
	})
	resetProbeCache()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	probeClock = func() time.Time { return now }
	probeTTL = 30 * time.Second
	calls := 0
	probeOnce = func(ctx context.Context, name string) probeResult {
		calls++
		return probeResult{OK: true} // a runtime that works
	}

	for i := 0; i < 5; i++ {
		_ = defaultRuntimeProbe(context.Background(), "agy")
	}
	if calls != 1 {
		t.Errorf("probed %d times in 5 dispatches inside the window, want 1", calls)
	}
	_ = defaultRuntimeProbe(context.Background(), "codex")
	if calls != 2 {
		t.Errorf("a different runtime shares the entry: %d calls", calls)
	}
	now = now.Add(29 * time.Second)
	_ = defaultRuntimeProbe(context.Background(), "agy")
	if calls != 2 {
		t.Errorf("answer expired early: %d calls", calls)
	}
	now = now.Add(2 * time.Second) // 31s after the first answer
	_ = defaultRuntimeProbe(context.Background(), "agy")
	if calls != 3 {
		t.Errorf("answer outlived the window: %d calls", calls)
	}
}

// A runtime that could not run is remembered only briefly, so a retry right after
// the operator signs in or installs the CLI is not told the old answer for long.
func TestDefaultRuntimeProbe_RemembersAnUnavailableRuntimeBriefly(t *testing.T) {
	origOnce, origTTL, origNeg, origClock := probeOnce, probeTTL, probeNegativeTTL, probeClock
	t.Cleanup(func() {
		probeOnce, probeTTL, probeNegativeTTL, probeClock = origOnce, origTTL, origNeg, origClock
		resetProbeCache()
	})
	resetProbeCache()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	probeClock = func() time.Time { return now }
	probeTTL = 30 * time.Second
	probeNegativeTTL = 5 * time.Second
	signedIn := false
	calls := 0
	probeOnce = func(ctx context.Context, name string) probeResult {
		calls++
		if signedIn {
			return probeResult{OK: true}
		}
		return probeResult{Reason: "not signed in"}
	}

	// Inside the short window the "no" is reused: a stuck keyring is not asked
	// again on every dispatch.
	for i := 0; i < 4; i++ {
		if p := defaultRuntimeProbe(context.Background(), "codex"); p.OK {
			t.Fatal("a signed-out codex probed OK")
		}
	}
	if calls != 1 {
		t.Errorf("probed %d times inside the window, want 1", calls)
	}

	// The operator runs `codex login`. Within seconds the daemon notices.
	signedIn = true
	now = now.Add(4 * time.Second)
	if p := defaultRuntimeProbe(context.Background(), "codex"); p.OK {
		t.Errorf("answer for a runtime that could not run expired early")
	}
	now = now.Add(2 * time.Second) // 6 seconds after the "no"
	if p := defaultRuntimeProbe(context.Background(), "codex"); !p.OK {
		t.Errorf("a retry 6 seconds after codex login still says not signed in: %+v", p)
	}
	// And the new answer is a "yes", which is remembered for the long window.
	callsAfter := calls
	now = now.Add(20 * time.Second)
	_ = defaultRuntimeProbe(context.Background(), "codex")
	if calls != callsAfter {
		t.Errorf("a working runtime was probed again inside the long window")
	}
}

// An answer is for the environment it was given in: a different PATH or a
// credential that appeared is never answered from the cache.
func TestDefaultRuntimeProbe_DoesNotReuseAnAnswerAcrossEnvironments(t *testing.T) {
	origOnce, origTTL := probeOnce, probeTTL
	t.Cleanup(func() { probeOnce, probeTTL = origOnce, origTTL; resetProbeCache() })
	resetProbeCache()
	probeTTL = time.Minute
	calls := 0
	probeOnce = func(context.Context, string) probeResult {
		calls++
		return probeResult{Reason: "not signed in"}
	}
	t.Setenv("OPENAI_API_KEY", "")
	_ = defaultRuntimeProbe(context.Background(), "codex")
	_ = defaultRuntimeProbe(context.Background(), "codex")
	if calls != 1 {
		t.Fatalf("an unchanged environment probed %d times, want 1", calls)
	}
	t.Setenv("OPENAI_API_KEY", "sk-new") // the operator signs in
	_ = defaultRuntimeProbe(context.Background(), "codex")
	if calls != 2 {
		t.Errorf("a changed environment was answered from the cache: %d calls", calls)
	}
}

func TestDefaultRuntimeProbe_DoesNotCacheACancelledAnswer(t *testing.T) {
	origOnce, origTTL := probeOnce, probeTTL
	t.Cleanup(func() { probeOnce, probeTTL = origOnce, origTTL; resetProbeCache() })
	resetProbeCache()
	probeTTL = time.Minute
	calls := 0
	probeOnce = func(ctx context.Context, name string) probeResult {
		calls++
		return probeResult{Reason: "cut short"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = defaultRuntimeProbe(ctx, "agy")
	_ = defaultRuntimeProbe(context.Background(), "agy")
	if calls != 2 {
		t.Errorf("a cancelled probe's answer was reused: %d calls", calls)
	}
}

func TestDefaultRuntimeProbe_TTLZeroNeverCaches(t *testing.T) {
	origOnce := probeOnce
	t.Cleanup(func() { probeOnce = origOnce; resetProbeCache() })
	resetProbeCache()
	if probeTTL != 0 {
		t.Fatalf("TestMain should leave probeTTL at 0, got %v", probeTTL)
	}
	calls := 0
	probeOnce = func(context.Context, string) probeResult { calls++; return probeResult{OK: true} }
	_ = defaultRuntimeProbe(context.Background(), "claude")
	_ = defaultRuntimeProbe(context.Background(), "claude")
	if calls != 2 {
		t.Errorf("calls = %d, want 2 with the cache off", calls)
	}
}

// A state default the reader refused is reported once, by the resolution that
// precedes real work, and is not used.
func TestRoute_UntrustedStateDefaultIsReportedAndIgnored(t *testing.T) {
	root := routingRoot(t)
	logbuf := captureRouteLog(t)
	withStateDefaultWarn(t, "", "ignoring the default runtime: the default-runtime file in the yakOS state directory is a symlink")
	got, err := route(t, root, projectWithYML(t, ""), "plain", nil)
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByDefault {
		t.Fatalf("got %+v %v, want claude by default", got, err)
	}
	if !strings.Contains(logbuf.String(), "is a symlink") {
		t.Errorf("the refused file was not reported: %q", logbuf.String())
	}
	// A handler that only asks what would run does not repeat the warning.
	logbuf.Reset()
	if _, err := PreferredRuntime(RouteQuery{YakosRoot: root, Project: projectWithYML(t, ""), Agent: "plain"}); err != nil {
		t.Fatal(err)
	}
	if logbuf.Len() != 0 {
		t.Errorf("PreferredRuntime printed %q", logbuf.String())
	}
}

// sec-324 F4 (adapted from its scratch probe zz_sec324_shadow_test.go): a cloned
// project ships .claude/agents/claude.md pinned to codex. The console's default
// pane is agent "claude" with runtime auto; before, the frontmatter pin outranked
// the agent-name rule and the pane went to codex while still reading "claude".
// The runtime-named file is now skipped, so the pane stays on claude.
func TestRoute_ProjectAgentNamedAfterARuntimeCannotHijackThePane(t *testing.T) {
	orig := agentscompose.WarnWriter
	agentscompose.WarnWriter = io.Discard
	t.Cleanup(func() { agentscompose.WarnWriter = orig })

	root := routingRoot(t)
	project := t.TempDir()
	agents := filepath.Join(project, ".claude", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	shadow := "---\nid: claude\nruntime: codex\n---\n\nYou are Claude.\n"
	if err := os.WriteFile(filepath.Join(agents, "claude.md"), []byte(shadow), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, override := range []string{"", "auto"} {
		got, err := PreferredRuntime(RouteQuery{YakosRoot: root, Project: project, Agent: "claude", Override: override})
		if err != nil || got.Runtime != "claude" || got.ChosenBy != RuntimeByAgentName {
			t.Errorf("override %q: preferred = %+v %v, want claude by agent-name", override, got, err)
		}
		routed, err := route(t, root, project, "claude", func(in *routeInput) { in.RuntimeOverride = override })
		if err != nil || routed.Runtime != "claude" || routed.RuntimeChosenBy != RuntimeByAgentName {
			t.Errorf("override %q: routed = %+v %v, want claude by agent-name", override, routed, err)
		}
	}
}

func TestPrefixedMessage(t *testing.T) {
	cases := map[string]string{
		"dispatch: no runtime available":   "dispatch: no runtime available",
		"dispatch:no space":                "dispatch:no space",
		"exec: \"claude\": not found":      "dispatch: exec: \"claude\": not found",
		"plain failure":                    "dispatch: plain failure",
		"the dispatch: word later on only": "dispatch: the dispatch: word later on only",
	}
	for in, want := range cases {
		if got := PrefixedMessage(errors.New(in)); got != want {
			t.Errorf("PrefixedMessage(%q) = %q, want %q", in, got, want)
		}
	}
	// An error this package raises is not prefixed twice.
	_, err := route(t, routingRoot(t), projectWithYML(t, ""), "no-such-agent", nil)
	if err == nil || strings.Contains(PrefixedMessage(err), "dispatch: dispatch:") {
		t.Errorf("a dispatch error was prefixed twice: %v", err)
	}
}
