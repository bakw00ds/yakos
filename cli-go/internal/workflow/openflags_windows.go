//go:build windows

package workflow

// Windows has neither flag; callers also Lstat/Stat before opening.
const (
	oNonblock = 0
	oNoFollow = 0
)
