package peerclaimconfirm_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/peerclaimconfirm"
)

func gateRun(t *testing.T, root string) {
	t.Helper()
	h := &peerclaimconfirm.Hook{WorkCurrentDir: root, ProjectDir: root, NowFn: fixedNow, User: "bob", Host: "dev01", PID: 2002}
	in := hooktype.HookInput{Event: "PostToolUse", Tool: "Write",
		Payload: realPayload(t, `{"tool_input":{"file_path":"a.go","content":"x"}}`),
		Env:     map[string]string{"YAKOS_COORD_ROOT": root, "YAKOS_PROJECT_NAME": "proj", "CLAUDE_PROJECT_DIR": "/x"}}
	if out, err := h.Run(context.Background(), in); err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v exit=%d", err, out.ExitCode)
	}
}

func activity(root string) string { return filepath.Join(root, "proj", "coord", "activity.ndjson") }

func TestCoordDirAbsentNoOp(t *testing.T) {
	root := t.TempDir()
	gateRun(t, root)
	if _, err := os.Stat(filepath.Join(root, "proj")); err == nil {
		t.Fatal("must not create a coord dir")
	}
}

func TestCoordDirPresentConfirms(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "proj", "coord"), 0o755); err != nil {
		t.Fatal(err)
	}
	gateRun(t, root)
	if _, err := os.Stat(activity(root)); err != nil {
		t.Fatalf("claim_confirmed not written with no YAKOS_COORD_ENABLED: %v", err)
	}
}

func TestCoordDirUnwritableNoOp(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs non-root POSIX")
	}
	root := t.TempDir()
	coord := filepath.Join(root, "proj", "coord")
	if err := os.MkdirAll(coord, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(coord, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(coord, 0o755) })
	gateRun(t, root)
	if _, err := os.Stat(activity(root)); err == nil {
		t.Fatal("unwritable coord dir must no-op")
	}
	// The hook must not even attempt the confirm: no audit log line either
	// (an attempted-but-failed write would still log).
	if _, err := os.Stat(filepath.Join(root, "logs", "peer-claim-confirm.ndjson")); err == nil {
		t.Fatal("unwritable coord dir must not produce a peer-claim-confirm log line")
	}
}
