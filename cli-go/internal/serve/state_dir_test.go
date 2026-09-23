package serve

// state_dir_test.go — tests for restStateDir/mustResolveStateDir (round-2
// review R4). Before this fix, restStateDir fell back to the literal "/tmp"
// whenever $HOME was empty (true under launchd/systemd with no HOME, cron,
// `env -i`, many containers, and Windows where HOME is normally unset). A
// fixed, world-writable, well-known path lets a local attacker pre-plant a
// rest-write-token file that LoadOrGenerateTokens then adopts.

import (
	"runtime"
	"testing"
)

// TestRestStateDir_EmptyHomeReturnsEmpty is the core R4 regression:
// restStateDir must NOT fall back to "/tmp" (or any other shared directory)
// when no home directory can be resolved — it must return "" so the caller
// (mustResolveStateDir) can hard-fail instead of silently using a
// world-writable path.
func TestRestStateDir_EmptyHomeReturnsEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir consults USERPROFILE/other APIs on windows; HOME-only unset doesn't reproduce the unresolvable case there")
	}
	t.Setenv("HOME", "")

	cfg := Config{}
	got := cfg.restStateDir()
	if got == "/tmp/.yakos-state" || got == "/tmp" {
		t.Fatalf("restStateDir() with empty HOME = %q; must not fall back to a shared /tmp path (R4 regression)", got)
	}
	if got != "" {
		t.Errorf("restStateDir() with empty HOME = %q; want \"\" (mustResolveStateDir turns this into a hard failure)", got)
	}
}

// TestMustResolveStateDir_EmptyHomeFailsClosed proves the Run()-level gate
// actually turns an unresolvable state dir into an error, rather than Run
// silently proceeding to derive token/console/perf paths from "/tmp".
func TestMustResolveStateDir_EmptyHomeFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see TestRestStateDir_EmptyHomeReturnsEmpty")
	}
	t.Setenv("HOME", "")

	cfg := Config{}
	if err := cfg.mustResolveStateDir(); err == nil {
		t.Fatal("mustResolveStateDir() with empty HOME: want error, got nil (R4 regression: daemon would start against an unresolvable/shared state dir)")
	}
}

// TestRestStateDir_ExplicitOverrideWins verifies RESTStateDir always takes
// precedence, even with HOME unset — a non-regression check for the escape
// hatch operators/tests use.
func TestRestStateDir_ExplicitOverrideWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("see TestRestStateDir_EmptyHomeReturnsEmpty")
	}
	t.Setenv("HOME", "")

	cfg := Config{RESTStateDir: "/explicit/override"}
	if got := cfg.restStateDir(); got != "/explicit/override" {
		t.Errorf("restStateDir() with RESTStateDir set = %q; want the explicit override", got)
	}
	if err := cfg.mustResolveStateDir(); err != nil {
		t.Errorf("mustResolveStateDir() with RESTStateDir set: %v; want nil", err)
	}
}
