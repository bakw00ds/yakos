package grpcserver_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/bakw00ds/yakos/internal/grpcserver"
	"github.com/bakw00ds/yakos/internal/wsbus"
	pb "github.com/bakw00ds/yakos/proto/yakos/v1"
)

// ---- helpers ----------------------------------------------------------------

const (
	testReadToken  = "test-read-token-abc123"
	testWriteToken = "test-write-token-def456"
)

// startTestServer creates a gRPC server on a random loopback port and returns
// a connected client connection.  The server runs for the duration of the test.
func startTestServer(t *testing.T, workspaceRoot string, bus *wsbus.Bus) *grpc.ClientConn {
	t.Helper()

	cfg := grpcserver.Config{
		ReadToken:       testReadToken,
		WriteToken:      testWriteToken,
		WorkspaceRoot:   workspaceRoot,
		Bus:             bus,
		DispatchService: grpcserver.NewDispatchServiceForTest(workspaceRoot, bus),
	}
	srv := grpcserver.New(cfg)

	// Listen on a random loopback port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go func() {
		_ = srv.ServeListener(ctx, ln)
	}()

	// Give the server a moment to start.
	time.Sleep(10 * time.Millisecond)

	conn, err := grpc.NewClient(
		ln.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(grpcserver.JSONCodec{})),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// ctxWithToken returns a context carrying the given Bearer token in gRPC metadata.
func ctxWithToken(ctx context.Context, token string) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token))
}

// ---- Kanban tests (15) ------------------------------------------------------

func TestKanban_List_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	resp, err := kc.List(ctxWithToken(context.Background(), testReadToken), &pb.KanbanListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if resp.Items == nil {
		t.Error("items should be non-nil (empty slice)")
	}
	if len(resp.Items) != 0 {
		t.Errorf("want 0 items; got %d", len(resp.Items))
	}
}

func TestKanban_Add_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// seed a kanban file
	kanbanDir := filepath.Join(dir, "work", "current")
	_ = os.MkdirAll(kanbanDir, 0755)

	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	resp, err := kc.Add(ctxWithToken(context.Background(), testWriteToken), &pb.KanbanAddRequest{Title: "Test task"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if resp.Id == "" {
		t.Error("expected non-empty id")
	}
}

func TestKanban_Add_EmptyTitle_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	_, err := kc.Add(ctxWithToken(context.Background(), testWriteToken), &pb.KanbanAddRequest{Title: ""})
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestKanban_Add_RequiresWriteToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	_, err := kc.Add(ctxWithToken(context.Background(), testReadToken), &pb.KanbanAddRequest{Title: "Should fail"})
	if err == nil {
		t.Fatal("expected auth error with read token on write endpoint")
	}
}

func TestKanban_List_RequiresReadToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	_, err := kc.List(context.Background(), &pb.KanbanListRequest{}) // no token
	if err == nil {
		t.Fatal("expected unauthenticated error with no token")
	}
}

func TestKanban_Add_Then_List(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	wctx := ctxWithToken(context.Background(), testWriteToken)
	rctx := ctxWithToken(context.Background(), testReadToken)

	addResp, err := kc.Add(wctx, &pb.KanbanAddRequest{Title: "Task A"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	listResp, err := kc.List(rctx, &pb.KanbanListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, it := range listResp.Items {
		if it.Id == addResp.Id {
			found = true
			if it.Column != "TODO" {
				t.Errorf("column=%q; want TODO", it.Column)
			}
		}
	}
	if !found {
		t.Errorf("added item %q not found in list", addResp.Id)
	}
}

func TestKanban_Move_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	wctx := ctxWithToken(context.Background(), testWriteToken)

	addResp, err := kc.Add(wctx, &pb.KanbanAddRequest{Title: "Moveable"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	moveResp, err := kc.Move(wctx, &pb.KanbanMoveRequest{Id: addResp.Id, To: "IN PROGRESS"})
	if err != nil {
		t.Fatalf("Move: %v", err)
	}
	if !moveResp.Ok {
		t.Error("Move returned ok=false")
	}
}

func TestKanban_Move_EmptyID_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	_, err := kc.Move(ctxWithToken(context.Background(), testWriteToken), &pb.KanbanMoveRequest{Id: "", To: "IN PROGRESS"})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestKanban_Done_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	wctx := ctxWithToken(context.Background(), testWriteToken)
	addResp, err := kc.Add(wctx, &pb.KanbanAddRequest{Title: "Completable"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	doneResp, err := kc.Done(wctx, &pb.KanbanDoneRequest{Id: addResp.Id})
	if err != nil {
		t.Fatalf("Done: %v", err)
	}
	if !doneResp.Ok {
		t.Error("Done returned ok=false")
	}
}

func TestKanban_Done_EmptyID_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	_, err := kc.Done(ctxWithToken(context.Background(), testWriteToken), &pb.KanbanDoneRequest{Id: ""})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestKanban_List_ColumnFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	wctx := ctxWithToken(context.Background(), testWriteToken)
	rctx := ctxWithToken(context.Background(), testReadToken)

	// Add a task and move it to DONE.
	addResp, err := kc.Add(wctx, &pb.KanbanAddRequest{Title: "Done task"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := kc.Done(wctx, &pb.KanbanDoneRequest{Id: addResp.Id}); err != nil {
		t.Fatalf("Done: %v", err)
	}

	// List only TODO — should not include the done item.
	listResp, err := kc.List(rctx, &pb.KanbanListRequest{Column: "TODO"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range listResp.Items {
		if it.Id == addResp.Id {
			t.Error("done item should not appear in TODO-filtered list")
		}
	}
}

func TestKanban_List_Limit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	wctx := ctxWithToken(context.Background(), testWriteToken)
	for i := 0; i < 5; i++ {
		if _, err := kc.Add(wctx, &pb.KanbanAddRequest{Title: "task"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	rctx := ctxWithToken(context.Background(), testReadToken)
	resp, err := kc.List(rctx, &pb.KanbanListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.Items) > 2 {
		t.Errorf("limit=2 but got %d items", len(resp.Items))
	}
}

func TestKanban_Watch_ReceivesAddEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	bus := wsbus.New()
	conn := startTestServer(t, dir, bus)
	kc := pb.NewKanbanClient(conn)

	wctx := ctxWithToken(context.Background(), testWriteToken)

	// Start watching.
	watchCtx, watchCancel := context.WithTimeout(wctx, 5*time.Second)
	defer watchCancel()
	watchStream, err := kc.Watch(watchCtx, &pb.KanbanWatchRequest{})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Allow the server-side bus subscription to register before publishing.
	// Without this, Ubuntu schedules the goroutines such that the Add publishes
	// before the watch loop subscribes, causing a Watch.Recv() deadline.
	// macOS happened to schedule favorably; Ubuntu CI didn't.
	time.Sleep(50 * time.Millisecond)

	// Add a task — this publishes to the bus.
	if _, err := kc.Add(wctx, &pb.KanbanAddRequest{Title: "Watched"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ev, err := watchStream.Recv()
	if err != nil {
		t.Fatalf("Watch Recv: %v", err)
	}
	if ev.Op != "added" {
		t.Errorf("op=%q; want added", ev.Op)
	}
}

func TestKanban_WriteToken_GrantsRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	kc := pb.NewKanbanClient(conn)

	// Write token must also be able to call read endpoints.
	_, err := kc.List(ctxWithToken(context.Background(), testWriteToken), &pb.KanbanListRequest{})
	if err != nil {
		t.Fatalf("write token should grant read access: %v", err)
	}
}

// ---- Cost tests (5) ---------------------------------------------------------

func TestCost_Aggregate_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	cc := pb.NewCostClient(conn)

	resp, err := cc.Aggregate(ctxWithToken(context.Background(), testReadToken), &pb.CostAggregateRequest{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if resp.By == "" {
		t.Error("expected non-empty By field")
	}
	if resp.Rows == nil {
		t.Error("rows should be non-nil")
	}
}

func TestCost_Aggregate_ByAgent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	cc := pb.NewCostClient(conn)

	resp, err := cc.Aggregate(ctxWithToken(context.Background(), testReadToken), &pb.CostAggregateRequest{By: "agent"})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if resp.By != "agent" {
		t.Errorf("by=%q; want agent", resp.By)
	}
}

func TestCost_Aggregate_InvalidBy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	cc := pb.NewCostClient(conn)

	_, err := cc.Aggregate(ctxWithToken(context.Background(), testReadToken), &pb.CostAggregateRequest{By: "invalid-axis"})
	if err == nil {
		t.Fatal("expected error for invalid by parameter")
	}
}

func TestCost_Aggregate_RequiresToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	cc := pb.NewCostClient(conn)

	_, err := cc.Aggregate(context.Background(), &pb.CostAggregateRequest{})
	if err == nil {
		t.Fatal("expected unauthenticated error")
	}
}

func TestCost_Aggregate_ByDay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	cc := pb.NewCostClient(conn)

	resp, err := cc.Aggregate(ctxWithToken(context.Background(), testReadToken), &pb.CostAggregateRequest{By: "day"})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if resp.By != "day" {
		t.Errorf("by=%q; want day", resp.By)
	}
}

// ---- Status tests (5) -------------------------------------------------------

func TestStatus_Read_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	sc := pb.NewStatusClient(conn)

	resp, err := sc.Read(ctxWithToken(context.Background(), testReadToken), &pb.StatusReadRequest{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	_ = resp // status may return zero values if no state exists
}

func TestStatus_Read_WithProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	sc := pb.NewStatusClient(conn)

	resp, err := sc.Read(ctxWithToken(context.Background(), testReadToken), &pb.StatusReadRequest{Project: "testproj"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	_ = resp
}

func TestStatus_Read_RequiresToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	sc := pb.NewStatusClient(conn)

	_, err := sc.Read(context.Background(), &pb.StatusReadRequest{})
	if err == nil {
		t.Fatal("expected unauthenticated error")
	}
}

func TestStatus_Read_WriteTokenGrantsAccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	sc := pb.NewStatusClient(conn)

	_, err := sc.Read(ctxWithToken(context.Background(), testWriteToken), &pb.StatusReadRequest{})
	if err != nil {
		t.Fatalf("write token should grant status read: %v", err)
	}
}

func TestStatus_Read_DefaultProject(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	sc := pb.NewStatusClient(conn)

	// WorkspaceRoot is dir whose base is the project name.
	resp, err := sc.Read(ctxWithToken(context.Background(), testReadToken), &pb.StatusReadRequest{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	_ = resp
}

// ---- Dispatch auth tests (5) ------------------------------------------------

func TestDispatch_Run_RequiresWriteToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	dc := pb.NewDispatchClient(conn)

	// Read token is insufficient for Dispatch.Run (write-level).
	_, err := dc.Run(ctxWithToken(context.Background(), testReadToken), &pb.DispatchRunRequest{Agent: "test", Task: "task"})
	if err == nil {
		t.Fatal("expected auth error with read token on dispatch endpoint")
	}
}

func TestDispatch_Run_NoToken_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	dc := pb.NewDispatchClient(conn)

	_, err := dc.Run(context.Background(), &pb.DispatchRunRequest{Agent: "test", Task: "task"})
	if err == nil {
		t.Fatal("expected auth error with no token")
	}
}

func TestDispatch_Run_EmptyAgent_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	dc := pb.NewDispatchClient(conn)

	_, err := dc.Run(ctxWithToken(context.Background(), testWriteToken), &pb.DispatchRunRequest{Agent: "", Task: "task"})
	if err == nil {
		t.Fatal("expected error for empty agent")
	}
}

func TestDispatch_Run_EmptyTask_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	dc := pb.NewDispatchClient(conn)

	_, err := dc.Run(ctxWithToken(context.Background(), testWriteToken), &pb.DispatchRunRequest{Agent: "backend", Task: ""})
	if err == nil {
		t.Fatal("expected error for empty task")
	}
}

func TestDispatch_Run_NoYakosRoot_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// YakosRoot is empty in cfg; dispatch.Run will fail
	conn := startTestServer(t, dir, wsbus.New()) // YakosRoot defaults to ""
	dc := pb.NewDispatchClient(conn)

	_, err := dc.Run(ctxWithToken(context.Background(), testWriteToken), &pb.DispatchRunRequest{Agent: "backend", Task: "do it"})
	if err == nil {
		t.Fatal("expected error when YakosRoot is empty")
	}
}

// ---- Refresh auth tests (3) -------------------------------------------------

func TestRefresh_Run_RequiresWriteToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	rc := pb.NewRefreshClient(conn)

	_, err := rc.Run(ctxWithToken(context.Background(), testReadToken), &pb.RefreshRunRequest{})
	if err == nil {
		t.Fatal("expected auth error with read token on refresh endpoint")
	}
}

func TestRefresh_Run_NoYakosRoot_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	rc := pb.NewRefreshClient(conn)

	_, err := rc.Run(ctxWithToken(context.Background(), testWriteToken), &pb.RefreshRunRequest{DryRun: true})
	if err == nil {
		t.Fatal("expected error when YakosRoot is empty")
	}
}

func TestRefresh_Run_NoToken_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	conn := startTestServer(t, dir, wsbus.New())
	rc := pb.NewRefreshClient(conn)

	_, err := rc.Run(context.Background(), &pb.RefreshRunRequest{})
	if err == nil {
		t.Fatal("expected auth error with no token")
	}
}

// ---- Bus event propagation (2) ----------------------------------------------

func TestKanban_Add_PublishesToBus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	bus := wsbus.New()
	sub := bus.Subscribe(wsbus.TopicKanbanAdded)
	defer sub.Unsubscribe()

	conn := startTestServer(t, dir, bus)
	kc := pb.NewKanbanClient(conn)

	if _, err := kc.Add(ctxWithToken(context.Background(), testWriteToken), &pb.KanbanAddRequest{Title: "Bus test"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	select {
	case ev := <-sub.C():
		if ev.Topic != wsbus.TopicKanbanAdded {
			t.Errorf("topic=%q; want %q", ev.Topic, wsbus.TopicKanbanAdded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no bus event received within 2s")
	}
}

func TestKanban_Move_PublishesToBus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "work", "current"), 0755)
	bus := wsbus.New()

	conn := startTestServer(t, dir, bus)
	kc := pb.NewKanbanClient(conn)

	wctx := ctxWithToken(context.Background(), testWriteToken)
	addResp, err := kc.Add(wctx, &pb.KanbanAddRequest{Title: "Move bus test"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	sub := bus.Subscribe(wsbus.TopicKanbanMoved)
	defer sub.Unsubscribe()

	if _, err := kc.Move(wctx, &pb.KanbanMoveRequest{Id: addResp.Id, To: "IN PROGRESS"}); err != nil {
		t.Fatalf("Move: %v", err)
	}

	select {
	case ev := <-sub.C():
		if ev.Topic != wsbus.TopicKanbanMoved {
			t.Errorf("topic=%q; want %q", ev.Topic, wsbus.TopicKanbanMoved)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no bus event received within 2s")
	}
}
