package openai_test

// fixture_test.go: a real openai.Server on a real loopback port in front of the
// real dispatch.Service, with fake `claude` and `codex` CLIs on PATH. Nothing here
// touches the network beyond 127.0.0.1, the operator's state or a real model.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/gateway/openai"
	"github.com/bakw00ds/yakos/internal/modelreg"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// fakeClaude logs its argv, answers with two text deltas and a result frame, and
// can be told to hang (touch @HANG@) or fail (touch @FAIL@).
const fakeClaude = `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '@ARGV@'
echo started >> '@STARTED@'
if [ -f '@HANG@' ]; then exec sleep 30; fi
if [ -f '@FAIL@' ]; then echo "boom" >&2; exit 1; fi
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-claude-1","model":"claude-sonnet-4-5-20250929"}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"claude says "}}}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}}'
printf '%s\n' '{"type":"stream_event","event":{"type":"content_block_stop","index":0}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":100,"session_id":"sess-claude-1","total_cost_usd":0.25,"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}'
`

const fakeCodex = `#!/bin/sh
{ echo "--- call"; for a in "$@"; do printf '%s\n' "$a"; done; } >> '@ARGV@'
printf '%s\n' '{"type":"thread.started","thread_id":"thread-codex-1"}'
printf '%s\n' '{"type":"item.completed","item":{"id":"i","type":"agent_message","text":"codex says hi"}}'
printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":5}}'
`

type fixture struct {
	t                *testing.T
	srv              *openai.Server
	base             string
	port             string
	store            *consoleui.Transcripts
	logDir           string
	claudeLog        string
	codexLog         string
	hang, fail       string
	started          string
	signedIn         map[string]bool
	workspace, yroot string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("shell stubs")
	}
	dir := t.TempDir()
	f := &fixture{t: t, logDir: filepath.Join(dir, "log"), claudeLog: filepath.Join(dir, "claude.argv"),
		codexLog: filepath.Join(dir, "codex.argv"), hang: filepath.Join(dir, "hang"), fail: filepath.Join(dir, "fail"),
		started: filepath.Join(dir, "started"), signedIn: map[string]bool{"claude": true, "codex": true}}
	must(t, os.MkdirAll(f.logDir, 0o700))
	t.Setenv("YAKOS_DISPATCH_LOG", f.logDir)
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("USERPROFILE", filepath.Join(dir, "home"))
	t.Setenv("YAKOS_RUNTIME", "")
	t.Setenv("OPENAI_API_KEY", "sk-test-not-real")
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-not-real")

	bin := filepath.Join(dir, "bin")
	must(t, os.MkdirAll(bin, 0o755))
	repl := strings.NewReplacer("@ARGV@", f.claudeLog, "@STARTED@", f.started, "@HANG@", f.hang, "@FAIL@", f.fail)
	must(t, os.WriteFile(filepath.Join(bin, "claude"), []byte(repl.Replace(fakeClaude)), 0o755))                           //nolint:gosec
	must(t, os.WriteFile(filepath.Join(bin, "codex"), []byte(strings.ReplaceAll(fakeCodex, "@ARGV@", f.codexLog)), 0o755)) //nolint:gosec
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	f.workspace, f.yroot = filepath.Join(dir, "ws"), filepath.Join(dir, "yroot")
	agents := filepath.Join(f.yroot, "lib", "agents")
	must(t, os.MkdirAll(agents, 0o755))
	must(t, os.MkdirAll(f.workspace, 0o755))
	for _, name := range []string{"lead", "alpha"} {
		body := "---\nid: " + name + "\n---\n\n## Purpose\n\nTest agent " + name + ".\n"
		must(t, os.WriteFile(filepath.Join(agents, name+".md"), []byte(body), 0o644)) //nolint:gosec
	}

	f.store = consoleui.NewTranscripts(filepath.Join(dir, "work"))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	addr := ln.Addr().String()
	_, f.port, _ = net.SplitHostPort(addr)
	_ = ln.Close()

	svc := dispatch.NewService(dispatch.ServiceConfig{YakosRoot: f.yroot, WorkspaceRoot: f.workspace, OperatorID: "daemon"})
	srv, err := openai.New(openai.Config{
		Addr: addr, Token: func() string { return testToken }, Service: svc, Transcripts: f.store,
		YakosRoot: f.yroot, Workspace: f.workspace,
		Registry: func(string) (*modelreg.Registry, error) { return modelreg.Load(modelreg.Options{}) },
		SignedIn: func(_ context.Context, h string) bool { return f.signedIn[h] },
	})
	must(t, err)
	l2, err := srv.Listen()
	must(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ServeListener(ctx, l2) }()
	t.Cleanup(func() { cancel(); <-done })
	f.srv, f.base = srv, "http://"+addr
	return f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// do sends a request with the bearer token unless hdr overrides Authorization.
func (f *fixture) do(method, path string, body any, hdr map[string]string) (*http.Response, []byte) {
	f.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		must(f.t, err)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.base+path, rd)
	must(f.t, err)
	req.Header.Set("Authorization", "Bearer "+testToken)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	must(f.t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	must(f.t, err)
	return resp, raw
}

// chat posts a chat completion.
func (f *fixture) chat(model string, msgs []map[string]any, extra map[string]any, hdr map[string]string) (*http.Response, []byte) {
	body := map[string]any{"model": model, "messages": msgs}
	for k, v := range extra {
		body[k] = v
	}
	return f.do(http.MethodPost, "/v1/chat/completions", body, hdr)
}

func user(text string) map[string]any { return map[string]any{"role": "user", "content": text} }

func (f *fixture) decode(raw []byte) map[string]any {
	f.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		f.t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	return m
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// ledger returns the dispatch-log rows written so far.
func (f *fixture) ledger() []map[string]any {
	var out []map[string]any
	for _, l := range strings.Split(readFile(filepath.Join(f.logDir, "dispatch-log.ndjson")), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(l), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out
}
