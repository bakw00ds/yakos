package hookio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const shapeFixtureDir = "../../../../.github/fixtures/hooks-shape"

type shapeFixture struct {
	Envelope json.RawMessage `json:"envelope"`
	Raw      *string         `json:"raw"`
	Want     struct {
		Error  string `json:"error"`
		Inputs []struct {
			Event     string         `json:"event"`
			Tool      string         `json:"tool"`
			WorkDir   string         `json:"work_dir"`
			ToolInput map[string]any `json:"tool_input"`
		} `json:"inputs"`
	} `json:"want"`
}

func TestDecodeShapeFixtures(t *testing.T) {
	for _, shape := range []string{ShapeCodex, ShapeAgy} {
		files, _ := filepath.Glob(filepath.Join(shapeFixtureDir, shape, "*.json"))
		if len(files) == 0 {
			t.Fatalf("no fixtures for %s", shape)
		}
		for _, f := range files {
			t.Run(shape+"/"+strings.TrimSuffix(filepath.Base(f), ".json"), func(t *testing.T) {
				var fx shapeFixture
				b, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(b, &fx); err != nil {
					t.Fatal(err)
				}
				data := []byte(fx.Envelope)
				if fx.Raw != nil {
					data = []byte(*fx.Raw)
				}
				got, err := DecodeShape(shape, data)
				if fx.Want.Error != "" {
					if err == nil {
						t.Fatalf("want error %q, got inputs", fx.Want.Error)
					}
					want := map[string]error{"not_json": ErrNotJSON, "not_object": ErrNotObject, "empty": ErrEmptyStdin}[fx.Want.Error]
					if want != nil && !errors.Is(err, want) {
						t.Fatalf("err = %v, want %v", err, want)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(fx.Want.Inputs) {
					t.Fatalf("got %d inputs, want %d", len(got), len(fx.Want.Inputs))
				}
				for i, w := range fx.Want.Inputs {
					g := got[i]
					if g.Event != w.Event || g.Tool != w.Tool || g.WorkDir != w.WorkDir {
						t.Errorf("input %d = {%s %s %s}, want {%s %s %s}", i, g.Event, g.Tool, g.WorkDir, w.Event, w.Tool, w.WorkDir)
					}
					if !reflect.DeepEqual(ToolInput(g), w.ToolInput) {
						t.Errorf("input %d tool_input = %v, want %v", i, ToolInput(g), w.ToolInput)
					}
				}
			})
		}
	}
}

func TestDecodeShapeOversizeAndUnknown(t *testing.T) {
	big := append([]byte(`{"tool_name":"Bash","x":"`), bytes.Repeat([]byte("a"), MaxShapeBytes)...)
	for _, s := range []string{ShapeCodex, ShapeAgy} {
		if _, err := DecodeShape(s, big); !errors.Is(err, ErrOversize) {
			t.Errorf("%s: err = %v, want ErrOversize", s, err)
		}
	}
	if _, err := DecodeShape("bogus", []byte(`{}`)); err == nil {
		t.Error("unknown shape accepted")
	}
}

func TestDecodeShapeAgyEventFromErrorKey(t *testing.T) {
	// "error" present (even empty) marks PostToolUse; absent is PreToolUse.
	pre, _ := DecodeShape(ShapeAgy, []byte(`{"toolCall":{"name":"x","args":{}}}`))
	post, _ := DecodeShape(ShapeAgy, []byte(`{"error":"boom","toolCall":{"name":"x","args":{}}}`))
	if pre[0].Event != "PreToolUse" || post[0].Event != "PostToolUse" {
		t.Fatalf("events = %s/%s", pre[0].Event, post[0].Event)
	}
}

func TestRespondFixtures(t *testing.T) {
	var cases []struct {
		Shape   string `json:"shape"`
		Event   string `json:"event"`
		Blocked bool   `json:"blocked"`
		Reason  string `json:"reason"`
		Want    struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
			Exit   int    `json:"exit"`
		} `json:"want"`
	}
	b, err := os.ReadFile(filepath.Join(shapeFixtureDir, "respond.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		r := Respond(c.Shape, c.Event, c.Blocked, c.Reason)
		if string(r.Stdout) != c.Want.Stdout || string(r.Stderr) != c.Want.Stderr || r.ExitCode != c.Want.Exit {
			t.Errorf("case %d (%s %s blocked=%v) = %q %q %d", i, c.Shape, c.Event, c.Blocked, r.Stdout, r.Stderr, r.ExitCode)
		}
	}
}

func TestRespondReasonBoundedAndValidJSON(t *testing.T) {
	r := Respond(ShapeAgy, "PreToolUse", true, strings.Repeat("é\"", 5000))
	var d struct{ Decision, Reason string }
	if err := json.Unmarshal(r.Stdout, &d); err != nil || d.Decision != "deny" {
		t.Fatalf("not valid deny JSON: %v %q", err, r.Stdout)
	}
	if len(d.Reason) > maxReason {
		t.Fatalf("reason %d bytes", len(d.Reason))
	}
}

func TestEnvelopeDirs(t *testing.T) {
	cases := []struct {
		shape, body string
		want        []string
	}{
		{"codex", `{"cwd":"/p","tool_name":"Bash"}`, []string{"/p"}},
		{"codex", `{"tool_name":"Bash"}`, nil},
		{"codex", `{"cwd":""}`, nil},
		{"codex", `{"cwd":7}`, nil},
		{"codex", `not json`, nil},
		{"codex", `[]`, nil},
		{"agy", `{"workspacePaths":["/a","/b",7,""]}`, []string{"/a", "/b"}},
		{"agy", `{"workspacePaths":"/a"}`, nil},
		{"agy", `{"cwd":"/ignored"}`, nil},
		{"claude", `{"cwd":"/p"}`, nil},
	}
	for _, c := range cases {
		got := EnvelopeDirs(c.shape, []byte(c.body))
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s %s: %q, want %q", c.shape, c.body, got, c.want)
		}
	}
	if got := EnvelopeDirs("codex", []byte(`{"cwd":"/p","x":"`+strings.Repeat("a", MaxShapeBytes)+`"}`)); got != nil {
		t.Errorf("oversize envelope named dirs: %q", got)
	}
}

func TestProjectContext(t *testing.T) {
	ctx := context.Background()
	if ProjectFrom(ctx) != "" {
		t.Fatal("project set on a bare context")
	}
	if got := ProjectFrom(WithProject(ctx, "/p")); got != "/p" {
		t.Errorf("ProjectFrom = %q", got)
	}
}
