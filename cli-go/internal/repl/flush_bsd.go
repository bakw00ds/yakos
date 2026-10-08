//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package repl

import "golang.org/x/sys/unix"

// FlushInput discards input queued on the terminal behind fd but not yet read.
func FlushInput(fd int) { _ = unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, 1) } // FREAD
