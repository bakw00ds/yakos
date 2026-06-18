//go:build windows

package start

import (
	"fmt"
	"io"
)

// runLocalPump on Windows returns an error because web terminal is not
// supported on this platform.  The caller should fall back to the legacy
// syscall.Exec path (--direct behaviour).
func runLocalPump(socketPath string, spawnSpec map[string]interface{}, ew io.Writer) (int, error) {
	return 1, fmt.Errorf("web terminal (--share-terminal) is not supported on Windows; use --direct to run the legacy path")
}
