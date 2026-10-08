//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package repl

// FlushInput is a no-op where the terminal input queue cannot be flushed.
func FlushInput(fd int) {}
