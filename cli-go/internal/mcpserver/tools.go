// tools.go — MCP tool registry.
//
// Each tool has:
//   - name: yakos.<domain>.<verb>
//   - description: what the tool does and its idempotency contract
//   - inputSchema: JSON Schema (draft 7 compatible) for the arguments object
//   - handler: wraps the corresponding internal/* package function
//
// Error semantics per §5 of the Phase 2 design:
//
//	Protocol errors (parse fail, method not found) → JSON-RPC error envelope.
//	Tool execution failures → ToolsCallResult with IsError:true.
//	Success → ToolsCallResult with content text.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/refresh"
	"github.com/bakw00ds/yakos/internal/supervise"
)

// toolHandler is a function that executes a tool call.
// args is the raw JSON arguments object (or null). Returns a ToolsCallResult.
type toolHandler func(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult

// tool is the registry entry for one MCP tool.
type tool struct {
	def     ToolDefinition
	handler toolHandler
}

// registry returns all MCP tools in Phase 2 first-cut order.
// The slice order determines tools/list output.
func registry() []tool {
	return []tool{
		toolDispatch(),
		toolKanbanList(),
		toolKanbanAdd(),
		toolKanbanMove(),
		toolKanbanDone(),
		toolRefresh(),
		toolSuperviseRun(),
		toolSuperviseAck(),
	}
}

// ---- JSON Schema helpers ----------------------------------------------------

// must panics if json.RawMessage construction fails (compile-time constant strings only).
func mustSchema(s string) json.RawMessage {
	return json.RawMessage(s)
}

// ---- yakos.dispatch ----------------------------------------------------------

func toolDispatch() tool {
	return tool{
		def: ToolDefinition{
			Name: "yakos.dispatch",
			Description: `Invoke a yakOS subagent to perform a task. Each call spawns a new agent process.
NOT idempotent — each call creates a new dispatch.
Idempotency-Key: not supported for this tool (each invocation is intentionally unique).`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "agent":    {"type": "string", "description": "Agent identifier (e.g. 'backend', 'security-reviewer')"},
    "task":     {"type": "string", "description": "Full task prompt passed to the agent"},
    "project":  {"type": "string", "description": "Absolute path to the project repository"},
    "runtime":  {"type": "string", "enum": ["claude","codex","gemini","agy"], "description": "Runtime override (empty = resolve from agent frontmatter)"},
    "model":    {"type": "string", "enum": ["haiku","sonnet","opus"], "description": "Model tier override"},
    "timeout":  {"type": "integer", "minimum": 0, "description": "Timeout in seconds (0 = 600s default)"}
  },
  "required": ["agent", "task"],
  "additionalProperties": false
}`),
		},
		handler: handleDispatch,
	}
}

type dispatchArgs struct {
	Agent   string `json:"agent"`
	Task    string `json:"task"`
	Project string `json:"project"`
	Runtime string `json:"runtime,omitempty"`
	Model   string `json:"model,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

func handleDispatch(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p dispatchArgs
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return errorContent(fmt.Sprintf("yakos.dispatch: invalid arguments: %v", err))
	}
	if p.Agent == "" {
		return errorContent("yakos.dispatch: agent is required")
	}
	if p.Task == "" {
		return errorContent("yakos.dispatch: task is required")
	}
	if cfg.YakosRoot == "" {
		return errorContent("yakos.dispatch: yakos_root not configured (daemon Config.YakosRoot empty)")
	}

	svc := cfg.DispatchService
	if svc == nil {
		// The daemon must inject the shared Service via Config.DispatchService.
		// A nil Service means this MCP session was started without the shared
		// governor — dispatch is unavailable.
		return errorContent("yakos.dispatch: dispatch service not configured (daemon DispatchService is nil)")
	}

	// MCP-originated dispatches are attributed as operator_id="mcp:<agent>"
	// per the unified-console-plan.md §"Ideas to add" convention. This lets
	// the attribution appear in the dispatch-log NDJSON and the WS event feed.
	// MCPParams sets the internal isMCPStamped flag so the "mcp:" prefix is
	// accepted by the Service (it is a reserved namespace for non-MCP transports).
	_, result, err := svc.Run(ctx, dispatch.MCPParams(dispatch.Params{
		Agent:      p.Agent,
		Task:       p.Task,
		Project:    p.Project, // empty → Service uses WorkspaceRoot
		Runtime:    p.Runtime,
		Model:      p.Model,
		Timeout:    p.Timeout,
		OperatorID: "mcp:" + p.Agent,
	}))
	if err != nil {
		return errorContent(fmt.Sprintf("yakos.dispatch: %v", err))
	}

	text := fmt.Sprintf(`{"exit_code":%d,"duration_s":%.2f,"output_bytes":%d,"model_resolved":%q}`,
		result.ExitCode, result.DurationS, result.OutputBytes, result.ModelResolved)
	return ToolsCallResult{Content: textContent(text)}
}

// ---- yakos.kanban.list -------------------------------------------------------

func toolKanbanList() tool {
	return tool{
		def: ToolDefinition{
			Name:        "yakos.kanban.list",
			Description: `List kanban board items. Idempotent (read-only).`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "column": {"type": "string", "enum": ["TODO","IN PROGRESS","DONE"], "description": "Filter by column (empty = all columns)"},
    "limit":  {"type": "integer", "minimum": 1, "description": "Maximum items to return (0 = no limit)"}
  },
  "additionalProperties": false
}`),
		},
		handler: handleKanbanList,
	}
}

type kanbanListArgs struct {
	Column string `json:"column,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func handleKanbanList(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p kanbanListArgs
	if len(args) > 0 && string(args) != "null" {
		dec := json.NewDecoder(strings.NewReader(string(args)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return errorContent(fmt.Sprintf("yakos.kanban.list: invalid arguments: %v", err))
		}
	}

	board, err := openBoard(cfg.WorkspaceRoot)
	if err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.list: %v", err))
	}

	type item struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Column string `json:"column"`
	}

	var items []item
	collect := func(col string, titles []string) {
		if p.Column != "" && p.Column != col {
			return
		}
		for _, t := range titles {
			// Extract ID from "K-N — title" format.
			id, title := splitTaskHeader(t)
			items = append(items, item{ID: id, Title: title, Column: col})
		}
	}
	collect(kanban.ColTODO, board.TODOItems)
	collect(kanban.ColInProgress, board.InProgressItems)
	collect(kanban.ColDone, board.DoneItems)

	if p.Limit > 0 && len(items) > p.Limit {
		items = items[:p.Limit]
	}

	out := struct {
		Items []item `json:"items"`
	}{Items: items}
	if out.Items == nil {
		out.Items = []item{}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return ToolsCallResult{Content: textContent(string(b))}
}

// splitTaskHeader splits "K-N — title" into (id, title).
// If the format is unrecognized, the whole string is returned as the title.
func splitTaskHeader(s string) (id, title string) {
	const emDash = "\xe2\x80\x94"
	if idx := strings.Index(s, " "+emDash+" "); idx >= 0 {
		id = strings.TrimSpace(s[:idx])
		title = strings.TrimSpace(s[idx+len(" "+emDash+" "):])
		return id, title
	}
	// Fallback: first token as id.
	parts := strings.SplitN(s, " ", 2)
	if len(parts) == 2 {
		return parts[0], strings.TrimSpace(parts[1])
	}
	return "", s
}

// ---- yakos.kanban.add -------------------------------------------------------

func toolKanbanAdd() tool {
	return tool{
		def: ToolDefinition{
			Name: "yakos.kanban.add",
			Description: `Add a new task to the TODO column. Conditionally idempotent when id is pre-supplied (same id → no-op if already present).
Idempotency-Key: supply id to make idempotent.`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "title":    {"type": "string", "description": "Task title (required)"},
    "category": {"type": "string", "description": "Category tag (default: 'other')"},
    "notes":    {"type": "string", "description": "Initial notes (single-line)"}
  },
  "required": ["title"],
  "additionalProperties": false
}`),
		},
		handler: handleKanbanAdd,
	}
}

type kanbanAddArgs struct {
	Title    string `json:"title"`
	Category string `json:"category,omitempty"`
	Notes    string `json:"notes,omitempty"`
}

func handleKanbanAdd(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p kanbanAddArgs
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.add: invalid arguments: %v", err))
	}
	if p.Title == "" {
		return errorContent("yakos.kanban.add: title is required")
	}

	boardPath := kanbanPath(cfg.WorkspaceRoot)
	board, err := loadOrSeedBoard(boardPath)
	if err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.add: %v", err))
	}

	id := board.Add(p.Title, p.Category, p.Notes)

	if err := board.Save(boardPath); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.add: save: %v", err))
	}

	return ToolsCallResult{Content: textContent(fmt.Sprintf(`{"id":%q}`, id))}
}

// ---- yakos.kanban.move -------------------------------------------------------

func toolKanbanMove() tool {
	return tool{
		def: ToolDefinition{
			Name:        "yakos.kanban.move",
			Description: `Move a task to a different column. Idempotent (moving to the same column is a no-op).`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "Task ID (e.g. K-3)"},
    "to": {"type": "string", "enum": ["TODO","IN PROGRESS","DONE"], "description": "Target column"}
  },
  "required": ["id", "to"],
  "additionalProperties": false
}`),
		},
		handler: handleKanbanMove,
	}
}

type kanbanMoveArgs struct {
	ID string `json:"id"`
	To string `json:"to"`
}

func handleKanbanMove(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p kanbanMoveArgs
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.move: invalid arguments: %v", err))
	}
	if p.ID == "" {
		return errorContent("yakos.kanban.move: id is required")
	}

	col, err := kanban.NormalizeColumn(p.To)
	if err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.move: %v", err))
	}

	boardPath := kanbanPath(cfg.WorkspaceRoot)
	board, err := openBoard(cfg.WorkspaceRoot)
	if err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.move: %v", err))
	}

	if err := board.Move(p.ID, col); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.move: %v", err))
	}

	if err := board.Save(boardPath); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.move: save: %v", err))
	}

	return ToolsCallResult{Content: textContent(`{"ok":true}`)}
}

// ---- yakos.kanban.done -------------------------------------------------------

func toolKanbanDone() tool {
	return tool{
		def: ToolDefinition{
			Name:        "yakos.kanban.done",
			Description: `Move a task to the DONE column. Idempotent (already-done task is a no-op).`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "id": {"type": "string", "description": "Task ID (e.g. K-3)"}
  },
  "required": ["id"],
  "additionalProperties": false
}`),
		},
		handler: handleKanbanDone,
	}
}

type kanbanDoneArgs struct {
	ID string `json:"id"`
}

func handleKanbanDone(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p kanbanDoneArgs
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.done: invalid arguments: %v", err))
	}
	if p.ID == "" {
		return errorContent("yakos.kanban.done: id is required")
	}

	boardPath := kanbanPath(cfg.WorkspaceRoot)
	board, err := openBoard(cfg.WorkspaceRoot)
	if err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.done: %v", err))
	}

	if err := board.Done(p.ID); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.done: %v", err))
	}

	if err := board.Save(boardPath); err != nil {
		return errorContent(fmt.Sprintf("yakos.kanban.done: save: %v", err))
	}

	return ToolsCallResult{Content: textContent(`{"ok":true}`)}
}

// ---- yakos.refresh -----------------------------------------------------------

func toolRefresh() tool {
	return tool{
		def: ToolDefinition{
			Name: "yakos.refresh",
			Description: `Detect and repair deployment drift: hook scripts, settings.json registrations, and agent symlinks.
Idempotent when dryRun=true (read-only). Non-idempotent otherwise (touches symlinks and files).`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "dryRun": {"type": "boolean", "description": "Report changes without writing (default: false)"}
  },
  "additionalProperties": false
}`),
		},
		handler: handleRefresh,
	}
}

type refreshArgs struct {
	DryRun bool `json:"dryRun,omitempty"`
}

func handleRefresh(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p refreshArgs
	if len(args) > 0 && string(args) != "null" {
		dec := json.NewDecoder(strings.NewReader(string(args)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return errorContent(fmt.Sprintf("yakos.refresh: invalid arguments: %v", err))
		}
	}

	if cfg.YakosRoot == "" {
		return errorContent("yakos.refresh: yakos_root not configured")
	}

	// Collect project paths from $HOME/agent-control.
	home := os.Getenv("HOME")
	projects := refresh.CollectProjects(home)
	if len(projects) == 0 && cfg.WorkspaceRoot != "" {
		projects = []string{cfg.WorkspaceRoot}
	}

	var out strings.Builder
	rcfg := refresh.Config{
		YakosRoot:    cfg.YakosRoot,
		ProjectPaths: projects,
		DryRun:       p.DryRun,
		Writer:       &out,
		ErrWriter:    &out,
	}
	if _, rErr := refresh.Run(rcfg); rErr != nil {
		return errorContent(fmt.Sprintf("yakos.refresh: %v", rErr))
	}
	return ToolsCallResult{Content: textContent(out.String())}
}

// ---- yakos.supervise.run -----------------------------------------------------

func toolSuperviseRun() tool {
	return tool{
		def: ToolDefinition{
			Name:        "yakos.supervise.run",
			Description: `List supervisor findings for the project. Idempotent (read-only).`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "project": {"type": "string", "description": "Project slug (empty = infer from workspace)"},
    "scope":   {"type": "string", "enum": ["recent","full"], "description": "Tail recent (default 10) or full findings list"}
  },
  "additionalProperties": false
}`),
		},
		handler: handleSuperviseRun,
	}
}

type superviseRunArgs struct {
	Project string `json:"project,omitempty"`
	Scope   string `json:"scope,omitempty"`
}

func handleSuperviseRun(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p superviseRunArgs
	if len(args) > 0 && string(args) != "null" {
		dec := json.NewDecoder(strings.NewReader(string(args)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return errorContent(fmt.Sprintf("yakos.supervise.run: invalid arguments: %v", err))
		}
	}

	tailN := 10
	if p.Scope == "full" {
		tailN = 0 // 0 means all in supervise.tail semantics
	}

	var out strings.Builder
	scfg := supervise.Config{
		Subcommand: "tail",
		Project:    p.Project,
		TailN:      tailN,
		Writer:     &out,
		ErrWriter:  &out,
		HomeDir:    os.Getenv("HOME"),
	}
	if _, err := supervise.Run(scfg); err != nil {
		return errorContent(fmt.Sprintf("yakos.supervise.run: %v", err))
	}
	return ToolsCallResult{Content: textContent(out.String())}
}

// ---- yakos.supervise.ack -----------------------------------------------------

func toolSuperviseAck() tool {
	return tool{
		def: ToolDefinition{
			Name:        "yakos.supervise.ack",
			Description: `Acknowledge a supervisor finding by its finding ID. Idempotent (re-acking is safe).`,
			InputSchema: mustSchema(`{
  "type": "object",
  "properties": {
    "finding_id": {"type": "string", "description": "Finding ID (e.g. f-2026-06-01T10-00-00Z-myproject-3)"},
    "project":    {"type": "string", "description": "Project slug (empty = infer from workspace)"},
    "note":       {"type": "string", "description": "Optional acknowledgement note"}
  },
  "required": ["finding_id"],
  "additionalProperties": false
}`),
		},
		handler: handleSuperviseAck,
	}
}

type superviseAckArgs struct {
	FindingID string `json:"finding_id"`
	Project   string `json:"project,omitempty"`
	Note      string `json:"note,omitempty"`
}

func handleSuperviseAck(ctx context.Context, cfg Config, args json.RawMessage) ToolsCallResult {
	var p superviseAckArgs
	dec := json.NewDecoder(strings.NewReader(string(args)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return errorContent(fmt.Sprintf("yakos.supervise.ack: invalid arguments: %v", err))
	}
	if p.FindingID == "" {
		return errorContent("yakos.supervise.ack: finding_id is required")
	}

	var out strings.Builder
	scfg := supervise.Config{
		Subcommand: "ack",
		Project:    p.Project,
		FindingID:  p.FindingID,
		Note:       p.Note,
		Writer:     &out,
		ErrWriter:  &out,
		HomeDir:    os.Getenv("HOME"),
	}
	if _, err := supervise.Run(scfg); err != nil {
		return errorContent(fmt.Sprintf("yakos.supervise.ack: %v", err))
	}
	return ToolsCallResult{Content: textContent(out.String())}
}

// ---- board helpers -----------------------------------------------------------

// kanbanPath returns the canonical kanban.md path for workspaceRoot.
func kanbanPath(workspaceRoot string) string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current", "kanban.md")
	}
	return filepath.Join(workspaceRoot, "work", "current", "kanban.md")
}

// openBoard opens and parses the kanban.md for workspaceRoot.
// Returns an empty seeded board if the file does not exist.
func openBoard(workspaceRoot string) (*kanban.Board, error) {
	path := kanbanPath(workspaceRoot)
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return kanban.Seed(), nil
		}
		return nil, fmt.Errorf("open kanban %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return kanban.Parse(f)
}

// loadOrSeedBoard opens the board or creates a seeded in-memory board if
// the file does not exist.  Unlike openBoard it also ensures the parent
// directory exists before Save is called.
func loadOrSeedBoard(path string) (*kanban.Board, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			// Ensure parent directory exists.
			if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil { //nolint:gosec
				return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), mkErr)
			}
			return kanban.Seed(), nil
		}
		return nil, fmt.Errorf("open kanban %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return kanban.Parse(f)
}

// costLogDir returns the dispatch-log directory for workspaceRoot.
func costLogDir(workspaceRoot string) string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current")
	}
	return filepath.Join(workspaceRoot, "work", "current")
}

// aggregateCost reads the dispatch log and returns a JSON text summary.
func aggregateCost(workspaceRoot string, by string) string {
	if by == "" {
		by = "runtime"
	}
	axis, err := cost.ParseAxis(by)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	dir := costLogDir(workspaceRoot)
	files, err := cost.LogFiles(dir)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	ch := cost.StreamFiles(files, "")
	report := cost.Aggregate(ch, axis, 0)
	b, _ := json.MarshalIndent(report, "", "  ")
	return string(b)
}
