package auth

import (
	"errors"
	"fmt"
	"sync"
)

// MockKeyring is an in-memory KeyringBackend for tests. It is safe for
// concurrent use.
type MockKeyring struct {
	mu      sync.Mutex
	entries map[string]string

	// Unavailable, when true, causes all operations to return ErrKeyringUnavailable.
	// Use this to simulate headless Linux without dbus.
	Unavailable bool
}

// NewMockKeyring returns an empty MockKeyring.
func NewMockKeyring() *MockKeyring {
	return &MockKeyring{entries: make(map[string]string)}
}

func (m *MockKeyring) key(service, account string) string {
	return service + "/" + account
}

// Get retrieves a stored secret or ErrKeyringUnavailable when Unavailable is set.
func (m *MockKeyring) Get(service, account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unavailable {
		return "", fmt.Errorf("%w: mock unavailable", ErrKeyringUnavailable)
	}
	v, ok := m.entries[m.key(service, account)]
	if !ok {
		return "", errors.New("keyring: not found")
	}
	return v, nil
}

// Set stores a secret. Returns ErrKeyringUnavailable when Unavailable is set.
func (m *MockKeyring) Set(service, account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unavailable {
		return fmt.Errorf("%w: mock unavailable", ErrKeyringUnavailable)
	}
	m.entries[m.key(service, account)] = secret
	return nil
}

// Delete removes an entry. Not-found is silently ignored.
func (m *MockKeyring) Delete(service, account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Unavailable {
		return fmt.Errorf("%w: mock unavailable", ErrKeyringUnavailable)
	}
	delete(m.entries, m.key(service, account))
	return nil
}
