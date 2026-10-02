//go:build !windows

package budget

// lockBusy reports whether an O_EXCL create error means "someone holds the
// lock, retry". On POSIX that is EEXIST only (checked by the caller).
func lockBusy(error) bool { return false }
