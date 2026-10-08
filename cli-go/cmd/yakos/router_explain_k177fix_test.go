package main

import (
	"os"
	"path/filepath"
	"testing"
)

// sec-370 HIGH: `router explain` must read the routing-shadow opt-in from the
// home state dir only. A policy planted behind YAKOS_DISPATCH_LOG (project
// settable) must read as off.
func TestJevShadowStateIgnoresDispatchLogEnv(t *testing.T) {
	home := t.TempDir()
	planted := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("YAKOS_DISPATCH_LOG", planted)
	if err := os.WriteFile(filepath.Join(planted, "decision-policy.yml"), []byte("routing_shadow: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := defaultExplainEnv()
	if got := jevShadowState(env, t.TempDir()); got != "off" {
		t.Errorf("a planted policy turned the shadow on: %q", got)
	}
}
