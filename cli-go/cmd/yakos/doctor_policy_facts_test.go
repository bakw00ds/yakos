package main

// doctor_policy_facts_test.go — K-137: runDoctor hands the --policy report the facts
// and the sign-in probe it cannot compute itself.

import (
	"context"
	"testing"

	"github.com/bakw00ds/yakos/internal/auth"
	"github.com/bakw00ds/yakos/internal/doctor"
)

func TestApplyPolicyFacts_WiresTheSignInProbeToAuthProbeRuntime(t *testing.T) {
	old := authProbeRuntime
	t.Cleanup(func() { authProbeRuntime = old })
	var askedID string
	authProbeRuntime = func(_ context.Context, id string) auth.ProbeResult {
		askedID = id
		// Asymmetric on purpose: present but not signed in, with a note, so a swapped or
		// dropped field cannot pass.
		return auth.ProbeResult{CLIPresent: true, Authed: false, Note: "the OS keyring did not answer"}
	}

	var cfg doctor.Config
	applyPolicyFacts(&cfg, t.TempDir(), t.TempDir())
	if cfg.PolicyProbeRuntime == nil {
		t.Fatal("the --policy report needs a sign-in probe")
	}
	got := cfg.PolicyProbeRuntime(context.Background(), "probe-me")
	want := doctor.RuntimeProbe{CLIPresent: true, Authed: false, Note: "the OS keyring did not answer"}
	if got != want {
		t.Errorf("probe result = %+v, want %+v", got, want)
	}
	if askedID != "probe-me" {
		t.Errorf("auth.ProbeRuntime was asked about %q, want the id the report passed", askedID)
	}
}
