// Package refresh is the public library API for yakOS deployment drift detection
// and repair.
//
// This package re-exports the stable types and functions from
// internal/refresh, presenting a clean public surface for use by IDE
// extensions, CI integrations, and alternative CLIs. The internal package
// retains its implementation details and is not importable outside this module.
//
// # Overview
//
// Refresh detects and repairs three classes of per-project deployment drift:
//
//  1. Hook script drift   — project hook scripts out of date vs framework
//  2. settings.json drift — hook registrations missing or superseded
//  3. Agent symlink drift — ~/.claude/agents/<id>.md missing or wrong
//
// # Stability: experimental
//
// This package follows semantic versioning (same module: github.com/bakw00ds/yakos).
// It is pre-1.0; breaking changes are allowed at minor version bumps and
// documented in pkg/CHANGELOG.md. Target for stable v1.0.0: two minor
// releases after Phase 2 GA.
package refresh

import (
	internalrefresh "github.com/bakw00ds/yakos/internal/refresh"
)

// Config controls a refresh run.
//
// YakosRoot and ProjectPaths are required. DryRun, Writer, ErrWriter, and
// HomeDir are optional.
type Config = internalrefresh.Config

// HookPhaseReport holds per-phase counts for hook script sync.
type HookPhaseReport = internalrefresh.HookPhaseReport

// SettingsPhaseReport holds per-phase counts for settings.json smart merge.
type SettingsPhaseReport = internalrefresh.SettingsPhaseReport

// AgentPhaseReport holds per-phase counts for agent symlink sync.
type AgentPhaseReport = internalrefresh.AgentPhaseReport

// ProjectReport is the result for one project directory.
type ProjectReport = internalrefresh.ProjectReport

// Report is the overall result of a refresh run.
type Report = internalrefresh.Report

// Run executes the refresh for all projects in cfg and returns a structured
// Report.  Output is streamed to cfg.Writer as work proceeds.
//
// Errors:
//   - returns error if YakosRoot is empty
//   - returns error on I/O failure writing hook scripts or settings
//
// Example:
//
//	projects := refresh.CollectProjects("/home/user")
//	report, err := refresh.Run(refresh.Config{
//	    YakosRoot:    "/home/user/yakos",
//	    ProjectPaths: projects,
//	    DryRun:       true,
//	    Writer:       os.Stdout,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("refreshed %d project(s)\n", len(report.Projects))
func Run(cfg Config) (*Report, error) {
	return internalrefresh.Run(cfg)
}

// CollectProjects returns the list of project directories managed by yakOS
// under ~/agent-control/.  Each entry has a .project-path file pointing to
// the actual project repository.
//
// The returned paths are absolute.  An empty slice (and no error) is returned
// when no projects are configured.
//
// Errors:
//   - returns empty slice when ~/agent-control/ does not exist
//
// Example:
//
//	projects := refresh.CollectProjects("/home/user")
//	fmt.Printf("found %d project(s)\n", len(projects))
func CollectProjects(homeDir string) []string {
	return internalrefresh.CollectProjects(homeDir)
}
