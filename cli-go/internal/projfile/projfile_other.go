//go:build !unix

package projfile

// readFlags on a system without O_NOFOLLOW: Windows has no FIFO to block
// on, and the Lstat and post-open checks apply everywhere.
const readFlags = 0

func linkRefused(error) bool { return false }
