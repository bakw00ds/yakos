package mtlscmd_test

import (
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/netid"
)

// K-98: without --role the cert is unmapped, and the summary must say so.
func TestIssueClient_NoRole_ReportsNone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out, _, err := run(dir, "issue-client", "judy")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Role: read") {
		t.Errorf("summary still claims read default:\n%s", out)
	}
	if !strings.Contains(out, "Role: none (unmapped") || !strings.Contains(out, "set-role '*' read") {
		t.Errorf("summary must say none/unmapped and show the fix:\n%s", out)
	}
	if got := netid.NewRoleMapper(dir).Lookup("judy"); got != netid.RoleNone {
		t.Errorf("Lookup=%v; want none", got)
	}
}

// K-98: "none" is the explicit-deny value and beats the "*" wildcard.
func TestSetRole_None_ExplicitDenyBeatsWildcard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, args := range [][]string{{"set-role", "*", "read"}, {"set-role", "mallory", "none"}} {
		if _, _, err := run(dir, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	m := netid.NewRoleMapper(dir)
	if got := m.Lookup("mallory"); got != netid.RoleNone {
		t.Errorf("mallory=%v; want none (explicit deny beats *)", got)
	}
	if got := m.Lookup("other"); got != netid.RoleRead {
		t.Errorf("other=%v; want read via wildcard", got)
	}
}
