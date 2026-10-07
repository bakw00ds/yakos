package workflow

// trigger_run.go — starting a run from a trigger (cron or webhook) with the
// one-active-run guard, and the trigger ledger (K-152).

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/dispatch"
)

// ErrRunActive is returned by StartTriggered when the workflow already has a
// run in flight. The trigger is skipped, not queued.
var ErrRunActive = errors.New("workflow: a run of this workflow is already active")

// Trigger sources recorded in the ledger.
const (
	TriggerCron    = "cron"
	TriggerWebhook = "webhook"
)

// triggerState counts in-flight runs per workflow name. The zero value is ready.
type triggerState struct {
	mu     sync.Mutex
	active map[string]int
}

func (s *triggerState) add(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		s.active = make(map[string]int)
	}
	s.active[name]++
}

func (s *triggerState) done(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[name] <= 1 {
		delete(s.active, name)
		return
	}
	s.active[name]--
}

// claim registers a run only if none is in flight.
func (s *triggerState) claim(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[name] > 0 {
		return false
	}
	if s.active == nil {
		s.active = make(map[string]int)
	}
	s.active[name] = 1
	return true
}

// triggerLedgerEntry is one line of <WorkDir>/workflows/triggers.ndjson.
type triggerLedgerEntry struct {
	TS       time.Time `json:"ts"`
	Workflow string    `json:"workflow"`
	Source   string    `json:"source"`
	Outcome  string    `json:"outcome"` // started | skipped
	Reason   string    `json:"reason,omitempty"`
	RunID    string    `json:"run_id,omitempty"`
}

func (e *Engine) appendTriggerLedger(ent triggerLedgerEntry) {
	dir := e.workflowsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("workflow: trigger ledger unavailable", "err", err)
		return
	}
	line, err := json.Marshal(ent)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "triggers.ndjson"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		slog.Warn("workflow: trigger ledger unavailable", "err", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// mintTriggerRunID returns "trg-<yyyymmdd-hhmmss>-<32 hex>", 52 path-safe
// characters with 128 random bits (same shape and strength as the console's
// run IDs). A randomness failure is an error; there is no weak fallback.
func mintTriggerRunID(now time.Time) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("workflow: mint run id: %w", err)
	}
	return fmt.Sprintf("trg-%s-%x", now.UTC().Format("20060102-150405"), b), nil
}

// StartTriggered starts wf as a new run on behalf of a trigger and returns its
// run ID without waiting for the run. source is TriggerCron or TriggerWebhook.
//
// At most one run of a workflow is active at a time: when one is already in
// flight (started by any path) the trigger is skipped, a ledger line records
// why, and ErrRunActive is returned. inputs overrides DECLARED input keys only
// (an undeclared key is dropped); the caller is responsible for having scanned
// the values. The run is parented to ctx and owned by ownerOpID.
func (e *Engine) StartTriggered(ctx context.Context, wf *Workflow, source, ownerOpID string, identity dispatch.IdentityCarrier, inputs map[string]string) (string, error) {
	if err := ValidateID("name", wf.Name); err != nil {
		return "", err
	}
	if !e.trig.claim(wf.Name) {
		e.appendTriggerLedger(triggerLedgerEntry{TS: time.Now().UTC(), Workflow: wf.Name, Source: source, Outcome: "skipped", Reason: "run already active"})
		slog.Info("workflow: trigger skipped, run already active", "workflow", wf.Name, "source", source)
		return "", ErrRunActive
	}
	runID, err := mintTriggerRunID(time.Now())
	if err != nil {
		e.trig.done(wf.Name)
		return "", err
	}
	cp := *wf
	if len(inputs) > 0 {
		cp.Inputs = make(map[string]string, len(wf.Inputs))
		for k, v := range wf.Inputs {
			cp.Inputs[k] = v
		}
		for k, v := range inputs {
			if _, declared := wf.Inputs[k]; declared {
				cp.Inputs[k] = v
			}
		}
	}
	e.appendTriggerLedger(triggerLedgerEntry{TS: time.Now().UTC(), Workflow: wf.Name, Source: source, Outcome: "started", RunID: runID})
	go func() {
		defer e.trig.done(wf.Name)
		if _, err := e.Run(ctx, &cp, runID, ownerOpID, identity); err != nil {
			slog.Error("workflow: triggered run failed", "run_id", runID, "workflow", wf.Name, "source", source, "err", err)
		}
	}()
	return runID, nil
}

// WrapTriggerPayload renders an external webhook payload as inert, delimited
// data for use as a workflow input: the same nonce-tagged untrusted boundary
// and standing notice that upstream node output gets, with closing-tag and
// placeholder look-alikes neutralized. The caller scans the raw payload first.
func WrapTriggerPayload(payload []byte) (string, error) {
	nonce, err := newOutputNonce()
	if err != nil {
		return "", fmt.Errorf("workflow: wrap payload: %w", err)
	}
	return untrustedOutputPreamble + string(wrapUntrustedNodeOutput("webhook", nonce, payload)), nil
}
