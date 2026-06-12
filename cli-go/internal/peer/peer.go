// Package peer is the Go port of cli/lib/peer.sh (rank 31).
//
// It implements the `yakos peer` subcommand family for multi-developer
// coordination on a shared dev box (Plan 1 / co-pilot mode).
// Coordination state lives under /var/lib/yakos/<project>/coord/.
//
// # Synopsis
//
//	yakos peer status [<project>]
//	yakos peer log [--since <iso>] [<project>]
//	yakos peer claim <file> [<project>]
//	yakos peer release <file> [<project>]
//	yakos peer claims [<project>]
//	yakos peer deadlock [<project>]
//	yakos peer propose-mode --mode <m> --targets <glob>... [--reason <t>] [--timeout <secs>] [<project>]
//	yakos peer respond-mode --to <proposal-id> --ack|--reject [--reason <t>] [<project>]
//	yakos peer handoff --to <user@host> --completed-scope <s> --notes <s> --next-action <s> [<project>]
//	yakos peer handoff --ack <handoff-id>|--reject <handoff-id> [--reason <t>] [<project>]
//
// # No-op when coord not configured
//
// All subcommands exit cleanly (exit 64 with an advisory message) when the
// coord directory does not exist for the project.  This preserves the
// load-bearing guarantee: yakOS works the same with or without multi-dev.
//
// # Mailbox wire format
//
// Appends are byte-identical to bash peer.sh output so that bash peer may
// consume Go-written mailboxes during shadow mode.
//
// # Project inference
//
// Matches the bash infer_project() logic:
//  1. If cwd is under ~/agent-control/<name>/, derive name from path.
//  2. Scan ~/agent-control/*/.project-path files for a match.
//
// # Coord directory resolution
//
// Defaults to /var/lib/yakos/<project>/coord/ (matching bash yakos_coord_dir).
// Tests inject CoordDirFn to point at a temp directory without root access.
package peer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/mailbox"
)

// ---- public types -----------------------------------------------------------

// Config carries all inputs to Run.
type Config struct {
	// Subcommand is one of: status, log, claim, release, claims, deadlock,
	// propose-mode, respond-mode, handoff, help.
	Subcommand string

	// Args are the remaining positional/flag arguments after the subcommand.
	Args []string

	// HomeDir overrides os.Getenv("HOME").  Empty means use env.
	HomeDir string

	// User overrides the current OS user (os.Getenv("USER") / id -un).
	// Injected in tests.
	User string

	// Host overrides the hostname.
	Host string

	// PID overrides the process ID.
	PID int

	// Now overrides time.Now() for deterministic tests.
	Now func() time.Time

	// CoordDirFn overrides the default /var/lib/yakos/<project>/coord path.
	// When nil, the default resolution is used.
	CoordDirFn func(project string) string

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand that was executed.
	Subcommand string

	// Project that was resolved.
	Project string

	// CoordDir is the coord directory used.
	CoordDir string

	// EventsEmitted is the count of NDJSON lines appended to the log(s).
	EventsEmitted int
}

// ---- entry point ------------------------------------------------------------

// Run executes the requested peer subcommand and returns a Result.
// It never calls os.Exit; callers translate non-nil error to exit codes.
func Run(cfg Config) (*Result, error) {
	cfg = cfg.withDefaults()

	switch cfg.Subcommand {
	case "", "help", "-h", "--help":
		PrintHelp(cfg.Writer)
		return &Result{Subcommand: "help"}, nil
	case "status":
		return runStatus(cfg)
	case "log":
		return runLog(cfg)
	case "claim":
		return runClaim(cfg)
	case "release":
		return runRelease(cfg)
	case "claims":
		return runClaims(cfg)
	case "deadlock":
		return runDeadlock(cfg)
	case "propose-mode":
		return runProposeMode(cfg)
	case "respond-mode":
		return runRespondMode(cfg)
	case "handoff":
		return runHandoff(cfg)
	default:
		return nil, fmt.Errorf("peer: unknown subcommand %q (try --help)", cfg.Subcommand)
	}
}

// PrintHelp writes the usage text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos peer <subcommand> [args...]

Multi-developer coordination on a shared dev box (Plan 1 / co-pilot mode).
Reads / appends shared state at /var/lib/yakos/<project>/coord/.

Subcommands:
  status [<project>]              Active peer sessions for the project. (M1)
  log [--since <iso>] [<project>] Tail coord/activity.ndjson. (M1)
  claim <file> [<project>]        Manually claim a file (operator override). (M2)
  release <file> [<project>]      Release this session's claim on a file. (M2)
  claims [<project>]              List all active claims for the project. (M2)
  deadlock [<project>]            Compute wait-for graph from activity log. (M2)
  propose-mode --mode <m> --targets <glob>... [--reason <t>] [--timeout <secs>] [<project>]
                                  Propose parallel/serialize/defer for contended
                                  targets; wait synchronously up to 60s (default)
                                  for peer's mode_response. (M3)
  respond-mode --to <proposal-id> --ack|--reject [--reason <t>] [<project>]
                                  Respond to a peer's mode proposal. (M3)
  handoff --to <user@host> --completed-scope <s> --notes <s> --next-action <s> [<project>]
                                  Hand off work to another peer; emits a
                                  peer_handoff event with structured context. (v0.35+)
  handoff --ack <handoff-id> | --reject <handoff-id> [--reason <t>] [<project>]
                                  Acknowledge or reject a received handoff. (v0.35+)

Setup:
  Each developer runs 'yakos init --multi-dev <name> --project <path>'.
  Coord dir must exist and be writable (group yakos-coord). See
  docs/co-pilot-mode.md for the dev-box admin recipe.

Examples:
  yakos peer status                  # auto-detect project from cwd
  yakos peer status myapp
  yakos peer log --since 2026-05-22T15:00:00Z myapp
`)
}

// ---- subcommand: status -----------------------------------------------------

func runStatus(cfg Config) (*Result, error) {
	projectArg := ""
	if len(cfg.Args) > 0 {
		projectArg = cfg.Args[0]
	}
	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos peer status — project: %s\n", project)
	_, _ = fmt.Fprintf(cfg.Writer, "coord dir: %s\n", coordDir)
	_, _ = fmt.Fprintln(cfg.Writer)

	sessionsDir := filepath.Join(coordDir, "sessions")
	entries, _ := os.ReadDir(sessionsDir)
	var ndjsonFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".ndjson") {
			ndjsonFiles = append(ndjsonFiles, e)
		}
	}

	if len(ndjsonFiles) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "  (no peer sessions on record)")
		_, _ = fmt.Fprintln(cfg.Writer)
		_, _ = fmt.Fprintln(cfg.Writer, "  Coord is configured but no session has emitted yet. Either you")
		_, _ = fmt.Fprintf(cfg.Writer, "  haven't started a session since enabling --multi-dev, or no one\n")
		_, _ = fmt.Fprintln(cfg.Writer, "  else has either. Start one: yakos start "+project)
		return &Result{Subcommand: "status", Project: project, CoordDir: coordDir}, nil
	}

	_, _ = fmt.Fprintf(cfg.Writer, "  %-32s %-22s %-12s %s\n", "SESSION", "LAST_EVENT", "EVENT_KIND", "AGENT")
	for _, e := range ndjsonFiles {
		name := strings.TrimSuffix(e.Name(), ".ndjson")
		path := filepath.Join(sessionsDir, e.Name())
		last, _ := mailbox.LastLine(path)
		if last == "" {
			_, _ = fmt.Fprintf(cfg.Writer, "  %-32s %-22s %-12s %s\n", name, "(empty)", "-", "-")
			continue
		}
		ts, kind, agent := extractEventFields(last)
		_, _ = fmt.Fprintf(cfg.Writer, "  %-32s %-22s %-12s %s\n", name, ts, kind, agent)
	}

	return &Result{Subcommand: "status", Project: project, CoordDir: coordDir}, nil
}

// ---- subcommand: log --------------------------------------------------------

func runLog(cfg Config) (*Result, error) {
	since := ""
	projectArg := ""
	args := cfg.Args

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--since":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer log: --since requires a value")
			}
			since = args[i]
		case strings.HasPrefix(args[i], "--since="):
			since = args[i][len("--since="):]
		case strings.HasPrefix(args[i], "-"):
			return nil, fmt.Errorf("peer log: unknown flag %q", args[i])
		default:
			if projectArg == "" {
				projectArg = args[i]
			} else {
				return nil, fmt.Errorf("peer log: too many positional args")
			}
		}
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	logPath := filepath.Join(coordDir, "activity.ndjson")
	lines, err := mailbox.ReadLines(logPath)
	if err != nil {
		return nil, fmt.Errorf("peer log: %w", err)
	}
	if lines == nil {
		_, _ = fmt.Fprintf(cfg.Writer, "yakos peer log: no activity yet (%s missing)\n", logPath)
		return &Result{Subcommand: "log", Project: project, CoordDir: coordDir}, nil
	}

	var out [][]byte
	if since != "" {
		out = mailbox.FilterSince(lines, since)
	} else {
		out = mailbox.Tail(lines, 50)
	}

	for _, l := range out {
		_, _ = fmt.Fprintf(cfg.Writer, "%s\n", l)
	}

	return &Result{Subcommand: "log", Project: project, CoordDir: coordDir}, nil
}

// ---- subcommand: claims -----------------------------------------------------

func runClaims(cfg Config) (*Result, error) {
	projectArg := ""
	if len(cfg.Args) > 0 {
		projectArg = cfg.Args[0]
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	claimsFile := filepath.Join(coordDir, "active-claims.json")
	data, readErr := os.ReadFile(claimsFile) //nolint:gosec
	if os.IsNotExist(readErr) {
		_, _ = fmt.Fprintln(cfg.Writer, "yakos peer claims: no active-claims.json yet (no edits posted)")
		return &Result{Subcommand: "claims", Project: project, CoordDir: coordDir}, nil
	}
	if readErr != nil {
		return nil, fmt.Errorf("peer claims: read %s: %w", claimsFile, readErr)
	}

	var doc struct {
		GeneratedAt string `json:"generated_at"`
		Claims      []struct {
			Path   string `json:"path"`
			Owners []struct {
				Status    string `json:"status"`
				ExpiresAt string `json:"expires_at"`
				User      string `json:"user"`
				Host      string `json:"host"`
				Agent     string `json:"agent"`
			} `json:"owners"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("peer claims: parse %s: %w", claimsFile, err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos peer claims — project: %s\n", project)
	_, _ = fmt.Fprintf(cfg.Writer, "generated_at: %s\n", doc.GeneratedAt)
	_, _ = fmt.Fprintln(cfg.Writer)
	_, _ = fmt.Fprintf(cfg.Writer, "  %-50s %-10s %-22s %s\n", "PATH", "STATUS", "EXPIRES", "OWNER")
	for _, c := range doc.Claims {
		if len(c.Owners) == 0 {
			continue
		}
		o := c.Owners[0]
		agent := o.Agent
		if agent == "" {
			agent = "-"
		}
		_, _ = fmt.Fprintf(cfg.Writer, "  %s %s %s %s@%s (agent: %s)\n",
			c.Path, o.Status, o.ExpiresAt, o.User, o.Host, agent)
	}

	return &Result{Subcommand: "claims", Project: project, CoordDir: coordDir}, nil
}

// ---- subcommand: claim ------------------------------------------------------

func runClaim(cfg Config) (*Result, error) {
	if len(cfg.Args) < 1 {
		return nil, fmt.Errorf("peer claim: <file> required")
	}
	file := cfg.Args[0]
	projectArg := ""
	if len(cfg.Args) >= 2 {
		projectArg = cfg.Args[1]
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	now := cfg.now()
	ts := mailbox.FormatTS(now)
	exp := mailbox.FormatExpiry(now, 1800)

	detail, _ := json.Marshal(map[string]interface{}{
		"path":        file,
		"ttl_seconds": 1800,
		"expires_at":  exp,
		"source":      "yakos peer claim",
	})
	e := mailbox.Event{
		Ts:   ts,
		Kind: "claim_confirmed",
		Actor: mailbox.Actor{
			User:      cfg.User,
			Host:      cfg.Host,
			PID:       cfg.PID,
			SessionID: "manual",
			Agent:     "operator",
		},
		Detail: json.RawMessage(detail),
	}
	emitted := 0

	activityPath := filepath.Join(coordDir, "activity.ndjson")
	if err := mailbox.AppendEvent(activityPath, e); err != nil {
		return nil, fmt.Errorf("peer claim: append activity: %w", err)
	}
	emitted++

	sessPath := filepath.Join(coordDir, "sessions",
		mailbox.SessionFileName(cfg.User, cfg.Host, cfg.PID))
	if err := mailbox.AppendEvent(sessPath, e); err != nil {
		return nil, fmt.Errorf("peer claim: append session: %w", err)
	}
	emitted++

	_, _ = fmt.Fprintf(cfg.Writer, "claimed: %s for %s@%s (30min TTL)\n", file, cfg.User, cfg.Host)
	_, _ = fmt.Fprintf(cfg.Writer, "view with: yakos peer claims %s\n", project)

	return &Result{Subcommand: "claim", Project: project, CoordDir: coordDir, EventsEmitted: emitted}, nil
}

// ---- subcommand: release ----------------------------------------------------

func runRelease(cfg Config) (*Result, error) {
	if len(cfg.Args) < 1 {
		return nil, fmt.Errorf("peer release: <file> required")
	}
	file := cfg.Args[0]
	projectArg := ""
	if len(cfg.Args) >= 2 {
		projectArg = cfg.Args[1]
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	ts := mailbox.FormatTS(cfg.now())
	detail, _ := json.Marshal(map[string]interface{}{
		"path":   file,
		"source": "yakos peer release",
	})
	e := mailbox.Event{
		Ts:   ts,
		Kind: "claim_released",
		Actor: mailbox.Actor{
			User:      cfg.User,
			Host:      cfg.Host,
			PID:       cfg.PID,
			SessionID: "manual",
			Agent:     "operator",
		},
		Detail: json.RawMessage(detail),
	}
	emitted := 0

	if err := mailbox.AppendEvent(filepath.Join(coordDir, "activity.ndjson"), e); err != nil {
		return nil, fmt.Errorf("peer release: append activity: %w", err)
	}
	emitted++

	sessPath := filepath.Join(coordDir, "sessions",
		mailbox.SessionFileName(cfg.User, cfg.Host, cfg.PID))
	if err := mailbox.AppendEvent(sessPath, e); err != nil {
		return nil, fmt.Errorf("peer release: append session: %w", err)
	}
	emitted++

	_, _ = fmt.Fprintln(cfg.Writer, "released: "+file+" (claim removed from active-claims.json on next rebuild)")

	return &Result{Subcommand: "release", Project: project, CoordDir: coordDir, EventsEmitted: emitted}, nil
}

// ---- subcommand: deadlock ---------------------------------------------------

func runDeadlock(cfg Config) (*Result, error) {
	projectArg := ""
	if len(cfg.Args) > 0 {
		projectArg = cfg.Args[0]
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	claimsFile := filepath.Join(coordDir, "active-claims.json")
	activityLog := filepath.Join(coordDir, "activity.ndjson")

	claimsData, cerr := os.ReadFile(claimsFile) //nolint:gosec
	actLines, aerr := mailbox.ReadLines(activityLog)

	if os.IsNotExist(cerr) || aerr != nil || actLines == nil {
		_, _ = fmt.Fprintln(cfg.Writer, "yakos peer deadlock: no claims or activity log to analyze")
		return &Result{Subcommand: "deadlock", Project: project, CoordDir: coordDir}, nil
	}
	if cerr != nil {
		return nil, fmt.Errorf("peer deadlock: read claims: %w", cerr)
	}

	var claimsDoc struct {
		Claims []struct {
			Path   string `json:"path"`
			Owners []struct {
				User string `json:"user"`
				Host string `json:"host"`
				PID  int    `json:"pid"`
			} `json:"owners"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(claimsData, &claimsDoc); err != nil {
		return nil, fmt.Errorf("peer deadlock: parse claims: %w", err)
	}

	// Build path → owner lookup from active-claims.json.
	type ownerKey struct {
		user, host string
		pid        int
	}
	pathOwner := map[string]ownerKey{}
	for _, c := range claimsDoc.Claims {
		if len(c.Owners) > 0 {
			o := c.Owners[0]
			pathOwner[c.Path] = ownerKey{o.User, o.Host, o.PID}
		}
	}

	_, _ = fmt.Fprintf(cfg.Writer, "yakos peer deadlock — project: %s\n", project)
	_, _ = fmt.Fprintln(cfg.Writer)

	// Scan activity log for claim_intent events.
	seen := map[string]bool{}
	found := false
	for _, l := range actLines {
		var ev struct {
			Kind  string `json:"kind"`
			Actor struct {
				User string `json:"user"`
				Host string `json:"host"`
				PID  int    `json:"pid"`
			} `json:"actor"`
			Detail struct {
				Path string `json:"path"`
			} `json:"detail"`
		}
		if err := json.Unmarshal(l, &ev); err != nil {
			continue
		}
		if ev.Kind != "claim_intent" {
			continue
		}
		owner, held := pathOwner[ev.Detail.Path]
		if !held {
			continue
		}
		// Owner is a different session than the intent actor.
		if owner.user == ev.Actor.User && owner.host == ev.Actor.Host && owner.pid == ev.Actor.PID {
			continue
		}
		msg := fmt.Sprintf("  %s@%s wants %s — held by %s@%s",
			ev.Actor.User, ev.Actor.Host, ev.Detail.Path, owner.user, owner.host)
		if !seen[msg] {
			seen[msg] = true
			_, _ = fmt.Fprintln(cfg.Writer, msg)
			found = true
		}
	}

	if !found {
		_, _ = fmt.Fprintln(cfg.Writer, "  (no current wait-for edges; no deadlock candidates)")
	} else {
		_, _ = fmt.Fprintln(cfg.Writer)
		_, _ = fmt.Fprintln(cfg.Writer, "Cycle detection deferred to v0.29; surface to humans for now.")
	}

	return &Result{Subcommand: "deadlock", Project: project, CoordDir: coordDir}, nil
}

// ---- subcommand: propose-mode -----------------------------------------------

func runProposeMode(cfg Config) (*Result, error) {
	mode := ""
	var targets []string
	reason := ""
	timeoutSecs := 60
	projectArg := ""
	args := cfg.Args

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--mode":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer propose-mode: --mode requires a value")
			}
			mode = args[i]
		case strings.HasPrefix(args[i], "--mode="):
			mode = args[i][len("--mode="):]
		case args[i] == "--targets":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer propose-mode: --targets requires a value")
			}
			targets = append(targets, args[i])
		case strings.HasPrefix(args[i], "--targets="):
			targets = append(targets, args[i][len("--targets="):])
		case args[i] == "--reason":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer propose-mode: --reason requires a value")
			}
			reason = args[i]
		case strings.HasPrefix(args[i], "--reason="):
			reason = args[i][len("--reason="):]
		case args[i] == "--timeout":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer propose-mode: --timeout requires a value")
			}
			_, _ = fmt.Sscanf(args[i], "%d", &timeoutSecs)
		case strings.HasPrefix(args[i], "--timeout="):
			_, _ = fmt.Sscanf(args[i][len("--timeout="):], "%d", &timeoutSecs)
		case strings.HasPrefix(args[i], "-"):
			return nil, fmt.Errorf("peer propose-mode: unknown flag %q", args[i])
		default:
			if projectArg == "" {
				projectArg = args[i]
			} else {
				return nil, fmt.Errorf("peer propose-mode: too many positional args")
			}
		}
	}

	if mode == "" {
		return nil, fmt.Errorf("peer propose-mode: --mode required (parallel|serialize|defer)")
	}
	switch mode {
	case "parallel", "serialize", "defer":
	default:
		return nil, fmt.Errorf("peer propose-mode: --mode must be parallel|serialize|defer (got %q)", mode)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("peer propose-mode: at least one --targets required")
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	now := cfg.now()
	ts := mailbox.FormatTS(now)
	proposalID := fmt.Sprintf("%d-%d-%d", now.Unix(), cfg.PID, now.Nanosecond()%100000)

	targetsJSON, _ := json.Marshal(targets)
	detail, _ := json.Marshal(map[string]interface{}{
		"proposal_id":   proposalID,
		"proposed_mode": mode,
		"targets":       json.RawMessage(targetsJSON),
		"reason":        reason,
	})
	e := mailbox.Event{
		Ts:   ts,
		Kind: "mode_proposal",
		Actor: mailbox.Actor{
			User:      cfg.User,
			Host:      cfg.Host,
			PID:       cfg.PID,
			SessionID: "operator",
			Agent:     "lead",
		},
		Detail: json.RawMessage(detail),
	}

	activityPath := filepath.Join(coordDir, "activity.ndjson")
	if err := mailbox.AppendEvent(activityPath, e); err != nil {
		return nil, fmt.Errorf("peer propose-mode: append activity: %w", err)
	}
	sessPath := filepath.Join(coordDir, "sessions",
		mailbox.SessionFileName(cfg.User, cfg.Host, cfg.PID))
	if err := mailbox.AppendEvent(sessPath, e); err != nil {
		return nil, fmt.Errorf("peer propose-mode: append session: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "proposed mode %q on targets [%s] — proposal_id=%s\n",
		mode, strings.Join(targets, " "), proposalID)
	_, _ = fmt.Fprintf(cfg.Writer, "waiting up to %ds for peer mode_response...\n", timeoutSecs)

	// Poll for a mode_response matching proposalID.
	deadline := now.Add(time.Duration(timeoutSecs) * time.Second)
	response := ""
	for cfg.now().Before(deadline) {
		response = findModeResponse(activityPath, proposalID)
		if response != "" {
			break
		}
		time.Sleep(1 * time.Second)
	}

	if response == "" {
		// Timeout: emit default_serialize response.
		ts2 := mailbox.FormatTS(cfg.now())
		timeoutDetail, _ := json.Marshal(map[string]interface{}{
			"proposal_id": proposalID,
			"response":    "default_serialize",
			"reason":      "peer did not respond within timeout",
			"timeout":     true,
		})
		te := mailbox.Event{
			Ts:   ts2,
			Kind: "mode_response",
			Actor: mailbox.Actor{
				User:      cfg.User,
				Host:      cfg.Host,
				PID:       cfg.PID,
				SessionID: "operator",
				Agent:     "lead",
			},
			Detail: json.RawMessage(timeoutDetail),
		}
		_ = mailbox.AppendEvent(activityPath, te)
		_ = mailbox.AppendEvent(sessPath, te)
		_, _ = fmt.Fprintf(cfg.Writer, "TIMEOUT: no peer response within %ds. Defaulting to serialize.\n", timeoutSecs)
		return &Result{Subcommand: "propose-mode", Project: project, CoordDir: coordDir, EventsEmitted: 2}, nil
	}

	var respObj struct {
		Detail struct {
			Response string `json:"response"`
			Reason   string `json:"reason"`
		} `json:"detail"`
		Actor struct {
			User string `json:"user"`
			Host string `json:"host"`
		} `json:"actor"`
	}
	if err := json.Unmarshal([]byte(response), &respObj); err == nil {
		peerWho := respObj.Actor.User + "@" + respObj.Actor.Host
		peerReason := respObj.Detail.Reason
		if peerReason == "" {
			peerReason = "(no reason)"
		}
		_, _ = fmt.Fprintf(cfg.Writer, "RESPONSE from %s: %s\n", peerWho, respObj.Detail.Response)
		_, _ = fmt.Fprintf(cfg.Writer, "reason: %s\n", peerReason)

		if respObj.Detail.Response == "reject" {
			_, _ = fmt.Fprintln(cfg.Writer, "STOP: peer rejected. Coordinate before retrying; don't silently override.")
			return nil, fmt.Errorf("peer propose-mode: peer rejected proposal")
		}
	}

	return &Result{Subcommand: "propose-mode", Project: project, CoordDir: coordDir, EventsEmitted: 2}, nil
}

// ---- subcommand: respond-mode -----------------------------------------------

func runRespondMode(cfg Config) (*Result, error) {
	to := ""
	ack := false
	reject := false
	reason := ""
	projectArg := ""
	args := cfg.Args

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--to":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer respond-mode: --to requires a value")
			}
			to = args[i]
		case strings.HasPrefix(args[i], "--to="):
			to = args[i][len("--to="):]
		case args[i] == "--ack":
			ack = true
		case args[i] == "--reject":
			reject = true
		case args[i] == "--reason":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer respond-mode: --reason requires a value")
			}
			reason = args[i]
		case strings.HasPrefix(args[i], "--reason="):
			reason = args[i][len("--reason="):]
		case strings.HasPrefix(args[i], "-"):
			return nil, fmt.Errorf("peer respond-mode: unknown flag %q", args[i])
		default:
			if projectArg == "" {
				projectArg = args[i]
			} else {
				return nil, fmt.Errorf("peer respond-mode: too many positional args")
			}
		}
	}

	if to == "" {
		return nil, fmt.Errorf("peer respond-mode: --to <proposal-id> required (get from 'yakos peer log')")
	}
	if !ack && !reject {
		return nil, fmt.Errorf("peer respond-mode: --ack or --reject required")
	}
	if ack && reject {
		return nil, fmt.Errorf("peer respond-mode: --ack and --reject are mutually exclusive")
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	response := "ack"
	if reject {
		response = "reject"
	}

	ts := mailbox.FormatTS(cfg.now())
	detail, _ := json.Marshal(map[string]interface{}{
		"proposal_id": to,
		"response":    response,
		"reason":      reason,
	})
	e := mailbox.Event{
		Ts:   ts,
		Kind: "mode_response",
		Actor: mailbox.Actor{
			User:      cfg.User,
			Host:      cfg.Host,
			PID:       cfg.PID,
			SessionID: "operator",
			Agent:     "lead",
		},
		Detail: json.RawMessage(detail),
	}

	if err := mailbox.AppendEvent(filepath.Join(coordDir, "activity.ndjson"), e); err != nil {
		return nil, fmt.Errorf("peer respond-mode: append activity: %w", err)
	}
	sessPath := filepath.Join(coordDir, "sessions",
		mailbox.SessionFileName(cfg.User, cfg.Host, cfg.PID))
	if err := mailbox.AppendEvent(sessPath, e); err != nil {
		return nil, fmt.Errorf("peer respond-mode: append session: %w", err)
	}

	_, _ = fmt.Fprintf(cfg.Writer, "responded %s to proposal %s\n", response, to)

	return &Result{Subcommand: "respond-mode", Project: project, CoordDir: coordDir, EventsEmitted: 2}, nil
}

// ---- subcommand: handoff ----------------------------------------------------

func runHandoff(cfg Config) (*Result, error) {
	to := ""
	completed := ""
	notes := ""
	nextAction := ""
	ack := ""
	rejectID := ""
	reason := ""
	projectArg := ""
	args := cfg.Args

	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--to":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer handoff: --to requires a value")
			}
			to = args[i]
		case strings.HasPrefix(args[i], "--to="):
			to = args[i][len("--to="):]
		case args[i] == "--completed-scope":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer handoff: --completed-scope requires a value")
			}
			completed = args[i]
		case strings.HasPrefix(args[i], "--completed-scope="):
			completed = args[i][len("--completed-scope="):]
		case args[i] == "--notes":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer handoff: --notes requires a value")
			}
			notes = args[i]
		case strings.HasPrefix(args[i], "--notes="):
			notes = args[i][len("--notes="):]
		case args[i] == "--next-action":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer handoff: --next-action requires a value")
			}
			nextAction = args[i]
		case strings.HasPrefix(args[i], "--next-action="):
			nextAction = args[i][len("--next-action="):]
		case args[i] == "--ack":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer handoff: --ack requires a handoff-id")
			}
			ack = args[i]
		case strings.HasPrefix(args[i], "--ack="):
			ack = args[i][len("--ack="):]
		case args[i] == "--reject":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer handoff: --reject requires a handoff-id")
			}
			rejectID = args[i]
		case strings.HasPrefix(args[i], "--reject="):
			rejectID = args[i][len("--reject="):]
		case args[i] == "--reason":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("peer handoff: --reason requires a value")
			}
			reason = args[i]
		case strings.HasPrefix(args[i], "--reason="):
			reason = args[i][len("--reason="):]
		case strings.HasPrefix(args[i], "-"):
			return nil, fmt.Errorf("peer handoff: unknown flag %q", args[i])
		default:
			if projectArg == "" {
				projectArg = args[i]
			} else {
				return nil, fmt.Errorf("peer handoff: too many positional args")
			}
		}
	}

	project, err := resolveProject(cfg, projectArg)
	if err != nil {
		return nil, err
	}
	coordDir, err := requireCoord(cfg, project)
	if err != nil {
		return nil, err
	}

	emitted := 0

	if ack != "" || rejectID != "" {
		target := ack
		response := "ack"
		if rejectID != "" {
			target = rejectID
			response = "reject"
		}
		ts := mailbox.FormatTS(cfg.now())
		detail, _ := json.Marshal(map[string]interface{}{
			"handoff_id": target,
			"response":   response,
			"reason":     reason,
		})
		e := mailbox.Event{
			Ts:   ts,
			Kind: "peer_handoff_response",
			Actor: mailbox.Actor{
				User:      cfg.User,
				Host:      cfg.Host,
				PID:       cfg.PID,
				SessionID: "operator",
				Agent:     "lead",
			},
			Detail: json.RawMessage(detail),
		}
		if err := mailbox.AppendEvent(filepath.Join(coordDir, "activity.ndjson"), e); err != nil {
			return nil, fmt.Errorf("peer handoff: append activity: %w", err)
		}
		emitted++
		sessPath := filepath.Join(coordDir, "sessions",
			mailbox.SessionFileName(cfg.User, cfg.Host, cfg.PID))
		if err := mailbox.AppendEvent(sessPath, e); err != nil {
			return nil, fmt.Errorf("peer handoff: append session: %w", err)
		}
		emitted++
		_, _ = fmt.Fprintf(cfg.Writer, "handoff %s: %s\n", response, target)
		return &Result{Subcommand: "handoff", Project: project, CoordDir: coordDir, EventsEmitted: emitted}, nil
	}

	if to == "" {
		return nil, fmt.Errorf("peer handoff: --to <user@host> required")
	}
	if completed == "" {
		return nil, fmt.Errorf("peer handoff: --completed-scope required")
	}

	now := cfg.now()
	ts := mailbox.FormatTS(now)
	handoffID := fmt.Sprintf("%d-%d-%d", now.Unix(), cfg.PID, now.Nanosecond()%100000)

	detail, _ := json.Marshal(map[string]interface{}{
		"handoff_id":      handoffID,
		"to":              to,
		"completed_scope": completed,
		"notes":           notes,
		"next_action":     nextAction,
	})
	e := mailbox.Event{
		Ts:   ts,
		Kind: "peer_handoff",
		Actor: mailbox.Actor{
			User:      cfg.User,
			Host:      cfg.Host,
			PID:       cfg.PID,
			SessionID: "operator",
			Agent:     "lead",
		},
		Detail: json.RawMessage(detail),
	}
	if err := mailbox.AppendEvent(filepath.Join(coordDir, "activity.ndjson"), e); err != nil {
		return nil, fmt.Errorf("peer handoff: append activity: %w", err)
	}
	emitted++
	sessPath := filepath.Join(coordDir, "sessions",
		mailbox.SessionFileName(cfg.User, cfg.Host, cfg.PID))
	if err := mailbox.AppendEvent(sessPath, e); err != nil {
		return nil, fmt.Errorf("peer handoff: append session: %w", err)
	}
	emitted++

	_, _ = fmt.Fprintf(cfg.Writer, "handoff sent to %s — handoff_id=%s\n", to, handoffID)
	_, _ = fmt.Fprintln(cfg.Writer, "remember to:")
	_, _ = fmt.Fprintln(cfg.Writer, "  - release any claims on the completed scope ('yakos peer release <file>')")
	_, _ = fmt.Fprintln(cfg.Writer, "  - update work/current/decisions.md with the handoff summary")

	return &Result{Subcommand: "handoff", Project: project, CoordDir: coordDir, EventsEmitted: emitted}, nil
}

// ---- project resolution -----------------------------------------------------

// resolveProject returns explicit if non-empty, otherwise infers from cfg's
// HomeDir using the agent-control walk matching bash infer_project().
func resolveProject(cfg Config, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	home := cfg.homeDir()
	acRoot := filepath.Join(home, "agent-control")

	cwd, _ := os.Getwd()
	cwd = cleanPath(cwd)

	// Case 1: cwd is under agent-control/<name>/
	if strings.HasPrefix(cwd, acRoot+"/") {
		rest := cwd[len(acRoot)+1:]
		name := rest
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[:i]
		}
		if name != "" {
			return name, nil
		}
	}

	// Case 2: scan .project-path files.
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return "", fmt.Errorf("peer: could not infer project from cwd; pass <name> explicitly")
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		p := cleanPath(strings.TrimSpace(string(data)))
		if cwd == p || strings.HasPrefix(cwd, p+"/") {
			return e.Name(), nil
		}
	}

	return "", fmt.Errorf("peer: could not infer project from cwd; pass <name> explicitly")
}

// requireCoord returns the coord directory for project, or an error with a
// descriptive advisory message when the directory is missing or not writable.
// The exit code semantics from bash are surfaced via error text convention:
// the caller may inspect the message but this package does not call os.Exit.
func requireCoord(cfg Config, project string) (string, error) {
	dir := cfg.coordDir(project)

	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf(
			"peer: coord not configured for %q\n"+
				"peer: expected directory: %s\n"+
				"peer: run 'yakos init --multi-dev %s --project <path>' to provision it\n"+
				"peer: see docs/co-pilot-mode.md for the dev-box admin recipe (groups, perms)",
			project, dir, project,
		)
	}

	// Check writability by attempting to open for writing.
	// We use O_WRONLY|O_EXCL on a temp path inside the dir.
	// Simpler: check that the directory permission bits include write for
	// the current user. We use the same approach as the bash source: just
	// attempt the open and handle the error gracefully.
	testFile := filepath.Join(dir, ".write-test-"+fmt.Sprint(os.Getpid()))
	f, werr := os.OpenFile(testFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if werr != nil {
		return "", fmt.Errorf(
			"peer: coord directory exists but is not writable by you:\n"+
				"peer:   %s\n"+
				"peer: ensure you are in the 'yakos-coord' group (or whoever owns the dir),\n"+
				"peer: then log out + back in (or 'newgrp yakos-coord') for the group\n"+
				"peer: membership to apply to your shell",
			dir,
		)
	}
	_ = f.Close()
	_ = os.Remove(testFile)

	return dir, nil
}

// ---- config helpers ---------------------------------------------------------

func (cfg Config) withDefaults() Config {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.User == "" {
		cfg.User = os.Getenv("USER")
		if cfg.User == "" {
			cfg.User = "unknown"
		}
	}
	if cfg.Host == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = "localhost"
		}
		// Match bash: `hostname -s` returns short hostname (first component).
		if i := strings.IndexByte(h, '.'); i > 0 {
			h = h[:i]
		}
		cfg.Host = h
	}
	if cfg.PID == 0 {
		cfg.PID = os.Getpid()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return cfg
}

func (cfg Config) homeDir() string {
	if cfg.HomeDir != "" {
		return cfg.HomeDir
	}
	h := os.Getenv("HOME")
	if h == "" {
		h = "/tmp"
	}
	return h
}

func (cfg Config) now() time.Time {
	if cfg.Now != nil {
		return cfg.Now()
	}
	return time.Now()
}

func (cfg Config) coordDir(project string) string {
	if cfg.CoordDirFn != nil {
		return cfg.CoordDirFn(project)
	}
	return fmt.Sprintf("/var/lib/yakos/%s/coord", project)
}

// ---- helpers ----------------------------------------------------------------

// extractEventFields pulls ts, kind, actor.agent from a raw NDJSON line.
// Returns "-" for any field that is absent or unparseable.
func extractEventFields(line string) (ts, kind, agent string) {
	ts, kind, agent = "-", "-", "-"
	var obj struct {
		Ts    string `json:"ts"`
		Kind  string `json:"kind"`
		Actor struct {
			Agent string `json:"agent"`
		} `json:"actor"`
	}
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return
	}
	if obj.Ts != "" {
		ts = obj.Ts
	}
	if obj.Kind != "" {
		kind = obj.Kind
	}
	if obj.Actor.Agent != "" {
		agent = obj.Actor.Agent
	}
	return
}

// findModeResponse scans the activity log for a mode_response event matching
// proposalID and returns the raw JSON line, or "" if not found.
func findModeResponse(activityPath, proposalID string) string {
	lines, _ := mailbox.ReadLines(activityPath)
	last := ""
	for _, l := range lines {
		var ev struct {
			Kind   string `json:"kind"`
			Detail struct {
				ProposalID string `json:"proposal_id"`
			} `json:"detail"`
		}
		if err := json.Unmarshal(l, &ev); err != nil {
			continue
		}
		if ev.Kind == "mode_response" && ev.Detail.ProposalID == proposalID {
			last = string(l)
		}
	}
	return last
}

// cleanPath returns filepath.Clean(p) or p if it's empty.
func cleanPath(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
