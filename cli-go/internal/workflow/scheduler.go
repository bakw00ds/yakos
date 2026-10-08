package workflow

// scheduler.go — the cron scheduler goroutine behind `triggers.cron` (K-152).
// It wakes at each minute boundary, re-reads the trusted enablement file (so
// revoking a trigger takes effect within a minute), and starts due workflows
// through Engine.StartTriggered.
//
// A trigger fires at most once per due time: if the daemon slept through
// several fire times it runs the workflow once on wake, not once per miss.

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// Clock is the scheduler's time source; tests substitute a fake.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the real clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time                         { return time.Now() }
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Scheduler starts cron-triggered runs.
type Scheduler struct {
	Clock  Clock
	Engine *Engine
	// Load returns the enablement file's content; it is called every tick. Any
	// error means "nothing is enabled".
	Load func() (Schedules, error)
	// OwnerOpID owns the runs the scheduler starts.
	OwnerOpID string

	mu      sync.Mutex
	state   map[string]*schedState
	lastErr string
	// refused remembers the workflow hash a refusal was already reported for,
	// so a changed file is logged once, not every minute.
	refused map[string]string
}

type schedState struct {
	expr string
	tz   string
	cron *Cron
	next time.Time
}

// Run blocks, ticking at each minute boundary, until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	clk := s.Clock
	if clk == nil {
		clk = SystemClock{}
	}
	for {
		now := clk.Now()
		wait := now.Truncate(time.Minute).Add(time.Minute).Sub(now)
		select {
		case <-ctx.Done():
			return
		case <-clk.After(wait):
			s.Tick(ctx, clk.Now())
		}
	}
}

// Tick evaluates every enabled cron trigger at now and starts the due ones.
func (s *Scheduler) Tick(ctx context.Context, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sched, err := s.Load()
	if err != nil {
		s.state = nil // untrusted or malformed: everything is off
		if msg := err.Error(); msg != s.lastErr {
			s.lastErr = msg
			slog.Warn("workflow: schedules ignored, no trigger will fire", "reason", msg)
		}
		return
	}
	s.lastErr = ""
	loc, err := sched.Location()
	if err != nil {
		s.state = nil
		return
	}

	names := make([]string, 0, len(sched.Workflows))
	for name, ent := range sched.Workflows {
		if ent.Cron {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	next := make(map[string]*schedState, len(names))
	for _, name := range names {
		wf, sha, err := LoadFile(filepath.Join(s.Engine.workflowsDir(), name+".yaml"))
		if err != nil || Validate(wf) != nil || wf.Triggers == nil || wf.Triggers.Cron == "" {
			continue
		}
		if perr := CheckPin(sched.Workflows[name], sha); perr != nil {
			// Enablement pins content: the file changed since the operator
			// enabled it. Refuse (and say so once per distinct content).
			if s.refused == nil {
				s.refused = make(map[string]string)
			}
			if s.refused[name] != sha {
				s.refused[name] = sha
				slog.Warn("workflow: cron trigger refused", "workflow", name, "reason", perr.Error())
				s.Engine.appendTriggerLedger(triggerLedgerEntry{TS: time.Now().UTC(), Workflow: name, Source: TriggerCron, Outcome: "refused", Reason: perr.Error()})
			}
			continue
		}
		delete(s.refused, name)
		c, err := ParseCron(wf.Triggers.Cron)
		if err != nil {
			continue
		}
		st := s.state[name]
		if st == nil || st.expr != wf.Triggers.Cron || st.tz != sched.Timezone {
			st = &schedState{expr: wf.Triggers.Cron, tz: sched.Timezone, cron: c, next: c.Next(now, loc)}
			next[name] = st
			continue
		}
		next[name] = st
		if st.next.IsZero() || now.Before(st.next) {
			continue
		}
		st.next = st.cron.Next(now, loc)
		if _, err := s.Engine.StartTriggered(ctx, wf, TriggerCron, s.OwnerOpID, dispatch.IdentityCarrier{}, nil); err != nil && !errors.Is(err, ErrRunActive) {
			slog.Error("workflow: cron trigger failed to start", "workflow", name, "err", err)
		}
	}
	s.state = next // workflows no longer enabled drop their state
}
