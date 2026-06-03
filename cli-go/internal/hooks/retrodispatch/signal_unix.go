//go:build !windows

package retrodispatch

import "syscall"

// zeroSignal is the null signal used to probe process liveness on Unix.
const zeroSignal = syscall.Signal(0)
