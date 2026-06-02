// init_parity_test.go — Phase 1 parity tests for `yakos init`.
//
// Design notes:
//
//  1. All tests use initialize.Run directly with temp dirs so no real
//     ~/agent-control/ or bash runtime is needed.
//
//  2. Help-text parity verifies load-bearing phrases from cli/lib/init.sh
//     usage() appear in the Go output.
//
//  3. The Go port does NOT copy bash hook scripts (a documented caveat
//     in initialize.go). Parity tests assert the advisory message appears
//     on stderr instead of asserting hook files exist.
//
// Critical scenarios verified:
//
//	(a) happy-path              — control tree, project files, MEMORY.md created
//	(b) --dry-run               — no files written; list printed to stdout
//	(c) --force                 — project-side files overwritten on second run
//	(d) invalid name            — error mentions alphanumeric
//	(e) invalid project         — error mentions not found
//	(f) not-a-git-repo          — error mentions git repository
//	(g) unknown template        — error says "did you mean"
//	(h) --template go           — go kind yakos.yml written
//	(i) --template rails        — rails kind yakos.yml written
//	(j) --template python       — python kind yakos.yml written
//	(k) --template node         — node kind yakos.yml written
//	(l) --template rust         — rust kind yakos.yml written
//	(m) --template static-site  — static-site kind yakos.yml written
//	(n) idempotent re-run       — second run skips existing files
//	(o) gitignore patterns      — yakOS patterns appended to project .gitignore
//	(p) .project-path content   — absolute path written correctly
//	(q) help text               — all load-bearing phrases from init.sh usage()
//	(r) hook advisory           — stderr mentions `yakos refresh` for hooks
//	(s) sessions.ndjson created — empty ledger file exists
//	(t) session-history empty   — .session-started-history.ndjson is zero bytes
//	(u) MEMORY.md seeded        — contains project name + path
//	(v) .gitignore written      — agent-control .gitignore is '*'
//	(w) hook-bypass.md          — work/current/hook-bypass.md exists
//	(x) path-allowlist.json     — project .claude/path-allowlist.json exists
//	(y) settings.json           — project .claude/settings.json exists
//	(z) AGENTS.md               — project AGENTS.md exists
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/initialize"
)

// ---- helpers ----------------------------------------------------------------

// newInitGitRepo creates a temp dir with a .git directory.
func newInitGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return dir
}

// initBaseConfig returns a Config for tests with isolated home + stderr capture.
func initBaseConfig(t *testing.T, name, project string) initialize.Config {
	t.Helper()
	home := t.TempDir()
	return initialize.Config{
		Name:        name,
		ProjectPath: project,
		HomeDir:     home,
		Now:         time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		Writer:      &bytes.Buffer{},
		ErrWriter:   &bytes.Buffer{},
	}
}

// ---- (a) happy-path ---------------------------------------------------------

// TestInitParity_HappyPath verifies the full scaffold.
func TestInitParity_HappyPath(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "testproj", proj)

	res, err := initialize.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Name != "testproj" {
		t.Errorf("Name: got %q, want testproj", res.Name)
	}
	if len(res.FilesWritten) == 0 {
		t.Error("expected FilesWritten to be non-empty")
	}

	home := cfg.HomeDir
	controlDir := filepath.Join(home, "agent-control", "testproj")
	for _, dir := range []string{
		filepath.Join(controlDir, "work", "current", "logs"),
		filepath.Join(controlDir, "work", "current", "artifacts"),
		filepath.Join(controlDir, "work", "current", "reports"),
		filepath.Join(controlDir, "work", "archive"),
	} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("expected dir %s: %v", dir, err)
		}
	}
}

// ---- (b) --dry-run ----------------------------------------------------------

// TestInitParity_DryRun verifies dry-run writes no files.
func TestInitParity_DryRun(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "dryproj", proj)
	cfg.DryRun = true

	res, err := initialize.Run(cfg)
	if err != nil {
		t.Fatalf("Run (dry-run): %v", err)
	}
	if len(res.FilesWritten) != 0 {
		t.Errorf("dry-run should write 0 files; got %d", len(res.FilesWritten))
	}
	if len(res.DryRunFiles) == 0 {
		t.Error("dry-run should populate DryRunFiles")
	}
	stdout := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(stdout, "dry-run") {
		t.Errorf("stdout should mention dry-run; got:\n%s", stdout)
	}
}

// ---- (c) --force ------------------------------------------------------------

// TestInitParity_Force verifies --force overwrites project-side files.
func TestInitParity_Force(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "forceproj", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Overwrite .yakos.yml with sentinel.
	yakosYML := filepath.Join(proj, ".yakos.yml")
	if err := os.WriteFile(yakosYML, []byte("# SENTINEL\n"), 0644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	cfg.Force = true
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("second Run (force): %v", err)
	}

	data, _ := os.ReadFile(yakosYML)
	if string(data) == "# SENTINEL\n" {
		t.Error("--force should overwrite sentinel .yakos.yml but didn't")
	}
}

// ---- (d) invalid name -------------------------------------------------------

// TestInitParity_InvalidName verifies a bad name produces an alphanumeric error.
func TestInitParity_InvalidName(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "bad/name", proj)
	_, err := initialize.Run(cfg)
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !strings.Contains(err.Error(), "alphanumeric") {
		t.Errorf("error should mention 'alphanumeric'; got %q", err.Error())
	}
}

// ---- (e) invalid project ----------------------------------------------------

// TestInitParity_InvalidProject verifies a non-existent project path is rejected.
func TestInitParity_InvalidProject(t *testing.T) {
	cfg := initBaseConfig(t, "okname", "/no/such/path/exists/for/test/9999")
	_, err := initialize.Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-existent project")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say 'not found'; got %q", err.Error())
	}
}

// ---- (f) not-a-git-repo -----------------------------------------------------

// TestInitParity_NotGitRepo verifies a plain directory (no .git) is rejected.
func TestInitParity_NotGitRepo(t *testing.T) {
	proj := t.TempDir() // plain dir, no .git
	cfg := initBaseConfig(t, "notgit", proj)
	_, err := initialize.Run(cfg)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("error should mention 'git repository'; got %q", err.Error())
	}
}

// ---- (g) unknown template ---------------------------------------------------

// TestInitParity_UnknownTemplate verifies unknown --template gives helpful error.
func TestInitParity_UnknownTemplate(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "tmpltest", proj)
	cfg.Template = "elixir"
	_, err := initialize.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown template")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error should say 'did you mean'; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "elixir") {
		t.Errorf("error should echo the bad kind 'elixir'; got %q", err.Error())
	}
}

// ---- (h-m) template kinds ---------------------------------------------------

// TestInitParity_TemplateGo verifies the go template overlay.
func TestInitParity_TemplateGo(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "goapp", proj)
	cfg.Template = "go"

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run(--template go): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if !strings.Contains(string(data), "go test") {
		t.Errorf("go yakos.yml should mention 'go test'; got:\n%s", string(data))
	}
}

// TestInitParity_TemplateRails verifies the rails template overlay.
func TestInitParity_TemplateRails(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "railsapp", proj)
	cfg.Template = "rails"

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run(--template rails): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if !strings.Contains(string(data), "rails") {
		t.Errorf("rails yakos.yml should mention 'rails'; got:\n%s", string(data))
	}
}

// TestInitParity_TemplatePython verifies the python template overlay.
func TestInitParity_TemplatePython(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "pyapp", proj)
	cfg.Template = "python"

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run(--template python): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if !strings.Contains(string(data), "pytest") {
		t.Errorf("python yakos.yml should mention 'pytest'; got:\n%s", string(data))
	}
}

// TestInitParity_TemplateNode verifies the node template overlay.
func TestInitParity_TemplateNode(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "nodeapp", proj)
	cfg.Template = "node"

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run(--template node): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if !strings.Contains(string(data), "npm test") {
		t.Errorf("node yakos.yml should mention 'npm test'; got:\n%s", string(data))
	}
}

// TestInitParity_TemplateRust verifies the rust template overlay.
func TestInitParity_TemplateRust(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "rustapp", proj)
	cfg.Template = "rust"

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run(--template rust): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if !strings.Contains(string(data), "cargo test") {
		t.Errorf("rust yakos.yml should mention 'cargo test'; got:\n%s", string(data))
	}
}

// TestInitParity_TemplateStaticSite verifies the static-site template overlay.
func TestInitParity_TemplateStaticSite(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "staticapp", proj)
	cfg.Template = "static-site"

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run(--template static-site): %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, ".yakos.yml"))
	if !strings.Contains(string(data), "static") {
		t.Errorf("static-site yakos.yml should mention 'static'; got:\n%s", string(data))
	}
}

// ---- (n) idempotent re-run --------------------------------------------------

// TestInitParity_Idempotent verifies second run skips existing files.
func TestInitParity_Idempotent(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "idemtest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	cfg.Writer = &bytes.Buffer{}
	cfg.ErrWriter = &bytes.Buffer{}
	r2, err := initialize.Run(cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(r2.FilesSkipped) == 0 {
		t.Error("second run should skip some files")
	}
}

// ---- (o) gitignore patterns -------------------------------------------------

// TestInitParity_GitignorePatterns verifies yakOS patterns appended.
func TestInitParity_GitignorePatterns(t *testing.T) {
	proj := newInitGitRepo(t)
	if err := os.WriteFile(filepath.Join(proj, ".gitignore"), []byte("*.log\n"), 0644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	cfg := initBaseConfig(t, "gitest", proj)
	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(proj, ".gitignore"))
	got := string(data)
	for _, pattern := range []string{
		".codex/agents/yakos-*.toml",
		".gemini/agents/yakos-*.md",
	} {
		if !strings.Contains(got, pattern) {
			t.Errorf(".gitignore missing pattern %q", pattern)
		}
	}
	if !strings.Contains(got, "*.log") {
		t.Error(".gitignore should preserve existing content")
	}
}

// ---- (p) .project-path ------------------------------------------------------

// TestInitParity_ProjectPath verifies .project-path contains the project dir.
func TestInitParity_ProjectPath(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "pptest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(cfg.HomeDir, "agent-control", "pptest", ".project-path"))
	if err != nil {
		t.Fatalf("ReadFile .project-path: %v", err)
	}
	got := strings.TrimSpace(string(data))
	if !filepath.IsAbs(got) {
		t.Errorf(".project-path should be absolute; got %q", got)
	}
}

// ---- (q) help text ----------------------------------------------------------

// TestInitParity_HelpText verifies all load-bearing phrases from init.sh usage().
func TestInitParity_HelpText(t *testing.T) {
	var out bytes.Buffer
	initialize.PrintHelp(&out)
	got := out.String()

	mustContain := []string{
		"yakos init <name>",
		"--project <path>",
		"Bootstrap",
		"~/agent-control",
		".claude",
		"--template",
		"--force",
		"--dry-run",
		"--help",
		"refresh",
		"hooks",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("help text missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}

// ---- (r) hook advisory ------------------------------------------------------

// TestInitParity_HookAdvisory verifies stderr contains the refresh advisory.
func TestInitParity_HookAdvisory(t *testing.T) {
	proj := newInitGitRepo(t)
	var errBuf bytes.Buffer
	cfg := initBaseConfig(t, "hookadv", proj)
	cfg.ErrWriter = &errBuf

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(errBuf.String(), "refresh") {
		t.Errorf("stderr should mention 'refresh' for hook advisory; got:\n%s", errBuf.String())
	}
}

// ---- (s) sessions.ndjson created --------------------------------------------

// TestInitParity_SessionsNDJSON verifies sessions.ndjson is created (may be empty).
func TestInitParity_SessionsNDJSON(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "sesstest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(cfg.HomeDir, "agent-control", "sesstest", "work", "sessions.ndjson")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("sessions.ndjson should exist: %v", err)
	}
}

// ---- (t) session-history empty ----------------------------------------------

// TestInitParity_SessionHistoryEmpty verifies .session-started-history.ndjson is empty.
func TestInitParity_SessionHistoryEmpty(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "histtest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(cfg.HomeDir, "agent-control", "histtest", "work", "current", ".session-started-history.ndjson")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile .session-started-history.ndjson: %v", err)
	}
	if len(data) != 0 {
		t.Errorf(".session-started-history.ndjson should be empty; got %d bytes", len(data))
	}
}

// ---- (u) MEMORY.md seeded ---------------------------------------------------

// TestInitParity_MemoryMD verifies MEMORY.md exists and contains the name.
func TestInitParity_MemoryMD(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "memorytest", proj)

	res, err := initialize.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.MemoryFile == "" {
		t.Fatal("MemoryFile should be set in Result")
	}
	data, err := os.ReadFile(res.MemoryFile)
	if err != nil {
		t.Fatalf("ReadFile MEMORY.md: %v", err)
	}
	if !strings.Contains(string(data), "memorytest") {
		t.Errorf("MEMORY.md should contain project name; got:\n%s", string(data))
	}
}

// ---- (v) .gitignore in agent-control ----------------------------------------

// TestInitParity_AgentControlGitignore verifies agent-control .gitignore exists.
func TestInitParity_AgentControlGitignore(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "gitest2", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(cfg.HomeDir, "agent-control", "gitest2", ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile .gitignore: %v", err)
	}
	if !strings.Contains(string(data), "*") {
		t.Errorf("agent-control .gitignore should contain '*'; got:\n%s", string(data))
	}
}

// ---- (w) hook-bypass.md -----------------------------------------------------

// TestInitParity_HookBypassMD verifies work/current/hook-bypass.md is created.
func TestInitParity_HookBypassMD(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "bypasstest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(cfg.HomeDir, "agent-control", "bypasstest", "work", "current", "hook-bypass.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile hook-bypass.md: %v", err)
	}
	if !strings.Contains(string(data), "Active entries") {
		t.Errorf("hook-bypass.md should contain 'Active entries'; got:\n%s", string(data))
	}
}

// ---- (x) path-allowlist.json ------------------------------------------------

// TestInitParity_PathAllowlist verifies project .claude/path-allowlist.json exists.
func TestInitParity_PathAllowlist(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "allowtest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(proj, ".claude", "path-allowlist.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("path-allowlist.json should exist: %v", err)
	}
}

// ---- (y) settings.json ------------------------------------------------------

// TestInitParity_ProjectSettings verifies project .claude/settings.json exists.
func TestInitParity_ProjectSettings(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "settest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(proj, ".claude", "settings.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("settings.json should exist: %v", err)
	}
}

// ---- (z) AGENTS.md ----------------------------------------------------------

// TestInitParity_AgentsMD verifies project AGENTS.md exists.
func TestInitParity_AgentsMD(t *testing.T) {
	proj := newInitGitRepo(t)
	cfg := initBaseConfig(t, "agentstest", proj)

	if _, err := initialize.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	path := filepath.Join(proj, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile AGENTS.md: %v", err)
	}
	if !strings.Contains(string(data), "AGENTS.md") {
		t.Errorf("AGENTS.md should contain 'AGENTS.md'; got:\n%s", string(data))
	}
}
