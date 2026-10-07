//go:build !windows

package interactive_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/runtime"
)

func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

// pidAlive is true while the process exists and is not a zombie-reaped pid.
func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// TestResumeEngine_CloseKillsGrandchildHoldingStdout drives the real
// dispatch.Service.RunStream with a codex stub that backgrounds a grandchild
// ("sleep 77 &") which inherits stdout, then waits. Without a group kill the
// grandchild outlives Close, ReadLineLoop never sees EOF, and Close burns the
// full resumeCloseWait (10 s). With it Close returns promptly and the
// grandchild is dead.
func TestResumeEngine_CloseKillsGrandchildHoldingStdout(t *testing.T) {
	bin := t.TempDir()
	scratch := t.TempDir()
	stubPID := filepath.Join(scratch, "stub.pid")
	gcPID := filepath.Join(scratch, "grandchild.pid")
	script := "#!/bin/sh\n" +
		"echo $$ > '" + stubPID + ".tmp' && mv '" + stubPID + ".tmp' '" + stubPID + "'\n" +
		"sleep 77 &\n" +
		"echo $! > '" + gcPID + ".tmp' && mv '" + gcPID + ".tmp' '" + gcPID + "'\n" +
		"wait\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// Process-group watchdog: a failed test must not orphan the stub or sleep.
	t.Cleanup(func() {
		if p := readPID(t, stubPID); p > 0 {
			_ = syscall.Kill(-p, syscall.SIGKILL)
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
		if p := readPID(t, gcPID); p > 0 {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	})
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("CODEX_HOME", "")
	t.Setenv("OPENAI_API_KEY", "sk-stub-not-real") // satisfies the sign-in probe; the stub never calls out
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())

	root := t.TempDir()
	agents := filepath.Join(root, "lib", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nid: general-codex\ndomain: cross-cutting\nruntime: codex\nmodel: gpt-5.5\n---\n\n## Purpose\n\nstub\n"
	if err := os.WriteFile(filepath.Join(agents, "general-codex.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})

	eng, err := interactive.NewResumeEngine(interactive.ResumeEngineParams{
		ConversationID: "c1", OwnerOperatorID: "alice", Runner: svc,
		Base:        dispatch.Params{Agent: "general-codex", Runtime: "codex", Project: t.TempDir()},
		OnChunk:     func(c dispatch.StreamChunk) { t.Logf("chunk %+v", c) },
		OnTurnError: func(err error) { t.Logf("turn error: %v", err) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := eng.SendUserTurn(runtime.EncodeUserTurn("hi")); err != nil {
		t.Fatal(err)
	}

	var gc int
	deadline := time.Now().Add(10 * time.Second)
	for gc == 0 {
		gc = readPID(t, gcPID)
		if gc == 0 {
			if time.Now().After(deadline) {
				t.Fatal("stub never started its grandchild")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	start := time.Now()
	_ = eng.Close()
	took := time.Since(start)
	t.Logf("Close took %v", took)
	if took > 5*time.Second {
		t.Fatalf("Close took %v: a grandchild holding stdout stalled the turn (resumeCloseWait is 10s)", took)
	}
	// The kill is SIGKILL; allow the kernel a moment to reap.
	for i := 0; i < 100 && pidAlive(gc); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if pidAlive(gc) {
		t.Errorf("grandchild %d survived Close", gc)
	}
}
