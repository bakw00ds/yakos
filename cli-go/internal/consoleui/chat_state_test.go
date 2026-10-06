package consoleui

import "testing"

// A cancel removes the session's entry at once so the pane can resend, but the
// cancelled turn's goroutine removes "its" entry when it finally ends. Keyed by
// sessionId alone that late remove deleted the NEXT turn's entry. Entries are
// keyed by generation now.
func TestChatState_ALateRemoveDoesNotDeleteTheNextTurnsEntry(t *testing.T) {
	cs := newChatState()
	cancelled1, cancelled2 := false, false

	gen1, ok := cs.add("s", func() { cancelled1 = true })
	if !ok {
		t.Fatal("the first turn was refused")
	}
	if _, dup := cs.add("s", func() {}); dup {
		t.Fatal("a second turn on a session that is running was accepted")
	}

	cs.cancel("s") // the pane's cancel: the entry goes at once
	if !cancelled1 {
		t.Fatal("cancel did not reach the first turn")
	}

	gen2, ok := cs.add("s", func() { cancelled2 = true }) // the pane resends on the same session
	if !ok {
		t.Fatal("the resend after a cancel was refused")
	}
	if gen2 == gen1 {
		t.Fatalf("both turns have generation %d", gen1)
	}

	cs.remove("s", gen1) // the cancelled turn's goroutine finishes and releases its entry

	if _, dup := cs.add("s", func() {}); dup {
		t.Error("the late remove deleted the second turn's entry: a third turn was accepted while it runs")
	}
	cs.cancel("s")
	if !cancelled2 {
		t.Error("the late remove deleted the second turn's entry: it can no longer be cancelled")
	}
}

func TestChatState_RemoveReleasesItsOwnEntry(t *testing.T) {
	cs := newChatState()
	gen, _ := cs.add("s", func() {})
	cs.remove("s", gen)
	if _, ok := cs.add("s", func() {}); !ok {
		t.Error("the session was not released by its own goroutine's remove")
	}
	// Removing what is not there, or an old generation twice, is harmless.
	cs.remove("nope", 1)
	cs.remove("s", gen)
	if _, dup := cs.add("s", func() {}); dup {
		t.Error("a stale remove released a live entry")
	}
}
