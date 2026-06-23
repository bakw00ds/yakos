package framework_test

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/framework"
)

// TestHasEmbeddedLib verifies the function returns a bool without panicking.
// In a bare checkout (no `make embed-lib`) it should return false; in a
// release build it should return true.  We don't hard-assert either direction
// because bare `go test` must pass.
func TestHasEmbeddedLib(t *testing.T) {
	_ = framework.HasEmbeddedLib() // must not panic
}

// TestDriftGuard asserts that, when HasEmbeddedLib() is true (i.e. the binary
// was built after `make embed-lib`), the embedded FS contains the key agent
// and rule files that constitute a correct release build.
//
// When HasEmbeddedLib() is false (bare dev build with only placeholders) the
// test skips gracefully, so bare `go test ./...` still passes.
func TestDriftGuard(t *testing.T) {
	if !framework.HasEmbeddedLib() {
		t.Skip("embedded lib not present (run `make embed-lib` first); skipping drift guard")
	}

	libFS := framework.LibFS()

	// Required agents.
	requiredAgents := []string{
		"embedded/agents/backend.md",
		"embedded/agents/security-reviewer.md",
		"embedded/agents/frontend.md",
		"embedded/agents/architect.md",
	}
	for _, path := range requiredAgents {
		if _, err := libFS.Open(path); err != nil {
			t.Errorf("drift guard: required agent missing: %s", path)
		}
	}

	// Required rules.
	requiredRules := []string{
		"embedded/rules/commit-format.md",
		"embedded/rules/git-hygiene.md",
		"embedded/rules/lead-dispatch-discipline.md",
	}
	for _, path := range requiredRules {
		if _, err := libFS.Open(path); err != nil {
			t.Errorf("drift guard: required rule missing: %s", path)
		}
	}

	// Agent count — must have at least 10 to catch a partial copy.
	agentCount := 0
	_ = fs.WalkDir(libFS, "embedded/agents", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			agentCount++
		}
		return nil
	})
	if agentCount < 10 {
		t.Errorf("drift guard: expected ≥10 agents in embedded FS, got %d", agentCount)
	}

	// Hooks must be present.
	hookCount := 0
	_ = fs.WalkDir(libFS, "embedded/hooks", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".sh") {
			hookCount++
		}
		return nil
	})
	if hookCount < 5 {
		t.Errorf("drift guard: expected ≥5 hook scripts in embedded FS, got %d", hookCount)
	}
}

// TestDriftGuard_SettingsPresent asserts that the embedded FS includes the
// settings subdirectory and its two required template files.  This test will
// FAIL (not skip) if settings is ever accidentally dropped from embed-lib.
func TestDriftGuard_SettingsPresent(t *testing.T) {
	if !framework.HasEmbeddedLib() {
		t.Skip("embedded lib not present (run `make embed-lib` first); skipping settings drift guard")
	}

	libFS := framework.LibFS()

	required := []string{
		"embedded/settings/settings.template.json",
		"embedded/settings/soul.template.md",
	}
	for _, path := range required {
		if _, err := libFS.Open(path); err != nil {
			t.Errorf("drift guard: required settings file missing: %s", path)
		}
	}
}

// TestMaterializeLib_NoOp verifies that MaterializeLib is a no-op when the
// embedded lib is absent (bare checkout).
func TestMaterializeLib_NoOp(t *testing.T) {
	if framework.HasEmbeddedLib() {
		t.Skip("embedded lib is present; skipping no-op test")
	}

	tmp := t.TempDir()
	if err := framework.MaterializeLib(tmp); err != nil {
		t.Fatalf("MaterializeLib returned unexpected error on bare build: %v", err)
	}

	// lib/ should not have been created.
	entries, _ := fs.ReadDir(os.DirFS(tmp), ".")
	if len(entries) != 0 {
		t.Errorf("expected no files written on bare build, but got %v", entries)
	}
}
