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
}

// C returns the channel on which events are delivered.
// The channel is closed when the subscription is cancelled or the bus is stopped.
func (s *Subscription) C() <-chan Event { return s.ch }

// Unsubscribe removes the subscription from the bus and closes the event channel.
// Safe to call multiple times; subsequent calls are no-ops.
func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		s.parent.unsubscribe(s)
		close(s.ch)
	})
}

// Bus is a thread-safe in-process publish/subscribe event bus.
//
// The zero value is not usable; use [New].
type Bus struct {
	mu   sync.RWMutex
	subs []*Subscription
	seq  atomic.Int64
}

// New creates and returns a ready Bus.
func New() *Bus {
	return &Bus{}
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
	seq := b.seq.Add(1)
	raw := MarshalPayload(payload)
	ev := Event{
		Seq:     seq,
		Topic:   topic,
		TS:      time.Now().UTC(),
		Payload: raw,
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
		select {
		case s.ch <- ev:
		default:
			// Subscriber's buffer is full; mark for removal.
			overflow = append(overflow, s)
		}
	}

	// Clean up overflowed subscribers outside the hot path.
	for _, s := range overflow {
		s.Unsubscribe()
	}

	return ev
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
