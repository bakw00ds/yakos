package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// auditLog returns a scratch ledger path (the hook-friendly name is built here).
func auditLog(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "ledger.ndjson")
}

func readAuditLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		out = append(out, m)
	}
	return out
}

func TestConfigChangedWritesOneAuditLine(t *testing.T) {
	log := auditLog(t)
	a := newAccountAt(Request{OperatorID: "alice"}, log, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	before, after := strings.Repeat("a", 64), strings.Repeat("b", 64)
	if err := a.ConfigChanged(ConfigChange{File: "router-policy.yml", Action: "models.pin", SHABefore: before, SHAAfter: after, Surface: "cli"}); err != nil {
		t.Fatal(err)
	}
	lines := readAuditLines(t, log)
	if len(lines) != 1 {
		t.Fatalf("%d lines", len(lines))
	}
	want := map[string]any{"type": "config_changed", "operator_id": "alice", "file": "router-policy.yml",
		"action": "models.pin", "policy_sha_before": before, "policy_sha_after": after, "surface": "cli", "ts": "2026-10-07T12:00:00Z"}
	for k, v := range want {
		if lines[0][k] != v {
			t.Errorf("%s = %v, want %v", k, lines[0][k], v)
		}
	}
	if len(lines[0]) != len(want) {
		t.Errorf("unexpected keys: %v", lines[0])
	}
}

func TestConfigChangedNamesNoPathAndSanitisesEveryField(t *testing.T) {
	log := auditLog(t)
	a := newAccountAt(Request{OperatorID: "evil\x1b[31m\nname"}, log, time.Now())
	err := a.ConfigChanged(ConfigChange{File: "model-registry.yml", Action: "a b/\n../etc", SHABefore: "not-hex", SHAAfter: "ABCDEF", Surface: "CLI"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	if strings.ContainsAny(strings.TrimSuffix(string(raw), "\n"), "\x1b\r") || strings.Count(string(raw), "\n") != 1 {
		t.Errorf("control characters reached the log: %q", raw)
	}
	l := readAuditLines(t, log)[0]
	if l["operator_id"] != "unknown" || l["action"] != "" || l["policy_sha_before"] != "" || l["surface"] != "" {
		t.Errorf("fields not reduced to the safe vocabulary: %v", l)
	}
}

func TestConfigChangedRefusesAnUnknownFile(t *testing.T) {
	log := auditLog(t)
	a := newAccountAt(Request{OperatorID: "alice"}, log, time.Now())
	for _, f := range []string{"", "/etc/passwd", "../router-policy.yml", "settings.json", "dispatch-log.ndjson"} {
		if err := a.ConfigChanged(ConfigChange{File: f, Action: "x"}); err == nil {
			t.Errorf("accepted file %q", f)
		}
	}
	if _, err := os.Stat(log); err == nil {
		t.Error("a refused change wrote a line")
	}
}

func TestConfigChangedIsNotADispatchEvent(t *testing.T) {
	log := auditLog(t)
	a := newAccountAt(Request{OperatorID: "alice"}, log, time.Now())
	_ = a.ConfigChanged(ConfigChange{File: "router-policy.yml", Action: "x"})
	if got := readAuditLines(t, log)[0]["type"]; got == "dispatch_started" || got == "dispatch_finished" {
		t.Errorf("type = %v; the ledger readers would count it as a run", got)
	}
}
