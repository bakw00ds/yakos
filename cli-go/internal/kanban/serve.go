// serve.go — `yakos kanban serve` HTTP server (rank 41 port).
//
// Runs an HTTP server that serves the kanban board as a live web UI.
// All mutations go through the in-memory Board + Save cycle, keeping
// kanban.md as the single source of truth — identical to the bash
// implementation's "shell back into kanban.sh" strategy, but native Go.
//
// Security:
//   - Binds to 127.0.0.1 by default; --allow-all-interfaces required for 0.0.0.0.
//   - Host header checked against an allow-list (DNS-rebinding defence).
//   - POST body capped at maxBodyBytes.
//   - Task IDs validated against taskIDPattern before any board mutation.
//   - Backslashes in title/notes rejected (sed/awk metacharacter risk is gone
//     in Go, but we keep the same contract as bash for API parity).
//
// Routes:
//
//	GET  /            — embedded HTML/JS UI (serve_ui.html via //go:embed)
//	GET  /api/board   — JSON snapshot of the current board
//	GET  /api/meta    — JSON project metadata + categories
//	POST /api/add     — add a task; returns updated board JSON
//	POST /api/move    — move task to a column; returns updated board JSON
//	POST /api/notes   — set task notes; returns updated board JSON
//	POST /api/delete  — delete a task; returns updated board JSON
//
// Decision D: static assets embedded via //go:embed.
// Decision Q8: atomic temp-rename on every board write (via Board.Save).
package kanban

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
)

//go:embed serve_ui.html
var serveUIHTML []byte

// taskIDPattern mirrors the bash/Python ID_RE: only K-<digits> accepted.
var taskIDPattern = regexp.MustCompile(`^K-\d+$`)

// defaultCategories matches bash KANBAN_DEFAULT_CATEGORIES.
var defaultCategories = []string{"bug", "feature", "chore", "question", "other"}

const (
	// maxBodyBytes caps POST request bodies to prevent memory exhaustion.
	maxBodyBytes = 256 * 1024

	// maxTitleLen matches Python's 500-character cap.
	maxTitleLen = 500

	// maxNotesLen matches Python's 2000-character cap.
	maxNotesLen = 2000
)

// ServeConfig holds the options for kanban serve.
type ServeConfig struct {
	// BoardPath is the absolute path to kanban.md.
	BoardPath string

	// Project is the project name displayed in the UI.
	Project string

	// Host is the bind address. Default (empty string) resolves to "127.0.0.1".
	// Pass "0.0.0.0" only when the operator has explicitly opted in via a flag.
	Host string

	// Port is the TCP port to bind. 0 means the OS picks a free port.
	Port int

	// OpenBrowser, when true, opens the browser after binding.
	OpenBrowser bool

	// Listener, when non-nil, is used directly instead of binding (injected in tests).
	Listener net.Listener

	// ErrWriter receives startup/shutdown messages.
	ErrWriter io.Writer
}

// ServeResult is returned by Serve when the server shuts down.
type ServeResult struct {
	// URL is the base URL the server was listening on, e.g. "http://127.0.0.1:8421".
	URL string
	// BoundPort is the actual port (useful when Port was 0).
	BoundPort int
}

// server holds the state shared across HTTP handlers.
type server struct {
	boardPath    string
	project      string
	host         string
	mu           sync.Mutex // serialises board mutations (mirrors Python _mutate_lock)
	allowedHosts map[string]struct{}
	mux          *http.ServeMux
	// skipHostCheck, when true, disables the inner hostOK check in all route
	// handlers.  Set by NewKanbanServer/Handler() when the server is mounted
	// behind an external edge auth (e.g. the consoleui RequireLocalHost +
	// RequireToken middleware).  The standalone Serve() path leaves this false.
	skipHostCheck bool
}

// Handler returns the underlying http.Handler for this server WITHOUT the
// inner Host-allowlist middleware.  The caller is responsible for supplying
// appropriate auth at the edge (e.g. the consoleui RequireToken wrapper).
//
// This mirrors the pattern in perfdash.Server.Handler() and
// metricsdash.Server.Handler().
func (s *server) Handler() http.Handler { return s.mux }

// KanbanServer is the exported handle to an in-process kanban HTTP server.
// Obtain one via NewKanbanServer; use Handler() to mount it under a parent mux.
type KanbanServer struct {
	inner *server
}

// Handler returns the kanban http.Handler without Host/token middleware.
// The caller must supply auth at the edge.
func (k *KanbanServer) Handler() http.Handler { return k.inner.Handler() }

// NewKanbanServer constructs an in-process kanban server for the given board
// file and project name.  boardPath must be an absolute path; host is used
// only for the Host-allowlist in standalone Serve mode (pass "" for
// "127.0.0.1").
//
// Callers that mount the handler under a parent mux (e.g. consoleui) should
// call Handler() on the returned value; callers that want a standalone server
// should call the package-level Serve function.
//
// When mounted via Handler(), the inner hostOK check is disabled because
// the console edge (RequireLocalHost + RequireToken) is the auth authority.
func NewKanbanServer(boardPath, project, host string) *KanbanServer {
	inner := newServer(boardPath, project, host)
	inner.skipHostCheck = true
	return &KanbanServer{inner: inner}
}

// newServer constructs a server and wires all routes onto its internal mux.
func newServer(boardPath, project, host string) *server {
	allowed := map[string]struct{}{
		"127.0.0.1": {},
		"localhost": {},
		"[::1]":     {},
		"::1":       {},
	}
	if host != "" {
		allowed[host] = struct{}{}
	}

	srv := &server{
		boardPath:    boardPath,
		project:      project,
		host:         host,
		allowedHosts: allowed,
		mux:          http.NewServeMux(),
	}
	srv.mux.HandleFunc("/", srv.handleRoot)
	srv.mux.HandleFunc("/api/board", srv.handleBoard)
	srv.mux.HandleFunc("/api/meta", srv.handleMeta)
	srv.mux.HandleFunc("/api/add", srv.handleAdd)
	srv.mux.HandleFunc("/api/move", srv.handleMove)
	srv.mux.HandleFunc("/api/notes", srv.handleNotes)
	srv.mux.HandleFunc("/api/delete", srv.handleDelete)
	srv.mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return srv
}

// Serve starts the HTTP server and blocks until it is shut down (e.g. by SIGINT).
// It is expected that the caller manages lifecycle via Listener or by OS signal.
//
// When cfg.Listener is provided (for tests), Serve uses it directly without
// binding a new socket. This lets tests pick an ephemeral port via net.Listen
// and pass the listener in, avoiding port-conflict races.
func Serve(cfg ServeConfig) (ServeResult, error) {
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = io.Discard
	}
	host := cfg.Host
	if host == "" {
		host = "127.0.0.1"
	}

	// Warn loudly about non-loopback binding, matching bash behavior.
	if !isLoopback(host) {
		fmt.Fprintf(cfg.ErrWriter,
			"WARNING: binding non-loopback host %q — the web UI is UNAUTHENTICATED "+
				"and can mutate the board; only do this on a trusted network.\n", host)
	}

	// Ensure board directory exists and seed if needed.
	if err := os.MkdirAll(pathDir(cfg.BoardPath), 0755); err != nil { //nolint:gosec
		return ServeResult{}, fmt.Errorf("kanban serve: mkdir %s: %w", pathDir(cfg.BoardPath), err)
	}
	if _, err := os.Stat(cfg.BoardPath); os.IsNotExist(err) {
		// Seed an empty board so the file exists before the server starts.
		b := Seed()
		if err := b.Save(cfg.BoardPath); err != nil {
			return ServeResult{}, fmt.Errorf("kanban serve: seed board: %w", err)
		}
	}

	srv := newServer(cfg.BoardPath, cfg.Project, host)

	mux := srv.mux

	var ln net.Listener
	if cfg.Listener != nil {
		ln = cfg.Listener
	} else {
		var err error
		addr := fmt.Sprintf("%s:%d", host, cfg.Port)
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			return ServeResult{}, fmt.Errorf("kanban serve: cannot bind %s: %w", addr, err)
		}
	}

	boundAddr := ln.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://%s:%d", host, boundAddr.Port)

	fmt.Fprintf(cfg.ErrWriter, "kanban web UI → %s   (project: %s)\n", url, cfg.Project)
	fmt.Fprintln(cfg.ErrWriter, "press Ctrl-C to stop")

	if cfg.OpenBrowser {
		openBrowser(url)
	}

	httpSrv := &http.Server{Handler: mux}
	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return ServeResult{URL: url, BoundPort: boundAddr.Port}, err
	}
	return ServeResult{URL: url, BoundPort: boundAddr.Port}, nil
}

// ---- handler helpers --------------------------------------------------------

// hostOK returns true when the request's Host header is in the allow-list OR
// when skipHostCheck is set (i.e. the server is mounted behind external edge
// auth that already validated the host, such as the consoleui middleware).
func (s *server) hostOK(r *http.Request) bool {
	if s.skipHostCheck {
		return true
	}
	host := r.Host
	// Strip port suffix.
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	_, ok := s.allowedHosts[host]
	return ok
}

func (s *server) sendJSON(w http.ResponseWriter, code int, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, `{"error":"marshal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(data)
}

func (s *server) forbiddenHost(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	s.sendJSON(w, http.StatusForbidden, map[string]string{
		"error": "forbidden host",
		"host":  host,
	})
}

// parseBody reads and JSON-decodes the request body into dst (must be a *map[string]interface{}).
// Returns false and writes an error response on failure.
func (s *server) parseBody(w http.ResponseWriter, r *http.Request, dst *map[string]interface{}) bool {
	length := r.ContentLength
	if length < 0 {
		length = 0
	}
	if length > maxBodyBytes {
		s.sendJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request too large"})
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "read error"})
		return false
	}
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}

// boardJSON returns the current board as a JSON-serializable map.
// Must be called inside the mutex.
func (s *server) boardJSON() (interface{}, error) {
	f, err := os.Open(s.boardPath)
	if os.IsNotExist(err) {
		return map[string]interface{}{
			ColTODO:       []interface{}{},
			ColInProgress: []interface{}{},
			ColDone:       []interface{}{},
		}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := Parse(f)
	if err != nil {
		return nil, err
	}
	return boardToAPIMap(b), nil
}

// boardToAPIMap converts a Board to the wire-format map the JS client expects.
// Each column is an array of task objects matching the Python parse_board() output.
func boardToAPIMap(b *Board) map[string]interface{} {
	type taskObj struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Done     bool   `json:"done"`
		Category string `json:"category"`
		Assigned string `json:"assigned"`
		Blockers string `json:"blockers"`
		Notes    string `json:"notes"`
	}

	result := map[string]interface{}{
		ColTODO:       []interface{}{},
		ColInProgress: []interface{}{},
		ColDone:       []interface{}{},
	}

	// Walk RawLines to extract typed task objects per column.
	// This mirrors Python parse_board() exactly.
	var curCol *[]interface{}
	var cur *taskObj

	flush := func() {
		if cur != nil && curCol != nil {
			*curCol = append(*curCol, *cur)
			cur = nil
		}
	}

	todoSlice := result[ColTODO].([]interface{})
	inProgSlice := result[ColInProgress].([]interface{})
	doneSlice := result[ColDone].([]interface{})

	for _, line := range b.RawLines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			flush()
			heading := strings.TrimSpace(trimmed[3:])
			switch heading {
			case ColTODO:
				curCol = &todoSlice
			case ColInProgress:
				curCol = &inProgSlice
			case ColDone:
				curCol = &doneSlice
			default:
				curCol = nil
			}
			continue
		}

		if curCol == nil {
			continue
		}

		// Strict task header: - [ ] K-N — title  or  - [x] K-N — title
		if strings.HasPrefix(line, "- [") && len(line) > 6 && line[4] == ']' {
			flush()
			checkbox := string(line[3])
			rest := strings.TrimSpace(line[6:])
			done := checkbox == "x" || checkbox == "X"
			id, title := parseTaskIDTitle(rest)
			cur = &taskObj{
				ID:       id,
				Title:    title,
				Done:     done,
				Category: "other",
			}
			continue
		}

		if cur != nil {
			if strings.HasPrefix(line, "  - category:") {
				v := strings.TrimSpace(strings.TrimPrefix(line, "  - category:"))
				if v == "" {
					v = "other"
				}
				cur.Category = v
			} else if strings.HasPrefix(line, "  - assigned:") {
				cur.Assigned = strings.TrimSpace(strings.TrimPrefix(line, "  - assigned:"))
			} else if strings.HasPrefix(line, "  - blockers:") {
				cur.Blockers = strings.TrimSpace(strings.TrimPrefix(line, "  - blockers:"))
			} else if strings.HasPrefix(line, "  - notes:") {
				cur.Notes = strings.TrimSpace(strings.TrimPrefix(line, "  - notes:"))
			}
		}
	}
	flush()

	// Write back the slices (Go's interface{} copy semantics mean we need to do this).
	result[ColTODO] = todoSlice
	result[ColInProgress] = inProgSlice
	result[ColDone] = doneSlice
	return result
}

// parseTaskIDTitle splits "K-3 — fix login bug" into ("K-3", "fix login bug").
// Handles both em-dash and ASCII-hyphen separators.
func parseTaskIDTitle(rest string) (id, title string) {
	// Extract K-<digits> token at the start.
	if len(rest) > 2 && rest[:2] == "K-" {
		j := 2
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j > 2 {
			id = rest[:j]
			rest = strings.TrimSpace(rest[j:])
			// Strip leading em-dash or ASCII hyphen separator.
			rest = strings.TrimPrefix(rest, emDash)
			rest = strings.TrimPrefix(rest, "-")
			title = strings.TrimSpace(rest)
			return
		}
	}
	// Freeform: no K-N id.
	rest = strings.TrimPrefix(rest, emDash)
	rest = strings.TrimPrefix(rest, "- ")
	return "", strings.TrimSpace(rest)
}

// boardCategories returns the deduplicated categories from the board.
// Known categories first, then any extras found in the board.
func boardCategories(b map[string]interface{}) []string {
	seen := map[string]struct{}{}
	for _, c := range defaultCategories {
		seen[c] = struct{}{}
	}
	var extras []string
	for _, col := range []string{ColTODO, ColInProgress, ColDone} {
		tasks, _ := b[col].([]interface{})
		for _, t := range tasks {
			m, _ := t.(map[string]interface{})
			if m == nil {
				continue
			}
			cat, _ := m["category"].(string)
			if cat == "" {
				cat = "other"
			}
			if _, ok := seen[cat]; !ok {
				extras = append(extras, cat)
				seen[cat] = struct{}{}
			}
		}
	}
	return append(defaultCategories, extras...)
}

// ---- route handlers ---------------------------------------------------------

func (s *server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(r) {
		s.forbiddenHost(w, r)
		return
	}
	if r.URL.Path != "/" {
		s.sendJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(serveUIHTML)
}

func (s *server) handleBoard(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(r) {
		s.forbiddenHost(w, r)
		return
	}
	if r.Method != http.MethodGet {
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	s.mu.Lock()
	boardData, err := s.boardJSON()
	s.mu.Unlock()
	if err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.sendJSON(w, http.StatusOK, boardData)
}

func (s *server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(r) {
		s.forbiddenHost(w, r)
		return
	}
	if r.Method != http.MethodGet {
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	s.mu.Lock()
	boardData, err := s.boardJSON()
	s.mu.Unlock()
	if err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	bm, _ := boardData.(map[string]interface{})
	cats := boardCategories(bm)
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"project":    s.project,
		"file":       s.boardPath,
		"columns":    []string{ColTODO, ColInProgress, ColDone},
		"categories": cats,
	})
}

func (s *server) handleAdd(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(r) {
		s.forbiddenHost(w, r)
		return
	}
	if r.Method != http.MethodPost {
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body map[string]interface{}
	if !s.parseBody(w, r, &body) {
		return
	}

	title, _ := body["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
		return
	}
	if len(title) > maxTitleLen {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "title too long"})
		return
	}
	if strings.ContainsAny(title, "\n\r") {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "title must be one line"})
		return
	}
	if strings.Contains(title, "\\") {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "title may not contain backslashes"})
		return
	}

	category, _ := body["category"].(string)
	category = strings.TrimSpace(category)
	if category == "" {
		category = "other"
	}
	if strings.ContainsAny(category, "\\\n\r") {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid category value"})
		return
	}

	notes, _ := body["notes"].(string)
	notes = strings.TrimSpace(notes)

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.loadBoard()
	if err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	b.Add(title, category, notes)
	if err := b.Save(s.boardPath); err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.sendJSON(w, http.StatusOK, boardToAPIMap(b))
}

func (s *server) handleMove(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(r) {
		s.forbiddenHost(w, r)
		return
	}
	if r.Method != http.MethodPost {
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body map[string]interface{}
	if !s.parseBody(w, r, &body) {
		return
	}

	taskID, _ := body["id"].(string)
	taskID = strings.TrimSpace(taskID)
	if !taskIDPattern.MatchString(taskID) {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "bad task id"})
		return
	}

	col, _ := body["col"].(string)
	col = strings.TrimSpace(col)
	normCol, err := NormalizeColumn(col)
	if err != nil {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "bad column"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.loadBoard()
	if err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := b.Move(taskID, normCol); err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := b.Save(s.boardPath); err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.sendJSON(w, http.StatusOK, boardToAPIMap(b))
}

func (s *server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(r) {
		s.forbiddenHost(w, r)
		return
	}
	if r.Method != http.MethodPost {
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body map[string]interface{}
	if !s.parseBody(w, r, &body) {
		return
	}

	taskID, _ := body["id"].(string)
	taskID = strings.TrimSpace(taskID)
	if !taskIDPattern.MatchString(taskID) {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "bad task id"})
		return
	}

	notes, _ := body["notes"].(string)
	// Collapse literal newlines to spaces — notes are single-line in markdown.
	notes = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(notes)
	if len(notes) > maxNotesLen {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "notes too long (max 2000 chars)"})
		return
	}
	if strings.Contains(notes, "\\") {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "notes may not contain backslashes"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.loadBoard()
	if err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := b.SetNotes(taskID, notes); err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := b.Save(s.boardPath); err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.sendJSON(w, http.StatusOK, boardToAPIMap(b))
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if !s.hostOK(r) {
		s.forbiddenHost(w, r)
		return
	}
	if r.Method != http.MethodPost {
		s.sendJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body map[string]interface{}
	if !s.parseBody(w, r, &body) {
		return
	}

	taskID, _ := body["id"].(string)
	taskID = strings.TrimSpace(taskID)
	if !taskIDPattern.MatchString(taskID) {
		s.sendJSON(w, http.StatusBadRequest, map[string]string{"error": "bad task id"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := s.loadBoard()
	if err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := b.Delete(taskID); err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := b.Save(s.boardPath); err != nil {
		s.sendJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.sendJSON(w, http.StatusOK, boardToAPIMap(b))
}

// loadBoard opens and parses the board file. Must be called within s.mu.
func (s *server) loadBoard() (*Board, error) {
	f, err := os.Open(s.boardPath)
	if os.IsNotExist(err) {
		return Seed(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", s.boardPath, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// ---- small utilities --------------------------------------------------------

func isLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "::1", "localhost", "[::1]":
		return true
	}
	// Also accept 127.x.x.x range.
	return strings.HasPrefix(host, "127.")
}

func pathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}

// openBrowser attempts to open the URL in the default browser.
// Failures are silently ignored (belt-and-suspenders, same as Python).
func openBrowser(url string) {
	// Implemented per-platform in serve_browser_*.go if needed.
	// For now: no-op. The URL is printed to stderr so the operator can open it.
	_ = url
}
