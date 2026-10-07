package dispatch

// stream_k144_test.go: the codex and agy chat path streams (K-144). The buffered
// fallback is gone, so chunks must arrive while the process is still running, the
// text the console assembled must equal the buffered parse for every recording,
// and stderr noise, malformed lines and tool errors must not disturb either.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// scriptAdapter runs a shell script as the harness.
type scriptAdapter struct{ name, script string }

func (a *scriptAdapter) Name() string                     { return a.name }
func (a *scriptAdapter) Available(_ context.Context) bool { return true }
func (a *scriptAdapter) Dispatch(_ context.Context, _ runtime.DispatchRequest) (*runtime.DispatchResult, error) {
	return &runtime.DispatchResult{}, nil
}
func (a *scriptAdapter) ChatExecCmd(ctx context.Context, _ runtime.ChatDispatchRequest) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", a.script) //nolint:gosec // test script
}

func streamScript(t *testing.T, a *scriptAdapter, onChunk func(StreamChunk)) (Result, error) {
	t.Helper()
	isolatedLogDir(t)
	return execWithStreaming(context.Background(),
		Request{AgentName: "chat-agent", Task: "t", Project: t.TempDir(), Runtime: a.name, ModelResolved: "sonnet", ModelChosenBy: "frontmatter"},
		a, runtime.ChatDispatchRequest{UserText: "t"}, onChunk)
}

// Chunks reach the consumer while the harness is still running: the script
// writes its second line only after the test saw the first chunk, and gives up
// (exit 7) if it never does, so a buffering implementation fails.
func TestStreamCodexAgyChunksArriveBeforeExit(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	for _, c := range []struct {
		name, first, second string
	}{
		{"codex",
			`{"type":"item.completed","item":{"id":"a","type":"agent_message","text":"first"}}`,
			`{"type":"item.completed","item":{"id":"b","type":"agent_message","text":"second"}}`},
		{"agy",
			`{"event":"step_update","step_update":{"step_index":1,"state":"ACTIVE","step_type":"agent_response","text_delta":"first"}}`,
			`{"event":"step_update","step_update":{"step_index":2,"state":"ACTIVE","step_type":"agent_response","text_delta":"second"}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			gate := filepath.Join(t.TempDir(), "go")
			script := "printf '%s\\n' '" + c.first + "'\n" +
				"i=0; while [ ! -e '" + gate + "' ]; do i=$((i+1)); [ $i -gt 400 ] && exit 7; sleep 0.025; done\n" +
				"printf '%s\\n' '" + c.second + "'\n"
			var chunks []StreamChunk
			res, err := streamScript(t, &scriptAdapter{c.name, script}, func(ch StreamChunk) {
				chunks = append(chunks, ch)
				if ch.Type == "token" && ch.Text == "first" {
					_ = os.WriteFile(gate, nil, 0o600)
				}
			})
			if err != nil || res.ExitCode != 0 {
				t.Fatalf("the first chunk did not arrive before exit: exit %d, err %v", res.ExitCode, err)
			}
			if got := joinTokens(chunks); got != "first\nsecond" {
				t.Errorf("assembled text = %q", got)
			}
		})
	}
}

// The text the console assembles from the chunks is the buffered parse's text,
// for every codex and agy recording, and the tool cards are in the stream.
func TestStreamedChunksEqualBufferedParse(t *testing.T) {
	var files []string
	for _, pat := range []string{
		filepath.Join("..", "runtime", "testdata", "*", "*", "*.ndjson"),
		filepath.Join("..", "..", "..", "tests", "fixtures", "runtime-streams", "codex-*.ndjson"),
		filepath.Join("..", "..", "..", "tests", "fixtures", "runtime-streams", "agy-*.ndjson"),
	} {
		m, _ := filepath.Glob(pat)
		files = append(files, m...)
	}
	if len(files) < 25 {
		t.Fatalf("corpus too small: %d", len(files))
	}
	for _, f := range files {
		name := "codex"
		if strings.Contains(f, "agy") {
			name = "agy"
		}
		data, err := os.ReadFile(f) //nolint:gosec // test path
		if err != nil {
			t.Fatal(err)
		}
		want := runtime.ParseOutput(runtime.ParserFor(name), data)
		chunks, res := runBuffered(t, name, data)
		got := strings.TrimRight(joinTokens(chunks), "\r\n")
		if got != want.Text && want.Text != "" {
			t.Errorf("%s: streamed %q, buffered %q", filepath.Base(f), got, want.Text)
		}
		if res.Text != want.Text {
			t.Errorf("%s: Result.Text %q, buffered %q", filepath.Base(f), res.Text, want.Text)
		}
		if last := chunks[len(chunks)-1]; last.Type != "summary" || last.NativeSessionID != want.SessionID {
			t.Errorf("%s: last chunk %+v", filepath.Base(f), last)
		}
	}
}

// stderr noise interleaved with stdout (codex logs to stderr) changes neither
// the chunks nor the result; it is kept as the stderr tail only for a failure.
func TestStreamInterleavedStderr(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "runtime", "testdata", "codex", runtime.RecordedVersions["codex"], "command.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		script.WriteString("echo 'WARN stderr noise' >&2\nprintf '%s\\n' '" + strings.ReplaceAll(l, "'", `'\''`) + "'\n")
	}
	var chunks []StreamChunk
	res, err := streamScript(t, &scriptAdapter{"codex", script.String()}, func(c StreamChunk) { chunks = append(chunks, c) })
	if err != nil {
		t.Fatal(err)
	}
	if got := joinTokens(chunks); got != "I’ll run the command now.\ndone" {
		t.Errorf("text = %q", got)
	}
	if res.StderrTail != "" || res.Usage == nil || res.Usage.CacheRead != 27392 {
		t.Errorf("stderr tail %q, usage %+v", res.StderrTail, res.Usage)
	}
	for _, c := range chunks {
		if strings.Contains(c.Text+c.ToolOutput, "stderr noise") {
			t.Errorf("stderr leaked into a chunk: %+v", c)
		}
	}
}

// A tool step that failed is an error card, a thinking item is a thinking chunk,
// a malformed line is skipped and counted, and none of them end the stream.
func TestStreamToolErrorThinkingAndMalformedLine(t *testing.T) {
	data := []byte(strings.Join([]string{
		`{"type":"thread.started","thread_id":"t-1"}`,
		`{"type":"item.completed","item":{"id":"r","type":"reasoning","text":"thinking it over"}}`,
		`{"type":"item.started","item":{"id":"c","type":"command_execution","command":"false","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"c","type":"command_execution","command":"false","aggregated_output":"boom","exit_code":2,"status":"failed"}}`,
		`{"type": this is not json`,
		`{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"failed"}}`,
	}, "\n") + "\n")
	chunks, res := runBuffered(t, "codex", data)
	if got := strings.Join(chunkTypes(chunks), ","); got != "thinking,tool_use,tool_result,token,summary" {
		t.Fatalf("chunk types = %s", got)
	}
	if chunks[0].Thinking != "thinking it over" || !chunks[2].IsError || !strings.Contains(chunks[2].ToolOutput, "boom") {
		t.Errorf("chunks = %+v", chunks)
	}
	if chunks[1].ToolName != "command_execution" || !strings.Contains(chunks[1].ToolInput, `"command":"false"`) {
		t.Errorf("tool_use = %+v", chunks[1])
	}
	if res.Text != "failed" || res.Truncated {
		t.Errorf("Result = %+v", res)
	}
}

// A hostile stream cannot make the console receive more text than the parser's
// cap, however many messages it prints.
func TestStreamTextIsBoundedByTheParserCap(t *testing.T) {
	msg := `{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"` + strings.Repeat("y", 200*1024) + `"}}` + "\n"
	chunks, res := runBuffered(t, "codex", []byte(strings.Repeat(msg, 8)))
	if n := len(joinTokens(chunks)); n > runtime.MaxParsedTextBytes+len(bufferedTruncationMarker)+16 {
		t.Errorf("console received %d bytes of text", n)
	}
	if !res.TextCapped || !res.Truncated {
		t.Errorf("Truncated/TextCapped = %v/%v", res.Truncated, res.TextCapped)
	}
}
