package dispatch

import (
	"reflect"
	"sort"
	"testing"
)

// The audit allow-list is exactly four fixed labels (sec-362 F3): widening it
// would let a caller put a path or an arbitrary name in the audit trail.
func TestConfigChanged_AllowListIsExactlyFourLabels(t *testing.T) {
	var got []string
	for k := range auditFiles {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"budget-policy.yml", "model-registry.yml", "router-policy.yml", "schedules.yml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("auditFiles keys = %v, want exactly %v", got, want)
	}
	a := &Account{}
	for _, f := range want {
		if _, err := a.configChangedLine(ConfigChange{File: f, Action: "x", Surface: "cli"}); err != nil {
			t.Errorf("%q refused: %v", f, err)
		}
	}
	for _, f := range []string{"evil.yml", "", "/abs/schedules.yml", "../schedules.yml", "schedules.yaml", "proj-abc123.yaml", "/etc/passwd"} {
		if _, err := a.configChangedLine(ConfigChange{File: f, Action: "x", Surface: "cli"}); err == nil {
			t.Errorf("%q accepted, want \"not an auditable policy file\"", f)
		}
	}
}
