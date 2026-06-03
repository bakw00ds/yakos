package wsbus

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// ---- Bus tests ---------------------------------------------------------------

func TestBus_PublishDelivered(t *testing.T) {
	b := New()
	sub := b.Subscribe("")
	defer sub.Unsubscribe()

	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1", Title: "test", Column: "TODO"})

	select {
	case ev := <-sub.C():
		if ev.Topic != TopicKanbanAdded {
			t.Errorf("topic=%q; want %q", ev.Topic, TopicKanbanAdded)
		}
		if ev.Seq <= 0 {
			t.Error("seq should be > 0")
		}
		if ev.TS.IsZero() {
			t.Error("ts should not be zero")
		}
	case <-time.After(time.Second):
		t.Fatal("event not delivered within 1s")
	}
}

func TestBus_TopicFilter_Match(t *testing.T) {
	b := New()
	sub := b.Subscribe(TopicKanbanAdded)
	defer sub.Unsubscribe()

	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})

	select {
	case <-sub.C():
	case <-time.After(time.Second):
		t.Fatal("matching event not delivered")
	}
}

func TestBus_TopicFilter_NoMatch(t *testing.T) {
	b := New()
	sub := b.Subscribe(TopicKanbanAdded)
	defer sub.Unsubscribe()

	b.Publish(TopicDispatchStarted, DispatchStartedPayload{Agent: "backend"})

	select {
	case ev := <-sub.C():
		t.Errorf("unexpected event delivered: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// expected: no delivery
	}
}

func TestBus_MultipleSubscribers(t *testing.T) {
	b := New()
	const n = 5
	subs := make([]*Subscription, n)
	for i := range subs {
		subs[i] = b.Subscribe("")
		defer subs[i].Unsubscribe()
	}

	b.Publish(TopicKanbanMoved, KanbanMovedPayload{ID: "K-1", From: "TODO", To: "IN PROGRESS"})

	for i, sub := range subs {
		select {
		case ev := <-sub.C():
			if ev.Topic != TopicKanbanMoved {
				t.Errorf("sub[%d] topic=%q; want %q", i, ev.Topic, TopicKanbanMoved)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub[%d]: event not delivered within 1s", i)
		}
	}
}

func TestBus_SequenceMonotonicallyIncreasing(t *testing.T) {
	b := New()
	sub := b.Subscribe("")
	defer sub.Unsubscribe()

	const n = 10
	for i := 0; i < n; i++ {
		b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
	}

	var last int64
	for i := 0; i < n; i++ {
		select {
		case ev := <-sub.C():
			if ev.Seq <= last {
				t.Errorf("seq=%d is not > last=%d", ev.Seq, last)
			}
			last = ev.Seq
		case <-time.After(time.Second):
			t.Fatalf("event %d not delivered", i)
		}
	}
}

func TestBus_Unsubscribe_NoMoreDelivery(t *testing.T) {
	b := New()
	sub := b.Subscribe("")

	sub.Unsubscribe()

	// Publishing after unsubscribe should not block.
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})

	// Channel should be closed.
	select {
	case _, ok := <-sub.C():
		if ok {
			t.Error("channel should be closed after Unsubscribe")
		}
	case <-time.After(50 * time.Millisecond):
		t.Error("channel not closed after Unsubscribe")
	}
}

func TestBus_Unsubscribe_IdempotentMultipleCalls(t *testing.T) {
	b := New()
	sub := b.Subscribe("")

	// Must not panic on multiple calls.
	sub.Unsubscribe()
	sub.Unsubscribe()
	sub.Unsubscribe()
}

func TestBus_Stop_ClosesAllSubscribers(t *testing.T) {
	b := New()
	sub1 := b.Subscribe("")
	sub2 := b.Subscribe(TopicPresence)

	b.Stop()

	for _, sub := range []*Subscription{sub1, sub2} {
		select {
		case _, ok := <-sub.C():
			if ok {
				t.Error("channel should be closed after Stop")
			}
		case <-time.After(50 * time.Millisecond):
			t.Error("channel not closed after Stop")
		}
	}
}

func TestBus_SubscriberCount(t *testing.T) {
	b := New()
	if got := b.SubscriberCount(); got != 0 {
		t.Errorf("initial count=%d; want 0", got)
	}

	sub1 := b.Subscribe("")
	sub2 := b.Subscribe(TopicKanbanAdded)

	if got := b.SubscriberCount(); got != 2 {
		t.Errorf("count=%d; want 2", got)
	}

	sub1.Unsubscribe()
	if got := b.SubscriberCount(); got != 1 {
		t.Errorf("count=%d; want 1 after first unsubscribe", got)
	}

	sub2.Unsubscribe()
	if got := b.SubscriberCount(); got != 0 {
		t.Errorf("count=%d; want 0 after both unsubscribed", got)
	}
}

func TestBus_PayloadUnmarshal_KanbanAdded(t *testing.T) {
	b := New()
	sub := b.Subscribe(TopicKanbanAdded)
	defer sub.Unsubscribe()

	want := KanbanAddedPayload{ID: "K-5", Title: "do the thing", Column: "TODO"}
	b.Publish(TopicKanbanAdded, want)

	ev := <-sub.C()
	var got KanbanAddedPayload
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got != want {
		t.Errorf("payload=%+v; want %+v", got, want)
	}
}

func TestBus_PayloadUnmarshal_KanbanMoved(t *testing.T) {
	b := New()
	sub := b.Subscribe(TopicKanbanMoved)
	defer sub.Unsubscribe()

	want := KanbanMovedPayload{ID: "K-3", From: "TODO", To: "IN PROGRESS"}
	b.Publish(TopicKanbanMoved, want)

	ev := <-sub.C()
	var got KanbanMovedPayload
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got != want {
		t.Errorf("payload=%+v; want %+v", got, want)
	}
}

func TestBus_PayloadUnmarshal_DispatchStarted(t *testing.T) {
	b := New()
	sub := b.Subscribe(TopicDispatchStarted)
	defer sub.Unsubscribe()

	ts := time.Now().UTC().Truncate(time.Second)
	want := DispatchStartedPayload{Agent: "backend", Project: "/path/to/proj", TS: ts}
	b.Publish(TopicDispatchStarted, want)

	ev := <-sub.C()
	var got DispatchStartedPayload
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got.Agent != want.Agent || got.Project != want.Project {
		t.Errorf("payload=%+v; want %+v", got, want)
	}
}

func TestBus_PayloadUnmarshal_DispatchFinished(t *testing.T) {
	b := New()
	sub := b.Subscribe(TopicDispatchFinished)
	defer sub.Unsubscribe()

	want := DispatchFinishedPayload{Agent: "backend", Project: "/proj", ExitCode: 0}
	b.Publish(TopicDispatchFinished, want)

	ev := <-sub.C()
	var got DispatchFinishedPayload
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Agent != want.Agent || got.ExitCode != want.ExitCode {
		t.Errorf("payload=%+v; want %+v", got, want)
	}
}

func TestBus_PayloadUnmarshal_Presence(t *testing.T) {
	b := New()
	sub := b.Subscribe(TopicPresence)
	defer sub.Unsubscribe()

	want := PresencePayload{User: "alice", Host: "dev-box", Status: "active"}
	b.Publish(TopicPresence, want)

	ev := <-sub.C()
	var got PresencePayload
	if err := json.Unmarshal(ev.Payload, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("payload=%+v; want %+v", got, want)
	}
}

func TestBus_ConcurrentPublishSubscribe(t *testing.T) {
	b := New()
	sub := b.Subscribe("")
	defer sub.Unsubscribe()

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
		}()
	}

	received := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range sub.C() {
			received++
			if received == n {
				return
			}
		}
	}()

	wg.Wait()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("only received %d/%d events", received, n)
	}
}

func TestBus_OverflowDropsSubscriber(t *testing.T) {
	b := New()
	sub := b.Subscribe("") // buffer = subBufferSize

	// Fill the buffer + 1 to trigger overflow detection on next Publish.
	for i := 0; i < subBufferSize+1; i++ {
		b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
	}

	// Give the overflow goroutine a moment to unsubscribe.
	time.Sleep(20 * time.Millisecond)

	if b.SubscriberCount() != 0 {
		t.Errorf("subscriber count=%d; want 0 after overflow", b.SubscriberCount())
	}
	_ = sub // ensure sub is referenced
}

func TestBus_PublishReturnsEvent(t *testing.T) {
	b := New()
	ev := b.Publish(TopicPresence, PresencePayload{User: "bob", Host: "laptop", Status: "idle"})
	if ev.Topic != TopicPresence {
		t.Errorf("topic=%q; want %q", ev.Topic, TopicPresence)
	}
	if ev.Seq <= 0 {
		t.Error("seq should be > 0")
	}
}

func TestBus_EmptyBusPublishNoError(t *testing.T) {
	b := New()
	// Publishing with no subscribers should not panic or block.
	b.Publish(TopicKanbanAdded, KanbanAddedPayload{ID: "K-1"})
}

func TestBus_AllTopicConstants(t *testing.T) {
	topics := []string{
		TopicKanbanAdded,
		TopicKanbanMoved,
		TopicDispatchStarted,
		TopicDispatchFinished,
		TopicPresence,
	}
	b := New()
	for _, topic := range topics {
		sub := b.Subscribe(topic)
		b.Publish(topic, PresencePayload{}) // any payload works
		select {
		case <-sub.C():
		case <-time.After(200 * time.Millisecond):
			t.Errorf("topic %q: event not delivered", topic)
		}
		sub.Unsubscribe()
	}
}
