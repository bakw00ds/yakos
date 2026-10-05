package dispatch

// K-132: console chat continuity and the routing fields in the dispatch record.
//
// A one-shot claude chat turn used to start a fresh `claude -p` with no memory
// of the previous turn, in the daemon's working directory. Now the first turn's
// session_id is surfaced on the summary chunk, a later turn can pass it back
// (Params.ResumeSessionID) and the subprocess runs in the project directory.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	rt "github.com/bakw00ds/yakos/internal/runtime"
)

const resultLineWithSession = `{"type":"result","subtype":"success","result":"ok","session_id":"sess-claude-1","total_cost_usd":0.001,"usage":{"input_tokens":3,"output_tokens":2}}`

func TestParseAndDispatch_ResultLineCarriesSessionID(t *testing.T) {
	ps := NewStreamParserState()
	noop := func(StreamChunk) {}

	// Non-result lines never carry one, even when claude stamps them with it.
	for _, line := range []string{
		`{"type":"system","subtype":"init","session_id":"sess-claude-1"}`,
		`{"type":"assistant","session_id":"sess-claude-1","message":{"content":[]}}`,
		`{"type":"stream_event","session_id":"sess-claude-1","event":{"type":"message_start"}}`,
	} {
		if got := ParseAndDispatch([]byte(line), ps, noop); got.SessionID != "" {
			t.Errorf("line %q: SessionID = %q, want none", line, got.SessionID)
		}
	}

	res := ParseAndDispatch([]byte(resultLineWithSession), ps, noop)
	if !res.IsResult || res.SessionID != "sess-claude-1" {
		t.Errorf("result line: IsResult=%v SessionID=%q, want true / sess-claude-1", res.IsResult, res.SessionID)
	}

	// A malformed id is dropped rather than stored.
	bad := ParseAndDispatch([]byte(`{"type":"result","session_id":"--model"}`), ps, noop)
	if !bad.IsResult || bad.SessionID != "" {
		t.Errorf("malformed id: IsResult=%v SessionID=%q, want result with no id", bad.IsResult, bad.SessionID)
	}
}

// fakeClaudeRecorder puts a `claude` stub on PATH that records its argv and
// working directory, then emits one result frame.
func fakeClaudeRecorder(t *testing.T) (argvFile, pwdFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	bin := t.TempDir()
	scratch := t.TempDir()
	argvFile = filepath.Join(scratch, "argv.txt")
	pwdFile = filepath.Join(scratch, "pwd.txt")
	script := "#!/bin/sh\n: > '" + argvFile + "'\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + argvFile + "'; done\n" +
		"pwd -P > '" + pwdFile + "'\nprintf '%s\\n' '" + resultLineWithSession + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YAKOS_ROOT", "")
	return argvFile, pwdFile
}

func collectChunks(t *testing.T, svc *Service, p Params) []StreamChunk {
	t.Helper()
	var chunks []StreamChunk
	if _, err := svc.RunStream(context.Background(), p, func(c StreamChunk) { chunks = append(chunks, c) }); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	return chunks
}

// The summary chunk tells the chat handler which runtime ran and which native
// session to resume next time.
func TestRunStream_SummaryCarriesRuntimeAndNativeSession(t *testing.T) {
	root := routingRoot(t)
	fakeClaudeRecorder(t)
	isolatedLogDir(t)
	svc := newResolutionSvc(t, root)

	chunks := collectChunks(t, svc, Params{Agent: "plain", Task: "hi", Project: t.TempDir()})
	sum := chunks[len(chunks)-1]
	if sum.Type != "summary" {
		t.Fatalf("last chunk is %q, want summary", sum.Type)
	}
	if sum.NativeSessionID != "sess-claude-1" {
		t.Errorf("NativeSessionID = %q, want sess-claude-1", sum.NativeSessionID)
	}
	if sum.RuntimeResolved != "claude" {
		t.Errorf("RuntimeResolved = %q, want claude", sum.RuntimeResolved)
	}
	for _, c := range chunks[:len(chunks)-1] {
		if c.NativeSessionID != "" || c.RuntimeResolved != "" {
			t.Errorf("only the summary carries these: %+v", c)
		}
	}
}

// Non-claude summaries name their runtime and carry no native session.
func TestRunStream_NonClaudeSummaryHasNoNativeSession(t *testing.T) {
	root := routingRoot(t)
	fakeCLIs(t)
	isolatedLogDir(t)
	svc := newResolutionSvc(t, root)

	chunks := collectChunks(t, svc, Params{Agent: "general-codex", Task: "hi", Project: t.TempDir()})
	sum := chunks[len(chunks)-1]
	if sum.Type != "summary" || sum.RuntimeResolved != "codex" || sum.NativeSessionID != "" {
		t.Errorf("summary = %+v, want codex with no native session", sum)
	}
}

// Continuity end to end: the resume id reaches claude's argv, before the '--'
// sentinel, and the subprocess runs in the project directory.
func TestRunStream_ClaudeChatResumesAndRunsInProject(t *testing.T) {
	root := routingRoot(t)
	argvFile, pwdFile := fakeClaudeRecorder(t)
	isolatedLogDir(t)
	svc := newResolutionSvc(t, root)
	project := t.TempDir()

	collectChunks(t, svc, Params{Agent: "plain", Task: "and then?", Project: project, ResumeSessionID: "sess-claude-1"})

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("fake claude not invoked: %v", err)
	}
	argv := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	resumeAt, dashAt := -1, -1
	for i, a := range argv {
		switch a {
		case "--resume":
			resumeAt = i
		case "--":
			dashAt = i
		}
	}
	if resumeAt < 0 || argv[resumeAt+1] != "sess-claude-1" {
		t.Fatalf("argv lacks --resume sess-claude-1: %v", argv)
	}
	if dashAt < resumeAt {
		t.Errorf("--resume must precede the '--' sentinel: %v", argv)
	}

	gotDir, err := os.ReadFile(pwdFile)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, _ := filepath.EvalSymlinks(project)
	if strings.TrimSpace(string(gotDir)) != wantDir {
		t.Errorf("claude ran in %q, want the project %q (not the daemon's cwd)", strings.TrimSpace(string(gotDir)), wantDir)
	}
}

// A first turn has no resume id and therefore no --resume.
func TestRunStream_FirstTurnHasNoResumeFlag(t *testing.T) {
	root := routingRoot(t)
	argvFile, _ := fakeClaudeRecorder(t)
	isolatedLogDir(t)
	collectChunks(t, newResolutionSvc(t, root), Params{Agent: "plain", Task: "hello", Project: t.TempDir()})
	raw, _ := os.ReadFile(argvFile)
	if strings.Contains(string(raw), "--resume") {
		t.Errorf("a first turn must not pass --resume: %q", raw)
	}
}

// Only claude resumes a native session; the field must not leak to other adapters.
func TestRunStream_ResumeSessionIDIsClaudeOnly(t *testing.T) {
	root := routingRoot(t)
	svc := newResolutionSvc(t, root)
	for agent, want := range map[string]string{"plain": "abc-123", "general-codex": "", "general-agy": ""} {
		var chat rt.ChatDispatchRequest
		withStreamRunFn(func(_ context.Context, _ Request, _ rt.Adapter, cr rt.ChatDispatchRequest, _ func(StreamChunk)) (Result, error) {
			chat = cr
			return Result{}, nil
		}, func() {
			if _, err := svc.RunStream(context.Background(), Params{Agent: agent, Task: "t", Project: t.TempDir(), ResumeSessionID: "abc-123"}, func(StreamChunk) {}); err != nil {
				t.Fatalf("%s: %v", agent, err)
			}
		})
		if chat.ResumeSessionID != want {
			t.Errorf("%s: ChatDispatchRequest.ResumeSessionID = %q, want %q", agent, chat.ResumeSessionID, want)
		}
	}
}

// The id lands on argv, so RunStream refuses anything outside the id alphabet
// before any process is started.
func TestRunStream_RejectsMalformedResumeSessionID(t *testing.T) {
	root := routingRoot(t)
	svc := newResolutionSvc(t, root)
	for _, bad := range []string{"-x", "a b", "a;b", "../x", strings.Repeat("a", 129)} {
		_, err := svc.RunStream(context.Background(), Params{Agent: "plain", Task: "t", Project: t.TempDir(), ResumeSessionID: bad}, func(StreamChunk) {})
		if err == nil || !strings.Contains(err.Error(), "invalid resume_session_id") {
			t.Errorf("ResumeSessionID %q: err = %v, want an invalid resume_session_id error", bad, err)
		}
	}
}

// ---- the dispatch record -------------------------------------------------------

// The two new fields are additive: a record that never went through routing
// (legacy writers, tests) must not grow keys.
func TestWriteFinished_RoutingFieldsAreAdditive(t *testing.T) {
	logDir := isolatedLogDir(t)
	path := dispatchLogPath() // resolves into the isolated directory above

	writeFinished(Request{AgentName: "a", Runtime: "claude", Project: "/p"}, Result{ModelChosenBy: "frontmatter", ModelResolved: "sonnet"}, fixedTime, path)
	writeFinished(Request{AgentName: "a", Runtime: "codex", Project: "/p", RuntimeChosenBy: RuntimeByFallback, FallbackFrom: "agy"}, Result{ModelChosenBy: "frontmatter", ModelResolved: "gpt-5"}, fixedTime, path)
	writeFinished(Request{AgentName: "a", Runtime: "codex", Project: "/p", RuntimeChosenBy: RuntimeByFrontmatter}, Result{ModelChosenBy: "frontmatter", ModelResolved: "gpt-5"}, fixedTime, path)

	ev := readDispatchLog(t, logDir)
	if _, ok := ev[0]["runtime_chosen_by"]; ok {
		t.Errorf("legacy record grew runtime_chosen_by: %v", ev[0])
	}
	if _, ok := ev[0]["fallback_from"]; ok {
		t.Errorf("legacy record grew fallback_from: %v", ev[0])
	}
	assertField(t, ev[1], "runtime_chosen_by", "fallback")
	assertField(t, ev[1], "fallback_from", "agy")
	assertField(t, ev[2], "runtime_chosen_by", "frontmatter")
	if _, ok := ev[2]["fallback_from"]; ok {
		t.Errorf("fallback_from must be omitted when no fallback happened: %v", ev[2])
	}

	// The existing schema is untouched: the old keys are all still there.
	raw, _ := json.Marshal(ev[0])
	for _, k := range []string{"type", "ts", "agent", "runtime", "project", "exit_code", "duration_s", "model_chosen_by", "model_resolved", "stderr_tail", "stderr_truncated", "eval_run_id"} {
		if !strings.Contains(string(raw), `"`+k+`"`) {
			t.Errorf("dispatch_finished lost key %q: %s", k, raw)
		}
	}
}
