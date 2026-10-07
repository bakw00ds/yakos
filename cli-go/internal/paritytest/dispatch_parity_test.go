package paritytest

// dispatch_parity_test.go: case "dispatch-dry-run-parity" (K-143).
//
// What is compared. cli/lib/dispatch.sh has no dry-run and the bash tree has
// no router, so the parity target is the RESOLUTION for a roster with no
// router policy file: which runtime an agent runs on, which model tier it
// gets, and the shape of the argv the runtime CLI is started with. The bash
// side runs the real dispatch.sh against stub runtime CLIs that record their
// argv and exit 0 (its resolution line is on stderr). The Go side asks
// `yakos dispatch --explain` for the decision and runs the real dispatch for
// the argv. Every agent of lib/agents plus a fixture project roster is
// resolved under each project-config variant (none, default-runtime,
// per-domain, router.disable_runtimes) and under an explicit --runtime whose
// CLI is missing. The first divergence that is not listed below fails the case.
//
// DOCUMENTED DIVERGENCES (Go-only behaviour; none applies to the rows that are
// expected to be equal, and each listed one must still be observed, so a
// divergence that quietly goes away fails the case too):
//
//	D1 router.disable_runtimes (.yakos.yml): Go skips or refuses a disabled
//	   runtime, bash does not read the key.
//	D2 explicit --runtime that cannot run: Go fails (ExplicitRuntimeError) unless
//	   --runtime-fallback opts in; bash walks the fallback lists (K-132).
//	D3 model of a non-claude runtime: bash prints the tier (sonnet), Go maps it
//	   to the runtime's model id (models registry). Models are compared for
//	   claude only.
//	D4 `model-policy:` agent frontmatter (a promoted eval policy): bash lets it
//	   override `model:`, Go does not read it (the router policy file replaces it).
//	D5 not exercised because the case runs with no policy file: router rules,
//	   agent pins in the policy, cooldown, sticky conversations, `--explain`
//	   itself (bash has none).
//
// The argv shape compared is the claude CLI's flags and flag values (paths
// replaced), the --agents ids and their model, and the --model value; the task
// text and the agent prompt body are not compared.
//
// Requires bin/yakos (make build) and jq. A missing binary skips unless
// YAKOS_REQUIRE_GO_BINARY=1, as in tests/run-runtime-fixtures.sh.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

type pvariant struct {
	name     string
	yml      string
	extra    []string // extra dispatch args for both sides
	agents   []string // nil: the whole roster; else only these (the fixture ids are always available)
	expected string   // non-empty: a documented divergence id; the row MUST diverge
}

// sample is the subset that covers each branch of the chain: a plain agent, one
// per mapped domain, a codex pin, an agy pin with a fallback.
var sample = []string{"backend", "security-reviewer", "test-runner", "general-codex", "fx-plain", "fx-review", "fx-agy-fallback"}

var parityVariants = []pvariant{
	{name: "no-config"},
	{name: "default-runtime-codex", yml: "default-runtime: codex\n", agents: sample},
	{name: "per-domain", yml: "per-domain:\n  security: codex\n  testing: codex\n  code-review: codex\n", agents: sample},
	{name: "default-codex-fallback-claude", yml: "default-runtime: codex\ndefault-fallback: [claude]\n", agents: sample},
	{name: "disable-runtime", yml: "default-runtime: codex\nrouter:\n  disable_runtimes: [codex]\n", agents: []string{"backend", "fx-plain"}, expected: "D1"},
	{name: "explicit-runtime-unavailable", yml: "default-fallback: [claude]\n", extra: []string{"--runtime", "agy"}, agents: []string{"backend", "fx-plain"}, expected: "D2"},
	{name: "model-policy-frontmatter", agents: []string{"fx-policy"}, expected: "D4"},
}

var fixtureAgents = map[string]string{
	"fx-plain":        "id: fx-plain\n",
	"fx-review":       "id: fx-review\ndomain: code-review\n",
	"fx-policy":       "id: fx-policy\nmodel: balanced\nmodel-policy: opus\n",
	"fx-agy-fallback": "id: fx-agy-fallback\nruntime: agy\nruntime-fallback: [codex]\n",
}

type resolution struct {
	Runtime, Model, Err string
	Argv                []string
}

func (r resolution) String() string {
	if r.Err != "" {
		return "ERR " + r.Err
	}
	return r.Runtime + "/" + r.Model
}

type parityEnv struct {
	bash, goBin, repo, tools string
	extraGoHome              func(home string) // seeds the Go side only (the divergence probe)
}

// requireBinaries returns the paths of bin/yakos and cli/yakos, or skips (fails
// under YAKOS_REQUIRE_GO_BINARY=1).
func requireBinaries(t *testing.T) parityEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash and POSIX stubs")
	}
	e := parityEnv{bash: bashBinary(), goBin: goBinary(), repo: filepath.Clean(repoRootFromSource())}
	for _, p := range []string{e.bash, e.goBin} {
		if _, err := os.Stat(p); err != nil {
			if os.Getenv("YAKOS_REQUIRE_GO_BINARY") == "1" {
				t.Fatalf("%s missing: run make build", p)
			}
			t.Skipf("%s missing: run make build", p)
		}
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required by cli/lib/dispatch.sh")
	}
	e.tools = toolDir(t)
	return e
}

// toolDir links every command of the usual bin directories except timeout and
// gtimeout into one directory, which becomes the whole PATH after the stubs.
// cli/lib/dispatch.sh runs its adapter through ct_timeout; with GNU timeout on
// PATH (Linux) that fails ("failed to run command 'yk_rt_dispatch'", exit 127,
// before any runtime starts) and no argv is recorded. macOS has neither, and
// ct_timeout then runs the command directly, which is the behavior we compare.
func toolDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, src := range []string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin"} {
		ents, err := os.ReadDir(src)
		if err != nil {
			continue
		}
		for _, en := range ents {
			n := en.Name()
			if n == "timeout" || n == "gtimeout" {
				continue
			}
			if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
				continue
			}
			_ = os.Symlink(filepath.Join(src, n), filepath.Join(dir, n))
		}
	}
	return dir
}

// newParityEnv is requireBinaries for the resolution comparison. The CI job runs
// it under YAKOS_IMPL=go and YAKOS_IMPL=bash: the Go side then gets the same
// value. With bash the "Go side" is the bash oracle itself, which has no
// --explain; TestLauncherDefaultIsGoAndBashOverrideWorks covers that path.
func newParityEnv(t *testing.T) parityEnv {
	t.Helper()
	e := requireBinaries(t)
	if os.Getenv("YAKOS_IMPL") == "bash" {
		t.Skip("YAKOS_IMPL=bash sends the Go binary's dispatch to bash; see TestLauncherDefaultIsGoAndBashOverrideWorks")
	}
	return e
}

// makeStubs writes the runtime CLIs for one invocation. The Go dispatcher hands
// its child a filtered environment, so the output path is baked into the stub.
func makeStubs(t *testing.T) (dir, argv string) {
	t.Helper()
	dir = t.TempDir()
	argv = filepath.Join(dir, "argv")
	for _, cli := range []string{"claude", "codex"} {
		body := "#!/bin/bash\nprintf '%s\\0' \"$@\" > '" + argv + "'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, cli), []byte(body), 0o755); err != nil { //nolint:gosec
			t.Fatal(err)
		}
	}
	return dir, argv
}

// run starts bin with a clean environment; a binary that exits non-zero is a
// result, anything else is fatal.
func (e parityEnv) run(t *testing.T, bin, home, project, stubs string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // controlled test paths
	cmd.Dir = project
	cmd.Env = []string{
		"HOME=" + home, "PATH=" + stubs + ":" + e.tools,
		"ANTHROPIC_API_KEY=x", "OPENAI_API_KEY=x", "TMPDIR=" + os.TempDir(),
	}
	// The Go binary gets the ambient YAKOS_IMPL only when it is "go"; unset
	// exercises the default (K-143). The bash CLI never reads it.
	if bin == e.goBin && os.Getenv("YAKOS_IMPL") == "go" {
		cmd.Env = append(cmd.Env, "YAKOS_IMPL=go")
	}
	var o, er bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &er
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s: %v", bin, err)
		}
		code = ee.ExitCode()
	}
	return o.String(), er.String(), code
}

var (
	bashLineRe = regexp.MustCompile(`yakos dispatch: agent=\S+ runtime=(\S+) model=(\S+) \(by:(\S+)\)`)
	goLineRe   = regexp.MustCompile(`(?m)^([a-z0-9._-]+)(?:/(\S+))? rule=`)
)

func (e parityEnv) bashSide(t *testing.T, project, agent string, extra []string) resolution {
	t.Helper()
	home := t.TempDir()
	stubs, out := makeStubs(t)
	_, stderr, code := e.run(t, e.bash, home, project, stubs, append([]string{"dispatch", agent, "do the thing", "--project", project}, extra...)...)
	// The bash CLI has no YAKOS_IMPL gate; it ignores the variable.
	m := bashLineRe.FindStringSubmatch(stderr)
	if m == nil {
		return resolution{Err: firstLine(stderr, "exit "+fmt.Sprint(code))}
	}
	return resolution{Runtime: m[1], Model: m[2], Argv: readArgv(out)}
}

func (e parityEnv) goSide(t *testing.T, project, agent string, extra []string) resolution {
	t.Helper()
	home := t.TempDir()
	stubs, out := makeStubs(t)
	if e.extraGoHome != nil {
		e.extraGoHome(home)
	}
	args := append([]string{"dispatch", agent, "do the thing", "--project", project}, extra...)
	stdout, stderr, code := e.run(t, e.goBin, home, project, stubs, append([]string{args[0], "--explain"}, args[1:]...)...)
	m := goLineRe.FindStringSubmatch(stdout)
	if code != 0 || m == nil {
		return resolution{Err: firstLine(stderr+stdout, "exit "+fmt.Sprint(code))}
	}
	res := resolution{Runtime: m[1], Model: m[2]}
	if res.Runtime == "claude" {
		_, _, _ = e.run(t, e.goBin, home, project, stubs, args...)
		res.Argv = readArgv(out)
	}
	return res
}

func firstLine(s, def string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.Contains(l, "timeout") {
			// Error text names the temp project; the table must not.
			return regexp.MustCompile(`/\S+`).ReplaceAllString(l, "<path>")
		}
	}
	return def
}

func readArgv(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}

// argvShape reduces a claude argv to its flags, with the values that are
// stable and the --agents JSON replaced by its agent ids and model.
func argvShape(argv []string, project string) string {
	var out []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch a {
		case "-p":
			i = len(argv) // the task text is not compared
		case "--agents":
			i++
			var m map[string]struct {
				Model string `json:"model"`
			}
			if i < len(argv) && json.Unmarshal([]byte(argv[i]), &m) == nil {
				var ids []string
				for id, v := range m {
					ids = append(ids, id+":"+v.Model)
				}
				sort.Strings(ids)
				out = append(out, "--agents{"+strings.Join(ids, ",")+"}")
			} else {
				out = append(out, "--agents{unparsed}")
			}
		default:
			out = append(out, strings.ReplaceAll(a, project, "<project>"))
		}
	}
	return strings.Join(out, " ")
}

type paritySpec struct {
	variant pvariant
	agent   string
	project string
}

// diverge says how bash and Go differ for one row ("" when they agree).
func diverge(bash, goR resolution, project string) string {
	if bash.Err != "" || goR.Err != "" {
		if bash.Err != "" && goR.Err != "" {
			return ""
		}
		return "error vs result"
	}
	if bash.Runtime != goR.Runtime {
		return "runtime"
	}
	if bash.Runtime == "claude" {
		if bash.Model != goR.Model {
			return "model"
		}
		if a, b := argvShape(bash.Argv, project), argvShape(goR.Argv, project); a != b {
			return "argv"
		}
	}
	return ""
}

// collectParity resolves every (variant, agent) row on both sides.
func collectParity(t *testing.T, e parityEnv, variants []pvariant) (header string, compared int, unexpected []string, seenExpected map[string]int) {
	t.Helper()
	roster := rosterAgents(t, e.repo)
	type job struct {
		spec paritySpec
		b, g resolution
	}
	var jobs []*job
	for _, v := range variants {
		project := t.TempDir()
		if v.yml != "" {
			mustWrite(t, filepath.Join(project, ".yakos.yml"), v.yml)
		}
		for id, fm := range fixtureAgents {
			mustWrite(t, filepath.Join(project, ".claude", "agents", id+".md"), "---\n"+fm+"---\n\n## Purpose\n\nParity fixture.\n")
		}
		agents := v.agents
		if agents == nil {
			agents = append(append([]string{}, roster...), "fx-plain", "fx-review", "fx-agy-fallback")
		}
		for _, a := range agents {
			jobs = append(jobs, &job{spec: paritySpec{v, a, project}})
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j *job) {
			defer wg.Done()
			defer func() { <-sem }()
			j.b = e.bashSide(t, j.spec.project, j.spec.agent, j.spec.variant.extra)
			j.g = e.goSide(t, j.spec.project, j.spec.agent, j.spec.variant.extra)
		}(j)
	}
	wg.Wait()
	seenExpected = map[string]int{}
	header = fmt.Sprintf("%-30s %-24s %-22s %-22s %s", "variant", "agent", "bash", "go", "diff")
	for _, j := range jobs {
		d := diverge(j.b, j.g, j.spec.project)
		if d != "" && j.spec.variant.expected != "" {
			seenExpected[j.spec.variant.expected]++
			continue
		}
		if d != "" || j.spec.variant.expected != "" {
			// Unexpected divergence; a row listed as divergent that agrees is
			// reported too (the list must stay honest).
			if d == "" {
				d = "expected " + j.spec.variant.expected + " but they agree"
			}
			unexpected = append(unexpected, fmt.Sprintf("%-30s %-24s %-22s %-22s %s",
				j.spec.variant.name, j.spec.agent, j.b, j.g, d))
		}
	}
	return header, len(jobs), unexpected, seenExpected
}

func rosterAgents(t *testing.T, repo string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(repo, "lib", "agents"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, en := range ents {
		n := en.Name()
		if en.IsDir() || !strings.HasSuffix(n, ".md") || n == "README.md" || n == "lead-template.md" {
			continue
		}
		ids = append(ids, strings.TrimSuffix(n, ".md"))
	}
	sort.Strings(ids)
	return ids
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchDryRunParity(t *testing.T) {
	e := newParityEnv(t)
	header, n, bad, seen := collectParity(t, e, parityVariants)
	if len(bad) > 0 {
		t.Fatalf("bash and Go resolve differently (%d rows):\n%s\n%s", len(bad), header, strings.Join(bad, "\n"))
	}
	t.Logf("%d rows compared, %d expected divergences observed %v", n, len(seen), seen)
	for _, v := range parityVariants {
		if v.expected != "" && seen[v.expected] == 0 {
			t.Errorf("documented divergence %s was not observed", v.expected)
		}
	}
}

// A Go-only difference in the resolution inputs (here a state default the bash
// side does not see) must fail the case with a table naming the rows.
func TestDispatchDryRunParityDetectsGoSideDivergence(t *testing.T) {
	e := newParityEnv(t)
	e.extraGoHome = func(home string) {
		dir := filepath.Join(home, ".yakos-state")
		mustWrite(t, filepath.Join(dir, "default-runtime"), "codex\n")
		_ = os.Chmod(dir, 0o700)
		_ = os.Chmod(filepath.Join(dir, "default-runtime"), 0o600)
	}
	probe := parityVariants[0]
	probe.agents = []string{"backend", "fx-plain"}
	_, _, bad, _ := collectParity(t, e, []pvariant{probe})
	if len(bad) == 0 {
		t.Fatal("a Go-side state default changed the resolution and the case did not notice")
	}
	if !strings.Contains(strings.Join(bad, "\n"), "runtime") {
		t.Errorf("the table does not name the divergence:\n%s", strings.Join(bad, "\n"))
	}
}

// `yakos dispatch` runs the Go path with YAKOS_IMPL unset, although the bash
// tree sits next to the binary; YAKOS_IMPL=bash is the way back (K-143).
func TestLauncherDefaultIsGoAndBashOverrideWorks(t *testing.T) {
	e := requireBinaries(t)
	stubs, _ := makeStubs(t)
	project, home := t.TempDir(), t.TempDir()
	run := func(impl string, args ...string) (string, int) {
		cmd := exec.Command(e.goBin, args...) //nolint:gosec // controlled test paths
		cmd.Dir = project
		cmd.Env = []string{"HOME=" + home, "PATH=" + stubs + ":" + e.tools, "ANTHROPIC_API_KEY=x"}
		if impl != "" {
			cmd.Env = append(cmd.Env, "YAKOS_IMPL="+impl)
		}
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	explain := []string{"dispatch", "--explain", "backend", "task", "--project", project}
	for _, impl := range []string{"", "go"} {
		out, code := run(impl, explain...)
		if code != 0 || !strings.HasPrefix(out, "claude/sonnet rule=R0") {
			t.Errorf("YAKOS_IMPL=%q: dispatch --explain must be answered by the Go path (exit %d):\n%s", impl, code, out)
		}
	}
	// bash has no --explain: reaching it proves the override sends dispatch there.
	out, code := run("bash", explain...)
	if code == 0 || !strings.Contains(out, "unknown flag '--explain'") {
		t.Errorf("YAKOS_IMPL=bash must restore the bash dispatch (exit %d):\n%s", code, out)
	}
}
