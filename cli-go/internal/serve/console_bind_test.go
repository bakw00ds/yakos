// console_bind_test.go — Phase 6c serve-level tests for --console-bind.
//
// Tests:
//  1. Run() with ConsoleBind pointing to an unwritable state dir (forces mTLS
//     generation failure) → returns error, no listener opened.
//  2. ConsoleBind on a loopback address → behaves like ConsoleAddr (no mTLS,
//     no error, loopback-safe path).
//  3. consoleBind() helper returns ConsoleBind when set, ConsoleAddr fallback,
//     and default when both are empty.
//  4. isWildcardBind predicate.
//  5. normalizeExternalHosts port defaulting.
//  6. Wildcard bind without --console-external-host → Run() refuses.
package serve_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// ---- 4. isWildcardBind predicate --------------------------------------------

// TestIsWildcardBind verifies that isWildcardBind correctly identifies wildcard
// addresses (0.0.0.0, ::, [::]) and returns false for specific addresses.
func TestIsWildcardBind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		addr string
		want bool
	}{
		{"0.0.0.0:7890", true},
		{"0.0.0.0:0", true},
		{"[::]:7890", true},   // IPv6 any-address (canonical bracket form)
		{"[::]:0", true},      // IPv6 any-address with OS-assigned port
		{"[::1]:7890", false}, // IPv6 loopback — not a wildcard
		{"127.0.0.1:7890", false},
		{"10.0.0.1:7890", false},
		{"192.168.1.50:7890", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.addr, func(t *testing.T) {
			t.Parallel()
			got := serve.IsWildcardBindForTest(tc.addr)
			if got != tc.want {
				t.Errorf("IsWildcardBind(%q) = %v; want %v", tc.addr, got, tc.want)
			}
		})
	}
}

// ---- 5. normalizeExternalHosts port defaulting --------------------------------

// TestNormalizeExternalHosts verifies port defaulting and comma-separated parsing.
func TestNormalizeExternalHosts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		hosts    []string
		bindAddr string
		want     []string
	}{
		{
			name:     "port_already_present",
			hosts:    []string{"192.168.1.50:7890"},
			bindAddr: "0.0.0.0:7890",
			want:     []string{"192.168.1.50:7890"},
		},
		{
			name:     "port_omitted_uses_bind_port",
			hosts:    []string{"192.168.1.50"},
			bindAddr: "0.0.0.0:7890",
			want:     []string{"192.168.1.50:7890"},
		},
		{
			name:     "comma_separated_values",
			hosts:    []string{"192.168.1.50:7890,myhost.example.com:7890"},
			bindAddr: "0.0.0.0:7890",
			want:     []string{"192.168.1.50:7890", "myhost.example.com:7890"},
		},
		{
			name:     "multiple_flags_plus_comma",
			hosts:    []string{"192.168.1.50:7890", "host2,host3"},
			bindAddr: "0.0.0.0:8443",
			want:     []string{"192.168.1.50:7890", "host2:8443", "host3:8443"},
		},
		{
			name:     "empty_entries_skipped",
			hosts:    []string{"", "  ", "192.168.1.50:7890"},
			bindAddr: "0.0.0.0:7890",
			want:     []string{"192.168.1.50:7890"},
		},
		{
			name:     "nil_input_returns_empty",
			hosts:    nil,
			bindAddr: "0.0.0.0:7890",
			want:     []string{},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := serve.NormalizeExternalHostsForTest(tc.hosts, tc.bindAddr)
			if len(got) != len(tc.want) {
				t.Fatalf("NormalizeExternalHosts(%v, %q) = %v; want %v", tc.hosts, tc.bindAddr, got, tc.want)
			}
			for i, g := range got {
				if g != tc.want[i] {
					t.Errorf("result[%d] = %q; want %q", i, g, tc.want[i])
				}
			}
		})
	}
}

// ---- 6. Wildcard bind without --console-external-host → Run() refuses --------

// TestConsoleBind_WildcardWithoutExternalHost_Refuses verifies that Run()
// returns a clear error when ConsoleBind is a wildcard address and
// ConsoleExternalHosts is empty.  This is the fail-closed requirement: a
// wildcard bind has no usable SAN for browser cert verification, so we refuse
// rather than issue a cert with no DNS/IP SANs.
func TestConsoleBind_WildcardWithoutExternalHost_Refuses(t *testing.T) {
	t.Parallel()

	wildcardAddrs := []struct {
		name string
		addr string
	}{
		{"ipv4-any", "0.0.0.0:7890"},
		{"ipv6-any", "[::]:7890"},
	}

	for _, tc := range wildcardAddrs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workspaceRoot := t.TempDir()
			cfg := serve.Config{
				WorkspaceRoot:        workspaceRoot,
				ConsoleBind:          tc.addr,
				ConsoleExternalHosts: nil, // intentionally absent
				ListenFn: func(path string) (net.Listener, error) {
					return net.Listen("tcp", "127.0.0.1:0")
				},
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := serve.Run(ctx, cfg)
			if err == nil {
				t.Error("Run() with wildcard ConsoleBind and no ConsoleExternalHosts should return error; got nil")
			}
			if err != nil && !strings.Contains(err.Error(), "console-external-host") {
				t.Errorf("error should mention --console-external-host; got: %v", err)
			}
		})
	}
}
