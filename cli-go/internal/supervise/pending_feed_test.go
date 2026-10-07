package supervise

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

// K-146: findings written by the dispatch event-scan feed for a codex or agy
// run are listed by `yakos supervise pending` in a separate "no ack needed"
// section: never counted as pending, never given an ID to ack.
func TestPending_ListsEventScanFeedFindingAsDetected(t *testing.T) {
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
			if res.PendingCount != 0 {
				t.Fatalf("PendingCount = %d, want 0: detect-only findings need no ack", res.PendingCount)
			}
			if res.DetectedCount != 2 {
				t.Fatalf("DetectedCount = %d, want 2", res.DetectedCount)
			}
			out := cfgOut(cfg)
			if !strings.Contains(out, "no ack needed") || !strings.Contains(out, "ignore-previous-instructions") || !strings.Contains(out, rt+" tool_result") {
				t.Errorf("pending output missing the detected section: %q", out)
			}
			if strings.Contains(out, "supervise ack ") {
				t.Errorf("a detect-only finding must not offer an ack: %q", out)
			}
		})
	}
}

// A real escalation still counts as pending next to detect-only findings.
func TestPending_DetectedDoesNotMaskRealEscalation(t *testing.T) {
	cfg := newCfg(t, "pending")
	cfg, repo := makeProject(t, cfg, "proj")
	writeYAML(t, repo, "supervisor:\n  enabled: true\n")
	wc := cfg.AgentControlRoot + "/proj/work/current"
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	crit := supervisorstream.FeedFinding{Runtime: "codex", Kind: "text", Severity: "critical", Labels: []string{"disregard-system-prompt"}, Session: "s1"}
	if err := supervisorstream.ReportFeedFinding(wc, crit, now); err != nil {
		t.Fatal(err)
	}
	real := `{"ts":"2026-10-07T12:05:00Z","recommended_action":"surface_to_operator","rationale":"real one"}` + "\n"
	f, err := os.OpenFile(wc+"/supervisor-findings.ndjson", os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(real)
	_ = f.Close()
	res, err := Run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.PendingCount != 1 || res.DetectedCount != 1 {
		t.Fatalf("pending=%d detected=%d, want 1 and 1", res.PendingCount, res.DetectedCount)
	}
}
