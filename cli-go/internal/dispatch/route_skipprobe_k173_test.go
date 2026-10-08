package dispatch

import (
	"strings"
	"testing"
)

// K-173: SkipProbe leaves the availability probe out and nothing else. A runtime
// that is down routes anyway, but a disabled runtime, a disabled model and a
// sensitive request are refused as before.
func TestRoute_SkipProbeSkipsOnlyTheProbe(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	probed := 0
	withProbe(t, func(name string) probeResult {
		probed++
		return probeResult{Reason: "CLI not found on PATH"}
	})
	routeWith := func(yml string, mut func(*routeInput)) (*routed, error) {
		return route(t, root, projectWithYML(t, yml), "plain", func(in *routeInput) {
			in.RuntimeOverride = "claude"
			if mut != nil {
				mut(in)
			}
		})
	}

	if _, err := routeWith("", nil); err == nil {
		t.Fatal("the unavailable explicit runtime routed without SkipProbe")
	}
	probed = 0
	got, err := routeWith("", func(in *routeInput) { in.SkipProbe = true })
	if err != nil || got.Runtime != "claude" || probed != 0 {
		t.Fatalf("SkipProbe: got %+v err %v probes %d", got, err, probed)
	}

	_, err = routeWith("router:\n  disable_models: [opus]\n", func(in *routeInput) { in.SkipProbe, in.ModelOverride = true, "opus" })
	if err == nil || !strings.Contains(err.Error(), "disable_models") {
		t.Errorf("a disabled model was not refused under SkipProbe: %v", err)
	}
	_, err = routeWith("router:\n  disable_runtimes: [claude]\n", func(in *routeInput) { in.SkipProbe = true })
	if ex, ok := AsExplicitRuntimeError(err); !ok || !strings.Contains(ex.Reason, "disabled by this project") {
		t.Errorf("a disabled runtime was not refused under SkipProbe: %v", err)
	}
	_, err = routeWith("", func(in *routeInput) { in.SkipProbe, in.Task = true, "key AKIA"+"IOSFODNN7EXAMPLE" })
	if err != nil {
		t.Errorf("sensitive on claude under SkipProbe should route to claude: %v", err)
	}
	// A sensitive request never leaves claude, whatever runtime was named.
	got, err = route(t, root, projectWithYML(t, ""), "plain", func(in *routeInput) {
		in.RuntimeOverride, in.SkipProbe, in.Task = "codex", true, "key AKIA"+"IOSFODNN7EXAMPLE"
	})
	if err != nil || got.Runtime != "claude" || got.Decision.RouteClass != "sensitive" {
		t.Errorf("sensitive under SkipProbe: got %+v err %v", got, err)
	}
}
