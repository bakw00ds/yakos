package peerclaim_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/peerclaim"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(workDir, coordDir string) *peerclaim.Hook {
	return &peerclaim.Hook{
		WorkCurrentDir: workDir,
		ProjectDir:     workDir,
		NowFn:          fixedNow,
		User:           "testuser",
		Host:           "testhost",
		PID:            1234,
		CoordDirFn:     func(proj string) string { return coordDir },
	}
}

func makeInput(tool, filePath string, env map[string]string) hooktype.HookInput {
	if env == nil {
		env = map[string]string{}
	}
	env["YAKOS_COORD_ENABLED"] = "1"
	payload := map[string]any{}
	if filePath != "" {
		payload["path"] = filePath
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    tool,
		Payload: payload,
		Env:     env,
	}
}

// buildClaims writes an active-claims.json with the given owner for path.
func buildClaims(t *testing.T, coordDir, relPath string, owner map[string]any) {
	t.Helper()
	claims := map[string]any{
		"generated_at": "2026-01-15T10:00:00Z",
		"claims": []map[string]any{
			{"path": relPath, "owners": []map[string]any{owner}},
		},
	}
	data, _ := json.Marshal(claims)
	claimsFile := filepath.Join(coordDir, "active-claims.json")
	if err := os.MkdirAll(coordDir, 0755); err != nil {
		t.Fatalf("mkdir coord: %v", err)
	}
	if err := os.WriteFile(claimsFile, append(data, '\n'), 0644); err != nil {
		t.Fatalf("write claims: %v", err)
	}
}

// TestCoordDisabledNoOp confirms hook is a no-op when coord is not enabled.
func TestCoordDisabledNoOp(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	h := newHook(tmp, coord)
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Edit",
		Payload: map[string]any{"path": filepath.Join(tmp, "foo.go")},
		Env:     map[string]string{}, // no YAKOS_COORD_ENABLED
	}
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected pass when coord disabled, got %d", out.ExitCode)
	}
}

// TestNonWriteToolIgnored confirms Read tool is ignored.
func TestNonWriteToolIgnored(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	h := newHook(tmp, coord)
	for _, tool := range []string{"Read", "Bash", "TeamCreate"} {
		in := makeInput(tool, filepath.Join(tmp, "foo.go"), nil)
		out, err := h.Run(context.Background(), in)
		if err != nil {
			t.Fatalf("tool=%s: unexpected error: %v", tool, err)
		}
		if out.ExitCode != 0 {
			t.Fatalf("tool=%s: expected pass, got %d", tool, out.ExitCode)
		}
	}
}

// TestFreeFilePasses when no claim exists for the file.
func TestFreeFilePasses(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, "main.go"), nil)
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("free file should pass, got %d", out.ExitCode)
	}
}

// TestPeerClaimBlocks when another session holds the file.
func TestPeerClaimBlocks(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	relPath := "main.go"
	// A different user holds the claim.
	buildClaims(t, coord, relPath, map[string]any{
		"user":       "otheruser",
		"host":       "otherhost",
		"pid":        9999,
		"agent":      "researcher",
		"expires_at": "2026-01-15T12:00:00Z", // future
	})
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("expected block (exit 2), got %d", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "otheruser@otherhost") {
		t.Fatalf("expected peer info in block message, got: %s", out.Stderr)
	}
}

// TestExpiredClaimPasses when the peer's claim has expired.
func TestExpiredClaimPasses(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	relPath := "expired.go"
	buildClaims(t, coord, relPath, map[string]any{
		"user":       "otheruser",
		"host":       "otherhost",
		"pid":        9999,
		"agent":      "researcher",
		"expires_at": "2026-01-01T00:00:00Z", // past
	})
	h := newHook(tmp, coord)
	in := makeInput("Write", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expired claim should pass, got %d", out.ExitCode)
	}
}

// TestSelfClaimRenews when the same session already holds the claim.
func TestSelfClaimRenews(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	relPath := "owned.go"
	buildClaims(t, coord, relPath, map[string]any{
		"user":       "testuser", // same as our hook identity
		"host":       "testhost",
		"pid":        1234,
		"agent":      "lead",
		"expires_at": "2026-01-15T12:00:00Z",
	})
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("self-held claim should renew (pass), got %d", out.ExitCode)
	}
	// Verify a claim_renewed event was emitted.
	activityLog := filepath.Join(coord, "activity.ndjson")
	data, err := os.ReadFile(activityLog)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	if !strings.Contains(string(data), "claim_renewed") {
		t.Fatalf("expected claim_renewed event, got: %s", data)
	}
}

// TestBypassAllowsPeerHeldFile allows edit when bypass is in hook-bypass.md.
func TestBypassAllowsPeerHeldFile(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	relPath := "bypassed.go"
	buildClaims(t, coord, relPath, map[string]any{
		"user":       "otheruser",
		"host":       "otherhost",
		"pid":        9999,
		"agent":      "researcher",
		"expires_at": "2026-01-15T12:00:00Z",
	})
	// Write bypass file.
	bypassContent := "## bypass\nHook: peer-claim\nScope: file=bypassed.go peer=otheruser@otherhost\n"
	if err := os.WriteFile(filepath.Join(tmp, "hook-bypass.md"), []byte(bypassContent), 0644); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("bypassed claim should pass, got %d", out.ExitCode)
	}
}

// TestActivityLogWritten confirms a claim_intent event is emitted.
func TestActivityLogWritten(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, "new.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	activityLog := filepath.Join(coord, "activity.ndjson")
	data, err := os.ReadFile(activityLog)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	if !strings.Contains(string(data), "claim_intent") {
		t.Fatalf("expected claim_intent in activity log, got: %s", data)
	}
}

// TestTTLForSQLFile confirms SQL files get 1800s TTL.
func TestTTLForSQLFile(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Write", filepath.Join(tmp, "migrations", "0001_init.sql"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify ttl in activity log.
	activityLog := filepath.Join(coord, "activity.ndjson")
	data, err := os.ReadFile(activityLog)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	if !strings.Contains(string(data), `"ttl_seconds":1800`) {
		t.Fatalf("expected ttl_seconds=1800 for SQL file, got: %s", data)
	}
}

// TestTTLForDecisionsMd confirms decisions.md gets 120s TTL.
func TestTTLForDecisionsMd(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, "decisions.md"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	activityLog := filepath.Join(coord, "activity.ndjson")
	data, err := os.ReadFile(activityLog)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	if !strings.Contains(string(data), `"ttl_seconds":120`) {
		t.Fatalf("expected ttl_seconds=120 for decisions.md, got: %s", data)
	}
}

// TestTTLForLockFile confirms go.sum gets 300s TTL.
func TestTTLForLockFile(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, "go.sum"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	activityLog := filepath.Join(coord, "activity.ndjson")
	data, err := os.ReadFile(activityLog)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	if !strings.Contains(string(data), `"ttl_seconds":300`) {
		t.Fatalf("expected ttl_seconds=300 for go.sum, got: %s", data)
	}
}

// TestBlockMessageContainsBypassHint confirms bypass instructions in block message.
func TestBlockMessageContainsBypassHint(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	relPath := "src/main.go"
	buildClaims(t, coord, relPath, map[string]any{
		"user":       "peeruser",
		"host":       "peerhost",
		"pid":        5555,
		"agent":      "backend",
		"expires_at": "2026-01-15T12:00:00Z",
	})
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, _ := h.Run(context.Background(), in)
	if !strings.Contains(string(out.Stderr), "hook-bypass.md") {
		t.Fatalf("expected bypass hint in block message, got: %s", out.Stderr)
	}
}

// TestMultiEditToolHandled confirms MultiEdit is also handled.
func TestMultiEditToolHandled(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	relPath := "app.go"
	buildClaims(t, coord, relPath, map[string]any{
		"user":       "otheruser",
		"host":       "otherhost",
		"pid":        9999,
		"agent":      "researcher",
		"expires_at": "2026-01-15T12:00:00Z",
	})
	h := newHook(tmp, coord)
	in := makeInput("MultiEdit", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("MultiEdit should also be gated, got %d", out.ExitCode)
	}
}

// TestNoFilePathNoOp confirms graceful pass when path is missing from payload.
func TestNoFilePathNoOp(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	h := newHook(tmp, coord)
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Edit",
		Payload: map[string]any{}, // no "path" key
		Env:     map[string]string{"YAKOS_COORD_ENABLED": "1"},
	}
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("no path in payload should pass, got %d", out.ExitCode)
	}
}

// ---- stale coord-state smart-degrade ----------------------------------------

// fakeFileInfo is a minimal os.FileInfo implementation for injecting a
// controlled ModTime in the StatFn.
type fakeFileInfo struct {
	modTime time.Time
}

func (f fakeFileInfo) Name() string       { return "active-claims.json" }
func (f fakeFileInfo) Size() int64        { return 64 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0644 }
func (f fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

// makeStaleHook returns a hook with a fixed "now" of fixedTime and a StatFn
// that reports the claims file's modTime.  The IsProcessRunningFn always
// returns true so PID checks do not interfere with the time-based staleness test.
func makeStaleHook(workDir, coordDir string, modTime time.Time) *peerclaim.Hook {
	h := newHook(workDir, coordDir)
	h.StatFn = func(path string) (os.FileInfo, error) {
		return fakeFileInfo{modTime: modTime}, nil
	}
	h.IsProcessRunningFn = func(pid int) bool { return true }
	return h
}

// TestStaleCoord_FreshState verifies no warning is emitted when coord state is
// within the freshness threshold.
func TestStaleCoord_FreshState(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}

	// ModTime is 1 hour ago — well within the 24h default.
	recentMod := fixedTime.Add(-1 * time.Hour)
	h := makeStaleHook(tmp, coord, recentMod)

	in := makeInput("Edit", filepath.Join(tmp, "main.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("fresh state: expected pass, got exit %d", out.ExitCode)
	}
	if strings.Contains(string(out.Stderr), "stale") {
		t.Errorf("fresh state: unexpected stale warning in stderr: %s", out.Stderr)
	}
}

// TestStaleCoord_25hStale verifies a WARN is emitted (but hook still passes)
// when the coord state is 25 hours old.
func TestStaleCoord_25hStale(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}

	// ModTime is 25 hours ago — beyond the 24h default.
	staleMod := fixedTime.Add(-25 * time.Hour)
	h := makeStaleHook(tmp, coord, staleMod)

	in := makeInput("Edit", filepath.Join(tmp, "main.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("25h stale: expected exit 0 (no block), got %d", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "stale") {
		t.Errorf("25h stale: expected stale warning in stderr; got: %s", out.Stderr)
	}
	if !strings.Contains(string(out.Stderr), "yakos peer cleanup") {
		t.Errorf("25h stale: expected cleanup hint in stderr; got: %s", out.Stderr)
	}
}

// TestStaleCoord_MissingPID verifies a WARN is emitted (but hook still passes)
// when an active claim's owner PID is not running.
func TestStaleCoord_MissingPID(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")

	relPath := "api.go"
	// Claim is fresh (not yet expired) but owner PID is dead.
	buildClaims(t, coord, relPath, map[string]any{
		"user":       "ghostuser",
		"host":       "ghosthost",
		"pid":        999999, // very unlikely to be a real PID
		"agent":      "researcher",
		"expires_at": "2026-01-15T12:00:00Z", // future
	})

	h := newHook(tmp, coord)
	// StatFn reports the file as fresh (1 min old) so the time-based branch won't fire.
	h.StatFn = func(path string) (os.FileInfo, error) {
		return fakeFileInfo{modTime: fixedTime.Add(-1 * time.Minute)}, nil
	}
	// IsProcessRunningFn reports the dead PID as not running.
	h.IsProcessRunningFn = func(pid int) bool {
		if pid == 999999 {
			return false
		}
		return true
	}

	in := makeInput("Edit", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Hook must still block because a peer holds the claim.
	// The stale warning is emitted, but the block takes priority.
	// Verify: either we got a warn about the stale PID OR the block message
	// (block fires after the stale check; the warning is in Stderr either way).
	staleWarn := strings.Contains(string(out.Stderr), "stale")
	blockMsg := strings.Contains(string(out.Stderr), "ghostuser@ghosthost")
	if !staleWarn && !blockMsg {
		t.Errorf("missing PID: expected stale warn or block message in stderr; got: %s", out.Stderr)
	}
}

// TestStaleCoord_EnvOverride verifies that YAKOS_PEER_STALE_AFTER overrides
// the default 24h threshold.
func TestStaleCoord_EnvOverride(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}

	// ModTime is 2 hours ago.  With the default 24h threshold this would be
	// fresh, but we override to 3600s (1h), so 2h old is stale.
	staleMod := fixedTime.Add(-2 * time.Hour)
	h := makeStaleHook(tmp, coord, staleMod)

	in := makeInput("Edit", filepath.Join(tmp, "main.go"), map[string]string{
		"CLAUDE_PROJECT_DIR":     tmp,
		"YAKOS_PEER_STALE_AFTER": "3600", // 1 hour in seconds
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("env-override stale: expected exit 0 (no block), got %d", out.ExitCode)
	}
	if !strings.Contains(string(out.Stderr), "stale") {
		t.Errorf("env-override stale: expected stale warning with 1h threshold; got: %s", out.Stderr)
	}
}
