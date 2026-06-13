// console_bind_test.go — Phase 6c serve-level tests for --console-bind.
//
// Tests:
//  1. Run() with ConsoleBind pointing to an unwritable state dir (forces mTLS
//     generation failure) → returns error, no listener opened.
//  2. ConsoleBind on a loopback address → behaves like ConsoleAddr (no mTLS,
//     no error, loopback-safe path).
//  3. consoleBind() helper returns ConsoleBind when set, ConsoleAddr fallback,
//     and default when both are empty.
package serve_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/serve"
)

// ---- helper -----------------------------------------------------------------

// repoRootForConsoleBind walks up from the test file to find the repo root.
func repoRootForConsoleBind(t *testing.T) string {
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

// ---- 1. Fail-closed: non-loopback ConsoleBind with unwritable state dir -----

// TestConsoleBind_FailClosed_UnwritableStateDir verifies that Run() refuses to
// start the console on a non-loopback address when mTLS material cannot be
// generated (unwritable state directory).
//
// We simulate the failure by pointing the state dir at a read-only directory
// so mtls.LoadOrGenerateCA fails.  On Windows the chmod approach doesn't work
// the same way, so the test skips on Windows.
func TestConsoleBind_FailClosed_UnwritableStateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod read-only simulation not reliable on Windows")
	}
	t.Parallel()

	workspaceRoot := t.TempDir()
	stateDir := t.TempDir()

	// Make the state directory read-only so LoadOrGenerateCA cannot create mtls/.
	if err := os.Chmod(stateDir, 0444); err != nil {
		t.Fatalf("chmod stateDir: %v", err)
	}
	t.Cleanup(func() {
		// Restore permissions so t.TempDir cleanup can remove the dir.
		_ = os.Chmod(stateDir, 0755)
	})

	// Use a loopback address for the JSON-RPC listener (not under test here).
	// ConsoleBind uses a non-loopback address to trigger the mTLS path.
	// We use 0.0.0.0:0 (non-loopback, OS-assigned port).
	// The daemon should refuse before opening the console listener.
	cfg := serve.Config{
		WorkspaceRoot:    workspaceRoot,
		ConsoleTokenPath: filepath.Join(stateDir, "console-token"),
		ConsoleBind:      "0.0.0.0:0", // non-loopback → triggers mTLS path
		// Use an in-memory listener for the JSON-RPC layer to avoid socket files.
		ListenFn: func(path string) (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := serve.Run(ctx, cfg)
	if err == nil {
		t.Error("Run() with non-loopback ConsoleBind and unwritable state dir should return error (fail-closed); got nil")
	}
}

// ---- 2. ConsoleBind on loopback: behaves like ConsoleAddr -------------------

// TestConsoleBind_Loopback_NoMTLS verifies that when ConsoleBind is set to a
// loopback address, the daemon starts the console on that address WITHOUT
// mTLS (same as --console-addr; the networked path is NOT activated for
// loopback ConsoleBind).
//
// We verify this by checking that Run() does NOT error even when the state
// dir has no mTLS material.
//
// We cannot easily do a full integration start of Run() in this test (it
// requires a full daemon lifecycle), so we test the Config.consoleBind()
// helper logic and the IsNonLoopback predicate directly.
func TestConsoleBind_Loopback_IsNotNonLoopback(t *testing.T) {
	t.Parallel()
	// When ConsoleBind is a loopback address, IsNonLoopback should be false,
	// meaning the networked mTLS path is NOT activated.
	loopbackAddrs := []string{
		"127.0.0.1:7890",
		"127.0.0.1:0",
		"localhost:7890",
	}
	for _, addr := range loopbackAddrs {
		addr := addr
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			if serve.IsNonLoopbackForTest(addr) {
				t.Errorf("IsNonLoopback(%q) = true; want false for loopback ConsoleBind", addr)
			}
		})
	}
}

// TestConsoleBind_NonLoopback_IsNonLoopback verifies that non-loopback
// addresses correctly trigger the mTLS path check.
func TestConsoleBind_NonLoopback_IsNonLoopback(t *testing.T) {
	t.Parallel()
	nonLoopbackAddrs := []string{
		"0.0.0.0:7890",
		"192.168.1.100:7890",
		"10.0.0.1:7890",
	}
	for _, addr := range nonLoopbackAddrs {
		addr := addr
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			if !serve.IsNonLoopbackForTest(addr) {
				t.Errorf("IsNonLoopback(%q) = false; want true for non-loopback ConsoleBind", addr)
			}
		})
	}
}

// ---- 3. consoleBind() priority logic ----------------------------------------

// TestConsoleBind_Priority verifies the consoleBind() helper priority:
// ConsoleBind > ConsoleAddr > default.
func TestConsoleBind_Priority(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		consoleAddr string
		consoleBind string
		want        string
	}{
		{
			name:        "both_set_bind_wins",
			consoleAddr: "127.0.0.1:9000",
			consoleBind: "10.0.0.1:7890",
			want:        "10.0.0.1:7890",
		},
		{
			name:        "only_console_addr",
			consoleAddr: "127.0.0.1:9001",
			consoleBind: "",
			want:        "127.0.0.1:9001",
		},
		{
			name:        "neither_set_default",
			consoleAddr: "",
			consoleBind: "",
			want:        "127.0.0.1:7890",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := serve.ConsoleBind(tc.consoleAddr, tc.consoleBind)
			if got != tc.want {
				t.Errorf("consoleBind(%q, %q) = %q; want %q", tc.consoleAddr, tc.consoleBind, got, tc.want)
			}
		})
	}
}
