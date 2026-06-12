// Package envcfg_test holds unit tests for the envcfg port (rank 29).
//
// All tests use temp directories and injectable fns so no real git repo,
// gh/glab binary, or filesystem side-effects are needed.
//
// Critical scenarios:
//
//	(a) status: no .yakos.yml → project dir resolved, branch unknown, no env mapped
//	(b) status: .yakos.yml with environments → mapped env returned
//	(c) status: branch not in any env → <none> mapping
//	(d) list: empty environments → no output
//	(e) list: configured environments → only configured printed in order
//	(f) validate: no .yakos.yml → error
//	(g) validate: .yakos.yml without environments section → [info] advisory
//	(h) validate: .yakos.yml with environments → [ok] lines printed
//	(i) validate: branch missing locally → [warn] line
//	(j) promote: missing env names → error
//	(k) promote: from env not configured → error
//	(l) promote: to env not configured → error
//	(m) promote: local != remote → error
//	(n) promote: tool=git → fallback advice printed to stderr
//	(o) promote: tool=gh → gh pr create called with correct args
//	(p) promote: tool=glab → glab mr create called with correct args
//	(q) unknown subcommand → error with hint
//	(r) help text contains key phrases
//	(s) project dir from YAKOS_PROJECT_DIR env
//	(t) project dir from .yakos.yml in cwd
//	(u) promote: local == remote → no divergence error
package envcfg_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/envcfg"
)

// ---- helpers ----------------------------------------------------------------

var fixedNow = func() time.Time { return time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC) }

func newCfg(t *testing.T, sub string) envcfg.Config {
	t.Helper()
	dir := t.TempDir()
	return envcfg.Config{
		Subcommand: sub,
		ProjectDir: dir,
		HomeDir:    t.TempDir(),
		Now:        fixedNow,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func out(cfg envcfg.Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func errOut(cfg envcfg.Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

func writeYML(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}
}

const envYML = `environments:
  dev:
    branch: main
  test:
    branch: staging
  prod:
    branch: production
`

// stubGit returns a GitFn that maps args → output.
// The args key is the space-joined tail after "-C <dir>".
func stubGit(responses map[string]string) func(args ...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		// Drop "-C <dir>" prefix (first two args).
		tail := args
		if len(tail) >= 2 && tail[0] == "-C" {
			tail = tail[2:]
		}
		key := strings.Join(tail, " ")
		if v, ok := responses[key]; ok {
			if v == "__err__" {
				return nil, fmt.Errorf("stub error for %q", key)
			}
			return []byte(v), nil
		}
		// Default: return empty success.
		return []byte(""), nil
	}
}

// ---- scenario (a): status no .yakos.yml -------------------------------------

func TestEnvParity_Status_NoYML(t *testing.T) {
	cfg := newCfg(t, "status")
	cfg.GitFn = stubGit(map[string]string{
		"rev-parse --abbrev-ref HEAD": "main\n",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Subcommand != "status" {
		t.Errorf("expected Subcommand='status'; got %q", res.Subcommand)
	}
	if res.CurrentBranch != "main" {
		t.Errorf("expected CurrentBranch='main'; got %q", res.CurrentBranch)
	}
	if res.MappedEnv != "" {
		t.Errorf("expected empty MappedEnv; got %q", res.MappedEnv)
	}
	o := out(cfg)
	if !strings.Contains(o, "Mapped environment: <none>") {
		t.Errorf("expected '<none>' in output; got: %q", o)
	}
}

// ---- scenario (b): status with .yakos.yml → env mapped ----------------------

func TestEnvParity_Status_BranchMapped(t *testing.T) {
	cfg := newCfg(t, "status")
	writeYML(t, cfg.ProjectDir, envYML)
	cfg.GitFn = stubGit(map[string]string{
		"rev-parse --abbrev-ref HEAD": "main\n",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.MappedEnv != "dev" {
		t.Errorf("expected MappedEnv='dev'; got %q", res.MappedEnv)
	}
	o := out(cfg)
	if !strings.Contains(o, "Mapped environment: dev") {
		t.Errorf("expected 'Mapped environment: dev'; got: %q", o)
	}
}

// ---- scenario (c): status branch not in any env → <none> --------------------

func TestEnvParity_Status_BranchNotMapped(t *testing.T) {
	cfg := newCfg(t, "status")
	writeYML(t, cfg.ProjectDir, envYML)
	cfg.GitFn = stubGit(map[string]string{
		"rev-parse --abbrev-ref HEAD": "feature/xyz\n",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.MappedEnv != "" {
		t.Errorf("expected empty MappedEnv; got %q", res.MappedEnv)
	}
	if !strings.Contains(out(cfg), "<none>") {
		t.Errorf("expected '<none>' in output; got: %q", out(cfg))
	}
}

// ---- scenario (d): list empty environments ----------------------------------

func TestEnvParity_List_Empty(t *testing.T) {
	cfg := newCfg(t, "list")
	// No .yakos.yml → no output.
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Subcommand != "list" {
		t.Errorf("expected Subcommand='list'")
	}
	if out(cfg) != "" {
		t.Errorf("expected no output for empty list; got: %q", out(cfg))
	}
}

// ---- scenario (e): list configured envs in order ----------------------------

func TestEnvParity_List_Configured(t *testing.T) {
	cfg := newCfg(t, "list")
	writeYML(t, cfg.ProjectDir, envYML)
	_, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "dev → main") {
		t.Errorf("expected 'dev → main'; got: %q", o)
	}
	if !strings.Contains(o, "test → staging") {
		t.Errorf("expected 'test → staging'; got: %q", o)
	}
	if !strings.Contains(o, "prod → production") {
		t.Errorf("expected 'prod → production'; got: %q", o)
	}
	// Verify ordering: dev before test before prod.
	devIdx := strings.Index(o, "dev")
	testIdx := strings.Index(o, "test")
	prodIdx := strings.Index(o, "prod")
	if devIdx >= testIdx || testIdx >= prodIdx {
		t.Errorf("expected dev < test < prod ordering; got output: %q", o)
	}
}

// ---- scenario (f): validate no .yakos.yml → error --------------------------

func TestEnvParity_Validate_NoYML_Error(t *testing.T) {
	cfg := newCfg(t, "validate")
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error when .yakos.yml missing")
	}
	if !strings.Contains(err.Error(), ".yakos.yml") {
		t.Errorf("expected .yakos.yml mention in error; got: %v", err)
	}
}

// ---- scenario (g): validate without environments section → advisory ---------

func TestEnvParity_Validate_NoEnvironmentsSection(t *testing.T) {
	cfg := newCfg(t, "validate")
	writeYML(t, cfg.ProjectDir, "yakos: 0.9\nprofile:\n  type: service\n")
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ValidationOK {
		t.Errorf("expected ValidationOK=true")
	}
	o := out(cfg)
	if !strings.Contains(o, "[info] no environments declared") {
		t.Errorf("expected advisory; got: %q", o)
	}
}

// ---- scenario (h): validate with environments → [ok] lines -----------------

func TestEnvParity_Validate_OK(t *testing.T) {
	cfg := newCfg(t, "validate")
	writeYML(t, cfg.ProjectDir, envYML)
	// Stub git: all branches exist.
	cfg.GitFn = stubGit(map[string]string{
		"rev-parse --verify main":       "abc123\n",
		"rev-parse --verify staging":    "def456\n",
		"rev-parse --verify production": "ghi789\n",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ValidationOK {
		t.Errorf("expected ValidationOK=true")
	}
	o := out(cfg)
	if !strings.Contains(o, "[ok] environments section present") {
		t.Errorf("expected '[ok] environments section present'; got: %q", o)
	}
	if !strings.Contains(o, "[ok] dev") {
		t.Errorf("expected '[ok] dev'; got: %q", o)
	}
}

// ---- scenario (i): validate branch missing locally → [warn] -----------------

func TestEnvParity_Validate_BranchMissingLocally(t *testing.T) {
	cfg := newCfg(t, "validate")
	writeYML(t, cfg.ProjectDir, envYML)
	// Stub: only main exists; staging and production don't.
	cfg.GitFn = stubGit(map[string]string{
		"rev-parse --verify main":       "abc123\n",
		"rev-parse --verify staging":    "__err__",
		"rev-parse --verify production": "__err__",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ValidationOK {
		t.Errorf("expected ValidationOK=true")
	}
	o := out(cfg)
	if !strings.Contains(o, "[warn]") {
		t.Errorf("expected [warn] for missing branches; got: %q", o)
	}
}

// ---- scenario (j): promote missing env names → error ------------------------

func TestEnvParity_Promote_MissingEnvNames_Error(t *testing.T) {
	cfg := newCfg(t, "promote")
	// PromoteFrom and PromoteTo both empty.
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error for missing env names")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (k): promote from env not configured → error ------------------

func TestEnvParity_Promote_FromNotConfigured_Error(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "prod"
	writeYML(t, cfg.ProjectDir, "environments:\n  prod:\n    branch: production\n")
	cfg.GitFn = stubGit(nil)
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error when from env not configured")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Errorf("expected 'dev' in error; got: %v", err)
	}
}

// ---- scenario (l): promote to env not configured → error --------------------

func TestEnvParity_Promote_ToNotConfigured_Error(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "prod"
	writeYML(t, cfg.ProjectDir, "environments:\n  dev:\n    branch: main\n")
	cfg.GitFn = stubGit(nil)
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error when to env not configured")
	}
	if !strings.Contains(err.Error(), "prod") {
		t.Errorf("expected 'prod' in error; got: %v", err)
	}
}

// ---- scenario (m): promote local != remote → error --------------------------

func TestEnvParity_Promote_LocalDivergence_Error(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeYML(t, cfg.ProjectDir, envYML)
	cfg.GitFn = stubGit(map[string]string{
		"fetch origin main":           "",
		"rev-parse main":              "aaaa\n",
		"rev-parse origin/main":       "bbbb\n",
		"log --oneline staging..main": "",
	})
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error for local/remote divergence")
	}
	if !strings.Contains(err.Error(), "differs") {
		t.Errorf("expected 'differs' in error; got: %v", err)
	}
}

// ---- scenario (n): promote tool=git → fallback advice -----------------------

func TestEnvParity_Promote_GitFallback(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeYML(t, cfg.ProjectDir, envYML)
	same := "abc123\n"
	cfg.GitFn = stubGit(map[string]string{
		"fetch origin main":           "",
		"rev-parse main":              same,
		"rev-parse origin/main":       same,
		"log --oneline staging..main": "abc add feature\n",
	})
	cfg.PRToolOverride = envcfg.PRToolGit
	_, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	e := errOut(cfg)
	if !strings.Contains(e, "No gh/glab detected") {
		t.Errorf("expected fallback advice in stderr; got: %q", e)
	}
	if !strings.Contains(e, "base:") {
		t.Errorf("expected 'base:' in stderr; got: %q", e)
	}
}

// ---- scenario (o): promote tool=gh → gh pr create called -------------------

func TestEnvParity_Promote_GH_Called(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeYML(t, cfg.ProjectDir, envYML)
	same := "abc123\n"
	cfg.GitFn = stubGit(map[string]string{
		"fetch origin main":           "",
		"rev-parse main":              same,
		"rev-parse origin/main":       same,
		"log --oneline staging..main": "abc add feature\n",
	})
	cfg.PRToolOverride = envcfg.PRToolGH
	var calledWith []string
	cfg.ExecFn = func(name string, args ...string) ([]byte, error) {
		calledWith = append([]string{name}, args...)
		return []byte("https://github.com/org/repo/pull/1\n"), nil
	}
	_, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(calledWith) == 0 || calledWith[0] != "gh" {
		t.Errorf("expected gh to be called; got: %v", calledWith)
	}
	joined := strings.Join(calledWith, " ")
	if !strings.Contains(joined, "pr create") {
		t.Errorf("expected 'pr create'; got: %q", joined)
	}
	if !strings.Contains(joined, "--base staging") {
		t.Errorf("expected '--base staging'; got: %q", joined)
	}
	if !strings.Contains(joined, "--head main") {
		t.Errorf("expected '--head main'; got: %q", joined)
	}
}

// ---- scenario (p): promote tool=glab → glab mr create called ---------------

func TestEnvParity_Promote_GLab_Called(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeYML(t, cfg.ProjectDir, envYML)
	same := "abc123\n"
	cfg.GitFn = stubGit(map[string]string{
		"fetch origin main":           "",
		"rev-parse main":              same,
		"rev-parse origin/main":       same,
		"log --oneline staging..main": "",
	})
	cfg.PRToolOverride = envcfg.PRToolGLab
	var calledWith []string
	cfg.ExecFn = func(name string, args ...string) ([]byte, error) {
		calledWith = append([]string{name}, args...)
		return []byte("https://gitlab.com/org/repo/merge_requests/1\n"), nil
	}
	_, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(calledWith) == 0 || calledWith[0] != "glab" {
		t.Errorf("expected glab to be called; got: %v", calledWith)
	}
	joined := strings.Join(calledWith, " ")
	if !strings.Contains(joined, "mr create") {
		t.Errorf("expected 'mr create'; got: %q", joined)
	}
	if !strings.Contains(joined, "--target-branch staging") {
		t.Errorf("expected '--target-branch staging'; got: %q", joined)
	}
}

// ---- scenario (q): unknown subcommand → error with hint ---------------------

func TestEnvParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newCfg(t, "bogus")
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "yakos env help") {
		t.Errorf("expected help hint; got: %v", err)
	}
}

// ---- scenario (r): help text key phrases ------------------------------------

func TestEnvParity_HelpText_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	envcfg.PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos env",
		"status",
		"promote",
		"validate",
		"list",
		"environments",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing %q; got:\n%s", phrase, help)
		}
	}
}

// ---- scenario (s): project dir from YAKOS_PROJECT_DIR env ------------------

func TestEnvParity_ProjectDir_FromEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("YAKOS_PROJECT_DIR", dir)
	cfg := envcfg.Config{
		Subcommand: "list",
		// No ProjectDir set; should pick up from env.
		HomeDir:   t.TempDir(),
		Writer:    &bytes.Buffer{},
		ErrWriter: &bytes.Buffer{},
	}
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ProjectDir != dir {
		t.Errorf("expected ProjectDir=%q; got %q", dir, res.ProjectDir)
	}
}

// ---- scenario (t): project dir from .yakos.yml in cwd ----------------------

func TestEnvParity_ProjectDir_FromCWD(t *testing.T) {
	dir := t.TempDir()
	// Write a .yakos.yml so the cwd-detection fires.
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte("yakos: 0.9\n"), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}

	// Resolve symlinks for comparison on macOS (/var/folders → /private/var/folders).
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = dir
	}

	orig, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	cfg := envcfg.Config{
		Subcommand: "list",
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Both resolved and unresolved paths are acceptable.
	resReal, _ := filepath.EvalSymlinks(res.ProjectDir)
	if resReal != realDir && res.ProjectDir != dir {
		t.Errorf("expected ProjectDir=%q (or %q); got %q", dir, realDir, res.ProjectDir)
	}
}

// ---- scenario (u): promote local == remote → success (no divergence) --------

func TestEnvParity_Promote_LocalEqualsRemote_OK(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeYML(t, cfg.ProjectDir, envYML)
	same := "abc123\n"
	cfg.GitFn = stubGit(map[string]string{
		"fetch origin main":           "",
		"rev-parse main":              same,
		"rev-parse origin/main":       same,
		"log --oneline staging..main": "",
	})
	cfg.PRToolOverride = envcfg.PRToolGit
	_, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("expected no divergence error; got: %v", err)
	}
}

// ---- scenario: promote title contains timestamp and env names ---------------

func TestEnvParity_Promote_TitleFormat(t *testing.T) {
	cfg := newCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeYML(t, cfg.ProjectDir, envYML)
	same := "abc123\n"
	cfg.GitFn = stubGit(map[string]string{
		"fetch origin main":           "",
		"rev-parse main":              same,
		"rev-parse origin/main":       same,
		"log --oneline staging..main": "",
	})
	cfg.PRToolOverride = envcfg.PRToolGH
	var title string
	cfg.ExecFn = func(name string, args ...string) ([]byte, error) {
		for i, a := range args {
			if a == "--title" && i+1 < len(args) {
				title = args[i+1]
			}
		}
		return []byte("https://github.com/org/repo/pull/1"), nil
	}
	if _, err := envcfg.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(title, "promote") {
		t.Errorf("expected 'promote' in title; got: %q", title)
	}
	if !strings.Contains(title, "main") {
		t.Errorf("expected 'main' (fromBranch) in title; got: %q", title)
	}
	if !strings.Contains(title, "staging") {
		t.Errorf("expected 'staging' (toBranch) in title; got: %q", title)
	}
	// Should contain an RFC3339 timestamp fragment.
	if !strings.Contains(title, "2026-06-02") {
		t.Errorf("expected date in title; got: %q", title)
	}
}
