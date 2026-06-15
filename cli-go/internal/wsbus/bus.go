package wsbus

import (
	"sync"
	"sync/atomic"
	"time"
)

// subBufferSize is the number of events each subscriber channel can buffer
// before the subscriber is considered slow and its subscription is dropped.
const subBufferSize = 256

// Subscription is an active subscription to the bus.
// Call [Subscription.Unsubscribe] to stop receiving events and release
// the channel.
type Subscription struct {
	topic  string // "" = all topics
	ch     chan Event
	once   sync.Once
	parent *Bus

	// mu guards closed and the channel send in Publish.
	// Lock ordering: b.mu is always released before any s.mu is taken,
	// so b.mu → s.mu is the only direction and deadlock is impossible.
	mu     sync.Mutex
	closed bool
}

// C returns the channel on which events are delivered.
// The channel is closed when the subscription is cancelled or the bus is stopped.
func (s *Subscription) C() <-chan Event { return s.ch }

// Unsubscribe removes the subscription from the bus and closes the event channel.
// Safe to call multiple times; subsequent calls are no-ops.
func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		// Remove from the bus subscriber list first (acquires/releases b.mu).
		// We must NOT hold s.mu here; b.unsubscribe takes b.mu, and Publish
		// releases b.mu before taking s.mu, so the ordering b.mu→s.mu is
		// consistent across all paths.
		s.parent.unsubscribe(s)

		// Now mark closed and close the channel under s.mu so that a
		// concurrent Publish that already snapshotted this sub sees the
		// closed flag and skips the send.
		s.mu.Lock()
		s.closed = true
		close(s.ch)
		s.mu.Unlock()
	})
}

// Bus is a thread-safe in-process publish/subscribe event bus.
//
// The zero value is not usable; use [New] or [NewWithReplay].
type Bus struct {
	mu     sync.RWMutex
	subs   []*Subscription
	seq    atomic.Int64
	replay *replayBuffer
}

// New creates and returns a ready Bus with the default replay buffer size
// (1000 events, or YAKOS_WS_REPLAY_BUFFER if set).
func New() *Bus {
	return NewWithReplay(0)
}

// NewWithReplay creates a ready Bus with a replay buffer of the given capacity.
// If capacity is 0, the default (YAKOS_WS_REPLAY_BUFFER or 1000) is used.
func NewWithReplay(capacity int) *Bus {
	return &Bus{replay: newReplayBuffer(capacity)}
}

// Subscribe returns a [Subscription] that receives events matching topic.
// Pass an empty string to receive all events.
// The caller must call [Subscription.Unsubscribe] when done.
func (b *Bus) Subscribe(topic string) *Subscription {
	s := &Subscription{
		topic:  topic,
		ch:     make(chan Event, subBufferSize),
		parent: b,
	}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
	return s
}

// Publish emits an event on the given topic with the provided payload.
// The event is delivered to all matching subscribers whose channels are not full.
// Subscribers with full channels are dropped and their subscriptions removed.
//
// Publish is non-blocking: it never blocks on a slow subscriber.
func (b *Bus) Publish(topic string, payload any) Event {
	return b.PublishMeta(topic, payload, EventMeta{})
}

// PublishMeta is like [Publish] but attaches server-side routing metadata to
// the event.  The metadata is carried in-memory through the fan-out and replay
// paths; it is NEVER included in the JSON sent to clients (EventMeta has no
// json tags).
//
// Use this for fleet.* topics where per-operator WS delivery filtering is
// required.  The WS handler reads ev.Meta to decide whether to forward the
// event to a given connection.
func (b *Bus) PublishMeta(topic string, payload any, meta EventMeta) Event {
	seq := b.seq.Add(1)
	raw := MarshalPayload(payload)
	ev := Event{
		Seq:     seq,
		Topic:   topic,
		TS:      time.Now().UTC(),
		Payload: raw,
		Meta:    &meta,
	}

	b.mu.RLock()
	subs := make([]*Subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.RUnlock()

	var overflow []*Subscription
	for _, s := range subs {
		if s.topic != "" && s.topic != topic {
			continue
		}
		// Guard the send under s.mu so it cannot race with Unsubscribe or
		// Stop closing s.ch.  b.mu has already been released above; taking
		// s.mu here is safe (lock order: b.mu always precedes s.mu and is
		// always released before s.mu is acquired).
		s.mu.Lock()
		if s.closed {
			// Already unsubscribed between snapshot and send; skip silently
			// (not an overflow — the subscriber chose to leave).
			s.mu.Unlock()
			continue
		}
		select {
		case s.ch <- ev:
		default:
			// Subscriber's buffer is full; mark for removal.
			overflow = append(overflow, s)
		}
		s.mu.Unlock()
	}

	// Clean up overflowed subscribers outside the hot path.
	for _, s := range overflow {
		s.Unsubscribe()
	}

	// Record in the ring buffer for future replay.
	if b.replay != nil {
		b.replay.append(ev)
	}

	return ev
}

// History returns all buffered events with Seq > sinceSeq, in ascending Seq order.
// If sinceSeq is 0, all retained events are returned.
//
// This supports the WS ?since=<seq> reconnect-replay feature (Q8 override):
// new clients connect, pass their last-seen seq, and receive missed events
// before joining the live stream.
func (b *Bus) History(sinceSeq int64) []Event {
	if b.replay == nil {
		return nil
	}
	return b.replay.since(sinceSeq)
}

// Stop closes all active subscriptions.  After Stop, Publish is safe to call
// but no events will be delivered.
func (b *Bus) Stop() {
	b.mu.Lock()
	subs := b.subs
	b.subs = nil
	b.mu.Unlock()

	for _, s := range subs {
		s.Unsubscribe()
	}
}

// unsubscribe removes s from the subscriber list.
func (b *Bus) unsubscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, sub := range b.subs {
		if sub == s {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			return
		}
	}
}

// SubscriberCount returns the number of active subscribers.
// Primarily useful for testing.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
