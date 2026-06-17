package dispatch_test

// registry_test.go — unit tests for SessionRegistry.
//
// Coverage:
//   1. Add registers an entry; Get returns it.
//   2. Add is a no-op when sessionID already exists (matches chatState.add behaviour).
//   3. UpdateStatus changes the status field; no-op for unknown session.
//   4. Remove deletes the entry; no-op for unknown session.
//   5. Snapshot returns a copy of all entries; changes after snapshot are not reflected.
//   6. TaskPreview is truncated to maxTaskPreview (120) runes at Add time.
//   7. All methods are race-safe (tested via -race flag + concurrent callers).
//   8. Default Status is StatusRunning when Add entry has empty status.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

func makeEntry(sessionID, agent, operatorID string) dispatch.SessionEntry {
	return dispatch.SessionEntry{
		SessionID:  sessionID,
		Agent:      agent,
		Runtime:    "claude",
		OperatorID: operatorID,
		StartedAt:  time.Now(),
		Status:     dispatch.StatusRunning,
	}
}

func TestRegistry_AddAndGet(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	e := makeEntry("sess-1", "backend", "alice")
	r.Add(e)

	got, ok := r.Get("sess-1")
	if !ok {
		t.Fatal("Get: expected entry to exist after Add")
	}
	if got.Agent != "backend" {
		t.Errorf("Get: agent=%q; want 'backend'", got.Agent)
	}
	if got.OperatorID != "alice" {
		t.Errorf("Get: operatorID=%q; want 'alice'", got.OperatorID)
	}
	if got.Status != dispatch.StatusRunning {
		t.Errorf("Get: status=%q; want StatusRunning", got.Status)
	}
}

func TestRegistry_AddDefaultsToRunning(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	e := makeEntry("sess-2", "backend", "alice")
	e.Status = "" // zero value — should default to StatusRunning
	r.Add(e)

	got, ok := r.Get("sess-2")
	if !ok {
		t.Fatal("Get: expected entry")
	}
	if got.Status != dispatch.StatusRunning {
		t.Errorf("status=%q after Add with empty status; want StatusRunning", got.Status)
	}
}

func TestRegistry_AddIsNoOpForExistingSession(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	e1 := makeEntry("sess-dup", "backend", "alice")
	r.Add(e1)

	// Second add with different agent — should be ignored.
	e2 := makeEntry("sess-dup", "frontend", "alice")
	r.Add(e2)

	got, _ := r.Get("sess-dup")
	if got.Agent != "backend" {
		t.Errorf("Agent=%q after duplicate Add; want 'backend' (second Add must be no-op)", got.Agent)
	}
}

func TestRegistry_UpdateStatus(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	r.Add(makeEntry("sess-3", "qa", "bob"))

	r.UpdateStatus("sess-3", dispatch.StatusFinished)
	got, _ := r.Get("sess-3")
	if got.Status != dispatch.StatusFinished {
		t.Errorf("status=%q; want StatusFinished", got.Status)
	}

	r.UpdateStatus("sess-3", dispatch.StatusFailed)
	got, _ = r.Get("sess-3")
	if got.Status != dispatch.StatusFailed {
		t.Errorf("status=%q; want StatusFailed", got.Status)
	}
}

func TestRegistry_UpdateStatus_NoOpForUnknown(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	// Should not panic.
	r.UpdateStatus("nonexistent", dispatch.StatusFinished)
}

func TestRegistry_Remove(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	r.Add(makeEntry("sess-4", "security", "carol"))

	r.Remove("sess-4")
	_, ok := r.Get("sess-4")
	if ok {
		t.Error("Get: entry should be absent after Remove")
	}
}

func TestRegistry_Remove_NoOpForUnknown(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	// Should not panic.
	r.Remove("no-such-session")
}

func TestRegistry_Snapshot(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	r.Add(makeEntry("s1", "a1", "alice"))
	r.Add(makeEntry("s2", "a2", "bob"))

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("Snapshot len=%d; want 2", len(snap))
	}

	// Mutations after snapshot must not affect the snapshot.
	r.Remove("s1")
	if len(snap) != 2 {
		t.Errorf("Snapshot slice affected by Remove; should be independent copy")
	}
}

func TestRegistry_TaskPreviewTruncatedAt120Runes(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	// Build a string that is definitely >120 runes (ASCII: bytes == runes here).
	longTask := strings.Repeat("x", 200)
	e := makeEntry("sess-trunc", "backend", "alice")
	e.TaskPreview = longTask
	r.Add(e)

	got, _ := r.Get("sess-trunc")
	if len([]rune(got.TaskPreview)) > 120 {
		t.Errorf("TaskPreview has %d runes after Add; want <=120", len([]rune(got.TaskPreview)))
	}
	// Exact truncation.
	if len([]rune(got.TaskPreview)) != 120 {
		t.Errorf("TaskPreview has %d runes; want exactly 120", len([]rune(got.TaskPreview)))
	}
}

func TestRegistry_TaskPreviewTruncatedMultibyteRunes(t *testing.T) {
	// Each emoji is 1 rune, 4 bytes.  200 emojis = 200 runes.
	// After truncation should be 120 runes (480 bytes), not 120 bytes.
	r := dispatch.NewSessionRegistry()
	multibyteTask := strings.Repeat("🔥", 200)
	e := makeEntry("sess-mb", "backend", "alice")
	e.TaskPreview = multibyteTask
	r.Add(e)

	got, _ := r.Get("sess-mb")
	runeCount := len([]rune(got.TaskPreview))
	if runeCount != 120 {
		t.Errorf("TaskPreview multibyte: got %d runes; want 120", runeCount)
	}
}

func TestRegistry_TaskPreviewExactly120Runes(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	task120 := strings.Repeat("y", 120)
	e := makeEntry("sess-120", "qa", "dave")
	e.TaskPreview = task120
	r.Add(e)

	got, _ := r.Get("sess-120")
	if got.TaskPreview != task120 {
		t.Errorf("TaskPreview changed for 120-rune input; want unchanged")
	}
}

// TestRegistry_ConversationIDStoredAndRetrieved verifies that ConversationID
// is preserved through Add / Get / Snapshot so the fleet handler can include it
// in /api/fleet rows without a second lookup.
func TestRegistry_ConversationIDStoredAndRetrieved(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	e := dispatch.SessionEntry{
		SessionID:      "sess-conv",
		ConversationID: "conv-abc123",
		Agent:          "backend",
		Runtime:        "claude",
		OperatorID:     "alice",
		StartedAt:      time.Now(),
		Status:         dispatch.StatusRunning,
	}
	r.Add(e)

	got, ok := r.Get("sess-conv")
	if !ok {
		t.Fatal("Get: expected entry to exist after Add")
	}
	if got.ConversationID != "conv-abc123" {
		t.Errorf("ConversationID=%q; want 'conv-abc123'", got.ConversationID)
	}

	// Snapshot must also carry the ConversationID.
	snap := r.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot len=%d; want 1", len(snap))
	}
	if snap[0].ConversationID != "conv-abc123" {
		t.Errorf("Snapshot[0].ConversationID=%q; want 'conv-abc123'", snap[0].ConversationID)
	}
}

// TestRegistry_ConversationIDEmptyWhenNotSet verifies that zero-value ConversationID
// is handled without panic and is returned as empty string.
func TestRegistry_ConversationIDEmptyWhenNotSet(t *testing.T) {
	r := dispatch.NewSessionRegistry()
	r.Add(makeEntry("sess-no-conv", "backend", "alice"))

	got, ok := r.Get("sess-no-conv")
	if !ok {
		t.Fatal("Get: expected entry to exist after Add")
	}
	if got.ConversationID != "" {
		t.Errorf("ConversationID=%q; want empty string when not set", got.ConversationID)
	}
}

func TestRegistry_ConcurrentRaceSafety(t *testing.T) {
	// This test is meaningful only with -race but is correct without it too.
	r := dispatch.NewSessionRegistry()
	const n = 50
	var wg sync.WaitGroup

	// Concurrent Adds.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "sess-race-" + string(rune('A'+i%26))
			r.Add(makeEntry(id, "agent", "op"))
		}(i)
	}

	// Concurrent Snapshots.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Snapshot()
		}()
	}

	// Concurrent UpdateStatus.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "sess-race-" + string(rune('A'+i%26))
			r.UpdateStatus(id, dispatch.StatusFinished)
		}(i)
	}

	// Concurrent Removes.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "sess-race-" + string(rune('A'+i%26))
			r.Remove(id)
		}(i)
	}

	wg.Wait()
}
