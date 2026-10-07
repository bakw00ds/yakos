package hooksinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Harness-service install (K-145): hooks.json files whose command is
// `<binary> hook run --shape <harness> <name>`, so codex and agy gate their
// own tool calls with yakOS's Go hooks instead of a project's bash scripts.
//
//	codex: $CODEX_HOME/hooks.json (the yakOS-owned profile). A user-level file
//	       needs no project trust; dispatch passes --dangerously-bypass-hook-trust
//	       so the per-hook hash gate is skipped (K-156). The alternative, a
//	       project .codex/hooks.json, needs [projects."<path>"] trust_level and
//	       one [hooks.state."<file>:pre_tool_use:<g>:<h>"] trusted_hash per hook
//	       in config.toml, keyed by absolute path: not used.
//	agy:   <workspace>/.agents/hooks.json, loaded by `agy -p` with no trust step.
//
// The command text is byte-stable (no run id, no temp path): codex hashes it.

// HarnessCodex and HarnessAgy name the supported service harnesses.
const (
	HarnessCodex = "codex"
	HarnessAgy   = "agy"
)

// DefaultBinary is the command word written when none is given.
const DefaultBinary = "yakos"

const shapeTimeoutSec = 30

var binaryRE = regexp.MustCompile(`^[A-Za-z0-9._/+@-]{1,200}$`)

type shapeHook struct {
	name  string
	event string
}

// shapeHooks is the fixed install set, in emission order.
var shapeHooks = []shapeHook{
	{"budget-guard", "PreToolUse"},
	{"path-allowlist", "PreToolUse"},
	{"secret-scan", "PreToolUse"},
	{"supervisor-stream", "PostToolUse"},
}

type shapeHandler struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type shapeGroup struct {
	Matcher string         `json:"matcher"`
	Hooks   []shapeHandler `json:"hooks"`
}

type shapeEvents struct {
	PreToolUse  []shapeGroup `json:"PreToolUse"`
	PostToolUse []shapeGroup `json:"PostToolUse"`
}

type agyEntry struct {
	PreToolUse  []shapeGroup `json:"PreToolUse"`
	PostToolUse []shapeGroup `json:"PostToolUse"`
	Enabled     bool         `json:"enabled"`
}

// agyKey is the top-level hook name yakOS owns inside agy's hooks.json.
const agyKey = "yakos"

func shapeEventsFor(harness, binary string) shapeEvents {
	var ev shapeEvents
	for _, h := range shapeHooks {
		g := shapeGroup{Matcher: "*", Hooks: []shapeHandler{{
			Type:    "command",
			Command: binary + " hook run --shape " + harness + " " + h.name,
			Timeout: shapeTimeoutSec,
		}}}
		if h.event == "PreToolUse" {
			ev.PreToolUse = append(ev.PreToolUse, g)
		} else {
			ev.PostToolUse = append(ev.PostToolUse, g)
		}
	}
	return ev
}

// RenderShapeFile returns the bytes of the hooks.json for harness. Same inputs
// give the same bytes.
func RenderShapeFile(harness, binary string) ([]byte, error) {
	if binary == "" {
		binary = DefaultBinary
	}
	if !binaryRE.MatchString(binary) {
		return nil, fmt.Errorf("hooks install: binary %q must be a plain command or path without spaces or shell characters", binary)
	}
	var v any
	switch harness {
	case HarnessCodex:
		v = struct {
			Hooks shapeEvents `json:"hooks"`
		}{shapeEventsFor(harness, binary)}
	case HarnessAgy:
		ev := shapeEventsFor(harness, binary)
		v = map[string]agyEntry{agyKey: {PreToolUse: ev.PreToolUse, PostToolUse: ev.PostToolUse, Enabled: true}}
	default:
		return nil, fmt.Errorf("hooks install: unknown harness %q (codex | agy)", harness)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// ShapeTarget is the hooks.json path for harness under dir: the CODEX_HOME for
// codex, the workspace for agy.
func ShapeTarget(harness, dir string) string {
	if harness == HarnessAgy {
		return filepath.Join(dir, ".agents", "hooks.json")
	}
	return filepath.Join(dir, "hooks.json")
}

// InstallShape writes the harness hooks.json under dir, atomically, and only
// when the content would change (so the file's mtime and codex's hook hash stay
// put across runs). It never writes through a symlink. For agy it merges: other
// top-level hook names in an existing file are kept byte-for-byte as parsed
// JSON; an unparsable existing file is an error, never overwritten.
func InstallShape(harness, dir, binary string) (path string, changed bool, err error) {
	want, err := RenderShapeFile(harness, binary)
	if err != nil {
		return "", false, err
	}
	if !filepath.IsAbs(dir) {
		return "", false, errors.New("hooks install: directory must be absolute")
	}
	path = ShapeTarget(harness, dir)
	if fi, lerr := os.Lstat(path); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		return path, false, errors.New("hooks install: refusing to write through a symlink")
	}
	existing, rerr := os.ReadFile(path) //nolint:gosec
	if rerr != nil && !os.IsNotExist(rerr) {
		return path, false, fmt.Errorf("hooks install: read existing: %w", rerr)
	}
	if harness == HarnessAgy && rerr == nil {
		want, err = mergeAgy(existing, want)
		if err != nil {
			return path, false, err
		}
	}
	if rerr == nil && bytes.Equal(existing, want) {
		return path, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, false, fmt.Errorf("hooks install: mkdir: %w", err)
	}
	mode := os.FileMode(0o600)
	if harness == HarnessAgy {
		mode = 0o644
	}
	if err := writeAtomic(path, want, mode); err != nil {
		return path, false, fmt.Errorf("hooks install: write: %w", err)
	}
	return path, true, nil
}

// mergeAgy replaces the yakOS key in an existing agy hooks.json with ours.
func mergeAgy(existing, ours []byte) ([]byte, error) {
	var cur, mine map[string]json.RawMessage
	if err := json.Unmarshal(existing, &cur); err != nil || cur == nil {
		return nil, errors.New("hooks install: existing .agents/hooks.json is not a JSON object; fix or remove it")
	}
	if err := json.Unmarshal(ours, &mine); err != nil {
		return nil, err
	}
	cur[agyKey] = mine[agyKey]
	b, err := json.MarshalIndent(cur, "", "  ") // map keys marshal sorted
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// writeAtomic writes via a unique temp file in the same directory, then renames.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".hooks-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// ShapeDrift reports whether the installed file at path matches what Install
// would write ("missing", "stale" or "current").
func ShapeDrift(harness, dir, binary string) string {
	want, err := RenderShapeFile(harness, binary)
	if err != nil {
		return "stale"
	}
	got, err := os.ReadFile(ShapeTarget(harness, dir)) //nolint:gosec
	if err != nil {
		return "missing"
	}
	if harness == HarnessAgy {
		if m, merr := mergeAgy(got, want); merr == nil {
			want = m
		}
	}
	if bytes.Equal(got, want) {
		return "current"
	}
	return "stale"
}
