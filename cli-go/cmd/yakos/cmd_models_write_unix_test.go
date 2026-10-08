//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// `--rules-file -` reads standard input, bounded.
func TestRouterPolicySetFromStdin(t *testing.T) {
	ledgerFor(t)
	env, _ := polEnv(t, "")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	go func() {
		_, _ = w.WriteString("- {match: {class: chat}, action: {runtime: claude, model: sonnet}}\n")
		_ = w.Close()
	}()
	if code, out, errs := runPolicy(t, env, "set", "--rules-file", "-"); code != 0 {
		t.Fatalf("exit %d out=%q err=%q", code, out, errs)
	}
	if code, out, _ := runPolicy(t, env, "get"); code != 0 || !strings.Contains(out, "R1: when class=chat -> runtime=claude model=sonnet") {
		t.Errorf("after set: %d %q", code, out)
	}
}

// A FIFO named as the rules file is refused without being opened, so it cannot
// block the command.
func TestRouterPolicySetRefusesAFIFO(t *testing.T) {
	ledgerFor(t)
	env, _ := polEnv(t, "")
	fifo := filepath.Join(t.TempDir(), "rules.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	done := make(chan int, 1)
	go func() {
		code, _, _ := runPolicy(t, env, "set", "--rules-file", fifo)
		done <- code
	}()
	select {
	case code := <-done:
		if code == 0 {
			t.Error("a FIFO was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the command blocked on a FIFO")
	}
}
