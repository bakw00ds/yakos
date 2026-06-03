// Package agent is the public library API for yakOS agent management.
//
// This package re-exports the stable types and functions from
// internal/agent, presenting a clean public surface for use by IDE
// extensions, CI integrations, and alternative CLIs. The internal package
// retains its implementation details and is not importable outside this module.
//
// # Overview
//
// The agent package provides four sub-operations:
//
//   - new  — scaffold a new agent file with validated frontmatter
//   - lint — audit every agent file for missing or invalid frontmatter fields
//   - diff — show the body diff of an agent vs its parent template
//   - list — list resolved agents in the project
//
// # Stability: experimental
//
// This package follows semantic versioning (same module: github.com/bakw00ds/yakos).
// It is pre-1.0; breaking changes are allowed at minor version bumps and
// documented in pkg/CHANGELOG.md. Target for stable v1.0.0: two minor
// releases after Phase 2 GA.
package agent

import (
	"io"

	internalagent "github.com/bakw00ds/yakos/internal/agent"
)

// Config drives Run.
//
// Subcommand is required ("new", "lint", "diff", or "list").  All other
// fields are subcommand-specific.  See field-level documentation for defaults.
type Config = internalagent.Config

// Result is returned by Run.
//
// Errors and Warnings are only populated for the lint subcommand.
type Result = internalagent.Result

// Run dispatches to the appropriate sub-action based on cfg.Subcommand.
//
// Errors:
//   - returns error for unknown subcommands
//   - returns error when required fields (Name for new/diff) are missing
//   - returns error on I/O failure reading agent files
//
// Example:
//
//	result, err := agent.Run(agent.Config{
//	    Subcommand: "lint",
//	    YakosRoot:  "/home/user/yakos",
//	    Project:    "/home/user/myproject",
//	    Writer:     os.Stdout,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("errors=%d warnings=%d\n", result.Errors, result.Warnings)
func Run(cfg Config) (*Result, error) {
	return internalagent.Run(cfg)
}

// PrintHelp writes the agent command usage text to w.
//
// The output matches the bash `yakos agent --help` format.
//
// Errors:
//   - returns error on write failure
func PrintHelp(w io.Writer) {
	internalagent.PrintHelp(w)
}
