package modelreg

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// RunSpec is one command for a Runner to run.
type RunSpec struct {
	// Path is the executable, already resolved and checked by the caller.
	Path string
	// Args are the arguments after the program name. They are fixed by the
	// caller; nothing here goes through a shell.
	Args []string
	// Env is the child's whole environment. The child gets exactly these
	// variables and nothing else, even when the slice is empty.
	Env []string
	// Dir is the child's working directory.
	Dir string
	// MaxStdout and MaxStderr bound what is kept of each stream; zero or less
	// means the package default (256 KiB and 16 KiB).
	MaxStdout, MaxStderr int
}

// RunResult is what a command printed and how it ended.
type RunResult struct {
	Stdout, Stderr []byte
	// ExitCode is the process's exit status; -1 when a signal ended it.
	ExitCode int
}

// Runner runs one command to completion. It returns ErrOutputTooLarge when
// standard output outgrows MaxStdout (after killing the process), ctx's error
// when ctx ends first, and any other error as it is. A non-zero exit is NOT an
// error: ExitCode carries it.
type Runner func(ctx context.Context, spec RunSpec) (RunResult, error)

// ErrOutputTooLarge is returned by a Runner whose command printed more than the
// bound on standard output.
var ErrOutputTooLarge = errors.New("modelreg: command output exceeded the limit")

// waitDelay is how long a run waits, once the process has ended, for the output
// pipes to close. A descendant that inherited them (a helper the CLI left
// running, or the child of a killed process) would otherwise hold the run open
// for as long as it lives. A variable so a test need not wait the full two
// seconds.
var waitDelay = 2 * time.Second

// execRunner is the production Runner.
//
// The command is the vendor's own CLI run the way the operator would run it, so
// what matters here is what it is NOT given: no shell, no standard input, no
// inherited environment (cmd.Env is a copy of spec.Env and never nil, which os/exec
// would read as "inherit the whole parent environment"), and a working directory
// the caller made for it rather than the caller's own (a project directory could
// hold configuration the CLI would load). What it may print is bounded while it
// prints, not after.
func execRunner(ctx context.Context, spec RunSpec) (RunResult, error) {
	maxOut, maxErr := spec.MaxStdout, spec.MaxStderr
	if maxOut <= 0 {
		maxOut = maxListingStdout
	}
	if maxErr <= 0 {
		maxErr = maxListingStderr
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(runCtx, spec.Path, spec.Args...) //nolint:gosec // Path is resolved and vetted by the caller; Args are fixed; no shell
	cmd.Dir = spec.Dir
	cmd.Env = append([]string{}, spec.Env...)
	cmd.Stdin = nil // the null device: the CLI can never wait for a keypress
	cmd.WaitDelay = waitDelay
	isolateProcess(cmd)
	out := &limitedBuffer{max: maxOut, onOverflow: cancel}
	errOut := &limitedBuffer{max: maxErr}
	cmd.Stdout, cmd.Stderr = out, errOut

	runErr := cmd.Run()

	// A bound or a deadline wins over whatever Wait reported: a killed process
	// reports "signal: killed", which says nothing about why.
	if out.overflowed() {
		return RunResult{}, ErrOutputTooLarge
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	res := RunResult{Stdout: out.bytes(), Stderr: errOut.bytes()}
	if runErr == nil || errors.Is(runErr, exec.ErrWaitDelay) {
		// ErrWaitDelay: the process itself exited with success but something it
		// started still held the pipes. What the process wrote has been read (the
		// copy ran for the whole delay); the straggler's later output is parsed as
		// defensively as everything else.
		return res, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	}
	return RunResult{}, runErr
}

// limitedBuffer keeps at most max bytes and drops the rest. Going over the bound
// is remembered and, for standard output, stops the process (onOverflow cancels
// its context); the excess is still read and discarded so the child is not left
// blocked on a full pipe while the kill lands.
type limitedBuffer struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	max        int
	over       bool
	onOverflow func()
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.max - b.buf.Len()
	if len(p) <= room {
		b.buf.Write(p)
		return len(p), nil
	}
	if room > 0 {
		b.buf.Write(p[:room])
	}
	if !b.over {
		b.over = true
		if b.onOverflow != nil {
			b.onOverflow()
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

func (b *limitedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}
