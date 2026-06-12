// Package peerclaim is the Go-native Tier-0 port of
// lib/hooks/peer-claim.sh.
//
// PreToolUse hook on Edit|Write|MultiEdit.
//
// Plan 1 M2 — co-pilot mode per-file claim enforcement.
//
// Gate logic:
//  1. If YAKOS_COORD_ENABLED is not set (or is "0"): exit 0 immediately.
//     This is the common single-dev case; this hook is a no-op for vanilla yakOS.
//  2. Resolve the target path to repo-relative form (claim keys are repo-relative).
//  3. Read active-claims.json (rebuild from activity.ndjson if missing).
//  4. If another session holds an unexpired claim → check hook-bypass.md.
//     If no bypass → block (exit 2).
//  5. Else (free or self-held) → emit a claim_intent or claim_renewed event
//     to the coord activity log.
//
// NOTE: This Go implementation deliberately does NOT fork a subprocess to run
// the coordinator. Coordination state (active-claims.json, activity.ndjson) is
// treated as a filesystem artifact.  Rebuilding the projection (step 3) is
// implemented in Go using the same fold logic as the bash hook.
//
// TTL defaults by file type:
//
//	SQL / migrations: 1800s (30 min)
//	scratchpad markdown (decisions/contracts/plan/status/findings): 120s
//	lock files (*.lock, *-lock.*, go.sum): 300s
//	everything else: 600s (10 min)
package peerclaim

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/mailbox"
)

// defaultStaleAfter is the default freshness threshold for coord state.
// Claims files older than this are considered stale and trigger a WARN.
const defaultStaleAfter = 24 * time.Hour

const hookName = "peer-claim"

// claimOwner is the owner block stored per path in active-claims.json.
type claimOwner struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	PID        int    `json:"pid"`
	SessionID  string `json:"session_id"`
	Agent      string `json:"agent"`
	Status     string `json:"status"` // "intent" or "confirmed"
	ClaimedAt  string `json:"claimed_at"`
	RenewedAt  string `json:"renewed_at"`
	ExpiresAt  string `json:"expires_at"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// claimEntry is a single entry in active-claims.json.
type claimEntry struct {
	Path   string       `json:"path"`
	Owners []claimOwner `json:"owners"`
}

// activeClaims is the shape of active-claims.json.
type activeClaims struct {
	GeneratedAt string       `json:"generated_at"`
	Claims      []claimEntry `json:"claims"`
}

// activityEvent is a partial parse of an activity.ndjson line.
type activityEvent struct {
	Ts     string          `json:"ts"`
	Kind   string          `json:"kind"`
	Actor  claimOwner      `json:"actor"`
	Detail json.RawMessage `json:"detail"`
}

// claimDetail is the payload inside an event's Detail.
type claimDetail struct {
	Path       string `json:"path"`
	TTLSeconds int    `json:"ttl_seconds"`
	ExpiresAt  string `json:"expires_at"`
	ViaBypass  bool   `json:"via_bypass,omitempty"`
}

// Hook implements runner.Hook for peer claim enforcement.
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/ for the session.
	WorkCurrentDir string

	// ProjectDir is used to resolve relative file paths.
	ProjectDir string

	// CoordDirFn returns the coord directory path given the project name.
	// When nil, the default /var/lib/yakos/<project>/coord/ is used.
	CoordDirFn func(project string) string

	// NowFn is injected for tests.
	NowFn func() time.Time

	// StatFn is injected for tests to override os.Stat on the claims file.
	// When nil, os.Stat is used.
	StatFn func(path string) (os.FileInfo, error)

	// IsProcessRunningFn is injected for tests to override live-PID detection.
	// When nil, the default OS-level check is used.
	IsProcessRunningFn func(pid int) bool

	// User/Host/PID identify this session (injected for tests).
	User string
	Host string
	PID  int
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir, projectDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		ProjectDir:     projectDir,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the peer-claim logic.
// Returns ExitCode=2 (block) when another session holds an unexpired claim.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	// Only act on write tools.
	switch in.Tool {
	case "Edit", "Write", "MultiEdit":
	default:
		return out, nil
	}

	// No-op when coord isn't enabled.
	if in.Env["YAKOS_COORD_ENABLED"] != "1" {
		return out, nil
	}

	filePath := stringField(in.Payload, "path")
	if filePath == "" {
		filePath = stringField(in.Payload, "file_path")
	}
	if filePath == "" {
		return out, nil
	}

	// Repo-relative path.
	relFile := h.toRelative(filePath, in)
	ttl := ttlForFile(relFile)

	now := h.NowFn()
	nowISO := mailbox.FormatTS(now)
	expISO := mailbox.FormatExpiry(now, ttl)

	// Caller identity.
	user := h.resolveUser(in)
	host := h.resolveHost(in)
	pid := h.resolvePID(in)
	agent := senderRole(in)

	coordDir := h.resolveCoordDir(in)
	claimsFile := filepath.Join(coordDir, "active-claims.json")
	activityLog := filepath.Join(coordDir, "activity.ndjson")

	// Rebuild projection if missing.
	if _, err := os.Stat(claimsFile); os.IsNotExist(err) {
		rebuildClaims(claimsFile, activityLog, nowISO)
	}

	// Smart-degrade: if coord state IS present, check for staleness.
	// A stale state emits a single WARN line but does not block (exit 0).
	h.checkStaleCoordState(&out, claimsFile, now, in)

	// Look up existing claim.
	existing := lookupClaim(claimsFile, relFile, nowISO)
	isMe := existing != nil && existing.User == user && existing.Host == host && existing.PID == pid

	if existing != nil && !isMe {
		// Conflict: another session holds this file.
		bypassScope := fmt.Sprintf("file=%s peer=%s@%s", relFile, existing.User, existing.Host)
		if h.isBypassed(bypassScope) {
			// Emit claim_intent with via_bypass.
			h.emitEvent(activityLog, "claim_intent", relFile, ttl, expISO, true,
				user, host, pid, agent, in.Env["CLAUDE_SESSION_ID"], nowISO)
			h.appendLog(&out, "WARN", "pass", "peer-held but bypass active",
				map[string]any{"file": relFile, "bypass": true})
			return out, nil
		}

		// Block.
		h.appendLog(&out, "BLOCK", "block", "peer holds claim",
			map[string]any{"file": relFile,
				"peer_user":  existing.User,
				"peer_host":  existing.Host,
				"peer_agent": existing.Agent})
		msg := fmt.Sprintf(
			"file '%s' is claimed by %s@%s (agent: %s, expires: %s).\n"+
				"       Options:\n"+
				"         1. wait for the claim to expire or be released\n"+
				"         2. coordinate via 'yakos peer log' + a SendMessage\n"+
				"         3. bypass: add an entry to work/current/hook-bypass.md with\n"+
				"            Hook: peer-claim\n"+
				"            Scope: %s",
			relFile, existing.User, existing.Host, existing.Agent, existing.ExpiresAt, bypassScope)
		out.Stderr = append(out.Stderr, []byte(msg+"\n")...)
		out.ExitCode = 2
		return out, nil
	}

	// Free or self-held: post claim_intent or claim_renewed.
	eventKind := "claim_intent"
	if isMe {
		eventKind = "claim_renewed"
	}
	h.emitEvent(activityLog, eventKind, relFile, ttl, expISO, false,
		user, host, pid, agent, in.Env["CLAUDE_SESSION_ID"], nowISO)

	h.appendLog(&out, "REPORT", "pass", "claim posted",
		map[string]any{"file": relFile, "ttl_seconds": ttl, "event": eventKind})
	return out, nil
}

// ---- TTL resolution ---------------------------------------------------------

func ttlForFile(relFile string) int {
	base := filepath.Base(relFile)
	switch {
	case strings.HasSuffix(relFile, ".sql") || strings.Contains(relFile, "/migrations/"):
		return 1800
	case base == "decisions.md" || base == "contracts.md" || base == "plan.md" ||
		base == "status.md" || base == "findings.md":
		return 120
	case strings.HasSuffix(relFile, ".lock") ||
		strings.Contains(relFile, "-lock.") ||
		base == "go.sum":
		return 300
	default:
		return 600
	}
}

// ---- claims projection -------------------------------------------------------

func lookupClaim(claimsFile, relFile, nowISO string) *claimOwner {
	data, err := os.ReadFile(claimsFile) //nolint:gosec
	if err != nil {
		return nil
	}
	var claims activeClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil
	}
	for _, entry := range claims.Claims {
		if entry.Path != relFile {
			continue
		}
		for i := range entry.Owners {
			o := &entry.Owners[i]
			if o.ExpiresAt > nowISO {
				return o
			}
		}
	}
	return nil
}

// rebuildClaims folds the activity log into a claims projection.
// Mirrors the bash hook's self-healing rebuild logic.
func rebuildClaims(claimsFile, activityLog, nowISO string) {
	data, err := os.ReadFile(activityLog) //nolint:gosec
	if err != nil {
		// No activity log: write an empty claims file.
		empty, _ := json.Marshal(activeClaims{GeneratedAt: nowISO, Claims: []claimEntry{}})
		_ = mailbox.WriteFile(claimsFile, append(empty, '\n'))
		return
	}

	// Parse all activity events.
	claimsMap := map[string]*claimEntry{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev activityEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		switch ev.Kind {
		case "team_deleted", "session_ended":
			// Drop all claims by this actor.
			for path := range claimsMap {
				entry := claimsMap[path]
				newOwners := entry.Owners[:0]
				for _, o := range entry.Owners {
					if o.User == ev.Actor.User && o.Host == ev.Actor.Host && o.PID == ev.Actor.PID {
						continue
					}
					newOwners = append(newOwners, o)
				}
				if len(newOwners) == 0 {
					delete(claimsMap, path)
				} else {
					entry.Owners = newOwners
				}
			}
		case "claim_released":
			var d claimDetail
			if err := json.Unmarshal(ev.Detail, &d); err == nil {
				delete(claimsMap, d.Path)
			}
		case "claim_intent", "claim_confirmed", "claim_renewed":
			var d claimDetail
			if err := json.Unmarshal(ev.Detail, &d); err != nil {
				continue
			}
			status := "intent"
			if ev.Kind == "claim_confirmed" {
				status = "confirmed"
			}
			claimsMap[d.Path] = &claimEntry{
				Path: d.Path,
				Owners: []claimOwner{{
					User:       ev.Actor.User,
					Host:       ev.Actor.Host,
					PID:        ev.Actor.PID,
					SessionID:  ev.Actor.SessionID,
					Agent:      ev.Actor.Agent,
					Status:     status,
					ClaimedAt:  ev.Ts,
					RenewedAt:  ev.Ts,
					ExpiresAt:  d.ExpiresAt,
					TTLSeconds: d.TTLSeconds,
				}},
			}
		}
	}

	// Drop expired.
	for path, entry := range claimsMap {
		valid := false
		for _, o := range entry.Owners {
			if o.ExpiresAt > nowISO {
				valid = true
				break
			}
		}
		if !valid {
			delete(claimsMap, path)
		}
	}

	// Build output.
	var claimsList []claimEntry
	for _, entry := range claimsMap {
		claimsList = append(claimsList, *entry)
	}
	out := activeClaims{GeneratedAt: nowISO, Claims: claimsList}
	outData, _ := json.Marshal(out)
	_ = mailbox.WriteFile(claimsFile, append(outData, '\n'))
}

// ---- event emission ---------------------------------------------------------

func (h *Hook) emitEvent(activityLog, kind, relFile string, ttl int, expiresAt string, viaBypass bool,
	user, host string, pid int, agent, sessionID, ts string) {
	if activityLog == "" {
		return
	}
	detail := claimDetail{
		Path:       relFile,
		TTLSeconds: ttl,
		ExpiresAt:  expiresAt,
		ViaBypass:  viaBypass,
	}
	detailBytes, err := json.Marshal(detail)
	if err != nil {
		return
	}
	event := mailbox.Event{
		Ts:   ts,
		Kind: kind,
		Actor: mailbox.Actor{
			User:      user,
			Host:      host,
			PID:       pid,
			SessionID: sessionID,
			Agent:     agent,
		},
		Detail: detailBytes,
	}
	_ = mailbox.AppendEvent(activityLog, event)
}

// ---- bypass check -----------------------------------------------------------

func (h *Hook) isBypassed(scope string) bool {
	if h.WorkCurrentDir == "" {
		return false
	}
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, hookName) && strings.Contains(content, scope)
}

// ---- resolution helpers -----------------------------------------------------

func (h *Hook) resolveCoordDir(in hooktype.HookInput) string {
	if h.CoordDirFn != nil {
		proj := in.Env["YAKOS_PROJECT_NAME"]
		if proj == "" {
			proj = "unknown"
		}
		return h.CoordDirFn(proj)
	}
	if d := in.Env["YAKOS_COORD_DIR"]; d != "" {
		return d
	}
	proj := in.Env["YAKOS_PROJECT_NAME"]
	if proj == "" {
		proj = "unknown"
	}
	return filepath.Join("/var/lib/yakos", proj, "coord")
}

func (h *Hook) toRelative(filePath string, in hooktype.HookInput) string {
	projectDir := h.ProjectDir
	if projectDir == "" {
		projectDir = in.Env["CLAUDE_PROJECT_DIR"]
	}
	// Normalise both paths to forward slashes so the result is a portable
	// claim key regardless of OS. Claim records are always written with "/"
	// separators; matching must use the same form.
	fileSlash := filepath.ToSlash(filePath)
	projSlash := filepath.ToSlash(projectDir)
	if projSlash != "" && strings.HasPrefix(fileSlash, projSlash+"/") {
		return fileSlash[len(projSlash)+1:]
	}
	return fileSlash
}

func (h *Hook) resolveUser(in hooktype.HookInput) string {
	if h.User != "" {
		return h.User
	}
	if u := in.Env["USER"]; u != "" {
		return u
	}
	return "unknown"
}

func (h *Hook) resolveHost(in hooktype.HookInput) string {
	if h.Host != "" {
		return h.Host
	}
	if hv := in.Env["HOSTNAME"]; hv != "" {
		return hv
	}
	return "unknown"
}

func (h *Hook) resolvePID(in hooktype.HookInput) int {
	if h.PID != 0 {
		return h.PID
	}
	return os.Getpid()
}

func senderRole(in hooktype.HookInput) string {
	if r := in.Env["YAKOS_AGENT_ROLE"]; r != "" {
		return r
	}
	return "lead"
}

func stringField(payload map[string]any, key string) string {
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ---- log helper -------------------------------------------------------------

func (h *Hook) appendLog(out *hooktype.HookOutput, severity, action, message string, extra map[string]any) {
	if h.WorkCurrentDir == "" {
		return
	}
	logFile := filepath.Join(h.WorkCurrentDir, "logs", hookName+".ndjson")
	ts := h.NowFn().UTC().Format(time.RFC3339)
	entry := map[string]any{
		"ts":       ts,
		"hook":     hookName,
		"severity": severity,
		"action":   action,
		"message":  message,
	}
	for k, v := range extra {
		entry[k] = v
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil { //nolint:gosec
		return
	}
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: open log: %v\n", hookName, err)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(data)
}

// ---- stale coord-state detection --------------------------------------------

// staleAfterDuration parses the YAKOS_PEER_STALE_AFTER env var (seconds) and
// returns the threshold.  Falls back to defaultStaleAfter when the env var is
// absent or unparseable.
func staleAfterDuration(in hooktype.HookInput) time.Duration {
	if raw := in.Env["YAKOS_PEER_STALE_AFTER"]; raw != "" {
		if secs, err := strconv.ParseInt(raw, 10, 64); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultStaleAfter
}

// checkStaleCoordState examines the active-claims file and emits a WARN line
// to Stderr (without blocking) when the coord state is stale.  Staleness is
// defined as either:
//   - the claims file's modification time is older than the stale threshold, OR
//   - at least one active claim's owner PID is not a running process.
//
// The hook continues to exit 0 in both cases — the intent is to surface a
// maintenance hint, not to block agent work.
func (h *Hook) checkStaleCoordState(out *hooktype.HookOutput, claimsFile string, now time.Time, in hooktype.HookInput) {
	statFn := h.StatFn
	if statFn == nil {
		statFn = os.Stat
	}

	fi, err := statFn(claimsFile)
	if err != nil {
		// File absent or unreadable — not a stale-detection case.
		return
	}

	threshold := staleAfterDuration(in)
	if now.Sub(fi.ModTime()) > threshold {
		msg := fmt.Sprintf("hook detected stale peer coord state (last modified %s ago, threshold %s); consider yakos peer cleanup",
			now.Sub(fi.ModTime()).Round(time.Minute),
			threshold.Round(time.Minute))
		out.Stderr = fmt.Appendf(out.Stderr, "WARN [peer-claim]: %s\n", msg)
		return
	}

	// File is fresh — check whether any active claim's owner PID is gone.
	data, err := os.ReadFile(claimsFile) //nolint:gosec
	if err != nil {
		return
	}
	var claims activeClaims
	if err := json.Unmarshal(data, &claims); err != nil {
		return
	}

	isRunning := h.IsProcessRunningFn
	if isRunning == nil {
		isRunning = isProcessRunning
	}

	nowISO := mailbox.FormatTS(now)
	for _, entry := range claims.Claims {
		for _, owner := range entry.Owners {
			if owner.ExpiresAt <= nowISO {
				continue // already expired; skip
			}
			if owner.PID > 0 && !isRunning(owner.PID) {
				msg := fmt.Sprintf("hook detected stale peer coord state from PID %d (not running); consider yakos peer cleanup",
					owner.PID)
				out.Stderr = fmt.Appendf(out.Stderr, "WARN [peer-claim]: %s\n", msg)
				return
			}
		}
	}
}
