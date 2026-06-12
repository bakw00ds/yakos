package consoleui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/wsbus"
)

// ---- PresenceManager tests --------------------------------------------------

func TestPresenceManager_JoinCreatesRecord(t *testing.T) {
	pm := NewPresenceManager(nil)
	hello := HelloMessage{
		Type:        "hello",
		OperatorID:  "alice",
		DisplayName: "Alice",
		Color:       "#ignored",
	}
	rec := pm.Join("conn-1", hello)

	if rec.OperatorID != "alice" {
		t.Errorf("OperatorID=%q; want %q", rec.OperatorID, "alice")
	}
	if rec.DisplayName != "Alice" {
		t.Errorf("DisplayName=%q; want %q", rec.DisplayName, "Alice")
	}
	if rec.Status != "online" {
		t.Errorf("Status=%q; want online", rec.Status)
	}
	// Color must be server-derived, not client-supplied #ignored.
	if rec.Color == "#ignored" {
		t.Error("Color must be server-derived; client color was not overridden")
	}
	// Color must be a valid #rrggbb.
	if len(rec.Color) != 7 || rec.Color[0] != '#' {
		t.Errorf("Color=%q; want #rrggbb format", rec.Color)
	}
}

func TestPresenceManager_Snapshot(t *testing.T) {
	pm := NewPresenceManager(nil)
	pm.Join("conn-1", HelloMessage{Type: "hello", OperatorID: "alice", DisplayName: "Alice"})
	pm.Join("conn-2", HelloMessage{Type: "hello", OperatorID: "bob", DisplayName: "Bob"})

	snap := pm.Snapshot()
	if len(snap) != 2 {
		t.Errorf("snapshot len=%d; want 2", len(snap))
	}
}

func TestPresenceManager_LeaveRemovesRecord(t *testing.T) {
	pm := NewPresenceManager(nil)
	pm.Join("conn-1", HelloMessage{Type: "hello", OperatorID: "alice"})
	pm.Leave("conn-1")

	snap := pm.Snapshot()
	if len(snap) != 0 {
		t.Errorf("snapshot len=%d; want 0 after leave", len(snap))
	}
}

func TestPresenceManager_LeaveUnknownConn(t *testing.T) {
	// Leave on unknown connID must be a no-op (not panic).
	pm := NewPresenceManager(nil)
	pm.Leave("nonexistent")
}

func TestPresenceManager_InvalidOperatorIDFallsToAnon(t *testing.T) {
	pm := NewPresenceManager(nil)
	// Leading dash = invalid.
	rec := pm.Join("conn-1", HelloMessage{Type: "hello", OperatorID: "--bad"})
	if rec.OperatorID == "--bad" {
		t.Error("invalid OperatorID should fall back to 'anon'")
	}
	if rec.OperatorID != "anon" {
		t.Errorf("OperatorID=%q; want 'anon' for invalid input", rec.OperatorID)
	}
}

func TestPresenceManager_EmptyOperatorIDFallsToAnon(t *testing.T) {
	pm := NewPresenceManager(nil)
	rec := pm.Join("conn-1", HelloMessage{Type: "hello"})
	if rec.OperatorID != "anon" {
		t.Errorf("OperatorID=%q; want 'anon' for empty input", rec.OperatorID)
	}
}

func TestPresenceManager_ServerDerivedColorDeterministic(t *testing.T) {
	// Same operatorId must always produce the same color.
	c1 := colorFromOperatorID("alice")
	c2 := colorFromOperatorID("alice")
	if c1 != c2 {
		t.Errorf("colorFromOperatorID not deterministic: %q != %q", c1, c2)
	}
}

func TestPresenceManager_ServerDerivedColorDifferent(t *testing.T) {
	// Different operatorIds should (almost certainly) produce different colors.
	c1 := colorFromOperatorID("alice")
	c2 := colorFromOperatorID("bob")
	if c1 == c2 {
		t.Error("different operatorIds produced same color (hash collision)")
	}
}

func TestPresenceManager_ColorFormat(t *testing.T) {
	// Server-derived color must be #rrggbb.
	color := colorFromOperatorID("testuser")
	if len(color) != 7 || color[0] != '#' {
		t.Errorf("colorFromOperatorID=%q; want #rrggbb", color)
	}
	for _, c := range color[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("colorFromOperatorID=%q; contains non-hex character %q", color, c)
		}
	}
}

func TestPresenceManager_PublishesJoinEvent(t *testing.T) {
	bus := wsbus.New()
	defer bus.Stop()

	sub := bus.Subscribe(wsbus.TopicPresence)
	defer sub.Unsubscribe()

	pm := NewPresenceManager(bus)
	pm.Join("conn-1", HelloMessage{Type: "hello", OperatorID: "alice", DisplayName: "Alice"})

	select {
	case ev := <-sub.C():
		if ev.Topic != wsbus.TopicPresence {
			t.Errorf("topic=%q; want %q", ev.Topic, wsbus.TopicPresence)
		}
		// Verify payload does not contain secret fields.
		for _, field := range []string{"token", "secret", "password", "credential"} {
			if strings.Contains(string(ev.Payload), `"`+field+`"`) {
				t.Errorf("presence payload contains sensitive field %q: %s", field, ev.Payload)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("presence join event not published within 1s")
	}
}

func TestPresenceManager_PublishesLeaveEvent(t *testing.T) {
	bus := wsbus.New()
	defer bus.Stop()

	pm := NewPresenceManager(bus)
	pm.Join("conn-1", HelloMessage{Type: "hello", OperatorID: "alice"})

	sub := bus.Subscribe(wsbus.TopicPresence)
	defer sub.Unsubscribe()

	// Drain the join event if already buffered.
	select {
	case <-sub.C():
	default:
	}

	pm.Leave("conn-1")

	select {
	case ev := <-sub.C():
		if ev.Topic != wsbus.TopicPresence {
			t.Errorf("topic=%q; want %q", ev.Topic, wsbus.TopicPresence)
		}
		// The leave event payload should have status "offline".
		payload := string(ev.Payload)
		if !strings.Contains(payload, `"offline"`) {
			t.Errorf("leave event payload should contain \"offline\": %s", payload)
		}
	case <-time.After(time.Second):
		t.Fatal("presence leave event not published within 1s")
	}
}

// TestPresencePayload_NoSecretFields verifies that the presenceBusPayload
// (published to the bus) contains no sensitive fields.
// This is a static check: the struct type itself must not have secret fields.
func TestPresencePayload_NoSecretFields(t *testing.T) {
	p := presenceBusPayload{
		OperatorID:  "alice",
		DisplayName: "Alice",
		Color:       "#a1b2c3",
		Status:      "online",
	}
	raw, _ := json.Marshal(p)
	s := string(raw)
	for _, field := range []string{"token", "secret", "password", "credential", "key"} {
		if strings.Contains(s, `"`+field+`"`) {
			t.Errorf("presenceBusPayload contains sensitive field %q: %s", field, s)
		}
	}
}

// ---- colorFromOperatorID tests -----------------------------------------------

func TestColorFromOperatorID_HighBit(t *testing.T) {
	// Colors should have high bit set (biased toward brightness).
	color := colorFromOperatorID("user")
	// Parse R, G, B.
	var r, g, b int
	if _, err := hexScan(color[1:3], color[3:5], color[5:7], &r, &g, &b); err != nil {
		t.Skipf("color parse: %v", err)
	}
	if r < 0x80 || g < 0x80 || b < 0x80 {
		t.Errorf("color=%q: expected R,G,B >= 0x80 (high bit set for brightness)", color)
	}
}

// hexScan parses three 2-char hex strings into int pointers.
func hexScan(rs, gs, bs string, r, g, b *int) (int, error) {
	var err error
	_, err = parseHex(rs, r)
	if err != nil {
		return 0, err
	}
	_, err = parseHex(gs, g)
	if err != nil {
		return 0, err
	}
	_, err = parseHex(bs, b)
	return 3, err
}

func parseHex(s string, v *int) (int, error) {
	if len(s) != 2 {
		return 0, &hexError{s}
	}
	n := 0
	for _, c := range s {
		n <<= 4
		switch {
		case c >= '0' && c <= '9':
			n += int(c - '0')
		case c >= 'a' && c <= 'f':
			n += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			n += int(c-'A') + 10
		default:
			return 0, &hexError{s}
		}
	}
	*v = n
	return 1, nil
}

type hexError struct{ s string }

func (e *hexError) Error() string { return "invalid hex: " + e.s }
