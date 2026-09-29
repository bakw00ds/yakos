package pathsafe

import "testing"

func TestValidateProjectSlug(t *testing.T) {
	bad := []string{".", "..", "../x", "a/b", `a\b`, "/abs", "x..y"}
	for _, s := range bad {
		if err := ValidateProjectSlug(s); err == nil {
			t.Errorf("ValidateProjectSlug(%q) = nil; want rejection", s)
		}
	}
	// "." names the base directory itself (S-2 R21): a slug is a child.
	good := []string{"", "yakos", "my-proj", "proj_1"}
	for _, s := range good {
		if err := ValidateProjectSlug(s); err != nil {
			t.Errorf("ValidateProjectSlug(%q) = %v; want nil", s, err)
		}
	}
}
