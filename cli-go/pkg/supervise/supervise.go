// Package supervise is the public library API for yakOS supervisor management.
//
// This package re-exports the stable types and functions from
// internal/supervise, presenting a clean public surface for use by IDE
// extensions, CI integrations, and alternative CLIs. The internal package
// retains its implementation details and is not importable outside this module.
//
// # Overview
//
// The supervisor manages the live shadow-agent scoring loop — dispatching a
// supervisor agent every N tool calls and writing findings to
// work/current/supervisor-findings.ndjson.  This package exposes:
//
//   - Run — execute any supervisor subcommand (enable/disable/status/tail/
//     clear/set/pending/ack/ack-all)
//   - The Config, Result, Finding, and AckRecord types
//   - PrintHelp — write the help text
//
// # Typical use
//
// IDE extensions typically call Run with Subcommand="pending" to show
// unacknowledged findings in a sidebar, and with Subcommand="ack" to let
// the operator dismiss them without leaving the editor.
//
// # Stability: experimental
//
// This package follows semantic versioning (same module: github.com/bakw00ds/yakos).
// It is pre-1.0; breaking changes are allowed at minor version bumps and
// documented in pkg/CHANGELOG.md. Target for stable v1.0.0: two minor
// releases after Phase 2 GA.
package supervise

import (
	"io"

	internalsupervise "github.com/bakw00ds/yakos/internal/supervise"
)

// Config carries everything Run needs.
//
// Subcommand is required.  All other fields are optional with sensible
// defaults (see field-level documentation).
type Config = internalsupervise.Config

// Result summarises what Run did.
type Result = internalsupervise.Result

// Finding is a parsed row from supervisor-findings.ndjson.
type Finding = internalsupervise.Finding

// AckRecord is a single row in supervisor-acks.ndjson.
type AckRecord = internalsupervise.AckRecord

// Run executes the supervise subcommand according to cfg.
//
// Subcommand is one of: enable, disable, status, tail, clear, set, pending,
// ack, ack-all.  Project is the project slug; when empty it is inferred from
// cwd.
//
// Errors:
//   - returns error for unknown subcommands
//   - returns error when project cannot be inferred from cwd
//   - returns error on I/O failure reading/writing state files
//
// Example:
//
//	result, err := supervise.Run(supervise.Config{
//	    Subcommand: "pending",
//	    Project:    "myproject",
//	    Writer:     os.Stdout,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("%d unacknowledged finding(s)\n", result.PendingCount)
func Run(cfg Config) (*Result, error) {
	return internalsupervise.Run(cfg)
}

// PrintHelp writes the supervisor help text to w.
//
// Errors:
//   - returns error on write failure
func PrintHelp(w io.Writer) {
	internalsupervise.PrintHelp(w)
}
