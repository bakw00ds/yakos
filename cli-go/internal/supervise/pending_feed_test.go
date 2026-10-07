package supervise

import (
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// K-146: a critical finding written by the dispatch event-scan feed for a codex
// or agy run is listed by `yakos supervise pending`; a warn finding is not.
func TestPending_ListsEventScanFeedFinding(t *testing.T) {
	for _, rt := range []string{"codex", "agy"} {
		t.Run(rt, func(t *testing.T) {
			cfg := newCfg(t, "pending")
			cfg, repo := makeProject(t, cfg, "proj")
			writeYAML(t, repo, "supervisor:\n  enabled: true\n")
			wc := cfg.AgentControlRoot + "/proj/work/current"
			now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
			crit := supervisorstream.FeedFinding{Runtime: rt, Kind: "tool_result", Severity: "critical", Labels: []string{"ignore-previous-instructions"}, Session: "s1"}
			warn := supervisorstream.FeedFinding{Runtime: rt, Kind: "text", Severity: "warn", Labels: []string{"long-base64-payload"}, Session: "s1"}
			if err := supervisorstream.ReportFeedFinding(wc, warn, now); err != nil {
				t.Fatal(err)
			}
			if err := supervisorstream.ReportFeedFinding(wc, crit, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			res, err := Run(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if res.PendingCount != 1 {
				t.Fatalf("PendingCount = %d, want 1 (the critical one)", res.PendingCount)
			}
			out := cfgOut(cfg)
			if !strings.Contains(out, "ignore-previous-instructions") || !strings.Contains(out, rt+" tool_result") {
				t.Errorf("pending output missing the feed finding: %q", out)
			}
			if strings.Contains(out, "long-base64-payload") {
				t.Errorf("a warn finding must not be pending: %q", out)
			}
		})
	}
}
