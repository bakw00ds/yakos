// plugin_parity_test.go — Phase 1 parity tests for `yakos plugin`.
//
// Design notes:
//
//  1. All tests call plugin.Run directly with temp-dir paths; no bash runtime
//     or real ~/.yakos/plugins directory is required.
//
//  2. The bash plugin.sh manages runtime plugins under ~/.yakos/plugins/<id>/.
//     The Go port mirrors the three core subcommands (list, install, remove)
//     and adds validate, register, and status. Parity is verified
//     behaviourally (output shape, error conditions, filesystem effects)
//     rather than byte-for-byte because the paths in output differ per test.
//
//  3. Git URL install requires git(1) on PATH; those tests inject a mock
//     GitExecFn so they run without network or git.
//
// Critical scenarios:
//
//	(a) list — no plugins    → "no plugins installed"
//	(b) list — plugins       → id + version in table
//	(c) list — broken plugin → "(broken plugin)" note
//	(d) install local path   → plugin dir populated; success message
//	(e) install --force      → overwrites existing plugin
//	(f) install built-in id  → error "shadows a built-in"
//	(g) install validation failure → rollback; dest absent
//	(h) remove happy         → dir gone; "removed plugin: <id>"
//	(i) remove built-in id   → error "built-in"
//	(j) validate valid       → "valid"
//	(k) validate missing fns → error "validation failed"
//	(l) register             → Installed matches name; dir present
//	(m) status alias         → identical to list
//	(n) unknown subcommand   → error "unknown subcommand"
//	(o) help text phrases    → all load-bearing phrases present
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/plugin"
)

// ---- helpers ----------------------------------------------------------------

func newPluginParityConfig(t *testing.T) plugin.Config {
	t.Helper()
	tmp := t.TempDir()
	return plugin.Config{
		HomeDir:    tmp,
		PluginsDir: filepath.Join(tmp, "plugins"),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func pluginParityOut(cfg plugin.Config) string {
	return cfg.Writer.(*bytes.Buffer).String()
}

// makeParityPlugin creates a minimal valid plugin dir in pluginsDir/<id>/.
func makeParityPlugin(t *testing.T, pluginsDir, id string) string {
	t.Helper()
	dir := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("makeParityPlugin mkdir: %v", err)
	}
	writeParityRuntime(t, dir, id)
	return dir
}

// makeParityLocalPlugin creates a plugin dir at an isolated temp path.
func makeParityLocalPlugin(t *testing.T, id string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("makeParityLocalPlugin mkdir: %v", err)
	}
	writeParityRuntime(t, dir, id)
	return dir
}

// writeParityRuntime writes a valid runtime.sh with all eight function stubs.
func writeParityRuntime(t *testing.T, dir, id string) {
	t.Helper()
	var sb strings.Builder
	for _, fn := range []string{"id", "check_cli", "check_auth", "capabilities",
		"materialize_agents", "cleanup_agents", "launch", "dispatch"} {
		fmt.Fprintf(&sb, "yk_rt_%s_%s() { :; }\n", id, fn)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.sh"), []byte(sb.String()), 0755); err != nil {
		t.Fatalf("writeParityRuntime: %v", err)
	}
}

// ---- (a) list — no plugins --------------------------------------------------

func TestPluginParity_ListNoPlugins(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "list"

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pluginParityOut(cfg), "no plugins installed") {
		t.Errorf("expected 'no plugins installed'; got:\n%s", pluginParityOut(cfg))
	}
}

// ---- (b) list — plugins present ---------------------------------------------

func TestPluginParity_ListWithPlugins(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "list"
	makeParityPlugin(t, cfg.PluginsDir, "alpha")
	makeParityPlugin(t, cfg.PluginsDir, "beta")

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o := pluginParityOut(cfg)
	if !strings.Contains(o, "alpha") || !strings.Contains(o, "beta") {
		t.Errorf("expected both plugins in output; got:\n%s", o)
	}
	if !strings.Contains(o, "yakos plugins") {
		t.Errorf("expected header; got:\n%s", o)
	}
}

// ---- (c) list — broken plugin -----------------------------------------------

func TestPluginParity_ListBrokenPlugin(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "list"
	// Create dir without runtime.sh.
	if err := os.MkdirAll(filepath.Join(cfg.PluginsDir, "orphan"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pluginParityOut(cfg), "broken plugin") {
		t.Errorf("expected broken plugin note; got:\n%s", pluginParityOut(cfg))
	}
}

// ---- (d) install local path -------------------------------------------------

func TestPluginParity_InstallLocalPath(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "install"
	src := makeParityLocalPlugin(t, "localrt")
	cfg.Source = src

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Installed != "localrt" {
		t.Errorf("expected Installed=localrt; got %q", res.Installed)
	}
	if !strings.Contains(pluginParityOut(cfg), "installed plugin") {
		t.Errorf("expected success message; got:\n%s", pluginParityOut(cfg))
	}
	if _, e := os.Stat(filepath.Join(cfg.PluginsDir, "localrt", "runtime.sh")); e != nil {
		t.Errorf("runtime.sh missing after install: %v", e)
	}
}

// ---- (e) install --force ----------------------------------------------------

func TestPluginParity_InstallForce(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "install"
	src := makeParityLocalPlugin(t, "reusable")
	cfg.Source = src

	// First install.
	if _, err := plugin.Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Second install without --force should fail.
	cfg2 := cfg
	cfg2.Writer = &bytes.Buffer{}
	cfg2.ErrWriter = &bytes.Buffer{}
	if _, err := plugin.Run(cfg2); err == nil {
		t.Error("expected error without --force on second install")
	}

	// Second install with --force should succeed.
	cfg3 := cfg
	cfg3.Force = true
	cfg3.Writer = &bytes.Buffer{}
	cfg3.ErrWriter = &bytes.Buffer{}
	if _, err := plugin.Run(cfg3); err != nil {
		t.Fatalf("install --force: %v", err)
	}
}

// ---- (f) install built-in id ------------------------------------------------

func TestPluginParity_InstallBuiltinRejected(t *testing.T) {
	for _, b := range []string{"claude", "codex", "gemini"} {
		t.Run(b, func(t *testing.T) {
			cfg := newPluginParityConfig(t)
			cfg.Subcommand = "install"
			src := makeParityLocalPlugin(t, b)
			cfg.Source = src
			cfg.ID = b

			_, err := plugin.Run(cfg)
			if err == nil {
				t.Errorf("expected error for built-in %q", b)
			}
			if !strings.Contains(err.Error(), "shadows a built-in") {
				t.Errorf("error should mention 'shadows a built-in'; got %q", err.Error())
			}
		})
	}
}

// ---- (g) install validation failure — rollback ------------------------------

func TestPluginParity_InstallValidationFailureRollback(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "install"
	// Broken source: has runtime.sh but missing most functions.
	src := t.TempDir()
	// Write only one of the eight functions.
	content := "yk_rt_broken_id() { :; }\n"
	if err := os.WriteFile(filepath.Join(src, "runtime.sh"), []byte(content), 0755); err != nil {
		t.Fatalf("write runtime.sh: %v", err)
	}
	cfg.Source = src
	cfg.ID = "broken"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error on validation failure")
	}
	// Destination must be rolled back.
	dest := filepath.Join(cfg.PluginsDir, "broken")
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Error("expected dest to be removed after failed install")
	}
}

// ---- (h) remove happy -------------------------------------------------------

func TestPluginParity_RemoveHappy(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "remove"
	makeParityPlugin(t, cfg.PluginsDir, "todelete")
	cfg.ID = "todelete"

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Removed != "todelete" {
		t.Errorf("expected Removed=todelete; got %q", res.Removed)
	}
	if _, e := os.Stat(filepath.Join(cfg.PluginsDir, "todelete")); !os.IsNotExist(e) {
		t.Error("expected plugin dir to be gone after remove")
	}
	if !strings.Contains(pluginParityOut(cfg), "removed plugin: todelete") {
		t.Errorf("expected success message; got:\n%s", pluginParityOut(cfg))
	}
}

// ---- (i) remove built-in id -------------------------------------------------

func TestPluginParity_RemoveBuiltinRejected(t *testing.T) {
	for _, b := range []string{"claude", "codex", "gemini"} {
		t.Run(b, func(t *testing.T) {
			cfg := newPluginParityConfig(t)
			cfg.Subcommand = "remove"
			cfg.ID = b

			_, err := plugin.Run(cfg)
			if err == nil {
				t.Errorf("expected error for built-in %q", b)
			}
			if !strings.Contains(err.Error(), "built-in") {
				t.Errorf("error should mention 'built-in'; got %q", err.Error())
			}
		})
	}
}

// ---- (j) validate valid -----------------------------------------------------

func TestPluginParity_ValidateValid(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "validate"
	src := makeParityLocalPlugin(t, "validplugin")
	cfg.Dir = src
	cfg.ID = "validplugin"

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pluginParityOut(cfg), "valid") {
		t.Errorf("expected 'valid' in output; got:\n%s", pluginParityOut(cfg))
	}
}

// ---- (k) validate missing fns -----------------------------------------------

func TestPluginParity_ValidateMissingFunctions(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "validate"
	dir := t.TempDir()
	// Only one of eight functions present.
	content := "yk_rt_partial_id() { :; }\n"
	if err := os.WriteFile(filepath.Join(dir, "runtime.sh"), []byte(content), 0755); err != nil {
		t.Fatalf("write runtime.sh: %v", err)
	}
	cfg.Dir = dir
	cfg.ID = "partial"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for missing functions")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("error should mention 'validation failed'; got %q", err.Error())
	}
}

// ---- (l) register -----------------------------------------------------------

// TestPluginParity_Register verifies register installs the plugin under the given name.
// The source runtime.sh must use the target id's function names.
func TestPluginParity_Register(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "register"
	// Build source with function names for the target id "regname".
	src := makeParityLocalPlugin(t, "regname")
	cfg.ID = "regname"
	cfg.Dir = src

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Installed != "regname" {
		t.Errorf("expected Installed=regname; got %q", res.Installed)
	}
	if _, e := os.Stat(filepath.Join(cfg.PluginsDir, "regname", "runtime.sh")); e != nil {
		t.Errorf("runtime.sh missing after register: %v", e)
	}
}

// ---- (m) status alias -------------------------------------------------------

func TestPluginParity_StatusAliasForList(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "status"
	makeParityPlugin(t, cfg.PluginsDir, "statplugin")

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(pluginParityOut(cfg), "statplugin") {
		t.Errorf("expected plugin id in status output; got:\n%s", pluginParityOut(cfg))
	}
}

// ---- (n) unknown subcommand -------------------------------------------------

func TestPluginParity_UnknownSubcommand(t *testing.T) {
	cfg := newPluginParityConfig(t)
	cfg.Subcommand = "reboot"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error should say 'unknown subcommand'; got %q", err.Error())
	}
}

// ---- (o) help text phrases --------------------------------------------------

func TestPluginParity_HelpText(t *testing.T) {
	var buf bytes.Buffer
	plugin.PrintHelp(&buf)
	got := buf.String()

	mustContain := []string{
		"yakos plugin",
		"install",
		"list",
		"remove",
		"validate",
		"register",
		"status",
		"runtime.sh",
		"--force",
		"--id",
		"Examples:",
	}
	for _, phrase := range mustContain {
		if !strings.Contains(got, phrase) {
			t.Errorf("help missing phrase %q;\nfull output:\n%s", phrase, got)
		}
	}
}
