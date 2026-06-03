// Package status is the public library API for yakOS project status reporting.
//
// This package re-exports the stable types and functions from
// internal/status, presenting a clean public surface for use by IDE
// extensions, CI integrations, and alternative CLIs. The internal package
// retains its implementation details and is not importable outside this module.
//
// # Overview
//
// Status reads a project's agent-control directory and produces a structured
// report: session age, scratchpad size, file ages, hook outcomes, bypass
// count, mailbox count, and MEMORY.md presence.
//
// The package is read-only: it never writes to any state file.
//
// # Stability: experimental
//
// This package follows semantic versioning (same module: github.com/bakw00ds/yakos).
// It is pre-1.0; breaking changes are allowed at minor version bumps and
// documented in pkg/CHANGELOG.md. Target for stable v1.0.0: two minor
// releases after Phase 2 GA.
package status

import (
	"io"
	"time"

	internalstatus "github.com/bakw00ds/yakos/internal/status"
)

// Config controls what Status reads and where it looks.
//
// Project is the required agent-control project name (e.g. "myproject").
// HomeDir, Env, Now, Writer, and ErrWriter are optional and default to
// sensible live values when zero/nil.
type Config = internalstatus.Config

// HookEntry is a summary of one hook log file.
type HookEntry = internalstatus.HookEntry

// Report is the structured result returned by Run.
//
// Use Format to render it as the human-readable dashboard identical to
// bash status.sh output.
type Report = internalstatus.Report

// Run runs the full status query against a project's work directory.
//
// It returns a structured Report; use Format to render it.
//
// Errors:
//   - rarely returns a Go error; missing files produce appropriate "absent"
//     labels rather than errors (matching bash behavior)
//
// Example:
//
//	rpt, err := status.Run(status.Config{Project: "myproject"})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if err := status.Format(os.Stdout, rpt); err != nil {
//	    log.Fatal(err)
//	}
func Run(cfg Config) (*Report, error) {
	return internalstatus.Status(cfg)
}

// Format writes the dashboard report to w.
//
// The output is byte-identical to bash status.sh for the same project
// state. Each field is labeled and indented; warnings are appended at the
// end when session age > 4h or scratchpad > 100 MB.
//
// Errors:
//   - returns error on write failure
func Format(w io.Writer, rpt *Report) error {
	return internalstatus.Format(w, rpt)
}

// FormatTable is an alias for Format, emphasizing the tabular nature of
// the output for callers that build dashboards.
func FormatTable(w io.Writer, rpt *Report) error {
	return internalstatus.Format(w, rpt)
}

// NewConfig returns a Config with defaults populated for live use:
// HomeDir from $HOME, Now = time.Now(), Writer = os.Stdout,
// ErrWriter = os.Stderr.
//
// Callers typically override Project and leave everything else at defaults.
func NewConfig(project string) Config {
	return Config{
		Project: project,
		Now:     time.Time{}, // zero → time.Now() in Run
	}
}
