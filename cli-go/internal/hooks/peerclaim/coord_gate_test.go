package peerclaim_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/peerclaim"
)

// gateHook has NO CoordDirFn: the coord dir must resolve from
// YAKOS_COORD_ROOT + project name exactly as bash yakos_coord_dir does.
func gateHook(tmp string) *peerclaim.Hook {
	return &peerclaim.Hook{WorkCurrentDir: tmp, ProjectDir: tmp, NowFn: fixedNow, User: "bob", Host: "dev01", PID: 2002}
}

func gateInput(t *testing.T, root string) hooktype.HookInput {
	return hooktype.HookInput{Event: "PreToolUse", Tool: "Edit",
		Payload: realPayload(t, `{"tool_name":"Edit","tool_input":{"file_path":"a.go","old_string":"a","new_string":"b"}}`),
		Env:     map[string]string{"YAKOS_COORD_ROOT": root, "YAKOS_PROJECT_NAME": "proj", "CLAUDE_PROJECT_DIR": "/x"}}
}

func peerHeld(t *testing.T, root string) string {
	coord := filepath.Join(root, "proj", "coord")
	buildClaims(t, coord, "a.go", map[string]any{"user": "alice", "host": "h", "pid": 1, "agent": "x", "status": "confirmed", "expires_at": "2099-01-01T00:00:00Z"})
	return coord
}

func TestCoordDirAbsentPasses(t *testing.T) {
	tmp := t.TempDir()
	out, err := gateHook(tmp).Run(context.Background(), gateInput(t, filepath.Join(tmp, "nocoord")))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v exit=%d", err, out.ExitCode)
	}
}

// No YAKOS_COORD_ENABLED anywhere: a present, writable coord dir enables it.
func TestCoordDirPresentBlocksPeerClaim(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "coord")
	peerHeld(t, root)
	out, err := gateHook(tmp).Run(context.Background(), gateInput(t, root))
	if err != nil || out.ExitCode != 2 {
		t.Fatalf("err=%v exit=%d stderr=%s", err, out.ExitCode, out.Stderr)
	}
}

// bash `[ -w ]` false => hook no-ops (exit 0), verified against lib/hooks/peer-claim.sh.
func TestCoordDirUnwritablePasses(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs non-root POSIX")
	}
	tmp := t.TempDir()
	root := filepath.Join(tmp, "coord")
	coord := peerHeld(t, root)
	if err := os.Chmod(coord, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(coord, 0o755) })
	out, err := gateHook(tmp).Run(context.Background(), gateInput(t, root))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v exit=%d", err, out.ExitCode)
	}
}

// Project name falls back to basename($CLAUDE_PROJECT_DIR) like bash.
func TestCoordProjectNameFromClaudeProjectDir(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "coord")
	peerHeld(t, root) // proj/coord
	in := gateInput(t, root)
	delete(in.Env, "YAKOS_PROJECT_NAME")
	in.Env["CLAUDE_PROJECT_DIR"] = "/somewhere/proj"
	out, err := gateHook(tmp).Run(context.Background(), in)
	if err != nil || out.ExitCode != 2 {
		t.Fatalf("err=%v exit=%d", err, out.ExitCode)
	}
}

// Own-claim matching uses (user, host, YAKOS_SESSION_PID), as bash me_pid does.
func TestSessionPIDFromEnvMatchesOwnClaim(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "coord")
	buildClaims(t, filepath.Join(root, "proj", "coord"), "a.go", map[string]any{
		"user": "bob", "host": "dev01", "pid": 2002, "agent": "x", "status": "confirmed", "expires_at": "2099-01-01T00:00:00Z"})
	h := &peerclaim.Hook{WorkCurrentDir: tmp, ProjectDir: tmp, NowFn: fixedNow}
	in := gateInput(t, root)
	in.Env["USER"], in.Env["HOSTNAME"], in.Env["YAKOS_SESSION_PID"] = "bob", "dev01", "2002"
	out, err := h.Run(context.Background(), in)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("own claim must pass: err=%v exit=%d stderr=%s", err, out.ExitCode, out.Stderr)
	}
}
