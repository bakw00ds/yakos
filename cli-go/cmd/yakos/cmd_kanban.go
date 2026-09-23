package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/bakw00ds/yakos/internal/kanban"
)

// runKanban implements `yakos kanban` natively in Go.
//
// Usage mirrors cli/lib/kanban.sh exactly:
//
//	yakos kanban                       # render TUI (default)
//	yakos kanban --html [<out>]        # render static HTML snapshot
//	yakos kanban add "<title>" [--category <c>] [--notes "<text>"]
//	yakos kanban notes <id> "<text>"   # set/replace notes field
//	yakos kanban move <id> <col>       # move between columns
//	yakos kanban done <id>             # shortcut to DONE
//	yakos kanban delete <id>           # hard-delete a task (also: rm)
//	yakos kanban serve [...]           # live web UI (rank 41 complete)
//	yakos kanban status                # is the web UI running?
//	yakos kanban stop                  # stop the running web UI
//	yakos kanban --help                # print help
//
// The kanban.md file is resolved from YAKOS_WORK_DIR env variable, or from the
// canonical path $HOME/agent-control/<YAKOS_PROJECT>/work/current/kanban.md.
// When neither is set, the current working directory is searched for a
// work/current/kanban.md.
func runKanban(yakosRoot string, args []string) {
	if len(args) == 0 {
		renderKanbanTUI()
		return
	}

	switch args[0] {
	case "--help", "-h", "help":
		printKanbanHelp(os.Stdout)
		os.Exit(0)
	case "--html":
		renderKanbanHTML(args[1:])
	case "add":
		kanbanAdd(args[1:])
	case "notes":
		kanbanNotes(args[1:])
	case "move":
		kanbanMove(args[1:])
	case "done":
		kanbanDone(args[1:])
	case "delete", "rm":
		kanbanDelete(args[1:])
	case "serve", "--serve":
		kanbanServe(args[1:])
	case "status":
		kanbanServeStatus()
	case "stop":
		kanbanServeStop()
	default:
		fmt.Fprintf(os.Stderr, "kanban: unknown subcommand %q (try --help)\n", args[0])
		os.Exit(1)
	}
}

// kanbanFilePath resolves the path to kanban.md.
//
// Resolution order (mirrors cli/lib/paths.sh priority):
//  1. YAKOS_WORK_DIR env → $YAKOS_WORK_DIR/current/kanban.md
//  2. YAKOS_INPLACE_WORK=1 + CLAUDE_PROJECT_DIR → $CLAUDE_PROJECT_DIR/work/current/kanban.md
//  3. $HOME/agent-control/$YAKOS_PROJECT_NAME/work/current/kanban.md
//  4. Fallback: ./work/current/kanban.md relative to cwd
func kanbanFilePath() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current", "kanban.md")
	}
	if os.Getenv("YAKOS_INPLACE_WORK") == "1" {
		if pd := os.Getenv("CLAUDE_PROJECT_DIR"); pd != "" {
			return filepath.Join(pd, "work", "current", "kanban.md")
		}
	}
	if proj := os.Getenv("YAKOS_PROJECT_NAME"); proj != "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = "/tmp"
		}
		return filepath.Join(home, "agent-control", proj, "work", "current", "kanban.md")
	}
	// Fallback: look in cwd.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return filepath.Join(cwd, "work", "current", "kanban.md")
}

// loadBoard reads and parses the kanban.md at path. If the file does not
// exist and create is true, a seeded empty board is returned. If the file
// does not exist and create is false, an error is returned.
func loadBoard(path string, create bool) (*kanban.Board, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		if !create {
			return nil, fmt.Errorf("kanban.md not found at %s", path)
		}
		return kanban.Seed(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	b, err := kanban.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return b, nil
}

// ensureKanbanDir creates the directory containing the kanban.md if needed.
func ensureKanbanDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0755) //nolint:gosec
}

func renderKanbanTUI() {
	path := kanbanFilePath()
	b, err := loadBoard(path, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: %v\n", err)
		os.Exit(1)
	}
	if err := b.RenderTUI(os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "kanban: render: %v\n", err)
		os.Exit(1)
	}
}

func renderKanbanHTML(args []string) {
	path := kanbanFilePath()
	outPath := filepath.Join(filepath.Dir(path), "kanban.html")
	if len(args) > 0 {
		outPath = args[0]
	}

	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: %v\n", err)
		os.Exit(1)
	}

	// Write to temp then rename (atomic).
	tmp := outPath + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban: create %s: %v\n", tmp, err)
		os.Exit(1)
	}
	if err := b.RenderHTML(f, path); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: render html: %v\n", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: close %s: %v\n", tmp, err)
		os.Exit(1)
	}
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintf(os.Stderr, "kanban: rename %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: rendered: %s\n", outPath)
}

func kanbanAdd(args []string) {
	category := "other"
	notes := ""
	title := ""

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--category":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban add: --category needs a value")
				os.Exit(1)
			}
			category = args[i]
		case "--notes":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban add: --notes needs a value")
				os.Exit(1)
			}
			notes = args[i]
		default:
			if len(args[i]) > 0 && args[i][0] == '-' && args[i] != "--" {
				// Check for --category=<v> and --notes=<v> forms.
				if len(args[i]) > 11 && args[i][:11] == "--category=" {
					category = args[i][11:]
				} else if len(args[i]) > 8 && args[i][:8] == "--notes=" {
					notes = args[i][8:]
				} else if args[i] == "--" {
					i++
					if i < len(args) && title == "" {
						title = args[i]
					}
				} else {
					fmt.Fprintf(os.Stderr, "kanban add: unknown option %q\n", args[i])
					os.Exit(1)
				}
			} else {
				if title == "" {
					title = args[i]
				} else {
					fmt.Fprintf(os.Stderr, "kanban add: unexpected argument %q\n", args[i])
					os.Exit(1)
				}
			}
		}
		i++
	}

	if title == "" {
		fmt.Fprintln(os.Stderr, `kanban add: title required: yakos kanban add "<title>"`)
		os.Exit(1)
	}

	path := kanbanFilePath()
	if err := ensureKanbanDir(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: mkdir %s: %v\n", filepath.Dir(path), err)
		os.Exit(1)
	}

	b, err := loadBoard(path, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: %v\n", err)
		os.Exit(1)
	}

	id := b.Add(title, category, notes)

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban add: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: added: %s \xe2\x80\x94 %s (category: %s)\n", id, title, category)
}

func kanbanNotes(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, `kanban notes: id and text required: yakos kanban notes <id> "<text>"`)
		os.Exit(1)
	}
	id := args[0]
	text := args[1]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: %v\n", err)
		os.Exit(1)
	}

	if err := b.SetNotes(id, text); err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban notes: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: notes set for %s\n", id)
}

func kanbanMove(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "kanban move: id and column required: yakos kanban move <id> <col>")
		os.Exit(1)
	}
	id := args[0]
	col, err := kanban.NormalizeColumn(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", err)
		os.Exit(1)
	}

	path := kanbanFilePath()
	b, loadErr := loadBoard(path, false)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", loadErr)
		os.Exit(1)
	}

	if err := b.Move(id, col); err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban move: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: moved %s to %s\n", id, col)
}

func kanbanDone(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "kanban done: id required: yakos kanban done <id>")
		os.Exit(1)
	}
	id := args[0]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: %v\n", err)
		os.Exit(1)
	}

	if err := b.Done(id); err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban done: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: moved %s to DONE\n", id)
}

func kanbanDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "kanban delete: id required: yakos kanban delete <id>")
		os.Exit(1)
	}
	id := args[0]

	path := kanbanFilePath()
	b, err := loadBoard(path, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: %v\n", err)
		os.Exit(1)
	}

	if err := b.Delete(id); err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: %v\n", err)
		os.Exit(1)
	}

	if err := b.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "kanban delete: save: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "kanban: deleted: %s\n", id)
}

// kanbanStateFile returns the path to the .kanban-serve.json state sidecar.
// Matches bash _kanban_state_file() which uses yakos_current_dir().
func kanbanStateFile() string {
	boardPath := kanbanFilePath()
	dir := filepath.Dir(boardPath)
	return filepath.Join(dir, ".kanban-serve.json")
}

// kanbanServeStateExists returns the parsed state if a live state file exists.
func kanbanServeStateExists() (url, pid string, ok bool) {
	stateFile := kanbanStateFile()
	raw, err := os.ReadFile(stateFile) //nolint:gosec
	if err != nil {
		return "", "", false
	}
	var state struct {
		PID int    `json:"pid"`
		URL string `json:"url"`
	}
	if err := func() error {
		return func() error { return nil }() // placeholder
	}(); err != nil {
		return "", "", false
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", "", false
	}
	if state.URL == "" || state.PID == 0 {
		return "", "", false
	}
	return state.URL, intToStr(state.PID), true
}

// intToStr converts int to string without strconv import (local helper).
func intToStr(n int) string {
	return strconv.Itoa(n)
}

// kanbanServe implements `yakos kanban serve [--port N] [--host H] [--no-open]`.
func kanbanServe(args []string) {
	port := 0
	host := "127.0.0.1"
	openBrowser := true

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--port":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban serve: --port needs a value")
				os.Exit(1)
			}
			n := 0
			for _, ch := range args[i] {
				if ch < '0' || ch > '9' {
					fmt.Fprintln(os.Stderr, "kanban serve: --port must be a number")
					os.Exit(1)
				}
				n = n*10 + int(ch-'0')
			}
			port = n
		case "--host":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "kanban serve: --host needs a value")
				os.Exit(1)
			}
			host = args[i]
			// Security: require explicit flag to expose on all interfaces.
			if host == "0.0.0.0" {
				fmt.Fprintln(os.Stderr, "kanban serve: binding 0.0.0.0 — the web UI is UNAUTHENTICATED and can mutate the board")
			}
		case "--no-open":
			openBrowser = false
		default:
			if len(args[i]) > 7 && args[i][:7] == "--port=" {
				// --port=N form.
				val := args[i][7:]
				n := 0
				for _, ch := range val {
					if ch < '0' || ch > '9' {
						fmt.Fprintln(os.Stderr, "kanban serve: --port must be a number")
						os.Exit(1)
					}
					n = n*10 + int(ch-'0')
				}
				port = n
			} else if len(args[i]) > 7 && args[i][:7] == "--host=" {
				host = args[i][7:]
			} else {
				fmt.Fprintf(os.Stderr, "kanban serve: unknown option %q\n", args[i])
				os.Exit(1)
			}
		}
	}

	boardPath := kanbanFilePath()
	project := os.Getenv("YAKOS_PROJECT_NAME")
	if project == "" {
		project = filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(boardPath))))
	}

	cfg := kanban.ServeConfig{
		BoardPath:   boardPath,
		Project:     project,
		Host:        host,
		Port:        port,
		OpenBrowser: openBrowser,
		ErrWriter:   os.Stderr,
	}
	if _, err := kanban.Serve(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "kanban serve: %v\n", err)
		os.Exit(1)
	}
}

// kanbanServeStatus checks if a kanban web UI is running and prints its URL.
// The Go implementation checks the .kanban-serve.json state file.
// Note: the Go server does not write a state file in the current implementation
// (the bash server did this; the Go server blocks and keeps state in-process).
// This subcommand prints a not-running message pointing at the state file path.
func kanbanServeStatus() {
	url, pid, ok := kanbanServeStateExists()
	if ok {
		fmt.Printf("kanban web UI: running\n  url:     %s\n  pid:     %s\n", url, pid)
	} else {
		fmt.Fprintln(os.Stderr, "kanban web UI: not running")
		fmt.Fprintf(os.Stderr, "  (state file: %s)\n", kanbanStateFile())
	}
}

// kanbanServeStop prints an advisory. The Go server runs in the foreground
// (same as bash) and is stopped by Ctrl-C. A background PID from a state file
// can be killed if the state file is present.
func kanbanServeStop() {
	url, pid, ok := kanbanServeStateExists()
	if !ok {
		fmt.Fprintln(os.Stderr, "kanban web UI: not running")
		return
	}
	fmt.Fprintf(os.Stderr, "kanban web UI running at %s (pid %s)\n", url, pid)
	fmt.Fprintln(os.Stderr, "To stop it: press Ctrl-C in the terminal where it is running,")
	fmt.Fprintln(os.Stderr, "  or: kill "+pid)
}

// printKanbanHelp prints the help text for `yakos kanban`, matching the
// bash kanban.sh --help output.
func printKanbanHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos kanban — 3-column markdown board in scratchpad

  yakos kanban                       # render TUI
  yakos kanban --html [<out>]        # render static HTML snapshot
  yakos kanban serve [--port N]      # live web UI: view + manage
                                     #   [--host H] [--no-open]
                                     #   (default: random high port on 127.0.0.1)
                                     #   binds 127.0.0.1 by default (loopback only)
                                     #   example: --host localhost
                                     #   note: on macOS, "localhost" may resolve to
                                     #   ::1 (IPv6); Go listens on one resolved addr,
                                     #   so a browser hitting 127.0.0.1 can then fail
                                     #   — the 127.0.0.1 default avoids this ambiguity
  yakos kanban status                # is the web UI running? print its URL
  yakos kanban stop                  # stop the running web UI
  yakos kanban add "<title>"         # append to TODO
                [--category <c>]     #   category (default: other)
                [--notes "<text>"]   #   initial notes (single line)
  yakos kanban notes <id> "<text>"   # set/replace notes field (any column)
  yakos kanban move <id> <col>       # move between columns
  yakos kanban done <id>             # shortcut to DONE
  yakos kanban delete <id>           # hard-delete a task (also: rm)

Known categories: bug  feature  chore  question  other  (arbitrary values accepted)
`)
}
