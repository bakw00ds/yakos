// Package kanban is the public library API for yakOS kanban board management.
//
// This package re-exports the stable types and functions from
// internal/kanban, presenting a clean public surface for use by IDE
// extensions, CI integrations, and alternative CLIs. The internal package
// retains its implementation details and is not importable outside this module.
//
// # Overview
//
// A kanban board is a 3-column markdown file (kanban.md) with TODO /
// IN PROGRESS / DONE sections. Each task has an auto-assigned ID (K-N),
// title, category, assigned user, blockers, and notes.
//
// # Stability: experimental
//
// This package follows semantic versioning (same module: github.com/bakw00ds/yakos).
// It is pre-1.0; breaking changes are allowed at minor version bumps and
// documented in pkg/CHANGELOG.md. Target for stable v1.0.0: two minor
// releases after Phase 2 GA.
package kanban

import (
	"fmt"
	"io"
	"os"

	internalkanban "github.com/bakw00ds/yakos/internal/kanban"
)

// Column constants for the three kanban columns.
const (
	ColTODO       = internalkanban.ColTODO
	ColInProgress = internalkanban.ColInProgress
	ColDone       = internalkanban.ColDone
)

// Board is a parsed snapshot of a kanban.md file.
//
// RawLines holds the original file lines verbatim; the write path uses
// RawLines as its source of truth for byte-identical preservation.
// TODOItems, InProgressItems, and DoneItems hold parsed task titles.
//
// Errors:
//   - mutations return error when a task ID is not found
//   - Save returns error on filesystem write failures
type Board = internalkanban.Board

// Item is the parsed representation of a single kanban task.
//
// ID is the auto-assigned K-N identifier; Title is the free-form task
// description; Category and Notes are optional sub-fields.
type Item = internalkanban.Item

// Parse reads a kanban.md board from r.
//
// It is tolerant of whitespace variation and does not fail if a column is
// absent. Unknown headings are skipped. Only top-level bullet lines under
// a known column heading are collected into the typed fields; sub-bullets
// and prose are preserved verbatim in RawLines.
//
// Errors:
//   - returns scanner error on I/O failure
//
// Example:
//
//	f, err := os.Open("work/current/kanban.md")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer f.Close()
//	board, err := kanban.Parse(f)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(board.Summary()) // "TODO: 2  IN PROGRESS: 1  DONE: 5"
func Parse(r io.Reader) (*Board, error) {
	return internalkanban.Parse(r)
}

// Load opens the kanban.md at path and parses it.
//
// It is a convenience wrapper around os.Open + Parse for callers that
// don't need streaming I/O.
//
// Errors:
//   - returns error if path cannot be opened
//   - returns scanner error on I/O failure
//
// Example:
//
//	board, err := kanban.Load("work/current/kanban.md")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(board.Summary())
func Load(path string) (*Board, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("kanban.Load: %w", err)
	}
	defer func() { _ = f.Close() }()
	return internalkanban.Parse(f)
}

// Seed creates a new empty board with a default "session" title.
//
// Use SeedWithTitle to provide a custom title. The generated content
// matches bash _kanban_seed exactly.
func Seed() *Board {
	return internalkanban.Seed()
}

// SeedWithTitle creates a new empty board with the given title comment.
//
// The format matches bash _kanban_seed:
//
//	# Kanban — <title>
//
//	## TODO
//
//	## IN PROGRESS
//
//	## DONE
func SeedWithTitle(title string) *Board {
	return internalkanban.SeedWithTitle(title)
}

// NormalizeColumn normalizes user-supplied column names to the canonical form.
//
// Accepted inputs (case-insensitive): "todo", "in progress", "in-progress",
// "in_progress", "done". Returns error for unknown values.
//
// Errors:
//   - returns error for unrecognized column strings
func NormalizeColumn(s string) (string, error) {
	return internalkanban.NormalizeColumn(s)
}
