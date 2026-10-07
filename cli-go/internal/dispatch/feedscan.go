package dispatch

// feedscan.go is the detect-and-report scan over a runtime's normalized event
// stream (K-146). Every tool_result and assistant text event a non-claude
// runtime (codex, agy) produces passes through observe, which runs the output
// injection scanner and the supervisor pre-filter's risk patterns over a bounded
// slice of it and reports hits to the supervisor findings/pending files.
//
// Detect-only is the default: the run is untouched. With kill_on_critical: true
// in the trusted user policy a CRITICAL hit cancels the dispatch through the
// context that owns the process group (runtime.ConfigureGroupKill).
//
// claude is deliberately not scanned here: its tool output is already scanned
// inside the session by the output-injection-scan PostToolUse hook, and its
// stream path is pinned by golden tests.
//
// Bounds: one event is scanned as at most feedScanEventBytes (head + tail), the
// whole run as at most feedScanTotalBytes, each scan has feedScanDeadline, and a
// scan that overruns it switches the feed off for the rest of the run so the
// stream is never stalled twice. Nothing is buffered: an event is scanned and
// dropped. No finding echoes content, a path or a tool name.

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/outputinjectionscan"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
	"github.com/bakw00ds/yakos/internal/runtime"
)

const (
	feedScanEventBytes = 32 * 1024
	feedScanTotalBytes = 4 * 1024 * 1024
	feedScanDeadline   = 500 * time.Millisecond
	feedScanMaxReports = 16
)

// criticalLabels are the injection-family labels of outputinjectionscan.Scan.
// Key and private-key shapes, base64, zero-width text and risk-regex hits are
// reported as warn: they are noisy in legitimate output and never kill a run.
var criticalLabels = map[string]bool{
	"ignore-previous-instructions": true,
	"ignore-everything-above":      true,
	"disregard-system-prompt":      true,
	"system-prompt-impersonation":  true,
	"model-format-token-injection": true,
}

type feedScanner struct {
	runtime     string
	session     string
	workCurrent string
	kill        bool
	cancel      context.CancelFunc

	scan     func(string) []string // test seam: the label producer
	deadline time.Duration

	scanned   int
	reports   int
	findings  int
	seen      map[string]struct{}
	off       bool
	cancelled string // ledger cancel reason, "" while the run is untouched
}

// newFeedScanner returns the scanner for a dispatch, or nil for claude.
func newFeedScanner(runtimeName, session string, cancel context.CancelFunc) *feedScanner {
	if runtimeName == "claude" || runtimeName == "" {
		return nil
	}
	f := &feedScanner{
		runtime: runtimeName, session: session, cancel: cancel,
		workCurrent: feedWorkCurrent(),
		kill:        supervisorstream.KillOnCritical(),
		scan:        feedScanFn, deadline: feedScanDeadline,
		seen: map[string]struct{}{},
	}
	if newFeedScannerHook != nil {
		newFeedScannerHook(f)
	}
	return f
}

// newFeedScannerHook lets a test adjust a freshly built scanner.
var newFeedScannerHook func(*feedScanner)

// feedWorkCurrent resolves the project's work/current directory the way the
// Go path helpers do (YAKOS_WORK_DIR, in-place work, then the agent-control
// project); "" when none resolves, in which case findings are counted only.
func feedWorkCurrent() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current")
	}
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		if pd := os.Getenv("CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work", "current")
		}
	}
	name, home := os.Getenv("YAKOS_PROJECT_NAME"), os.Getenv("HOME")
	if name == "" || home == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return ""
	}
	return filepath.Join(home, "agent-control", name, "work", "current")
}

// labelsFor is the production scan: the injection scanner, then the risk
// patterns, over text already bounded by the caller.
func labelsFor(text string) []string {
	labels := outputinjectionscan.Scan(text)
	if r := supervisorstream.RiskLabel(text); r != "" {
		labels = append(labels, r)
	}
	return labels
}

// feedScanFn is the label producer new scanners use; tests replace it.
var feedScanFn = labelsFor

// boundEvent keeps the head and tail of an oversize event.
func boundEvent(s string) string {
	if len(s) <= feedScanEventBytes {
		return s
	}
	h := feedScanEventBytes / 2
	return s[:h] + "\n" + s[len(s)-h:]
}

// observe scans one normalized event. It never blocks longer than the scan
// deadline and never fails the stream.
func (f *feedScanner) observe(ev runtime.NativeEvent) {
	if f == nil || f.off || f.cancelled != "" {
		return
	}
	var kind, text string
	switch ev.Kind {
	case runtime.EventToolResult:
		kind, text = "tool_result", ev.ToolOutput
	case runtime.EventToken:
		if ev.Plain {
			return
		}
		kind, text = "text", ev.Text
	default:
		return
	}
	if text == "" {
		return
	}
	text = boundEvent(text)
	if f.scanned+len(text) > feedScanTotalBytes {
		f.off = true
		slog.Warn("dispatch: event scan: byte budget reached; feed off for the rest of the run", "runtime", f.runtime)
		return
	}
	f.scanned += len(text)

	done := make(chan []string, 1)
	scan := f.scan
	go func() { done <- scan(text) }()
	timer := time.NewTimer(f.deadline)
	defer timer.Stop()
	var labels []string
	select {
	case labels = <-done:
	case <-timer.C:
		f.off = true
		slog.Warn("dispatch: event scan: scan overran its deadline; feed off for the rest of the run", "runtime", f.runtime)
		return
	}
	if len(labels) == 0 {
		return
	}
	f.report(kind, labels)
}

func (f *feedScanner) report(kind string, labels []string) {
	sev, firstCritical := "warn", ""
	for _, l := range labels {
		if criticalLabels[l] {
			sev, firstCritical = "critical", l
			break
		}
	}
	key := kind + "|" + sev + "|" + strings.Join(labels, ";")
	if _, dup := f.seen[key]; !dup && f.reports < feedScanMaxReports {
		f.seen[key] = struct{}{}
		f.reports++
		f.findings++
		if f.workCurrent != "" {
			err := supervisorstream.ReportFeedFinding(f.workCurrent, supervisorstream.FeedFinding{
				Runtime: f.runtime, Kind: kind, Severity: sev, Labels: labels, Session: f.session,
			}, time.Now())
			if err != nil {
				slog.Warn("dispatch: event scan: finding not recorded", "runtime", f.runtime)
			}
		}
	}
	if sev == "critical" && f.kill && f.cancel != nil {
		f.cancelled = "kill_on_critical:" + firstCritical
		f.cancel()
	}
}

// scanCaptured is the one-shot path: the run has already finished, so this is
// report-only (kill_on_critical cannot cancel a completed run). It replays the
// captured stdout through a fresh parser of the same runtime.
func scanCaptured(runtimeName, session string, out []byte) int {
	f := newFeedScanner(runtimeName, session, nil)
	if f == nil {
		return 0
	}
	p := runtime.ParserFor(runtimeName)
	for len(out) > 0 && !f.off {
		var line []byte
		if i := bytes.IndexByte(out, '\n'); i >= 0 {
			line, out = out[:i], out[i+1:]
		} else {
			line, out = out, nil
		}
		for _, ev := range p.Feed(line) {
			f.observe(ev)
		}
	}
	return f.findings
}
