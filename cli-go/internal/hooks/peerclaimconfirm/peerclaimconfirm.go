// Package peerclaimconfirm is the Go-native Tier-0 port of
// lib/hooks/peer-claim-confirm.sh.
//
// PostToolUse hook on Edit|Write|MultiEdit — companion to peerclaim.
//
// When the edit succeeds, this hook:
//  1. Emits a claim_confirmed event for the file to the coord activity log.
//  2. Rebuilds active-claims.json atomically (temp-rename, Q8) from the
//     activity log — this is the canonical writer.
//
// No-op when YAKOS_COORD_ENABLED is not "1". Always exits 0. This hook is
// telemetry, not policy. The PreToolUse gate (peerclaim) is where blocking
// happens; by the time we run, the edit has already landed.
package peerclaimconfirm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/mailbox"
)

const hookName = "peer-claim-confirm"

// Hook implements runner.Hook for peer claim confirmation.
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

// Run executes the peer-claim-confirm logic. Always returns ExitCode 0.
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

	relFile := h.toRelative(filePath, in)
	ttl := ttlForFile(relFile)

	now := h.NowFn()
	nowISO := mailbox.FormatTS(now)
	expISO := mailbox.FormatExpiry(now, ttl)

	user := h.resolveUser(in)
	host := h.resolveHost(in)
	pid := h.resolvePID(in)
	agent := senderRole(in)
	sessionID := in.Env["CLAUDE_SESSION_ID"]

	coordDir := h.resolveCoordDir(in)
	activityLog := filepath.Join(coordDir, "activity.ndjson")
	claimsFile := filepath.Join(coordDir, "active-claims.json")

	// Emit the confirmation event.
	detail := map[string]any{
		"path":        relFile,
		"ttl_seconds": ttl,
		"expires_at":  expISO,
	}
	detailBytes, _ := json.Marshal(detail)
	event := mailbox.Event{
		Ts:   nowISO,
		Kind: "claim_confirmed",
		Actor: mailbox.Actor{
			User:      user,
			Host:      host,
			PID:       pid,
			SessionID: sessionID,
			Agent:     agent,
		},
		Detail: detailBytes,
	}
	// Best-effort; confirmation is telemetry.
	if appendErr := mailbox.AppendEvent(activityLog, event); appendErr != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: append event: %v\n", hookName, appendErr)
	}

	// Rebuild active-claims.json atomically.
	rebuildClaims(claimsFile, activityLog, nowISO)

	h.appendLog(&out, "REPORT", "pass", "claim confirmed",
		map[string]any{"file": relFile, "ttl_seconds": ttl})
	return out, nil
}

// ---- rebuild projection (canonical writer) ----------------------------------

// activityEvent is a partial parse of an activity.ndjson line.
type activityEvent struct {
	Ts     string          `json:"ts"`
	Kind   string          `json:"kind"`
	Actor  actorShape      `json:"actor"`
	Detail json.RawMessage `json:"detail"`
}

type actorShape struct {
	User      string `json:"user"`
	Host      string `json:"host"`
	PID       int    `json:"pid"`
	SessionID string `json:"session_id"`
	Agent     string `json:"agent"`
}

type claimDetail struct {
	Path       string `json:"path"`
	TTLSeconds int    `json:"ttl_seconds"`
	ExpiresAt  string `json:"expires_at"`
}

type claimOwner struct {
	User       string `json:"user"`
	Host       string `json:"host"`
	PID        int    `json:"pid"`
	SessionID  string `json:"session_id"`
	Agent      string `json:"agent"`
	Status     string `json:"status"`
	ClaimedAt  string `json:"claimed_at"`
	RenewedAt  string `json:"renewed_at"`
	ExpiresAt  string `json:"expires_at"`
	TTLSeconds int    `json:"ttl_seconds"`
}

type claimEntry struct {
	Path   string       `json:"path"`
	Owners []claimOwner `json:"owners"`
}

type activeClaims struct {
	GeneratedAt string       `json:"generated_at"`
	Claims      []claimEntry `json:"claims"`
}

// rebuildClaims folds the activity log into a projection and writes it
// atomically to claimsFile.
func rebuildClaims(claimsFile, activityLog, nowISO string) {
	data, err := os.ReadFile(activityLog) //nolint:gosec
	if err != nil {
		return
	}

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

	var claimsList []claimEntry
	for _, entry := range claimsMap {
		claimsList = append(claimsList, *entry)
	}
	out := activeClaims{GeneratedAt: nowISO, Claims: claimsList}
	outData, _ := json.Marshal(out)
	_ = mailbox.WriteFile(claimsFile, append(outData, '\n'))
}

// ---- TTL resolution (matches peerclaim) -------------------------------------

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
	if projectDir != "" && strings.HasPrefix(filePath, projectDir+"/") {
		return filePath[len(projectDir)+1:]
	}
	return filePath
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
