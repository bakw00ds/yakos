// Package telemetry provides opt-in, anonymised command telemetry for yakOS.
//
// # Privacy guarantees
//
//   - DEFAULT OFF. No data is recorded or transmitted without an explicit
//     `yakos telemetry enable` from the operator.
//   - No PII ever. Usernames, hostnames, file paths, project names, tool
//     inputs and outputs are never recorded. The only identity token is a
//     sha256-derived session_hash that is irreversible and changes each session.
//   - Local-only by default. Records are written to ~/.yakos-state/telemetry.ndjson
//     only. No network call is made unless the operator also sets an explicit
//     endpoint URL via `yakos telemetry set-endpoint`.
//   - Fail-silent. Any recording or shipping error is swallowed; the CLI must
//     never block or abort on a telemetry failure.
//
// # Schema
//
// Each telemetry record is one NDJSON line with the fields defined by Event.
// See the README.md in this package for the full schema and opt-in flow.
package telemetry

import "time"

// Event is the canonical telemetry record shape.  Every field that could
// identify the operator (usernames, hostnames, file paths, project names,
// tool inputs/outputs) is explicitly absent.
//
// JSON field names match the published schema in README.md exactly.
type Event struct {
	// TS is the UTC timestamp when the command was invoked.
	TS time.Time `json:"ts"`

	// YakosVersion is the value from the VERSION file plus " (go)" suffix.
	YakosVersion string `json:"yakos_version"`

	// OS is GOOS (e.g. "darwin", "linux", "windows").
	OS string `json:"os"`

	// Arch is GOARCH (e.g. "arm64", "amd64").
	Arch string `json:"arch"`

	// Command is the top-level subcommand (e.g. "validate", "kanban").
	// When the CLI is invoked with no subcommand this is "".
	Command string `json:"command"`

	// Subcommand is the second-level subcommand when applicable
	// (e.g. "lint" for `yakos hooks lint`), or "" when absent.
	Subcommand string `json:"subcommand"`

	// ExitCode is the process exit code (0 = success).
	ExitCode int `json:"exit_code"`

	// DurationMS is the wall-clock duration of the command in milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// AgentCount is the number of agents dispatched (0 for non-dispatch commands).
	AgentCount int `json:"agent_count"`

	// Runtime is the agent runtime used (e.g. "claude"), or null for non-dispatch.
	Runtime *string `json:"runtime"`

	// SessionHash is the sha256 of CLAUDE_SESSION_ID (or a random per-process
	// value when the env var is absent).  It is a 64-character hex string and
	// is irreversible — no username or identity can be derived from it.
	SessionHash string `json:"session_hash"`
}
