package metrics

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// makeGitRepo creates a minimal fake git repo structure under dir
// (just a .git/hooks directory, no actual git data needed for hook tests).
func makeGitRepo(t *testing.T) (projectDir string, hooksDir string) {
	t.Helper()
	dir := t.TempDir()
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0755); err != nil {
		t.Fatalf("makeGitRepo: %v", err)
	}
	return dir, hooks
}

// TestInstallHook_NewFile verifies that install-hook creates a new hook file
// with the shebang and managed block when no hook file existed before.
func TestInstallHook_NewFile(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	var out strings.Builder
	res, err := RunInstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
		Writer:     &out,
	})
	if err != nil {
		t.Fatalf("RunInstallHook: %v", err)
	}
	if !res.Installed {
		t.Error("expected Installed=true")
	}
	if res.HookFile != hookFile {
		t.Errorf("HookFile: got %q; want %q", res.HookFile, hookFile)
	}

	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read hook file: %v", err)
	}
	s := string(content)

	// Must have shebang.
	if !strings.HasPrefix(s, "#!/bin/sh") {
		t.Errorf("hook file missing shebang: %s", s)
	}
	// Must contain open and close sentinels.
	if !strings.Contains(s, hookBlockOpen) {
		t.Errorf("hook file missing open sentinel: %s", s)
	}
	if !strings.Contains(s, hookBlockClose) {
		t.Errorf("hook file missing close sentinel: %s", s)
	}
	// Must have --skip-analyzers (fast [E] only, never [T]).
	if !strings.Contains(s, "--skip-analyzers") {
		t.Errorf("hook file missing --skip-analyzers: %s", s)
	}
	// Must NOT have --deep.
	if strings.Contains(s, "--deep") {
		t.Errorf("hook file must NOT contain --deep: %s", s)
	}
	// Must NOT be bare collect without --skip-analyzers.
	// (Checked by verifying --skip-analyzers is present, which we already did above.)
	// Must have || true (so hook never blocks).
	if !strings.Contains(s, "|| true") {
		t.Errorf("hook file missing '|| true' safety: %s", s)
	}
	// Must propagate YAKOS_IMPL=go.
	if !strings.Contains(s, "YAKOS_IMPL=go") {
		t.Errorf("hook file missing YAKOS_IMPL=go: %s", s)
	}
	// Must have --trigger git-hook.
	if !strings.Contains(s, "--trigger git-hook") {
		t.Errorf("hook file missing '--trigger git-hook': %s", s)
	}
	// File must be executable (Unix only — Windows does not honour Unix perm bits;
	// os.WriteFile(..., 0755) is a no-op there and Mode().Perm() returns 0666).
	fi, err := os.Stat(hookFile)
	if err != nil {
		t.Fatalf("stat hook file: %v", err)
	}
	if goruntime.GOOS != "windows" && fi.Mode()&0111 == 0 {
		t.Errorf("hook file should be executable; mode: %v", fi.Mode())
	}
}

// TestInstallHook_ExistingUserContent verifies that installing over an existing
// hook file preserves the user's content and appends the managed block.
func TestInstallHook_ExistingUserContent(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	// Write a user-owned hook first.
	userContent := "#!/bin/sh\necho 'user hook'\n"
	if err := os.WriteFile(hookFile, []byte(userContent), 0755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	var out strings.Builder
	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
		Writer:     &out,
	})
	if err != nil {
		t.Fatalf("RunInstallHook: %v", err)
	}

	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	s := string(content)

	// User content must still be present.
	if !strings.Contains(s, "echo 'user hook'") {
		t.Errorf("user hook content was lost: %s", s)
	}
	// Managed block must be present.
	if !strings.Contains(s, hookBlockOpen) {
		t.Errorf("managed block open sentinel missing: %s", s)
	}
	if !strings.Contains(s, "--skip-analyzers") {
		t.Errorf("hook missing --skip-analyzers: %s", s)
	}
}

// TestInstallHook_Idempotent verifies that running install-hook twice results
// in the managed block appearing exactly once.
func TestInstallHook_Idempotent(t *testing.T) {
	projectDir, _ := makeGitRepo(t)

	cfg := InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	}

	_, err := RunInstallHook(cfg)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	_, err = RunInstallHook(cfg)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}

	hookFile := filepath.Join(projectDir, ".git", "hooks", "post-commit")
	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	s := string(content)

	// Open sentinel should appear exactly once.
	count := strings.Count(s, hookBlockOpen)
	if count != 1 {
		t.Errorf("open sentinel appears %d times (expected 1):\n%s", count, s)
	}
	// Close sentinel should appear exactly once.
	count = strings.Count(s, hookBlockClose)
	if count != 1 {
		t.Errorf("close sentinel appears %d times (expected 1):\n%s", count, s)
	}
}

// TestInstallHook_IdempotentOverExistingUser verifies idempotency when there is
// existing user content: block appears exactly once, user content intact.
func TestInstallHook_IdempotentOverExistingUser(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	userContent := "#!/bin/sh\necho 'user hook'\n"
	if err := os.WriteFile(hookFile, []byte(userContent), 0755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	cfg := InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	}

	for i := 0; i < 3; i++ {
		if _, err := RunInstallHook(cfg); err != nil {
			t.Fatalf("install #%d: %v", i+1, err)
		}
	}

	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	s := string(content)

	if strings.Count(s, hookBlockOpen) != 1 {
		t.Errorf("open sentinel appears more than once:\n%s", s)
	}
	if strings.Count(s, hookBlockClose) != 1 {
		t.Errorf("close sentinel appears more than once:\n%s", s)
	}
	if !strings.Contains(s, "echo 'user hook'") {
		t.Errorf("user hook content was lost:\n%s", s)
	}
}

// TestInstallHook_Force verifies that --force replaces the entire hook file.
func TestInstallHook_Force(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	// Write an existing complex user hook.
	existing := "#!/bin/sh\necho 'old hook'\nsome-other-command\n"
	if err := os.WriteFile(hookFile, []byte(existing), 0755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
		Force:      true,
	})
	if err != nil {
		t.Fatalf("RunInstallHook --force: %v", err)
	}

	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	s := string(content)

	// Old user content should be gone.
	if strings.Contains(s, "old hook") {
		t.Errorf("--force should have replaced user content; old content still present:\n%s", s)
	}
	if strings.Contains(s, "some-other-command") {
		t.Errorf("--force should have removed other commands:\n%s", s)
	}
	// Managed block must be present.
	if !strings.Contains(s, hookBlockOpen) {
		t.Errorf("managed block missing after --force:\n%s", s)
	}
}

// TestInstallHook_PrePush verifies that the pre-push hook is supported.
func TestInstallHook_PrePush(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "pre-push")

	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "pre-push",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	})
	if err != nil {
		t.Fatalf("RunInstallHook pre-push: %v", err)
	}

	if _, err := os.Stat(hookFile); os.IsNotExist(err) {
		t.Error("pre-push hook file not created")
	}
}

// TestInstallHook_InvalidHookName verifies that unsupported hook names are rejected.
func TestInstallHook_InvalidHookName(t *testing.T) {
	projectDir, _ := makeGitRepo(t)
	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "prepare-commit-msg",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	})
	if err == nil {
		t.Error("expected error for unsupported hook name")
	}
}

// TestInstallHook_NoDeep verifies that the installed hook command does NOT
// contain --deep (which would invoke slow LLM collectors on every commit).
func TestInstallHook_NoDeep(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	})
	if err != nil {
		t.Fatalf("RunInstallHook: %v", err)
	}

	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "--deep") {
		t.Error("installed hook must NOT contain --deep (slow LLM collectors)")
	}
}

// TestInstallHook_SkipAnalyzersPresent verifies that --skip-analyzers is in
// the managed block (fast [E] only on commit hooks).
func TestInstallHook_SkipAnalyzersPresent(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	})
	if err != nil {
		t.Fatalf("RunInstallHook: %v", err)
	}

	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	// Must have --skip-analyzers (no bare collect command without it).
	if !strings.Contains(string(content), "--skip-analyzers") {
		t.Error("installed hook must contain --skip-analyzers")
	}
}

// --- uninstall-hook tests ----------------------------------------------------

// TestUninstallHook_RemovesBlock verifies that uninstall removes the managed block
// while preserving user content.
func TestUninstallHook_RemovesBlock(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	// Write a hook that has user content + managed block.
	initial := "#!/bin/sh\necho 'user hook'\n\n" + hookBlockOpen + "\nsome-managed-line\n" + hookBlockClose + "\n"
	if err := os.WriteFile(hookFile, []byte(initial), 0755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	var out strings.Builder
	res, err := RunUninstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		Writer:     &out,
	})
	if err != nil {
		t.Fatalf("RunUninstallHook: %v", err)
	}
	if !res.Uninstalled {
		t.Error("expected Uninstalled=true")
	}
	if res.AlreadyAbsent {
		t.Error("expected AlreadyAbsent=false")
	}

	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	s := string(content)

	// Managed block must be gone.
	if strings.Contains(s, hookBlockOpen) {
		t.Errorf("managed block open sentinel still present after uninstall:\n%s", s)
	}
	if strings.Contains(s, hookBlockClose) {
		t.Errorf("managed block close sentinel still present after uninstall:\n%s", s)
	}
	if strings.Contains(s, "some-managed-line") {
		t.Errorf("managed content still present after uninstall:\n%s", s)
	}
	// User content must survive.
	if !strings.Contains(s, "echo 'user hook'") {
		t.Errorf("user content was removed by uninstall:\n%s", s)
	}
}

// TestUninstallHook_RemovesFileWhenManagedOnly verifies that if only the managed
// block remains (shebang + block), the file itself is deleted.
func TestUninstallHook_RemovesFileWhenManagedOnly(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	// Install (which creates a managed-only file).
	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	_, err = RunUninstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	if _, err := os.Stat(hookFile); !os.IsNotExist(err) {
		t.Error("hook file should have been deleted when managed-only content remains")
	}
}

// TestUninstallHook_NotInstalledNoOp verifies that uninstalling when the managed
// block is absent produces a friendly message and no error.
func TestUninstallHook_NotInstalledNoOp(t *testing.T) {
	projectDir, hooksDir := makeGitRepo(t)
	hookFile := filepath.Join(hooksDir, "post-commit")

	// Write a user hook WITHOUT a managed block.
	if err := os.WriteFile(hookFile, []byte("#!/bin/sh\necho 'user'\n"), 0755); err != nil { //nolint:gosec
		t.Fatal(err)
	}

	var out strings.Builder
	res, err := RunUninstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		Writer:     &out,
	})
	if err != nil {
		t.Fatalf("RunUninstallHook: %v", err)
	}
	if !res.AlreadyAbsent {
		t.Error("expected AlreadyAbsent=true for hook without managed block")
	}

	// User hook must be untouched.
	content, err := os.ReadFile(hookFile) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "echo 'user'") {
		t.Error("user hook content was modified by uninstall no-op")
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("expected 'nothing to do' message; got: %s", out.String())
	}
}

// TestUninstallHook_FileAbsentNoOp verifies that uninstalling when there is no
// hook file at all is a friendly no-op.
func TestUninstallHook_FileAbsentNoOp(t *testing.T) {
	projectDir, _ := makeGitRepo(t)

	var out strings.Builder
	res, err := RunUninstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		Writer:     &out,
	})
	if err != nil {
		t.Fatalf("RunUninstallHook (no file): %v", err)
	}
	if !res.AlreadyAbsent {
		t.Error("expected AlreadyAbsent=true when hook file does not exist")
	}
	if !strings.Contains(out.String(), "nothing to do") {
		t.Errorf("expected 'nothing to do' message; got: %s", out.String())
	}
}

// TestUninstallHook_Idempotent verifies that running uninstall-hook twice is safe.
func TestUninstallHook_Idempotent(t *testing.T) {
	projectDir, _ := makeGitRepo(t)

	// Install first.
	_, err := RunInstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		YakosBin:   "/usr/local/bin/yakos",
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	// Uninstall once.
	res1, err := RunUninstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("first uninstall: %v", err)
	}
	if !res1.Uninstalled {
		t.Error("first uninstall: expected Uninstalled=true")
	}

	// Uninstall again — must be a no-op.
	var out strings.Builder
	res2, err := RunUninstallHook(InstallHookConfig{
		HookName:   "post-commit",
		ProjectDir: projectDir,
		Writer:     &out,
	})
	if err != nil {
		t.Fatalf("second uninstall: %v", err)
	}
	if !res2.AlreadyAbsent {
		t.Error("second uninstall: expected AlreadyAbsent=true")
	}
}

// --- ParseArgs for install-hook / uninstall-hook -----------------------------

// TestParseArgs_InstallHook verifies that install-hook is parsed into the right
// config (and wired out of the stub group).
func TestParseArgs_InstallHook(t *testing.T) {
	cfg, err := ParseArgs([]string{"install-hook"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs install-hook: %v", err)
	}
	if cfg.Subcommand != "install-hook" {
		t.Errorf("Subcommand: got %q; want install-hook", cfg.Subcommand)
	}
}

func TestParseArgs_InstallHook_Flags(t *testing.T) {
	cfg, err := ParseArgs([]string{"install-hook", "--pre-push", "--force"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Subcommand != "install-hook" {
		t.Errorf("Subcommand: got %q; want install-hook", cfg.Subcommand)
	}
	if !cfg.HookForce {
		t.Error("expected HookForce=true")
	}
	if cfg.HookName != "pre-push" {
		t.Errorf("HookName: got %q; want pre-push", cfg.HookName)
	}
}

func TestParseArgs_UninstallHook(t *testing.T) {
	cfg, err := ParseArgs([]string{"uninstall-hook", "--post-commit"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs uninstall-hook: %v", err)
	}
	if cfg.Subcommand != "uninstall-hook" {
		t.Errorf("Subcommand: got %q; want uninstall-hook", cfg.Subcommand)
	}
	if cfg.HookName != "post-commit" {
		t.Errorf("HookName: got %q; want post-commit", cfg.HookName)
	}
}

// --- isShebangOnly unit tests ------------------------------------------------

func TestIsShebangOnly(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", true},
		{"whitespace", "   \n\n  ", true},
		{"shebang only", "#!/bin/sh\n", true},
		{"shebang + blank", "#!/bin/sh\n\n", true},
		{"user content", "#!/bin/sh\necho hi\n", false},
		{"user content no shebang", "echo hi\n", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isShebangOnly(tc.content); got != tc.want {
				t.Errorf("isShebangOnly(%q) = %v; want %v", tc.content, got, tc.want)
			}
		})
	}
}
