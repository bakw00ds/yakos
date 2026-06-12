//go:build windows

package retrodispatch

import "os"

// zeroSignal on Windows: os.Signal is an interface; we use a sentinel
// that the os.Process.Signal implementation handles gracefully (as a no-op
// liveness check substitute). On Windows, FindProcess+Signal(0) is not
// supported, so we treat every process as not-in-flight on Windows.
const zeroSignal = windowsNoSignal(0)

type windowsNoSignal int

func (windowsNoSignal) Signal()        {}
func (windowsNoSignal) String() string { return "no-op signal" }

// Ensure the type satisfies os.Signal at compile time.
var _ os.Signal = windowsNoSignal(0)
