package consoleui

import "crypto/rand"

// cryptoRead fills b with cryptographically-random bytes.
// Thin wrapper so flows_handler.go can call it without importing crypto/rand directly
// (keeps the import in one place and makes it easy to mock in tests).
func cryptoRead(b []byte) (int, error) {
	return rand.Read(b)
}
