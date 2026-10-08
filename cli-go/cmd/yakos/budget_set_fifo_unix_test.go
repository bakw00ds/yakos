//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/budget"
)

// A FIFO where the policy file should be must not hang `budget set` (the audit
// line's before/after sha read used a plain os.Open, which blocks on a FIFO).
func TestBudgetSetDoesNotHangOnAFifoPolicy(t *testing.T) {
	home, override := t.TempDir(), t.TempDir()
	if err := syscall.Mkfifo(budget.PolicyPath(override), 0o600); err != nil {
		t.Skip("no mkfifo:", err)
	}
	b, _ := json.Marshal([]string{"budget", "set", "backend", "12"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBudgetHelperMain$")
	cmd.Env = []string{"YAKOS_TEST_MAIN_ARGS=" + string(b), "YAKOS_DISPATCH_LOG=" + override, "HOME=" + home, "PATH=" + t.TempDir()}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("the set hung on a FIFO policy file: %s", out.String())
	}
	if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() == 0 {
		t.Errorf("a FIFO policy was accepted: %v %s", err, out.String())
	}
	if fi, err := os.Lstat(filepath.Join(override, budget.PolicyFileName)); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Error("the FIFO was replaced")
	}
}
