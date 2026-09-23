package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
)

// wantHooks is the exact set of hooks settings.template.json registers,
// per the S-6 structural plan §2.5 ("None [stay bash], on porting
// grounds"). Kept as an explicit sorted literal (not derived from the
// template at test time) so a change to either side is a visible, reviewed
// diff rather than a silent pass-through.
var wantHooks = []string{
	"auto-compact-trigger",
	"budget-guard",
	"context-inject",
	"context-threshold",
	"cycle-counter",
	"mailbox-mirror",
	"output-injection-scan",
	"path-allowlist",
	"path-log",
	"peer-claim",
	"peer-claim-confirm",
	"plan-outcome-capture",
	"plan-quality-gate",
	"retro-dispatch",
	"secret-scan",
	"session-end-check",
	"supervisor-ack-gate",
	"supervisor-gate",
	"supervisor-stream",
	"task-complete-dispatch",
	"task-dependency-gate",
	"team-lifecycle",
}

func TestNames_MatchesSettingsTemplate(t *testing.T) {
	got := registry.Names()
	if len(got) != len(wantHooks) {
		t.Fatalf("Names() has %d entries, want %d\ngot:  %v\nwant: %v", len(got), len(wantHooks), got, wantHooks)
	}
	for i := range got {
		if got[i] != wantHooks[i] {
			t.Errorf("Names()[%d]=%q, want %q", i, got[i], wantHooks[i])
		}
	}
}

func TestNames_Sorted(t *testing.T) {
	got := registry.Names()
	if !sort.StringsAreSorted(got) {
		t.Errorf("Names() not sorted: %v", got)
	}
}

func TestNames_StableAcrossCalls(t *testing.T) {
	a := registry.Names()
	b := registry.Names()
	if len(a) != len(b) {
		t.Fatalf("length differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("Names() not stable at index %d: %q vs %q", i, a[i], b[i])
		}
	}
}

func TestAll_SortedByName(t *testing.T) {
	entries := registry.All()
	for i := 1; i < len(entries); i++ {
		if entries[i-1].Name >= entries[i].Name {
			t.Errorf("entries not sorted: %q before %q", entries[i-1].Name, entries[i].Name)
		}
	}
}

func TestLookup_UnknownHook(t *testing.T) {
	_, _, ok := registry.Lookup("does-not-exist", registry.Config{})
	if ok {
		t.Error("expected ok=false for unknown hook")
	}
}

func TestLookup_EveryRegisteredNameConstructs(t *testing.T) {
	dir := t.TempDir()
	cfg := registry.Config{
		WorkCurrentDir: filepath.Join(dir, "work", "current"),
		ProjectDir:     dir,
		StateDir:       filepath.Join(dir, "state"),
	}
	for _, name := range registry.Names() {
		hook, entry, ok := registry.Lookup(name, cfg)
		if !ok {
			t.Errorf("Lookup(%q) ok=false", name)
			continue
		}
		if hook == nil {
			t.Errorf("Lookup(%q) returned nil Hook", name)
			continue
		}
		if hook.Name() != name {
			t.Errorf("Lookup(%q).Name()=%q", name, hook.Name())
		}
		if entry.Name != name {
			t.Errorf("Lookup(%q) entry.Name=%q", name, entry.Name)
		}
	}
}

// TestLookup_EveryHookRunsWithoutPanicking exercises Run once per
// registered hook with an empty-but-valid HookInput, over a real temp
// dir, to catch a constructor/dispatch wiring bug (nil pointer, wrong
// param order) that a name-only check wouldn't. It does not assert
// hook-specific behavior — that's each hook's own package's job.
func TestLookup_EveryHookRunsWithoutPanicking(t *testing.T) {
	dir := t.TempDir()
	workCurrent := filepath.Join(dir, "work", "current")
	if err := os.MkdirAll(workCurrent, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := registry.Config{
		WorkCurrentDir: workCurrent,
		ProjectDir:     dir,
		StateDir:       filepath.Join(dir, "state"),
	}
	in := hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    "Edit",
		Payload: map[string]any{},
		Env:     map[string]string{},
	}
	for _, name := range registry.Names() {
		hook, _, ok := registry.Lookup(name, cfg)
		if !ok {
			t.Fatalf("Lookup(%q) ok=false", name)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("hook %q panicked: %v", name, r)
				}
			}()
			if _, err := hook.Run(context.Background(), in); err != nil {
				t.Logf("hook %q returned error (not necessarily a bug): %v", name, err)
			}
		}()
	}
}

func TestGoReady_PathLogOnly(t *testing.T) {
	// path-log is the one hook converted to hookio/hooklog end-to-end and
	// brought to parity in S-6 A-1 (structural plan §2.4's "convert one
	// hook ... to prove the pattern"). Every other hook is still
	// bash-registered pending A-2. Update this test alongside any future
	// GoReady flip, so the "who is safe for refresh --hooks-impl=go" list
	// stays a reviewed, visible diff.
	entries := registry.All()
	var goReady []string
	for _, e := range entries {
		if e.GoReady {
			goReady = append(goReady, e.Name)
		}
	}
	if len(goReady) != 1 || goReady[0] != "path-log" {
		t.Errorf("GoReady hooks=%v, want [path-log]", goReady)
	}
}
