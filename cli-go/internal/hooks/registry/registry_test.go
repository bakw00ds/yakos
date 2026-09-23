package registry_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/registry"
)

// repoRoot walks up from the test's working directory to the repo root,
// identified by the top-level VERSION file. Same pattern as
// cmd/yakos/main_test.go's repoRoot — duplicated here rather than shared
// because the two live in different modules' test packages and importing
// a test-only helper across packages isn't idiomatic Go.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		fi, err := os.Stat(filepath.Join(dir, "VERSION"))
		if err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == "" || parent == dir {
			t.Fatal("could not find repo root (no VERSION regular file in any parent dir)")
		}
		dir = parent
	}
}

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

func TestGoReady_S6A2aSet(t *testing.T) {
	// path-log is the reference conversion (S-6 A-1, structural plan
	// §2.4's "convert one hook ... to prove the pattern"). S-6 A-2a is
	// now converting the mechanical log-schema-only hooks to 100% parity
	// (tests/run-hook-parity.sh) one at a time, flipping GoReady as each
	// lands: cycle-counter. Every other hook is still bash-registered
	// pending the rest of A-2. Update this test alongside any future
	// GoReady flip, so the "who is safe for refresh --hooks-impl=go" list
	// stays a reviewed, visible diff.
	entries := registry.All()
	var goReady []string
	for _, e := range entries {
		if e.GoReady {
			goReady = append(goReady, e.Name)
		}
	}
	want := []string{"cycle-counter", "mailbox-mirror", "path-log"}
	if len(goReady) != len(want) {
		t.Fatalf("GoReady hooks=%v, want %v", goReady, want)
	}
	for i, name := range want {
		if goReady[i] != name {
			t.Errorf("GoReady hooks=%v, want %v", goReady, want)
			break
		}
	}
}

// failClosedLineRE matches an actual `HOOK_FAIL_CLOSED=1` assignment line
// (allowing leading whitespace, e.g. inside an `if`/function body), not a
// comment that merely mentions the variable (lib/hooks/*.sh has several —
// "see HOOK_FAIL_CLOSED in lib/hook-input.sh" etc.).
var failClosedLineRE = regexp.MustCompile(`(?m)^\s*HOOK_FAIL_CLOSED=1\s*$`)

// TestFailClosed_MatchesShScripts pins registry.go's FailClosed flags
// against the actual bash scripts' HOOK_FAIL_CLOSED=1 assignments, so the
// two can never silently drift apart again (S-6 A-1 round-2 fix — the
// registry originally had peer-claim and plan-quality-gate's flags
// swapped; nothing caught it because no test read FailClosed at all).
//
// Ground truth is lib/hooks/<name>.sh; a handful of hooks (currently only
// auto-compact-trigger) don't have a live copy there and are checked
// against lib/hooks/legacy/<name>.sh instead — same script content, kept
// under legacy/ for hooks not (yet) promoted to lib/hooks/ directly. If
// neither copy exists the test fails loudly rather than skipping, since a
// missing ground-truth file means this check isn't actually verifying
// anything for that hook.
func TestFailClosed_MatchesShScripts(t *testing.T) {
	root := repoRoot(t)
	for _, e := range registry.All() {
		primary := filepath.Join(root, "lib", "hooks", e.Name+".sh")
		legacy := filepath.Join(root, "lib", "hooks", "legacy", e.Name+".sh")

		path := primary
		src, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			path = legacy
			src, err = os.ReadFile(path)
		}
		if err != nil {
			t.Errorf("hook %q: no ground-truth script found at %s or %s: %v", e.Name, primary, legacy, err)
			continue
		}

		want := failClosedLineRE.Match(src)
		if e.FailClosed != want {
			t.Errorf("registry entry %q: FailClosed=%v, but %s %s HOOK_FAIL_CLOSED=1 (want FailClosed=%v)",
				e.Name, e.FailClosed, path, map[bool]string{true: "sets", false: "does not set"}[want], want)
		}
	}
}
