package consoleui

import "crypto/rand"

// cryptoRead fills b with cryptographically-random bytes.
// It is a variable (not a plain function) so tests can inject a failing
// source and prove callers fail closed instead of degrading to a weak ID.
var cryptoRead = func(b []byte) (int, error) {
	return rand.Read(b)
}
