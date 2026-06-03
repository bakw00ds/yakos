// completion_parity_test.go — Phase 1 parity tests for `yakos completion`.
//
// Design notes:
//
//  1. All tests call completion.Run directly with injected Environ and HomeDir.
//
//  2. Parity is verified behaviourally: output shape, file placement, error
//     conditions.
//
// Critical scenarios:
//
//	(a) completion entry in portedCommands
//	(b) bash subcommand emits non-empty output
//	(c) zsh subcommand emits non-empty output
//	(d) fish subcommand emits non-empty output
//	(e) install detects bash from $SHELL
//	(f) install detects zsh from $SHELL
//	(g) install writes file at expected default path (bash)
//	(h) install writes file at expected default path (zsh)
//	(i) YAKOS_COMPLETION_SHELL overrides $SHELL detection
//	(j) unknown subcommand → error
//	(k) help output contains key phrases
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/completion"
)

// ---- scenario (a): portedCommands entry -------------------------------------

func TestCompletionParity_PortedCommandEntry(t *testing.T) {
	for _, cmd := range portedCommands {
		if cmd.Name == "completion" {
			return
		}
	}
	t.Error("expected 'completion' in portedCommands; not found")
}

// ---- helpers ----------------------------------------------------------------

func newCompletionCfg(t *testing.T, sub string) completion.Config {
	t.Helper()
	return completion.Config{
		Subcommand: sub,
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
		Environ:    func(string) string { return "" },
	}
}

func completionOut(cfg completion.Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func completionEnvWith(pairs ...string) func(string) string {
	m := make(map[string]string)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(key string) string { return m[key] }
}

// ---- scenario (b): bash emits non-empty output -----------------------------

func TestCompletionParity_Bash_NonEmpty(t *testing.T) {
	cfg := newCompletionCfg(t, "bash")
	if _, err := completion.Run(cfg); err != nil {
		t.Fatalf("bash: %v", err)
	}
	if len(completionOut(cfg)) == 0 {
		t.Error("bash output is empty")
	}
}

// ---- scenario (c): zsh emits non-empty output ------------------------------

func TestCompletionParity_Zsh_NonEmpty(t *testing.T) {
	cfg := newCompletionCfg(t, "zsh")
	if _, err := completion.Run(cfg); err != nil {
		t.Fatalf("zsh: %v", err)
	}
	if len(completionOut(cfg)) == 0 {
		t.Error("zsh output is empty")
	}
}

// ---- scenario (d): fish emits non-empty output -----------------------------

func TestCompletionParity_Fish_NonEmpty(t *testing.T) {
	cfg := newCompletionCfg(t, "fish")
	if _, err := completion.Run(cfg); err != nil {
		t.Fatalf("fish: %v", err)
	}
	if len(completionOut(cfg)) == 0 {
		t.Error("fish output is empty")
	}
}

// ---- scenario (e): install detects bash ------------------------------------

func TestCompletionParity_Install_DetectsBash(t *testing.T) {
	cfg := newCompletionCfg(t, "install")
	cfg.Environ = completionEnvWith("SHELL", "/bin/bash")
	res, err := completion.Run(cfg)
	if err != nil {
		t.Fatalf("install bash: %v", err)
	}
	if res.Shell != "bash" {
		t.Errorf("expected shell=bash; got %q", res.Shell)
	}
}

// ---- scenario (f): install detects zsh ------------------------------------

func TestCompletionParity_Install_DetectsZsh(t *testing.T) {
	cfg := newCompletionCfg(t, "install")
	cfg.Environ = completionEnvWith("SHELL", "/bin/zsh")
	res, err := completion.Run(cfg)
	if err != nil {
		t.Fatalf("install zsh: %v", err)
	}
	if res.Shell != "zsh" {
		t.Errorf("expected shell=zsh; got %q", res.Shell)
	}
}

// ---- scenario (g): install bash writes to expected default path ------------

func TestCompletionParity_Install_Bash_DefaultPath(t *testing.T) {
	cfg := newCompletionCfg(t, "install")
	cfg.Environ = completionEnvWith("SHELL", "/bin/bash")
	res, err := completion.Run(cfg)
	if err != nil {
		t.Fatalf("install bash: %v", err)
	}
	if !strings.HasSuffix(res.InstalledPath, "yakos") {
		t.Errorf("expected path ending in 'yakos'; got %q", res.InstalledPath)
	}
	if _, err := os.Stat(res.InstalledPath); err != nil {
		t.Errorf("installed file missing: %v", err)
	}
}

// ---- scenario (h): install zsh writes to expected default path -------------

func TestCompletionParity_Install_Zsh_DefaultPath(t *testing.T) {
	cfg := newCompletionCfg(t, "install")
	cfg.Environ = completionEnvWith("SHELL", "/bin/zsh")
	res, err := completion.Run(cfg)
	if err != nil {
		t.Fatalf("install zsh: %v", err)
	}
	if !strings.HasSuffix(res.InstalledPath, "_yakos") {
		t.Errorf("expected path ending in '_yakos'; got %q", res.InstalledPath)
	}
}

// ---- scenario (i): YAKOS_COMPLETION_SHELL overrides $SHELL -----------------

func TestCompletionParity_Install_EnvOverride(t *testing.T) {
	cfg := newCompletionCfg(t, "install")
	cfg.Environ = completionEnvWith("SHELL", "/bin/zsh", "YAKOS_COMPLETION_SHELL", "bash")
	res, err := completion.Run(cfg)
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if res.Shell != "bash" {
		t.Errorf("expected shell=bash after override; got %q", res.Shell)
	}
}

// ---- scenario (j): unknown subcommand → error ------------------------------

func TestCompletionParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newCompletionCfg(t, "bogus")
	_, err := completion.Run(cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (k): help output contains key phrases -----------------------

func TestCompletionParity_Help_KeyPhrases(t *testing.T) {
	var buf bytes.Buffer
	completion.PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos completion",
		"bash",
		"zsh",
		"fish",
		"install",
		"fpath",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing phrase %q", phrase)
		}
	}
}
