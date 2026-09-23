//go:build !windows

package dispatch

import "syscall"

// noFollowFlag is OR'd into appendEvent's os.OpenFile call (round-2 review
// R4) so opening the dispatch-log path refuses to follow a symlink: a local
// attacker who plants a symlink at the resolved dispatch-log path (most
// reachable via statepath's os.TempDir() last-resort fallback when HOME is
// unresolvable, but a real protection regardless of how the path is
// resolved) cannot redirect the daemon's append-only write to an arbitrary
// target file. The open then fails with ELOOP; appendEvent's caller already
// treats a failed append as non-fatal (logging is best-effort), so this
// fails safe rather than fatal.
const noFollowFlag = syscall.O_NOFOLLOW
