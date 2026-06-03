//go:build !windows

package start

import "syscall"

// execSyscall replaces the current process image with argv0 using syscall.Exec.
// On success it never returns (POSIX exec semantics).
func execSyscall(argv0 string, argv []string, env []string) error {
	return syscall.Exec(argv0, argv, env)
}
