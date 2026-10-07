package dispatch

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// matrixResult renders one routed dispatch (or its error) the way the golden
// does. Temp paths never appear in the rows (checked by the generator run).
func matrixResult(rr *routed, err error) string {
	if err != nil {
		return "ERR " + err.Error()
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s|%v", rr.Runtime, rr.RuntimeChosenBy, rr.FallbackFrom, rr.Model, rr.ModelChosenBy, rr.ModelExplicit)
}

// With no policy file the router must reproduce the pre-router chain. The
// expected rows are a frozen table generated at base 24f0a6ba, not computed from
// chooseRuntime, so a broken chain (a skipped fallback, a changed precedence) is
// caught here rather than mirrored by the oracle.
func TestRoute_NoPolicyMatchesFrozenP0aGolden(t *testing.T) {
	captureRouteLog(t)
	resetRouterState(t)
	root := routingRoot(t)
	f, err := os.Open(filepath.Join("testdata", "route_p0a_golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	golden := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("bad golden line %q", line)
		}
		golden[k] = v
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	seen, bad := 0, 0
	for pi, yml := range matrixProjects {
		project := projectWithYML(t, yml)
		for _, probe := range matrixProbeNames() {
			withProbe(t, matrixProbes[probe])
			for _, agent := range matrixAgents(t, root) {
				for oi, ov := range matrixOverrides {
					key := matrixKey(pi, probe, agent, oi)
					want, ok := golden[key]
					if !ok {
						t.Fatalf("no golden row for %s", key)
					}
					rr, err := routeDispatch(context.Background(), routeInput{
						YakosRoot: root, Project: project, Agent: agent,
						RuntimeOverride: ov.RuntimeOverride, RuntimeEnvDefault: ov.RuntimeEnvDefault,
						RuntimeFallbackOptIn: ov.RuntimeFallbackOptIn, ModelOverride: ov.ModelOverride,
						EvalRunID: ov.EvalRunID, Class: ov.Class, TaskBytes: ov.TaskBytes, ConversationID: ov.ConversationID,
					})
					seen++
					if err == nil && (rr.Decision.RuleID != "R0" || rr.Decision.PolicySHA != "") {
						t.Fatalf("%s: no policy must mean R0 and no sha: %+v", key, rr.Decision)
					}
					if got := matrixNorm(matrixResult(rr, err), root, project); got != want {
						bad++
						if bad <= 10 {
							t.Errorf("%s diverged from base 24f0a6ba:\n want %s\n got  %s", key, want, got)
						}
					}
				}
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d rows diverged", bad, seen)
	}
	if seen != len(golden) {
		t.Fatalf("matrix has %d rows, golden %d", seen, len(golden))
	}
}
