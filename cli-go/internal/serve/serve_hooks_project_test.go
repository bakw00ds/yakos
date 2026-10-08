package serve

import "testing"

// K-170 (e): the hooks endpoint's nonce is bound to the IDE root (the repo behind
// .project-path) when there is one, else to the workspace.
func TestHooksProjectDir(t *testing.T) {
	if got := hooksProjectDir("/repo", "/agent-control/x"); got != "/repo" {
		t.Errorf("ide root not preferred: %q", got)
	}
	if got := hooksProjectDir("", "/agent-control/x"); got != "/agent-control/x" {
		t.Errorf("workspace fallback: %q", got)
	}
}
