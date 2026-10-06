package budget

// window_keep_test.go: setting one limit must not silently change the window the
// agent's other limit counts in (K-136, security review of #330). The dollar and
// token limits share one window, so a token limit added with no window given used
// to turn a lifetime dollar cap into a monthly one that re-opens every month.

import "testing"

func windowOf(t *testing.T, dir, agent string) string {
	t.Helper()
	pol, err := LoadPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	return pol.Agents[agent].Window
}

func TestSetLimits_EmptyWindowKeepsTheCurrentWindow(t *testing.T) {
	dir := t.TempDir()
	if err := SetLimit(dir, "backend", 20, Lifetime); err != nil {
		t.Fatal(err)
	}

	// A token limit with no window leaves the dollar limit in its lifetime window.
	if err := SetTokenLimit(dir, "backend", 1_000_000, ""); err != nil {
		t.Fatal(err)
	}
	if w := windowOf(t, dir, "backend"); w != "lifetime" {
		t.Fatalf("adding a token limit turned a lifetime window into %q", w)
	}
	pol, _ := LoadPolicy(dir)
	if a := pol.Agents["backend"]; a.LimitUSD == nil || *a.LimitUSD != 20 || a.LimitTokens == nil || *a.LimitTokens != 1_000_000 {
		t.Fatalf("both limits stand: %+v", a)
	}

	// So does a dollar limit with no window.
	if err := SetLimit(dir, "backend", 30, ""); err != nil {
		t.Fatal(err)
	}
	if w := windowOf(t, dir, "backend"); w != "lifetime" {
		t.Fatalf("raising a dollar limit turned a lifetime window into %q", w)
	}

	// An explicit window still changes it, for either limit.
	if err := SetTokenLimit(dir, "backend", 1_000_000, Monthly); err != nil {
		t.Fatal(err)
	}
	if w := windowOf(t, dir, "backend"); w != "monthly" {
		t.Fatalf("an explicit window must apply, got %q", w)
	}
	if err := SetLimit(dir, "backend", 30, Lifetime); err != nil {
		t.Fatal(err)
	}
	if w := windowOf(t, dir, "backend"); w != "lifetime" {
		t.Fatalf("an explicit window must apply, got %q", w)
	}
}

// An agent with no entry counts monthly, and the window is stored explicitly (as
// every set has always done), so the file reads the same under an older build.
func TestSetLimits_EmptyWindowOnANewAgentIsMonthlyAndStored(t *testing.T) {
	dir := t.TempDir()
	if err := SetTokenLimit(dir, "fresh", 500, ""); err != nil {
		t.Fatal(err)
	}
	if w := windowOf(t, dir, "fresh"); w != "monthly" {
		t.Fatalf("a new agent's window = %q, want monthly stored explicitly", w)
	}
	if err := SetLimit(dir, "fresh2", 5, ""); err != nil {
		t.Fatal(err)
	}
	if w := windowOf(t, dir, "fresh2"); w != "monthly" {
		t.Fatalf("a new agent's window = %q, want monthly stored explicitly", w)
	}
}

// The window a new agent inherits from the policy's global default is the one it
// keeps; a built-in agent keeps its monthly window.
func TestSetLimits_EmptyWindowKeepsTheGlobalDefaultsWindow(t *testing.T) {
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{Default: AgentLimit{Window: "lifetime"}, Agents: map[string]AgentLimit{}}); err != nil {
		t.Fatal(err)
	}
	if err := SetTokenLimit(dir, "newcomer", 500, ""); err != nil {
		t.Fatal(err)
	}
	if w := windowOf(t, dir, "newcomer"); w != "lifetime" {
		t.Fatalf("the agent counted in the global default's lifetime window and must keep it, got %q", w)
	}
	if err := SetLimit(dir, "supervisor", 50, ""); err != nil {
		t.Fatal(err)
	}
	if lim := Resolve("supervisor", mustLoad(t, dir), nil); lim.USD != 50 {
		t.Fatalf("supervisor limit = %+v", lim)
	}
}

func mustLoad(t *testing.T, dir string) Policy {
	t.Helper()
	pol, err := LoadPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	return pol
}

// The empty window is "keep", not "anything goes": a window that is neither
// monthly nor lifetime is still refused, and nothing is written.
func TestSetLimits_UnknownWindowIsStillRefused(t *testing.T) {
	dir := t.TempDir()
	if err := SetLimit(dir, "backend", 1, Window("weekly")); err == nil {
		t.Error("SetLimit accepted a weekly window")
	}
	if err := SetTokenLimit(dir, "backend", 1, Window("weekly")); err == nil {
		t.Error("SetTokenLimit accepted a weekly window")
	}
	if pol := mustLoad(t, dir); len(pol.Agents) != 0 {
		t.Errorf("a refused set must not write: %+v", pol.Agents)
	}
}
