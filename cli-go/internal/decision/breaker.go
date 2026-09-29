package decision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// Breaker defaults (ADR-0009 §4): 5 consecutive failures open the circuit for
// 10 minutes.
const (
	DefaultBreakerThreshold = 5
	DefaultBreakerCooldown  = 10 * time.Minute
)

// BreakerState is the persisted circuit-breaker state. It is shared by every
// hook process on the machine through decision-breaker.json.
type BreakerState struct {
	ConsecutiveFailures int    `json:"consecutive_failures"`
	OpenUntil           string `json:"open_until,omitempty"` // RFC3339; empty when closed
	LastFailureClass    string `json:"last_failure_class,omitempty"`
}

// Breaker is a file-backed circuit breaker. After the cooldown the circuit is
// half-open: the next call is allowed, and one more failure re-opens it at
// once (the failure count is not reset until a success).
type Breaker struct {
	Path      string
	Threshold int
	Cooldown  time.Duration
	Now       func() time.Time
	mu        sync.Mutex
}

// NewBreaker returns a Breaker at path (default state-dir file when empty).
func NewBreaker(path string) *Breaker {
	if path == "" {
		path = filepath.Join(statepath.Dir(), BreakerFileName)
	}
	return &Breaker{Path: path, Threshold: DefaultBreakerThreshold, Cooldown: DefaultBreakerCooldown}
}

func (b *Breaker) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Breaker) threshold() int {
	if b.Threshold > 0 {
		return b.Threshold
	}
	return DefaultBreakerThreshold
}

func (b *Breaker) cooldown() time.Duration {
	if b.Cooldown > 0 {
		return b.Cooldown
	}
	return DefaultBreakerCooldown
}

// Load returns the persisted state. A missing file is a closed breaker; an
// unreadable or corrupt file is an error (callers treat Allow as an error too:
// an unknown breaker state fails closed).
func (b *Breaker) Load() (BreakerState, error) {
	var s BreakerState
	data, err := os.ReadFile(b.Path) //nolint:gosec // state-dir file
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return BreakerState{}, err
	}
	return s, nil
}

// Allow returns a ClassBreakerOpen error while the circuit is open.
func (b *Breaker) Allow() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, err := b.Load()
	if err != nil {
		return newErr(ClassBreakerOpen, "breaker state unreadable: %v", err)
	}
	if s.OpenUntil != "" {
		until, perr := time.Parse(time.RFC3339, s.OpenUntil)
		if perr != nil || b.now().Before(until) {
			return newErr(ClassBreakerOpen, "circuit open until %s after %d consecutive failures", s.OpenUntil, s.ConsecutiveFailures)
		}
	}
	return nil
}

// Success closes the circuit and clears the failure count.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, err := b.Load(); err == nil && s.ConsecutiveFailures == 0 && s.OpenUntil == "" {
		return // nothing to write on the hot path
	}
	_ = b.save(BreakerState{})
}

// Failure records one failed call and opens the circuit at the threshold.
func (b *Breaker) Failure(class string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, _ := b.Load() // corrupt state restarts the count
	s.ConsecutiveFailures++
	s.LastFailureClass = class
	if s.ConsecutiveFailures >= b.threshold() {
		s.OpenUntil = b.now().Add(b.cooldown()).UTC().Format(time.RFC3339)
	}
	_ = b.save(s)
}

func (b *Breaker) save(s BreakerState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileAtomic(b.Path, data)
}

// writeFileAtomic writes data 0600 via temp+rename in a secured state dir.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := statepath.SecureDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".decision-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil && !isWindows() {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
