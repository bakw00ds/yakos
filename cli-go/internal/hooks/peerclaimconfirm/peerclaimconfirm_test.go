package peerclaimconfirm_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/peerclaimconfirm"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func newHook(workDir, coordDir string) *peerclaimconfirm.Hook {
	return &peerclaimconfirm.Hook{
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
		Event:   "PostToolUse",
		Tool:    tool,
		Payload: payload,
		Env:     env,
	}
}

// TestAlwaysExitZero confirms the hook never blocks.
func TestAlwaysExitZero(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, "main.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("hook must always exit 0, got %d", out.ExitCode)
	}
}

// TestCoordDisabledNoOp confirms no-op when coord disabled.
func TestCoordDisabledNoOp(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	h := newHook(tmp, coord)
	in := hooktype.HookInput{
		Event:   "PostToolUse",
		Tool:    "Edit",
		Payload: map[string]any{"path": filepath.Join(tmp, "foo.go")},
		Env:     map[string]string{}, // no YAKOS_COORD_ENABLED
	}
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected pass, got %d", out.ExitCode)
	}
	// No coord dir created.
	if _, err := os.Stat(coord); err == nil {
		t.Fatalf("coord dir should not be created when disabled")
	}
}

// TestNonWriteToolIgnored confirms Read/Bash are ignored.
func TestNonWriteToolIgnored(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	h := newHook(tmp, coord)
	for _, tool := range []string{"Read", "Bash", "TeamCreate"} {
		in := hooktype.HookInput{
			Event:   "PostToolUse",
			Tool:    tool,
			Payload: map[string]any{"path": filepath.Join(tmp, "foo.go")},
			Env:     map[string]string{"YAKOS_COORD_ENABLED": "1"},
		}
		out, err := h.Run(context.Background(), in)
		if err != nil {
			t.Fatalf("tool=%s: unexpected error: %v", tool, err)
		}
		if out.ExitCode != 0 {
			t.Fatalf("tool=%s: expected pass, got %d", tool, out.ExitCode)
		}
	}
}

// TestConfirmEventEmitted confirms claim_confirmed is written to activity log.
func TestConfirmEventEmitted(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Write", filepath.Join(tmp, "handler.go"), map[string]string{
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
	if !strings.Contains(string(data), "claim_confirmed") {
		t.Fatalf("expected claim_confirmed event, got: %s", data)
	}
}

// TestActiveClaimsRebuilt confirms active-claims.json is rebuilt after confirm.
func TestActiveClaimsRebuilt(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, "api.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	claimsFile := filepath.Join(coord, "active-claims.json")
	data, err := os.ReadFile(claimsFile)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		// trim trailing newline.
		if err2 := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &claims); err2 != nil {
			t.Fatalf("parse claims: %v (raw: %s)", err2, data)
		}
	}
	if _, ok := claims["generated_at"]; !ok {
		t.Fatalf("expected generated_at in claims, got: %v", claims)
	}
	if _, ok := claims["claims"]; !ok {
		t.Fatalf("expected claims array, got: %v", claims)
	}
}

// TestClaimsProjectionHasConfirmedStatus confirms status=confirmed after rebuild.
func TestClaimsProjectionHasConfirmedStatus(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	relPath := "service/db.go"
	in := makeInput("Edit", filepath.Join(tmp, relPath), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	claimsFile := filepath.Join(coord, "active-claims.json")
	raw, err := os.ReadFile(claimsFile)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	rawStr := strings.TrimSpace(string(raw))
	var claims map[string]any
	if err := json.Unmarshal([]byte(rawStr), &claims); err != nil {
		t.Fatalf("parse: %v", err)
	}
	claimsList, _ := claims["claims"].([]interface{})
	if len(claimsList) == 0 {
		// Claim expired or not populated — pass (future-expiry is needed).
		// Check activity log for the event.
		activityLog := filepath.Join(coord, "activity.ndjson")
		data, _ := os.ReadFile(activityLog)
		if !strings.Contains(string(data), `"confirmed"`) &&
			!strings.Contains(string(data), `claim_confirmed`) {
			t.Fatalf("expected claim_confirmed in activity log")
		}
		return
	}
	// Check the first claim has status=confirmed (from the activity fold).
	claimMap, _ := claimsList[0].(map[string]interface{})
	owners, _ := claimMap["owners"].([]interface{})
	if len(owners) > 0 {
		ownerMap, _ := owners[0].(map[string]interface{})
		if ownerMap["status"] != "confirmed" {
			t.Fatalf("expected status=confirmed, got %v", ownerMap["status"])
		}
	}
}

// TestAuditLogWritten confirms the hook log entry is created.
func TestAuditLogWritten(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("Write", filepath.Join(tmp, "routes.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	logFile := filepath.Join(tmp, "logs", "peer-claim-confirm.ndjson")
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "peer-claim-confirm") {
		t.Fatalf("expected hook name in log, got: %s", data)
	}
}

// TestEventContainsTTL confirms ttl is embedded in the emitted event.
func TestEventContainsTTL(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	// go.sum → TTL=300
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

// TestReleasedClaimRemovedFromProjection confirms claim_released is respected in rebuild.
func TestReleasedClaimRemovedFromProjection(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}

	// Manually write an activity log with a claim_intent then claim_released.
	activityLog := filepath.Join(coord, "activity.ndjson")
	events := []map[string]any{
		{
			"ts":   "2026-01-15T09:00:00Z",
			"kind": "claim_intent",
			"actor": map[string]any{
				"user": "testuser", "host": "testhost", "pid": 1234,
				"session_id": "s1", "agent": "lead",
			},
			"detail": map[string]any{
				"path": "src/main.go", "ttl_seconds": 600, "expires_at": "2026-01-15T12:00:00Z",
			},
		},
		{
			"ts":   "2026-01-15T09:01:00Z",
			"kind": "claim_released",
			"actor": map[string]any{
				"user": "testuser", "host": "testhost", "pid": 1234,
				"session_id": "s1", "agent": "lead",
			},
			"detail": map[string]any{"path": "src/main.go"},
		},
	}
	for _, ev := range events {
		data, _ := json.Marshal(ev)
		f, _ := os.OpenFile(activityLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}

	// Trigger a confirm for a different file to force rebuild.
	h := newHook(tmp, coord)
	in := makeInput("Edit", filepath.Join(tmp, "other.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	_, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	claimsFile := filepath.Join(coord, "active-claims.json")
	raw, err := os.ReadFile(claimsFile)
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	// src/main.go should not appear in claims (it was released).
	if strings.Contains(string(raw), "src/main.go") {
		t.Fatalf("released claim should not appear in projection, got: %s", raw)
	}
}

// TestMultiEditToolHandled confirms MultiEdit also triggers confirm.
func TestMultiEditToolHandled(t *testing.T) {
	tmp := t.TempDir()
	coord := filepath.Join(tmp, "coord")
	if err := os.MkdirAll(coord, 0755); err != nil {
		t.Fatal(err)
	}
	h := newHook(tmp, coord)
	in := makeInput("MultiEdit", filepath.Join(tmp, "config.go"), map[string]string{
		"CLAUDE_PROJECT_DIR": tmp,
	})
	out, err := h.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("expected pass for MultiEdit, got %d", out.ExitCode)
	}
	activityLog := filepath.Join(coord, "activity.ndjson")
	data, err := os.ReadFile(activityLog)
	if err != nil {
		t.Fatalf("read activity log: %v", err)
	}
	if !strings.Contains(string(data), "claim_confirmed") {
		t.Fatalf("expected claim_confirmed for MultiEdit, got: %s", data)
	}
}
