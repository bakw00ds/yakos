//go:build !windows

package dispatch

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	rt "github.com/bakw00ds/yakos/internal/runtime"
)

// The one-shot path (execWithStderrCapture, used by Service.Run) shares
// ConfigureGroupKill: cancelling ctx must end a stub whose backgrounded
// grandchild holds stdout, and kill that grandchild.
func TestExecWithStderrCapture_CancelKillsGrandchildHoldingStdout(t *testing.T) {
	bin := t.TempDir()
	scratch := t.TempDir()
	gcFile := filepath.Join(scratch, "gc.pid")
	stubFile := filepath.Join(scratch, "stub.pid")
	script := "#!/bin/sh\necho $$ > '" + stubFile + "'\nsleep 77 &\necho $! > '" + gcFile + ".tmp' && mv '" + gcFile + ".tmp' '" + gcFile + "'\nwait\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	pid := func(p string) int {
		b, _ := os.ReadFile(p)
		n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		return n
	}
	t.Cleanup(func() { // watchdog: never leave the stub or its sleep behind
		if p := pid(stubFile); p > 0 {
			_ = syscall.Kill(-p, syscall.SIGKILL)
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
		if p := pid(gcFile); p > 0 {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	})
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		var errBuf bytes.Buffer
		_, _, _ = execWithStderrCapture(ctx, &rt.CodexAdapter{}, rt.DispatchRequest{AgentName: "x", Task: "t"}, &errBuf)
	}()
	var gc int
	for i := 0; i < 500 && gc == 0; i++ {
		if gc = pid(gcFile); gc == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if gc == 0 {
		t.Fatal("grandchild never started")
	}
	start := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("cancel did not end the run: grandchild holds stdout")
	}
	t.Logf("cancel to return: %v", time.Since(start))
	for i := 0; i < 100 && syscall.Kill(gc, 0) == nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(gc, 0) == nil {
		t.Errorf("grandchild %d survived cancel", gc)
	}
}
