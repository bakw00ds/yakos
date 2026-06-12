// env_parity_test.go — Phase 1 parity tests for `yakos env` (rank 29).
//
// Design notes:
//
//  1. All tests call envcfg.Run directly with temp-dir paths and stub
//     GitFn/ExecFn; no real git repo or gh/glab binary required.
//
//  2. The bash env.sh manages dev/test/prod environment promotion:
//     status    — show current branch → env mapping
//     promote   — open a PR from one env's branch to another's
//     validate  — check .yakos.yml environments section
//     list      — list configured envs
//
//  3. Parity is verified behaviourally (output shape, filesystem effects,
//     error conditions) rather than byte-for-byte.
//
// Critical scenarios:
//
//	(a) status: branch mapped to env
//	(b) status: branch not mapped → <none>
//	(c) list: envs printed in dev/test/prod order
//	(d) validate: no .yakos.yml → error
//	(e) validate: environments section present → [ok] output
//	(f) promote: gh tool called with correct flags
//	(g) promote: git fallback prints advice to stderr
//	(h) promote: diverged local/remote → error
//	(i) unknown subcommand → error with hint
//	(j) help text key phrases
//	(k) env entry in portedCommands
//	(l) validate: missing branch locally → [warn]
//	(m) promote: to/from env not configured → error
//	(n) list: empty environments → empty output
//	(o) status: PR tool printed
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/envcfg"
)

// ---- helpers ----------------------------------------------------------------

func newEnvCfg(t *testing.T, sub string) envcfg.Config {
	t.Helper()
	return envcfg.Config{
		Subcommand: sub,
		ProjectDir: t.TempDir(),
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func envOut(cfg envcfg.Config) string    { return cfg.Writer.(*bytes.Buffer).String() }
func envErrOut(cfg envcfg.Config) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

const testEnvYML = `environments:
  dev:
    branch: main
  test:
    branch: staging
  prod:
    branch: production
`

func writeEnvYML(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".yakos.yml"), []byte(testEnvYML), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}
}

func stubEnvGit(responses map[string]string) func(args ...string) ([]byte, error) {
	return func(args ...string) ([]byte, error) {
		tail := args
		if len(tail) >= 2 && tail[0] == "-C" {
			tail = tail[2:]
		}
		key := strings.Join(tail, " ")
		if v, ok := responses[key]; ok {
			if v == "__err__" {
				return nil, bytes.ErrTooLarge
			}
			return []byte(v), nil
		}
		return []byte(""), nil
	}
}

// ---- scenario (a): status branch mapped to env ------------------------------

func TestEnvParity_Status_Mapped(t *testing.T) {
	cfg := newEnvCfg(t, "status")
	writeEnvYML(t, cfg.ProjectDir)
	cfg.GitFn = stubEnvGit(map[string]string{
		"rev-parse --abbrev-ref HEAD": "staging\n",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.MappedEnv != "test" {
		t.Errorf("expected MappedEnv='test'; got %q", res.MappedEnv)
	}
	if !strings.Contains(envOut(cfg), "Mapped environment: test") {
		t.Errorf("expected mapped env in output; got: %q", envOut(cfg))
	}
}

// ---- scenario (b): status branch not mapped → <none> ------------------------

func TestEnvParity_Status_NotMapped(t *testing.T) {
	cfg := newEnvCfg(t, "status")
	writeEnvYML(t, cfg.ProjectDir)
	cfg.GitFn = stubEnvGit(map[string]string{
		"rev-parse --abbrev-ref HEAD": "feat/new-thing\n",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.MappedEnv != "" {
		t.Errorf("expected empty MappedEnv; got %q", res.MappedEnv)
	}
	if !strings.Contains(envOut(cfg), "<none>") {
		t.Errorf("expected '<none>'; got: %q", envOut(cfg))
	}
}

// ---- scenario (c): list envs in dev/test/prod order -------------------------

func TestEnvParity_List_Order(t *testing.T) {
	cfg := newEnvCfg(t, "list")
	writeEnvYML(t, cfg.ProjectDir)
	if _, err := envcfg.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := envOut(cfg)
	devIdx := strings.Index(o, "dev")
	testIdx := strings.Index(o, "test")
	prodIdx := strings.Index(o, "prod")
	if devIdx < 0 || testIdx < 0 || prodIdx < 0 {
		t.Fatalf("expected dev/test/prod in output; got: %q", o)
	}
	if devIdx >= testIdx || testIdx >= prodIdx {
		t.Errorf("expected dev < test < prod ordering in output; got: %q", o)
	}
}

// ---- scenario (d): validate no .yakos.yml → error ---------------------------

func TestEnvParity_Validate_NoYML_Error(t *testing.T) {
	cfg := newEnvCfg(t, "validate")
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error when no .yakos.yml")
	}
	if !strings.Contains(err.Error(), ".yakos.yml") {
		t.Errorf("expected .yakos.yml in error; got: %v", err)
	}
}

// ---- scenario (e): validate environments section present --------------------

func TestEnvParity_Validate_SectionPresent(t *testing.T) {
	cfg := newEnvCfg(t, "validate")
	writeEnvYML(t, cfg.ProjectDir)
	cfg.GitFn = stubEnvGit(map[string]string{
		"rev-parse --verify main":       "abc\n",
		"rev-parse --verify staging":    "def\n",
		"rev-parse --verify production": "ghi\n",
	})
	res, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.ValidationOK {
		t.Errorf("expected ValidationOK=true")
	}
	if !strings.Contains(envOut(cfg), "[ok] environments section present") {
		t.Errorf("expected [ok] line; got: %q", envOut(cfg))
	}
}

// ---- scenario (f): promote gh tool called with correct flags ----------------

func TestEnvParity_Promote_GHFlags(t *testing.T) {
	cfg := newEnvCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeEnvYML(t, cfg.ProjectDir)
	same := "abc123\n"
	cfg.GitFn = stubEnvGit(map[string]string{
		"fetch origin main":           "",
		"rev-parse main":              same,
		"rev-parse origin/main":       same,
		"log --oneline staging..main": "abc feat: add widget\n",
	})
	cfg.PRToolOverride = envcfg.PRToolGH
	var called []string
	cfg.ExecFn = func(name string, args ...string) ([]byte, error) {
		called = append([]string{name}, args...)
		return []byte("https://github.com/org/repo/pull/42"), nil
	}
	if _, err := envcfg.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(called) == 0 {
		t.Fatal("expected ExecFn to be called")
	}
	joined := strings.Join(called, " ")
	for _, want := range []string{"gh", "pr", "create", "--base", "staging", "--head", "main"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in gh call; got: %q", want, joined)
		}
	}
}

// ---- scenario (g): promote git fallback prints advice ----------------------

func TestEnvParity_Promote_GitFallbackAdvice(t *testing.T) {
	cfg := newEnvCfg(t, "promote")
	cfg.PromoteFrom = "test"
	cfg.PromoteTo = "prod"
	writeEnvYML(t, cfg.ProjectDir)
	same := "def456\n"
	cfg.GitFn = stubEnvGit(map[string]string{
		"fetch origin staging":              "",
		"rev-parse staging":                 same,
		"rev-parse origin/staging":          same,
		"log --oneline production..staging": "",
	})
	cfg.PRToolOverride = envcfg.PRToolGit
	if _, err := envcfg.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	e := envErrOut(cfg)
	if !strings.Contains(e, "No gh/glab") {
		t.Errorf("expected fallback advice; got: %q", e)
	}
	if !strings.Contains(e, "base: production") {
		t.Errorf("expected base branch in advice; got: %q", e)
	}
}

// ---- scenario (h): promote diverged local/remote → error -------------------

func TestEnvParity_Promote_Diverged(t *testing.T) {
	cfg := newEnvCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "test"
	writeEnvYML(t, cfg.ProjectDir)
	cfg.GitFn = stubEnvGit(map[string]string{
		"fetch origin main":     "",
		"rev-parse main":        "aaaa\n",
		"rev-parse origin/main": "bbbb\n",
	})
	cfg.PRToolOverride = envcfg.PRToolGit
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error for diverged branches")
	}
	if !strings.Contains(err.Error(), "differs") {
		t.Errorf("expected 'differs' in error; got: %v", err)
	}
}

// ---- scenario (i): unknown subcommand → error with hint --------------------

func TestEnvParity_UnknownSubcommand(t *testing.T) {
	cfg := newEnvCfg(t, "unknown-sub")
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

// ---- scenario (j): help text key phrases ------------------------------------

func TestEnvParity_HelpText(t *testing.T) {
	var buf bytes.Buffer
	envcfg.PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{"yakos env", "status", "promote", "validate", "list", "environments"} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing %q; got:\n%s", phrase, help)
		}
	}
}

// ---- scenario (k): env entry in portedCommands ------------------------------

func TestEnvParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "env" {
			return
		}
	}
	t.Error("expected 'env' in portedCommands; not found")
}

// ---- scenario (l): validate missing branch locally → [warn] -----------------

func TestEnvParity_Validate_BranchMissing(t *testing.T) {
	cfg := newEnvCfg(t, "validate")
	writeEnvYML(t, cfg.ProjectDir)
	cfg.GitFn = stubEnvGit(map[string]string{
		"rev-parse --verify main":       "abc\n",
		"rev-parse --verify staging":    "__err__",
		"rev-parse --verify production": "__err__",
	})
	_, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(envOut(cfg), "[warn]") {
		t.Errorf("expected [warn] for missing branches; got: %q", envOut(cfg))
	}
}

// ---- scenario (m): promote to/from not configured → error ------------------

func TestEnvParity_Promote_NotConfigured(t *testing.T) {
	cfg := newEnvCfg(t, "promote")
	cfg.PromoteFrom = "dev"
	cfg.PromoteTo = "prod"
	// Only dev configured.
	if err := os.WriteFile(filepath.Join(cfg.ProjectDir, ".yakos.yml"), []byte("environments:\n  dev:\n    branch: main\n"), 0644); err != nil {
		t.Fatalf("write .yakos.yml: %v", err)
	}
	cfg.GitFn = stubEnvGit(nil)
	_, err := envcfg.Run(cfg)
	if err == nil {
		t.Fatal("expected error when prod not configured")
	}
	if !strings.Contains(err.Error(), "prod") {
		t.Errorf("expected 'prod' in error; got: %v", err)
	}
}

// ---- scenario (n): list empty environments → empty output -------------------

func TestEnvParity_List_Empty(t *testing.T) {
	cfg := newEnvCfg(t, "list")
	// No .yakos.yml → empty list.
	_, err := envcfg.Run(cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if envOut(cfg) != "" {
		t.Errorf("expected empty output for empty list; got: %q", envOut(cfg))
	}
}

// ---- scenario (o): status shows PR tool ------------------------------------

func TestEnvParity_Status_ShowsPRTool(t *testing.T) {
	cfg := newEnvCfg(t, "status")
	cfg.GitFn = stubEnvGit(map[string]string{
		"rev-parse --abbrev-ref HEAD": "main\n",
	})
	cfg.PRToolOverride = envcfg.PRToolGit
	if _, err := envcfg.Run(cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := envOut(cfg)
	if !strings.Contains(o, "PR tool detected:") {
		t.Errorf("expected 'PR tool detected:' in output; got: %q", o)
	}
}
