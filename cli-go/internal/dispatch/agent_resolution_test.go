package dispatch

// Tests for generic-runtime agent resolution (the fix for "agent 'claude' not
// found in composed set" when the chat pane defaults to agent="claude").
//
// These tests exercise the resolution path through Service.Run using the
// package-internal setRunFn helper to avoid spawning real runtime processes.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
	rt "github.com/bakw00ds/yakos/internal/runtime"
)

// buildMinimalYakosRoot creates a minimal yakOS root with a single specialist
// agent (backend) in lib/agents/. Used to verify specialist resolution is
// unaffected by the generic-agent fallback.
func buildMinimalYakosRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	agentsDir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", agentsDir, err)
	}
	content := "---\nid: backend\nmodel: sonnet\n---\n\n## Purpose\n\nBackend specialist.\n"
	if err := os.WriteFile(filepath.Join(agentsDir, "backend.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write backend.md: %v", err)
	}
	return root
}

// newResolutionSvc builds a Service with a minimal yakOS root and an injected
// workspace.  Tests replace runFn via setRunFn before calling svc.Run.
func newResolutionSvc(t *testing.T, yakosRoot string) *Service {
	t.Helper()
	return NewService(ServiceConfig{
		YakosRoot:     yakosRoot,
		WorkspaceRoot: t.TempDir(),
	})
}

// TestGenericAgent_Claude verifies that agent="claude" (the console chat
// default) resolves to the generic claude agent rather than failing with
// "not found in composed set". The key assertion is that the call succeeds;
// runtime resolution to "claude" happens inside the real Run() which the fake
// runFn bypasses — that path is covered by runtime_test.go.
func TestGenericAgent_Claude(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	var capturedReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		capturedReq = r
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "claude",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=claude: unexpected error: %v", err)
	}
	// The request must carry the correct agent name through to runFn.
	if capturedReq.AgentName != "claude" {
		t.Errorf("captured AgentName = %q, want %q", capturedReq.AgentName, "claude")
	}
}

// TestGenericAgent_Codex verifies that agent="codex" resolves via the generic
// fallback without error. When a Params.Runtime override is given, the
// resolved runtime is passed through to runFn; that is also verified here.
func TestGenericAgent_Codex(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	var capturedReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		capturedReq = r
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "codex",
		Task:    "hello",
		Project: t.TempDir(),
		// Explicit runtime override so Service.Run passes it through to runFn.
		Runtime: "codex",
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=codex: unexpected error: %v", err)
	}
	// When Params.Runtime is set, it flows through Service.Run into Request.Runtime.
	if capturedReq.Runtime != "codex" {
		t.Errorf("Runtime = %q, want %q", capturedReq.Runtime, "codex")
	}
}

// TestGenericAgent_Gemini pins the retirement of gemini (K-132): a bare "gemini"
// agent name is no longer a runtime catch-all.
func TestGenericAgent_Gemini(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		t.Error("runFn must not be reached for an unknown agent")
		return nil, Result{}, nil
	})

	// Service.Run delegates agent lookup to Run, which the fake bypasses, so
	// assert on the resolver the real Run uses.
	if _, err := resolveAgent(nil, "gemini", yakosRoot, ""); err == nil || !strings.Contains(err.Error(), "not found in composed set") {
		t.Fatalf("resolveAgent(gemini) = %v, want a not-found error", err)
	}
	_ = svc
}

// TestGenericAgent_Agy verifies that "agy" (the third known runtime) also
// resolves generically.
func TestGenericAgent_Agy(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "agy",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=agy: unexpected error: %v", err)
	}
}

// TestUnknownAgent_StillErrors verifies that a genuinely unknown agent name
// (not a runtime, not in the roster) still returns the "not found" error.
func TestUnknownAgent_StillErrors(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	// No need to override runFn: the error should surface before Run() is called.
	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "nope",
		Task:    "hello",
		Project: t.TempDir(),
	})
	if err == nil {
		t.Fatal("Service.Run with unknown agent: expected error, got nil")
	}
	const wantSub = "not found in composed set"
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("unexpected error message (want %q in): %v", wantSub, err)
	}
}

// TestSpecialistAgent_StillResolves verifies that a real specialist agent
// (backend, present in the roster) still resolves correctly and the generic
// fallback does not interfere.
func TestSpecialistAgent_StillResolves(t *testing.T) {
	yakosRoot := buildMinimalYakosRoot(t)
	svc := newResolutionSvc(t, yakosRoot)

	var capturedReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		capturedReq = r
		return []byte("ok"), Result{ExitCode: 0, ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, nil
	})

	_, _, err := svc.Run(context.Background(), Params{
		Agent:   "backend",
		Task:    "implement GET /v1/users",
		Project: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Service.Run with agent=backend: unexpected error: %v", err)
	}
	if capturedReq.AgentName != "backend" {
		t.Errorf("AgentName = %q, want %q", capturedReq.AgentName, "backend")
	}
}

// ============================================================================
// K-132 P0a: runtime pins, project config and the fallback chain.
//
// Before this change the Go dispatcher ignored an agent's `runtime:` pin
// (K-127): general-codex and general-agy ran on claude. These tests drive the
// real routing step (routeDispatch) and the real Run, with fake CLIs on PATH,
// so a regression to "everything runs on claude" fails them.
// ============================================================================

// routingRoot builds a yakOS root whose roster covers every routing case.
func routingRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	agents := map[string]string{
		// The two framework agents that carry pins (lib/agents/general-*.md).
		"general-codex": "domain: cross-cutting\nruntime: codex\nmodel: gpt-5\n",
		"general-agy":   "domain: cross-cutting\nruntime: agy\nmodel: gemini-3.5\n",
		// A pin with a fallback chain.
		"pinned-fb": "domain: platform\nruntime: agy\nruntime-fallback: [codex, claude]\n",
		// No pin: the project file and defaults decide.
		"reviewer": "domain: code-review\nmodel: sonnet\n",
		"plain":    "domain: misc\nmodel: sonnet\n",
		"bare":     "domain: misc\n",
		// Models that only mean something on one runtime.
		"alias-codex": "runtime: codex\nmodel: balanced\n",
		"tier-codex":  "runtime: codex\nmodel: opus\n",
		"gpt-claude":  "model: gpt-5\n",
		// Pinned to codex, but with a Claude-tier model of its own for when it
		// falls back to claude.
		"pinned-claude-fb": "runtime: codex\nmodel: haiku\n",
		// A pin this dispatcher cannot run (bash-only runtime).
		"sdk-pinned":  "runtime: claude-sdk\n",
		"sdk-with-fb": "runtime: claude-sdk\nruntime-fallback: [claude]\n",
	}
	for id, fm := range agents {
		body := "---\nid: " + id + "\n" + fm + "---\n\n## Purpose\n\nRouting test agent " + id + ".\n"
		if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// projectWithYML returns a project dir whose .yakos.yml is yml ("" = none).
func projectWithYML(t *testing.T, yml string) string {
	t.Helper()
	dir := t.TempDir()
	if yml != "" {
		if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// captureRouteLog redirects the resolver's notices into a buffer.
func captureRouteLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := routeLog
	routeLog = &buf
	t.Cleanup(func() { routeLog = orig })
	return &buf
}

func route(t *testing.T, root, project, agent string, mut func(*routeInput)) (*routed, error) {
	t.Helper()
	in := routeInput{YakosRoot: root, Project: project, Agent: agent}
	if mut != nil {
		mut(&in)
	}
	return routeDispatch(in)
}

// K-127: an agent's `runtime:` pin selects the adapter, and its non-Claude model
// survives.
func TestRoute_AgentPinSelectsRuntimeAndModel(t *testing.T) {
	root := routingRoot(t)
	project := projectWithYML(t, "")
	cases := []struct {
		agent       string
		wantRT      string
		wantBy      string
		wantModel   string
		wantExpl    bool
		wantModelBy string
	}{
		{"general-codex", "codex", RuntimeByFrontmatter, "gpt-5", true, "frontmatter"},
		{"general-agy", "agy", RuntimeByFrontmatter, "gemini-3.5", true, "frontmatter"},
		// No pin: claude by default, the agent's own tier.
		{"plain", "claude", RuntimeByDefault, "sonnet", true, "frontmatter"},
		// No pin, no model: claude's default, not passed to the CLI.
		{"bare", "claude", RuntimeByDefault, "sonnet", false, "frontmatter"},
		// A non-Claude model on an agent that resolves to claude is ignored, as before.
		{"gpt-claude", "claude", RuntimeByDefault, "sonnet", false, "frontmatter"},
		// An alias resolves against the runtime that runs it (D4).
		{"alias-codex", "codex", RuntimeByFrontmatter, "gpt-5-mini", true, "frontmatter"},
		// A Claude tier means nothing to codex: the codex default applies instead.
		{"tier-codex", "codex", RuntimeByFrontmatter, "gpt-5-mini", false, "frontmatter"},
	}
	for _, c := range cases {
		got, err := route(t, root, project, c.agent, nil)
		if err != nil {
			t.Errorf("%s: %v", c.agent, err)
			continue
		}
		if got.Runtime != c.wantRT || got.RuntimeChosenBy != c.wantBy {
			t.Errorf("%s: runtime %q by %q, want %q by %q", c.agent, got.Runtime, got.RuntimeChosenBy, c.wantRT, c.wantBy)
		}
		if got.Model != c.wantModel || got.ModelExplicit != c.wantExpl || got.ModelChosenBy != c.wantModelBy {
			t.Errorf("%s: model %q explicit=%v by %q, want %q explicit=%v by %q",
				c.agent, got.Model, got.ModelExplicit, got.ModelChosenBy, c.wantModel, c.wantExpl, c.wantModelBy)
		}
		if got.Adapter == nil || got.Adapter.Name() != c.wantRT {
			t.Errorf("%s: adapter = %v, want one named %q", c.agent, got.Adapter, c.wantRT)
		}
	}
}

// The composed agent keeps the raw model so the router can resolve it per runtime.
func TestRoute_ComposedAgentCarriesModelRaw(t *testing.T) {
	root := routingRoot(t)
	got, err := route(t, root, projectWithYML(t, ""), "general-codex", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.ModelRaw != "gpt-5" || got.Agent.Model != "" || got.Agent.Runtime != "codex" {
		t.Errorf("agent = %+v, want Runtime codex, ModelRaw gpt-5, Model blank", got.Agent)
	}
}

// An explicit runtime outranks the pin; the pin's foreign model then drops.
func TestRoute_ExplicitRuntimeBeatsPin(t *testing.T) {
	root := routingRoot(t)
	got, err := route(t, root, projectWithYML(t, ""), "general-codex", func(in *routeInput) { in.RuntimeOverride = "claude" })
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByOverride {
		t.Errorf("runtime %q by %q, want claude by override", got.Runtime, got.RuntimeChosenBy)
	}
	if got.Model != "sonnet" || got.ModelExplicit {
		t.Errorf("gpt-5 means nothing to claude; got model %q explicit=%v, want the default sonnet", got.Model, got.ModelExplicit)
	}
}

// "auto" is the console's spelling of "no override".
func TestRoute_AutoOverrideMeansNone(t *testing.T) {
	root := routingRoot(t)
	for _, v := range []string{"", "auto", " auto "} {
		got, err := route(t, root, projectWithYML(t, ""), "general-codex", func(in *routeInput) { in.RuntimeOverride = v })
		if err != nil {
			t.Fatalf("override %q: %v", v, err)
		}
		if got.Runtime != "codex" || got.RuntimeChosenBy != RuntimeByFrontmatter {
			t.Errorf("override %q: runtime %q by %q, want codex by frontmatter", v, got.Runtime, got.RuntimeChosenBy)
		}
	}
}

// A bare runtime name (the console default agent "claude") still runs on that
// runtime, but an agent that has a pin of its own is not hijacked by its name.
func TestRoute_BareRuntimeNameStillSelectsItself(t *testing.T) {
	root := routingRoot(t)
	project := projectWithYML(t, "default-runtime: agy\n")
	for _, name := range []string{"claude", "codex", "agy"} {
		got, err := route(t, root, project, name, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Runtime != name || got.RuntimeChosenBy != RuntimeByAgentName {
			t.Errorf("agent %q: runtime %q by %q, want itself by agent-name", name, got.Runtime, got.RuntimeChosenBy)
		}
	}
}

// .yakos.yml: per-domain, then default-runtime; the agent's own pin outranks both.
func TestRoute_ProjectConfigPerDomainAndDefaultRuntime(t *testing.T) {
	root := routingRoot(t)
	project := projectWithYML(t, "default-runtime: agy\nper-domain:\n  code-review: codex\n  cross-cutting: claude\n")

	cases := []struct{ agent, wantRT, wantBy string }{
		{"reviewer", "codex", RuntimeByPerDomain},        // domain rule
		{"plain", "agy", RuntimeByProjectDefault},        // no rule for "misc": project default
		{"general-codex", "codex", RuntimeByFrontmatter}, // pin beats the cross-cutting -> claude rule
		{"general-agy", "agy", RuntimeByFrontmatter},
	}
	for _, c := range cases {
		got, err := route(t, root, project, c.agent, nil)
		if err != nil {
			t.Errorf("%s: %v", c.agent, err)
			continue
		}
		if got.Runtime != c.wantRT || got.RuntimeChosenBy != c.wantBy {
			t.Errorf("%s: runtime %q by %q, want %q by %q", c.agent, got.Runtime, got.RuntimeChosenBy, c.wantRT, c.wantBy)
		}
	}
}

// The state default and YAKOS_RUNTIME sit below the project file, in bash order.
func TestRoute_DefaultsRankAsInBash(t *testing.T) {
	root := routingRoot(t)

	// state file only
	withStateDefault(t, "agy")
	got, err := route(t, root, projectWithYML(t, ""), "plain", nil)
	if err != nil || got.Runtime != "agy" || got.RuntimeChosenBy != RuntimeByStateDefault {
		t.Fatalf("state default: %+v %v, want agy by state-default", got, err)
	}

	// the CLI-supplied YAKOS_RUNTIME beats the state file
	got, err = route(t, root, projectWithYML(t, ""), "plain", func(in *routeInput) { in.RuntimeEnvDefault = "codex" })
	if err != nil || got.Runtime != "codex" || got.RuntimeChosenBy != RuntimeByEnv {
		t.Fatalf("env default: %+v %v, want codex by env", got, err)
	}

	// the project default beats both
	got, err = route(t, root, projectWithYML(t, "default-runtime: claude\n"), "plain", func(in *routeInput) { in.RuntimeEnvDefault = "codex" })
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByProjectDefault {
		t.Fatalf("project default: %+v %v, want claude by project-default", got, err)
	}

	// and the agent's pin beats the environment (as in bash: YAKOS_RUNTIME is a default, not an override)
	got, err = route(t, root, projectWithYML(t, ""), "general-agy", func(in *routeInput) { in.RuntimeEnvDefault = "codex" })
	if err != nil || got.Runtime != "agy" || got.RuntimeChosenBy != RuntimeByFrontmatter {
		t.Fatalf("pin vs env: %+v %v, want agy by frontmatter", got, err)
	}
}

// A default that names a runtime this dispatcher cannot run must not make every
// agent undispatchable.
func TestRoute_UnusableAmbientDefaultsAreIgnored(t *testing.T) {
	root := routingRoot(t)
	logbuf := captureRouteLog(t)
	withStateDefault(t, "claude-sdk")
	got, err := route(t, root, projectWithYML(t, "default-runtime: antigravity-sdk\n"), "plain", func(in *routeInput) { in.RuntimeEnvDefault = "my-plugin" })
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByDefault {
		t.Fatalf("got %+v %v, want claude by default", got, err)
	}
	for _, want := range []string{"default-runtime", "YAKOS_RUNTIME", "state dir"} {
		if !strings.Contains(logbuf.String(), want) {
			t.Errorf("expected a notice mentioning %q, got %q", want, logbuf.String())
		}
	}
}

// The daemon never reads YAKOS_RUNTIME: only a caller that passes it explicitly
// (the CLI one-shot path) gets it.
func TestRoute_DaemonPathIgnoresYAKOSRuntime(t *testing.T) {
	root := routingRoot(t)
	t.Setenv("YAKOS_RUNTIME", "codex")

	got, err := route(t, root, projectWithYML(t, ""), "plain", nil)
	if err != nil || got.Runtime != "claude" {
		t.Fatalf("routeDispatch read the environment: %+v %v", got, err)
	}

	// Through the daemon's streaming entry point as well.
	svc := newResolutionSvc(t, root)
	var captured Request
	withStreamRunFn(func(_ context.Context, r Request, _ rt.Adapter, _ rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
		captured = r
		return Result{}, nil
	}, func() {
		if _, err := svc.RunStream(context.Background(), Params{Agent: "plain", Task: "t", Project: t.TempDir()}, func(StreamChunk) {}); err != nil {
			t.Fatal(err)
		}
	})
	if captured.Runtime != "claude" || captured.RuntimeChosenBy != RuntimeByDefault {
		t.Errorf("RunStream: runtime %q by %q with YAKOS_RUNTIME=codex in the daemon env, want claude by default", captured.Runtime, captured.RuntimeChosenBy)
	}

	// And Service.Run builds its Request without an env default.
	var runReq Request
	setRunFn(t, func(_ context.Context, r Request) ([]byte, Result, error) {
		runReq = r
		return nil, Result{}, nil
	})
	if _, _, err := svc.Run(context.Background(), Params{Agent: "plain", Task: "t", Project: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if runReq.RuntimeEnvDefault != "" {
		t.Errorf("Service.Run set RuntimeEnvDefault=%q; only the CLI path may", runReq.RuntimeEnvDefault)
	}
}

// D13: the preferred runtime is skipped when it is not signed in; the chain
// continues and the skip is recorded.
func TestRoute_FallbackWhenProbeFails(t *testing.T) {
	root := routingRoot(t)
	logbuf := captureRouteLog(t)
	withProbe(t, func(name string) probeResult {
		if name == "agy" {
			return probeResult{Reason: "not signed in; run: yakos auth login agy"}
		}
		return probeResult{OK: true}
	})

	got, err := route(t, root, projectWithYML(t, ""), "pinned-fb", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "codex" || got.RuntimeChosenBy != RuntimeByFallback || got.FallbackFrom != "agy" {
		t.Errorf("got runtime %q by %q from %q, want codex by fallback from agy", got.Runtime, got.RuntimeChosenBy, got.FallbackFrom)
	}
	// One stderr line says why.
	lines := strings.Split(strings.TrimSpace(logbuf.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "preferred runtime unavailable") ||
		!strings.Contains(lines[0], "agy: not signed in") || !strings.Contains(lines[0], "falling back to 'codex'") {
		t.Errorf("fallback notice = %q", logbuf.String())
	}

	// A second failure walks further down the agent's list.
	withProbe(t, func(name string) probeResult {
		if name == "claude" {
			return probeResult{OK: true}
		}
		return probeResult{Reason: "not signed in"}
	})
	got, err = route(t, root, projectWithYML(t, ""), "pinned-fb", nil)
	if err != nil || got.Runtime != "claude" || got.FallbackFrom != "agy" {
		t.Errorf("got %+v %v, want claude, falling back from agy", got, err)
	}
}

// The agent's list comes before the project's default-fallback.
func TestRoute_FallbackOrderAgentThenProject(t *testing.T) {
	root := routingRoot(t)
	captureRouteLog(t)
	project := projectWithYML(t, "default-fallback: [codex, claude]\n")
	var tried []string
	withProbe(t, func(name string) probeResult {
		tried = append(tried, name)
		return probeResult{Reason: "down"}
	})
	_, _ = route(t, root, project, "pinned-fb", nil) // agy -> codex, claude (agent) -> default-fallback adds nothing new
	if got := strings.Join(tried, ","); got != "agy,codex,claude" {
		t.Errorf("probe order = %s, want agy,codex,claude (agent fallbacks first, repeats dropped)", got)
	}

	tried = nil
	_, _ = route(t, root, project, "plain", nil) // claude (default) -> project fallback: codex
	if got := strings.Join(tried, ","); got != "claude,codex" {
		t.Errorf("probe order = %s, want claude,codex", got)
	}
}

// Nothing in the chain can run: fail fast, say why, start nothing.
func TestRoute_NoCandidateFailsFast(t *testing.T) {
	root := routingRoot(t)
	withProbe(t, func(name string) probeResult {
		return probeResult{Reason: "not signed in; run: yakos auth login " + name}
	})
	_, err := route(t, root, projectWithYML(t, ""), "general-agy", nil)
	if err == nil {
		t.Fatal("expected an error when the pinned runtime is not signed in and there is no fallback")
	}
	for _, want := range []string{"no runtime available", `"general-agy"`, "agy: not signed in", "yakos auth login agy", "runtime-fallback"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

// An agent never silently runs on another vendor when it has no fallback.
func TestRoute_PinnedRuntimeDoesNotQuietlyFallToClaude(t *testing.T) {
	root := routingRoot(t)
	withProbe(t, func(name string) probeResult {
		if name == "claude" {
			return probeResult{OK: true}
		}
		return probeResult{Reason: "not signed in"}
	})
	if got, err := route(t, root, projectWithYML(t, ""), "general-codex", nil); err == nil {
		t.Fatalf("general-codex ran on %q although codex is down and nothing allows a fallback", got.Runtime)
	}
}

func TestRoute_UnsupportedPinsAreSkipped(t *testing.T) {
	root := routingRoot(t)
	captureRouteLog(t)

	_, err := route(t, root, projectWithYML(t, ""), "sdk-pinned", nil)
	if err == nil || !strings.Contains(err.Error(), "not supported by the Go dispatcher") {
		t.Errorf("sdk-pinned: err = %v, want an unsupported-runtime error", err)
	}

	got, err := route(t, root, projectWithYML(t, ""), "sdk-with-fb", nil)
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByFallback || got.FallbackFrom != "claude-sdk" {
		t.Errorf("sdk-with-fb: %+v %v, want claude by fallback from claude-sdk", got, err)
	}
}

// An explicit runtime that names no adapter is an error, whatever the fallbacks.
func TestRoute_ExplicitUnknownRuntimeIsAnError(t *testing.T) {
	root := routingRoot(t)
	for _, name := range []string{"gemini", "nope", "claude-sdk"} {
		_, err := route(t, root, projectWithYML(t, "default-fallback: [claude]\n"), "plain", func(in *routeInput) { in.RuntimeOverride = name })
		if err == nil || !strings.Contains(err.Error(), "unknown runtime") {
			t.Errorf("override %q: err = %v, want unknown runtime", name, err)
		}
	}
}

// Matches cli/lib/dispatch.sh: an explicit runtime that is unavailable also
// walks the fallback lists, and says so.
func TestRoute_UnavailableOverrideWalksFallbacks(t *testing.T) {
	root := routingRoot(t)
	captureRouteLog(t)
	withProbe(t, func(name string) probeResult {
		if name == "codex" {
			return probeResult{Reason: "CLI not found on PATH"}
		}
		return probeResult{OK: true}
	})
	got, err := route(t, root, projectWithYML(t, "default-fallback: [claude]\n"), "plain", func(in *routeInput) { in.RuntimeOverride = "codex" })
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByFallback || got.FallbackFrom != "codex" {
		t.Errorf("got %+v %v, want claude by fallback from codex", got, err)
	}
}

// A broken .yakos.yml is reported once and never breaks dispatch.
func TestRoute_MalformedProjectConfigWarnsAndContinues(t *testing.T) {
	root := routingRoot(t)
	logbuf := captureRouteLog(t)
	got, err := route(t, root, projectWithYML(t, "default-runtime: [unclosed\n"), "plain", nil)
	if err != nil || got.Runtime != "claude" {
		t.Fatalf("got %+v %v, want claude", got, err)
	}
	if !strings.Contains(logbuf.String(), ".yakos.yml") || !strings.Contains(logbuf.String(), "cannot parse") {
		t.Errorf("expected a warning about .yakos.yml, got %q", logbuf.String())
	}
}

// ---- model resolution --------------------------------------------------------

func TestRoute_ExplicitModelIsValidatedPerRuntime(t *testing.T) {
	root := routingRoot(t)
	project := projectWithYML(t, "")

	// codex: an alias resolves against codex's column; an id passes through.
	for _, c := range []struct{ override, want string }{{"balanced", "gpt-5-mini"}, {"best", "gpt-5"}, {"gpt-5", "gpt-5"}, {"o4-mini", "o4-mini"}} {
		got, err := route(t, root, project, "general-codex", func(in *routeInput) { in.ModelOverride = c.override })
		if err != nil || got.Model != c.want || got.ModelChosenBy != "override" || !got.ModelExplicit {
			t.Errorf("codex %q: %+v %v, want %q by override", c.override, got, err, c.want)
		}
	}
	// agy
	got, err := route(t, root, project, "general-agy", func(in *routeInput) { in.ModelOverride = "frontier" })
	if err != nil || got.Model != "claude-fable-5" {
		t.Errorf("agy frontier: %+v %v", got, err)
	}
	// claude keeps the tier rule and its old error text.
	if _, err := route(t, root, project, "plain", func(in *routeInput) { in.ModelOverride = "gpt-5" }); err == nil ||
		!strings.Contains(err.Error(), `invalid model tier "gpt-5" (must be haiku|sonnet|opus|fable)`) {
		t.Errorf("claude gpt-5: err = %v", err)
	}
	if got, err := route(t, root, project, "plain", func(in *routeInput) { in.ModelOverride = "balanced" }); err != nil || got.Model != "sonnet" {
		t.Errorf("claude balanced: %+v %v", got, err)
	}
	// A bare Claude tier names no codex or agy model: use an alias instead.
	for _, tier := range []string{"haiku", "sonnet", "opus", "fable"} {
		for _, agent := range []string{"general-codex", "general-agy"} {
			_, err := route(t, root, project, agent, func(in *routeInput) { in.ModelOverride = tier })
			if err == nil || !strings.Contains(err.Error(), "invalid model") {
				t.Errorf("%s with Claude tier %q: err = %v, want an invalid-model error", agent, tier, err)
			}
		}
	}
	// Garbage never reaches a CLI.
	for _, bad := range []string{"Bad Model", "gpt-5; rm -rf /", "-gpt5", strings.Repeat("a", 65)} {
		_, err := route(t, root, project, "general-codex", func(in *routeInput) { in.ModelOverride = bad })
		if err == nil || !strings.Contains(err.Error(), "invalid model") || !strings.Contains(err.Error(), "runtime codex") {
			t.Errorf("codex %q: err = %v, want an invalid-model error naming the runtime", bad, err)
		}
	}
}

// The model is judged against the runtime that actually runs. When the runtime
// changed under an explicit model (a fallback), the model is dropped for that
// runtime's default instead of failing the request.
func TestRoute_ExplicitModelDroppedWhenRuntimeFellBack(t *testing.T) {
	root := routingRoot(t)
	logbuf := captureRouteLog(t)
	withProbe(t, func(name string) probeResult {
		if name == "codex" {
			return probeResult{Reason: "not signed in"}
		}
		return probeResult{OK: true}
	})
	got, err := route(t, root, projectWithYML(t, "default-fallback: [claude]\n"), "general-codex", func(in *routeInput) { in.ModelOverride = "gpt-5" })
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "claude" || got.Model != "sonnet" || got.ModelExplicit {
		t.Errorf("got runtime %q model %q explicit=%v, want claude with its default", got.Runtime, got.Model, got.ModelExplicit)
	}
	if !strings.Contains(logbuf.String(), `model "gpt-5" does not fit fallback runtime claude`) {
		t.Errorf("expected a notice, got %q", logbuf.String())
	}

	// After dropping the override, the agent's own pin for the fallback runtime
	// still applies before the default.
	got, err = route(t, root, projectWithYML(t, "default-fallback: [claude]\n"), "pinned-claude-fb", func(in *routeInput) { in.ModelOverride = "gpt-5" })
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != "claude" || got.Model != "haiku" || !got.ModelExplicit {
		t.Errorf("got runtime %q model %q explicit=%v, want claude with the agent's own haiku pin", got.Runtime, got.Model, got.ModelExplicit)
	}
}

func TestRoute_EvalRunIDLabelsModelChoice(t *testing.T) {
	root := routingRoot(t)
	got, err := route(t, root, projectWithYML(t, ""), "bare", func(in *routeInput) { in.EvalRunID = "e-1" })
	if err != nil || got.ModelChosenBy != "eval" {
		t.Errorf("got %+v %v, want model_chosen_by eval", got, err)
	}
	got, err = route(t, root, projectWithYML(t, ""), "bare", func(in *routeInput) { in.EvalRunID = "e-1"; in.ModelOverride = "haiku" })
	if err != nil || got.ModelChosenBy != "override" {
		t.Errorf("override beats eval: got %+v %v", got, err)
	}
}

// ---- the chain as a pure function ---------------------------------------------

func TestChooseRuntime_PrecedenceTable(t *testing.T) {
	pinned := &agentscompose.ComposedAgent{ID: "a", Runtime: "codex", Domain: "d"}
	unpinned := &agentscompose.ComposedAgent{ID: "a", Domain: "d"}
	cases := []struct {
		name   string
		in     chainInput
		wantRT string
		wantBy string
	}{
		{"override first", chainInput{override: "agy", agentName: "a", agent: pinned, stateDefault: "claude"}, "agy", RuntimeByOverride},
		{"pin", chainInput{agentName: "a", agent: pinned, envDefault: "agy", stateDefault: "claude"}, "codex", RuntimeByFrontmatter},
		{"env", chainInput{agentName: "a", agent: unpinned, envDefault: "agy", stateDefault: "codex"}, "agy", RuntimeByEnv},
		{"state", chainInput{agentName: "a", agent: unpinned, stateDefault: "codex"}, "codex", RuntimeByStateDefault},
		{"default", chainInput{agentName: "a", agent: unpinned}, "claude", RuntimeByDefault},
		{"nil agent", chainInput{agentName: "a"}, "claude", RuntimeByDefault},
	}
	for _, c := range cases {
		got, _, err := chooseRuntime(c.in, func(string) probeResult { return probeResult{OK: true} })
		if err != nil || got.Runtime != c.wantRT || got.ChosenBy != c.wantBy {
			t.Errorf("%s: %+v %v, want %q by %q", c.name, got, err, c.wantRT, c.wantBy)
		}
	}
}

// PreferredRuntime never consults the machine: a handler uses it to validate a
// request before queueing it.
func TestPreferredRuntime_DoesNotProbe(t *testing.T) {
	root := routingRoot(t)
	withProbe(t, func(name string) probeResult {
		t.Errorf("PreferredRuntime probed %q", name)
		return probeResult{}
	})
	got, err := PreferredRuntime(RouteQuery{YakosRoot: root, Project: projectWithYML(t, ""), Agent: "general-codex"})
	if err != nil || got.Runtime != "codex" || got.ChosenBy != RuntimeByFrontmatter {
		t.Errorf("got %+v %v, want codex by frontmatter", got, err)
	}
	// With no roster it still resolves (an unknown agent falls to the defaults).
	got, err = PreferredRuntime(RouteQuery{Agent: "x"})
	if err != nil || got.Runtime != "claude" {
		t.Errorf("no roster: %+v %v", got, err)
	}
	// An explicit unknown runtime is still an error.
	if _, err := PreferredRuntime(RouteQuery{Agent: "x", Override: "gemini"}); err == nil {
		t.Error("override gemini: expected an error")
	}
}

func TestResolveRuntime_AppliesProbeAndAuto(t *testing.T) {
	root := routingRoot(t)
	captureRouteLog(t)
	withProbe(t, func(name string) probeResult {
		if name == "agy" {
			return probeResult{Reason: "not signed in"}
		}
		return probeResult{OK: true}
	})
	got, err := ResolveRuntime(RouteQuery{YakosRoot: root, Project: projectWithYML(t, ""), Agent: "pinned-fb", Override: "auto"})
	if err != nil || got.Runtime != "codex" || got.FallbackFrom != "agy" || len(got.Skipped) != 1 || got.Skipped[0].Runtime != "agy" {
		t.Errorf("got %+v %v", got, err)
	}
}

func TestRosterRuntimes(t *testing.T) {
	root := routingRoot(t)
	roster, err := agentscompose.Compose(root, "")
	if err != nil {
		t.Fatal(err)
	}
	project := projectWithYML(t, "default-runtime: agy\nper-domain:\n  code-review: codex\n")
	got := RosterRuntimes(roster, project)
	want := map[string]string{
		"general-codex": "codex", "general-agy": "agy", "pinned-fb": "agy",
		"reviewer": "codex", "plain": "agy", "bare": "agy",
		"sdk-pinned": "claude-sdk", // reported as written
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("RosterRuntimes[%q] = %q, want %q", id, got[id], w)
		}
	}
}

// ---- end to end: the right binary is executed ----------------------------------

// fakeCLIs puts stub claude/codex/agy executables first on PATH. Each appends
// its own name to the returned file, then prints output valid for its adapter.
func fakeCLIs(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "invoked.txt")
	script := func(name, out string) string {
		return "#!/bin/sh\nprintf '%s\\n' " + name + " >> '" + rec + "'\nprintf '%s\\n' '" + out + "'\n"
	}
	stubs := map[string]string{
		"claude": script("claude", `{"type":"result","subtype":"success","result":"ok","session_id":"sess-claude-1","total_cost_usd":0.001,"usage":{"input_tokens":3,"output_tokens":2}}`),
		"codex":  script("codex", "codex answer"),
		"agy":    script("agy", "agy answer"),
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	return rec
}

func invoked(t *testing.T, rec string) []string {
	t.Helper()
	b, err := os.ReadFile(rec)
	if err != nil {
		return nil
	}
	return strings.Fields(string(b))
}

// K-127 end to end: dispatching general-codex executes codex, not claude.
func TestRun_PinnedAgentsExecTheirOwnRuntime(t *testing.T) {
	root := routingRoot(t)
	for _, c := range []struct{ agent, bin string }{{"general-codex", "codex"}, {"general-agy", "agy"}} {
		rec := fakeCLIs(t)
		logDir := isolatedLogDir(t)
		out, res, err := Run(context.Background(), Request{AgentName: c.agent, Task: "hi", Project: t.TempDir(), YakosRoot: root})
		if err != nil {
			t.Fatalf("%s: %v", c.agent, err)
		}
		if got := invoked(t, rec); strings.Join(got, ",") != c.bin {
			t.Errorf("%s executed %v, want only %q", c.agent, got, c.bin)
		}
		if !strings.Contains(string(out), c.bin+" answer") {
			t.Errorf("%s: output %q does not come from %s", c.agent, out, c.bin)
		}
		if res.RuntimeChosenBy != RuntimeByFrontmatter || res.FallbackFrom != "" {
			t.Errorf("%s: result routing = %q/%q", c.agent, res.RuntimeChosenBy, res.FallbackFrom)
		}
		events := readDispatchLog(t, logDir)
		fin := events[len(events)-1]
		assertField(t, fin, "runtime", c.bin)
		assertField(t, fin, "runtime_chosen_by", "frontmatter")
		if _, ok := fin["fallback_from"]; ok {
			t.Errorf("%s: fallback_from must be omitted when no fallback happened: %v", c.agent, fin)
		}
	}
}

// The model id the agent pins reaches the dispatch record (wp-p0b wires -m).
func TestRun_NonClaudeModelIsResolvedAndLogged(t *testing.T) {
	root := routingRoot(t)
	fakeCLIs(t)
	logDir := isolatedLogDir(t)
	if _, _, err := Run(context.Background(), Request{AgentName: "general-codex", Task: "hi", Project: t.TempDir(), YakosRoot: root}); err != nil {
		t.Fatal(err)
	}
	events := readDispatchLog(t, logDir)
	fin := events[len(events)-1]
	assertField(t, fin, "model_resolved", "gpt-5")
	assertField(t, fin, "model_chosen_by", "frontmatter")
	started := events[len(events)-2]
	assertField(t, started, "model", "gpt-5")
}

// The fallback is executed, recorded in the log, and printed once.
func TestRun_FallbackIsExecutedAndRecorded(t *testing.T) {
	root := routingRoot(t)
	rec := fakeCLIs(t)
	logDir := isolatedLogDir(t)
	logbuf := captureRouteLog(t)
	withProbe(t, func(name string) probeResult {
		if name == "agy" {
			return probeResult{Reason: "not signed in; run: yakos auth login agy"}
		}
		return probeResult{OK: true}
	})
	_, res, err := Run(context.Background(), Request{AgentName: "pinned-fb", Task: "hi", Project: t.TempDir(), YakosRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := invoked(t, rec); strings.Join(got, ",") != "codex" {
		t.Errorf("executed %v, want only codex", got)
	}
	if res.RuntimeChosenBy != RuntimeByFallback || res.FallbackFrom != "agy" {
		t.Errorf("result routing = %q from %q", res.RuntimeChosenBy, res.FallbackFrom)
	}
	events := readDispatchLog(t, logDir)
	fin := events[len(events)-1]
	assertField(t, fin, "runtime", "codex")
	assertField(t, fin, "runtime_chosen_by", "fallback")
	assertField(t, fin, "fallback_from", "agy")
	if n := strings.Count(logbuf.String(), "falling back"); n != 1 {
		t.Errorf("want exactly one fallback line, got %d: %q", n, logbuf.String())
	}
}

// Failing fast means no process and no dispatch event of any kind.
func TestRun_NoUsableRuntimeStartsNothing(t *testing.T) {
	root := routingRoot(t)
	rec := fakeCLIs(t)
	logDir := isolatedLogDir(t)
	withProbe(t, func(name string) probeResult {
		return probeResult{Reason: "not signed in; run: yakos auth login " + name}
	})
	_, _, err := Run(context.Background(), Request{AgentName: "general-agy", Task: "hi", Project: t.TempDir(), YakosRoot: root})
	if err == nil || !strings.Contains(err.Error(), "agy: not signed in") {
		t.Fatalf("err = %v", err)
	}
	if got := invoked(t, rec); len(got) != 0 {
		t.Errorf("a process was started: %v", got)
	}
	if entries, _ := os.ReadDir(logDir); len(entries) != 0 {
		t.Errorf("a dispatch that never started wrote %d log file(s)", len(entries))
	}
}

// RunStream routes identically: the streaming path used by console chat.
func TestRunStream_PinnedAgentRoutesAndCarriesModel(t *testing.T) {
	root := routingRoot(t)
	svc := newResolutionSvc(t, root)
	cases := []struct {
		agent, runtimeParam, wantRT, wantModel string
		wantExplicit                           bool
	}{
		{"general-codex", "", "codex", "gpt-5", true},
		{"general-agy", "auto", "agy", "gemini-3.5", true},
		{"general-codex", "claude", "claude", "sonnet", false},
		{"bare", "codex", "codex", "gpt-5-mini", false},
		{"plain", "", "claude", "sonnet", true},
	}
	for _, c := range cases {
		var req Request
		var chat rt.ChatDispatchRequest
		var adapterName string
		withStreamRunFn(func(_ context.Context, r Request, a rt.Adapter, cr rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
			req, chat, adapterName = r, cr, a.Name()
			return Result{}, nil
		}, func() {
			if _, err := svc.RunStream(context.Background(), Params{Agent: c.agent, Task: "hi", Project: t.TempDir(), Runtime: c.runtimeParam}, func(StreamChunk) {}); err != nil {
				t.Fatalf("%s/%q: %v", c.agent, c.runtimeParam, err)
			}
		})
		if req.Runtime != c.wantRT || adapterName != c.wantRT {
			t.Errorf("%s/%q: runtime %q adapter %q, want %q", c.agent, c.runtimeParam, req.Runtime, adapterName, c.wantRT)
		}
		if chat.ModelOverride != c.wantModel || chat.ModelExplicit != c.wantExplicit {
			t.Errorf("%s/%q: chat model %q explicit=%v, want %q explicit=%v", c.agent, c.runtimeParam, chat.ModelOverride, chat.ModelExplicit, c.wantModel, c.wantExplicit)
		}
	}
}

// Defaults are resolved per runtime, not the literal "sonnet" (D4).
func TestRunStream_DefaultModelIsPerRuntime(t *testing.T) {
	root := routingRoot(t)
	svc := newResolutionSvc(t, root)
	for rtName, want := range map[string]string{"claude": "sonnet", "codex": "gpt-5-mini", "agy": "gemini-3.1-pro"} {
		var req Request
		withStreamRunFn(func(_ context.Context, r Request, _ rt.Adapter, _ rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
			req = r
			return Result{}, nil
		}, func() {
			if _, err := svc.RunStream(context.Background(), Params{Agent: "bare", Task: "hi", Project: t.TempDir(), Runtime: rtName}, func(StreamChunk) {}); err != nil {
				t.Fatal(err)
			}
		})
		if req.ModelResolved != want {
			t.Errorf("%s: ModelResolved = %q, want %q", rtName, req.ModelResolved, want)
		}
	}
}

// ---- the real probe ------------------------------------------------------------

func TestDefaultRuntimeProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"OPENAI_API_KEY", "CODEX_HOME", "ANTIGRAVITY_API_KEY", "GEMINI_API_KEY"} {
		t.Setenv(k, "")
	}

	// Nothing installed.
	t.Setenv("PATH", t.TempDir())
	if p := defaultRuntimeProbe("codex"); p.OK || !strings.Contains(p.Reason, "not found on PATH") {
		t.Errorf("codex missing: %+v", p)
	}
	if p := defaultRuntimeProbe("claude-sdk"); p.OK || !strings.Contains(p.Reason, "not supported") {
		t.Errorf("claude-sdk: %+v", p)
	}

	// Installed but signed out: the D13 case.
	bin := t.TempDir()
	for _, n := range []string{"codex", "agy", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	if p := defaultRuntimeProbe("codex"); p.OK || !strings.Contains(p.Reason, "not signed in") || !strings.Contains(p.Reason, "yakos auth login codex") {
		t.Errorf("codex signed out: %+v", p)
	}
	if p := defaultRuntimeProbe("agy"); p.OK || !strings.Contains(p.Reason, "not signed in") {
		t.Errorf("agy signed out: %+v", p)
	}
	// claude's credentials cannot be probed: installed counts.
	if p := defaultRuntimeProbe("claude"); !p.OK {
		t.Errorf("claude installed: %+v", p)
	}

	// Signed in.
	t.Setenv("OPENAI_API_KEY", "sk-test")
	if p := defaultRuntimeProbe("codex"); !p.OK {
		t.Errorf("codex with a key: %+v", p)
	}
	t.Setenv("ANTIGRAVITY_API_KEY", "k")
	if p := defaultRuntimeProbe("agy"); !p.OK {
		t.Errorf("agy with a key: %+v", p)
	}
}
