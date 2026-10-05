package agentscompose

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleAgent() ComposedAgent {
	return ComposedAgent{
		ID:          "backend",
		Description: `Implements "server" code, C:\x`,
		Prompt:      "# Backend\n\nYou write Go.\nUse \"quotes\" and a \\ backslash.\n\n\n",
		Tools:       []string{"Read", "Edit"},
		Model:       "",
	}
}

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits and symlinks; skipping on Windows")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- golden bytes --------------------------------------------------------------

func TestEmitCodexTOML_Golden(t *testing.T) {
	got := string(EmitCodexTOML(sampleAgent()))
	want := "# yakos-generated: rewritten on every dispatch. Delete this line to keep your edits.\n" +
		"name = \"backend\"\n" +
		"description = \"Implements \\\"server\\\" code, C:\\\\x\"\n" +
		"developer_instructions = \"\"\"\n" +
		"# Backend\n\nYou write Go.\nUse \"quotes\" and a \\\\ backslash.\n" +
		"\"\"\"\n"
	if got != want {
		t.Errorf("codex TOML mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitCodexTOML_ModelEmptyDescriptionAndTripleQuotes(t *testing.T) {
	got := string(EmitCodexTOML(ComposedAgent{ID: "x", Model: "gpt-5", Prompt: `a """ b`}))
	want := "# yakos-generated: rewritten on every dispatch. Delete this line to keep your edits.\n" +
		"name = \"x\"\n" +
		"description = \"Agent: x\"\n" +
		"model = \"gpt-5\"\n" +
		"developer_instructions = \"\"\"\n" +
		"a \\\"\\\"\\\" b\n" +
		"\"\"\"\n"
	if got != want {
		t.Errorf("mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitCodexTOML_DescriptionStaysOnOneLine(t *testing.T) {
	got := string(EmitCodexTOML(ComposedAgent{ID: "x", Description: "line one\nline two\r\nline three\rend"}))
	if !strings.Contains(got, "description = \"line one line two line three end\"\n") {
		t.Errorf("description must be collapsed to one line, got %q", got)
	}
}

func TestEmitAgySkill_Golden(t *testing.T) {
	a := sampleAgent()
	a.Model = "gemini-3.1-pro"
	got := string(EmitAgySkill(a))
	want := "---\n" +
		"name: yakos-backend\n" +
		"description: \"Implements \\\"server\\\" code, C:\\\\x\"\n" +
		"model: \"gemini-3.1-pro\"\n" +
		"tools: [\"Read\", \"Edit\"]\n" +
		"---\n" +
		"<!-- yakos-generated: rewritten on every dispatch. Delete this line to keep your edits. -->\n" +
		"\n" +
		"# Backend\n\nYou write Go.\nUse \"quotes\" and a \\ backslash.\n"
	if got != want {
		t.Errorf("agy skill mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestEmitAgySkill_MinimalAgent(t *testing.T) {
	got := string(EmitAgySkill(ComposedAgent{ID: "x"}))
	want := "---\nname: yakos-x\ndescription: \"Agent: x\"\n---\n" +
		"<!-- yakos-generated: rewritten on every dispatch. Delete this line to keep your edits. -->\n\n\n"
	if got != want {
		t.Errorf("mismatch\n got: %q\nwant: %q", got, want)
	}
}

// ---- codex materializer -------------------------------------------------------

func TestMaterializeCodexAgent_WritesPublicFileUnderCodexAgents(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(work, ".codex", "agents", "yakos-backend.toml")
	if res.Path != want || !res.Written || res.Skipped != "" {
		t.Fatalf("result = %+v, want a fresh write of %s", res, want)
	}
	if got := mustRead(t, want); got != string(EmitCodexTOML(sampleAgent())) {
		t.Errorf("file content differs from EmitCodexTOML:\n%s", got)
	}
	fi, _ := os.Stat(want)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 0644", fi.Mode().Perm())
	}
	dirFi, _ := os.Stat(filepath.Dir(want))
	if dirFi.Mode().Perm() != 0o755 {
		t.Errorf("dir mode = %o, want 0755", dirFi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(want), ".yakos-tmp-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestMaterializeCodexAgent_UnchangedFileIsNotRewritten(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	if _, err := MaterializeCodexAgent(work, sampleAgent()); err != nil {
		t.Fatal(err)
	}
	path := CodexAgentPath(work, "backend")
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	if res.Written || res.Skipped != SkipUnchanged {
		t.Errorf("result = %+v, want skipped as unchanged", res)
	}
	fi, _ := os.Stat(path)
	if !fi.ModTime().Equal(old) {
		t.Errorf("mtime moved to %v: an unchanged file must not be rewritten", fi.ModTime())
	}

	changed := sampleAgent()
	changed.Prompt += "one more line\n"
	res, err = MaterializeCodexAgent(work, changed)
	if err != nil || !res.Written {
		t.Fatalf("a changed prompt must rewrite the file: %+v, %v", res, err)
	}
	if !strings.Contains(mustRead(t, path), "one more line") {
		t.Error("rewritten file lacks the new prompt")
	}
}

func TestMaterializeCodexAgent_RefusesFileWithoutMarker(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	path := CodexAgentPath(work, "backend")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "name = \"my-own\"\ndescription = \"hand written\"\ndeveloper_instructions = \"\"\"\nkeep me\n\"\"\"\n"
	if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	if res.Written || res.Skipped != SkipNotYakosManaged {
		t.Errorf("result = %+v, want skipped as not yakos-generated", res)
	}
	if got := mustRead(t, path); got != mine {
		t.Errorf("an operator's file was modified:\n%s", got)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %o", fi.Mode().Perm())
	}
}

func TestMaterializeCodexAgent_UpgradesLegacyFileInPlace(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	path := CodexAgentPath(work, "backend")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "name = \"backend\"\ndescription = \"old\"\ndeveloper_instructions = \"\"\"\nold body\n\"\"\"\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil || !res.Written {
		t.Fatalf("a legacy yakOS-generated file must be upgraded: %+v, %v", res, err)
	}
	if !strings.HasPrefix(mustRead(t, path), codexMarkerLine+"\n") {
		t.Error("the upgraded file must carry the marker")
	}
}

func TestMaterializeCodexAgent_MarkerPastTheScanWindowDoesNotCount(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	path := CodexAgentPath(work, "backend")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("# filler\n", markerScanLines) + "# yakos-generated: too late\nname = \"other\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := MaterializeCodexAgent(work, sampleAgent())
	if err != nil || res.Written || res.Skipped != SkipNotYakosManaged {
		t.Fatalf("result = %+v, %v; the marker must be in the first %d lines", res, err, markerScanLines)
	}
}

func TestMaterializeCodexAgent_RefusesSymlinks(t *testing.T) {
	skipWindows(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "victim.toml")
	if err := os.WriteFile(target, []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, setup := range map[string]func(work string){
		"symlinked .codex": func(work string) {
			must(t, os.Symlink(outside, filepath.Join(work, ".codex")))
		},
		"symlinked .codex/agents": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".codex"), 0o755))
			must(t, os.Symlink(outside, filepath.Join(work, ".codex", "agents")))
		},
		"symlinked target file": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".codex", "agents"), 0o755))
			must(t, os.Symlink(target, CodexAgentPath(work, "backend")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			setup(work)
			res, err := MaterializeCodexAgent(work, sampleAgent())
			if err == nil {
				t.Fatalf("expected a refusal, got %+v", res)
			}
			if mustRead(t, target) != "precious\n" {
				t.Fatal("a file outside the project was modified through a symlink")
			}
			if left, _ := filepath.Glob(filepath.Join(outside, "*")); len(left) != 1 {
				t.Errorf("files appeared outside the project: %v", left)
			}
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMaterialize_RejectsUnsafeIDsAndRelativeDirs(t *testing.T) {
	work := t.TempDir()
	for _, id := range []string{"", "../escape", "a/b", `a\b`, "-leading-dash", ".hidden", "has space", "semi;colon", strings.Repeat("a", 101), "nul\x00byte"} {
		a := sampleAgent()
		a.ID = id
		if _, err := MaterializeCodexAgent(work, a); err == nil {
			t.Errorf("codex: id %q must be refused", id)
		}
		if _, err := MaterializeAgyAgent(work, a); err == nil {
			t.Errorf("agy: id %q must be refused", id)
		}
	}
	for _, id := range []string{"backend", "general-codex", "a.b_c-1", "X9"} {
		a := sampleAgent()
		a.ID = id
		if _, err := MaterializeCodexAgent(work, a); err != nil {
			t.Errorf("codex: id %q must be accepted: %v", id, err)
		}
	}
	if _, err := MaterializeCodexAgent("relative/dir", sampleAgent()); err == nil {
		t.Error("a relative working directory must be refused")
	}
	if _, err := MaterializeCodexAgent("", sampleAgent()); err == nil {
		t.Error("an empty working directory must be refused")
	}
	if entries, _ := os.ReadDir(work); len(entries) != 1 { // only .codex from the accepted ids
		t.Errorf("unexpected entries after refusals: %v", entries)
	}
}

func TestMaterializeRuntimeAgent_Dispatches(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	if res, err := MaterializeRuntimeAgent("codex", work, sampleAgent()); err != nil || res.Path != CodexAgentPath(work, "backend") {
		t.Errorf("codex: %+v, %v", res, err)
	}
	if res, err := MaterializeRuntimeAgent("agy", work, sampleAgent()); err != nil || res.Path != AgySkillPath(work, "backend") {
		t.Errorf("agy: %+v, %v", res, err)
	}
	for _, rt := range []string{"claude", "gemini", "", "nope"} {
		if _, err := MaterializeRuntimeAgent(rt, work, sampleAgent()); !errors.Is(err, ErrUnsupportedRuntime) {
			t.Errorf("%q: err = %v, want ErrUnsupportedRuntime", rt, err)
		}
	}
}

func TestMaterializeCodexAgent_ConcurrentDispatchesOfOneAgent(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := MaterializeCodexAgent(work, sampleAgent()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent materialize: %v", err)
	}
	if got := mustRead(t, CodexAgentPath(work, "backend")); got != string(EmitCodexTOML(sampleAgent())) {
		t.Error("file is not the complete expected content after concurrent writes")
	}
	if left, _ := filepath.Glob(filepath.Join(work, ".codex", "agents", ".yakos-tmp-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// ---- agy materializer ----------------------------------------------------------

func TestMaterializeAgyAgent_WritesSkillDirectory(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	res, err := MaterializeAgyAgent(work, sampleAgent())
	if err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(work, ".agents", "skills", "yakos-backend", "SKILL.md")
	if res.Path != skill || !res.Written {
		t.Fatalf("result = %+v, want a fresh write of %s", res, skill)
	}
	if got := mustRead(t, skill); got != string(EmitAgySkill(sampleAgent())) {
		t.Errorf("SKILL.md differs from EmitAgySkill:\n%s", got)
	}
	gi := filepath.Join(filepath.Dir(skill), ".gitignore")
	if got := mustRead(t, gi); got != "*\n" {
		t.Errorf(".gitignore = %q", got)
	}
	for _, p := range []string{skill, gi} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o644 {
			t.Errorf("%s mode = %o, want 0644", p, fi.Mode().Perm())
		}
	}
	// Idempotent and content-stable.
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	must(t, os.Chtimes(skill, old, old))
	res, err = MaterializeAgyAgent(work, sampleAgent())
	if err != nil || res.Written || res.Skipped != SkipUnchanged {
		t.Fatalf("second call = %+v, %v; want unchanged", res, err)
	}
	if fi, _ := os.Stat(skill); !fi.ModTime().Equal(old) {
		t.Error("an unchanged SKILL.md was rewritten")
	}
}

func TestMaterializeAgyAgent_LeavesOperatorSkillAlone(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	skill := AgySkillPath(work, "backend")
	must(t, os.MkdirAll(filepath.Dir(skill), 0o755))
	mine := "---\nname: yakos-backend\ndescription: mine\n---\nhand written\n"
	must(t, os.WriteFile(skill, []byte(mine), 0o644))
	res, err := MaterializeAgyAgent(work, sampleAgent())
	if err != nil || res.Written || res.Skipped != SkipNotYakosManaged {
		t.Fatalf("result = %+v, %v; want skipped as not yakos-generated", res, err)
	}
	if mustRead(t, skill) != mine {
		t.Error("an operator's SKILL.md was modified")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(skill), ".gitignore")); !os.IsNotExist(err) {
		t.Error("a .gitignore must not be written into a directory the operator owns")
	}
}

func TestMaterializeAgyAgent_RefusesSymlinks(t *testing.T) {
	skipWindows(t)
	outside := t.TempDir()
	for name, setup := range map[string]func(work string){
		"symlinked .agents": func(work string) {
			must(t, os.Symlink(outside, filepath.Join(work, ".agents")))
		},
		"symlinked skills dir": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".agents"), 0o755))
			must(t, os.Symlink(outside, filepath.Join(work, ".agents", "skills")))
		},
		"symlinked skill dir": func(work string) {
			must(t, os.MkdirAll(filepath.Join(work, ".agents", "skills"), 0o755))
			must(t, os.Symlink(outside, AgySkillDir(work, "backend")))
		},
		"symlinked SKILL.md": func(work string) {
			must(t, os.MkdirAll(AgySkillDir(work, "backend"), 0o755))
			must(t, os.WriteFile(filepath.Join(outside, "x"), []byte("precious"), 0o644))
			must(t, os.Symlink(filepath.Join(outside, "x"), AgySkillPath(work, "backend")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			work := t.TempDir()
			setup(work)
			if res, err := MaterializeAgyAgent(work, sampleAgent()); err == nil {
				t.Fatalf("expected a refusal, got %+v", res)
			}
			if entries, _ := os.ReadDir(outside); len(entries) > 1 {
				t.Errorf("files appeared outside the project: %v", entries)
			}
		})
	}
}

// TestMaterializeAgyAgent_GeneratedFilesAreInvisibleToGit proves the nested
// .gitignore does its job without the project's own .gitignore.
func TestMaterializeAgyAgent_GeneratedFilesAreInvisibleToGit(t *testing.T) {
	skipWindows(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	if _, err := MaterializeAgyAgent(work, sampleAgent()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run("status", "--porcelain", "-uall")); got != "" {
		t.Errorf("generated skill files are visible to git: %q", got)
	}
}

func TestMaterializeAgyAgent_NormalisesGitignore(t *testing.T) {
	skipWindows(t)
	work := t.TempDir()
	_, err := MaterializeAgyAgent(work, sampleAgent())
	must(t, err)
	gi := filepath.Join(AgySkillDir(work, "backend"), ".gitignore")
	must(t, os.WriteFile(gi, []byte("something else\n"), 0o644))
	if _, err := MaterializeAgyAgent(work, sampleAgent()); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, gi) != "*\n" {
		t.Error(".gitignore inside the generated skill directory must be restored")
	}
}

func TestHasMarker(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"first line", "# yakos-generated: x\nname", true},
		{"line 12", strings.Repeat("a\n", 11) + "yakos-generated: x\n", true},
		{"line 13", strings.Repeat("a\n", 12) + "yakos-generated: x\n", false},
		{"absent", "name = \"x\"\n", false},
		{"empty", "", false},
		{"similar", "# yakos generated\n", false},
	} {
		if got := hasMarker([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: hasMarker = %v, want %v", tc.name, got, tc.want)
		}
	}
}
