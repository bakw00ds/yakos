package budget

import "testing"

// MaxModel is the ceiling the dispatcher applies (K-139c): the agent's own, with a
// project's renamed supervisor taking the supervisor's, and never a looser one.
func TestMaxModel_RenamedSupervisorKeepsTheSupervisorCeiling(t *testing.T) {
	dir := t.TempDir()
	proj := projectWith(t, renamedSupervisor)
	o := Options{StateDir: dir, Project: proj}

	if got := MaxModel("supervisor", o); got != "sonnet" {
		t.Errorf("supervisor built-in = %q", got)
	}
	if got := MaxModel("watchdog", o); got != "sonnet" {
		t.Errorf("a renamed supervisor keeps the sonnet ceiling, got %q", got)
	}
	if got := MaxModel("watchdog", Options{StateDir: dir, Project: projectWith(t, "")}); got != "" {
		t.Errorf("an agent no project names a supervisor has no ceiling, got %q", got)
	}
	if got := MaxModel("watchdog", Options{StateDir: dir}); got != "" {
		t.Errorf("no project, no alias: %q", got)
	}

	// The operator lifts the supervisor's ceiling: the alias follows it. The alias's
	// own, lower, ceiling still wins (a project can only tighten).
	if err := SetMaxModel(dir, "supervisor", "fable"); err != nil {
		t.Fatal(err)
	}
	if got := MaxModel("watchdog", o); got != "fable" {
		t.Errorf("lifted supervisor ceiling, alias = %q", got)
	}
	if err := SetMaxModel(dir, "watchdog", "haiku"); err != nil {
		t.Fatal(err)
	}
	if got := MaxModel("watchdog", o); got != "haiku" {
		t.Errorf("the lower of the two ceilings, got %q", got)
	}
}

func TestClampModel_RenamedSupervisorIsClamped(t *testing.T) {
	o := Options{StateDir: t.TempDir(), Project: projectWith(t, renamedSupervisor)}
	if m, note := ClampModel("watchdog", "opus", o); m != "sonnet" || note == "" {
		t.Errorf("ClampModel(watchdog, opus) = (%q, %q)", m, note)
	}
	if m, note := ClampModel("watchdog", "haiku", o); m != "haiku" || note != "" {
		t.Errorf("below the ceiling: (%q, %q)", m, note)
	}
}
