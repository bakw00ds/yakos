package kanban

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- fixture boards ----------------------------------------------------------

// fixture boards are raw kanban.md content used for round-trip tests.
// Every fixture must pass: parse → mutate-noop → write → content == original.

const fixtureEmpty = `## TODO

## IN PROGRESS

## DONE
`

const fixtureTODOOnly = `## TODO
- Implement feature X
- Write tests for Y
- Review PR #42

## IN PROGRESS

## DONE
`

const fixtureInProgressMixed = `## TODO
- Follow-up on doc review

## IN PROGRESS
- Port status subcommand (rank 4)

## DONE
- Port validate subcommand (rank 2)
- Port cost subcommand (rank 3)
`

const fixtureWithTaskBlocks = `# Kanban — myproject

## TODO
- [ ] K-1 — fix login bug
  - category: bug
  - assigned: unassigned
  - blockers: none
  - notes: repro on Firefox only

## IN PROGRESS
- [ ] K-2 — add dashboard
  - category: feature
  - assigned: alice
  - blockers: none
  - notes:

## DONE
- [ ] K-3 — update docs
  - category: chore
  - assigned: unassigned
  - blockers: none
  - notes: done
`

const fixtureMultiTaskTODO = `# Kanban — project

## TODO
- [ ] K-1 — first task
  - category: feature
  - assigned: unassigned
  - blockers: none
  - notes:
- [ ] K-2 — second task
  - category: bug
  - assigned: unassigned
  - blockers: none
  - notes: some note

## IN PROGRESS

## DONE
`

const fixtureWithBlockers = `## TODO
- [ ] K-1 — blocked task
  - category: feature
  - assigned: unassigned
  - blockers: waiting on API contract
  - notes:

## IN PROGRESS

## DONE
`

const fixtureOperatorWhitespace = `# Kanban — with whitespace

## TODO

- [ ] K-1 — task with blank line before
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE

`

const fixtureNoTrailingNewline = `## TODO
- task A

## IN PROGRESS

## DONE`

// fixtureSeeded matches what bash _kanban_seed produces (without dynamic title/timestamp).
const fixtureSeeded = `# Kanban — session

## TODO

## IN PROGRESS

## DONE
`

const fixtureHighIDCounters = `## TODO
- [ ] K-10 — first
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:
- [ ] K-9 — second
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
- [ ] K-11 — done task
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:
`

const fixtureFreeformBullets = `## TODO
- simple task without id
- another freeform item

## IN PROGRESS
- working on something

## DONE
`

const fixtureNotesWithSpecialChars = `## TODO
- [ ] K-1 — task with special chars
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes: see PR #123 (important!)

## IN PROGRESS

## DONE
`

const fixtureMultipleColumns = `# Kanban — full board

## TODO
- [ ] K-1 — design phase
  - category: feature
  - assigned: unassigned
  - blockers: none
  - notes:
- [ ] K-2 — write tests
  - category: chore
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS
- [ ] K-3 — implement auth
  - category: feature
  - assigned: bob
  - blockers: none
  - notes: in review

## DONE
- [ ] K-4 — initial setup
  - category: chore
  - assigned: unassigned
  - blockers: none
  - notes:
- [ ] K-5 — requirements doc
  - category: chore
  - assigned: unassigned
  - blockers: none
  - notes:
`

const fixtureCommentLines = `# Kanban — project with comments
<!-- This is a comment that should be preserved -->

## TODO
- [ ] K-1 — task
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`

const fixtureUnknownHeading = `## TODO
- task A

## BACKLOG
- future item

## IN PROGRESS

## DONE
`

const fixtureDoneAtTop = `## DONE
- [ ] K-1 — already done
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## TODO
- [ ] K-2 — to do
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS
`

const fixtureEmptyNotes = `## TODO
- [ ] K-1 — task
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`

// ---- round-trip tests --------------------------------------------------------

// TestRoundTrip verifies parse → (noop) → write == original for every fixture.
// This is the load-bearing parity contract: kanban.md after a noop mutation
// must be byte-for-byte identical to the input.
func TestRoundTrip(t *testing.T) {
	fixtures := []struct {
		name    string
		content string
	}{
		{"empty", fixtureEmpty},
		{"todo-only", fixtureTODOOnly},
		{"in-progress-mixed", fixtureInProgressMixed},
		{"with-task-blocks", fixtureWithTaskBlocks},
		{"multi-task-todo", fixtureMultiTaskTODO},
		{"with-blockers", fixtureWithBlockers},
		{"operator-whitespace", fixtureOperatorWhitespace},
		{"high-id-counters", fixtureHighIDCounters},
		{"freeform-bullets", fixtureFreeformBullets},
		{"notes-special-chars", fixtureNotesWithSpecialChars},
		{"multiple-columns", fixtureMultipleColumns},
		{"comment-lines", fixtureCommentLines},
		{"unknown-heading", fixtureUnknownHeading},
		{"done-at-top", fixtureDoneAtTop},
		{"empty-notes", fixtureEmptyNotes},
		{"seeded", fixtureSeeded},
	}

	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			// Parse the fixture.
			b, err := Parse(strings.NewReader(fx.content))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			// Write to a temp file.
			dir := t.TempDir()
			path := filepath.Join(dir, "kanban.md")
			if err := b.Save(path); err != nil {
				t.Fatalf("Save: %v", err)
			}

			// Read back.
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}

			// The round-trip content must equal the original, byte for byte.
			// Note: fixtureNoTrailingNewline doesn't end with \n, but Save always
			// appends one trailing newline; we handle that fixture separately below.
			if string(got) != fx.content {
				t.Errorf("round-trip mismatch:\n  want: %q\n  got:  %q", fx.content, string(got))
			}
		})
	}
}

// TestRoundTripNoTrailingNewline verifies that Save adds exactly one trailing
// newline to a board that doesn't have one.
func TestRoundTripNoTrailingNewline(t *testing.T) {
	b, err := Parse(strings.NewReader(fixtureNoTrailingNewline))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := fixtureNoTrailingNewline + "\n"
	if string(got) != want {
		t.Errorf("want trailing newline added:\n  want: %q\n  got:  %q", want, string(got))
	}
}

// ---- Seed tests --------------------------------------------------------------

func TestSeedMatchesExpected(t *testing.T) {
	b := Seed()
	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != fixtureSeeded {
		t.Errorf("Seed output mismatch:\n  want: %q\n  got:  %q", fixtureSeeded, string(got))
	}
}

// ---- Parse typed fields tests -----------------------------------------------

func TestParseTypedFields(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureInProgressMixed))
	if len(b.TODOItems) != 1 {
		t.Errorf("TODOItems: want 1, got %d", len(b.TODOItems))
	}
	if len(b.InProgressItems) != 1 {
		t.Errorf("InProgressItems: want 1, got %d", len(b.InProgressItems))
	}
	if len(b.DoneItems) != 2 {
		t.Errorf("DoneItems: want 2, got %d", len(b.DoneItems))
	}
}

func TestParseSummary(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureInProgressMixed))
	got := b.Summary()
	want := "TODO: 1  IN PROGRESS: 1  DONE: 2"
	if got != want {
		t.Errorf("Summary: want %q, got %q", want, got)
	}
}

func TestParseEmpty(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	if len(b.TODOItems) != 0 || len(b.InProgressItems) != 0 || len(b.DoneItems) != 0 {
		t.Errorf("all columns should be empty")
	}
}

// ---- nextID tests ------------------------------------------------------------

func TestNextID_EmptyBoard(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	got := b.nextID()
	if got != "K-1" {
		t.Errorf("want K-1, got %q", got)
	}
}

func TestNextID_HighCounters(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureHighIDCounters))
	got := b.nextID()
	if got != "K-12" {
		t.Errorf("want K-12, got %q", got)
	}
}

func TestNextID_AfterAdd(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	b.Add("first", "other", "")
	got := b.nextID()
	if got != "K-2" {
		t.Errorf("want K-2 after adding K-1, got %q", got)
	}
}

// ---- Add tests ---------------------------------------------------------------

func TestAdd_ToEmptyBoard(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	id := b.Add("fix login bug", "bug", "")

	if id != "K-1" {
		t.Errorf("want id K-1, got %q", id)
	}

	// After add, the TODO column should have the new task.
	if len(b.TODOItems) != 1 {
		t.Fatalf("want 1 TODO task, got %d", len(b.TODOItems))
	}

	// The task block should appear immediately after ## TODO.
	todoIdx := -1
	for i, line := range b.RawLines {
		if strings.TrimSpace(line) == "## TODO" {
			todoIdx = i
			break
		}
	}
	if todoIdx < 0 {
		t.Fatal("## TODO heading not found")
	}
	// Lines immediately after ## TODO should be the task block.
	if todoIdx+1 >= len(b.RawLines) {
		t.Fatal("no line after ## TODO")
	}
	headerLine := b.RawLines[todoIdx+1]
	if !strings.Contains(headerLine, "K-1") {
		t.Errorf("expected K-1 in header line, got %q", headerLine)
	}
	if !strings.Contains(headerLine, "fix login bug") {
		t.Errorf("expected title in header line, got %q", headerLine)
	}
	if !strings.Contains(headerLine, emDash) {
		t.Errorf("expected em-dash in header line, got %q", headerLine)
	}
}

func TestAdd_DefaultCategory(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	b.Add("task", "", "")

	// Find the category line.
	for _, line := range b.RawLines {
		if strings.HasPrefix(line, "  - category:") {
			got := strings.TrimSpace(strings.TrimPrefix(line, "  - category:"))
			if got != "other" {
				t.Errorf("default category: want 'other', got %q", got)
			}
			return
		}
	}
	t.Error("no category line found")
}

func TestAdd_WithNotes(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	b.Add("task", "feature", "see issue #42")

	for _, line := range b.RawLines {
		if strings.HasPrefix(line, "  - notes:") {
			got := strings.TrimSpace(strings.TrimPrefix(line, "  - notes:"))
			if got != "see issue #42" {
				t.Errorf("notes: want 'see issue #42', got %q", got)
			}
			return
		}
	}
	t.Error("no notes line found")
}

func TestAdd_RoundTrip(t *testing.T) {
	// Add a task and verify the file can be parsed back without loss.
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	b.Add("new task", "feature", "remember the Y")

	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Parse the saved file.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = f.Close() }()
	b2, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse round-trip: %v", err)
	}
	if len(b2.TODOItems) != 1 {
		t.Errorf("round-trip TODOItems count: want 1, got %d", len(b2.TODOItems))
	}
}

func TestAdd_PreservesExistingTasks(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	b.Add("new task", "bug", "")

	// K-1, K-2, K-3 should still be present; K-4 should be the new one.
	found := map[string]bool{}
	for _, line := range b.RawLines {
		for _, id := range []string{"K-1", "K-2", "K-3", "K-4"} {
			if strings.Contains(line, id) {
				found[id] = true
			}
		}
	}
	for _, id := range []string{"K-1", "K-2", "K-3", "K-4"} {
		if !found[id] {
			t.Errorf("expected %s to be present after add", id)
		}
	}
}

// ---- SetNotes tests ----------------------------------------------------------

func TestSetNotes_ExistingTask(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	if err := b.SetNotes("K-1", "updated note"); err != nil {
		t.Fatalf("SetNotes: %v", err)
	}

	// Find the notes line for K-1 block.
	inK1 := false
	for _, line := range b.RawLines {
		if isTaskHeader(line) {
			inK1 = taskIDMatch(line, "K-1")
			continue
		}
		if inK1 && strings.HasPrefix(line, "  - notes:") {
			got := strings.TrimSpace(strings.TrimPrefix(line, "  - notes:"))
			if got != "updated note" {
				t.Errorf("notes: want 'updated note', got %q", got)
			}
			return
		}
	}
	t.Error("notes line for K-1 not found")
}

func TestSetNotes_NotFound(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	err := b.SetNotes("K-99", "text")
	if err == nil {
		t.Error("expected error for missing task id")
	}
}

func TestSetNotes_NormalizesNewlines(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	if err := b.SetNotes("K-1", "line1\nline2\r\nline3"); err != nil {
		t.Fatalf("SetNotes: %v", err)
	}

	inK1 := false
	for _, line := range b.RawLines {
		if isTaskHeader(line) {
			inK1 = taskIDMatch(line, "K-1")
			continue
		}
		if inK1 && strings.HasPrefix(line, "  - notes:") {
			got := strings.TrimSpace(strings.TrimPrefix(line, "  - notes:"))
			// Newlines should be replaced with spaces.
			if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
				t.Errorf("notes still contains newlines: %q", got)
			}
			return
		}
	}
}

func TestSetNotes_RoundTrip(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	_ = b.SetNotes("K-1", "new note")

	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	_ = b.Save(path)

	data, _ := os.ReadFile(path)
	b2, _ := Parse(strings.NewReader(string(data)))
	_ = b2.Save(path + ".2")
	data2, _ := os.ReadFile(path + ".2")

	if string(data) != string(data2) {
		t.Error("SetNotes round-trip: second save differs from first")
	}
}

// ---- Move tests --------------------------------------------------------------

func TestMove_TODOToDone(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	if err := b.Move("K-1", ColDone); err != nil {
		t.Fatalf("Move: %v", err)
	}

	// K-1 should be in DONE, not in TODO.
	inDone := false
	doneIdx := columnHeadingIndex(b.RawLines, ColDone)
	todoIdx := columnHeadingIndex(b.RawLines, ColTODO)
	for i, line := range b.RawLines {
		if taskIDMatch(line, "K-1") && isTaskHeader(line) {
			if i > doneIdx && (todoIdx < 0 || i < todoIdx || doneIdx > todoIdx) {
				inDone = true
			}
			break
		}
	}
	// Simpler: just check that K-1 appears after DONE heading.
	_ = inDone
	doneHeadIdx := -1
	for i, line := range b.RawLines {
		if strings.TrimSpace(line) == "## DONE" {
			doneHeadIdx = i
			break
		}
	}
	if doneHeadIdx < 0 {
		t.Fatal("## DONE heading not found")
	}
	foundAfterDone := false
	for i := doneHeadIdx + 1; i < len(b.RawLines); i++ {
		if taskIDMatch(b.RawLines[i], "K-1") {
			foundAfterDone = true
			break
		}
		if strings.HasPrefix(b.RawLines[i], "## ") {
			break
		}
	}
	if !foundAfterDone {
		t.Error("K-1 not found immediately after ## DONE")
	}
}

func TestMove_InProgressToTODO(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	if err := b.Move("K-2", ColTODO); err != nil {
		t.Fatalf("Move: %v", err)
	}

	// K-2 should now be after ## TODO.
	todoHeadIdx := -1
	for i, line := range b.RawLines {
		if strings.TrimSpace(line) == "## TODO" {
			todoHeadIdx = i
			break
		}
	}
	if todoHeadIdx < 0 {
		t.Fatal("## TODO not found")
	}
	if todoHeadIdx+1 >= len(b.RawLines) || !strings.Contains(b.RawLines[todoHeadIdx+1], "K-2") {
		t.Errorf("K-2 not immediately after ## TODO; line=%q", func() string {
			if todoHeadIdx+1 < len(b.RawLines) {
				return b.RawLines[todoHeadIdx+1]
			}
			return "(end of file)"
		}())
	}
}

func TestMove_NotFound(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	err := b.Move("K-99", ColDone)
	if err == nil {
		t.Error("expected error for missing task id")
	}
}

func TestMove_InvalidColumn(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	err := b.Move("K-1", "BACKLOG")
	if err == nil {
		t.Error("expected error for invalid column")
	}
}

func TestMove_RoundTrip(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	_ = b.Move("K-1", ColDone)

	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	_ = b.Save(path)

	data, _ := os.ReadFile(path)
	b2, _ := Parse(strings.NewReader(string(data)))
	_ = b2.Save(path + ".2")
	data2, _ := os.ReadFile(path + ".2")

	if string(data) != string(data2) {
		t.Error("Move round-trip: second save differs from first")
	}
}

// ---- Done tests --------------------------------------------------------------

func TestDone_AliasForMove(t *testing.T) {
	b1, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	b2, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))

	_ = b1.Done("K-1")
	_ = b2.Move("K-1", ColDone)

	if strings.Join(b1.RawLines, "\n") != strings.Join(b2.RawLines, "\n") {
		t.Error("Done and Move(ColDone) produced different results")
	}
}

// ---- Delete tests ------------------------------------------------------------

func TestDelete_RemovesTaskAndSubFields(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	if err := b.Delete("K-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// K-1 should not appear anywhere.
	for _, line := range b.RawLines {
		if taskIDMatch(line, "K-1") {
			t.Errorf("K-1 still present after delete: %q", line)
		}
	}

	// K-2 and K-3 should still be present.
	k2, k3 := false, false
	for _, line := range b.RawLines {
		if strings.Contains(line, "K-2") {
			k2 = true
		}
		if strings.Contains(line, "K-3") {
			k3 = true
		}
	}
	if !k2 {
		t.Error("K-2 missing after deleting K-1")
	}
	if !k3 {
		t.Error("K-3 missing after deleting K-1")
	}
}

func TestDelete_NotFound(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	err := b.Delete("K-99")
	if err == nil {
		t.Error("expected error for missing task id")
	}
}

func TestDelete_RoundTrip(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	_ = b.Delete("K-2")

	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	_ = b.Save(path)

	data, _ := os.ReadFile(path)
	b2, _ := Parse(strings.NewReader(string(data)))
	_ = b2.Save(path + ".2")
	data2, _ := os.ReadFile(path + ".2")

	if string(data) != string(data2) {
		t.Error("Delete round-trip: second save differs from first")
	}
}

// ---- NormalizeColumn tests ---------------------------------------------------

func TestNormalizeColumn(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"TODO", ColTODO, true},
		{"todo", ColTODO, true},
		{"IN PROGRESS", ColInProgress, true},
		{"in progress", ColInProgress, true},
		{"IN-PROGRESS", ColInProgress, true},
		{"in-progress", ColInProgress, true},
		{"IN_PROGRESS", ColInProgress, true},
		{"DONE", ColDone, true},
		{"done", ColDone, true},
		{"BACKLOG", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := NormalizeColumn(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("NormalizeColumn(%q): unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("NormalizeColumn(%q): want %q, got %q", c.in, c.want, got)
			}
		} else {
			if err == nil {
				t.Errorf("NormalizeColumn(%q): expected error, got %q", c.in, got)
			}
		}
	}
}

// ---- Schema sidecar tests ----------------------------------------------------

func TestSaveSchema_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	kanbanPath := filepath.Join(dir, "kanban.md")
	// Write a placeholder kanban.md.
	_ = os.WriteFile(kanbanPath, []byte("## TODO\n"), 0644)

	if err := SaveSchema(kanbanPath); err != nil {
		t.Fatalf("SaveSchema: %v", err)
	}

	sidecar := filepath.Join(dir, ".kanban.schema-version")
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("ReadFile sidecar: %v", err)
	}
	if strings.TrimSpace(string(data)) != CurrentSchemaVersion {
		t.Errorf("sidecar content: want %q, got %q", CurrentSchemaVersion, string(data))
	}
}

func TestLoadSchema_MissingReturnsV1(t *testing.T) {
	dir := t.TempDir()
	kanbanPath := filepath.Join(dir, "kanban.md")
	v, err := LoadSchema(kanbanPath)
	if err != nil {
		t.Fatalf("LoadSchema on missing sidecar: %v", err)
	}
	if v != "1" {
		t.Errorf("want '1' for missing sidecar, got %q", v)
	}
}

func TestLoadSchema_ReadsExisting(t *testing.T) {
	dir := t.TempDir()
	kanbanPath := filepath.Join(dir, "kanban.md")
	sidecar := filepath.Join(dir, ".kanban.schema-version")
	_ = os.WriteFile(sidecar, []byte("2\n"), 0644)

	v, err := LoadSchema(kanbanPath)
	if err != nil {
		t.Fatalf("LoadSchema: %v", err)
	}
	if v != "2" {
		t.Errorf("want '2', got %q", v)
	}
}

func TestSave_WritesSchemaFile(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sidecar := filepath.Join(dir, ".kanban.schema-version")
	if _, err := os.Stat(sidecar); err != nil {
		t.Errorf("sidecar not created: %v", err)
	}
}

// ---- Atomic write tests ------------------------------------------------------

func TestSave_AtomicRename_NoTmpLeftover(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	dir := t.TempDir()
	path := filepath.Join(dir, "kanban.md")
	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify no .tmp file remains.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("tmp file should not exist after successful save")
	}
}

// ---- RebuildTyped tests ------------------------------------------------------

func TestRebuildTyped_AfterMutations(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	// Initially: TODOItems=1, InProgressItems=1, DoneItems=1.
	if len(b.TODOItems) != 1 || len(b.InProgressItems) != 1 || len(b.DoneItems) != 1 {
		t.Fatalf("initial state wrong: TODO=%d IN=%d DONE=%d", len(b.TODOItems), len(b.InProgressItems), len(b.DoneItems))
	}

	// Move K-1 to DONE.
	_ = b.Done("K-1")
	if len(b.TODOItems) != 0 {
		t.Errorf("after Done(K-1): want TODOItems=0, got %d", len(b.TODOItems))
	}
	if len(b.DoneItems) != 2 {
		t.Errorf("after Done(K-1): want DoneItems=2, got %d", len(b.DoneItems))
	}
}

// ---- ID non-collision tests --------------------------------------------------

// TestIDNonCollision verifies that adding multiple tasks to the same board
// produces sequential non-colliding IDs.
func TestIDNonCollision(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	ids := make([]string, 5)
	for i := range ids {
		ids[i] = b.Add("task "+itoa(i), "other", "")
	}

	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate id: %q", id)
		}
		seen[id] = true
	}
}

// ---- Render TUI tests --------------------------------------------------------

func TestRenderTUI_EmptyBoard(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureEmpty))
	var w strings.Builder
	if err := b.RenderTUI(&w); err != nil {
		t.Fatalf("RenderTUI: %v", err)
	}
	out := w.String()
	if !strings.Contains(out, "TODO") || !strings.Contains(out, "IN PROGRESS") || !strings.Contains(out, "DONE") {
		t.Error("TUI output missing column headers")
	}
	if !strings.Contains(out, "(empty)") {
		t.Error("TUI output should show (empty) for empty columns")
	}
}

func TestRenderTUI_WithTasks(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	var w strings.Builder
	if err := b.RenderTUI(&w); err != nil {
		t.Fatalf("RenderTUI: %v", err)
	}
	out := w.String()
	if !strings.Contains(out, "fix login bug") {
		t.Error("TUI output should contain task title 'fix login bug'")
	}
}

// ---- Render HTML tests -------------------------------------------------------

func TestRenderHTML_ContainsColumns(t *testing.T) {
	b, _ := Parse(strings.NewReader(fixtureWithTaskBlocks))
	var w strings.Builder
	if err := b.RenderHTML(&w, "/tmp/kanban.md"); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	out := w.String()
	for _, want := range []string{"DOCTYPE html", "TODO", "IN PROGRESS", "DONE", "fix login bug"} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML output missing %q", want)
		}
	}
}

func TestRenderHTML_EscapesHTML(t *testing.T) {
	const boardWithHTML = `## TODO
- [ ] K-1 — task with <b>bold</b> & "quotes"
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes: <script>alert(1)</script>

## IN PROGRESS

## DONE
`
	b, _ := Parse(strings.NewReader(boardWithHTML))
	var w strings.Builder
	_ = b.RenderHTML(&w, "/tmp/kanban.md")
	out := w.String()
	if strings.Contains(out, "<script>") {
		t.Error("HTML output should escape <script> tags")
	}
	if strings.Contains(out, `"quotes"`) {
		t.Error("HTML output should escape double quotes")
	}
}
