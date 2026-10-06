package consoleui_test

// chat_runtime_resolved_test.go — an "auto" pane learns where its agent really
// ran. The pane's runtime select says auto, the dispatcher picks the runtime
// (the agent's pin, the project default, a fallback), and the summary event of
// the SSE stream carries it as runtime_resolved, so the pane is never left
// showing a runtime the turn did not use (sec-324 F4). The chip that displays
// it is P3; the data is here.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
)

func TestChatDispatch_SummaryEventNamesTheRuntimeThatRan(t *testing.T) {
	root := routingYakosRoot(t)
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex", "agy"} {
		script := "#!/bin/sh\nprintf '%s\\n' '" + name + " says hi'\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	t.Setenv("OPENAI_API_KEY", "sk-test") // a signed-in codex, so the probe passes

	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: root, WorkspaceRoot: t.TempDir()})
	ts, tok, workDir, _ := newRoutingServer(t, root, svc)
	store := consoleui.NewTranscripts(workDir)

	cases := []struct{ agent, conv, want string }{
		{"general-codex", "conv-rr-codex", "codex"}, // an auto pane, an agent pinned to codex
		{"backend", "conv-rr-claude", "claude"},     // an auto pane, a plain agent
	}
	for _, tc := range cases {
		t.Run(tc.agent, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			resp, err := sseGetWithCancel(ctx, ts.URL+"/api/chat/stream?operatorId=alice", tok)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			events := make(chan map[string]any, 16)
			go func() {
				sc := bufio.NewScanner(resp.Body)
				sc.Buffer(make([]byte, 1<<20), 1<<20)
				for sc.Scan() {
					line := sc.Text()
					if !strings.HasPrefix(line, "data:") {
						continue
					}
					var ev map[string]any
					if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev) == nil {
						events <- ev
					}
				}
				close(events)
			}()

			sess := "s-" + tc.conv
			if code, body := postDispatch(t, ts, tok, map[string]any{"runtime": "auto", "agent": tc.agent, "conversationId": tc.conv, "sessionId": sess}); code != http.StatusAccepted {
				t.Fatalf("dispatch: %d %s", code, body)
			}
			deadline := time.After(15 * time.Second)
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						t.Fatal("the stream ended before a summary event")
					}
					if ev["type"] != "summary" || ev["session_id"] != sess {
						continue
					}
					if ev["runtime_resolved"] != tc.want {
						t.Errorf("summary runtime_resolved = %v, want %q (event: %v)", ev["runtime_resolved"], tc.want, ev)
					}
					waitForTurns(t, store, tc.conv, 1)
					return
				case <-deadline:
					t.Fatal("no summary event within the deadline")
				}
			}
		})
	}
}
