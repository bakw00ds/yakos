package wsbus

import (
	"os"
	"strconv"
	"sync"
)

// defaultReplayBufferSize is the number of events retained in the ring buffer.
// Override via YAKOS_WS_REPLAY_BUFFER environment variable.
const defaultReplayBufferSize = 1000

// replayBuffer is a fixed-capacity ring buffer that retains the most recent
// events published on the bus.  When the buffer is full, the oldest event is
// overwritten by the newest.
//
// Thread safety: all methods are guarded by the embedded mutex.
type replayBuffer struct {
	mu       sync.RWMutex
	buf      []Event
	cap      int
	head     int // index of next write slot
	size     int // number of valid entries
}

// newReplayBuffer creates a replayBuffer with the given capacity.
// If cap <= 0, the default capacity is used.
func newReplayBuffer(cap int) *replayBuffer {
	if cap <= 0 {
		cap = replayBufferCapFromEnv()
	}
	return &replayBuffer{
		buf: make([]Event, cap),
		cap: cap,
	}
}

// replayBufferCapFromEnv reads YAKOS_WS_REPLAY_BUFFER; falls back to the default.
func replayBufferCapFromEnv() int {
	if v := os.Getenv("YAKOS_WS_REPLAY_BUFFER"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultReplayBufferSize
}

// append adds ev to the ring buffer.  If the buffer is full, the oldest entry
// is silently overwritten.
func (r *replayBuffer) append(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.head] = ev
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
}

// since returns all buffered events with Seq > sinceSeq, in ascending Seq order.
// If sinceSeq == 0, all buffered events are returned.
func (r *replayBuffer) since(sinceSeq int64) []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.size == 0 {
		return nil
	}

	// The oldest element sits at (head - size + cap) % cap when the buffer is full,
	// or at 0 when partially filled.
	oldest := (r.head - r.size + r.cap) % r.cap

	var out []Event
	for i := 0; i < r.size; i++ {
		idx := (oldest + i) % r.cap
		ev := r.buf[idx]
		if ev.Seq > sinceSeq {
			out = append(out, ev)
		}
	}
	return out
}

// len returns the number of valid entries in the buffer.
func (r *replayBuffer) len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

// cap_ returns the ring buffer capacity.
func (r *replayBuffer) cap_() int {
	return r.cap
}
