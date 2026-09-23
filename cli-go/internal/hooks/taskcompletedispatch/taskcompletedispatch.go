// Package taskcompletedispatch is the Go-native Tier-0 port of
// lib/hooks/task-complete-dispatch.sh.
//
// Status: REPORT-ONLY in v0.1 (matching bash original).
//
// Reason: Routing depends on agent_type being present in the TaskCompleted
// stdin JSON. Phase 0 Test 5 validated TaskCompleted hooks fire and can block,
// but did NOT dump the TaskCompleted JSON shape — the presence of agent_type is
// unverified. Dispatching to the wrong validator on a guessed-at field would
// silently mis-route. v0.1 logs the routing decision it WOULD make and exits 0.
//
// Per-domain validators in scripts/hooks/per-domain/ are themselves functional;
// a future BLOCKING v0.2 of this hook can invoke them once the schema is confirmed.
package taskcompletedispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hookio"
	"github.com/bakw00ds/yakos/internal/hooks/hooklog"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
)

const hookName = "task-complete-dispatch"

// domainRoutes maps agent role strings to per-domain validator slugs.
// Mirrors the bash case statement.
var domainRoutes = map[string]string{
	"go-api":        "backend",
	"backend":       "backend",
	"api":           "backend",
	"flutter-ui":    "mobile",
	"mobile":        "mobile",
	"ios":           "mobile",
	"nextjs":        "frontend",
	"web":           "frontend",
	"frontend":      "frontend",
	"db-migrations": "db-migration",
	"db":            "db-migration",
	"database":      "db-migration",
}

// Hook implements runner.Hook for task complete dispatch (report-only).
type Hook struct {
	// WorkCurrentDir is the absolute path to work/current/. Used for log writes.
	WorkCurrentDir string

	// HooksDirHint is the directory where per-domain validators live.
	// Typically lib/hooks/per-domain/. Used only to construct the would_run path
	// in log entries (nothing is actually executed in v0.1).
	HooksDirHint string

	// NowFn is injected for tests.
	NowFn func() time.Time
}

// New returns a Hook with sensible defaults.
func New(workCurrentDir string) *Hook {
	return &Hook{
		WorkCurrentDir: workCurrentDir,
		NowFn:          time.Now,
	}
}

// Name returns the canonical hook name.
func (h *Hook) Name() string { return hookName }

// Run executes the report-only task complete dispatch logic.
// Always returns ExitCode 0 in v0.1.
func (h *Hook) Run(_ context.Context, in hooktype.HookInput) (hooktype.HookOutput, error) {
	out := hooktype.HookOutput{ExitCode: 0}

	agentRole := senderRole(in)

	// Look up per-domain route. Case-sensitive, matching bash's `case
	// "$agent" in go-api|backend|api) ...` (no case-folding on either
	// side).
	domain := domainRoutes[agentRole]

	// Construct the would_run path (report-only; nothing executes).
	wouldRun := ""
	if domain != "" {
		hooksDir := h.HooksDirHint
		if hooksDir == "" {
			hooksDir = "lib/hooks/per-domain"
		}
		wouldRun = filepath.Join(hooksDir, domain+"-validate.sh")
	}

	// Bypass-aware (even though we don't actually run anything in v0.1).
	bypassActive := h.isBypassed(domain)

	now := time.Now()
	if h.NowFn != nil {
		now = h.NowFn()
	}
	err := hooklog.Append(h.WorkCurrentDir, hooklog.Entry{
		Hook:      hookName,
		Severity:  "REPORT",
		Decision:  "pass",
		Reason:    "report-only in v0.1 (UNCLEAR — see hook source)",
		Agent:     agentRole,
		SessionID: hookio.PayloadString(in, "session_id"),
		Event:     in.Event,
		Extra: map[string]any{
			"mode":          "report-only",
			"agent_type":    agentRole,
			"routed_domain": domain,
			"would_run":     wouldRun,
			// bash's --arg bypass "$bypass_active" always yields a JSON
			// STRING ("true"/"false"), not a boolean — ho_log's extra jq
			// object is built entirely from --arg-typed shell strings.
			"bypass_active": strconv.FormatBool(bypassActive),
		},
	}, now)
	if err != nil {
		out.Stderr = fmt.Appendf(out.Stderr, "%s: log: %v\n", hookName, err)
	}

	return out, nil
}

// isBypassed checks whether hook-bypass.md mentions this hook + domain.
func (h *Hook) isBypassed(domain string) bool {
	if h.WorkCurrentDir == "" || domain == "" {
		return false
	}
	bypassFile := filepath.Join(h.WorkCurrentDir, "hook-bypass.md")
	data, err := os.ReadFile(bypassFile) //nolint:gosec
	if err != nil {
		return false
	}
	content := string(data)
	return strings.Contains(content, hookName) && strings.Contains(content, domain)
}

// ---- helpers -----------------------------------------------------------------

// senderRole extracts the agent/role, matching hi_sender_role exactly:
// hi_field_or '.agent_type' 'lead' (top-level, fallback "lead" when
// absent/empty), trimmed, then the "yakos:" namespace prefix stripped.
func senderRole(in hooktype.HookInput) string {
	raw := hookio.PayloadString(in, "agent_type")
	if raw == "" {
		raw = "lead"
	}
	raw = strings.TrimSpace(raw)
	return strings.TrimPrefix(raw, "yakos:")
}
