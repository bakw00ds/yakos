package dispatch

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
)

// Tests for the K-139a fixup round: cooldown scope, explicit re-pin, pin scope.

func allProbesOK(t *testing.T) {
	t.Helper()
	withProbe(t, func(string) probeResult { return probeResult{OK: true} })
}

func failThrice(rt string) {
	for i := 0; i < 3; i++ {
		noteRun(context.Background(), rt, 1, nil)
	}
}

const fallbackToCodex = "default-fallback: [codex]\n"

// With no policy file the P0a chain is reproduced exactly, failures included:
// three failed claude runs must not send the conversation to codex.
func TestRoute_NoPolicyThreeFailuresStayOnClaude(t *testing.T) {
	captureRouteLog(t)
	allProbesOK(t)
	resetRouterState(t)
	root, project := routingRoot(t), projectWithYML(t, fallbackToCodex)
	for i := 0; i < 3; i++ {
		got, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "c" })
		if err != nil || got.Runtime != "claude" {
			t.Fatalf("turn %d: %+v %v", i, got, err)
		}
		noteRun(context.Background(), "claude", 1, nil)
	}
	got, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "c" })
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy == RuntimeByFallback {
		t.Fatalf("after 3 failures with no policy: %+v %v", got, err)
	}
	// And without a conversation id either.
	got, err = route(t, root, project, "plain", nil)
	if err != nil || got.Runtime != "claude" {
		t.Fatalf("no conversation: %+v %v", got, err)
	}
}

// A pinned conversation is never moved by the cooldown: its runtime is used
// even while cooling (the turn may fail with the runtime's own error).
func TestRoute_PinnedConversationStaysPinnedThroughCooldown(t *testing.T) {
	captureRouteLog(t)
	allProbesOK(t)
	setPolicy(t, "# no rules\n")
	root, project := routingRoot(t), projectWithYML(t, fallbackToCodex)
	first, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "c" })
	if err != nil || first.Runtime != "claude" {
		t.Fatalf("first: %+v %v", first, err)
	}
	failThrice("claude")
	next, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "c" })
	if err != nil || next.Runtime != "claude" || next.RuntimeChosenBy != RuntimeBySticky {
		t.Fatalf("a pinned conversation must stay on claude: %+v %v", next, err)
	}
}

// A new conversation (no pin) does skip a cooling runtime when a policy file
// exists; so does a request with no conversation id.
func TestRoute_UnpinnedConversationSkipsCoolingRuntime(t *testing.T) {
	captureRouteLog(t)
	allProbesOK(t)
	setPolicy(t, "# no rules\n")
	root, project := routingRoot(t), projectWithYML(t, fallbackToCodex)
	failThrice("claude")
	for _, conv := range []string{"fresh", ""} {
		got, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = conv })
		if err != nil || got.Runtime != "codex" || got.FallbackFrom != "claude" {
			t.Fatalf("conversation %q: %+v %v", conv, got, err)
		}
	}
}

// The operator moving a conversation (an explicit runtime) re-pins it; a later
// turn with no override stays where the operator put it, not on the first pin.
func TestRoute_ExplicitOverrideRepinsTheConversation(t *testing.T) {
	captureRouteLog(t)
	allProbesOK(t)
	setPolicy(t, "rules:\n  - match: {class: chat}\n    action: {runtime: codex}\n")
	root, project := routingRoot(t), projectWithYML(t, "")
	turn := func(mut func(*routeInput)) *routed {
		t.Helper()
		got, err := route(t, root, project, "plain", func(in *routeInput) {
			in.ConversationID = "c"
			if mut != nil {
				mut(in)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := turn(func(in *routeInput) { in.Class = "chat" }); got.Runtime != "codex" {
		t.Fatalf("turn 1: %s", got.Runtime)
	}
	if got := turn(func(in *routeInput) { in.RuntimeOverride = "claude" }); got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeByOverride {
		t.Fatalf("turn 2: %s by %s", got.Runtime, got.RuntimeChosenBy)
	}
	if got := turn(nil); got.Runtime != "claude" || got.RuntimeChosenBy != RuntimeBySticky {
		t.Fatalf("turn 3 must stay on claude: %s by %s", got.Runtime, got.RuntimeChosenBy)
	}
	// A rule-derived decision never overwrites the pin: class chat on turn 4 still
	// stays on claude.
	if got := turn(func(in *routeInput) { in.Class = "chat" }); got.Runtime != "claude" {
		t.Fatalf("turn 4: a rule must not move a pinned conversation: %s", got.Runtime)
	}
}

// A model override alone re-pins the model too.
func TestRoute_ExplicitModelOverrideRepinsTheModel(t *testing.T) {
	captureRouteLog(t)
	allProbesOK(t)
	setPolicy(t, "# no rules\n")
	root, project := routingRoot(t), projectWithYML(t, "")
	run := func(model string) *routed {
		t.Helper()
		got, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID, in.ModelOverride = "m", model })
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	run("")
	if got := run("haiku"); got.Model != "haiku" {
		t.Fatalf("override: %s", got.Model)
	}
	if got := run(""); got.Model != "haiku" || got.ModelChosenBy != "sticky" {
		t.Fatalf("the override must have re-pinned: %s by %s", got.Model, got.ModelChosenBy)
	}
}

// sec M1: a pin is scoped to the project root it was made under.
func TestRoute_PinFromAnotherProjectIsIgnored(t *testing.T) {
	captureRouteLog(t)
	allProbesOK(t)
	setPolicy(t, "# no rules\n")
	root := routingRoot(t)
	a, b := projectWithYML(t, "default-runtime: codex\n"), projectWithYML(t, "")
	got, err := route(t, root, a, "plain", func(in *routeInput) { in.ConversationID = "shared" })
	if err != nil || got.Runtime != "codex" {
		t.Fatalf("project A: %+v %v", got, err)
	}
	got, err = route(t, root, b, "plain", func(in *routeInput) { in.ConversationID = "shared" })
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy == RuntimeBySticky || strings.HasPrefix(got.Decision.Reason, "sticky") {
		t.Fatalf("project B must get its own decision, not A's pin: %+v %v", got, err)
	}
	// B's decision replaced the pin: B's next turn is B's pin.
	got, _ = route(t, root, b, "plain", func(in *routeInput) { in.ConversationID = "shared" })
	if got.RuntimeChosenBy != RuntimeBySticky || got.Runtime != "claude" {
		t.Fatalf("project B's own pin: %+v", got)
	}
}

// sec M1: a policy edit (new sha) drops the old pin.
func TestRoute_PolicyEditDropsThePin(t *testing.T) {
	captureRouteLog(t)
	allProbesOK(t)
	dir := setPolicy(t, "rules:\n  - match: {class: chat}\n    action: {runtime: codex}\n")
	root, project := routingRoot(t), projectWithYML(t, "")
	got, err := route(t, root, project, "plain", func(in *routeInput) { in.ConversationID, in.Class = "c", "chat" })
	if err != nil || got.Runtime != "codex" {
		t.Fatalf("turn 1: %+v %v", got, err)
	}
	rewritePolicy(t, dir, "rules:\n  - match: {class: chat}\n    action: {runtime: agy}\n")
	got, err = route(t, root, project, "plain", func(in *routeInput) { in.ConversationID = "c" })
	if err != nil || got.Runtime != "claude" || got.RuntimeChosenBy == RuntimeBySticky {
		t.Fatalf("an edited policy must not honour the old pin: %+v %v", got, err)
	}
}

// rewritePolicy replaces the policy file in dir (same 0600 trust), keeping the
// router state, so a test can edit the policy between turns.
func rewritePolicy(t *testing.T, dir, body string) {
	t.Helper()
	p := routerpolicy.Path(dir)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
}
