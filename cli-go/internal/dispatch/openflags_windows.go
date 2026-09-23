//go:build windows

package dispatch

// noFollowFlag is a no-op on Windows: os.O_NOFOLLOW-equivalent behavior has
// no portable Go os.OpenFile flag on this platform, and creating a
// filesystem symlink on Windows normally requires an elevated privilege
// (SeCreateSymbolicLinkPrivilege) that an unprivileged local attacker does
// not hold, unlike POSIX symlink creation. See openflags_unix.go for the
// POSIX side of this defense (round-2 review R4).
const noFollowFlag = 0
