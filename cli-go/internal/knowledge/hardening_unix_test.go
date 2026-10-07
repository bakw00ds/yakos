//go:build !windows

package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// F2: a FIFO as SKILL.md or as a rule file is refused at once, not waited on.
func TestFIFORefusedWithoutBlocking(t *testing.T) {
	root, project := fixture(t)
	skill := filepath.Join(project, ".claude/skills/demo/SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(skill, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	// If the fix is missing the open blocks; opening the write end releases the
	// stuck goroutine so the test process does not leak it.
	t.Cleanup(func() {
		if f, err := os.OpenFile(skill, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
	})

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := SkillText(root, project, "demo")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || err == ErrNotFound {
			t.Fatalf("a FIFO skill must be refused with an error, got %v", err)
		}
		if d := time.Since(start); d > 250*time.Millisecond {
			t.Fatalf("refusal took %v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SkillText blocked on a FIFO SKILL.md")
	}

	// readRooted straight on a FIFO that the directory listing said was a file
	// (the swap-race window of the rule reader).
	rules := filepath.Join(project, ".claude/rules")
	fifo := filepath.Join(rules, "pipe.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
	})
	r, err := os.OpenRoot(rules)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	res := make(chan error, 1)
	go func() { _, err := readRooted(r, "pipe.md", MaxFileBytes); res <- err }()
	select {
	case err := <-res:
		if err == nil || !strings.Contains(err.Error(), "regular") {
			t.Fatalf("want a not-a-regular-file refusal, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("readRooted blocked on a FIFO rule file")
	}
	// And Compose lists neither the FIFO nor stalls.
	cdone := make(chan Pack, 1)
	go func() { cdone <- Compose(opts(root, project)) }()
	select {
	case p := <-cdone:
		if strings.Contains(p.Text, "pipe") {
			t.Fatal("FIFO reached the pack")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Compose blocked")
	}
}
