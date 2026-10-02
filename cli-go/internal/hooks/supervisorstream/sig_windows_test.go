//go:build windows

package supervisorstream_test

import "os"

func syscallZero() os.Signal { return os.Kill }
