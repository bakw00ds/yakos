package cost

import (
	"strings"
	"testing"
)

func TestPrintTable_BasicShape(t *testing.T) {
	rpt := Report{
		Events: 3,
		Rows: []Row{
			{Key: "claude", Count: 3, OK: 2, Fail: 1, TotalDurationS: 60, TotalInTokens: 500, TotalOutTokens: 250},
		},
	}
	var sb strings.Builder
	if err := PrintTable(&sb, rpt, "", "runtime"); err != nil {
		t.Fatalf("PrintTable: %v", err)
	}
	out := sb.String()

	// Header line
	if !strings.Contains(out, "yakos cost — 3 dispatch event(s) by runtime") {
		t.Errorf("missing header; got:\n%s", out)
	}
	// Column headers
	if !strings.Contains(out, "key") || !strings.Contains(out, "est_in_tok") {
		t.Errorf("missing column headers; got:\n%s", out)
	}
	// Data row
	if !strings.Contains(out, "claude") {
		t.Errorf("missing data row; got:\n%s", out)
	}
	// TOTAL row
	if !strings.Contains(out, "TOTAL") {
		t.Errorf("missing TOTAL row; got:\n%s", out)
	}
}

func TestPrintTable_SinceInHeader(t *testing.T) {
	rpt := Report{Events: 1, Rows: []Row{{Key: "r", Count: 1, OK: 1}}}
	var sb strings.Builder
	_ = PrintTable(&sb, rpt, "2026-05-01", "agent")
	if !strings.Contains(sb.String(), "since 2026-05-01") {
		t.Errorf("expected 'since 2026-05-01' in header; got:\n%s", sb.String())
	}
}

func TestPrintTable_KeyTruncatedAt24(t *testing.T) {
	longKey := strings.Repeat("x", 30)
	rpt := Report{Events: 1, Rows: []Row{{Key: longKey, Count: 1, OK: 1}}}
	var sb strings.Builder
	_ = PrintTable(&sb, rpt, "", "agent")
	// The key should be truncated to 24 chars in the output
	if strings.Contains(sb.String(), longKey) {
		t.Errorf("key should be truncated; found full 30-char key in:\n%s", sb.String())
	}
	if !strings.Contains(sb.String(), longKey[:24]) {
		t.Errorf("expected truncated key %q; got:\n%s", longKey[:24], sb.String())
	}
}

func TestPrintJSON_Shape(t *testing.T) {
	rpt := Report{
		Events: 2,
		Rows: []Row{
			{Key: "claude", Count: 2, OK: 2, TotalDurationS: 30, TotalInTokens: 100, TotalOutTokens: 50},
		},
	}
	var sb strings.Builder
	if err := PrintJSON(&sb, rpt); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, `"events":2`) {
		t.Errorf("missing events:2 in JSON; got: %s", out)
	}
	if !strings.Contains(out, `"rows"`) {
		t.Errorf("missing rows in JSON; got: %s", out)
	}
	if !strings.Contains(out, `"key":"claude"`) {
		t.Errorf("missing key:claude in JSON; got: %s", out)
	}
}

func TestPrintNoFiles(t *testing.T) {
	var sb strings.Builder
	_ = PrintNoFiles(&sb, false, "/home/user/.yakos-state")
	if !strings.Contains(sb.String(), "no dispatch-log.ndjson found at /home/user/.yakos-state") {
		t.Errorf("unexpected output: %s", sb.String())
	}
}

func TestPrintNoFiles_JSON(t *testing.T) {
	var sb strings.Builder
	_ = PrintNoFiles(&sb, true, "/home/user/.yakos-state")
	if strings.TrimSpace(sb.String()) != `{"events":0,"rows":[]}` {
		t.Errorf("unexpected JSON output: %s", sb.String())
	}
}

func TestPrintNoEvents(t *testing.T) {
	var sb strings.Builder
	_ = PrintNoEvents(&sb, false, "2026-05-01")
	if !strings.Contains(sb.String(), "no dispatch_finished events match the filter (since=2026-05-01)") {
		t.Errorf("unexpected output: %s", sb.String())
	}
}

func TestPrintNoEvents_JSON(t *testing.T) {
	var sb strings.Builder
	_ = PrintNoEvents(&sb, true, "2026-05-01")
	if strings.TrimSpace(sb.String()) != `{"events":0,"rows":[]}` {
		t.Errorf("unexpected JSON output: %s", sb.String())
	}
}
