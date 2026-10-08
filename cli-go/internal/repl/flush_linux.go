package repl

import "golang.org/x/sys/unix"

// FlushInput discards input queued on the terminal behind fd but not yet read.
func FlushInput(fd int) { _ = unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIFLUSH) }
