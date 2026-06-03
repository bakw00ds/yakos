// Package completion tests — Phase 1 parity coverage for `yakos completion`.
//
// Design:
//   - All tests use Config injection; no real filesystem side-effects beyond t.TempDir().
//   - Environ is always injected to control shell detection without needing
//     a real shell environment.
//   - Embedded templates are verified to be non-empty and contain expected
//     shell-specific markers.
//
// Scenarios:
//
//	(a) bash subcommand emits non-empty bash template
//	(b) zsh subcommand emits non-empty zsh template
//	(c) fish subcommand emits non-empty fish template
//	(d) bash template contains bash-specific marker (_yakos function)
//	(e) zsh template contains zsh-specific marker (#compdef yakos)
//	(f) fish template contains fish-specific marker (complete -c yakos)
//	(g) install: bash shell detected from $SHELL
//	(h) install: zsh shell detected from $SHELL
//	(i) install: fish shell detected from $SHELL
//	(j) install: YAKOS_COMPLETION_SHELL overrides $SHELL
//	(k) install: ShellOverride field overrides env
//	(l) install: bash writes to BASH_COMPLETION_USER_DIR when set
//	(m) install: bash writes to ~/.local/share/bash-completion/completions/yakos by default
//	(n) install: zsh writes to YAKOS_ZSH_COMPDIR when set
//	(o) install: zsh writes to ~/.zsh/completions/_yakos by default
//	(p) install: fish writes to XDG_CONFIG_HOME/fish/completions/yakos.fish
//	(q) install: fish writes to ~/.config/fish/completions/yakos.fish by default
//	(r) install: unknown shell → error
//	(s) install: undetectable shell → error
//	(t) install: bash output contains source-line hint
//	(u) install: zsh output contains fpath hint
//	(v) install: fish output contains auto-source note
//	(w) unknown subcommand → error
//	(x) help subcommand → prints help, no error
//	(y) PrintHelp contains key phrases
//	(z) installed file is readable and non-empty
//	(aa) Result.Shell set correctly on bash install
//	(ab) Result.InstalledPath non-empty on install
package completion

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- helpers ----------------------------------------------------------------

func newCfg(t *testing.T, sub string) Config {
	t.Helper()
	home := t.TempDir()
	return Config{
		Subcommand: sub,
		HomeDir:    home,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
		Environ:    func(string) string { return "" },
	}
}

func cfgOut(cfg Config) string { return cfg.Writer.(*bytes.Buffer).String() }

func envWith(pairs ...string) func(string) string {
	m := make(map[string]string)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(key string) string { return m[key] }
}

// ---- scenario (a): bash emits non-empty output -----------------------------

func TestCompletionParity_Bash_NonEmpty(t *testing.T) {
	cfg := newCfg(t, "bash")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("bash: %v", err)
	}
	if res.Shell != "bash" {
		t.Errorf("expected shell=bash; got %q", res.Shell)
	}
	if len(cfgOut(cfg)) == 0 {
		t.Error("bash output is empty")
	}
}

// ---- scenario (b): zsh emits non-empty output ------------------------------

func TestCompletionParity_Zsh_NonEmpty(t *testing.T) {
	cfg := newCfg(t, "zsh")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("zsh: %v", err)
	}
	if res.Shell != "zsh" {
		t.Errorf("expected shell=zsh; got %q", res.Shell)
	}
	if len(cfgOut(cfg)) == 0 {
		t.Error("zsh output is empty")
	}
}

// ---- scenario (c): fish emits non-empty output -----------------------------

func TestCompletionParity_Fish_NonEmpty(t *testing.T) {
	cfg := newCfg(t, "fish")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("fish: %v", err)
	}
	if res.Shell != "fish" {
		t.Errorf("expected shell=fish; got %q", res.Shell)
	}
	if len(cfgOut(cfg)) == 0 {
		t.Error("fish output is empty")
	}
}

// ---- scenario (d): bash template has bash marker --------------------------

func TestCompletionParity_BashTemplate_HasMarker(t *testing.T) {
	cfg := newCfg(t, "bash")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("bash: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "_yakos") {
		t.Errorf("bash template missing _yakos function; got: %q", out[:min(200, len(out))])
	}
}

// ---- scenario (e): zsh template has zsh marker ----------------------------

func TestCompletionParity_ZshTemplate_HasMarker(t *testing.T) {
	cfg := newCfg(t, "zsh")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("zsh: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "#compdef yakos") {
		t.Errorf("zsh template missing #compdef yakos; snippet: %q", out[:min(200, len(out))])
	}
}

// ---- scenario (f): fish template has fish marker --------------------------

func TestCompletionParity_FishTemplate_HasMarker(t *testing.T) {
	cfg := newCfg(t, "fish")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("fish: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "complete -c yakos") {
		t.Errorf("fish template missing 'complete -c yakos'; snippet: %q", out[:min(200, len(out))])
	}
}

// ---- scenario (g): install detects bash from $SHELL -----------------------

func TestCompletionParity_Install_DetectsBash(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/bash")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install bash: %v", err)
	}
	if res.Shell != "bash" {
		t.Errorf("expected shell=bash; got %q", res.Shell)
	}
}

// ---- scenario (h): install detects zsh from $SHELL ------------------------

func TestCompletionParity_Install_DetectsZsh(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/zsh")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install zsh: %v", err)
	}
	if res.Shell != "zsh" {
		t.Errorf("expected shell=zsh; got %q", res.Shell)
	}
}

// ---- scenario (i): install detects fish from $SHELL -----------------------

func TestCompletionParity_Install_DetectsFish(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/usr/local/bin/fish")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install fish: %v", err)
	}
	if res.Shell != "fish" {
		t.Errorf("expected shell=fish; got %q", res.Shell)
	}
}

// ---- scenario (j): YAKOS_COMPLETION_SHELL overrides $SHELL ----------------

func TestCompletionParity_Install_EnvOverridesBASH(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/zsh", "YAKOS_COMPLETION_SHELL", "bash")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("override to bash: %v", err)
	}
	if res.Shell != "bash" {
		t.Errorf("expected shell=bash (env override); got %q", res.Shell)
	}
}

// ---- scenario (k): ShellOverride field wins over env ----------------------

func TestCompletionParity_Install_ShellOverrideField(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/bash", "YAKOS_COMPLETION_SHELL", "bash")
	cfg.ShellOverride = "zsh"
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("ShellOverride=zsh: %v", err)
	}
	if res.Shell != "zsh" {
		t.Errorf("expected shell=zsh (ShellOverride); got %q", res.Shell)
	}
}

// ---- scenario (l): bash uses BASH_COMPLETION_USER_DIR ---------------------

func TestCompletionParity_Install_Bash_CustomDir(t *testing.T) {
	customDir := t.TempDir()
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/bash", "BASH_COMPLETION_USER_DIR", customDir)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install bash custom: %v", err)
	}
	expected := filepath.Join(customDir, "yakos")
	if res.InstalledPath != expected {
		t.Errorf("expected installed at %s; got %s", expected, res.InstalledPath)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Errorf("installed file not found: %v", err)
	}
}

// ---- scenario (m): bash defaults to ~/.local/share/... --------------------

func TestCompletionParity_Install_Bash_DefaultDir(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/bash")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install bash default: %v", err)
	}
	expected := filepath.Join(cfg.HomeDir, ".local", "share", "bash-completion", "completions", "yakos")
	if res.InstalledPath != expected {
		t.Errorf("expected %s; got %s", expected, res.InstalledPath)
	}
}

// ---- scenario (n): zsh uses YAKOS_ZSH_COMPDIR ----------------------------

func TestCompletionParity_Install_Zsh_CustomDir(t *testing.T) {
	customDir := t.TempDir()
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/zsh", "YAKOS_ZSH_COMPDIR", customDir)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install zsh custom: %v", err)
	}
	expected := filepath.Join(customDir, "_yakos")
	if res.InstalledPath != expected {
		t.Errorf("expected %s; got %s", expected, res.InstalledPath)
	}
}

// ---- scenario (o): zsh defaults to ~/.zsh/completions/_yakos --------------

func TestCompletionParity_Install_Zsh_DefaultDir(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/zsh")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install zsh default: %v", err)
	}
	expected := filepath.Join(cfg.HomeDir, ".zsh", "completions", "_yakos")
	if res.InstalledPath != expected {
		t.Errorf("expected %s; got %s", expected, res.InstalledPath)
	}
}

// ---- scenario (p): fish uses XDG_CONFIG_HOME ------------------------------

func TestCompletionParity_Install_Fish_XDGDir(t *testing.T) {
	xdg := t.TempDir()
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/usr/bin/fish", "XDG_CONFIG_HOME", xdg)
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install fish XDG: %v", err)
	}
	expected := filepath.Join(xdg, "fish", "completions", "yakos.fish")
	if res.InstalledPath != expected {
		t.Errorf("expected %s; got %s", expected, res.InstalledPath)
	}
}

// ---- scenario (q): fish defaults to ~/.config/fish/completions/yakos.fish -

func TestCompletionParity_Install_Fish_DefaultDir(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/usr/local/bin/fish")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install fish default: %v", err)
	}
	expected := filepath.Join(cfg.HomeDir, ".config", "fish", "completions", "yakos.fish")
	if res.InstalledPath != expected {
		t.Errorf("expected %s; got %s", expected, res.InstalledPath)
	}
}

// ---- scenario (r): install unsupported shell → error ----------------------

func TestCompletionParity_Install_UnsupportedShell_Error(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.ShellOverride = "tcsh"
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported shell")
	}
	if !strings.Contains(err.Error(), "unsupported shell") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (s): install undetectable shell → error ---------------------

func TestCompletionParity_Install_UndetectableShell_Error(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/usr/bin/tcsh") // unknown
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for undetectable shell")
	}
	if !strings.Contains(err.Error(), "could not detect shell") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (t): bash install output has source-line hint ---------------

func TestCompletionParity_Install_Bash_SourceHint(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/bash")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install bash: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "~/.bashrc") && !strings.Contains(out, ".bashrc") {
		t.Errorf("expected .bashrc hint in output; got: %q", out)
	}
}

// ---- scenario (u): zsh install output has fpath hint ---------------------

func TestCompletionParity_Install_Zsh_FpathHint(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/zsh")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install zsh: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "fpath") {
		t.Errorf("expected fpath hint in output; got: %q", out)
	}
}

// ---- scenario (v): fish install output has auto-source note ---------------

func TestCompletionParity_Install_Fish_AutoSourceNote(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/usr/local/bin/fish")
	if _, err := Run(cfg); err != nil {
		t.Fatalf("install fish: %v", err)
	}
	out := cfgOut(cfg)
	if !strings.Contains(out, "auto-sources") {
		t.Errorf("expected 'auto-sources' in output; got: %q", out)
	}
}

// ---- scenario (w): unknown subcommand → error -----------------------------

func TestCompletionParity_UnknownSubcommand_Error(t *testing.T) {
	cfg := newCfg(t, "bogus")
	_, err := Run(cfg)
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---- scenario (x): help subcommand prints help ----------------------------

func TestCompletionParity_Help_PrintsHelp(t *testing.T) {
	cfg := newCfg(t, "help")
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	out := cfgOut(cfg)
	if len(out) == 0 {
		t.Error("expected help output; got empty string")
	}
}

// ---- scenario (y): PrintHelp key phrases ----------------------------------

func TestCompletionParity_PrintHelp_Phrases(t *testing.T) {
	var buf bytes.Buffer
	PrintHelp(&buf)
	help := buf.String()
	for _, phrase := range []string{
		"yakos completion",
		"bash",
		"zsh",
		"fish",
		"install",
		"~/.bashrc",
		"fpath",
		"compinit",
	} {
		if !strings.Contains(help, phrase) {
			t.Errorf("help missing phrase %q", phrase)
		}
	}
}

// ---- scenario (z): installed file is readable and non-empty ---------------

func TestCompletionParity_Install_FileReadable(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/bash")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	data, rerr := os.ReadFile(res.InstalledPath) //nolint:gosec
	if rerr != nil {
		t.Fatalf("read installed file: %v", rerr)
	}
	if len(data) == 0 {
		t.Error("installed file is empty")
	}
}

// ---- scenario (aa): Result.Shell on bash install --------------------------

func TestCompletionParity_Install_ResultShell_Bash(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/bash")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Shell != "bash" {
		t.Errorf("expected Result.Shell=bash; got %q", res.Shell)
	}
	if res.Subcommand != "install" {
		t.Errorf("expected Result.Subcommand=install; got %q", res.Subcommand)
	}
}

// ---- scenario (ab): Result.InstalledPath non-empty on install -------------

func TestCompletionParity_Install_ResultInstalledPath(t *testing.T) {
	cfg := newCfg(t, "install")
	cfg.Environ = envWith("SHELL", "/bin/zsh")
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.InstalledPath == "" {
		t.Error("expected non-empty InstalledPath")
	}
}

// ---- min helper for Go < 1.21 compat ----------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
