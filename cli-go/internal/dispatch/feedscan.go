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
// Bounds: one event is scanned as at most feedScanEventBytes (head + tail), in
// chunks of feedScanChunkBytes that each carry their own deadline, the whole run
// as at most feedScanTotalBytes. A chunk that overruns its deadline skips the
// rest of that event; the third overrun switches the feed off. Nothing is
// buffered beyond a feedScanCarryBytes tail of the previous text event, which
// lets a phrase split across streaming fragments match. No finding echoes
// content, a path or a tool name.
//
// Detect-and-report is NOT a security boundary. Documented limits: the middle of
// an event larger than feedScanEventBytes is not scanned (and the parser drops
// bytes past 256 KiB before this runs); there is no encoding normalisation
// (homoglyphs, NBSP, newline between words, entities, short base64 evade it);
// runtimes with prose-only output (Plain events) are not scanned; and the byte
// budget is attacker-reachable (enough benign output ahead of a payload turns the
// feed off). The switch-off is recorded: the ledger's scan_off_reason and one
// review finding.
//
// Severity never reaches the ack gates: findings are written with
// recommended_action "review", which neither gate acts on (see
// supervisorstream.ReportFeedFinding). A critical text finding is still
// kill-eligible under kill_on_critical, including assistant text.

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/outputinjectionscan"
	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
	"github.com/bakw00ds/yakos/internal/runtime"
)

const (
	feedScanEventBytes = 32 * 1024
	feedScanTotalBytes = 4 * 1024 * 1024
	// feedScanChunkBytes bounds one scan call so a crafted event cannot make a
	// single call slow; feedScanOverlap keeps a match that spans two chunks, and
	// feedScanCarryBytes is the text tail carried into the next fragment.
	feedScanChunkBytes = 8 * 1024
	feedScanOverlap    = 512
	feedScanCarryBytes = 256
	feedScanDeadline   = 500 * time.Millisecond // per chunk
	feedScanMaxOverrun = 3
	// feedScanMaxWritten findings per run reach the findings file; up to
	// feedScanMaxCounted distinct ones are counted in the ledger.
	feedScanMaxWritten = 3
	feedScanMaxCounted = 64
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
	written   int
	findings  int
	overruns  int
	carry     string // tail of the previous text event
	seen      map[string]struct{}
	off       bool
	offReason string // "budget" or "deadline", "" while the feed is on
	cancelled string // ledger cancel reason, "" while the run is untouched
}

// newFeedScanner returns the scanner for a dispatch, or nil for claude.
//
// project is the request's project path: the findings directory is derived from
// it, never from the daemon's own environment (see feedWorkCurrent).
func newFeedScanner(runtimeName, session, project string, cancel context.CancelFunc) *feedScanner {
	if runtimeName == "claude" || runtimeName == "" {
		return nil
	}
	f := &feedScanner{
		runtime: runtimeName, session: session, cancel: cancel,
		workCurrent: feedWorkCurrent(project),
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

// feedWorkCurrent resolves the work/current directory of the REQUEST's project
// (an absolute path); "" when none resolves, in which case findings are counted
// only. Trust source: the project path comes from the dispatch request, so a
// daemon serving several projects files each run's findings under its own
// project and never under a project named by the daemon's environment.
//
//   - YAKOS_INPLACE_WORK=1: <project>/work/current (the layout paths.sh uses).
//   - YAKOS_WORK_DIR: honoured only when YAKOS_PROJECT_NAME (same operator
//     environment) names this very project (basename equal); otherwise a
//     daemon-wide override would mis-file another project's findings.
//   - otherwise $HOME/agent-control/<basename(project)>/work/current.
func feedWorkCurrent(project string) string {
	if project == "" || !filepath.IsAbs(project) {
		return ""
	}
	name := filepath.Base(project)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		return ""
	}
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		return filepath.Join(project, "work", "current")
	}
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" && os.Getenv("YAKOS_PROJECT_NAME") == name {
		return filepath.Join(v, "current")
	}
	home := os.Getenv("HOME")
	if home == "" {
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

// keyLabel drops a trailing parenthesised suffix: a label like
// "zero-width-unicode-steganography(N chars)" carries a model-chosen count that
// must not make otherwise identical findings look distinct.
func keyLabel(l string) string {
	if i := strings.IndexByte(l, '('); i > 0 && strings.HasSuffix(l, ")") {
		return l[:i]
	}
	return l
}

// scanChunks scans text in bounded, overlapping chunks, each with its own
// deadline. ok is false when a chunk overran (the remainder is skipped).
func (f *feedScanner) scanChunks(text string) (labels []string, ok bool) {
	seen := map[string]bool{}
	for start := 0; start < len(text); {
		end := start + feedScanChunkBytes
		if end > len(text) {
			end = len(text)
		}
		chunk := text[start:end]
		done := make(chan []string, 1)
		scan := f.scan
		go func() { done <- scan(chunk) }()
		timer := time.NewTimer(f.deadline)
		select {
		case ls := <-done:
			timer.Stop()
			for _, l := range ls {
				if !seen[l] {
					seen[l] = true
					labels = append(labels, l)
				}
			}
		case <-timer.C:
			return labels, false
		}
		if end == len(text) {
			break
		}
		start = end - feedScanOverlap
	}
	return labels, true
}

// observe scans one normalized event. It never blocks longer than one chunk's
// deadline per overrun and never fails the stream.
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
	raw := text
	text = boundEvent(text)
	if kind == "text" {
		// Streaming fragments: prepend the previous fragment's tail so a phrase
		// split across two of them still matches.
		text = f.carry + text
		if len(raw) >= feedScanCarryBytes {
			f.carry = raw[len(raw)-feedScanCarryBytes:]
		} else if len(f.carry)+len(raw) > feedScanCarryBytes {
			f.carry = (f.carry + raw)[len(f.carry)+len(raw)-feedScanCarryBytes:]
		} else {
			f.carry += raw
		}
	}
	if f.scanned+len(text) > feedScanTotalBytes {
		f.switchOff(kind, "budget")
		return
	}
	f.scanned += len(text)

	labels, ok := f.scanChunks(text)
	if !ok {
		f.overruns++
		slog.Warn("dispatch: event scan: a scan chunk overran its deadline; event skipped", "runtime", f.runtime)
		if f.overruns >= feedScanMaxOverrun {
			f.switchOff(kind, "deadline")
			return
		}
	}
	if len(labels) == 0 {
		return
	}
	f.report(kind, labels)
}

// switchOff ends the feed for the rest of the run and records why: the ledger's
// scan_off_reason and one review finding (exempt from the written-findings cap,
// bounded because the feed can only go off once).
func (f *feedScanner) switchOff(kind, reason string) {
	f.off, f.offReason = true, reason
	slog.Warn("dispatch: event scan: feed off for the rest of the run", "runtime", f.runtime, "reason", reason)
	f.write(kind, "warn", []string{"event-scan-disabled:" + reason})
}

func (f *feedScanner) write(kind, sev string, labels []string) {
	if f.workCurrent == "" {
		return
	}
	err := supervisorstream.ReportFeedFinding(f.workCurrent, supervisorstream.FeedFinding{
		Runtime: f.runtime, Kind: kind, Severity: sev, Labels: labels, Session: f.session,
	}, time.Now())
	if err != nil {
		slog.Warn("dispatch: event scan: finding not recorded", "runtime", f.runtime)
	}
}

func (f *feedScanner) report(kind string, labels []string) {
	sev, firstCritical := "warn", ""
	keys := make([]string, 0, len(labels))
	for _, l := range labels {
		keys = append(keys, keyLabel(l))
		if criticalLabels[l] && firstCritical == "" {
			sev, firstCritical = "critical", l
		}
	}
	sort.Strings(keys)
	key := sev + "|" + kind + "|" + strings.Join(keys, ";")
	if _, dup := f.seen[key]; !dup && f.findings < feedScanMaxCounted {
		f.seen[key] = struct{}{}
		f.findings++
		if f.written < feedScanMaxWritten { // beyond the cap: counted in the ledger only
			f.written++
			f.write(kind, sev, labels)
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
func scanCaptured(runtimeName, session, project string, out []byte) (findings int, offReason string) {
	f := newFeedScanner(runtimeName, session, project, nil)
	if f == nil {
		return 0, ""
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
	return f.findings, f.offReason
}
