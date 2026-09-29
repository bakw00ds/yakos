package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/framework"
)

// C1 (security-review-2026-09-14.md): before an upstream node's raw output
// is spliced into a downstream node's prompt, run it through the same
// injection-pattern detector that already covers Bash/Read/WebFetch/mcp__
// tool output inside a live session — lib/hooks/output-injection-scan.sh —
// but BLOCKING for this call site. A hit here means an upstream node's
// output is about to become another dispatched agent's instructions under
// bypassPermissions; that is a materially higher-stakes trust boundary than
// a human-supervised tool result inside a live session, so a match refuses
// the downstream node run rather than merely warning.
//
// The bash script is invoked with a synthetic tool_name of
// "WorkflowNodeOutput", which its own case statement (see
// lib/hooks/legacy/output-injection-scan.sh) recognizes as the one call
// site that blocks (ho_block, exit 2) on a match. Every existing
// PostToolUse caller (Bash/Read/WebFetch/mcp__, invoked by Claude Code
// itself via settings.json, never with this synthetic tool_name) is
// completely unaffected — same patterns, same WARN-only, non-blocking
// behavior as before this change.
//
// HOOK_FAIL_CLOSED=1 is set in THIS subprocess call's own environment only
// (built from os.Environ() plus one appended entry; the current process's
// environment is never mutated), so it can never leak into that live-session
// invocation of the same script. hook-input.sh's existing fail-closed
// machinery (security-review-2026-09-14.md's predecessor round, C5) then
// already does the right thing on a broken jq / malformed stdin for this
// call: it blocks, subject to the same YAKOS_HOOKS_FAIL_OPEN=1 /
// hook-bypass.md ("degraded-input") escape hatches documented there — no
// new escape-hatch mechanism was needed for that failure mode.

const (
	// outputInjectionScanRelPath is the hook script's path relative to the
	// yakOS framework root (lib root).
	outputInjectionScanRelPath = "lib/hooks/output-injection-scan.sh"

	// envWorkflowScanDisable, when "1", skips the workflow node-output scan
	// entirely — an explicit, documented operator opt-out. Mirrors the
	// hook script's own YAKOS_INJECTION_SCAN_DISABLE, which governs its
	// normal (non-workflow) callers.
	envWorkflowScanDisable = "YAKOS_WORKFLOW_INJECTION_SCAN_DISABLE"

	// envHooksFailOpen is the existing S-1 emergency kill switch
	// (lib/hooks/lib/hook-input.sh). Reused here for a distinct failure
	// mode: "the hook script itself could not be located or executed,"
	// as opposed to "the hook ran and found a match."
	envHooksFailOpen = "YAKOS_HOOKS_FAIL_OPEN"

	// workflowScanTimeout bounds how long one scan may run. The scan is a
	// handful of grep/jq passes over at most 50 KiB (the hook script's own
	// truncation), so this is generous headroom, not a tuned budget.
	workflowScanTimeout = 30 * time.Second
)

// OutputScanFunc is called once per upstream ${nodes.<id>.output} reference
// substituted into a downstream node's prompt, with the raw (already
// tail-truncated, not yet delimiter-wrapped) upstream bytes. A non-nil
// error means the substitution — and therefore the downstream node run —
// is refused (C1). nodeID is the upstream node that produced the output;
// agent is the downstream node's agent name (log/context only, not
// security-load-bearing).
type OutputScanFunc func(ctx context.Context, nodeID, agent string, output []byte) error

// NewOutputInjectionScanFunc returns the production OutputScanFunc, which
// shells out to lib/hooks/output-injection-scan.sh under yakosRoot.
//
// project is the workflow's project directory (Engine.Project). It is set
// as both the subprocess's working directory and its CLAUDE_PROJECT_DIR
// env var (R3, s3-flows-security-review-2026-09-21.md): without it, the
// script's own `${CLAUDE_PROJECT_DIR:-$PWD}` fallback resolves against
// whatever directory the daemon happens to be running in — not the
// workflow's actual project — for both its .yakos.yml lookup and its log
// directory. project may be empty (some callers may not have one); the
// script falls back to its own process cwd in that case, exactly as
// before this fix.
//
// A nil error from the returned func means "the scan ran clean" OR
// "scanning was explicitly skipped" (disabled via env, or the emergency
// fail-open override was used because the hook infrastructure itself could
// not be reached) — every skip path is logged to stderr, so it is never a
// silent pass.
func NewOutputInjectionScanFunc(yakosRoot, project string) OutputScanFunc {
	// N3 (s3-flows-security-review-r2-2026-09-21.md): resolve the
	// subprocess's working directory ONCE, at construction time, rather
	// than trusting project on every call. R3 (round 2) set cmd.Dir =
	// project unconditionally; if project does not exist (a renamed or
	// removed workspace — a configuration fault, not a security-
	// infrastructure fault), exec.Cmd's own chdir fails BEFORE the script
	// ever runs, which routed through handleScanInfraFailure and failed
	// closed for every node consuming upstream output, surfacing a raw
	// "chdir: no such file or directory" error confusingly attributed to
	// the injection scanner. Fall back to an inherited cwd (cmd.Dir == "",
	// the pre-R3 behavior) when project isn't usable, and warn once here
	// instead of once per scan call. CLAUDE_PROJECT_DIR is still set to the
	// original project value either way (below) — the hook script's own
	// "${CLAUDE_PROJECT_DIR:-$PWD}" fallback and its log-directory
	// mkdir -p handle a stale/nonexistent value gracefully; only cmd.Dir's
	// hard os/exec chdir cannot.
	cmdDir := project
	if project != "" {
		if fi, statErr := os.Stat(project); statErr != nil {
			fmt.Fprintf(os.Stderr,
				"workflow: WARN — output-injection-scan project directory %q is not usable (%v); "+
					"running the scan from the process's own working directory instead of failing "+
					"closed on a chdir error.\n", project, statErr)
			cmdDir = ""
		} else if !fi.IsDir() {
			fmt.Fprintf(os.Stderr,
				"workflow: WARN — output-injection-scan project directory %q is not a directory; "+
					"running the scan from the process's own working directory instead of failing "+
					"closed on a chdir error.\n", project)
			cmdDir = ""
		}
	}

	// N9: pin the integrity digest of the hook script set at construction.
	// A node running under bypassPermissions that later overwrites the
	// script cannot make the scan a no-op: every call re-hashes and
	// compares. If an embedded framework copy is available the pin is also
	// checked against it, so a script already tampered with BEFORE
	// construction is refused too.
	pin := &hookPin{}
	pinErr := pin.pinAtConstruction(yakosRoot)

	if os.Getenv(envWorkflowScanDisable) == "1" {
		// N11: never a silent disable. Loud, once, at construction; and
		// again (plus a run-visible record) on every skipped scan.
		fmt.Fprintf(os.Stderr, "workflow: WARN — %s=1 is set: the Flows upstream-output injection scan is DISABLED "+
			"for every workflow run by this process. Node output will be spliced into downstream prompts UNSCANNED.\n",
			envWorkflowScanDisable)
	}

	return func(ctx context.Context, nodeID, _ string, output []byte) error {
		rep := scanReportFrom(ctx)
		if os.Getenv(envWorkflowScanDisable) == "1" {
			reason := envWorkflowScanDisable + "=1: injection scan disabled by operator environment; output of node \"" + nodeID + "\" forwarded UNSCANNED"
			fmt.Fprintf(os.Stderr, "workflow: WARN — %s\n", reason)
			rep.add(ScanEvent{Kind: ScanEventDisabled, UpstreamNode: nodeID, Reason: reason})
			return nil
		}

		// Stage 1 (pure Go, full payload): K-83 R4/N10, N8, N12. The bytes
		// scanned here are exactly the bytes the engine forwards.
		allow := scanAllowFrom(ctx)
		var hits, suppressed []string
		for _, id := range scanPatternMatches(output) {
			if allow[id] {
				suppressed = append(suppressed, id)
			} else {
				hits = append(hits, id)
			}
		}
		if len(suppressed) > 0 {
			reason := fmt.Sprintf("scan_allow on node %q suppressed pattern(s) %s; its output is NOT blocked for them", nodeID, strings.Join(suppressed, ", "))
			fmt.Fprintf(os.Stderr, "workflow: WARN — %s\n", reason)
			rep.add(ScanEvent{Kind: ScanEventAllowUsed, UpstreamNode: nodeID, Patterns: suppressed, Reason: reason})
		}
		if len(hits) > 0 {
			return fmt.Errorf("node %q output blocked by output-injection-scan: go-scan: BLOCKED — suspicious patterns detected in upstream workflow node output (%s). "+
				"Refusing to splice this into a downstream node's prompt. To permit a known false positive, list the pattern under scan_allow on node %q.",
				nodeID, strings.Join(hits, "; "), nodeID)
		}

		// Stage 2: the bash hook (independent second opinion + its NDJSON
		// log). Existence first, then integrity (N9), then dependencies (N12).
		hookPath := filepath.Join(yakosRoot, filepath.FromSlash(outputInjectionScanRelPath))
		if _, err := os.Stat(hookPath); err != nil {
			return handleScanInfraFailure(rep, nodeID, fmt.Errorf("output-injection-scan hook not found at %s: %w", hookPath, err))
		}

		if pinErr != nil {
			return handleScanInfraFailure(rep, nodeID, fmt.Errorf("output-injection-scan hook integrity check failed at startup: %w", pinErr))
		}
		if err := pin.check(yakosRoot); err != nil {
			return handleScanInfraFailure(rep, nodeID, err)
		}
		for _, dep := range hookDependencies {
			if _, err := lookPath(dep); err != nil {
				return handleScanInfraFailure(rep, nodeID, fmt.Errorf(
					"output-injection-scan hook needs %q on PATH but it was not found (%v); without it a hook pattern would be silently skipped", dep, err))
			}
		}

		payload, err := json.Marshal(map[string]string{
			"tool_name":     "WorkflowNodeOutput",
			"tool_response": string(output),
			"agent_type":    "flows:" + nodeID,
		})
		if err != nil {
			return handleScanInfraFailure(rep, nodeID, fmt.Errorf("marshal output-injection-scan payload: %w", err))
		}

		scanCtx, cancel := context.WithTimeout(ctx, workflowScanTimeout)
		defer cancel()

		cmd := exec.CommandContext(scanCtx, "bash", hookPath) //nolint:gosec
		cmd.Stdin = bytes.NewReader(payload)
		// cmd.Dir: empty string means "inherit the calling process's cwd"
		// (Go's documented default), so this is safe whenever cmdDir=="" —
		// either because project itself was empty, or because it wasn't
		// usable (N3, see cmdDir's resolution above).
		cmd.Dir = cmdDir
		// HOOK_FAIL_CLOSED=1 and CLAUDE_PROJECT_DIR are set on THIS
		// exec.Cmd's own Env slice only (built from os.Environ(), not
		// os.Setenv) — neither can affect any other invocation of this
		// script, including a live Claude Code session's own PostToolUse
		// call to the exact same file. An empty project value here still
		// takes the script's own "${CLAUDE_PROJECT_DIR:-$PWD}" fallback
		// (POSIX ":-" triggers on empty as well as unset).
		cmd.Env = append(os.Environ(), "HOOK_FAIL_CLOSED=1", "CLAUDE_PROJECT_DIR="+project)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr

		runErr := cmd.Run()
		if runErr == nil {
			return nil // exit 0: clean pass.
		}

		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			// Could not even launch bash / the script.
			return handleScanInfraFailure(rep, nodeID, fmt.Errorf("run output-injection-scan: %w", runErr))
		}

		if exitErr.ExitCode() == 2 {
			// The hook's own ho_block already wrote its stderr message and
			// its own NDJSON log record; surface the message here.
			msg := strings.TrimSpace(stderr.String())
			// R6: a hook-stage block is honored unless EVERY label the hook
			// reported is in this node's scan_allow list. An unparseable
			// message stays a block (fail closed).
			if labels, ok := hookBlockLabels(msg); ok && len(labels) > 0 && allLabelsAllowed(labels, allow) {
				reason := fmt.Sprintf("scan_allow on node %q suppressed hook-stage pattern(s) %s; its output is NOT blocked for them", nodeID, strings.Join(labels, ", "))
				fmt.Fprintf(os.Stderr, "workflow: WARN — %s\n", reason)
				rep.add(ScanEvent{Kind: ScanEventAllowUsed, UpstreamNode: nodeID, Patterns: labels, Reason: reason})
				return nil
			}
			if msg == "" {
				msg = "output-injection-scan blocked this node's output"
			}
			return fmt.Errorf("node %q output blocked by output-injection-scan: %s", nodeID, msg)
		}

		// Any other non-zero exit is an unexpected script failure. Treat it
		// like an infra failure (fail closed, subject to the same escape
		// hatch) rather than silently treating "the scanner crashed" as
		// "the scanner found nothing."
		return handleScanInfraFailure(rep, nodeID, fmt.Errorf("output-injection-scan exited %d: %s", exitErr.ExitCode(), strings.TrimSpace(stderr.String())))
	}
}

// handleScanInfraFailure is the fail-closed default for "the scan itself
// could not be trusted" (missing script, exec failure, unexpected exit
// code) — distinct from "the scan ran and found a match" (handled directly
// in NewOutputInjectionScanFunc). It fails closed (blocks the node) unless
// the existing YAKOS_HOOKS_FAIL_OPEN=1 emergency override
// (lib/hooks/lib/hook-input.sh) is set, in which case it warns to stderr
// and lets the node proceed unscanned.
func handleScanInfraFailure(rep *scanReport, nodeID string, cause error) error {
	if os.Getenv(envHooksFailOpen) == "1" {
		reason := fmt.Sprintf("output-injection-scan hook stage could not run (%v), but %s=1 is set; "+
			"proceeding WITHOUT the hook stage for node %q output (the in-process Go scan stage still ran).", cause, envHooksFailOpen, nodeID)
		fmt.Fprintf(os.Stderr, "workflow: WARN — %s\n", reason)
		rep.add(ScanEvent{Kind: ScanEventFailOpen, UpstreamNode: nodeID, Reason: reason})
		return nil
	}
	return fmt.Errorf("%w (set %s=1 to proceed without the hook stage for this call, or %s=1 to disable this scan entirely)",
		cause, envHooksFailOpen, envWorkflowScanDisable)
}

// hookDependencies are the external binaries the hook script's own critical
// path needs (N12: awk was added silently by the N2 rewrite; a missing awk
// made bash return 127, the `if` false, and the base64 pattern quietly dead).
// A missing one is an infrastructure failure that fails loudly with a clear
// message rather than the hook silently skipping a pattern.
var hookDependencies = []string{"bash", "grep", "awk", "jq"}

// lookPath is exec.LookPath, replaceable in tests.
var lookPath = exec.LookPath

// hookSetRelPaths lists (relative to lib/hooks) every file the hook executes
// or sources: the script itself and everything under lib/ it dot-sources.
func hookSetRelPaths(yakosRoot string) ([]string, error) {
	hooksDir := filepath.Join(yakosRoot, "lib", "hooks")
	rels := []string{"output-injection-scan.sh"}
	entries, err := os.ReadDir(filepath.Join(hooksDir, "lib"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sh") {
			rels = append(rels, "lib/"+e.Name())
		}
	}
	sort.Strings(rels)
	return rels, nil
}

// hookSetDigest returns a sha256 over the sorted (name, content-sha256)
// pairs of the hook file set (N9).
func hookSetDigest(yakosRoot string) (string, error) {
	rels, err := hookSetRelPaths(yakosRoot)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, rel := range rels {
		b, err := os.ReadFile(filepath.Join(yakosRoot, "lib", "hooks", filepath.FromSlash(rel))) //nolint:gosec
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(b)
		fmt.Fprintf(h, "%s\x00%s\n", rel, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// embeddedHookFile returns the embedded framework copy of a lib/hooks file,
// if this binary carries one (release builds after `make embed-lib`; dev
// builds carry only placeholders). Replaceable in tests.
var embeddedHookFile = func(rel string) ([]byte, bool) {
	b, err := framework.LibFS().ReadFile("embedded/hooks/" + rel)
	if err != nil {
		return nil, false
	}
	return b, true
}

// verifyAgainstEmbedded compares each on-disk hook file with the embedded
// copy, when one exists, and reports any difference (N9).
func verifyAgainstEmbedded(yakosRoot string) error {
	rels, err := hookSetRelPaths(yakosRoot)
	if err != nil {
		return err
	}
	for _, rel := range rels {
		want, ok := embeddedHookFile(rel)
		if !ok {
			continue
		}
		got, err := os.ReadFile(filepath.Join(yakosRoot, "lib", "hooks", filepath.FromSlash(rel))) //nolint:gosec
		if err != nil {
			return err
		}
		if sha256.Sum256(got) != sha256.Sum256(want) {
			return fmt.Errorf("lib/hooks/%s differs from the embedded framework copy (on-disk sha256 %x, embedded %x)",
				rel, sha256.Sum256(got), sha256.Sum256(want))
		}
	}
	return nil
}

// hookPin holds the pinned digest of the hook file set. If the files did not
// exist at construction the pin is taken lazily on first use (the existing
// "hook not found" path still reports that case first).
type hookPin struct {
	mu     sync.Mutex
	digest string
}

func (p *hookPin) pinAtConstruction(yakosRoot string) error {
	d, err := hookSetDigest(yakosRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // reported later as "hook not found"; pin lazily
		}
		return err
	}
	if err := verifyAgainstEmbedded(yakosRoot); err != nil {
		return err
	}
	p.mu.Lock()
	p.digest = d
	p.mu.Unlock()
	return nil
}

// check re-hashes the hook file set and compares with the pin.
func (p *hookPin) check(yakosRoot string) error {
	d, err := hookSetDigest(yakosRoot)
	if err != nil {
		return fmt.Errorf("output-injection-scan hook integrity check: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.digest == "" {
		if err := verifyAgainstEmbedded(yakosRoot); err != nil {
			return fmt.Errorf("output-injection-scan hook integrity check: %w", err)
		}
		p.digest = d
		return nil
	}
	if d != p.digest {
		return fmt.Errorf("output-injection-scan hook integrity check FAILED: the hook script set under %s/lib/hooks changed since the "+
			"scan was initialised (pinned sha256 %s, now %s); refusing to execute a modified scanner", yakosRoot, p.digest, d)
	}
	return nil
}

var hookLabelsRe = regexp.MustCompile(`(?s)workflow node output \((.*?)\)\. Refusing`)
var labelCountSuffixRe = regexp.MustCompile(`\(\d+ chars\)$`)

// hookBlockLabels extracts the pattern labels from the hook's block message.
func hookBlockLabels(msg string) ([]string, bool) {
	m := hookLabelsRe.FindStringSubmatch(msg)
	if m == nil {
		return nil, false
	}
	var out []string
	for _, l := range strings.Split(m[1], "; ") {
		l = strings.TrimSpace(labelCountSuffixRe.ReplaceAllString(strings.TrimSpace(l), ""))
		if l != "" {
			out = append(out, l)
		}
	}
	return out, len(out) > 0
}

func allLabelsAllowed(labels []string, allow map[string]bool) bool {
	for _, l := range labels {
		if !allow[l] {
			return false
		}
	}
	return true
}

// ---- per-call context: scan_allow list and run-visible report ----

type scanCtxKey int

const (
	scanAllowKey scanCtxKey = iota
	scanReportKey
)

// withScanAllow attaches the PRODUCING node's scan_allow list to ctx. The
// list applies to that one node's output only: the engine attaches a fresh
// list (possibly empty) for every upstream node it scans, so an opt-out on
// one node never suppresses detection on another.
func withScanAllow(ctx context.Context, ids []string) context.Context {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return context.WithValue(ctx, scanAllowKey, m)
}

func scanAllowFrom(ctx context.Context) map[string]bool {
	m, _ := ctx.Value(scanAllowKey).(map[string]bool)
	return m
}

// ScanEventKind classifies a ScanEvent.
type ScanEventKind string

const (
	// ScanEventDisabled: the whole scan was skipped by
	// YAKOS_WORKFLOW_INJECTION_SCAN_DISABLE=1 (N11).
	ScanEventDisabled ScanEventKind = "scan_disabled"
	// ScanEventFailOpen: the hook stage was skipped under
	// YAKOS_HOOKS_FAIL_OPEN=1; the in-process stage still ran.
	ScanEventFailOpen ScanEventKind = "hook_stage_fail_open"
	// ScanEventAllowUsed: a node's scan_allow list suppressed a match (R6).
	ScanEventAllowUsed ScanEventKind = "scan_allow_used"
)

// ScanEvent is one visible, non-blocking deviation from a full scan.
type ScanEvent struct {
	Kind         ScanEventKind `json:"kind"`
	UpstreamNode string        `json:"upstream_node"`
	Patterns     []string      `json:"patterns,omitempty"`
	Reason       string        `json:"reason"`
}

// scanReport collects ScanEvents for one scan call.
type scanReport struct {
	mu     sync.Mutex
	events []ScanEvent
}

func (r *scanReport) add(ev ScanEvent) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func withScanReport(ctx context.Context, r *scanReport) context.Context {
	return context.WithValue(ctx, scanReportKey, r)
}

func scanReportFrom(ctx context.Context) *scanReport {
	r, _ := ctx.Value(scanReportKey).(*scanReport)
	return r
}

// scanStatusFile is written into a run's directory whenever the scan was
// disabled, partially skipped, or a scan_allow entry was used (N11, R6). It
// is a sibling of run.json, not a field inside it, because run.json's
// schema lives in runstate.go (out of this change's scope).
const scanStatusFile = "scan_status.json"

var scanStatusMu sync.Mutex

type scanStatusDoc struct {
	Events []scanStatusEntry `json:"events"`
}

type scanStatusEntry struct {
	DownstreamNode string `json:"downstream_node"`
	ScanEvent
	TS time.Time `json:"ts"`
}

// appendScanStatus appends events to <runDir>/scan_status.json atomically.
// Failure is logged, never fatal: the stderr warning was already emitted.
func appendScanStatus(runDir, downstream string, events []ScanEvent) {
	if runDir == "" || len(events) == 0 {
		return
	}
	scanStatusMu.Lock()
	defer scanStatusMu.Unlock()
	path := filepath.Join(runDir, scanStatusFile)
	var doc scanStatusDoc
	if b, err := os.ReadFile(path); err == nil { //nolint:gosec
		_ = json.Unmarshal(b, &doc)
	}
	now := time.Now().UTC()
	for _, ev := range events {
		doc.Events = append(doc.Events, scanStatusEntry{DownstreamNode: downstream, ScanEvent: ev, TS: now})
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err == nil {
		tmp := path + ".tmp"
		if err = os.WriteFile(tmp, b, 0o644); err == nil { //nolint:gosec
			err = os.Rename(tmp, path)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "workflow: WARN — could not record %s in %s: %v\n", scanStatusFile, runDir, err)
	}
}
