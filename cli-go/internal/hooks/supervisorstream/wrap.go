package supervisorstream

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// WrapperConfig is the detached run wrapper's configuration (K-117).
type WrapperConfig struct {
	State, Lock, Log, Pending string
	Findings                  string
	DeadlineS, Cap, Ceil      int
	IntervalS, BackoffMin     int
	DelayS                    int
}

// WrapperConfigFromEnv reads the _SSW_* variables the hook sets (the same ones
// the bash wrapper reads).
func WrapperConfigFromEnv(get func(string) string) WrapperConfig {
	n := func(k string) int {
		v, _ := parseDecimal(get(k))
		return v
	}
	c := WrapperConfig{
		State: get("_SSW_STATE"), Lock: get("_SSW_LOCK"), Log: get("_SSW_LOG"), Pending: get("_SSW_PENDING"), Findings: get("_SSW_FINDINGS"), Ceil: n("_SSW_CEIL"),
		DeadlineS: n("_SSW_DEADLINE"), Cap: n("_SSW_CAP"), IntervalS: n("_SSW_INTERVAL"),
		BackoffMin: n("_SSW_BACKOFF"), DelayS: n("_SSW_DELAY"),
	}
	if c.DeadlineS < 1 {
		c.DeadlineS = 240
	}
	return c
}

func (c WrapperConfig) log(severity, reason string, extra map[string]any) {
	rec := map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339), "hook": hookName,
		"severity": severity, "decision": "pass", "reason": reason,
	}
	for k, v := range extra {
		rec[k] = v
	}
	data, err := json.Marshal(rec)
	if err != nil || c.Log == "" {
		return
	}
	f, err := os.OpenFile(c.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
	_ = f.Close()
}

// headBuf keeps the first max bytes written to it.
type headBuf struct {
	max int
	b   []byte
}

func (h *headBuf) Write(p []byte) (int, error) {
	if room := h.max - len(h.b); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		h.b = append(h.b, p[:room]...)
	}
	return len(p), nil
}

// lockRetry takes the state lock with up to three 3 s budgets; the wrapper is
// detached, so waiting costs no latency. State is never written without it.
func lockRetry(lock string) (func(), bool) {
	for i := 0; i < 3; i++ {
		if rel, ok := acquireLock(lock); ok {
			return rel, true
		}
	}
	return nil, false
}

// runOnce runs argv under the wall-clock deadline and reports its exit code,
// whether the deadline fired and whether the output was an account session
// limit.
func runOnce(c WrapperConfig, argv []string, stdout, stderr io.Writer) (rc int, hit, slimit bool) {
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec
	head := &headBuf{max: 4096}
	cmd.Stdout = io.MultiWriter(stdout, head)
	cmd.Stderr = stderr
	cmd.WaitDelay = 10 * time.Second
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "supervisor-wrap: start %s: %v\n", argv[0], err)
		return 127, false, false
	}
	var fired atomic.Bool
	timer := time.AfterFunc(time.Duration(c.DeadlineS)*time.Second, func() {
		fired.Store(true)
		killTree(cmd.Process.Pid, 2*time.Second)
	})
	err := cmd.Wait()
	timer.Stop()
	rc = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			rc = ee.ExitCode()
			if rc < 0 {
				rc = 137
			}
		} else {
			rc = 1
		}
	}
	slimit = rc != 0 && strings.Contains(strings.ToLower(string(head.b)), "session limit")
	return rc, fired.Load(), slimit
}

// RunWrapper is the Go twin of the bash supervisor-wrap script: it runs
// argv ([cli dispatch agent task flags...]) under a wall-clock deadline, then
// drains coalesced triggers with at most one follow-up run per completion. It
// never returns a failing status for a failed run (exit 0 always).
func RunWrapper(c WrapperConfig, argv []string, stdout, stderr io.Writer) int {
	if len(argv) < 4 {
		return 0
	}
	if c.DelayS > 0 {
		time.Sleep(time.Duration(c.DelayS) * time.Second)
	}
	task := argv[3]
	for {
		runArgv := append([]string(nil), argv...)
		n := 0
		if rel, ok := lockRetry(c.Lock); ok {
			st := loadRunState(c.State)
			n = st.pending
			if n > 0 {
				_ = os.Rename(c.Pending, c.Pending+".run")
				st.pending, st.high = 0, 0
				_ = st.save(c.State)
			}
			rel()
		} else {
			c.log("WARN", "state lock busy; running without claiming coalesced events", nil)
		}
		if n > 0 {
			runArgv[3] = task + "\n\n" + fmt.Sprintf("Coalesced events: %d more escalated tool calls arrived while the previous supervisor run was in flight or throttled. Their redacted previews (one JSON object per line) are in %s.run. Read that file in addition to the buffer and judge them too.", n, c.Pending)
		}
		t0 := time.Now()
		rc, hit, slimit := runOnce(c, runArgv, stdout, stderr)
		dur := int(time.Since(t0).Seconds())
		switch {
		case hit:
			c.log("WARN", "supervisor run exceeded its wall-clock deadline and was killed",
				map[string]any{"rc": rc, "deadline_s": c.DeadlineS, "duration_s": dur})
		case rc != 0:
			sl := 0
			if slimit {
				sl = 1
			}
			c.log("WARN", "supervisor run exited non-zero", map[string]any{"rc": rc, "duration_s": dur, "session_limit": sl})
		default:
			c.log("REPORT", "supervisor run finished", map[string]any{"rc": 0, "duration_s": dur})
		}
		if peek := loadRunState(c.State); !slimit && peek.pending > 0 && peek.high == 0 {
			if rem := peek.last + int64(c.IntervalS) - time.Now().Unix(); rem > 0 {
				time.Sleep(time.Duration(rem) * time.Second)
			}
		}
		rel, ok := lockRetry(c.Lock)
		if !ok {
			c.log("WARN", "state lock busy; leaving the in-flight marker to expire", nil)
			return 0
		}
		st := loadRunState(c.State)
		now := time.Now().Unix()
		if slimit && st.backoff <= now {
			st.backoff = now + int64(c.BackoffMin)*60
			c.log("WARN", "account session limit reached; skipping supervisor launches for a while",
				map[string]any{"backoff_min": c.BackoffMin})
		}
		follow := ""
		if !slimit && st.pending > 0 && st.backoff <= now {
			switch {
			case st.high > 0:
				if c.Ceil == 0 || st.hlaunches < c.Ceil {
					follow = "high"
				} else if st.ceillog != 1 {
					st.ceillog = 1
					c.log("WARN", "high-risk supervisor launch ceiling reached for this session", map[string]any{"ceiling": c.Ceil})
					if c.Findings != "" {
						writeSynthFinding(c.Findings, c.Ceil, time.Now())
					}
					fmt.Fprintln(stderr, "supervisor-stream: high-risk launch ceiling reached for this session; skipping further supervisor runs")
				}
			case c.Cap == 0 || st.launches < c.Cap:
				follow = "routine"
			case st.caplog != 1:
				st.caplog = 1
				c.log("WARN", "supervisor launch cap reached for this session; skipping further launches", map[string]any{"cap": c.Cap})
				fmt.Fprintln(stderr, "supervisor-stream: launch cap reached for this session; skipping further routine supervisor runs")
			}
		}
		if follow != "" {
			c.log("REPORT", "coalesced triggers: launching one follow-up supervisor run",
				map[string]any{"coalesced": st.pending, "kind": follow})
			if follow == "high" {
				st.hlaunches++
			} else {
				st.launches++
			}
			st.last, st.start, st.hasStart = now, now, true
			_ = st.save(c.State)
			rel()
			continue
		}
		st.hasStart, st.start = false, 0
		_ = st.save(c.State)
		rel()
		return 0
	}
}
