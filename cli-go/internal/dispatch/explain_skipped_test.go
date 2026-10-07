package dispatch

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A runtime skipped for the cooldown reaches the explain output as a cooling
// Skip (K-139b); the seconds left are in the reason, the flag is what explain
// renders.
func TestExplain_ReportsACoolingRuntimeAsSkipped(t *testing.T) {
	captureRouteLog(t)
	root := routingRoot(t)
	project := projectWithYML(t, "")
	setPolicy(t, "# a policy file with no rules still engages the cooldown\n")
	clk := &fakeClock{t: time.Unix(5_000, 0)}
	seedCooldownForTest(t, project, "agy", clk.now)
	d, err := Explain(context.Background(), ExplainQuery{YakosRoot: root, Project: project, Agent: "pinned-fb"})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Skipped) != 1 || d.Skipped[0].Runtime != "agy" || !d.Skipped[0].Cooling ||
		!strings.HasPrefix(d.Skipped[0].Reason, coolingReasonPrefix) {
		t.Fatalf("Skipped = %+v, want agy cooling", d.Skipped)
	}
	if d.Runtime != "codex" || d.FallbackFrom != "agy" {
		t.Errorf("route = %s from %q", d.Runtime, d.FallbackFrom)
	}
}
