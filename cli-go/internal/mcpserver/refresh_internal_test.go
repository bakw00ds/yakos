package mcpserver

// refresh_internal_test.go — internal (white-box) tests for the M2 fix:
// yakos.refresh's dryRun flag must default to true (safe/read-only), not
// false, when the caller omits it. See security-review-2026-09-14.md M2.
//
// This uses the unexported resolveDryRun/refreshArgs directly rather than
// exercising the full handleRefresh -> refresh.Run path, because Run calls
// refresh.CollectProjects(os.Getenv("HOME")) directly (not test-injectable),
// so a full end-to-end test would scan the real developer/CI machine's home
// directory. The dryRun-default decision is a pure function; testing it in
// isolation avoids that unrelated flakiness while still proving the fix.

import (
	"encoding/json"
	"testing"
)

// TestResolveDryRun_OmittedDefaultsTrue is the core M2 regression: omitting
// dryRun entirely must resolve to a SAFE (dry-run) call, not a destructive
// one. Before the fix, refreshArgs.DryRun was a plain bool, whose Go zero
// value (false) was indistinguishable from an explicit dryRun:false — so
// "just call yakos.refresh" silently meant "rewrite hooks/settings/symlinks
// across every project under $HOME/agent-control".
func TestResolveDryRun_OmittedDefaultsTrue(t *testing.T) {
	got := resolveDryRun(refreshArgs{})
	if !got {
		t.Fatal("resolveDryRun(refreshArgs{}) = false; want true (omitted dryRun must default to safe/dry-run)")
	}
}

// TestResolveDryRun_ExplicitFalseApplies verifies the opt-in write path
// still works: an explicit dryRun:false must resolve to false so an
// operator can still actually apply changes.
func TestResolveDryRun_ExplicitFalseApplies(t *testing.T) {
	f := false
	got := resolveDryRun(refreshArgs{DryRun: &f})
	if got {
		t.Fatal("resolveDryRun({DryRun: false}) = true; want false (explicit dryRun:false must apply changes)")
	}
}

// TestResolveDryRun_ExplicitTrueStaysDryRun verifies an explicit dryRun:true
// still resolves to true (no change in behavior for the explicit case).
func TestResolveDryRun_ExplicitTrueStaysDryRun(t *testing.T) {
	tr := true
	got := resolveDryRun(refreshArgs{DryRun: &tr})
	if !got {
		t.Fatal("resolveDryRun({DryRun: true}) = false; want true")
	}
}

// TestRefreshArgs_JSONOmittedFieldIsNilPointer proves the *bool field
// actually distinguishes "field absent from the JSON" from "field present
// and false" — the property the whole M2 fix depends on. A plain `bool`
// field cannot make this distinction; encoding/json always decodes an
// absent boolean field to Go's zero value (false).
func TestRefreshArgs_JSONOmittedFieldIsNilPointer(t *testing.T) {
	var p refreshArgs
	if err := json.Unmarshal([]byte(`{}`), &p); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p.DryRun != nil {
		t.Fatalf("DryRun = %v; want nil for an omitted field", *p.DryRun)
	}

	var p2 refreshArgs
	if err := json.Unmarshal([]byte(`{"dryRun":false}`), &p2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if p2.DryRun == nil || *p2.DryRun != false {
		t.Fatalf("DryRun for explicit {\"dryRun\":false} = %v; want non-nil pointer to false", p2.DryRun)
	}
}
