package hooksinstall

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
// The command word is the ABSOLUTE path of the yakos binary (K-145 fix): a bare
// name resolves through the harness's PATH (fail-open when absent, hijackable
// when PATH holds "." or a workspace dir), and a relative path resolves against
// a project-controlled cwd.

// HarnessCodex and HarnessAgy name the supported service harnesses.
const (
	HarnessCodex = "codex"
	HarnessAgy   = "agy"
)

// maxShapeFile bounds every read of a hooks.json (a symlink to /dev/zero or a
// huge file must not be slurped).
const maxShapeFile = 1 << 20

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

// RenderShapeFile returns the bytes of the hooks.json for harness. binary must
// be an absolute path (see ResolveBinary for the filesystem checks). Same
// inputs give the same bytes.
func RenderShapeFile(harness, binary string) ([]byte, error) {
	if err := checkBinaryText(binary); err != nil {
		return nil, err
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

// checkBinaryText validates the command word without touching the filesystem:
// absolute, clean, and made of safe characters only.
func checkBinaryText(binary string) error {
	switch {
	case binary == "":
		return errors.New("hooks install: binary path required")
	case !filepath.IsAbs(binary):
		return fmt.Errorf("hooks install: binary %q must be an absolute path (a bare or relative name resolves through the harness's PATH or cwd)", binary)
	case !binaryRE.MatchString(binary) || filepath.Clean(binary) != binary:
		return fmt.Errorf("hooks install: binary %q must be a clean absolute path without spaces or shell characters", binary)
	}
	return nil
}

// ResolveBinary returns the absolute, symlink-resolved path of the yakos binary
// the hooks will run. An empty binary means the running executable. The result
// must be a regular file that is not group- or world-writable.
func ResolveBinary(binary string) (string, error) {
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("hooks install: cannot locate the running yakos binary: %w", err)
		}
		binary = exe
	}
	if err := checkBinaryText(binary); err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", fmt.Errorf("hooks install: binary %q: %w", binary, err)
	}
	if err := checkBinaryText(resolved); err != nil {
		return "", err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("hooks install: binary %q: %w", binary, err)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("hooks install: binary %q is not a regular file", binary)
	}
	if modeWritableByOthers(fi) {
		return "", fmt.Errorf("hooks install: binary %q is group- or world-writable", binary)
	}
	return resolved, nil
}

// ShapeResult is what InstallShapeReport did.
type ShapeResult struct {
	Path    string
	Changed bool
	// Foreign names the other top-level hook entries kept in an existing agy
	// hooks.json (sorted, sanitized). agy runs them headless with no trust
	// step, so the caller must show them.
	Foreign []string
}

// InstallShape is InstallShapeReport without the report.
func InstallShape(harness, dir, binary string) (path string, changed bool, err error) {
	r, err := InstallShapeReport(harness, dir, binary)
	return r.Path, r.Changed, err
}

// InstallShapeReport writes the harness hooks.json under dir, atomically, and
// only when the content would change (so the file's mtime and codex's hook hash
// stay put across runs). All access below dir goes through os.OpenRoot, and a
// symlinked .agents is refused, so neither a link nor a swap can redirect the
// write. For agy it merges: other top-level hook names in an existing file are
// kept as parsed JSON and reported in ShapeResult.Foreign; an unparsable existing
// file is an error, never overwritten.
func InstallShapeReport(harness, dir, binary string) (ShapeResult, error) {
	var res ShapeResult
	bin, err := ResolveBinary(binary)
	if err != nil {
		return res, err
	}
	want, err := RenderShapeFile(harness, bin)
	if err != nil {
		return res, err
	}
	if !filepath.IsAbs(dir) {
		return res, errors.New("hooks install: directory must be absolute")
	}
	res.Path = ShapeTarget(harness, dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, fmt.Errorf("hooks install: mkdir: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return res, fmt.Errorf("hooks install: open directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	sub, name := root, "hooks.json"
	if harness == HarnessAgy {
		sub, err = openAgentsDir(root)
		if err != nil {
			return res, err
		}
		defer func() { _ = sub.Close() }()
	}

	existing, exists, err := readInRoot(sub, name)
	if err != nil {
		return res, err
	}
	if harness == HarnessAgy && exists {
		want, res.Foreign, err = mergeAgy(existing, want)
		if err != nil {
			return res, err
		}
	}
	if exists && bytes.Equal(existing, want) {
		return res, nil
	}
	mode := os.FileMode(0o600)
	if harness == HarnessAgy {
		mode = 0o644
	}
	if err := writeAtomicIn(sub, name, want, mode); err != nil {
		return res, fmt.Errorf("hooks install: write: %w", err)
	}
	res.Changed = true
	return res, nil
}

// openAgentsDir opens <workspace>/.agents as a root, creating it when absent.
// It refuses a symlink or a non-directory (a cloned repo can commit
// .agents -> /some/dir), and checks that the opened directory is the one that
// was inspected.
func openAgentsDir(root *os.Root) (*os.Root, error) {
	const d = ".agents"
	fi, err := root.Lstat(d)
	if errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(d, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("hooks install: mkdir: %w", err)
		}
		fi, err = root.Lstat(d)
	}
	if err != nil {
		return nil, fmt.Errorf("hooks install: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return nil, errors.New("hooks install: refusing to use .agents: it is a symlink or not a directory")
	}
	sub, err := root.OpenRoot(d)
	if err != nil {
		return nil, fmt.Errorf("hooks install: open .agents: %w", err)
	}
	sfi, err := sub.Stat(".")
	if err != nil || !os.SameFile(fi, sfi) {
		_ = sub.Close()
		return nil, errors.New("hooks install: .agents changed while it was being opened; refusing")
	}
	return sub, nil
}

// readInRoot reads name inside root, refusing a symlink or non-regular file.
func readInRoot(root *os.Root, name string) (data []byte, exists bool, err error) {
	fi, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("hooks install: read existing: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, false, errors.New("hooks install: refusing to write through a symlink or non-regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, false, fmt.Errorf("hooks install: read existing: %w", err)
	}
	defer func() { _ = f.Close() }()
	if ofi, serr := f.Stat(); serr != nil || !os.SameFile(fi, ofi) {
		return nil, false, errors.New("hooks install: existing file changed while it was being read; refusing")
	}
	data, err = io.ReadAll(io.LimitReader(f, maxShapeFile+1))
	if err != nil {
		return nil, false, fmt.Errorf("hooks install: read existing: %w", err)
	}
	if len(data) > maxShapeFile {
		return data[:maxShapeFile], true, nil // oversized: never equal to ours; agy merge rejects it
	}
	return data, true, nil
}

// mergeAgy replaces the yakOS key in an existing agy hooks.json with ours and
// returns the sanitized names of the other entries that were kept.
func mergeAgy(existing, ours []byte) ([]byte, []string, error) {
	var cur, mine map[string]json.RawMessage
	if err := json.Unmarshal(existing, &cur); err != nil || cur == nil {
		return nil, nil, errors.New("hooks install: existing .agents/hooks.json is not a JSON object; fix or remove it")
	}
	if err := json.Unmarshal(ours, &mine); err != nil {
		return nil, nil, err
	}
	var foreign []string
	for k := range cur {
		if k != agyKey {
			foreign = append(foreign, SafeName(k))
		}
	}
	sort.Strings(foreign)
	cur[agyKey] = mine[agyKey]
	b, err := json.MarshalIndent(cur, "", "  ") // map keys marshal sorted
	if err != nil {
		return nil, nil, err
	}
	return append(b, '\n'), foreign, nil
}

// SafeName quotes a name taken from a file so control characters and escape
// sequences cannot reach a terminal, and caps its length.
func SafeName(s string) string {
	const max = 48
	if len(s) > max {
		s = s[:max] + "..."
	}
	return strconv.QuoteToASCII(s)
}

// writeAtomicIn writes via a unique temp file in root, then renames.
func writeAtomicIn(root *os.Root, name string, data []byte, mode os.FileMode) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return err
	}
	tmp := ".hooks-" + hex.EncodeToString(suffix[:]) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = root.Remove(tmp)
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
	if err := root.Rename(tmp, name); err != nil {
		return err
	}
	ok = true
	return nil
}

// Inspection is the state of an installed hooks.json.
type Inspection struct {
	// State is "missing", "stale" (differs from what Install would write for the
	// current binary), "unsafe" (a link, another owner, or group/world-writable)
	// or "current".
	State string
	// Binary is the command word the file runs ("" when it cannot be read).
	Binary string
	// BinaryMissing is set when Binary is not a regular file any more.
	BinaryMissing bool
}

// InspectShape compares the installed file at harness/dir with what Install
// would write for binary ("" = the running executable).
func InspectShape(harness, dir, binary string) Inspection {
	var in Inspection
	path := ShapeTarget(harness, dir)
	got, state := readSafeFile(path)
	if state != "" {
		in.State = state
		return in
	}
	in.Binary = installedBinary(harness, got)
	if in.Binary != "" {
		if fi, err := os.Stat(in.Binary); err != nil || !fi.Mode().IsRegular() {
			in.BinaryMissing = true
		}
	}
	bin, err := ResolveBinary(binary)
	if err != nil {
		in.State = "stale"
		return in
	}
	want, err := RenderShapeFile(harness, bin)
	if err != nil {
		in.State = "stale"
		return in
	}
	if harness == HarnessAgy {
		if m, _, merr := mergeAgy(got, want); merr == nil {
			want = m
		}
	}
	if bytes.Equal(got, want) {
		in.State = "current"
	} else {
		in.State = "stale"
	}
	return in
}

// ShapeDrift is InspectShape's state: "missing", "stale", "unsafe" or
// "current".
func ShapeDrift(harness, dir, binary string) string {
	return InspectShape(harness, dir, binary).State
}

// CodexHooksTrusted reports whether the codex profile's hooks.json is exactly
// what yakOS writes for the running binary, in a profile directory and file that
// only this user can change. Dispatch passes --dangerously-bypass-hook-trust
// only then: the flag turns the file into reviewless command execution, so a
// planted or edited file must not get it.
func CodexHooksTrusted(profile string) bool {
	fi, err := os.Lstat(profile)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !ownedByCurrentUser(fi) || modeWritableByOthers(fi) {
		return false
	}
	return InspectShape(HarnessCodex, profile, "").State == "current"
}

// readSafeFile reads path if it is a regular file owned by this user that no
// one else can write. The state is "" on success, else "missing" or "unsafe".
func readSafeFile(path string) ([]byte, string) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "missing"
		}
		return nil, "unsafe"
	}
	if !fi.Mode().IsRegular() || !ownedByCurrentUser(fi) || modeWritableByOthers(fi) {
		return nil, "unsafe"
	}
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, "unsafe"
	}
	defer func() { _ = f.Close() }()
	if ofi, serr := f.Stat(); serr != nil || !os.SameFile(fi, ofi) {
		return nil, "unsafe"
	}
	data, err := io.ReadAll(io.LimitReader(f, maxShapeFile+1))
	if err != nil || len(data) > maxShapeFile {
		return nil, "unsafe"
	}
	return data, ""
}

// installedBinary extracts the command word of the first PreToolUse hook.
func installedBinary(harness string, data []byte) string {
	var ev shapeEvents
	switch harness {
	case HarnessCodex:
		var v struct {
			Hooks shapeEvents `json:"hooks"`
		}
		if json.Unmarshal(data, &v) != nil {
			return ""
		}
		ev = v.Hooks
	default:
		var v map[string]agyEntry
		if json.Unmarshal(data, &v) != nil {
			return ""
		}
		e := v[agyKey]
		ev = shapeEvents{PreToolUse: e.PreToolUse, PostToolUse: e.PostToolUse}
	}
	if len(ev.PreToolUse) == 0 || len(ev.PreToolUse[0].Hooks) == 0 {
		return ""
	}
	cmd := ev.PreToolUse[0].Hooks[0].Command
	bin, _, _ := strings.Cut(cmd, " hook run ")
	if checkBinaryText(bin) != nil {
		return ""
	}
	return bin
}
