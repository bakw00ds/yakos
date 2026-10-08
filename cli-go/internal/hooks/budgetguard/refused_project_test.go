package budgetguard_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// K-164: the hook runs on every tool call, so a .yakos.yml that is a link to
// /dev/zero must be refused, not read to the end, and the refusal is surfaced
// without the project's path. A refused file is absent: no caps are enforced.
func TestBudgetGuard_RefusedProjectFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	work, proj := t.TempDir(), t.TempDir()
	if err := os.Symlink("/dev/zero", filepath.Join(proj, ".yakos.yml")); err != nil {
		t.Fatal(err)
	}
	h := makeHook(work, proj)
	type res struct {
		code int
		err  string
	}
	done := make(chan res, 1)
	go func() {
		out, err := h.Run(context.Background(), makeInput("Edit", nil))
		if err != nil {
			t.Error(err)
		}
		done <- res{out.ExitCode, string(out.Stderr)}
	}()
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("exit %d", r.code)
		}
		if !strings.Contains(r.err, ".yakos.yml ignored: is a symlink") || strings.Contains(r.err, proj) {
			t.Fatalf("stderr = %q", r.err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("budget-guard did not return on a link to /dev/zero")
	}
}
