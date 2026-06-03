package kanban_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/pkg/kanban"
)

func ExampleParse() {
	src := `# Kanban — example

## TODO
- [ ] K-1 — implement API
  - category: feat
  - assigned: alice
  - blockers: none
  - notes: high priority

## IN PROGRESS
- [ ] K-2 — write tests
  - category: test
  - assigned: bob
  - blockers: none
  - notes:

## DONE
- [x] K-3 — design review
  - category: other
  - assigned: carol
  - blockers: none
  - notes:
`
	board, err := kanban.Parse(strings.NewReader(src))
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse: %v\n", err)
		return
	}
	fmt.Println(board.Summary())
	// Output: TODO: 1  IN PROGRESS: 1  DONE: 1
}

func ExampleLoad() {
	// Create a temporary kanban file.
	tmp, _ := os.MkdirTemp("", "kanban-example-*")
	defer os.RemoveAll(tmp)

	path := filepath.Join(tmp, "kanban.md")
	_ = os.WriteFile(path, []byte(`# Kanban — example

## TODO
- [ ] K-1 — task one
  - category: other
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`), 0644)

	board, err := kanban.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return
	}
	fmt.Println(len(board.TODOItems), "todo item(s)")
	// Output: 1 todo item(s)
}

func ExampleBoard_Add() {
	src := `# Kanban — example

## TODO

## IN PROGRESS

## DONE
`
	board, _ := kanban.Parse(strings.NewReader(src))
	id := board.Add("implement health endpoint", "feat", "")
	fmt.Println("added", id)
	// Output: added K-1
}

func ExampleBoard_Move() {
	src := `# Kanban — example

## TODO
- [ ] K-1 — implement health endpoint
  - category: feat
  - assigned: unassigned
  - blockers: none
  - notes:

## IN PROGRESS

## DONE
`
	board, _ := kanban.Parse(strings.NewReader(src))
	if err := board.Move("K-1", kanban.ColInProgress); err != nil {
		fmt.Fprintf(os.Stderr, "move: %v\n", err)
		return
	}
	fmt.Printf("in_progress=%d todo=%d\n", len(board.InProgressItems), len(board.TODOItems))
	// Output: in_progress=1 todo=0
}

func ExampleNormalizeColumn() {
	col, err := kanban.NormalizeColumn("in progress")
	if err != nil {
		fmt.Fprintf(os.Stderr, "normalize: %v\n", err)
		return
	}
	fmt.Println(col)
	// Output: IN PROGRESS
}
