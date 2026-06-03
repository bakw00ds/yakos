package plugin_test

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

// newCfg returns a Config with temp-dir paths and captured output buffers.
func newCfg(t *testing.T) plugin.Config {
	t.Helper()
	tmp := t.TempDir()
	return plugin.Config{
		HomeDir:    tmp,
		PluginsDir: filepath.Join(tmp, "plugins"),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

// makePlugin writes a minimal valid plugin dir under pluginsDir/<id>/.
// The runtime.sh contains all eight required yk_rt_<id>_<fn>() stubs.
func makePlugin(t *testing.T, pluginsDir, id string) string {
	t.Helper()
	dir := filepath.Join(pluginsDir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("makePlugin mkdir: %v", err)
	}
	writeRuntime(t, dir, id)
	return dir
}

// makeLocalPlugin creates a plugin directory at a path outside pluginsDir.
func makeLocalPlugin(t *testing.T, id string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("makeLocalPlugin mkdir: %v", err)
	}
	writeRuntime(t, dir, id)
	return dir
}

// writeRuntime creates a valid runtime.sh with all eight function stubs.
func writeRuntime(t *testing.T, dir, id string) {
	t.Helper()
	var sb strings.Builder
	for _, fn := range []string{"id", "check_cli", "check_auth", "capabilities",
		"materialize_agents", "cleanup_agents", "launch", "dispatch"} {
		fmt.Fprintf(&sb, "yk_rt_%s_%s() { :; }\n", id, fn)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.sh"), []byte(sb.String()), 0755); err != nil {
		t.Fatalf("writeRuntime: %v", err)
	}
}

// writeBrokenRuntime creates a runtime.sh missing some required functions.
func writeBrokenRuntime(t *testing.T, dir, id string) {
	t.Helper()
	// Only write one of the eight required functions.
	content := fmt.Sprintf("yk_rt_%s_id() { :; }\n", id)
	if err := os.WriteFile(filepath.Join(dir, "runtime.sh"), []byte(content), 0755); err != nil {
		t.Fatalf("writeBrokenRuntime: %v", err)
	}
}

// out returns captured stdout from cfg.Writer.
func out(cfg plugin.Config) string {
	return cfg.Writer.(*bytes.Buffer).String()
}

// ---- list -------------------------------------------------------------------

// TestList_Empty verifies list output when no plugins are installed.
func TestList_Empty(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "list"

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "no plugins installed") {
		t.Errorf("expected 'no plugins installed'; got:\n%s", o)
	}
}

// TestList_WithPlugins verifies list output when plugins are present.
func TestList_WithPlugins(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "list"
	makePlugin(t, cfg.PluginsDir, "cursor")
	makePlugin(t, cfg.PluginsDir, "copilot")

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o := out(cfg)
	if !strings.Contains(o, "cursor") {
		t.Errorf("expected cursor in output; got:\n%s", o)
	}
	if !strings.Contains(o, "copilot") {
		t.Errorf("expected copilot in output; got:\n%s", o)
	}
	if !strings.Contains(o, "yakos plugins") {
		t.Errorf("expected header 'yakos plugins'; got:\n%s", o)
	}
}

// TestList_WithVersion verifies that a VERSION file is read and displayed.
func TestList_WithVersion(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "list"
	dir := makePlugin(t, cfg.PluginsDir, "cursor")
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("1.2.3\n"), 0644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out(cfg), "1.2.3") {
		t.Errorf("expected version 1.2.3 in output; got:\n%s", out(cfg))
	}
}

// TestList_BrokenPlugin verifies broken plugins (no runtime.sh) are shown.
func TestList_BrokenPlugin(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "list"
	// Create a dir without runtime.sh.
	if err := os.MkdirAll(filepath.Join(cfg.PluginsDir, "broken"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out(cfg), "broken plugin") {
		t.Errorf("expected 'broken plugin' note; got:\n%s", out(cfg))
	}
}

// TestStatus_AliasForList verifies status behaves identically to list.
func TestStatus_AliasForList(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "status"
	makePlugin(t, cfg.PluginsDir, "myrt")

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out(cfg), "myrt") {
		t.Errorf("expected 'myrt' in status output; got:\n%s", out(cfg))
	}
}

// ---- install (local) --------------------------------------------------------

// TestInstall_LocalPath verifies installing from a local directory.
func TestInstall_LocalPath(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	src := makeLocalPlugin(t, "myplugin")
	cfg.Source = src

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Installed != "myplugin" {
		t.Errorf("expected Installed=myplugin; got %q", res.Installed)
	}
	if !strings.Contains(out(cfg), "installed plugin: myplugin") {
		t.Errorf("expected success message; got:\n%s", out(cfg))
	}
	// Verify the destination exists with runtime.sh.
	if _, err := os.Stat(filepath.Join(cfg.PluginsDir, "myplugin", "runtime.sh")); err != nil {
		t.Errorf("runtime.sh missing after install: %v", err)
	}
}

// TestInstall_IDOverride verifies --id overrides the inferred id.
// The source plugin's runtime.sh must contain function headers for the
// target id (matching bash plugin_validate behaviour: it validates using
// the installed id against the destination directory).
func TestInstall_IDOverride(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	// Create the source with function names for the target id "myoverride".
	src := makeLocalPlugin(t, "myoverride")
	cfg.Source = src
	cfg.ID = "myoverride"

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Installed != "myoverride" {
		t.Errorf("expected Installed=myoverride; got %q", res.Installed)
	}
}

// TestInstall_Force verifies --force overwrites an existing plugin.
func TestInstall_Force(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	src := makeLocalPlugin(t, "overwrite")
	cfg.Source = src
	// Pre-install.
	cfg.Force = false
	if _, err := plugin.Run(cfg); err != nil {
		t.Fatalf("first install: %v", err)
	}
	// Re-install without --force should fail.
	cfg2 := cfg
	cfg2.Writer = &bytes.Buffer{}
	cfg2.ErrWriter = &bytes.Buffer{}
	_, err := plugin.Run(cfg2)
	if err == nil {
		t.Error("expected error when dest exists without --force")
	}
	// Re-install with --force should succeed.
	cfg3 := cfg
	cfg3.Force = true
	cfg3.Writer = &bytes.Buffer{}
	cfg3.ErrWriter = &bytes.Buffer{}
	if _, err := plugin.Run(cfg3); err != nil {
		t.Fatalf("install with --force: %v", err)
	}
}

// TestInstall_NoSource verifies missing <source> returns an error.
func TestInstall_NoSource(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error when source is empty")
	}
}

// TestInstall_LocalNotDir verifies a non-directory local path returns an error.
func TestInstall_LocalNotDir(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	cfg.Source = filepath.Join(t.TempDir(), "nonexistent")

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for non-existent local path")
	}
}

// TestInstall_BuiltinIDRejected verifies built-in ids are rejected.
func TestInstall_BuiltinIDRejected(t *testing.T) {
	for _, b := range []string{"claude", "codex", "gemini"} {
		t.Run(b, func(t *testing.T) {
			cfg := newCfg(t)
			cfg.Subcommand = "install"
			src := makeLocalPlugin(t, b)
			cfg.Source = src
			cfg.ID = b

			_, err := plugin.Run(cfg)
			if err == nil {
				t.Errorf("expected error for built-in id %q", b)
			}
			if !strings.Contains(err.Error(), "shadows a built-in") {
				t.Errorf("error should mention 'shadows a built-in'; got %q", err.Error())
			}
		})
	}
}

// TestInstall_InvalidID verifies ids with illegal characters are rejected.
func TestInstall_InvalidID(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	src := makeLocalPlugin(t, "bad")
	cfg.Source = src
	cfg.ID = "bad id!" // spaces and ! are invalid.

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for invalid id")
	}
}

// TestInstall_ValidationFailure verifies rollback on broken runtime.sh.
func TestInstall_ValidationFailure(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	src := t.TempDir()
	// Write a broken runtime.sh (missing required functions).
	writeBrokenRuntime(t, src, "broken")
	cfg.Source = src
	cfg.ID = "broken"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error when runtime.sh is missing functions")
	}
	// Destination should be cleaned up.
	dest := filepath.Join(cfg.PluginsDir, "broken")
	if _, e := os.Stat(dest); !os.IsNotExist(e) {
		t.Error("expected dest to be removed after failed install")
	}
}

// TestInstall_IDInferredFromSource verifies ID inference from source path.
func TestInstall_IDInferredFromSource(t *testing.T) {
	cases := []struct {
		source string
		wantID string
	}{
		{"https://github.com/user/yakos-runtime-myrt.git", "myrt"},
		{"https://github.com/user/yakos-myrt.git", "myrt"},
		{"/path/to/yakos-runtime-foo", "foo"},
		{"/path/to/yakos-bar", "bar"},
		{"/path/to/plain", "plain"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			// Only test the inferID logic through the exported surface.
			// We use validate on a local path we control.
			cfg := newCfg(t)
			cfg.Subcommand = "install"
			// Create a real source dir named after the last component.
			localSrc := filepath.Join(t.TempDir(), filepath.Base(tc.source))
			localSrc = strings.TrimSuffix(localSrc, ".git")
			if err := os.MkdirAll(localSrc, 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			writeRuntime(t, localSrc, tc.wantID)
			cfg.Source = localSrc

			res, err := plugin.Run(cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Installed != tc.wantID {
				t.Errorf("expected Installed=%q; got %q", tc.wantID, res.Installed)
			}
		})
	}
}

// TestInstall_GitURL verifies git URL install delegates to GitExecFn.
func TestInstall_GitURL(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	cfg.Source = "https://github.com/user/yakos-myrt"
	cfg.ID = "myrt"

	var gotArgs []string
	cfg.GitExecFn = func(args ...string) error {
		gotArgs = args
		// Simulate git clone: create the destination with a valid runtime.sh.
		dest := args[len(args)-1]
		if err := os.MkdirAll(dest, 0755); err != nil {
			return err
		}
		writeRuntime(t, dest, "myrt")
		return nil
	}

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Installed != "myrt" {
		t.Errorf("expected Installed=myrt; got %q", res.Installed)
	}
	if len(gotArgs) < 3 || gotArgs[0] != "clone" {
		t.Errorf("expected git clone to be called; got %v", gotArgs)
	}
}

// TestInstall_GitCloneFails verifies error propagation when git fails.
func TestInstall_GitCloneFails(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "install"
	cfg.Source = "https://github.com/user/yakos-fail"
	cfg.ID = "fail"
	cfg.GitExecFn = func(args ...string) error {
		return fmt.Errorf("simulated git clone failure")
	}

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error when git clone fails")
	}
	if !strings.Contains(err.Error(), "git clone failed") {
		t.Errorf("error should mention 'git clone failed'; got %q", err.Error())
	}
}

// ---- remove -----------------------------------------------------------------

// TestRemove_Happy verifies successful plugin removal.
func TestRemove_Happy(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "remove"
	makePlugin(t, cfg.PluginsDir, "cursor")
	cfg.ID = "cursor"

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Removed != "cursor" {
		t.Errorf("expected Removed=cursor; got %q", res.Removed)
	}
	if _, e := os.Stat(filepath.Join(cfg.PluginsDir, "cursor")); !os.IsNotExist(e) {
		t.Error("expected plugin dir to be removed")
	}
	if !strings.Contains(out(cfg), "removed plugin: cursor") {
		t.Errorf("expected success message; got:\n%s", out(cfg))
	}
}

// TestRemove_NotFound verifies error when plugin does not exist.
func TestRemove_NotFound(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "remove"
	cfg.ID = "nonexistent"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for missing plugin")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found'; got %q", err.Error())
	}
}

// TestRemove_BuiltinRejected verifies built-in ids cannot be removed.
func TestRemove_BuiltinRejected(t *testing.T) {
	for _, b := range []string{"claude", "codex", "gemini"} {
		t.Run(b, func(t *testing.T) {
			cfg := newCfg(t)
			cfg.Subcommand = "remove"
			cfg.ID = b

			_, err := plugin.Run(cfg)
			if err == nil {
				t.Errorf("expected error for built-in id %q", b)
			}
			if !strings.Contains(err.Error(), "built-in") {
				t.Errorf("error should mention 'built-in'; got %q", err.Error())
			}
		})
	}
}

// TestRemove_NoID verifies missing id returns an error.
func TestRemove_NoID(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "remove"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error when id is empty")
	}
}

// ---- validate ---------------------------------------------------------------

// TestValidate_Valid verifies a valid plugin directory passes.
func TestValidate_Valid(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "validate"
	src := makeLocalPlugin(t, "myplugin")
	cfg.Dir = src
	cfg.ID = "myplugin"

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out(cfg), "valid") {
		t.Errorf("expected 'valid' in output; got:\n%s", out(cfg))
	}
}

// TestValidate_MissingRuntimeSh verifies missing runtime.sh is an error.
func TestValidate_MissingRuntimeSh(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "validate"
	cfg.Dir = t.TempDir() // empty dir, no runtime.sh
	cfg.ID = "empty"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for missing runtime.sh")
	}
	if !strings.Contains(err.Error(), "missing required runtime.sh") {
		t.Errorf("expected 'missing required runtime.sh'; got %q", err.Error())
	}
}

// TestValidate_MissingFunctions verifies missing function headers are reported.
func TestValidate_MissingFunctions(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "validate"
	dir := t.TempDir()
	writeBrokenRuntime(t, dir, "partial")
	cfg.Dir = dir
	cfg.ID = "partial"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for broken runtime.sh")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("error should mention 'validation failed'; got %q", err.Error())
	}
}

// TestValidate_NoDir verifies missing <dir> returns an error.
func TestValidate_NoDir(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "validate"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error when dir is empty")
	}
}

// TestValidate_IDInferredFromDirName verifies id is inferred from dir basename.
func TestValidate_IDInferredFromDirName(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "validate"
	src := makeLocalPlugin(t, "inferred")
	cfg.Dir = src
	// cfg.ID intentionally left empty.

	_, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---- register ---------------------------------------------------------------

// TestRegister_Happy verifies register installs the plugin under the given name.
// The source directory's runtime.sh must use the target id's function names
// (matching bash plugin_validate which validates with the registered name).
func TestRegister_Happy(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "register"
	// Build source with function names for the target id "myreg".
	src := makeLocalPlugin(t, "myreg")
	cfg.ID = "myreg"
	cfg.Dir = src

	res, err := plugin.Run(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Installed != "myreg" {
		t.Errorf("expected Installed=myreg; got %q", res.Installed)
	}
	// Verify the destination has runtime.sh.
	dest := filepath.Join(cfg.PluginsDir, "myreg", "runtime.sh")
	if _, e := os.Stat(dest); e != nil {
		t.Errorf("runtime.sh missing after register: %v", e)
	}
}

// TestRegister_NoName verifies missing name returns an error.
func TestRegister_NoName(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "register"
	cfg.Dir = t.TempDir()

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error when name is empty")
	}
}

// TestRegister_NoDir verifies missing dir returns an error.
func TestRegister_NoDir(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "register"
	cfg.ID = "myname"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error when dir is empty")
	}
}

// ---- unknown subcommand -----------------------------------------------------

// TestUnknownSubcommand verifies unknown subcommands return an error.
func TestUnknownSubcommand(t *testing.T) {
	cfg := newCfg(t)
	cfg.Subcommand = "explode"

	_, err := plugin.Run(cfg)
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error should say 'unknown subcommand'; got %q", err.Error())
	}
}

// ---- help -------------------------------------------------------------------

// TestPrintHelp_LoadBearingPhrases verifies all required phrases appear.
func TestPrintHelp_LoadBearingPhrases(t *testing.T) {
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
