package serve_test

// bash_wiring_test.go — verifies that serve.Config.ConsoleAllowBash is
// correctly threaded through BuildConsoleCfgForTest into
// consoleui.Config.AllowNetworkedBash.
//
// This is a compile+runtime guard: if either field is renamed or the wiring
// in export_test.go BuildConsoleCfgForTest is dropped, this test fails.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bakw00ds/yakos/internal/serve"
)

// repoRootForBashWiring walks up from the test file to find the repo root.
func repoRootForBashWiring(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		fi, err := os.Stat(filepath.Join(dir, "VERSION"))
		if err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

// TestConsoleAllowBash_WiredIntoConsoleCfg verifies that ConsoleAllowBash=true
// in serve.Config flows into AllowNetworkedBash=true in the consoleui.Config
// returned by BuildConsoleCfgForTest.
func TestConsoleAllowBash_WiredIntoConsoleCfg(t *testing.T) {
	t.Parallel()

	yakosRoot := repoRootForBashWiring(t)
	workspaceRoot := t.TempDir()

	cfg := serve.Config{
		WorkspaceRoot:    workspaceRoot,
		YakosRoot:        yakosRoot,
		ConsoleAllowBash: true,
	}

	consoleCfg := serve.BuildConsoleCfgForTest(cfg)
	if !consoleCfg.AllowNetworkedBash {
		t.Error("BuildConsoleCfgForTest: AllowNetworkedBash=false; want true when ConsoleAllowBash=true")
	}
}

// TestConsoleAllowBash_DefaultFalse verifies that ConsoleAllowBash defaults to
// false (off-by-default RCE guard).
func TestConsoleAllowBash_DefaultFalse(t *testing.T) {
	t.Parallel()

	yakosRoot := repoRootForBashWiring(t)
	workspaceRoot := t.TempDir()

	cfg := serve.Config{
		WorkspaceRoot: workspaceRoot,
		YakosRoot:     yakosRoot,
		// ConsoleAllowBash not set — must default to false.
	}

	consoleCfg := serve.BuildConsoleCfgForTest(cfg)
	if consoleCfg.AllowNetworkedBash {
		t.Error("BuildConsoleCfgForTest: AllowNetworkedBash=true; want false by default (off-by-default RCE guard)")
	}
}
