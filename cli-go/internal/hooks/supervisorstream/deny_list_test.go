package supervisorstream_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A non-string element in lead.deny must not drop the rest of the list: bash's
// `(.lead.deny // []) | .[]` still yields ".env" and the write escalates.
func TestDenyListNonStringElementStillEscalates(t *testing.T) {
	for _, deny := range []string{`[5, ".env"]`, `[null, ".env"]`, `[{"x":1}, true, ".env"]`, `[[".x"], ".env"]`} {
		t.Run(deny, func(t *testing.T) {
			work, proj := ssProject(t, `{"lead":{"deny":`+deny+`}}`)
			out := ssRun(t, work, proj, `{"tool_input":{"file_path":".env","new_string":"x"}}`, map[string]string{})
			if _, err := os.Stat(filepath.Join(work, ".supervisor-counter")); err != nil {
				t.Fatalf("sensitive-path escalation did not fire with deny=%s: %v", deny, err)
			}
			if !strings.Contains(string(out.Stderr), "WARN") || !strings.Contains(string(out.Stderr), "lead.deny[") {
				t.Fatalf("expected a WARN about the non-string element, stderr=%q", out.Stderr)
			}
		})
	}
}

// Control: an all-string list produces no WARN, and a non-matching path does
// not escalate on account of a skipped element.
func TestDenyListAllStringsNoWarn(t *testing.T) {
	work, proj := ssProject(t, `{"lead":{"deny":[".env"]}}`)
	out := ssRun(t, work, proj, `{"tool_input":{"file_path":".env","new_string":"x"}}`, map[string]string{})
	if strings.Contains(string(out.Stderr), "WARN") {
		t.Fatalf("unexpected WARN: %q", out.Stderr)
	}
	work2, proj2 := ssProject(t, `{"lead":{"deny":[5]}}`)
	ssRun(t, work2, proj2, `{"tool_input":{"file_path":"main.go","new_string":"x"}}`, map[string]string{})
	if _, err := os.Stat(filepath.Join(work2, ".supervisor-counter")); err == nil {
		t.Fatal("a skipped non-string element must not cause an escalation")
	}
}
