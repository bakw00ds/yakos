package repl

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// GuardTerminal runs a native session that shares the terminal on fd and puts
// the terminal back afterwards, whatever the session did: it saves the mode
// before run, then (after a normal return, an error or a panic) restores it
// and discards any input still queued on the terminal. A child that died raw or
// with echo off therefore cannot leave the REPL unusable, and keystrokes the
// child did not consume are not replayed as chat messages.
func GuardTerminal(fd int, run func() error) error {
	var saved *term.State
	if term.IsTerminal(fd) {
		saved, _ = term.GetState(fd)
	}
	restore := func() {
		if saved != nil {
			_ = term.Restore(fd, saved)
		}
		FlushInput(fd)
	}
	defer func() {
		if p := recover(); p != nil {
			restore()
			panic(p)
		}
	}()
	err := run()
	restore()
	return err
}

// RestoreTerminalOnSignal restores the terminal mode captured now when the
// process gets SIGTERM or SIGHUP, then exits with the conventional status. The
// returned func stops the watcher.
func RestoreTerminalOnSignal(fd int) (stop func()) {
	if !term.IsTerminal(fd) {
		return func() {}
	}
	saved, err := term.GetState(fd)
	if err != nil {
		return func() {}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-ch:
			_ = term.Restore(fd, saved)
			code := 128
			if sig, ok := s.(syscall.Signal); ok {
				code += int(sig)
			}
			os.Exit(code)
		case <-done:
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
