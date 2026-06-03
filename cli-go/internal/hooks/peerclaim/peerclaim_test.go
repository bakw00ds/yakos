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
