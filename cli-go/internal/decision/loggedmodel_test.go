package decision

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type hostileModelProvider struct{ model string }

func (hostileModelProvider) Name() string                    { return "jev" }
func (hostileModelProvider) Available(context.Context) error { return nil }
func (h hostileModelProvider) Decide(context.Context, Request) (*Result, error) {
	return &Result{Model: h.model, Answers: map[string]Answer{}}, nil
}

// The provider reports the model it served. A hostile endpoint controls that
// string, so the log must not carry it verbatim.
func TestExecute_LogsOnlyABoundedModelName(t *testing.T) {
	secret := "AKIA" + strings.Repeat("Z", 16)
	hostile := strings.Repeat("X", 900_000) + secret + "\nINJECTED"
	lg := NewLogger(filepath.Join(t.TempDir(), "l.ndjson"))
	eng := &Engine{Provider: hostileModelProvider{model: hostile}, Logger: lg}
	set := testSet(t)
	if out := eng.Execute(context.Background(), set, map[string]any{"tool": "x"}, ModeShadow, "s", 0); out.Err != nil {
		t.Fatal(out.Err)
	}
	data, err := os.ReadFile(lg.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4096 {
		t.Errorf("log line is %d bytes for a hostile model string", len(data))
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "INJECTED") {
		t.Errorf("hostile model text reached the log: %.200s", data)
	}
	if strings.Count(string(data), "\n") != 1 {
		t.Error("exactly one line per call")
	}
}

func TestLoggedModel(t *testing.T) {
	cases := []struct{ got, pinned, want string }{
		{"jev-1.13.0", "jev-1.13.0", "jev-1.13.0"},
		{"jev-1.14.0", "jev-1.13.0", "jev-1.14.0"},
		{"Jev 1.14/../x\n", "jev-1.13.0", "ev1.14..x"},
		{strings.Repeat("a", 100), "jev-1.13.0", strings.Repeat("a", 64)},
		{"", "jev-1.13.0", ""},
	}
	for _, c := range cases {
		if got := loggedModel(c.got, c.pinned); got != c.want {
			t.Errorf("loggedModel(%q) = %q, want %q", c.got, got, c.want)
		}
	}
}
