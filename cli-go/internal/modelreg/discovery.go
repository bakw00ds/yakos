package modelreg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// Discovery asks a harness's own command which models the signed-in account can
// use, and keeps the answer as "available" flags next to the catalog. Today one
// harness has a listing wired in: `agy models`. claude takes four fixed tier
// names and codex's catalog (`codex debug models`) is per login and is not read
// here; both report "unsupported", and their entries stay "unknown".
//
// The contract the rest of the program leans on:
//
//   - Snapshot and Kick never wait for the command. A dispatch can call them.
//   - Probe is bounded: it returns by its caller's context and by Timeout, and
//     the process is killed when either ends.
//   - A listing that cannot be used (the command failed, timed out, printed
//     nothing parsable, or printed too much) changes nothing: the previous
//     snapshot, in memory and on disk, stays in effect. A glitch must not mark
//     every model unavailable.
//   - Output is data. Ids that fail ValidID are never kept, names are sanitized,
//     and no text from the command's standard output ever reaches a reason.

const (
	agyHarness = "agy"
	// agySource is what a snapshot says produced it.
	agySource = "agy models"

	// DefaultProbeTimeout, DefaultFreshFor and DefaultFailBackoff are what a
	// DiscovererConfig left at zero gets. The Registry marks a snapshot stale
	// after DefaultFreshFor, the window Kick uses.
	DefaultProbeTimeout = 15 * time.Second
	DefaultFreshFor     = 6 * time.Hour
	DefaultFailBackoff  = 60 * time.Second

	// Bounds on what is read from the command.
	maxListingStdout = 256 << 10
	maxListingStderr = 16 << 10
)

// probeGrace is how long a probe whose context has ended waits for its work to
// notice, before it reports failure anyway: the run's own wait delay plus a
// second for the process to be reaped and the output parsed. A variable so a test
// need not wait three seconds.
var probeGrace = waitDelay + time.Second

// ProbeStatus is how a probe ended.
type ProbeStatus string

// The probe statuses.
const (
	// ProbeUpdated: a listing was taken and replaced the snapshot.
	ProbeUpdated ProbeStatus = "updated"
	// ProbeSkipped: the harness cannot be listed right now (its CLI is not on
	// PATH, it is not signed in); nothing was run. Not an error.
	ProbeSkipped ProbeStatus = "skipped"
	// ProbeUnsupported: no listing is wired in for this harness. Not an error.
	ProbeUnsupported ProbeStatus = "unsupported"
	// ProbeFailed: the command ran (or was cut short) and produced nothing
	// usable. Probe also returns an error.
	ProbeFailed ProbeStatus = "failed"
)

// ProbeReport is what one probe did.
type ProbeReport struct {
	Harness string      `json:"harness"`
	Status  ProbeStatus `json:"status"`
	// Reason is one operator-facing line for skipped, unsupported and failed. It
	// holds no control characters and is at most maxReasonRunes long.
	Reason string `json:"reason,omitempty"`
	// Snapshot is the snapshot in effect after the probe: the new one when
	// updated, the previous one (if any) otherwise.
	Snapshot Snapshot `json:"snapshot"`
	// Previous is the snapshot an update replaced; nil when there was none.
	Previous *Snapshot `json:"previous,omitempty"`
	// Added and Removed are the ids newly listed and no longer listed compared with
	// Previous, sorted; empty when Previous is nil.
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	// Dropped counts listing lines that were not usable ids, so a changed output
	// format is visible instead of silently shortening the list.
	Dropped int `json:"dropped,omitempty"`
	// Warnings are things worth saying that did not stop the probe, such as a
	// cache that could not be written.
	Warnings []string `json:"warnings,omitempty"`
}

// DiscovererConfig configures a Discoverer. Everything but Probe has a default.
type DiscovererConfig struct {
	// StateDir holds the on-disk cache. The caller passes statepath.TrustedDir():
	// a project can set the environment variable statepath.Dir() honours, so that
	// one must never be used for a file that steers anything (K-129). Empty means
	// memory only, and so does a relative path.
	StateDir string
	// Probe is the signed-in check (the P0a auth probe, injected by cmd/yakos). A
	// nil Probe is read as "not ready": discovery fails closed.
	Probe ProbeFunc
	// Env is the child's whole environment, already filtered by the caller to the
	// variables the harness may see (runtime.FilterEnvFor). The child gets exactly
	// this and never the parent's environment.
	Env []string
	// LookPath finds the harness's executable; default exec.LookPath.
	LookPath func(file string) (string, error)
	// Run runs the command; default is the real exec runner.
	Run Runner
	// Timeout bounds one probe; default 15s.
	Timeout time.Duration
	// FreshFor is how long a snapshot is current; default 6h.
	FreshFor time.Duration
	// FailBackoff is how long Kick leaves a harness alone after a probe that did
	// not produce a snapshot; default 60s. An explicit Probe ignores it.
	FailBackoff time.Duration
	// Now is the clock; default time.Now.
	Now func() time.Time
}

// Discoverer holds the discovery results of one process (a daemon keeps one for
// its lifetime) and mirrors them to the state directory. It is safe for
// concurrent use.
type Discoverer struct {
	cfg DiscovererConfig

	mu       sync.Mutex
	snaps    map[string]Snapshot
	inflight map[string]*probeCall
	failedAt map[string]time.Time
	active   int           // probes running
	idle     chan struct{} // closed while active == 0
}

// probeCall is one probe in flight, shared by every caller that asks for the
// same harness while it runs.
type probeCall struct {
	done   chan struct{}
	cancel context.CancelFunc
	// waiters counts the callers still interested; the process is stopped when the
	// last one gives up. A background refresh (Kick) counts as one that never
	// leaves. Guarded by Discoverer.mu.
	waiters int
	// dead is set, under Discoverer.mu and together with the cancel, when the last
	// caller gave up. The call stays registered until its process has ended (a
	// descendant holding the pipes can take waitDelay), and a caller that arrives
	// meanwhile must not join it: it would be told "cancelled" though it never
	// cancelled anything. A dead call is treated as absent; the next probe starts
	// a fresh one beside it.
	dead   bool
	report ProbeReport // set before done is closed
	err    error
}

var _ SnapshotSource = (*Discoverer)(nil)

// NewDiscoverer returns a Discoverer using cfg with defaults filled in.
func NewDiscoverer(cfg DiscovererConfig) *Discoverer {
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	if cfg.Run == nil {
		cfg.Run = execRunner
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultProbeTimeout
	}
	if cfg.FreshFor <= 0 {
		cfg.FreshFor = DefaultFreshFor
	}
	if cfg.FailBackoff <= 0 {
		cfg.FailBackoff = DefaultFailBackoff
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.Env = append([]string{}, cfg.Env...) // never nil: nil would mean "inherit"
	if cfg.StateDir != "" && !filepath.IsAbs(cfg.StateDir) {
		// A relative directory would resolve against the working directory, which
		// for a daemon can be a project. No state directory is safer than that one.
		cfg.StateDir = ""
	}
	idle := make(chan struct{})
	close(idle)
	return &Discoverer{
		cfg:      cfg,
		snaps:    make(map[string]Snapshot),
		inflight: make(map[string]*probeCall),
		failedAt: make(map[string]time.Time),
		idle:     idle,
	}
}

// Snapshot returns the latest listing of harness: the one in memory while it is
// fresh, otherwise whichever of memory and the on-disk cache is newer (another
// process may have refreshed the file). Stale answers are still returned; the
// Registry marks them. It never runs a command and reads at most one small file.
// It reports false for a harness with no listing wired in and for one with no
// snapshot. The Models of the result are the caller's to keep.
func (d *Discoverer) Snapshot(harness string) (Snapshot, bool) {
	if harness != agyHarness {
		return Snapshot{}, false
	}
	d.mu.Lock()
	mem, haveMem := d.snaps[harness]
	d.mu.Unlock()
	if haveMem && d.cfg.Now().Sub(mem.ProbedAt) <= d.cfg.FreshFor {
		return cloneSnapshot(mem), true
	}
	if disk, ok := readCacheFile(d.cfg.StateDir, d.cfg.Now())[harness]; ok && (!haveMem || disk.ProbedAt.After(mem.ProbedAt)) {
		d.mu.Lock()
		if cur, have := d.snaps[harness]; !have || disk.ProbedAt.After(cur.ProbedAt) {
			d.snaps[harness] = disk
		}
		d.mu.Unlock()
		return cloneSnapshot(disk), true
	}
	if haveMem {
		return cloneSnapshot(mem), true
	}
	return Snapshot{}, false
}

// Probe lists harness's models now and records the result. Concurrent probes of
// one harness share a single run of the command. It returns by ctx and by
// Timeout, whichever is first, and the process is stopped when no caller is left
// waiting. An explicit Probe ignores FreshFor and FailBackoff.
//
// The error is non-nil exactly when the status is ProbeFailed; the error wraps
// its cause, so errors.Is(err, context.DeadlineExceeded) and
// errors.Is(err, ErrOutputTooLarge) work. Skipped and unsupported are answers,
// not errors.
func (d *Discoverer) Probe(ctx context.Context, harness string) (ProbeReport, error) {
	if harness != agyHarness {
		return unsupportedReport(harness), nil
	}
	if err := ctx.Err(); err != nil {
		return d.abandoned(harness, err)
	}
	d.mu.Lock()
	call := d.inflight[harness]
	if call == nil || call.dead {
		call = d.startLocked(harness)
	}
	call.waiters++
	d.mu.Unlock()

	select {
	case <-call.done:
		return cloneReport(call.report), call.err // each caller owns its copy
	case <-ctx.Done():
		d.mu.Lock()
		call.waiters--
		if call.waiters == 0 {
			// Nobody is waiting for it any more: stop the process. The decision and
			// the mark are made under the lock, so no other caller can join between
			// "the last one left" and "the call is cancelled".
			call.dead = true
			call.cancel()
		}
		d.mu.Unlock()
		return d.abandoned(harness, ctx.Err())
	}
}

// Kick refreshes harness in the background when its snapshot is missing or
// older than FreshFor, nothing is running for it, and the last attempt did not
// fail inside FailBackoff. It returns at once: the work runs in a goroutine bounded
// by Timeout. This is what a dispatch path calls; it never waits for agy.
func (d *Discoverer) Kick(harness string) {
	if harness != agyHarness {
		return
	}
	if snap, ok := d.Snapshot(harness); ok && d.cfg.Now().Sub(snap.ProbedAt) <= d.cfg.FreshFor {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.inflight[harness]; c != nil && !c.dead {
		return
	}
	if t, ok := d.failedAt[harness]; ok && d.cfg.Now().Sub(t) < d.cfg.FailBackoff {
		return
	}
	call := d.startLocked(harness)
	call.waiters++ // a background refresh is not abandoned by an impatient caller
}

// WaitIdle blocks until the probes running now have finished or ctx ends. It is
// for tests and for an orderly shutdown.
func (d *Discoverer) WaitIdle(ctx context.Context) error {
	d.mu.Lock()
	idle := d.idle
	d.mu.Unlock()
	select {
	case <-idle: // already idle: that is the answer, whatever ctx says
		return nil
	default:
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// startLocked starts a probe of harness. d.mu must be held.
func (d *Discoverer) startLocked(harness string) *probeCall {
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.Timeout)
	call := &probeCall{done: make(chan struct{}), cancel: cancel}
	d.inflight[harness] = call
	if d.active == 0 {
		d.idle = make(chan struct{})
	}
	d.active++
	go d.run(ctx, harness, call)
	return call
}

// run does one probe and publishes its result. It never panics out: a parser or
// a cache bug must not take a daemon down.
func (d *Discoverer) run(ctx context.Context, harness string, call *probeCall) {
	type outcome struct {
		rep ProbeReport
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		var o outcome
		defer func() {
			if r := recover(); r != nil {
				o = outcome{
					ProbeReport{Harness: harness, Status: ProbeFailed, Reason: "internal error while listing models"},
					fmt.Errorf("modelreg: %s discovery: internal error: %v", harness, r),
				}
			}
			done <- o
		}()
		o.rep, o.err = d.discover(ctx, harness)
	}()
	var (
		rep ProbeReport
		err error
	)
	select {
	case o := <-done:
		rep, err = o.rep, o.err
	case <-ctx.Done():
		// discover ends by itself soon after ctx does: the runner kills the process
		// and returns, and a sign-in check that honours ctx returns. Give it a
		// moment, but a sign-in function that ignores its context must not hold
		// the probe, and every caller waiting for it, for as long as it likes.
		select {
		case o := <-done:
			rep, err = o.rep, o.err
		case <-time.After(probeGrace):
			rep = ProbeReport{Harness: harness}
			if snap, ok := d.Snapshot(harness); ok {
				rep.Snapshot = snap
			}
			rep, err = probeFailed(rep, "the sign-in check or agy models did not stop when asked to", ctx.Err())
		}
	}
	stoppedByCaller := errors.Is(ctx.Err(), context.Canceled)
	call.cancel()

	d.mu.Lock()
	call.report, call.err = rep, err
	if d.inflight[harness] == call { // a newer call may have replaced a dead one
		delete(d.inflight, harness)
	}
	switch {
	case rep.Status == ProbeUpdated:
		delete(d.failedAt, harness)
	case stoppedByCaller:
		// The last caller walked away; that says nothing about the harness.
	default:
		d.failedAt[harness] = d.cfg.Now()
	}
	d.active--
	if d.active == 0 {
		close(d.idle)
	}
	d.mu.Unlock()
	close(call.done)
}

// discover is the probe itself: sign-in check, find the CLI, run `agy models`,
// parse, and on success replace the snapshot in memory and on disk.
func (d *Discoverer) discover(ctx context.Context, harness string) (ProbeReport, error) {
	rep := ProbeReport{Harness: harness}
	prev, havePrev := d.Snapshot(harness)
	if havePrev {
		rep.Snapshot = prev
	}

	if d.cfg.Probe == nil {
		return probeSkipped(rep, "no sign-in probe configured")
	}
	ready, why := d.cfg.Probe(ctx, harness)
	if err := ctx.Err(); err != nil {
		return probeFailed(rep, "stopped before agy could be checked", err)
	}
	if !ready {
		if why == "" {
			why = "not signed in"
		}
		return probeSkipped(rep, why)
	}

	path, err := d.cfg.LookPath(harness)
	if err != nil {
		if errors.Is(err, exec.ErrDot) {
			return probeSkipped(rep, "refusing a relative PATH entry for agy")
		}
		return probeSkipped(rep, "agy CLI not found on PATH")
	}
	if !filepath.IsAbs(path) {
		return probeSkipped(rep, "refusing a relative PATH entry for agy")
	}

	dir, err := d.workDir()
	if err != nil {
		return probeFailed(rep, "cannot create a private working directory for agy", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	res, err := d.cfg.Run(ctx, RunSpec{
		Path:      path,
		Args:      []string{"models"},
		Env:       append([]string{}, d.cfg.Env...), // a copy per run: a Runner may not change the next run's environment
		Dir:       dir,
		MaxStdout: maxListingStdout,
		MaxStderr: maxListingStderr,
	})
	switch {
	case errors.Is(err, ErrOutputTooLarge):
		return probeFailed(rep, fmt.Sprintf("agy models printed more than %d KiB", maxListingStdout>>10), err)
	case errors.Is(err, context.DeadlineExceeded):
		return probeFailed(rep, "agy models did not finish within "+d.cfg.Timeout.String(), err)
	case errors.Is(err, context.Canceled):
		return probeFailed(rep, "agy models was cancelled", err)
	case err != nil:
		return probeFailed(rep, "cannot run agy models: "+err.Error(), err)
	}
	if res.ExitCode != 0 {
		reason := fmt.Sprintf("agy models exited with status %d", res.ExitCode)
		if line := stderrReason(res.Stderr); line != "" {
			reason += ": " + line
		}
		return probeFailed(rep, reason, nil)
	}

	models, dropped := parseAgyModels(res.Stdout)
	rep.Dropped = dropped
	if len(models) == 0 {
		reason := "no models in the listing"
		if dropped > 0 {
			reason += fmt.Sprintf(" (%d lines were not usable ids)", dropped)
		}
		return probeFailed(rep, reason, nil)
	}

	snap := Snapshot{
		Harness:  harness,
		Source:   agySource,
		ProbedAt: d.cfg.Now().UTC().Truncate(time.Second),
		Models:   models,
	}
	d.mu.Lock()
	d.snaps[harness] = snap
	all := make(map[string]Snapshot, len(d.snaps))
	for h, s := range d.snaps {
		all[h] = s
	}
	d.mu.Unlock()

	rep.Status = ProbeUpdated
	rep.Snapshot = cloneSnapshot(snap)
	if havePrev {
		p := cloneSnapshot(prev)
		rep.Previous = &p
		rep.Added, rep.Removed = diffSnapshotIDs(prev, snap)
	}
	if d.cfg.StateDir != "" {
		if err := writeCacheFile(d.cfg.StateDir, all); err != nil {
			rep.Warnings = append(rep.Warnings, "cache not written: "+sanitizeText(err.Error(), maxReasonRunes))
		}
	}
	return rep, nil
}

// workDir makes the private directory agy runs in. It goes inside the secured
// state directory when there is one: the process's temp directory follows TMPDIR,
// which a project's environment can set, and whoever owns the parent of a
// directory can swap it after it is made. The state directory is owner-only
// (statepath.SecureDir refuses a symlink, another owner, and loosens the mode),
// so nobody else can. Without a state directory the temp directory is used.
func (d *Discoverer) workDir() (string, error) {
	if d.cfg.StateDir != "" && statepath.SecureDir(d.cfg.StateDir) == nil {
		if dir, err := os.MkdirTemp(d.cfg.StateDir, ".discover-*"); err == nil {
			return dir, nil
		}
	}
	return os.MkdirTemp("", "yakos-modelreg-*")
}

// cloneReport copies r's slices so callers that shared one probe cannot change
// each other's report.
func cloneReport(r ProbeReport) ProbeReport {
	r.Snapshot = cloneSnapshot(r.Snapshot)
	if r.Previous != nil {
		prev := cloneSnapshot(*r.Previous)
		r.Previous = &prev
	}
	r.Added = append([]string(nil), r.Added...)
	r.Removed = append([]string(nil), r.Removed...)
	r.Warnings = append([]string(nil), r.Warnings...)
	return r
}

// abandoned is the report for a caller that stopped waiting.
func (d *Discoverer) abandoned(harness string, ctxErr error) (ProbeReport, error) {
	rep := ProbeReport{Harness: harness}
	if snap, ok := d.Snapshot(harness); ok {
		rep.Snapshot = snap
	}
	return probeFailed(rep, "stopped waiting for the listing: "+ctxErr.Error(), ctxErr)
}

func unsupportedReport(harness string) ProbeReport {
	return ProbeReport{
		Harness: sanitizeText(harness, 32),
		Status:  ProbeUnsupported,
		Reason:  "no model listing is wired in for " + sanitizeText(harness, 32),
	}
}

func probeSkipped(rep ProbeReport, reason string) (ProbeReport, error) {
	rep.Status = ProbeSkipped
	rep.Reason = sanitizeText(reason, maxReasonRunes)
	return rep, nil
}

func probeFailed(rep ProbeReport, reason string, cause error) (ProbeReport, error) {
	rep.Status = ProbeFailed
	rep.Reason = sanitizeText(reason, maxReasonRunes)
	if cause == nil {
		cause = errors.New(rep.Reason)
	}
	return rep, fmt.Errorf("modelreg: %s discovery failed: %w", rep.Harness, cause)
}

// diffSnapshotIDs returns the ids in next and not in prev, and the ids in prev and not in
// next, each sorted.
func diffSnapshotIDs(prev, next Snapshot) (added, removed []string) {
	for _, m := range next.Models {
		if !prev.Has(m.ID) {
			added = append(added, m.ID)
		}
	}
	for _, m := range prev.Models {
		if !next.Has(m.ID) {
			removed = append(removed, m.ID)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}
