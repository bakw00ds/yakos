package refresh

// autocompact.go — default-on auto-compaction for the lead session (K-118 W3).
//
// Claude Code has its own auto-compaction. Its window is configurable: the
// `autoCompactWindow` key in settings.json (also `--autocompact`, the
// /autocompact command, and the CLAUDE_CODE_AUTO_COMPACT_WINDOW env var, which
// beats the setting). The harness compacts the conversation when context
// approaches that window, so no hook is involved: a hook cannot run /compact
// (a Stop hook's block reason reaches the model as text, not as a slash
// command).
//
// Refresh keeps the key in step with <project>/.yakos.yml:
//
//	auto_compact_window: <absent>  -> write 150000 when settings.json has no key
//	auto_compact_window: <tokens>  -> force that value (100000..1000000)
//	auto_compact_window: off       -> remove the key (harness default applies)
//
// A key already present in settings.json is left alone unless .yakos.yml names
// a value, so a hand edit is never clobbered by the default.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bakw00ds/yakos/internal/projfile"
)

const (
	// DefaultAutoCompactWindow is the token window refresh writes by default.
	DefaultAutoCompactWindow = 150000

	autoCompactMin = 100000  // the harness rejects windows below 100k
	autoCompactMax = 1000000 // ... and above 1M

	autoCompactYAMLKey     = "auto_compact_window"
	autoCompactSettingsKey = "autoCompactWindow"
	autoCompactOff         = "off"
)

var autoCompactLineRe = regexp.MustCompile(`(?m)^` + autoCompactYAMLKey + `:[ \t]*([^\n#\r]*?)[ \t]*(#[^\n\r]*)?\r?$`)

// autoCompactSetting is the resolved .yakos.yml choice.
type autoCompactSetting struct {
	Off      bool
	Window   int  // tokens; meaningful when !Off
	Explicit bool // .yakos.yml named it (so it overrides a settings.json value)
}

// readAutoCompactSetting parses .yakos.yml. A bad value is an error rather
// than a silent default.
func readAutoCompactSetting(projPath string) (autoCompactSetting, error) {
	def := autoCompactSetting{Window: DefaultAutoCompactWindow}
	data, err := projfile.Read(projPath)
	if err != nil {
		if os.IsNotExist(err) {
			return def, nil
		}
		return def, err
	}
	m := autoCompactLineRe.FindSubmatch(data)
	if m == nil {
		return def, nil
	}
	v := strings.ToLower(strings.Trim(strings.TrimSpace(string(m[1])), `"'`))
	switch v {
	case "", "default", "auto":
		return def, nil
	case autoCompactOff, "false", "disabled", "0":
		return autoCompactSetting{Off: true, Explicit: true}, nil
	}
	n, perr := parseTokens(v)
	if perr != nil || n < autoCompactMin || n > autoCompactMax {
		return def, fmt.Errorf("%s: %s: invalid value %q (want off, auto, or %d-%d tokens, e.g. 150000 or 150k)",
			filepath.Join(projPath, ".yakos.yml"), autoCompactYAMLKey, v, autoCompactMin, autoCompactMax)
	}
	return autoCompactSetting{Window: n, Explicit: true}, nil
}

// parseTokens accepts 150000, 150k, or 1m.
func parseTokens(v string) (int, error) {
	mult := 1
	switch {
	case strings.HasSuffix(v, "k"):
		mult, v = 1000, strings.TrimSuffix(v, "k")
	case strings.HasSuffix(v, "m"):
		mult, v = 1000000, strings.TrimSuffix(v, "m")
	}
	n, err := strconv.Atoi(v)
	return n * mult, err
}

// applyAutoCompact brings <project>/.claude/settings.json in line with the
// setting. It returns a one-line status for the refresh output and whether
// the file changed (or would, under dryRun).
func applyAutoCompact(projPath, settingsFile string, dryRun bool) (status string, changed bool, err error) {
	set, err := readAutoCompactSetting(projPath)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(settingsFile) //nolint:gosec
	if err != nil {
		return "", false, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", false, fmt.Errorf("deployed settings invalid JSON at %s: %w", settingsFile, err)
	}
	cur, has := doc[autoCompactSettingsKey]

	switch {
	case set.Off:
		status = "off (autoCompactWindow removed; Claude Code default applies)"
		if !has {
			return "off", false, nil
		}
		delete(doc, autoCompactSettingsKey)
	case has && !set.Explicit:
		return fmt.Sprintf("%v (kept from settings.json)", cur), false, nil
	default:
		src := "default"
		if set.Explicit {
			src = ".yakos.yml"
		}
		status = fmt.Sprintf("%d (%s)", set.Window, src)
		if f, ok := cur.(float64); ok && int(f) == set.Window {
			return status, false, nil
		}
		doc[autoCompactSettingsKey] = set.Window
	}
	if dryRun {
		return status, true, nil
	}
	out := insertAutoCompactKey(data, set.Window, has || set.Off)
	if out == nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return "", false, err
		}
		out = buf.Bytes()
	}
	tmp, err := os.CreateTemp(filepath.Dir(settingsFile), ".yakos-refresh-tmp-*")
	if err != nil {
		return "", false, err
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write(out); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", false, werr
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", false, err
	}
	// Keep the file's existing mode: a 0600 settings.json must not widen.
	mode := os.FileMode(0o644)
	if fi, serr := os.Stat(settingsFile); serr == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return "", false, err
	}
	if err := os.Rename(tmpPath, settingsFile); err != nil {
		_ = os.Remove(tmpPath)
		return "", false, err
	}
	return status, true, nil
}

// insertAutoCompactKey adds `"autoCompactWindow": n,` as the first member of
// the top-level object by text edit, so the rest of a hand-formatted
// settings.json (key order, indentation) is untouched. It returns nil, telling
// the caller to re-encode, when the key is already present or being removed
// (replace), the object is empty, or the result would not parse.
func insertAutoCompactKey(data []byte, n int, replaceOrRemove bool) []byte {
	if replaceOrRemove {
		return nil
	}
	i := bytes.IndexByte(data, '{')
	if i < 0 || strings.TrimSpace(string(data[:i])) != "" {
		return nil
	}
	rest := bytes.TrimLeft(data[i+1:], " \t\r\n")
	if len(rest) == 0 || rest[0] == '}' {
		return nil
	}
	nl := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		nl = "\r\n"
	}
	ins := nl + "  \"" + autoCompactSettingsKey + "\": " + strconv.Itoa(n) + ","
	out := append(append(append([]byte{}, data[:i+1]...), ins...), data[i+1:]...)
	var probe map[string]any
	if json.Unmarshal(out, &probe) != nil {
		return nil
	}
	if f, ok := probe[autoCompactSettingsKey].(float64); !ok || int(f) != n {
		return nil
	}
	return out
}
