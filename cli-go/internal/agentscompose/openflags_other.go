//go:build !unix

package agentscompose

// readFlags on a system without O_NOFOLLOW: Windows has no FIFO to block on, and
// the check that the opened file is the inspected one is the same everywhere.
const readFlags = 0

func linkRefused(error) bool { return false }
