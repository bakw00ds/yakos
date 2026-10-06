package modelreg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// ---- test doubles -------------------------------------------------------------
//
// Everything here is prefixed "disc" so it cannot collide with the registry's own
// test helpers in this package.

// discClock is a settable clock safe for concurrent use.
type discClock struct {
	mu sync.Mutex
	t  time.Time
}

func newDiscClock() *discClock {
	return &discClock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
}

func (c *discClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *discClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// discRunner records the commands it is asked to run and answers through fn.
type discRunner struct {
	mu    sync.Mutex
	calls []RunSpec
	fn    func(ctx context.Context, spec RunSpec) (RunResult, error)
}

func (r *discRunner) run(ctx context.Context, spec RunSpec) (RunResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, spec)
	r.mu.Unlock()
	return r.fn(ctx, spec)
}

func (r *discRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *discRunner) last() RunSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[len(r.calls)-1]
}

// discListing is the result of an `agy models` run that listed ids.
func discListing(ids ...string) RunResult {
	var sb strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&sb, "%s\tName of %s\n", id, id)
	}
	return RunResult{Stdout: []byte(sb.String()), Stderr: []byte("Fetching available models...\n")}
}

// discGate holds a runner until the test lets it go (or its context ends).
type discGate struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newDiscGate() *discGate {
	return &discGate{started: make(chan struct{}, 64), release: make(chan struct{})}
}

func (g *discGate) open() { g.once.Do(func() { close(g.release) }) }

func (g *discGate) fn(result RunResult) func(context.Context, RunSpec) (RunResult, error) {
	return func(ctx context.Context, _ RunSpec) (RunResult, error) {
		g.started <- struct{}{}
		select {
		case <-g.release:
			return result, nil
		case <-ctx.Done():
			return RunResult{}, ctx.Err()
		}
	}
}

// discRig is a Discoverer wired to doubles: a sign-in probe that answers from
// ready, a LookPath that returns an absolute path, a fake clock and a runner.
type discRig struct {
	t        *testing.T
	d        *Discoverer
	clock    *discClock
	runner   *discRunner
	stateDir string
	agyPath  string
	probes   atomic.Int32 // calls to the sign-in probe
	looks    atomic.Int32 // calls to LookPath
	ready    atomic.Bool
}

func newDiscRig(t *testing.T, mutate ...func(*DiscovererConfig)) *discRig {
	t.Helper()
	r := &discRig{t: t, clock: newDiscClock(), stateDir: t.TempDir(), agyPath: filepath.Join(t.TempDir(), "agy")}
	r.ready.Store(true)
	r.runner = &discRunner{fn: func(context.Context, RunSpec) (RunResult, error) {
		return discListing("model-a", "model-b", "model-c"), nil
	}}
	cfg := DiscovererConfig{
		StateDir: r.stateDir,
		Probe: func(context.Context, string) (bool, string) {
			r.probes.Add(1)
			if r.ready.Load() {
				return true, ""
			}
			return false, "not signed in; run: yakos auth login agy"
		},
		Env:      []string{"PATH=/usr/bin", "HOME=/nonexistent"},
		LookPath: func(string) (string, error) { r.looks.Add(1); return r.agyPath, nil },
		Run:      r.runner.run,
		Timeout:  5 * time.Second,
		Now:      r.clock.Now,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	r.d = NewDiscoverer(cfg)
	return r
}

// another returns a second Discoverer over the same state directory and clock, as
// a second process would be.
func (r *discRig) another() *Discoverer {
	return NewDiscoverer(DiscovererConfig{
		StateDir: r.stateDir,
		Probe:    func(context.Context, string) (bool, string) { return true, "" },
		LookPath: func(string) (string, error) { return r.agyPath, nil },
		Run:      r.runner.run,
		Timeout:  5 * time.Second,
		Now:      r.clock.Now,
	})
}

func (r *discRig) waiters() int {
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	if c := r.d.inflight[agyHarness]; c != nil {
		return c.waiters
	}
	return -1
}

func (r *discRig) idle() {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.d.WaitIdle(ctx); err != nil {
		r.t.Fatalf("the Discoverer did not go idle: %v", err)
	}
}

// discWait polls cond until it holds or the timeout passes.
func discWait(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func discIDs(s Snapshot) []string {
	out := make([]string, len(s.Models))
	for i, m := range s.Models {
		out[i] = m.ID
	}
	return out
}

// discAbs returns an absolute path that exists on no machine, valid on every OS.
func discAbs(t *testing.T) string { return filepath.Join(t.TempDir(), "agy") }

// ---- defaults ---------------------------------------------------------------

func TestDiscoveryNew_Defaults(t *testing.T) {
	d := NewDiscoverer(DiscovererConfig{})
	if d.cfg.Timeout != 15*time.Second || d.cfg.FreshFor != 6*time.Hour || d.cfg.FailBackoff != 60*time.Second {
		t.Errorf("defaults = %v, %v, %v", d.cfg.Timeout, d.cfg.FreshFor, d.cfg.FailBackoff)
	}
	if d.cfg.LookPath == nil || d.cfg.Run == nil || d.cfg.Now == nil {
		t.Error("LookPath, Run and Now must default")
	}
	// A nil Env would make os/exec hand the child the whole parent environment.
	if d.cfg.Env == nil {
		t.Error("Env must never be nil")
	}
	cfg := DiscovererConfig{Env: []string{"A=1"}}
	d = NewDiscoverer(cfg)
	cfg.Env[0] = "A=changed"
	if d.cfg.Env[0] != "A=1" {
		t.Error("the Discoverer must keep its own copy of Env")
	}
}

// A relative state directory would resolve against the working directory, which
// for a daemon can be a project: that is no state directory at all.
func TestDiscoveryNew_RelativeStateDirMeansNoDisk(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	if err := os.Mkdir("state", 0o700); err != nil {
		t.Fatal(err)
	}
	// A cache a project planted where the relative path would point.
	discWriteCache(t, "state", discCacheOf(discT0.Add(-time.Hour), "planted-1"), 0o600)
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = "state" })
	if s, ok := rig.d.Snapshot("agy"); ok {
		t.Errorf("a cache in a relative directory was read: %+v", s)
	}
	// And agy is not run in it either: with no usable state directory there is no
	// private place to run it (see TestDiscoveryProbe_WithoutAStateDirAgyIsNotRun).
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeSkipped {
		t.Fatalf("probe: %q %q %v, want skipped", rep.Status, rep.Reason, err)
	}
	if rig.runner.count() != 0 {
		t.Errorf("agy ran %d time(s) with a relative state directory", rig.runner.count())
	}
	entries, _ := os.ReadDir("state")
	if len(entries) != 1 || entries[0].Name() != DiscoveryFileName {
		t.Errorf("the relative directory was written to: %v", entries)
	}
	raw, _ := os.ReadFile(filepath.Join("state", DiscoveryFileName))
	if !strings.Contains(string(raw), "planted-1") {
		t.Errorf("the planted cache was overwritten: %s", raw)
	}
}

// ---- Probe: what runs, and when ------------------------------------------------

func TestDiscoveryProbe_OnlyAgyHasAListing(t *testing.T) {
	for _, h := range []string{"claude", "codex", "gemini", "", "AGY", "agy "} {
		t.Run("harness "+h, func(t *testing.T) {
			rig := newDiscRig(t)
			rep, err := rig.d.Probe(context.Background(), h)
			if err != nil {
				t.Fatalf("an unsupported harness is an answer, not an error: %v", err)
			}
			if rep.Status != ProbeUnsupported || rep.Reason == "" {
				t.Errorf("got %q %q, want unsupported with a reason", rep.Status, rep.Reason)
			}
			if rig.probes.Load() != 0 || rig.looks.Load() != 0 || rig.runner.count() != 0 {
				t.Errorf("nothing may run for %q: probes=%d looks=%d runs=%d", h, rig.probes.Load(), rig.looks.Load(), rig.runner.count())
			}
			if _, ok := rig.d.Snapshot(h); ok {
				t.Errorf("Snapshot(%q) = true", h)
			}
			rig.d.Kick(h)
			rig.idle()
			if rig.runner.count() != 0 {
				t.Errorf("Kick(%q) ran something", h)
			}
		})
	}
}

func TestDiscoveryProbe_NotSignedInSkipsWithoutRunning(t *testing.T) {
	rig := newDiscRig(t)
	rig.ready.Store(false)
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil {
		t.Fatalf("skipped is not an error: %v", err)
	}
	if rep.Status != ProbeSkipped || rep.Reason != "not signed in; run: yakos auth login agy" {
		t.Errorf("got %q %q", rep.Status, rep.Reason)
	}
	if rig.runner.count() != 0 || rig.looks.Load() != 0 {
		t.Errorf("agy ran although the sign-in probe said no: runs=%d looks=%d", rig.runner.count(), rig.looks.Load())
	}
}

func TestDiscoveryProbe_ProbeFuncOmittedFailsClosed(t *testing.T) {
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.Probe = nil })
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeSkipped || rep.Reason != "no sign-in probe configured" {
		t.Errorf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if rig.runner.count() != 0 {
		t.Error("agy ran with no sign-in probe")
	}
}

func TestDiscoveryProbe_NotReadyWithoutAReasonSaysNotSignedIn(t *testing.T) {
	rig := newDiscRig(t, func(c *DiscovererConfig) {
		c.Probe = func(context.Context, string) (bool, string) { return false, "" }
	})
	rep, _ := rig.d.Probe(context.Background(), "agy")
	if rep.Status != ProbeSkipped || rep.Reason != "not signed in" {
		t.Errorf("got %q %q", rep.Status, rep.Reason)
	}
}

func TestDiscoveryProbe_CLIMissingSkips(t *testing.T) {
	rig := newDiscRig(t, func(c *DiscovererConfig) {
		c.LookPath = func(string) (string, error) { return "", &exec.Error{Name: "agy", Err: exec.ErrNotFound} }
	})
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeSkipped || rep.Reason != "agy CLI not found on PATH" {
		t.Errorf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	if rig.runner.count() != 0 {
		t.Error("ran a command that was not found")
	}
}

// A relative result from LookPath means PATH has a relative entry (".", "bin"):
// the binary would be whatever the current directory, a project, holds.
func TestDiscoveryProbe_RefusesARelativePath(t *testing.T) {
	cases := []struct {
		name string
		path string
		err  error
	}{
		{"relative path, no error", "./agy", nil},
		{"bare name, no error", "agy", nil},
		{"relative dir, no error", filepath.Join("bin", "agy"), nil},
		// An absolute path with the ErrDot error: only the error check can refuse it.
		{"ErrDot with an absolute path", discAbs(t), &exec.Error{Name: "agy", Err: exec.ErrDot}},
		{"ErrDot with a relative path", "./agy", &exec.Error{Name: "agy", Err: exec.ErrDot}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newDiscRig(t, func(c *DiscovererConfig) {
				c.LookPath = func(string) (string, error) { return tc.path, tc.err }
			})
			rep, err := rig.d.Probe(context.Background(), "agy")
			if err != nil {
				t.Fatalf("refusing a PATH entry is a skip, not an error: %v", err)
			}
			if rep.Status != ProbeSkipped || !strings.Contains(rep.Reason, "relative PATH entry") {
				t.Errorf("got %q %q", rep.Status, rep.Reason)
			}
			if rig.runner.count() != 0 {
				t.Errorf("ran %q", tc.path)
			}
		})
	}
}

func TestDiscoveryProbe_RunsExactlyAgyModelsInAPrivateDirectory(t *testing.T) {
	var sawDir string
	rig := newDiscRig(t)
	rig.runner.fn = func(_ context.Context, spec RunSpec) (RunResult, error) {
		sawDir = spec.Dir
		fi, err := os.Stat(spec.Dir)
		if err != nil || !fi.IsDir() {
			t.Errorf("the working directory must exist during the run: %v", err)
		} else if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
			t.Errorf("working directory mode = %o, want 0700", fi.Mode().Perm())
		}
		return discListing("model-a"), nil
	}
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	spec := rig.runner.last()
	if spec.Path != rig.agyPath {
		t.Errorf("Path = %q, want what LookPath returned (%q)", spec.Path, rig.agyPath)
	}
	if !reflect.DeepEqual(spec.Args, []string{"models"}) {
		t.Errorf("Args = %q, want exactly [models]", spec.Args)
	}
	if want := []string{"PATH=/usr/bin", "HOME=/nonexistent"}; !reflect.DeepEqual(spec.Env, want) {
		t.Errorf("Env = %q, want %q (the configured environment and nothing else)", spec.Env, want)
	}
	if spec.MaxStdout != 256<<10 || spec.MaxStderr != 16<<10 {
		t.Errorf("limits = %d / %d, want 256 KiB / 16 KiB", spec.MaxStdout, spec.MaxStderr)
	}
	// Never the caller's directory: a project could hold config the CLI would load.
	cwd, _ := os.Getwd()
	if !filepath.IsAbs(sawDir) || sawDir == cwd || sawDir == "" || sawDir == "." {
		t.Errorf("working directory %q must be a fresh absolute directory, not the caller's (%q)", sawDir, cwd)
	}
	// Inside the secured state directory, not the temp directory: TMPDIR follows
	// the process environment, which a project can set, and whoever owns a
	// directory's parent can swap it after it is made.
	if filepath.Dir(sawDir) != filepath.Clean(rig.stateDir) || !strings.HasPrefix(filepath.Base(sawDir), ".discover-") {
		t.Errorf("working directory %q is not a .discover-* directory directly under the state directory %q", sawDir, rig.stateDir)
	}
	if _, err := os.Stat(sawDir); !os.IsNotExist(err) {
		t.Errorf("the private working directory must be removed after the run, stat err = %v", err)
	}
}

// agy runs in a private directory inside the secured state directory and nowhere
// else: the temp directory follows TMPDIR, which a project's environment can set,
// and agy run in a directory the project chose may load the project's workspace
// configuration. With no state directory the probe is skipped.
func TestDiscoveryProbe_WithoutAStateDirAgyIsNotRun(t *testing.T) {
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.StateDir = "" })
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err != nil || rep.Status != ProbeSkipped {
		t.Fatalf("got %q %q %v, want skipped", rep.Status, rep.Reason, err)
	}
	if rep.Reason != "agy was not run: there is no secured yakOS state directory to run it in" {
		t.Errorf("Reason = %q", rep.Reason)
	}
	if rig.runner.count() != 0 {
		t.Errorf("agy ran %d time(s) with nowhere private to run", rig.runner.count())
	}
	if _, ok := rig.d.Snapshot("agy"); ok {
		t.Error("a skipped probe left a snapshot")
	}
}

func TestDiscoveryProbe_EnvIsCopiedPerRun(t *testing.T) {
	rig := newDiscRig(t)
	rig.runner.fn = func(_ context.Context, spec RunSpec) (RunResult, error) {
		spec.Env[0] = "PATH=tampered"
		return discListing("model-a"), nil
	}
	for i := 0; i < 2; i++ {
		if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
			t.Fatal(err)
		}
		if got := rig.runner.last().Env[0]; got != "PATH=tampered" {
			t.Fatalf("setup: runner saw %q", got)
		}
	}
	if rig.d.cfg.Env[0] != "PATH=/usr/bin" {
		t.Errorf("a runner changed the Discoverer's own environment: %q", rig.d.cfg.Env)
	}
}

// ---- Probe: results ----------------------------------------------------------------

func TestDiscoveryProbe_UpdatedReportsAndDiffs(t *testing.T) {
	rig := newDiscRig(t)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
		return discListing("model-a", "model-b", "model-c"), nil
	}
	first, err := rig.d.Probe(context.Background(), "agy")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != ProbeUpdated || first.Previous != nil || len(first.Added) != 0 || len(first.Removed) != 0 {
		t.Errorf("first probe: %+v", first)
	}
	if first.Snapshot.Harness != "agy" || first.Snapshot.Source != "agy models" || !first.Snapshot.ProbedAt.Equal(rig.clock.Now()) {
		t.Errorf("first snapshot: %+v", first.Snapshot)
	}
	if got := discIDs(first.Snapshot); !reflect.DeepEqual(got, []string{"model-a", "model-b", "model-c"}) {
		t.Errorf("ids = %q", got)
	}
	if first.Snapshot.Models[0].Name != "Name of model-a" {
		t.Errorf("name = %q", first.Snapshot.Models[0].Name)
	}

	rig.clock.Advance(time.Minute)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
		res := discListing("model-b", "model-d", "model-c")
		res.Stdout = append(res.Stdout, []byte("Not A Valid Id\tX\n")...)
		return res, nil
	}
	second, err := rig.d.Probe(context.Background(), "agy")
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != ProbeUpdated {
		t.Fatalf("second probe: %+v", second)
	}
	if second.Previous == nil || !reflect.DeepEqual(discIDs(*second.Previous), []string{"model-a", "model-b", "model-c"}) {
		t.Errorf("Previous = %+v", second.Previous)
	}
	if !reflect.DeepEqual(second.Added, []string{"model-d"}) || !reflect.DeepEqual(second.Removed, []string{"model-a"}) {
		t.Errorf("Added = %q, Removed = %q", second.Added, second.Removed)
	}
	if second.Dropped != 1 {
		t.Errorf("Dropped = %d, want 1 (the line that was not an id)", second.Dropped)
	}
	got, ok := rig.d.Snapshot("agy")
	if !ok || !reflect.DeepEqual(discIDs(got), []string{"model-b", "model-d", "model-c"}) || !got.ProbedAt.Equal(rig.clock.Now()) {
		t.Errorf("Snapshot after the second probe = %+v, %v", got, ok)
	}
}

func TestDiscoveryProbe_AddedAndRemovedAreSorted(t *testing.T) {
	rig := newDiscRig(t)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("m-z", "m-y", "m-x"), nil }
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("n-c", "n-a", "n-b"), nil }
	rep, _ := rig.d.Probe(context.Background(), "agy")
	if !reflect.DeepEqual(rep.Added, []string{"n-a", "n-b", "n-c"}) || !reflect.DeepEqual(rep.Removed, []string{"m-x", "m-y", "m-z"}) {
		t.Errorf("Added = %q, Removed = %q", rep.Added, rep.Removed)
	}
}

func TestDiscoveryProbe_NonZeroExitFails(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	before, _ := rig.d.Snapshot("agy")
	// The stdout is a perfectly good listing: only the exit status says it failed.
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
		res := discListing("model-x", "model-y")
		res.ExitCode = 2
		res.Stderr = []byte("Fetching available models...\nError: session expired\n")
		return res, nil
	}
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err == nil || rep.Status != ProbeFailed {
		t.Fatalf("got %q, %v; want failed with an error", rep.Status, err)
	}
	// What agy printed on standard error is not repeated (see the sentinel tests).
	if rep.Reason != "agy models exited with status 2 (run `agy models` to see its message)" {
		t.Errorf("Reason = %q", rep.Reason)
	}
	after, _ := rig.d.Snapshot("agy")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a failed probe changed the snapshot: %+v -> %+v", before, after)
	}
	if !reflect.DeepEqual(discIDs(rep.Snapshot), []string{"model-a", "model-b", "model-c"}) {
		t.Errorf("the report must carry the snapshot still in effect, got %q", discIDs(rep.Snapshot))
	}
}

func TestDiscoveryProbe_ReasonNeverHoldsStandardOutput(t *testing.T) {
	rig := newDiscRig(t)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{ExitCode: 1, Stdout: []byte("SENTINEL-STDOUT-DO-NOT-LEAK\n")}, nil
	}
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(rep.Reason, "SENTINEL") || strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("standard output leaked into %q / %v", rep.Reason, err)
	}
	if rep.Reason != "agy models exited with status 1 (run `agy models` to see its message)" {
		t.Errorf("Reason = %q", rep.Reason)
	}
	// And a listing with no usable id, whatever it said.
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{Stdout: []byte("SENTINEL-STDOUT Not An Id\n")}, nil
	}
	rep, _ = rig.d.Probe(context.Background(), "agy")
	if strings.Contains(rep.Reason, "SENTINEL") {
		t.Errorf("standard output leaked into %q", rep.Reason)
	}
}

// Nothing the command printed reaches a reason, a warning or an error: vendor error
// text can hold a token, an API-key URL or a home path, and a probe's output is read
// by agents (so it reaches a model provider) and is meant to be served over an API.
// The sentinels are token-shaped, a key in a URL query, a home path and an
// escape-laden line; the report is also rendered as JSON, the form an API serves.
func TestDiscoveryProbe_NothingTheCommandPrintedReachesAnyOutput(t *testing.T) {
	const (
		token = "ya29.SENTINEL-TOKEN-9f8e7d6c5b4a"
		url   = "https://example.invalid/v1/models?key=AIzaSENTINELKEY0123456789abcdefghijk"
		home  = "/Users/someone/.gemini/antigravity-cli/oauth_creds.json"
	)
	leaked := func(t *testing.T, where, text string) {
		t.Helper()
		for _, s := range []string{token, "SENTINEL", "?key=", home, "/Users/", "oauth_creds", "antigravity-cli"} {
			if strings.Contains(text, s) {
				t.Errorf("%s holds %q: %q", where, s, text)
			}
		}
	}
	stderrText := "Fetching available models...\nError: token " + token + " rejected; see " + home + "\nGET " + url + " failed\n\x1b[31mred\x1b[0m\n"
	cases := []struct {
		name string
		res  RunResult
		err  error
	}{
		{"non-zero exit", RunResult{ExitCode: 3, Stderr: []byte(stderrText), Stdout: []byte(token + "\n" + home + "\n")}, nil},
		{"exit zero and no usable listing", RunResult{Stderr: []byte(stderrText), Stdout: []byte("error: " + token + " at " + home + "\n")}, nil},
		{"killed by a signal", RunResult{ExitCode: -1, Stderr: []byte(stderrText)}, nil},
		{"runner error naming a path", RunResult{}, &fs.PathError{Op: "fork/exec", Path: home, Err: fs.ErrPermission}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newDiscRig(t)
			rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return tc.res, tc.err }
			rep, err := rig.d.Probe(context.Background(), "agy")
			if rep.Status != ProbeFailed || err == nil {
				t.Fatalf("got %q, %v; want failed with an error", rep.Status, err)
			}
			leaked(t, "Reason", rep.Reason)
			leaked(t, "error text", err.Error())
			for _, w := range rep.Warnings {
				leaked(t, "a warning", w)
			}
			js, jerr := json.Marshal(rep)
			if jerr != nil {
				t.Fatal(jerr)
			}
			leaked(t, "the JSON a server would send", string(js))
			if strings.ContainsAny(rep.Reason, "\n\r\x1b") || utf8.RuneCountInString(rep.Reason) > maxReasonRunes {
				t.Errorf("Reason is not one short plain line: %q", rep.Reason)
			}
		})
	}
}

// A listing with no usable id must not become an empty snapshot: that would mark
// every model unavailable because of one bad run.
func TestDiscoveryProbe_NoUsableIDsKeepsTheSnapshotAndTheCache(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
	}{
		{"empty", ""},
		{"only comments and blanks", "# nothing\n\n"},
		{"only lines that are not ids", "Not An Id\tName\nAnother One\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newDiscRig(t)
			if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
				t.Fatal(err)
			}
			cacheBefore, err := os.ReadFile(filepath.Join(rig.stateDir, DiscoveryFileName))
			if err != nil {
				t.Fatal(err)
			}
			snapBefore, _ := rig.d.Snapshot("agy")
			rig.clock.Advance(time.Hour)
			rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
				return RunResult{Stdout: []byte(tc.stdout)}, nil
			}
			rep, err := rig.d.Probe(context.Background(), "agy")
			if err == nil || rep.Status != ProbeFailed || !strings.HasPrefix(rep.Reason, "no models in the listing") {
				t.Fatalf("got %q %q %v, want failed: no models in the listing", rep.Status, rep.Reason, err)
			}
			snapAfter, _ := rig.d.Snapshot("agy")
			if !reflect.DeepEqual(snapBefore, snapAfter) {
				t.Errorf("the snapshot changed: %+v -> %+v", snapBefore, snapAfter)
			}
			cacheAfter, _ := os.ReadFile(filepath.Join(rig.stateDir, DiscoveryFileName))
			if string(cacheBefore) != string(cacheAfter) {
				t.Errorf("the cache was rewritten by a failed probe:\n%s\n---\n%s", cacheBefore, cacheAfter)
			}
		})
	}
}

func TestDiscoveryProbe_UnusableLinesAreCountedInTheFailure(t *testing.T) {
	rig := newDiscRig(t)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{Stdout: []byte("Not An Id\nAlso Not\n")}, nil
	}
	rep, _ := rig.d.Probe(context.Background(), "agy")
	if rep.Reason != "no models in the listing (2 lines were not usable ids)" || rep.Dropped != 2 {
		t.Errorf("Reason = %q, Dropped = %d", rep.Reason, rep.Dropped)
	}
}

func TestDiscoveryProbe_RunnerErrorsFail(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantIs     error
		wantReason string
	}{
		{"output too large", ErrOutputTooLarge, ErrOutputTooLarge, "agy models printed more than 256 KiB"},
		{"deadline", context.DeadlineExceeded, context.DeadlineExceeded, "agy models did not finish within 5s"},
		{"cancelled", context.Canceled, context.Canceled, "agy models was cancelled"},
		// os/exec errors carry the program's absolute path; the reason names none.
		{"permission", &fs.PathError{Op: "fork/exec", Path: "/abs/secret-dir/agy", Err: fs.ErrPermission}, fs.ErrPermission, "agy could not be started: permission denied"},
		{"vanished", &fs.PathError{Op: "fork/exec", Path: "/abs/secret-dir/agy", Err: fs.ErrNotExist}, fs.ErrNotExist, "agy could not be started: it is no longer where PATH found it"},
		{"other", errors.New("fork/exec /abs/secret-dir/agy: exec format error"), nil, "agy could not be started"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newDiscRig(t)
			if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
				t.Fatal(err)
			}
			before, _ := rig.d.Snapshot("agy")
			rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return RunResult{}, tc.err }
			rep, err := rig.d.Probe(context.Background(), "agy")
			if rep.Status != ProbeFailed || err == nil {
				t.Fatalf("got %q, %v; want failed with an error", rep.Status, err)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("errors.Is(%v, %v) = false", err, tc.wantIs)
			}
			if rep.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", rep.Reason, tc.wantReason)
			}
			// The error prints as the reason: its cause (a path) is for errors.Is only.
			if strings.Contains(err.Error(), "/abs/") || strings.Contains(rep.Reason, "/abs/") {
				t.Errorf("a path reached the reason or the error: %q / %v", rep.Reason, err)
			}
			after, _ := rig.d.Snapshot("agy")
			if !reflect.DeepEqual(before, after) {
				t.Errorf("a failed probe changed the snapshot")
			}
		})
	}
}

func TestDiscoveryProbe_PanicInTheRunnerIsContained(t *testing.T) {
	rig := newDiscRig(t)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { panic("boom") }
	rep, err := rig.d.Probe(context.Background(), "agy")
	if err == nil || rep.Status != ProbeFailed || !strings.Contains(rep.Reason, "internal error") {
		t.Fatalf("got %q %q %v", rep.Status, rep.Reason, err)
	}
	rig.idle() // the failed probe released its slot
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("model-a"), nil }
	if rep, err := rig.d.Probe(context.Background(), "agy"); err != nil || rep.Status != ProbeUpdated {
		t.Errorf("the Discoverer is unusable after a panic: %q %v", rep.Status, err)
	}
}

// ---- Probe: bounds ----------------------------------------------------------

// probeWithin runs Probe and fails the test if it has not returned in limit.
func probeWithin(t *testing.T, d *Discoverer, ctx context.Context, limit time.Duration) (ProbeReport, error) {
	t.Helper()
	type result struct {
		rep ProbeReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		rep, err := d.Probe(ctx, "agy")
		done <- result{rep, err}
	}()
	select {
	case r := <-done:
		return r.rep, r.err
	case <-time.After(limit):
		t.Fatalf("Probe had not returned after %v", limit)
		return ProbeReport{}, nil
	}
}

func TestDiscoveryProbe_TimeoutBoundsTheRunner(t *testing.T) {
	gate := newDiscGate()
	t.Cleanup(gate.open)
	rig := newDiscRig(t, func(c *DiscovererConfig) { c.Timeout = 150 * time.Millisecond })
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil { // a snapshot to keep
		t.Fatal(err)
	}
	rig.runner.fn = gate.fn(discListing("model-new"))
	start := time.Now()
	rep, err := probeWithin(t, rig.d, context.Background(), 5*time.Second)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("Probe took %v with a 150ms Timeout", elapsed)
	}
	if rep.Status != ProbeFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %q, %v; want failed with a deadline error", rep.Status, err)
	}
	if rep.Reason != "agy models did not finish within 150ms" {
		t.Errorf("Reason = %q", rep.Reason)
	}
	if got, _ := rig.d.Snapshot("agy"); !reflect.DeepEqual(discIDs(got), []string{"model-a", "model-b", "model-c"}) {
		t.Errorf("a timed-out probe changed the snapshot: %q", discIDs(got))
	}
}

// The Timeout also bounds the sign-in check, and a check that ran out of time is
// a stopped probe, not an answer that the harness is signed out.
func TestDiscoveryProbe_TimeoutAlsoBoundsTheSignInCheck(t *testing.T) {
	rig := newDiscRig(t, func(c *DiscovererConfig) {
		c.Timeout = 100 * time.Millisecond
		c.Probe = func(ctx context.Context, _ string) (bool, string) {
			<-ctx.Done() // a keyring read that never answers
			return false, "not signed in"
		}
	})
	rep, err := probeWithin(t, rig.d, context.Background(), 5*time.Second)
	if rep.Status != ProbeFailed || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %q %q %v, want failed with a deadline error", rep.Status, rep.Reason, err)
	}
	if rig.runner.count() != 0 {
		t.Error("agy ran although the sign-in check never finished")
	}
}

func TestDiscoveryProbe_CallerCancelStopsTheRun(t *testing.T) {
	gate := newDiscGate()
	t.Cleanup(gate.open)
	rig := newDiscRig(t)
	sawCancel := make(chan struct{})
	rig.runner.fn = func(ctx context.Context, _ RunSpec) (RunResult, error) {
		gate.started <- struct{}{}
		select {
		case <-ctx.Done():
			close(sawCancel)
			return RunResult{}, ctx.Err()
		case <-gate.release:
			return discListing("model-a"), nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { _, err := rig.d.Probe(ctx, "agy"); errCh <- err }()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never started")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Probe returned %v, want a cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Probe did not return after its context was cancelled")
	}
	select {
	case <-sawCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("the run was not stopped when the only caller left (agy would keep running until the Timeout)")
	}
}

// Walking away is not the harness's failure: no backoff for the next Kick.
func TestDiscoveryProbe_CallerCancelIsNotAFailureForBackoff(t *testing.T) {
	rig := newDiscRig(t)
	var calls atomic.Int32
	rig.runner.fn = func(ctx context.Context, _ RunSpec) (RunResult, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return RunResult{}, ctx.Err()
		}
		return discListing("model-a"), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := rig.d.Probe(ctx, "agy"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the caller's deadline, got %v", err)
	}
	rig.idle()
	rig.d.Kick("agy")
	rig.idle()
	if got := calls.Load(); got != 2 {
		t.Errorf("runner calls = %d, want 2: Kick must not back off after a caller gave up", got)
	}
}

func TestDiscoveryProbe_AlreadyCancelledContextRunsNothing(t *testing.T) {
	rig := newDiscRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := rig.d.Probe(ctx, "agy")
	if !errors.Is(err, context.Canceled) || rep.Status != ProbeFailed {
		t.Errorf("got %q, %v", rep.Status, err)
	}
	rig.idle() // anything started has had its chance to touch the doubles
	if rig.runner.count() != 0 || rig.probes.Load() != 0 {
		t.Errorf("a cancelled caller started work: runs=%d probes=%d", rig.runner.count(), rig.probes.Load())
	}
}

func TestDiscoveryProbe_SingleFlight(t *testing.T) {
	gate := newDiscGate()
	t.Cleanup(gate.open)
	rig := newDiscRig(t)
	rig.runner.fn = gate.fn(discListing("model-a", "model-b"))
	const callers = 8
	reps := make([]ProbeReport, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reps[i], errs[i] = rig.d.Probe(context.Background(), "agy")
		}(i)
	}
	discWait(t, 5*time.Second, func() bool { return rig.waiters() == callers }, "every caller to join the one probe in flight")
	gate.open()
	wg.Wait()
	if got := rig.runner.count(); got != 1 {
		t.Errorf("the command ran %d times for %d concurrent probes, want 1", got, callers)
	}
	for i := range reps {
		if errs[i] != nil || reps[i].Status != ProbeUpdated || !reflect.DeepEqual(discIDs(reps[i].Snapshot), []string{"model-a", "model-b"}) {
			t.Errorf("caller %d: %q %v", i, reps[i].Status, errs[i])
		}
	}
	// And the next probe, once the first is over, is a new run.
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("model-c"), nil }
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	if got := rig.runner.count(); got != 2 {
		t.Errorf("runs = %d, want 2", got)
	}
}

func TestDiscoveryProbe_ALateCallerLeavingDoesNotStopTheSharedRun(t *testing.T) {
	gate := newDiscGate()
	t.Cleanup(gate.open)
	rig := newDiscRig(t)
	stopped := make(chan struct{})
	rig.runner.fn = func(ctx context.Context, _ RunSpec) (RunResult, error) {
		select {
		case <-ctx.Done():
			close(stopped)
			return RunResult{}, ctx.Err()
		case <-gate.release:
			return discListing("model-a"), nil
		}
	}
	leader := make(chan error, 1)
	go func() { _, err := rig.d.Probe(context.Background(), "agy"); leader <- err }()
	discWait(t, 5*time.Second, func() bool { return rig.waiters() == 1 }, "the leader to start the probe")

	joinerCtx, cancelJoiner := context.WithCancel(context.Background())
	joiner := make(chan error, 1)
	go func() { _, err := rig.d.Probe(joinerCtx, "agy"); joiner <- err }()
	discWait(t, 5*time.Second, func() bool { return rig.waiters() == 2 }, "the joiner to attach")

	cancelJoiner()
	select {
	case err := <-joiner:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("joiner got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the joiner did not return after cancelling")
	}
	select {
	case <-stopped:
		t.Fatal("the shared run was stopped although a caller was still waiting for it")
	case <-time.After(200 * time.Millisecond):
	}
	gate.open()
	select {
	case err := <-leader:
		if err != nil {
			t.Errorf("leader got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the leader never got its answer")
	}
}

// Callers that shared one probe each get their own report: one of them editing
// its slices must not change what another sees.
func TestDiscoveryProbe_JoinersGetIndependentReports(t *testing.T) {
	gate := newDiscGate()
	t.Cleanup(gate.open)
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil { // a previous snapshot, so Added/Removed/Previous exist
		t.Fatal(err)
	}
	rig.clock.Advance(time.Minute)
	rig.runner.fn = gate.fn(discListing("model-b", "model-z"))
	reps := make([]ProbeReport, 2)
	var wg sync.WaitGroup
	for i := range reps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reps[i], _ = rig.d.Probe(context.Background(), "agy")
		}(i)
	}
	discWait(t, 5*time.Second, func() bool { return rig.waiters() == 2 }, "both callers to join")
	gate.open()
	wg.Wait()
	for i := range reps {
		if reps[i].Status != ProbeUpdated || len(reps[i].Added) != 1 || len(reps[i].Removed) != 2 || reps[i].Previous == nil {
			t.Fatalf("caller %d: %+v", i, reps[i])
		}
	}
	reps[0].Added[0] = "mutated"
	reps[0].Removed[0] = "mutated"
	reps[0].Snapshot.Models[0].ID = "mutated"
	reps[0].Previous.Models[0].ID = "mutated"
	if reps[1].Added[0] != "model-z" || reps[1].Removed[0] != "model-a" ||
		reps[1].Snapshot.Models[0].ID != "model-b" || reps[1].Previous.Models[0].ID != "model-a" {
		t.Errorf("one caller's edits reached the other's report: %+v", reps[1])
	}
}

func TestDiscoveryProbe_ExplicitProbeIgnoresFreshnessAndBackoff(t *testing.T) {
	rig := newDiscRig(t)
	// Fresh snapshot: a second explicit probe still runs.
	for i := 0; i < 2; i++ {
		if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
			t.Fatal(err)
		}
	}
	if got := rig.runner.count(); got != 2 {
		t.Errorf("runs = %d, want 2 (a fresh snapshot does not excuse an explicit probe)", got)
	}
	// Inside a failure backoff: still runs.
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return RunResult{ExitCode: 1}, nil }
	if _, err := rig.d.Probe(context.Background(), "agy"); err == nil {
		t.Fatal("want a failure")
	}
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("model-a"), nil }
	if rep, err := rig.d.Probe(context.Background(), "agy"); err != nil || rep.Status != ProbeUpdated {
		t.Errorf("an explicit probe inside the backoff: %q %v", rep.Status, err)
	}
}

// ---- Snapshot -------------------------------------------------------------------

func TestDiscoverySnapshot_NeverRunsAnything(t *testing.T) {
	rig := newDiscRig(t)
	if _, ok := rig.d.Snapshot("agy"); ok {
		t.Error("a snapshot before any probe")
	}
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	runs, probes, looks := rig.runner.count(), rig.probes.Load(), rig.looks.Load()
	rig.clock.Advance(100 * time.Hour) // far past stale
	for i := 0; i < 3; i++ {
		if _, ok := rig.d.Snapshot("agy"); !ok {
			t.Fatal("a stale snapshot must still be returned")
		}
	}
	if rig.runner.count() != runs || rig.probes.Load() != probes || rig.looks.Load() != looks {
		t.Errorf("Snapshot ran something: runs %d->%d probes %d->%d looks %d->%d",
			runs, rig.runner.count(), probes, rig.probes.Load(), looks, rig.looks.Load())
	}
}

func TestDiscoverySnapshot_ReturnsACopy(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	a, _ := rig.d.Snapshot("agy")
	a.Models[0].ID = "mutated"
	b, _ := rig.d.Snapshot("agy")
	if b.Models[0].ID != "model-a" {
		t.Errorf("a caller changed the Discoverer's snapshot: %q", b.Models[0].ID)
	}
	rep, _ := rig.d.Probe(context.Background(), "agy")
	rep.Snapshot.Models[0].ID = "mutated-again"
	if c, _ := rig.d.Snapshot("agy"); c.Models[0].ID != "model-a" {
		t.Errorf("the report shares memory with the snapshot: %q", c.Models[0].ID)
	}
}

func TestDiscoverySnapshot_ReadsAnotherProcessesCache(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	other := rig.another()
	got, ok := other.Snapshot("agy")
	if !ok || !reflect.DeepEqual(discIDs(got), []string{"model-a", "model-b", "model-c"}) || got.Source != "agy models" {
		t.Errorf("a new process must see the cache: %+v %v", got, ok)
	}
}

func TestDiscoverySnapshot_StaleMemoryPrefersANewerCache(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	rig.clock.Advance(7 * time.Hour) // memory is now stale
	// Another process refreshed the cache meanwhile.
	other := rig.another()
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("fresh-1", "fresh-2"), nil }
	if _, err := other.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	got, _ := rig.d.Snapshot("agy")
	if !reflect.DeepEqual(discIDs(got), []string{"fresh-1", "fresh-2"}) {
		t.Errorf("a stale memory snapshot must yield to a newer cache, got %q", discIDs(got))
	}
}

func TestDiscoverySnapshot_FreshMemoryIsNotReplacedByDisk(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	rig.clock.Advance(time.Hour) // still fresh
	other := rig.another()
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("other-1"), nil }
	if _, err := other.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	got, _ := rig.d.Snapshot("agy")
	if !reflect.DeepEqual(discIDs(got), []string{"model-a", "model-b", "model-c"}) {
		t.Errorf("a fresh memory snapshot is the answer without a disk read, got %q", discIDs(got))
	}
}

func TestDiscoverySnapshot_StaleMemoryKeepsItselfWhenTheCacheIsOlder(t *testing.T) {
	rig := newDiscRig(t)
	other := rig.another()
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("old-1"), nil }
	if _, err := other.Probe(context.Background(), "agy"); err != nil { // cache at T0
		t.Fatal(err)
	}
	rig.clock.Advance(time.Hour)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("new-1", "new-2"), nil }
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil { // memory and cache at T0+1h
		t.Fatal(err)
	}
	// Put the older snapshot back on disk, then let memory go stale.
	oldCache := cacheFile{Schema: cacheSchema, Harnesses: map[string]cacheHarness{agyHarness: {
		Source: agySource, ProbedAt: rig.clock.Now().Add(-time.Hour), Models: []DiscoveredModel{{ID: "old-1"}},
	}}}
	discWriteCache(t, rig.stateDir, oldCache, 0o600)
	rig.clock.Advance(7 * time.Hour)
	got, _ := rig.d.Snapshot("agy")
	if !reflect.DeepEqual(discIDs(got), []string{"new-1", "new-2"}) {
		t.Errorf("the newer of memory and disk wins, got %q", discIDs(got))
	}
}

// ---- Kick ---------------------------------------------------------------------------

func TestDiscoveryKick_ReturnsAtOnceWithAHungRunner(t *testing.T) {
	gate := newDiscGate()
	t.Cleanup(gate.open)
	rig := newDiscRig(t)
	rig.runner.fn = gate.fn(discListing("model-a"))

	returned := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		rig.d.Kick("agy")
		returned <- time.Since(start)
	}()
	select {
	case el := <-returned:
		if el > time.Second { // a blocked Kick would wait out the rig Timeout (5s)
			t.Errorf("Kick took %v with a hung runner", el)
		}
	case <-time.After(time.Second):
		t.Fatal("Kick blocked on the runner")
	}
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Kick never started the probe")
	}
	// The probe is running: WaitIdle waits for it, or for its own context.
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := rig.d.WaitIdle(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("WaitIdle with a probe running = %v, want the context's deadline", err)
	}
	gate.open()
	rig.idle()
	if _, ok := rig.d.Snapshot("agy"); !ok {
		t.Error("the background probe left no snapshot")
	}
}

func TestDiscoveryKick_StartsWhenThereIsNoSnapshot(t *testing.T) {
	rig := newDiscRig(t)
	rig.d.Kick("agy")
	rig.idle()
	if rig.runner.count() != 1 {
		t.Errorf("runs = %d, want 1", rig.runner.count())
	}
	if _, ok := rig.d.Snapshot("agy"); !ok {
		t.Error("no snapshot after a Kick")
	}
}

func TestDiscoveryKick_DoesNotStartASecondProbe(t *testing.T) {
	gate := newDiscGate()
	t.Cleanup(gate.open)
	rig := newDiscRig(t)
	rig.runner.fn = gate.fn(discListing("model-a"))
	for i := 0; i < 5; i++ {
		rig.d.Kick("agy")
	}
	<-gate.started
	for i := 0; i < 5; i++ {
		rig.d.Kick("agy") // one is already in flight
	}
	time.Sleep(50 * time.Millisecond)
	gate.open()
	rig.idle()
	if got := rig.runner.count(); got != 1 {
		t.Errorf("runs = %d, want 1: Kick must not stack probes", got)
	}
}

func TestDiscoveryKick_FreshSnapshotIsLeftAlone(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	rig.clock.Advance(5 * time.Hour) // inside the 6h freshness window
	rig.d.Kick("agy")
	rig.idle()
	if got := rig.runner.count(); got != 1 {
		t.Errorf("runs = %d, want 1: a fresh snapshot needs no refresh", got)
	}
}

func TestDiscoveryKick_StaleSnapshotRefreshes(t *testing.T) {
	rig := newDiscRig(t)
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	rig.clock.Advance(6*time.Hour + time.Minute)
	rig.d.Kick("agy")
	rig.idle()
	if got := rig.runner.count(); got != 2 {
		t.Errorf("runs = %d, want 2: a stale snapshot is refreshed in the background", got)
	}
	got, _ := rig.d.Snapshot("agy")
	if !got.ProbedAt.Equal(rig.clock.Now()) {
		t.Errorf("ProbedAt = %v, want %v", got.ProbedAt, rig.clock.Now())
	}
}

func TestDiscoveryKick_BacksOffAfterAFailure(t *testing.T) {
	rig := newDiscRig(t)
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{ExitCode: 1, Stderr: []byte("boom")}, nil
	}
	rig.d.Kick("agy")
	rig.idle()
	if rig.runner.count() != 1 {
		t.Fatalf("runs = %d, want 1", rig.runner.count())
	}
	rig.d.Kick("agy") // same instant
	rig.idle()
	rig.clock.Advance(59 * time.Second)
	rig.d.Kick("agy") // still inside the 60s backoff
	rig.idle()
	if got := rig.runner.count(); got != 1 {
		t.Errorf("runs = %d, want 1: Kick must leave a failing harness alone for the backoff", got)
	}
	rig.clock.Advance(2 * time.Second)
	rig.d.Kick("agy") // 61s after the failure
	rig.idle()
	if got := rig.runner.count(); got != 2 {
		t.Errorf("runs = %d, want 2 once the backoff is over", got)
	}
}

// A harness that is not installed or not signed in is asked about once a minute,
// not on every dispatch: the sign-in probe can itself be slow (an OS keyring read).
func TestDiscoveryKick_BacksOffAfterASkip(t *testing.T) {
	rig := newDiscRig(t)
	rig.ready.Store(false)
	rig.d.Kick("agy")
	rig.idle()
	if rig.probes.Load() != 1 {
		t.Fatalf("sign-in probes = %d, want 1", rig.probes.Load())
	}
	for i := 0; i < 5; i++ {
		rig.d.Kick("agy")
		rig.idle()
	}
	if got := rig.probes.Load(); got != 1 {
		t.Errorf("sign-in probes = %d, want 1 inside the backoff", got)
	}
	rig.clock.Advance(61 * time.Second)
	rig.ready.Store(true)
	rig.d.Kick("agy")
	rig.idle()
	if rig.runner.count() != 1 {
		t.Errorf("runs = %d, want 1 after the backoff and a sign-in", rig.runner.count())
	}
}

func TestDiscoveryKick_SuccessClearsTheBackoff(t *testing.T) {
	// A backoff far longer than the freshness window, so that only a cleared
	// failure memory lets the next Kick through.
	rig := newDiscRig(t, func(c *DiscovererConfig) {
		c.FreshFor = time.Hour
		c.FailBackoff = 24 * time.Hour
	})
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return RunResult{ExitCode: 1}, nil }
	rig.d.Kick("agy")
	rig.idle()
	rig.d.Kick("agy") // inside the 24h backoff
	rig.idle()
	if got := rig.runner.count(); got != 1 {
		t.Fatalf("setup: runs = %d, want 1 (the backoff must hold)", got)
	}
	rig.runner.fn = func(context.Context, RunSpec) (RunResult, error) { return discListing("model-a"), nil }
	if _, err := rig.d.Probe(context.Background(), "agy"); err != nil { // an explicit probe succeeds
		t.Fatal(err)
	}
	rig.clock.Advance(2 * time.Hour) // the snapshot is stale again, the old backoff would still run
	rig.d.Kick("agy")
	rig.idle()
	if got := rig.runner.count(); got != 3 {
		t.Errorf("runs = %d, want 3: the success must have cleared the failure memory", got)
	}
}

func TestDiscoveryKick_CacheWrittenByAnotherProcessIsEnough(t *testing.T) {
	rig := newDiscRig(t)
	other := rig.another()
	if _, err := other.Probe(context.Background(), "agy"); err != nil {
		t.Fatal(err)
	}
	before := rig.runner.count()
	rig.d.Kick("agy")
	rig.idle()
	if rig.runner.count() != before {
		t.Errorf("Kick ran although a fresh cache exists")
	}
}

// ---- WaitIdle -----------------------------------------------------------------------

func TestDiscoveryWaitIdle(t *testing.T) {
	rig := newDiscRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rig.d.WaitIdle(ctx); err != nil {
		t.Errorf("an idle Discoverer: %v", err)
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := rig.d.WaitIdle(cancelled); err != nil {
		t.Errorf("an idle Discoverer returns before it looks at the context, got %v", err)
	}
}

// ---- race -----------------------------------------------------------------------

// Many goroutines mixing every entry point must not race (run with -race).
func TestDiscoveryConcurrentUse(t *testing.T) {
	rig := newDiscRig(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				switch (i + j) % 4 {
				case 0:
					rig.d.Kick("agy")
				case 1:
					_, _ = rig.d.Probe(context.Background(), "agy")
				case 2:
					rig.d.Snapshot("agy")
				case 3:
					rig.clock.Advance(time.Minute)
				}
			}
		}(i)
	}
	wg.Wait()
	rig.idle()
}
