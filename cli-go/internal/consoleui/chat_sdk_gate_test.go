package consoleui_test

// chat_sdk_gate_test.go — K-137: selecting the SDK engine without an API key
// must fail loudly in the console, never start the sidecar, and never fall back
// to another engine or to the operator's claude.ai login.
//
// The browser sees the failure two ways, both asserted here: the live SSE
// "error" frame and the persisted transcript entry that a reload shows.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/interactive"
	"github.com/bakw00ds/yakos/internal/wsbus"
)

func TestStructuredQuestions_StartWithoutAPIKeyIsSurfacedAndNothingIsSpawned(t *testing.T) {
	// No API key; an OAuth login in the environment must not stand in for one.
	if old, had := os.LookupEnv("ANTHROPIC_API_KEY"); had {
		_ = os.Unsetenv("ANTHROPIC_API_KEY")
		t.Cleanup(func() { _ = os.Setenv("ANTHROPIC_API_KEY", old) })
	}
	const secret = "CONSOLEGATESECRET9876543210"
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "sk-ant-oat01-"+secret)

	stateDir := t.TempDir()
	tok, err := consoleui.LoadOrCreateToken(stateDir)
	if err != nil {
		t.Fatalf("LoadOrCreateToken: %v", err)
	}
	bus := wsbus.New()
	t.Cleanup(bus.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := interactive.NewManager(ctx, interactive.ManagerConfig{Cap: 4})
	t.Cleanup(mgr.Stop)

	// A real SDKEngine whose command provider counts how often a process would
	// have been spawned.
	var spawns atomic.Int32
	var factory interactive.SDKEngineFactory = func(p interactive.SDKEngineParams) (*interactive.SDKEngine, error) {
		return interactive.NewSDKEngineWithProvider(p, func() *exec.Cmd {
			spawns.Add(1)
			return exec.Command(os.Args[0], "-test.run=^$")
		})
	}
	srv := consoleui.MustNew(t, consoleui.Config{
		Token:              tok,
		KanbanBoardPath:    t.TempDir() + "/kanban.md",
		KanbanProject:      "test",
		MetricsProjectDir:  t.TempDir(),
		PerfWorkDir:        t.TempDir(),
		Bus:                bus,
		WorkDir:            t.TempDir(),
		DispatchService:    dispatch.NewService(dispatch.ServiceConfig{WorkspaceRoot: t.TempDir()}),
		InteractiveManager: mgr,
		SDKEngineFactory:   &factory,
	})
	ts := httptest.NewServer(consoleui.RequireTokenForNonStatic(tok,
		consoleui.RequireJSONForMutations(srv.HandlerForTest())))
	t.Cleanup(ts.Close)

	// Watch the live stream before dispatching.
	sseCtx, sseCancel := context.WithTimeout(ctx, 20*time.Second)
	defer sseCancel()
	sse, err := sseGetWithCancel(sseCtx, ts.URL+"/api/chat/stream?operatorId=alice", tok)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer sse.Body.Close()
	frames := make(chan consoleui.SSEEvent, 16)
	go func() {
		sc := bufio.NewScanner(sse.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var ev consoleui.SSEEvent
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) == nil {
				frames <- ev
			}
		}
	}()

	body, _ := json.Marshal(map[string]any{
		"agent":               "claude",
		"task":                "hi",
		"sessionId":           "sess-sdk-gate",
		"conversationId":      "conv-sdk-gate",
		"operatorId":          "alice",
		"interactive":         true,
		"structuredQuestions": true,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/api/chat/dispatch", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("dispatch status = %d, want 202 (the failure is reported on the stream)", resp.StatusCode)
	}

	check := func(where, text string) {
		t.Helper()
		for _, want := range []string{"ANTHROPIC_API_KEY", "CLI engine"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s must name %q so the operator knows what to do: %q", where, want, text)
			}
		}
		if strings.Contains(text, secret) {
			t.Errorf("%s echoed token material: %q", where, text)
		}
	}

	// 1. The live SSE error frame.
	select {
	case ev := <-frames:
		if ev.Type != "error" {
			t.Fatalf("first SSE frame type = %q, want error (no silent fallback to another engine)", ev.Type)
		}
		check("the SSE error frame", ev.Text)
	case <-time.After(10 * time.Second):
		t.Fatal("no SSE error frame within 10s: the operator would see nothing")
	}

	// 2. The persisted transcript entry a page reload shows.
	deadline := time.Now().Add(10 * time.Second)
	for {
		tr := get(t, ts.URL+"/api/chat/transcript?conversationId=conv-sdk-gate&operatorId=alice", tok)
		var entries []consoleui.TranscriptEntry
		_ = json.NewDecoder(tr.Body).Decode(&entries)
		tr.Body.Close()
		var errText string
		for _, e := range entries {
			if e.Role == consoleui.RoleError {
				errText = e.Text
			}
		}
		if errText != "" {
			check("the transcript error entry", errText)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no error entry in the transcript within 10s: %+v", entries)
		}
		time.Sleep(25 * time.Millisecond)
	}

	if n := spawns.Load(); n != 0 {
		t.Errorf("the sidecar command was built %d time(s) although the key gate refused", n)
	}
}
