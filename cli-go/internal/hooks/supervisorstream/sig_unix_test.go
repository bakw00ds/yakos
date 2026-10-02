//go:build !windows

package supervisorstream_test

import (
	"os"
	"syscall"
)

func syscallZero() os.Signal { return syscall.Signal(0) }
