package dispatch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/agentscompose"
)

// End-to-end through the real Run: a fake codex/agy binary on PATH records its
// argv and working directory, so these tests prove the agent file exists BEFORE
// the runtime starts and that the argv matches the sandboxed contract.

// fakeRuntime installs a fake binary named name on PATH. It records argv (one
// element per line) to <dir>/argv and the working directory to <dir>/cwd.
func fakeRuntime(t *testing.T, name string) (recDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake runtime scripts need sh; skipping on Windows")
	}
	bin := t.TempDir()
	rec := t.TempDir()
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$@" > "` + rec + `/argv"` + "\n" +
		`pwd -P > "` + rec + `/cwd"` + "\n" +
		`ls ".codex/agents" ".agents/skills" > "` + rec + `/agent-files" 2>&1` + "\n" +
		"printf 'fake %s output\\n' " + name + "\n"
	if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return rec
}

// isolateHome keeps the adapters from reading the developer's real
// ~/.yakos-state (router policy, codex profile) and the dispatch-log from
// writing there.
func isolateHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	isolatedLogDir(t)
}

func runFake(t *testing.T, rt, agent string, mutate func(*Request)) (project string, recDir string, notes string, err error) {
	t.Helper()
	isolateHome(t)
	recDir = fakeRuntime(t, rt)
	var buf bytes.Buffer
	prev := materializeNoteWriter
	materializeNoteWriter = &buf
	t.Cleanup(func() { materializeNoteWriter = prev })

	project = t.TempDir()
	req := Request{
		AgentName: agent,
		Task:      "review the change",
		Project:   project,
		YakosRoot: buildMinimalYakosRoot(t),
		Runtime:   rt,
	}
	if mutate != nil {
		mutate(&req)
	}
	_, _, err = Run(context.Background(), req)
	return project, recDir, buf.String(), err
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunCodex_MaterializesAgentBeforeExecAndRunsSandboxed(t *testing.T) {
	project, rec, notes, err := runFake(t, "codex", "backend", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if notes != "" {
		t.Errorf("unexpected notes: %q", notes)
	}
	toml := readFile(t, agentscompose.CodexAgentPath(project, "backend"))
	for _, want := range []string{"# yakos-generated:", `name = "backend"`, "Backend specialist."} {
		if !strings.Contains(toml, want) {
			t.Errorf("TOML lacks %q:\n%s", want, toml)
		}
	}
	if strings.Contains(toml, "model =") {
		t.Errorf("the composed Claude tier must not leak into the codex agent file:\n%s", toml)
	}
	// The file existed when codex started (the fake lists it from its cwd).
	if got := readFile(t, filepath.Join(rec, "agent-files")); !strings.Contains(got, "yakos-backend.toml") {
		t.Errorf("codex started before the agent file existed; ls said: %q", got)
	}
	argv := readFile(t, filepath.Join(rec, "argv"))
	for _, want := range []string{"exec\n", "--json\n", "--sandbox\nworkspace-write\n", "approval_policy=\"never\"\n"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(argv, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("dispatch must be sandboxed by default:\n%s", argv)
	}
	wantDir, _ := filepath.EvalSymlinks(project)
	if got := strings.TrimSpace(readFile(t, filepath.Join(rec, "cwd"))); got != wantDir {
		t.Errorf("codex ran in %q, want the project %q", got, wantDir)
	}
}

func TestRunCodex_EffortReachesTheFramedArgv(t *testing.T) {
	_, rec, _, err := runFake(t, "codex", "backend", func(r *Request) { r.Effort = "high" })
	if err != nil {
		t.Fatal(err)
	}
	if argv := readFile(t, filepath.Join(rec, "argv")); !strings.Contains(argv, `model_reasoning_effort="high"`) {
		t.Errorf("effort did not reach codex:\n%s", argv)
	}
}

func TestRunAgy_MaterializesSkillBeforeExec(t *testing.T) {
	project, rec, notes, err := runFake(t, "agy", "backend", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if notes != "" {
		t.Errorf("unexpected notes: %q", notes)
	}
	skill := readFile(t, agentscompose.AgySkillPath(project, "backend"))
	for _, want := range []string{"name: yakos-backend", "yakos-generated:", "Backend specialist."} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md lacks %q:\n%s", want, skill)
		}
	}
	if strings.Contains(skill, "model:") {
		t.Errorf("the composed Claude tier must not leak into the agy skill:\n%s", skill)
	}
	if got := readFile(t, filepath.Join(rec, "agent-files")); !strings.Contains(got, "yakos-backend") {
		t.Errorf("agy started before the skill existed; ls said: %q", got)
	}
	argv := readFile(t, filepath.Join(rec, "argv"))
	for _, want := range []string{"--sandbox\n", "--dangerously-skip-permissions\n", "--output-format\nstream-json\n", "@yakos-backend review the change"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv lacks %q:\n%s", want, argv)
		}
	}
}

func TestRunClaude_WritesNoAgentFiles(t *testing.T) {
	isolateHome(t)
	fakeRuntime(t, "claude")
	project := t.TempDir()
	_, _, err := Run(context.Background(), Request{
		AgentName: "backend", Task: "t", Project: project,
		YakosRoot: buildMinimalYakosRoot(t), Runtime: "claude",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".codex", ".agents"} {
		if _, err := os.Stat(filepath.Join(project, p)); !os.IsNotExist(err) {
			t.Errorf("claude dispatch must not create %s (err=%v)", p, err)
		}
	}
}

func TestRunCodex_LeavesAnOperatorsAgentFileAlone(t *testing.T) {
	isolateHome(t)
	rec := fakeRuntime(t, "codex")
	var buf bytes.Buffer
	prev := materializeNoteWriter
	materializeNoteWriter = &buf
	t.Cleanup(func() { materializeNoteWriter = prev })

	project := t.TempDir()
	path := agentscompose.CodexAgentPath(project, "backend")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "name = \"mine\"\ndescription = \"hand written\"\ndeveloper_instructions = \"\"\"\nmy rules\n\"\"\"\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := Run(context.Background(), Request{
		AgentName: "backend", Task: "t", Project: project,
		YakosRoot: buildMinimalYakosRoot(t), Runtime: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != mine {
		t.Errorf("an operator's agent file was overwritten:\n%s", got)
	}
	if !strings.Contains(buf.String(), "no yakos-generated marker") {
		t.Errorf("the skipped file must be explained, got %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(rec, "argv")); err != nil {
		t.Error("the dispatch must still run")
	}
}

func TestRunCodex_UnwritableProjectDoesNotBlockTheDispatch(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	isolateHome(t)
	rec := fakeRuntime(t, "codex")
	var buf bytes.Buffer
	prev := materializeNoteWriter
	materializeNoteWriter = &buf
	t.Cleanup(func() { materializeNoteWriter = prev })

	project := t.TempDir()
	if err := os.Chmod(project, 0o555); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(project, 0o755) }) //nolint:gosec
	_, _, err := Run(context.Background(), Request{
		AgentName: "backend", Task: "t", Project: project,
		YakosRoot: buildMinimalYakosRoot(t), Runtime: "codex",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "not written") {
		t.Errorf("a failed materialize must be reported, got %q", buf.String())
	}
	if _, err := os.Stat(filepath.Join(rec, "argv")); err != nil {
		t.Error("the dispatch must still run when the agent file cannot be written")
	}
}

func TestRunCodex_WorkDirOverrideReceivesTheAgentFile(t *testing.T) {
	isolateHome(t)
	rec := fakeRuntime(t, "codex")
	project, worktree := t.TempDir(), t.TempDir()
	_, _, err := Run(context.Background(), Request{
		AgentName: "backend", Task: "t", Project: project, WorkDirOverride: worktree,
		YakosRoot: buildMinimalYakosRoot(t), Runtime: "codex",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agentscompose.CodexAgentPath(worktree, "backend")); err != nil {
		t.Errorf("the agent file must be written where codex runs (the worktree): %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".codex")); !os.IsNotExist(err) {
		t.Errorf("the main checkout must not be touched (err=%v)", err)
	}
	wantDir, _ := filepath.EvalSymlinks(worktree)
	if got := strings.TrimSpace(readFile(t, filepath.Join(rec, "cwd"))); got != wantDir {
		t.Errorf("codex ran in %q, want the worktree %q", got, wantDir)
	}
}

func TestMaterializeAgentFiles_IgnoresOtherRuntimes(t *testing.T) {
	project := t.TempDir()
	for _, rt := range []string{"claude", "gemini", "", "x"} {
		materializeAgentFiles(rt, project, "", agentscompose.ComposedAgent{ID: "backend", Prompt: "p"})
	}
	if entries, _ := os.ReadDir(project); len(entries) != 0 {
		t.Errorf("unexpected files written: %v", entries)
	}
}
