package consoleui

// K-98 / K-86 review r1 finding 4: on the networked listener the WS operator
// ID is server-derived. A bearer-only connection has none, so its hello
// operator_id must never be trusted for owner-scoped filtering.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

// dialNetworked opens /v1/events on a networked handler. identity, when
// non-nil, is stamped on the request as the resolver would have; bearer-only
// otherwise. The client sends a hello claiming helloOp.
func dialNetworked(t *testing.T, bus *wsbus.Bus, identity *netid.Identity, helloOp string) *websocket.Conn {
	t.Helper()
	pm := NewPresenceManager(bus)
	h := buildConsoleWSHandlerNetworked(testToken, bus, pm, []string{"127.0.0.1:7890"})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if identity != nil {
			r = injectIdentity(r, *identity)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	cfg, err := websocket.NewConfig("ws"+strings.TrimPrefix(ts.URL, "http")+"/v1/events", "http://127.0.0.1:7890/")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Header = http.Header{"Origin": {"http://127.0.0.1:7890"}}
	if identity == nil || identity.AuthMethod == netid.AuthMethodCert {
		// bearer-only, or a cert client (which also presents the bearer)
		cfg.Protocol = []string{consoleSubprotocol, testToken}
	}
	conn, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	waitForHello(t, conn, bus, helloOp)
	waitForNSubscribers(t, bus, 1)
	return conn
}

func publishFleet(bus *wsbus.Bus, owner string) {
	bus.PublishMeta(wsbus.TopicFleetStarted, wsbus.FleetStartedPayload{
		SessionID: "sess-" + owner, Agent: "backend", TS: time.Now().UTC(),
	}, wsbus.EventMeta{OwnerOperatorID: owner})
}

func TestWSNetworked_BearerOnly_SpoofedHello_GetsNoScopedEvents(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	conn := dialNetworked(t, bus, nil, "alice") // claims to be alice
	publishFleet(bus, "alice")
	assertNoFleetEvent(t, conn, "bearer-only spoofer", 400*time.Millisecond)
}

func TestWSNetworked_CertIdentityWins_OverSpoofedHello(t *testing.T) {
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	id := netid.Identity{OperatorID: "bob", Role: netid.RoleRead, Authenticated: true, Resolved: true, AuthMethod: netid.AuthMethodCert}
	conn := dialNetworked(t, bus, &id, "alice") // authenticated bob claims alice

	publishFleet(bus, "alice")
	assertNoFleetEvent(t, conn, "bob claiming alice", 400*time.Millisecond)

	publishFleet(bus, "bob")
	if _, ok := receiveFleetEvent(t, conn, time.Now().Add(2*time.Second)); !ok {
		t.Fatal("authenticated bob did not receive his own fleet event")
	}
}

func TestWSLoopback_HelloOperatorStillCooperative(t *testing.T) {
	bus, _, wsURL, _ := newConsoleWSTestServer(t)
	conn := dialAndHandshake(t, wsURL, testToken, "alice")
	waitForNSubscribers(t, bus, 1)
	publishFleet(bus, "alice")
	if _, ok := receiveFleetEvent(t, conn, time.Now().Add(2*time.Second)); !ok {
		t.Fatal("loopback: hello operator_id no longer honoured")
	}
}
