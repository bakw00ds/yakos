package auth

// KeyringService is the service name used for all yakOS keyring entries.
const KeyringService = "yakos"

// KeyringBackend abstracts OS keychain access so tests can inject a mock
// without touching the real keychain.
type KeyringBackend interface {
	// Get retrieves the secret for the given service and account.
	// Returns ("", ErrKeyringUnavailable) when the keyring service is
	// absent (e.g. headless Linux without dbus).
	Get(service, account string) (string, error)

	// Set stores a secret in the keyring.
	Set(service, account, secret string) error

	// Delete removes the entry for service+account.
	// A non-existent entry is not an error.
	Delete(service, account string) error
}
