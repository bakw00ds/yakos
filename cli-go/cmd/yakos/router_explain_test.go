package main

// router_explain_test.go: `yakos router explain` and `yakos dispatch --explain`
// (K-139b). The goldens under testdata/router-explain/ pin the printed form; the
// CI step route-explain-golden runs them (TestRouterExplainGolden*) under
// YAKOS_IMPL=go. Set YAKOS_UPDATE_GOLDEN=1 to rewrite them.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/router"
)

const explainSamplePolicy = `gateway_classes:
  subagent: haiku
  opus: claude-opus-4-1-20250805
rules:
  - match: {domain: code-review}
    action: {runtime: codex, model: gpt-5.5, fallbacks: [claude]}
  - match: {task_bytes_gt: 20000}
    action: {model: haiku}
    override_pins: true
  - match: {class: chat}
    action: {runtime: claude, model: sonnet, fallbacks: [codex]}
`

// explainFixture is the fixture roster, a home with stub CLIs on PATH and the
// state directories the run may not touch.
type explainFixture struct {
	root, project, home, ledger, bin, ran string
}

func newExplainFixture(t *testing.T, policy string) explainFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stubs and POSIX modes")
	}
	f := explainFixture{root: t.TempDir(), project: t.TempDir(), home: t.TempDir(), ledger: t.TempDir(), bin: t.TempDir()}
	agents := filepath.Join(f.root, "lib", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, fm := range map[string]string{
		"backend":  "domain: code\n",
		"reviewer": "domain: code-review\n",
		"pinned":   "domain: code\nmodel: opus\n",
	} {
		body := "---\nid: " + id + "\n" + fm + "---\n\n## Purpose\n\nExplain fixture " + id + ".\n"
		if err := os.WriteFile(filepath.Join(agents, id+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.ran = filepath.Join(t.TempDir(), "ran")
	for _, cli := range []string{"claude", "codex"} {
		stub := "#!/bin/sh\nprintf '%s\\n' \"$0\" >> '" + f.ran + "'\n"
		if err := os.WriteFile(filepath.Join(f.bin, cli), []byte(stub), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	if policy != "" {
		dir := filepath.Join(f.home, ".yakos-state")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "router-policy.yml")
		if err := os.WriteFile(p, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// run executes `yakos <args>` on the Go implementation with the stubs on PATH.
func (f explainFixture) run(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	all := append([]string{
		"YAKOS_IMPL=go", "YAKOS_ROOT=" + f.root, "HOME=" + f.home,
		"PATH=" + f.bin + string(os.PathListSeparator) + "/usr/bin:/bin", "OPENAI_API_KEY=x",
	}, env...)
	return runYakos(t, f.ledger, all, args...)
}

// snapshot lists every file under dir with its size and content, so a run that
// writes anything shows up.
func snapshotDir(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		b, _ := os.ReadFile(p)
		lines = append(lines, fmt.Sprintf("%s %v %x", rel, fi.Mode(), b))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

type explainCase struct {
	name   string
	args   []string
	env    []string
	bigTsk bool // add --task-file with a 21000-byte file
}

func explainCases() (noPolicy, withPolicy []explainCase) {
	noPolicy = []explainCase{
		{name: "backend", args: []string{"router", "explain", "backend"}},
		{name: "reviewer", args: []string{"router", "explain", "reviewer"}},
		{name: "pinned-json", args: []string{"router", "explain", "pinned", "--json"}},
		{name: "dispatch-explain", args: []string{"dispatch", "--explain", "backend"}},
		{name: "dispatch-explain-runtime-override", args: []string{"dispatch", "--explain", "backend", "do the thing", "--runtime", "codex"}},
		{name: "class-subagent", args: []string{"router", "explain", "backend", "--class", "subagent"}},
	}
	withPolicy = []explainCase{
		{name: "backend-default", args: []string{"router", "explain", "backend"}},
		{name: "backend-chat-demo", args: []string{"router", "explain", "backend", "--class", "chat"}},
		{name: "backend-chat-json", args: []string{"router", "explain", "backend", "--class", "chat", "--json"}},
		{name: "reviewer-r1", args: []string{"router", "explain", "reviewer"}},
		{name: "backend-big-task-r2", args: []string{"router", "explain", "backend"}, bigTsk: true},
		{name: "pinned-chat-pin-outranks-rule", args: []string{"router", "explain", "pinned", "--class", "chat"}},
		{name: "pinned-big-task-override-pins", args: []string{"router", "explain", "pinned"}, bigTsk: true},
		{name: "dispatch-explain-model-override", args: []string{"dispatch", "--explain", "backend", "--model", "opus"}},
		{name: "class-subagent-env", args: []string{"router", "explain", "backend", "--class", "subagent"}},
		{name: "class-opus-env-overridden", args: []string{"router", "explain", "backend", "--class", "opus"}, env: []string{"ANTHROPIC_DEFAULT_OPUS_MODEL=claude-opus-4-1-20250805"}},
		{name: "class-opus-env-json", args: []string{"router", "explain", "backend", "--class", "opus", "--json"}},
	}
	return
}

// goldenCompare compares got with testdata/router-explain/<name>.golden.
func goldenCompare(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "router-explain", name+".golden")
	if os.Getenv("YAKOS_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (YAKOS_UPDATE_GOLDEN=1 writes it): %v", name, err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden:\n--- want\n%s--- got\n%s", name, want, got)
	}
}

func runExplainGoldens(t *testing.T, prefix, policy string, cases []explainCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newExplainFixture(t, policy)
			args := append([]string{}, c.args...)
			args = append(args, "--project", f.project)
			if c.bigTsk {
				big := filepath.Join(t.TempDir(), "task.txt")
				if err := os.WriteFile(big, bytes.Repeat([]byte("x"), 21000), 0o644); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--task-file", big)
			}
			before, ledgerBefore := snapshotDir(t, f.home), snapshotDir(t, f.ledger)
			code, out := f.run(t, c.env, args...)
			goldenCompare(t, prefix+"-"+c.name, fmt.Sprintf("exit: %d\n%s", code, out))
			if code != 0 {
				t.Errorf("exit %d:\n%s", code, out)
			}
			// An explain starts nothing, writes no ledger row and changes no state.
			if b, err := os.ReadFile(f.ran); err == nil && len(b) > 0 {
				t.Errorf("a CLI was run by the explain: %s", b)
			}
			if got := snapshotDir(t, f.ledger); got != ledgerBefore {
				t.Errorf("the ledger directory was written: %s", got)
			}
			if after := snapshotDir(t, f.home); after != before {
				t.Errorf("the home directory changed:\nbefore %s\nafter  %s", before, after)
			}
			for _, bad := range []string{f.root, f.project, f.home, f.bin} {
				if strings.Contains(out, bad) {
					t.Errorf("output names a path %q:\n%s", bad, out)
				}
			}
		})
	}
}

func TestRouterExplainGoldenNoPolicy(t *testing.T) {
	np, _ := explainCases()
	runExplainGoldens(t, "nopolicy", "", np)
}

func TestRouterExplainGoldenWithPolicy(t *testing.T) {
	_, wp := explainCases()
	runExplainGoldens(t, "policy", explainSamplePolicy, wp)
}

// The router needs the demo from docs/routing.md to stay true.
func TestRouterExplainDemoLine(t *testing.T) {
	f := newExplainFixture(t, explainSamplePolicy)
	code, out := f.run(t, nil, "router", "explain", "backend", "--class", "chat", "--project", f.project)
	if first, _, _ := strings.Cut(out, "\n"); code != 0 || first != "claude/sonnet rule=R3 chain=[claude codex]" {
		t.Fatalf("exit %d, first line %q:\n%s", code, first, out)
	}
}

// A runtime on its cooldown is shown as cooling, without the seconds left.
func TestRouterExplainGoldenCooling(t *testing.T) {
	d := router.RouteDecision{
		Runtime: "claude", Provider: "anthropic", ModelID: "sonnet",
		Chain: []string{"codex", "claude"}, RuleID: "R1", RouteClass: "default",
		Reason:       "rule R1 matched [domain=code-review]: runtime=codex fallbacks=[claude]; fell back from codex",
		FallbackFrom: "codex", PolicySHA: "0123456789abcdef",
		Skipped: []router.Skip{{Runtime: "codex", Reason: "cooling down after repeated failures (41s left)", Cooling: true}},
	}
	var b bytes.Buffer
	router.WriteExplain(&b, router.ExplainView{Agent: "reviewer", Decision: d})
	goldenCompare(t, "cooling", b.String())
	d.Skipped[0].Reason = "cooling down after repeated failures (7s left)"
	var b2 bytes.Buffer
	router.WriteExplain(&b2, router.ExplainView{Agent: "reviewer", Decision: d})
	if b.String() != b2.String() {
		t.Errorf("the seconds left on a cooldown reach the output:\n%s\n%s", b.String(), b2.String())
	}
	j, err := router.ExplainJSON(router.ExplainView{Agent: "reviewer", Decision: d})
	if err != nil {
		t.Fatal(err)
	}
	goldenCompare(t, "cooling-json", string(j))
}

// The JSON form has a fixed schema: every key present, arrays never null.
func TestRouterExplainJSONSchema(t *testing.T) {
	f := newExplainFixture(t, explainSamplePolicy)
	_, out := f.run(t, nil, "router", "explain", "backend", "--json", "--project", f.project)
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := "agent chain fallback_from jev_shadow model overrides policy_sha provider reason route_class rule runtime skipped"
	if got := strings.Join(keys, " "); got != want {
		t.Errorf("keys %q, want %q", got, want)
	}
	for _, k := range []string{"chain", "overrides", "skipped"} {
		if !strings.HasPrefix(string(m[k]), "[") {
			t.Errorf("%s = %s, want an array", k, m[k])
		}
	}
	if code, out := f.run(t, nil, "router", "explain", "backend", "--runtime", "x", "--project", f.project); code != 2 {
		t.Errorf("router explain accepted --runtime: exit %d\n%s", code, out)
	}
}

func TestRouterExplainArgvSafety(t *testing.T) {
	f := newExplainFixture(t, explainSamplePolicy)
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"bad agent":           {[]string{"router", "explain", "../etc/passwd"}, "invalid agent name"},
		"agent with space":    {[]string{"router", "explain", "a b"}, "invalid agent name"},
		"bad class":           {[]string{"router", "explain", "backend", "--class", "a;rm -rf"}, "invalid class"},
		"unknown class":       {[]string{"router", "explain", "backend", "--class", "nope"}, `unknown class "nope" (known: chat, default, fable, haiku, opus, sonnet, subagent)`},
		"no agent":            {[]string{"router", "explain"}, "expected exactly one <agent>"},
		"two agents":          {[]string{"router", "explain", "backend", "reviewer"}, "expected exactly one <agent>"},
		"unknown subcommand":  {[]string{"router", "frobnicate"}, "unknown subcommand"},
		"task file missing":   {[]string{"router", "explain", "backend", "--task-file", filepath.Join(f.home, "nope")}, "--task-file: cannot stat the file"},
		"task file directory": {[]string{"router", "explain", "backend", "--task-file", f.home}, "--task-file: not a regular file"},
	} {
		t.Run(name, func(t *testing.T) {
			code, out := f.run(t, nil, append(tc.args, "--project", f.project)...)
			if code != 2 {
				t.Errorf("exit %d, want 2:\n%s", code, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
			for _, p := range []string{f.home, f.root, f.project} {
				if strings.Contains(out, p) {
					t.Errorf("message names a path %q:\n%s", p, out)
				}
			}
		})
	}
	// An agent that does not exist is a usage error, exit 2, and names no path.
	for _, args := range [][]string{
		{"router", "explain", "ghost", "--project", f.project},
		{"dispatch", "--explain", "ghost", "--project", f.project},
	} {
		code, out := f.run(t, nil, args...)
		if code != 2 {
			t.Errorf("%v unknown agent: exit %d, want 2:\n%s", args, code, out)
		}
		if !strings.Contains(out, `"ghost" not found`) || strings.Contains(out, "/") ||
			strings.Contains(out, "yakosRoot=") || strings.Contains(out, "project=") {
			t.Errorf("%v unknown agent output leaks a path or lacks the name:\n%s", args, out)
		}
	}
}

// With nothing able to run, the explain fails like dispatch would and says why.
func TestRouterExplainNoRuntimeExitsOne(t *testing.T) {
	f := newExplainFixture(t, "")
	if err := os.Remove(filepath.Join(f.bin, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.bin, "codex")); err != nil {
		t.Fatal(err)
	}
	code, out := f.run(t, nil, "router", "explain", "backend", "--project", f.project)
	if code != 1 || !strings.Contains(out, "router explain:") || strings.Contains(out, "dispatch: dispatch:") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// `yakos dispatch --explain` needs no task, and without --explain a missing task
// is still an error.
func TestDispatchExplainTaskOptional(t *testing.T) {
	f := newExplainFixture(t, "")
	if code, out := f.run(t, nil, "dispatch", "--explain", "backend", "--project", f.project); code != 0 || !strings.HasPrefix(out, "claude/sonnet rule=R0 chain=[claude]") {
		t.Errorf("exit %d:\n%s", code, out)
	}
	if code, out := f.run(t, nil, "dispatch", "backend", "--project", f.project); code != 1 || !strings.Contains(out, "missing <task-prompt>") {
		t.Errorf("exit %d:\n%s", code, out)
	}
}

// The command is Go-only: shadow mode must not hand it to the bash CLI.
func TestRouterIsForcedGo(t *testing.T) {
	if !isRouterForceGo([]string{"router", "explain", "x"}) || isRouterForceGo([]string{"models"}) || isRouterForceGo(nil) {
		t.Fatal("isRouterForceGo does not select exactly `router`")
	}
}

// routerMain with an injected router: the command passes the overrides, the task
// size and the class through, and shows rule=override when the operator chose.
func TestRouterMainPassesQueryAndMarksOverride(t *testing.T) {
	task := filepath.Join(t.TempDir(), "task")
	if err := os.WriteFile(task, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	var got dispatch.ExplainQuery
	env := explainEnv{
		explain: func(_ context.Context, q dispatch.ExplainQuery) (router.RouteDecision, error) {
			got = q
			return router.RouteDecision{Runtime: "claude", Chain: []string{"claude"}, RuleID: "R2", RouteClass: "default"}, nil
		},
		stateDir: func() string { return t.TempDir() },
		environ:  func() []string { return nil },
		cwd:      os.Getwd,
	}
	proj := t.TempDir()
	var out, errb bytes.Buffer
	if code := routerMain(t.TempDir(), []string{"explain", "backend", "--task-file", task, "--project", proj}, &out, &errb, env); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got.TaskBytes != 5 || got.Agent != "backend" || got.Runtime != "" || got.Model != "" {
		t.Errorf("query %+v", got)
	}
	if !strings.Contains(out.String(), "rule=R2") || strings.Contains(out.String(), "override") {
		t.Errorf("no override was given:\n%s", out.String())
	}
	out.Reset()
	if code := explainRun(&out, &errb, env, explainArgs{Project: proj, Agent: "backend", Runtime: "codex", Model: "gpt-5.5"}, "x"); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "rule=override") || !strings.Contains(out.String(), "overrides: model,runtime") || !strings.Contains(out.String(), "underlying_rule: R2") {
		t.Errorf("override not shown:\n%s", out.String())
	}
	env.explain = func(context.Context, dispatch.ExplainQuery) (router.RouteDecision, error) {
		return router.RouteDecision{}, errors.New("dispatch: no runtime can run")
	}
	errb.Reset()
	if code := explainRun(&out, &errb, env, explainArgs{Agent: "backend"}, "router explain"); code != 1 || errb.String() != "router explain: no runtime can run\n" {
		t.Errorf("exit %d, stderr %q", code, errb.String())
	}
}

// K-177: explain says whether the Jev routing shadow is on. Only the user-level
// policy turns it on; a project file cannot.
func TestJevShadowStateOnlyTheUserPolicyTurnsItOn(t *testing.T) {
	state := t.TempDir()
	proj := t.TempDir()
	env := explainEnv{stateDir: func() string { return state }, environ: func() []string { return nil }}
	if got := jevShadowState(env, proj); got != "off" {
		t.Errorf("default = %q", got)
	}
	if err := os.WriteFile(filepath.Join(proj, ".yakos.yml"), []byte("decisions:\n  provider: jev\n  routing_shadow: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := jevShadowState(env, proj); got != "off" {
		t.Errorf("a project file turned it on: %q", got)
	}
	if err := os.WriteFile(filepath.Join(state, "decision-policy.yml"), []byte("routing_shadow: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := jevShadowState(env, proj); got != "on" {
		t.Errorf("user policy = %q", got)
	}
	env.environ = func() []string { return []string{"YAKOS_DECISION_DISABLE=1"} }
	if got := jevShadowState(env, proj); got != "off" {
		t.Errorf("kill switch = %q", got)
	}
}
