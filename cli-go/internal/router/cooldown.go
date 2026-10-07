package router

import (
	"sync"
	"time"
)

// Cooldown parameters: a runtime that fails FailuresToCool times in a row is
// skipped for CoolFor.
const (
	FailuresToCool = 3
	CoolFor        = 60 * time.Second
)

// Cooldown tracks consecutive failures per runtime, in memory only: a restart
// forgets them, which is the safe direction. Safe for concurrent use.
type Cooldown struct {
	mu    sync.Mutex
	now   func() time.Time
	state map[string]*coolState
}

type coolState struct {
	fails int
	until time.Time
}

// NewCooldown returns a tracker reading time from now (time.Now when nil), so a
// test can drive it with a fake clock.
func NewCooldown(now func() time.Time) *Cooldown {
	if now == nil {
		now = time.Now
	}
	return &Cooldown{now: now, state: map[string]*coolState{}}
}

// Failure records one failed run on runtime. The third in a row starts the
// cooldown; failures during it do not extend it.
func (c *Cooldown) Failure(runtime string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state[runtime]
	if s == nil {
		s = &coolState{}
		c.state[runtime] = s
	}
	now := c.now()
	if now.Before(s.until) {
		return
	}
	s.fails++
	if s.fails >= FailuresToCool {
		s.fails = 0
		s.until = now.Add(CoolFor)
	}
}

// Success clears the failure count of runtime.
func (c *Cooldown) Success(runtime string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.state, runtime)
}

// Cooling reports whether runtime is being skipped and for how much longer.
func (c *Cooldown) Cooling(runtime string) (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state[runtime]
	if s == nil {
		return false, 0
	}
	if left := s.until.Sub(c.now()); left > 0 {
		return true, left
	}
	return false, 0
}
