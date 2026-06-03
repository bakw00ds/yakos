//go:build !test_no_keyring

package auth

import (
	"errors"
	"fmt"

	gokeyring "github.com/zalando/go-keyring"
)

// realKeyringBackend delegates to go-keyring, which abstracts macOS Keychain
// Services, Linux secret-service (D-Bus), and Windows DPAPI.
//
// When the underlying service is unavailable (e.g. headless Linux without
// dbus), go-keyring returns an error. This implementation surfaces that as
// a wrapped ErrKeyringUnavailable so callers can degrade gracefully.
type realKeyringBackend struct{}

// ErrKeyringUnavailable is returned by KeyringBackend.Get/Set/Delete when the
// OS keychain service is not accessible (headless Linux, container without
// dbus, etc.).
var ErrKeyringUnavailable = errors.New("keyring: service unavailable")

func (r *realKeyringBackend) Get(service, account string) (string, error) {
	val, err := gokeyring.Get(service, account)
	if err != nil {
		if isUnavailable(err) {
			return "", fmt.Errorf("%w: %v", ErrKeyringUnavailable, err)
		}
		return "", err
	}
	return val, nil
}

func (r *realKeyringBackend) Set(service, account, secret string) error {
	err := gokeyring.Set(service, account, secret)
	if err != nil {
		if isUnavailable(err) {
			return fmt.Errorf("%w: %v", ErrKeyringUnavailable, err)
		}
		return err
	}
	return nil
}

func (r *realKeyringBackend) Delete(service, account string) error {
	err := gokeyring.Delete(service, account)
	if err != nil {
		if isUnavailable(err) {
			return fmt.Errorf("%w: %v", ErrKeyringUnavailable, err)
		}
		// ErrNotFound variants: not an error for our usage.
		if errors.Is(err, gokeyring.ErrNotFound) {
			return nil
		}
		return err
	}
	return nil
}

// isUnavailable returns true for dbus-unavailable / keychain-unavailable errors.
// go-keyring does not export a typed sentinel; we match on the error message.
func isUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, fragment := range []string{
		"dbus",
		"D-Bus",
		"secret service",
		"not available",
		"no such interface",
		"connection refused",
	} {
		if containsFold(msg, fragment) {
			return true
		}
	}
	return false
}

// containsFold is a simple case-insensitive substring check.
func containsFold(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	sLow := toLower(s)
	subLow := toLower(sub)
	for i := 0; i <= len(sLow)-len(subLow); i++ {
		if sLow[i:i+len(subLow)] == subLow {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// defaultKeyringBackend returns the real keyring backend.
func defaultKeyringBackend() KeyringBackend {
	return &realKeyringBackend{}
}
