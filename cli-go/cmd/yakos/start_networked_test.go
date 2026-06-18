// start_networked_test.go — unit tests for the networked-console helpers.
//
// v0.46 introduced networkedFromFlags and validateNetworkedStartMode to fix
// two regressions.  v0.47 added validateNetworkedStartMode to block interactive
// + networked mode.  v0.48 removes that block: interactive + networked now
// auto-spawns a detached daemon via shouldSpawnDaemon / spawnDetachedDaemon.
//
// All helpers tested here are pure functions; no binary required.
package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// ---- networkedFromFlags -------------------------------------------------------

// TestNetworkedFromFlags_ExplicitFlag verifies that --networked sets networked.
func TestNetworkedFromFlags_ExplicitFlag(t *testing.T) {
	if !networkedFromFlags(true, "") {
		t.Error("networkedFromFlags(true, \"\") should be true")
	}
}

// TestNetworkedFromFlags_WildcardBind verifies that 0.0.0.0:<port> implies networked.
func TestNetworkedFromFlags_WildcardBind(t *testing.T) {
	if !networkedFromFlags(false, "0.0.0.0:7890") {
		t.Error("networkedFromFlags(false, \"0.0.0.0:7890\") should be true")
	}
}

// TestNetworkedFromFlags_RealIPBind verifies that a real IP bind implies networked.
func TestNetworkedFromFlags_RealIPBind(t *testing.T) {
	if !networkedFromFlags(false, "192.168.1.50:7890") {
		t.Error("networkedFromFlags(false, \"192.168.1.50:7890\") should be true")
	}
}

// TestNetworkedFromFlags_LoopbackBind verifies that 127.0.0.1 does NOT imply networked.
func TestNetworkedFromFlags_LoopbackBind(t *testing.T) {
	if networkedFromFlags(false, "127.0.0.1:7890") {
		t.Error("networkedFromFlags(false, \"127.0.0.1:7890\") should be false")
	}
}

// TestNetworkedFromFlags_LocalhostBind verifies that localhost does NOT imply networked.
func TestNetworkedFromFlags_LocalhostBind(t *testing.T) {
	if networkedFromFlags(false, "localhost:7890") {
		t.Error("networkedFromFlags(false, \"localhost:7890\") should be false")
	}
}

// TestNetworkedFromFlags_EmptyBind verifies that an empty bind does NOT imply networked.
func TestNetworkedFromFlags_EmptyBind(t *testing.T) {
	if networkedFromFlags(false, "") {
		t.Error("networkedFromFlags(false, \"\") should be false")
	}
}

// TestNetworkedFromFlags_ExplicitFlagWithLoopbackBind verifies that --networked
// wins even when --console-bind is loopback (explicit flag beats the bind addr).
func TestNetworkedFromFlags_ExplicitFlagWithLoopbackBind(t *testing.T) {
	if !networkedFromFlags(true, "127.0.0.1:7890") {
		t.Error("networkedFromFlags(true, \"127.0.0.1:7890\") should be true")
	}
}

// ---- validateNetworkedStartMode (retained for reference; no longer called) ---
//
// validateNetworkedStartMode is kept in the codebase as documentation of the
// old constraint.  These tests confirm its return values are still correct even
// though the function is not called from runStart.

// TestValidateNetworkedStartMode_NetworkedNoREPL verifies no error when
// networked=true and noREPL=true (valid combination).
func TestValidateNetworkedStartMode_NetworkedNoREPL(t *testing.T) {
	if err := validateNetworkedStartMode(true, true); err != nil {
		t.Errorf("validateNetworkedStartMode(true, true) should be nil; got %v", err)
	}
}

// TestValidateNetworkedStartMode_NotNetworked verifies no error when not networked.
func TestValidateNetworkedStartMode_NotNetworked(t *testing.T) {
	if err := validateNetworkedStartMode(false, false); err != nil {
		t.Errorf("validateNetworkedStartMode(false, false) should be nil; got %v", err)
	}
}

// TestValidateNetworkedStartMode_NetworkedREPL verifies the retained function
// still returns the old guidance when networked=true and noREPL=false.
// NOTE: this combination is now handled by auto-spawn, not rejected.
func TestValidateNetworkedStartMode_NetworkedREPL(t *testing.T) {
	err := validateNetworkedStartMode(true, false)
	if err == nil {
		t.Fatal("validateNetworkedStartMode(true, false) should return an error")
	}
	msg := err.Error()
	for _, want := range []string{
		"--no-repl",
		"--web",
		"Interactive REPL mode replaces the process",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q; got:\n%s", want, msg)
		}
	}
}

// ---- shouldSpawnDaemon -------------------------------------------------------

// TestShouldSpawnDaemon_NetworkedFlag verifies that --networked alone triggers spawn.
func TestShouldSpawnDaemon_NetworkedFlag(t *testing.T) {
	if !shouldSpawnDaemon(true, false, false, false) {
		t.Error("shouldSpawnDaemon(networked=true) should be true")
	}
}

// TestShouldSpawnDaemon_ConsoleBindProvided verifies that --console-bind triggers spawn.
func TestShouldSpawnDaemon_ConsoleBindProvided(t *testing.T) {
	if !shouldSpawnDaemon(false, true, false, false) {
		t.Error("shouldSpawnDaemon(consoleBindProvided=true) should be true")
	}
}

// TestShouldSpawnDaemon_ConsoleExternalHostProvided verifies --console-external-host triggers spawn.
func TestShouldSpawnDaemon_ConsoleExternalHostProvided(t *testing.T) {
	if !shouldSpawnDaemon(false, false, true, false) {
		t.Error("shouldSpawnDaemon(consoleExternalHostProvided=true) should be true")
	}
}

// TestShouldSpawnDaemon_ConsoleAddrProvided verifies --console-addr triggers spawn.
func TestShouldSpawnDaemon_ConsoleAddrProvided(t *testing.T) {
	if !shouldSpawnDaemon(false, false, false, true) {
		t.Error("shouldSpawnDaemon(consoleAddrProvided=true) should be true")
	}
}

// TestShouldSpawnDaemon_NoFlagsNoSpawn verifies plain `yakos start` does NOT spawn.
func TestShouldSpawnDaemon_NoFlagsNoSpawn(t *testing.T) {
	if shouldSpawnDaemon(false, false, false, false) {
		t.Error("shouldSpawnDaemon(all false) should be false — loopback-only start must be unchanged")
	}
}

// ---- buildServeArgs ----------------------------------------------------------

// TestBuildServeArgs_EmptyInput verifies that a zero-value input produces no args
// (no spurious flags injected for the no-console case).
func TestBuildServeArgs_EmptyInput(t *testing.T) {
	got := buildServeArgs(serveArgsInput{})
	if len(got) != 0 {
		t.Errorf("empty input should produce no args; got %v", got)
	}
}

// TestBuildServeArgs_ConsoleAddr verifies --console-addr forwarding.
func TestBuildServeArgs_ConsoleAddr(t *testing.T) {
	got := buildServeArgs(serveArgsInput{consoleAddr: "127.0.0.1:8080"})
	want := []string{"--console-addr", "127.0.0.1:8080"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

// TestBuildServeArgs_NetworkedAutoDerive verifies that when networked=true and
// consoleBind is empty, --console-bind 0.0.0.0:<port> is derived.
func TestBuildServeArgs_NetworkedAutoDerive(t *testing.T) {
	got := buildServeArgs(serveArgsInput{
		networked:           true,
		consoleBind:         "",
		consolePort:         "7890",
		detectedNetworkedIP: "192.168.1.50",
	})
	// Expect --console-bind 0.0.0.0:7890 and --console-external-host 192.168.1.50:7890.
	if len(got) < 4 {
		t.Fatalf("expected at least 4 args; got %v", got)
	}
	if got[0] != "--console-bind" || got[1] != "0.0.0.0:7890" {
		t.Errorf("expected --console-bind 0.0.0.0:7890; got %v %v", got[0], got[1])
	}
	if got[2] != "--console-external-host" || got[3] != "192.168.1.50:7890" {
		t.Errorf("expected --console-external-host 192.168.1.50:7890; got %v %v", got[2], got[3])
	}
}

// TestBuildServeArgs_ExplicitBindDoesNotDouble verifies that an explicit
// consoleBind does NOT also emit the auto-derived 0.0.0.0 bind.
func TestBuildServeArgs_ExplicitBindDoesNotDouble(t *testing.T) {
	got := buildServeArgs(serveArgsInput{
		networked:   true,
		consoleBind: "10.0.0.1:7890",
		consolePort: "7890",
	})
	for i, a := range got {
		if a == "--console-bind" && i+1 < len(got) && got[i+1] == "0.0.0.0:7890" {
			t.Errorf("should not emit auto-derived 0.0.0.0 bind when explicit consoleBind set; args: %v", got)
		}
	}
	// Explicit bind must be present.
	found := false
	for i, a := range got {
		if a == "--console-bind" && i+1 < len(got) && got[i+1] == "10.0.0.1:7890" {
			found = true
		}
	}
	if !found {
		t.Errorf("explicit console-bind 10.0.0.1:7890 not in args: %v", got)
	}
}

// TestBuildServeArgs_IDERootFromBanner verifies that bannerProjectRepo is used
// for --ide-root when ideRoot is empty and noProjectIDE is false.
func TestBuildServeArgs_IDERootFromBanner(t *testing.T) {
	got := buildServeArgs(serveArgsInput{bannerProjectRepo: "/home/user/myapp"})
	want := []string{"--ide-root", "/home/user/myapp"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v; want %v", got, want)
	}
}

// TestBuildServeArgs_NoProjectIDESkipsIDE verifies --no-project-ide suppresses ide-root.
func TestBuildServeArgs_NoProjectIDESkipsIDE(t *testing.T) {
	got := buildServeArgs(serveArgsInput{noProjectIDE: true, bannerProjectRepo: "/home/user/myapp"})
	for _, a := range got {
		if a == "--ide-root" {
			t.Errorf("--ide-root should be suppressed when noProjectIDE=true; got %v", got)
		}
	}
}

// ---- daemonAlive (liveness check) -------------------------------------------

// TestDaemonAlive_AbsentFile verifies that a non-existent pidfile returns false.
func TestDaemonAlive_AbsentFile(t *testing.T) {
	if daemonAlive("/tmp/yakos-test-absent-pidfile-that-does-not-exist.pid") {
		t.Error("daemonAlive should return false for absent pidfile")
	}
}

// TestDaemonAlive_SelfPID verifies that our own PID is alive.
func TestDaemonAlive_SelfPID(t *testing.T) {
	dir := t.TempDir()
	pidPath := dir + "/test.pid"
	pid := os.Getpid()
	if err := os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", pid)), 0600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if !daemonAlive(pidPath) {
		t.Errorf("daemonAlive should return true for our own PID (%d)", pid)
	}
}

// TestDaemonAlive_StalePID verifies that a PID of 999999999 (non-existent) returns false.
func TestDaemonAlive_StalePID(t *testing.T) {
	dir := t.TempDir()
	pidPath := dir + "/stale.pid"
	// PID 999999999 is extremely unlikely to exist.
	if err := os.WriteFile(pidPath, []byte("999999999\n"), 0600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	if daemonAlive(pidPath) {
		t.Error("daemonAlive should return false for a stale non-existent PID")
	}
}

// ---- readPIDFile -------------------------------------------------------------

// TestReadPIDFile_Valid verifies normal numeric content is read correctly.
func TestReadPIDFile_Valid(t *testing.T) {
	dir := t.TempDir()
	pidPath := dir + "/test.pid"
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	pid, err := readPIDFile(pidPath)
	if err != nil {
		t.Fatalf("readPIDFile: %v", err)
	}
	if pid != 12345 {
		t.Errorf("got pid %d; want 12345", pid)
	}
}

// TestReadPIDFile_Absent verifies error on missing file.
func TestReadPIDFile_Absent(t *testing.T) {
	_, err := readPIDFile("/tmp/yakos-test-no-such-file-pid.pid")
	if err == nil {
		t.Error("readPIDFile should return error for absent file")
	}
}

// TestReadPIDFile_Malformed verifies error on non-numeric content.
func TestReadPIDFile_Malformed(t *testing.T) {
	dir := t.TempDir()
	pidPath := dir + "/bad.pid"
	if err := os.WriteFile(pidPath, []byte("not-a-number\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := readPIDFile(pidPath)
	if err == nil {
		t.Error("readPIDFile should return error for non-numeric content")
	}
}
