//go:build !unix

package budget

// projectReadFlags on a system without O_NOFOLLOW: Windows has no FIFO to block
// on, and the Lstat and post-open checks apply everywhere.
const projectReadFlags = 0

func projectLinkRefused(error) bool { return false }
