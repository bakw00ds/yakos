// Package grpcserver implements the yakOS daemon gRPC API.
//
// # Services
//
// Five services are exposed over a single gRPC listener:
//
//   - Dispatch.Run / Dispatch.Stream — run an agent; stream token output
//   - Kanban.List / Add / Move / Done / Watch — board CRUD + live events
//   - Cost.Aggregate — dispatch-log cost summary
//   - Status.Read — project status report
//   - Refresh.Run — detect and repair deployment drift
//
// # Authentication
//
// Two-token model: the read token grants access to read-only methods; the
// write token grants access to all methods.  Tokens are passed in the
// "authorization" gRPC metadata key as "Bearer <token>".
//
// # Wire encoding
//
// gRPC + JSON codec (registered via encoding.RegisterCodec).  This avoids the
// protoc toolchain dependency while keeping the standard gRPC framing, mTLS,
// and streaming semantics.  Callers must set the codec to "json" via
// grpc.WithDefaultCallOptions(grpc.ForceCodec(jsonCodec{})).
// See NewClientConn for a convenience constructor.
//
// # Stability: experimental
package grpcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/bakw00ds/yakos/internal/cost"
	"github.com/bakw00ds/yakos/internal/dispatch"
	iKanban "github.com/bakw00ds/yakos/internal/kanban"
	"github.com/bakw00ds/yakos/internal/refresh"
	iStatus "github.com/bakw00ds/yakos/internal/status"
	"github.com/bakw00ds/yakos/internal/wsbus"
	pb "github.com/bakw00ds/yakos/proto/yakos/v1"
)

func init() {
	// Register the JSON codec once at package init so both client and server pick
	// it up via the "json" content-subtype.
	encoding.RegisterCodec(jsonCodec{})
}

// JSONCodec is a gRPC encoding.Codec that marshals/unmarshals with encoding/json.
// Registered as content-subtype "json".  Exported so test packages can pass it
// to grpc.ForceCodec.
type JSONCodec = jsonCodec

// jsonCodec is the internal implementation; aliased by JSONCodec.
type jsonCodec struct{}

func (jsonCodec) Name() string { return "json" }

func (jsonCodec) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// Config holds all gRPC server configuration.
type Config struct {
	// Addr is the TCP address to listen on (e.g. "127.0.0.1:7893").
	Addr string

	// ReadToken grants access to read-only methods.
	ReadToken string

	// WriteToken grants access to all methods (also grants read).
	WriteToken string

	// WorkspaceRoot is the workspace path used for kanban resolution.
	WorkspaceRoot string

	// YakosRoot is the framework root used for refresh/dispatch.
	YakosRoot string

	// Bus is the in-process event bus used by Kanban.Watch.
	Bus *wsbus.Bus

	// ListenFn replaces net.Listen in tests.
	ListenFn func(network, addr string) (net.Listener, error)
}

// Server is the gRPC API server.
type Server struct {
	cfg    Config
	gSrv   *grpc.Server
}

// New creates a Server and registers all service implementations.
func New(cfg Config) *Server {
	s := &Server{cfg: cfg}

	// Build the gRPC server with the auth interceptor.
	s.gSrv = grpc.NewServer(
		grpc.UnaryInterceptor(s.unaryAuth),
		grpc.StreamInterceptor(s.streamAuth),
	)

	// Register all services.
	pb.RegisterDispatchServer(s.gSrv, &dispatchSrv{cfg: cfg})
	pb.RegisterKanbanServer(s.gSrv, &kanbanSrv{cfg: cfg})
	pb.RegisterCostServer(s.gSrv, &costSrv{cfg: cfg})
	pb.RegisterStatusServer(s.gSrv, &statusSrv{cfg: cfg})
	pb.RegisterRefreshServer(s.gSrv, &refreshSrv{cfg: cfg})

	return s
}

// GRPCServer returns the underlying *grpc.Server (for bufconn injection in tests).
func (s *Server) GRPCServer() *grpc.Server { return s.gSrv }

// Serve listens on cfg.Addr and blocks until ctx is cancelled or a fatal
// error occurs.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := s.listen()
	if err != nil {
		return fmt.Errorf("grpcserver: listen %s: %w", s.cfg.Addr, err)
	}

	// Shutdown when ctx is cancelled.
	go func() {
		<-ctx.Done()
		s.gSrv.GracefulStop()
	}()

	if err := s.gSrv.Serve(ln); err != nil && ctx.Err() == nil {
		return fmt.Errorf("grpcserver: serve: %w", err)
	}
	return nil
}

// ServeListener starts the gRPC server on the provided listener.
// Suitable for tests that inject a bufconn.Listener.
func (s *Server) ServeListener(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		s.gSrv.GracefulStop()
	}()
	if err := s.gSrv.Serve(ln); err != nil && ctx.Err() == nil {
		return fmt.Errorf("grpcserver: serve: %w", err)
	}
	return nil
}

func (s *Server) listen() (net.Listener, error) {
	listenFn := s.cfg.ListenFn
	if listenFn == nil {
		listenFn = net.Listen
	}
	addr := s.cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:7893"
	}
	return listenFn("tcp", addr)
}

// ---- auth interceptors -------------------------------------------------------

type authLevel int

const (
	authRead  authLevel = 1
	authWrite authLevel = 2
)

// methodAuthLevel returns the required auth level for a given full method name.
// Write methods are those that mutate state.
func methodAuthLevel(fullMethod string) authLevel {
	switch fullMethod {
	case "/yakos.v1.Kanban/Add",
		"/yakos.v1.Kanban/Move",
		"/yakos.v1.Kanban/Done",
		"/yakos.v1.Dispatch/Run",
		"/yakos.v1.Dispatch/Stream",
		"/yakos.v1.Refresh/Run":
		return authWrite
	default:
		return authRead
	}
}

// grantedLevel returns the auth level granted by the token, or 0 if not authorized.
func (s *Server) grantedLevel(token string) authLevel {
	if token == "" {
		return 0
	}
	if token == s.cfg.WriteToken {
		return authWrite
	}
	if token == s.cfg.ReadToken {
		return authRead
	}
	return 0
}

// bearerFromMD extracts the Bearer token from gRPC incoming metadata.
func bearerFromMD(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	v := vals[0]
	const prefix = "bearer "
	if len(v) > len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return v[len(prefix):]
	}
	return ""
}

func (s *Server) unaryAuth(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	required := methodAuthLevel(info.FullMethod)
	granted := s.grantedLevel(bearerFromMD(ctx))
	if granted < required {
		return nil, status.Errorf(codes.Unauthenticated, "grpcserver: missing or insufficient token")
	}
	return handler(ctx, req)
}

func (s *Server) streamAuth(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	required := methodAuthLevel(info.FullMethod)
	granted := s.grantedLevel(bearerFromMD(ss.Context()))
	if granted < required {
		return status.Errorf(codes.Unauthenticated, "grpcserver: missing or insufficient token")
	}
	return handler(srv, ss)
}

// ---- Dispatch service -------------------------------------------------------

type dispatchSrv struct {
	pb.UnimplementedDispatchServer
	cfg Config
}

func (d *dispatchSrv) Run(ctx context.Context, req *pb.DispatchRunRequest) (*pb.DispatchRunResponse, error) {
	if req.Agent == "" {
		return nil, status.Errorf(codes.InvalidArgument, "agent is required")
	}
	if req.Task == "" {
		return nil, status.Errorf(codes.InvalidArgument, "task is required")
	}
	yakosRoot := req.YakosRoot
	if yakosRoot == "" {
		yakosRoot = d.cfg.YakosRoot
	}
	if yakosRoot == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "yakos_root not configured")
	}
	project := req.Project
	if project == "" {
		project = d.cfg.WorkspaceRoot
	}

	dr := dispatch.Request{
		AgentName: req.Agent,
		Task:      req.Task,
		Project:   project,
		Runtime:   req.Runtime,
		Model:     req.Model,
		YakosRoot: yakosRoot,
		Timeout:   int(req.Timeout),
	}

	if d.cfg.Bus != nil {
		d.cfg.Bus.Publish(wsbus.TopicDispatchStarted, wsbus.DispatchStartedPayload{Agent: req.Agent, Project: project})
	}

	_, result, err := dispatch.Run(ctx, dr)
	if err != nil {
		if d.cfg.Bus != nil {
			d.cfg.Bus.Publish(wsbus.TopicDispatchFinished, wsbus.DispatchFinishedPayload{Agent: req.Agent, Project: project, ExitCode: -1})
		}
		return nil, status.Errorf(codes.Internal, "dispatch: %v", err)
	}

	if d.cfg.Bus != nil {
		d.cfg.Bus.Publish(wsbus.TopicDispatchFinished, wsbus.DispatchFinishedPayload{Agent: req.Agent, Project: project, ExitCode: result.ExitCode})
	}

	return &pb.DispatchRunResponse{
		ExitCode:      int32(result.ExitCode),
		DurationS:     result.DurationS,
		OutputBytes:   result.OutputBytes,
		ModelResolved: result.ModelResolved,
	}, nil
}

func (d *dispatchSrv) Stream(req *pb.DispatchRunRequest, stream pb.Dispatch_StreamServer) error {
	if req.Agent == "" {
		return status.Errorf(codes.InvalidArgument, "agent is required")
	}
	if req.Task == "" {
		return status.Errorf(codes.InvalidArgument, "task is required")
	}
	yakosRoot := req.YakosRoot
	if yakosRoot == "" {
		yakosRoot = d.cfg.YakosRoot
	}
	if yakosRoot == "" {
		return status.Errorf(codes.FailedPrecondition, "yakos_root not configured")
	}
	project := req.Project
	if project == "" {
		project = d.cfg.WorkspaceRoot
	}

	// Stream chunks: dispatch runs synchronously; we emit a summary chunk when done.
	// Full token streaming would require dispatch.Stream which is not wired yet;
	// for Phase 2 we run synchronously and send a single "summary" chunk.
	dr := dispatch.Request{
		AgentName: req.Agent,
		Task:      req.Task,
		Project:   project,
		Runtime:   req.Runtime,
		Model:     req.Model,
		YakosRoot: yakosRoot,
		Timeout:   int(req.Timeout),
	}

	if d.cfg.Bus != nil {
		d.cfg.Bus.Publish(wsbus.TopicDispatchStarted, wsbus.DispatchStartedPayload{Agent: req.Agent, Project: project})
	}

	_, result, err := dispatch.Run(stream.Context(), dr)
	if err != nil {
		if d.cfg.Bus != nil {
			d.cfg.Bus.Publish(wsbus.TopicDispatchFinished, wsbus.DispatchFinishedPayload{Agent: req.Agent, Project: project, ExitCode: -1})
		}
		return status.Errorf(codes.Internal, "dispatch: %v", err)
	}

	if d.cfg.Bus != nil {
		d.cfg.Bus.Publish(wsbus.TopicDispatchFinished, wsbus.DispatchFinishedPayload{Agent: req.Agent, Project: project, ExitCode: result.ExitCode})
	}

	return stream.Send(&pb.DispatchStreamChunk{
		Type:          "summary",
		ExitCode:      int32(result.ExitCode),
		DurationS:     result.DurationS,
		OutputBytes:   result.OutputBytes,
		ModelResolved: result.ModelResolved,
	})
}

// ---- Kanban service ---------------------------------------------------------

type kanbanSrv struct {
	pb.UnimplementedKanbanServer
	cfg Config
}

func (k *kanbanSrv) kanbanPath() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current", "kanban.md")
	}
	return filepath.Join(k.cfg.WorkspaceRoot, "work", "current", "kanban.md")
}

func (k *kanbanSrv) openBoard() (*iKanban.Board, error) {
	path := k.kanbanPath()
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return iKanban.Seed(), nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return iKanban.Parse(f)
}

func (k *kanbanSrv) openOrSeedBoard() (*iKanban.Board, error) {
	path := k.kanbanPath()
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr != nil { //nolint:gosec
				return nil, mkErr
			}
			return iKanban.Seed(), nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return iKanban.Parse(f)
}

func (k *kanbanSrv) List(ctx context.Context, req *pb.KanbanListRequest) (*pb.KanbanListResponse, error) {
	board, err := k.openBoard()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kanban: %v", err)
	}

	var items []*pb.KanbanItem
	collect := func(col string, titles []string) {
		if req.Column != "" && req.Column != col {
			return
		}
		for _, t := range titles {
			id, title := splitHeader(t)
			items = append(items, &pb.KanbanItem{Id: id, Title: title, Column: col})
		}
	}
	collect(iKanban.ColTODO, board.TODOItems)
	collect(iKanban.ColInProgress, board.InProgressItems)
	collect(iKanban.ColDone, board.DoneItems)

	if req.Limit > 0 && int32(len(items)) > req.Limit {
		items = items[:req.Limit]
	}
	if items == nil {
		items = []*pb.KanbanItem{}
	}
	return &pb.KanbanListResponse{Items: items}, nil
}

func (k *kanbanSrv) Add(ctx context.Context, req *pb.KanbanAddRequest) (*pb.KanbanAddResponse, error) {
	if req.Title == "" {
		return nil, status.Errorf(codes.InvalidArgument, "title is required")
	}
	board, err := k.openOrSeedBoard()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kanban: %v", err)
	}
	id := board.Add(req.Title, req.Category, req.Notes)
	if err := board.Save(k.kanbanPath()); err != nil {
		return nil, status.Errorf(codes.Internal, "kanban save: %v", err)
	}
	if k.cfg.Bus != nil {
		k.cfg.Bus.Publish(wsbus.TopicKanbanAdded, wsbus.KanbanAddedPayload{ID: id, Title: req.Title, Column: iKanban.ColTODO})
	}
	return &pb.KanbanAddResponse{Id: id}, nil
}

func (k *kanbanSrv) Move(ctx context.Context, req *pb.KanbanMoveRequest) (*pb.KanbanMoveResponse, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}
	col, err := iKanban.NormalizeColumn(req.To)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	board, err := k.openOrSeedBoard()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kanban: %v", err)
	}
	from := columnOfID(board, req.Id)
	if err := board.Move(req.Id, col); err != nil {
		return nil, status.Errorf(codes.NotFound, "kanban.move: %v", err)
	}
	if err := board.Save(k.kanbanPath()); err != nil {
		return nil, status.Errorf(codes.Internal, "kanban save: %v", err)
	}
	if k.cfg.Bus != nil {
		k.cfg.Bus.Publish(wsbus.TopicKanbanMoved, wsbus.KanbanMovedPayload{ID: req.Id, From: from, To: col})
	}
	return &pb.KanbanMoveResponse{Ok: true}, nil
}

func (k *kanbanSrv) Done(ctx context.Context, req *pb.KanbanDoneRequest) (*pb.KanbanDoneResponse, error) {
	if req.Id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "id is required")
	}
	board, err := k.openOrSeedBoard()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "kanban: %v", err)
	}
	from := columnOfID(board, req.Id)
	if err := board.Done(req.Id); err != nil {
		return nil, status.Errorf(codes.NotFound, "kanban.done: %v", err)
	}
	if err := board.Save(k.kanbanPath()); err != nil {
		return nil, status.Errorf(codes.Internal, "kanban save: %v", err)
	}
	if k.cfg.Bus != nil {
		k.cfg.Bus.Publish(wsbus.TopicKanbanMoved, wsbus.KanbanMovedPayload{ID: req.Id, From: from, To: iKanban.ColDone})
	}
	return &pb.KanbanDoneResponse{Ok: true}, nil
}

// Watch streams Kanban events until the client disconnects or the server shuts down.
// It subscribes to kanban.added and kanban.moved topics on the bus.
func (k *kanbanSrv) Watch(req *pb.KanbanWatchRequest, stream pb.Kanban_WatchServer) error {
	if k.cfg.Bus == nil {
		// No bus configured; block until client disconnects.
		<-stream.Context().Done()
		return nil
	}

	sub := k.cfg.Bus.Subscribe("") // all topics
	defer sub.Unsubscribe()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case ev, ok := <-sub.C():
			if !ok {
				return nil
			}
			var ke *pb.KanbanEvent
			switch ev.Topic {
			case wsbus.TopicKanbanAdded:
				var p wsbus.KanbanAddedPayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					continue
				}
				ke = &pb.KanbanEvent{Op: "added", Id: p.ID, Title: p.Title, To: p.Column}
			case wsbus.TopicKanbanMoved:
				var p wsbus.KanbanMovedPayload
				if err := json.Unmarshal(ev.Payload, &p); err != nil {
					continue
				}
				ke = &pb.KanbanEvent{Op: "moved", Id: p.ID, From: p.From, To: p.To}
			default:
				continue
			}
			if err := stream.Send(ke); err != nil {
				return err
			}
		}
	}
}

// ---- Cost service -----------------------------------------------------------

type costSrv struct {
	pb.UnimplementedCostServer
	cfg Config
}

func (c *costSrv) logDir() string {
	if v := os.Getenv("YAKOS_WORK_DIR"); v != "" {
		return filepath.Join(v, "current")
	}
	return filepath.Join(c.cfg.WorkspaceRoot, "work", "current")
}

func (c *costSrv) Aggregate(ctx context.Context, req *pb.CostAggregateRequest) (*pb.CostAggregateResponse, error) {
	by := req.By
	if by == "" {
		by = "runtime"
	}
	axis, err := cost.ParseAxis(by)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid by: %v", err)
	}

	files, err := cost.LogFiles(c.logDir())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "log glob: %v", err)
	}

	ch := cost.StreamFiles(files, req.Since)
	report := cost.Aggregate(ch, axis, int(req.Limit))

	resp := &pb.CostAggregateResponse{By: by}
	for _, r := range report.Rows {
		resp.Rows = append(resp.Rows, &pb.CostRow{
			Key:        r.Key,
			Dispatches: r.Count,
			TotalS:     r.TotalDurationS,
		})
	}
	if resp.Rows == nil {
		resp.Rows = []*pb.CostRow{}
	}
	return resp, nil
}

// ---- Status service ---------------------------------------------------------

type statusSrv struct {
	pb.UnimplementedStatusServer
	cfg Config
}

func (s *statusSrv) Read(ctx context.Context, req *pb.StatusReadRequest) (*pb.StatusReadResponse, error) {
	project := req.Project
	if project == "" {
		project = filepath.Base(s.cfg.WorkspaceRoot)
		if project == "." || project == "/" {
			project = "unknown"
		}
	}

	rpt, err := iStatus.Status(iStatus.Config{Project: project})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "status: %v", err)
	}

	// Marshal to JSON to extract fields from the untyped report.
	b, err := json.Marshal(rpt)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "status marshal: %v", err)
	}
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)

	resp := &pb.StatusReadResponse{Project: project}
	if v, ok := m["phase"].(string); ok {
		resp.Phase = v
	}
	if v, ok := m["summary"].(string); ok {
		resp.Summary = v
	}

	return resp, nil
}

// ---- Refresh service --------------------------------------------------------

type refreshSrv struct {
	pb.UnimplementedRefreshServer
	cfg Config
}

func (r *refreshSrv) Run(ctx context.Context, req *pb.RefreshRunRequest) (*pb.RefreshRunResponse, error) {
	if r.cfg.YakosRoot == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "yakos_root not configured")
	}

	home := os.Getenv("HOME")
	projects := refresh.CollectProjects(home)
	if len(projects) == 0 && r.cfg.WorkspaceRoot != "" {
		projects = []string{r.cfg.WorkspaceRoot}
	}

	var out strings.Builder
	rcfg := refresh.Config{
		YakosRoot:    r.cfg.YakosRoot,
		ProjectPaths: projects,
		DryRun:       req.DryRun,
		Writer:       &out,
		ErrWriter:    &out,
	}
	if _, err := refresh.Run(rcfg); err != nil {
		return nil, status.Errorf(codes.Internal, "refresh: %v", err)
	}
	return &pb.RefreshRunResponse{Output: out.String()}, nil
}

// ---- helpers ----------------------------------------------------------------

// splitHeader splits "K-N — title" into (id, title).
func splitHeader(s string) (id, title string) {
	const emDash = "\xe2\x80\x94"
	// Strip checkbox prefix "[ ] " / "[x] ".
	s = strings.TrimPrefix(s, "[ ] ")
	s = strings.TrimPrefix(s, "[x] ")
	s = strings.TrimPrefix(s, "[X] ")
	if idx := strings.Index(s, " "+emDash+" "); idx >= 0 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+len(" "+emDash+" "):])
	}
	parts := strings.SplitN(s, " ", 2)
	if len(parts) == 2 {
		return parts[0], strings.TrimSpace(parts[1])
	}
	return "", s
}

// columnOfID finds the column that holds a task with the given ID prefix.
func columnOfID(board *iKanban.Board, id string) string {
	prefix := id + " "
	matches := func(items []string) bool {
		for _, t := range items {
			if strings.HasPrefix(t, prefix) || t == id {
				return true
			}
		}
		return false
	}
	if matches(board.TODOItems) {
		return iKanban.ColTODO
	}
	if matches(board.InProgressItems) {
		return iKanban.ColInProgress
	}
	if matches(board.DoneItems) {
		return iKanban.ColDone
	}
	return ""
}

