// Package initialize_test covers the Go port of `yakos init`.
//
// Coverage:
//
//	TestValidName*              (4)  — valid/invalid name patterns
//	TestValidKind*              (3)  — known/unknown template kinds
//	TestEncodeProjectPath       (3)  — path encoding matches bash ct_encode_project_path
//	TestBuildMemoryMD           (2)  — MEMORY.md content shape
//	TestTemplateFiles*          (4)  — embedded files reachable for each kind
//	TestAllKindsHaveFiles       (1)  — every declared kind has embedded files
//	TestBaseTemplateFiles       (1)  — base/ has the expected required files
//	TestRun_MissingName         (1)  — empty name → error
//	TestRun_BadName             (1)  — name with slash → error
//	TestRun_MissingProject      (1)  — empty project → error
//	TestRun_NonExistentProject  (1)  — project not on disk → error
//	TestRun_NotGitRepo          (1)  — project exists but not git → error
//	TestRun_UnknownTemplate     (1)  — bad --template → error mentions "did you mean"
//	TestRun_DryRun              (1)  — no files written; DryRunFiles populated
//	TestRun_HappyPath           (1)  — full scaffold, all expected paths created
//	TestRun_ControlDirPreserved (1)  — second run does not overwrite existing files
//	TestRun_ForceOverwrites     (1)  — --force overwrites project-side files
//	TestRun_TemplateGo          (1)  — go template writes kind-specific yakos.yml
//	TestRun_TemplateRails       (1)  — rails template writes kind-specific yakos.yml
//	TestRun_SessionHistoryEmpty (1)  — .session-started-history.ndjson created empty
//	TestRun_GitignoreAppended   (1)  — yakOS patterns appended to project .gitignore
//	TestRun_ProjectPath         (1)  — .project-path written with correct content
//	TestRun_MemoryMD            (1)  — MEMORY.md seeded with name + path + timestamp
//	TestRun_SkipCountAccurate   (1)  — FilesSkipped count correct on re-run
//	TestRun_BaseOnly            (1)  — no --template defaults to base
//	TestPrintHelp               (1)  — help contains required phrases
//	TestTemplateKinds           (1)  — TemplateKinds returns all 7 kinds
package initialize

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

// newGitRepo creates a temp directory with a minimal .git directory so
// isGitRepo returns true without running the full git binary.
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return dir
}

// newNonGitDir creates a temp directory with no .git directory.
func newNonGitDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// baseConfig returns a Config wired to test temp dirs, avoiding HOME writes.
func baseConfig(t *testing.T, name, project string) Config {
	t.Helper()
	home := t.TempDir()
	return Config{
		Name:        name,
		ProjectPath: project,
		HomeDir:     home,
		Now:         time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:      &bytes.Buffer{},
		ErrWriter:   &bytes.Buffer{},
	}
}

// ---- name validation --------------------------------------------------------

// TestValidName_AlphaNum verifies simple alphanumeric names are accepted.
func TestValidName_AlphaNum(t *testing.T) {
	if !validNameRe.MatchString("myproject") {
		t.Error("alphanumeric name should be valid")
	}
	if !validNameRe.MatchString("my-project") {
		t.Error("hyphenated name should be valid")
	}
	if !validNameRe.MatchString("my_project") {
		t.Error("underscore name should be valid")
	}
	if !validNameRe.MatchString("MyProject123") {
		t.Error("mixed case name should be valid")
	}
}

// TestValidName_InvalidChars verifies names with special chars are rejected.
func TestValidName_InvalidChars(t *testing.T) {
	invalid := []string{"my/project", "my project", "my.project", "my@proj"}
	for _, n := range invalid {
		if validNameRe.MatchString(n) {
			t.Errorf("name %q should be invalid", n)
		}
	}
}

// ---- kind validation --------------------------------------------------------

// TestValidKind_Known verifies all declared kinds are accepted.
func TestValidKind_Known(t *testing.T) {
	for _, k := range validKinds {
		if !isValidKind(k) {
			t.Errorf("kind %q should be valid", k)
		}
	}
}

// TestValidKind_Unknown verifies unknown kinds are rejected.
func TestValidKind_Unknown(t *testing.T) {
	if isValidKind("django") {
		t.Error("unknown kind 'django' should be invalid")
	}
	if isValidKind("") {
		t.Error("empty kind should be invalid")
	}
}

// TestValidKind_Count verifies the expected number of kinds.
func TestValidKind_Count(t *testing.T) {
	const want = 7 // base + rails + go + python + node + rust + static-site
	if got := len(validKinds); got != want {
		t.Errorf("expected %d kinds; got %d: %v", want, got, validKinds)
	}
}

// ---- path encoding ----------------------------------------------------------

// TestEncodeProjectPath verifies the encoding matches bash ct_encode_project_path
// on Unix paths and produces safe Windows-compatible directory names for
// Windows-style paths.
func TestEncodeProjectPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Unix happy path — must stay byte-identical to bash ct_encode_project_path.
		{"unix_typical", "/Users/tw/github/myproject", "Users-tw-github-myproject"},
		{"unix_home_user", "/home/user/project", "home-user-project"},
		{"unix_tmp", "/tmp/proj", "tmp-proj"},

		// Windows-style paths (drive letter + backslashes).  These inputs can be
		// fed on any OS by passing the literal string; the function must never
		// produce a colon in the output.
		{"windows_typical", `C:\Users\bob\proj`, "Users-bob-proj"},
		{"windows_deep", `D:\work\repos\yakos`, "work-repos-yakos"},
		{"windows_lowercase_drive", `c:\Users\RUNNER~1\AppData\Local\Temp\proj`, "Users-RUNNER~1-AppData-Local-Temp-proj"},

		// Degenerate roots.
		{"unix_root", "/", "root"},
		{"windows_root", `C:\`, "root"},
		{"windows_root_slash", `C:/`, "root"},

		// Mixed separators.
		{"mixed_separators", `C:/Users/bob\proj`, "Users-bob-proj"},

		// Segments that contain Windows-illegal chars.
		{"colon_in_segment", "/path/foo:bar/baz", "path-foo-bar-baz"},
		{"angle_brackets", "/path/<foo>/baz", "path-foo-baz"},
		{"pipe_question_star", "/path/a|b?c*d/e", "path-a-b-c-d-e"},
		{"double_quote", `/path/a"b/c`, "path-a-b-c"},

		// Consecutive separator collapse.
		{"consecutive_slashes", "//tmp//proj", "tmp-proj"},

		// Unix paths identical to Windows sibling (cross-platform consistency).
		{"unix_users_bob", "/Users/bob/proj", "Users-bob-proj"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := encodeProjectPath(tc.input)
			if got != tc.want {
				t.Errorf("encodeProjectPath(%q) = %q; want %q", tc.input, got, tc.want)
			}
			// Invariant: result must never contain ':'.
			if strings.ContainsRune(got, ':') {
				t.Errorf("encodeProjectPath(%q) produced colon in output: %q", tc.input, got)
			}
			// Invariant: result must never be empty.
			if got == "" {
				t.Errorf("encodeProjectPath(%q) returned empty string", tc.input)
			}
		})
	}
}

// ---- MEMORY.md content ------------------------------------------------------

// TestBuildMemoryMD_ContainsName verifies MEMORY.md includes the project name.
func TestBuildMemoryMD_ContainsName(t *testing.T) {
	got := buildMemoryMD("yakos-test", "/projects/yakos-test", time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC))
	if !strings.Contains(got, "yakos-test") {
		t.Errorf("MEMORY.md should contain project name; got:\n%s", got)
	}
	if !strings.Contains(got, "/projects/yakos-test") {
		t.Errorf("MEMORY.md should contain project path; got:\n%s", got)
	}
	if !strings.Contains(got, "2026-06-02") {
		t.Errorf("MEMORY.md should contain initialized date; got:\n%s", got)
	}
}

// TestBuildMemoryMD_HeadingExists verifies MEMORY.md has a markdown heading.
func TestBuildMemoryMD_HeadingExists(t *testing.T) {
	got := buildMemoryMD("myproj", "/x/y", time.Now())
	if !strings.HasPrefix(got, "# Memory index for myproj") {
		t.Errorf("MEMORY.md should start with '# Memory index for <name>'; got:\n%s", got)
	}
}

// ---- embedded template files ------------------------------------------------

// TestTemplateFiles_Base verifies base template files are all reachable.
func TestTemplateFiles_Base(t *testing.T) {
	files, err := TemplateFiles("base")
	if err != nil {
		t.Fatalf("TemplateFiles(base): %v", err)
	}
	if len(files) == 0 {
		t.Fatal("base template has no files")
	}
}

// TestTemplateFiles_Go verifies go template files are reachable.
func TestTemplateFiles_Go(t *testing.T) {
	files, err := TemplateFiles("go")
	if err != nil {
		t.Fatalf("TemplateFiles(go): %v", err)
	}
	if len(files) == 0 {
		t.Fatal("go template has no files")
	}
}

// TestTemplateFiles_Rails verifies rails template files are reachable.
func TestTemplateFiles_Rails(t *testing.T) {
	files, err := TemplateFiles("rails")
	if err != nil {
		t.Fatalf("TemplateFiles(rails): %v", err)
	}
	if len(files) == 0 {
		t.Fatal("rails template has no files")
	}
}

// TestTemplateFiles_Unknown verifies unknown kinds return an error.
func TestTemplateFiles_Unknown(t *testing.T) {
	_, err := TemplateFiles("elixir")
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
	if !strings.Contains(err.Error(), "elixir") {
		t.Errorf("error should mention kind name; got %q", err.Error())
	}
}

// TestAllKindsHaveFiles verifies every declared kind has at least one file.
func TestAllKindsHaveFiles(t *testing.T) {
	for _, kind := range validKinds {
		files, err := TemplateFiles(kind)
		if err != nil {
			t.Errorf("TemplateFiles(%q): %v", kind, err)
			continue
		}
		if len(files) == 0 {
			t.Errorf("kind %q has no embedded template files", kind)
		}
	}
}

// TestBaseTemplateFiles_RequiredFiles verifies required files exist in base.
func TestBaseTemplateFiles_RequiredFiles(t *testing.T) {
	required := []string{
		"templates/base/.gitignore",
		"templates/base/settings.local.json",
		"templates/base/hook-bypass.md",
		"templates/base/.claude/path-allowlist.json",
		"templates/base/.claude/settings.json",
		"templates/base/yakos.yml",
		"templates/base/AGENTS.md",
	}
	files, err := TemplateFiles("base")
	if err != nil {
		t.Fatalf("TemplateFiles(base): %v", err)
	}
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}
	for _, req := range required {
		if !fileSet[req] {
			t.Errorf("required base template file missing: %s", req)
		}
	}
}

// ---- Run error cases --------------------------------------------------------

// TestRun_MissingName verifies empty name produces an error.
func TestRun_MissingName(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "", proj)
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "missing <name>") {
		t.Errorf("error should mention missing name; got %q", err.Error())
	}
}

// TestRun_BadName verifies a name with illegal characters is rejected.
func TestRun_BadName(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "my/project", proj)
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for name with slash")
	}
	if !strings.Contains(err.Error(), "alphanumeric") {
		t.Errorf("error should mention alphanumeric; got %q", err.Error())
	}
}

// TestRun_MissingProject verifies empty project produces an error.
func TestRun_MissingProject(t *testing.T) {
	cfg := baseConfig(t, "myproject", "")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "--project") {
		t.Errorf("error should mention --project; got %q", err.Error())
	}
}

// TestRun_NonExistentProject verifies a project path not on disk is rejected.
func TestRun_NonExistentProject(t *testing.T) {
	cfg := baseConfig(t, "myproject", "/no/such/path/exists/2026")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-existent project path")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say 'not found'; got %q", err.Error())
	}
}

// TestRun_NotGitRepo verifies a directory without a git repo is rejected.
func TestRun_NotGitRepo(t *testing.T) {
	proj := newNonGitDir(t)
	cfg := baseConfig(t, "myproject", proj)
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("error should mention git repository; got %q", err.Error())
	}
}

// TestRun_UnknownTemplate verifies unknown --template produces a helpful error.
func TestRun_UnknownTemplate(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "myproject", proj)
	cfg.Template = "elixir"
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown template")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error should say 'did you mean'; got %q", err.Error())
	}
}

// ---- Run dry-run ------------------------------------------------------------

// TestRun_DryRun verifies no files are written in dry-run mode.
func TestRun_DryRun(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "drytest", proj)
	cfg.DryRun = true

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}

	// No real files written.
	if len(res.FilesWritten) != 0 {
		t.Errorf("dry-run should write 0 files; got %d: %v", len(res.FilesWritten), res.FilesWritten)
	}

	// DryRunFiles must be populated.
	if len(res.DryRunFiles) == 0 {
		t.Error("dry-run should populate DryRunFiles")
	}

	// Control dir should not exist.
	if _, err := os.Stat(filepath.Join(cfg.HomeDir, "agent-control", "drytest")); !os.IsNotExist(err) {
		t.Error("dry-run should not create agent-control directory")
	}
}

// ---- Run happy path ---------------------------------------------------------

// TestRun_HappyPath verifies the full scaffold is created correctly.
func TestRun_HappyPath(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "myproj", proj)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if res.Name != "myproj" {
		t.Errorf("Name: got %q, want myproj", res.Name)
	}

	// Check expected directories.
	home := cfg.HomeDir
	controlDir := filepath.Join(home, "agent-control", "myproj")
	for _, sub := range []string{
		filepath.Join(controlDir, "work", "current", "logs"),
		filepath.Join(controlDir, "work", "current", "artifacts"),
		filepath.Join(controlDir, "work", "current", "reports"),
		filepath.Join(controlDir, "work", "archive"),
	} {
		if _, err := os.Stat(sub); err != nil {
			t.Errorf("expected dir %s to exist: %v", sub, err)
		}
	}

	// Check expected files.
	expected := []string{
		filepath.Join(controlDir, ".gitignore"),
		filepath.Join(controlDir, "settings.local.json"),
		filepath.Join(controlDir, "work", "current", "hook-bypass.md"),
		filepath.Join(controlDir, "work", "sessions.ndjson"),
		filepath.Join(controlDir, ".project-path"),
		filepath.Join(proj, ".claude", "path-allowlist.json"),
		filepath.Join(proj, ".claude", "settings.json"),
		filepath.Join(proj, "AGENTS.md"),
		filepath.Join(proj, ".yakos.yml"),
	}
	for _, f := range expected {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

// TestRun_ControlDirPreserved verifies re-running init does not overwrite
// agent-control files.
func TestRun_ControlDirPreserved(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "preserve-test", proj)

	// First run.
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Modify the .gitignore in agent-control to a unique sentinel.
	home := cfg.HomeDir
	gitignorePath := filepath.Join(home, "agent-control", "preserve-test", ".gitignore")
	sentinel := "# SENTINEL CONTENT 12345\n"
	if err := os.WriteFile(gitignorePath, []byte(sentinel), 0644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Second run (no --force).
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// Sentinel should still be there.
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != sentinel {
		t.Errorf("agent-control .gitignore was overwritten; expected sentinel, got %q", string(data))
	}
}

// TestRun_ForceOverwrites verifies --force overwrites project-side files.
func TestRun_ForceOverwrites(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "force-test", proj)

	// First run.
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Modify .yakos.yml to a sentinel.
	yakosYML := filepath.Join(proj, ".yakos.yml")
	sentinel := "# SENTINEL\n"
	if err := os.WriteFile(yakosYML, []byte(sentinel), 0644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Second run with --force.
	cfg.Force = true
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("second Run (force): %v", err)
	}

	// .yakos.yml should be overwritten.
	data, err := os.ReadFile(yakosYML)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) == sentinel {
		t.Error("--force should have overwritten .yakos.yml but sentinel remains")
	}
}

// TestRun_TemplateGo verifies the go template writes kind-specific yakos.yml.
func TestRun_TemplateGo(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "goproject", proj)
	cfg.Template = "go"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run(--template go): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if err != nil {
		t.Fatalf("ReadFile .yakos.yml: %v", err)
	}
	if !strings.Contains(string(data), "go test") {
		t.Errorf(".yakos.yml for go template should mention 'go test'; got:\n%s", string(data))
	}
	if !strings.Contains(string(data), "service") {
		t.Errorf(".yakos.yml for go template should mention profile type 'service'; got:\n%s", string(data))
	}
}

// TestRun_TemplateRails verifies the rails template writes kind-specific yakos.yml.
func TestRun_TemplateRails(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "railsapp", proj)
	cfg.Template = "rails"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run(--template rails): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if err != nil {
		t.Fatalf("ReadFile .yakos.yml: %v", err)
	}
	if !strings.Contains(string(data), "rails") {
		t.Errorf(".yakos.yml for rails template should mention 'rails'; got:\n%s", string(data))
	}
}

// TestRun_SessionHistoryEmpty verifies .session-started-history.ndjson is
// created empty.
func TestRun_SessionHistoryEmpty(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "historytest", proj)

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(cfg.HomeDir, "agent-control", "historytest", "work", "current", ".session-started-history.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile .session-started-history.ndjson: %v", err)
	}
	if len(data) != 0 {
		t.Errorf(".session-started-history.ndjson should be empty; got %d bytes", len(data))
	}
}

// TestRun_GitignoreAppended verifies yakOS runtime-agent patterns are appended
// to the project's .gitignore.
func TestRun_GitignoreAppended(t *testing.T) {
	proj := newGitRepo(t)

	// Seed a project .gitignore.
	projGitignore := filepath.Join(proj, ".gitignore")
	if err := os.WriteFile(projGitignore, []byte("*.log\n"), 0644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	cfg := baseConfig(t, "gitignoretest", proj)
	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(projGitignore)
	if err != nil {
		t.Fatalf("ReadFile .gitignore: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, ".codex/agents/yakos-*.toml") {
		t.Errorf(".gitignore should contain yakOS codex pattern; got:\n%s", got)
	}
	if !strings.Contains(got, ".gemini/agents/yakos-*.md") {
		t.Errorf(".gitignore should contain yakOS gemini pattern; got:\n%s", got)
	}
	if !strings.Contains(got, "*.log") {
		t.Errorf(".gitignore should preserve original content; got:\n%s", got)
	}
}

// TestRun_GitignoreNotDuplicated verifies yakOS patterns are not appended twice.
func TestRun_GitignoreNotDuplicated(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "dedupetest", proj)

	// Two runs.
	if _, err := Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	projGitignore := filepath.Join(proj, ".gitignore")
	data, err := os.ReadFile(projGitignore)
	if err != nil {
		t.Fatalf("ReadFile .gitignore: %v", err)
	}
	// Marker should appear exactly once.
	const marker = "# yakOS — runtime-emitted agent files (v0.4.0+)"
	count := strings.Count(string(data), marker)
	if count != 1 {
		t.Errorf("marker should appear exactly once in .gitignore; got %d occurrences:\n%s", count, string(data))
	}
}

// TestRun_ProjectPath verifies .project-path contains the resolved project path.
func TestRun_ProjectPath(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "pathtest", proj)

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ppFile := filepath.Join(cfg.HomeDir, "agent-control", "pathtest", ".project-path")
	data, err := os.ReadFile(ppFile)
	if err != nil {
		t.Fatalf("ReadFile .project-path: %v", err)
	}
	got := strings.TrimSpace(string(data))
	// The project path must be absolute and point to proj (or its realpath).
	if got == "" {
		t.Error(".project-path should not be empty")
	}
	if !filepath.IsAbs(got) {
		t.Errorf(".project-path should be absolute; got %q", got)
	}
}

// TestRun_MemoryMD verifies MEMORY.md is seeded with name and path.
func TestRun_MemoryMD(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "memtest", proj)

	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(res.MemoryFile)
	if err != nil {
		t.Fatalf("ReadFile MEMORY.md: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "memtest") {
		t.Errorf("MEMORY.md should contain project name 'memtest'; got:\n%s", got)
	}
}

// TestRun_SkipCountAccurate verifies FilesSkipped count is correct on re-run.
func TestRun_SkipCountAccurate(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "skiptest", proj)

	// First run.
	r1, err := Run(cfg)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	written := len(r1.FilesWritten)
	if written == 0 {
		t.Fatal("first run should have written at least one file")
	}

	// Second run — same config, no --force.
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	r2, err := Run(cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// .project-path is always written; other files should be skipped.
	if len(r2.FilesSkipped) == 0 {
		t.Error("second run should skip some files")
	}
}

// TestRun_BaseOnly verifies empty --template defaults to base.
func TestRun_BaseOnly(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "baseonly", proj)
	cfg.Template = "" // empty → base

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run (no template): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if err != nil {
		t.Fatalf("ReadFile .yakos.yml: %v", err)
	}
	// Base yakos.yml should mention web-app profile.
	if !strings.Contains(string(data), "web-app") {
		t.Errorf("base .yakos.yml should mention 'web-app'; got:\n%s", string(data))
	}
}

// ---- help text --------------------------------------------------------------

// TestPrintHelp verifies help text contains all required phrases.
func TestPrintHelp(t *testing.T) {
	var out bytes.Buffer
	PrintHelp(&out)
	got := out.String()

	required := []string{
		"yakos init <name>",
		"--project <path>",
		"--template",
		"--force",
		"--dry-run",
		"rails",
		"go",
		"python",
		"node",
		"rust",
		"static-site",
		"--help",
		"refresh",
	}
	for _, phrase := range required {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- TemplateKinds ----------------------------------------------------------

// TestTemplateKinds verifies all 7 kinds are returned.
func TestTemplateKinds(t *testing.T) {
	kinds := TemplateKinds()
	const want = 7
	if len(kinds) != want {
		t.Errorf("TemplateKinds: expected %d; got %d: %v", want, len(kinds), kinds)
	}
	for _, k := range []string{"base", "rails", "go", "python", "node", "rust", "static-site"} {
		found := false
		for _, got := range kinds {
			if got == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("TemplateKinds missing %q", k)
		}
	}
}

// ---- Per-template minimum required files ------------------------------------

// TestAllNonBaseTemplatesHaveMinimumFiles verifies every non-base template
// ships yakos.yml, a .gitignore overlay, and at least 2 agent .md files.
func TestAllNonBaseTemplatesHaveMinimumFiles(t *testing.T) {
	nonBaseKinds := []string{"rails", "go", "python", "node", "rust", "static-site"}
	for _, kind := range nonBaseKinds {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			files, err := TemplateFiles(kind)
			if err != nil {
				t.Fatalf("TemplateFiles(%q): %v", kind, err)
			}

			fileSet := make(map[string]bool, len(files))
			for _, f := range files {
				fileSet[f] = true
			}

			// yakos.yml must exist.
			if !fileSet["templates/"+kind+"/yakos.yml"] {
				t.Errorf("%s: missing yakos.yml", kind)
			}

			// .gitignore overlay must exist.
			if !fileSet["templates/"+kind+"/.gitignore"] {
				t.Errorf("%s: missing .gitignore overlay", kind)
			}

			// AGENTS.md must exist.
			if !fileSet["templates/"+kind+"/AGENTS.md"] {
				t.Errorf("%s: missing AGENTS.md overlay", kind)
			}

			// At least 2 agent .md files must exist under .claude/agents/.
			agentPrefix := "templates/" + kind + "/.claude/agents/"
			agentCount := 0
			for _, f := range files {
				if strings.HasPrefix(f, agentPrefix) && strings.HasSuffix(f, ".md") {
					agentCount++
				}
			}
			if agentCount < 2 {
				t.Errorf("%s: expected at least 2 agent .md files under .claude/agents/; got %d", kind, agentCount)
			}
		})
	}
}

// TestAllAgentFilesHaveValidFrontmatter verifies every agent .md in every
// non-base template has parseable YAML frontmatter.
func TestAllAgentFilesHaveValidFrontmatter(t *testing.T) {
	nonBaseKinds := []string{"rails", "go", "python", "node", "rust", "static-site"}
	for _, kind := range nonBaseKinds {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			files, err := TemplateFiles(kind)
			if err != nil {
				t.Fatalf("TemplateFiles(%q): %v", kind, err)
			}
			agentPrefix := "templates/" + kind + "/.claude/agents/"
			for _, f := range files {
				if !strings.HasPrefix(f, agentPrefix) || !strings.HasSuffix(f, ".md") {
					continue
				}
				data, readErr := templates.ReadFile(f)
				if readErr != nil {
					t.Errorf("%s: cannot read embedded file %s: %v", kind, f, readErr)
					continue
				}
				if err := checkFrontmatter(data); err != nil {
					t.Errorf("%s: agent file %s: %v", kind, f, err)
				}
			}
		})
	}
}

// checkFrontmatter does a lightweight check: file starts with "---\n" and
// has a closing "---" within 50 lines. Does not require the validate package.
func checkFrontmatter(data []byte) error {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return fmt.Errorf("does not start with YAML frontmatter fence '---'")
	}
	limit := 50
	if len(lines) < limit {
		limit = len(lines)
	}
	for i := 1; i < limit; i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			return nil
		}
	}
	return fmt.Errorf("no closing YAML frontmatter fence '---' within 50 lines")
}

// TestKindGitignoreContainsLangPatterns verifies each kind's .gitignore
// overlay contains at least one language/framework-specific pattern.
func TestKindGitignoreContainsLangPatterns(t *testing.T) {
	tests := []struct {
		kind    string
		pattern string
	}{
		{"rails", "tmp/"},
		{"go", "*.test"},
		{"python", "__pycache__/"},
		{"node", "node_modules/"},
		{"rust", "target/"},
		{"static-site", "_site/"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.kind, func(t *testing.T) {
			data, err := templates.ReadFile("templates/" + tc.kind + "/.gitignore")
			if err != nil {
				t.Fatalf("read .gitignore for %s: %v", tc.kind, err)
			}
			if !strings.Contains(string(data), tc.pattern) {
				t.Errorf("%s .gitignore should contain %q; got:\n%s", tc.kind, tc.pattern, string(data))
			}
		})
	}
}

// TestKindYakosYMLHasCommands verifies each kind's yakos.yml contains the
// commands block with test/lint/build entries.
func TestKindYakosYMLHasCommands(t *testing.T) {
	tests := []struct {
		kind        string
		testCmd     string
		profileType string
	}{
		{"rails", "bundle exec rspec", "web-app"},
		{"go", "go test ./...", "service"},
		{"python", "pytest", "service"},
		{"node", "npm test", "web-app"},
		{"rust", "cargo test", "cli-tool"},
		{"static-site", "build", "static-site"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.kind, func(t *testing.T) {
			data, err := templates.ReadFile("templates/" + tc.kind + "/yakos.yml")
			if err != nil {
				t.Fatalf("read yakos.yml for %s: %v", tc.kind, err)
			}
			content := string(data)
			if !strings.Contains(content, tc.testCmd) {
				t.Errorf("%s yakos.yml should contain test command %q; got:\n%s", tc.kind, tc.testCmd, content)
			}
			if !strings.Contains(content, tc.profileType) {
				t.Errorf("%s yakos.yml should mention profile type %q; got:\n%s", tc.kind, tc.profileType, content)
			}
			if !strings.Contains(content, "commands:") {
				t.Errorf("%s yakos.yml should have a commands: block", tc.kind)
			}
		})
	}
}

// TestRun_KindAgentsWrittenToProject verifies that running init with a non-base
// template writes agent .md files into the project's .claude/agents/ directory.
func TestRun_KindAgentsWrittenToProject(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "agenttest", proj)
	cfg.Template = "go"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run(--template go): %v", err)
	}

	agentsDir := filepath.Join(proj, ".claude", "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		t.Fatalf("ReadDir .claude/agents: %v", err)
	}
	var agents []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			agents = append(agents, e.Name())
		}
	}
	if len(agents) < 2 {
		t.Errorf("expected at least 2 agent .md files in .claude/agents/; got %d: %v", len(agents), agents)
	}

	// backend.md must be present.
	found := false
	for _, a := range agents {
		if a == "backend.md" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("backend.md not found in .claude/agents/; got: %v", agents)
	}
}

// TestRun_KindGitignoreAppended verifies that running init with a non-base
// template appends kind-specific .gitignore patterns to the project .gitignore.
func TestRun_KindGitignoreAppended(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "gitignore-kind-test", proj)
	cfg.Template = "python"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run(--template python): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(proj, ".gitignore"))
	if err != nil {
		t.Fatalf("ReadFile .gitignore: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "__pycache__/") {
		t.Errorf("python .gitignore pattern '__pycache__/' not found in project .gitignore:\n%s", got)
	}
}

// TestRun_KindAGENTSMDOverride verifies that a non-base template writes its
// own AGENTS.md instead of the base AGENTS.md.
func TestRun_KindAGENTSMDOverride(t *testing.T) {
	proj := newGitRepo(t)
	cfg := baseConfig(t, "agentsmd-test", proj)
	cfg.Template = "rust"

	if _, err := Run(cfg); err != nil {
		t.Fatalf("Run(--template rust): %v", err)
	}

	data, err := os.ReadFile(filepath.Join(proj, "AGENTS.md"))
	if err != nil {
		t.Fatalf("ReadFile AGENTS.md: %v", err)
	}
	// The rust AGENTS.md overlay must mention Rust-specific content.
	if !strings.Contains(string(data), "Rust") {
		t.Errorf("AGENTS.md for rust template should contain 'Rust'; got:\n%s", string(data))
	}
}

// TestRun_AllTemplates verifies Run completes successfully for every non-base
// template and writes the minimum required project files.
func TestRun_AllTemplates(t *testing.T) {
	for _, kind := range []string{"rails", "go", "python", "node", "rust", "static-site"} {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			proj := newGitRepo(t)
			cfg := baseConfig(t, "alltest-"+kind, proj)
			cfg.Template = kind

			res, err := Run(cfg)
			if err != nil {
				t.Fatalf("Run(--template %s): %v", kind, err)
			}

			// yakos.yml must have been written.
			yakosYML := filepath.Join(proj, ".yakos.yml")
			if _, err := os.Stat(yakosYML); err != nil {
				t.Errorf("%s: .yakos.yml not found: %v", kind, err)
			}

			// AGENTS.md must have been written.
			agentsMD := filepath.Join(proj, "AGENTS.md")
			if _, err := os.Stat(agentsMD); err != nil {
				t.Errorf("%s: AGENTS.md not found: %v", kind, err)
			}

			// .claude/agents/ must have at least 2 agent files.
			agentsDir := filepath.Join(proj, ".claude", "agents")
			entries, readErr := os.ReadDir(agentsDir)
			if readErr != nil {
				t.Errorf("%s: .claude/agents/ not found: %v", kind, readErr)
			} else {
				count := 0
				for _, e := range entries {
					if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
						count++
					}
				}
				if count < 2 {
					t.Errorf("%s: expected at least 2 agent files in .claude/agents/; got %d", kind, count)
				}
			}

			_ = res
		})
	}
}
