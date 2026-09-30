package supervisorstream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// K-111 P2b: shadow decision for the supervisor pre-filter.
//
// After the local pre-filter has decided (pass or escalate), and only when a
// decision provider is configured, the hook starts `yakos decide
// supervisor-prefilter --shadow` detached, feeding it the event as a small JSON
// state on stdin. The child redacts, asks the provider, and appends one record
// to the decision log next to the local verdict. Nothing here waits for it,
// reads its result, or changes the exit code, the buffer, the counter or the
// escalation path: with provider none this file does nothing at all. Bash
// twin: the _ss_shadow block in supervisor-stream.sh.

const (
	shadowSurface = "supervisor-prefilter"
	// shadowPreviewCap bounds the previewed text handed to the child. The child
	// redacts the whole string and then cuts it to 2 KiB, so anything past 2 KiB
	// never leaves; 4 KiB just keeps the pipe small.
	shadowPreviewCap = 4096
)

// shadowInput is what the hook knows at the moment the local verdict is made.
type shadowInput struct {
	Tool     string
	Verdict  string // "pass" | "escalate"
	Trigger  string // escalation reason, e.g. "risk-regex:rm\s+-rf"
	FilePath string
	Command  string
	New      string
	Content  string
	Session  string
}

// shadowProvider resolves the provider as `yakos decide` does
// (decision.ResolveProvider): kill switch, then $YAKOS_DECISION_PROVIDER, then
// `provider:` in the USER-level policy file. A project .yakos.yml can never
// enable a provider, only veto the policy switch with `provider: none`. Only
// "jev" and "mock" start a call.
func shadowProvider(in hooktype.HookInput, doc *yakosYMLSupervisor) string {
	if in.Env["YAKOS_DECISION_DISABLE"] == "1" {
		return ""
	}
	name := in.Env["YAKOS_DECISION_PROVIDER"]
	if name == "" {
		pol, _ := decision.LoadPolicy(filepath.Join(statepath.Dir(), decision.PolicyFileName))
		name = pol.Provider
		if name != "" && projectVetoesProvider(doc) {
			return ""
		}
	}
	switch name {
	case "jev":
		if in.Env["TYPESAFE_API_KEY"] == "" {
			return "" // decide would only log no_key on every tool call
		}
		return name
	case "mock":
		return name
	}
	return ""
}

// projectSurfaceOff reports `decisions.surfaces.supervisor-prefilter.mode: off`
// in the project .yakos.yml. The call would only fail as "disabled", so the hook
// skips it (and never writes a raw state file for it).
func projectSurfaceOff(doc *yakosYMLSupervisor) bool {
	if doc == nil || doc.Decisions.Kind != yaml.MappingNode {
		return false
	}
	var d struct {
		Surfaces map[string]struct {
			Mode string `yaml:"mode"`
		} `yaml:"surfaces"`
	}
	return doc.Decisions.Decode(&d) == nil && d.Surfaces[shadowSurface].Mode == "off"
}

// projectVetoesProvider reports an explicit `decisions.provider: none` in the
// project .yakos.yml.
func projectVetoesProvider(doc *yakosYMLSupervisor) bool {
	if doc == nil || doc.Decisions.Kind != yaml.MappingNode {
		return false
	}
	var d struct {
		Provider string `yaml:"provider"`
	}
	return doc.Decisions.Decode(&d) == nil && d.Provider == "none"
}

// shadowTriggerKind reduces an escalation reason to its kind
// ("risk-regex:rm\s+-rf" -> "risk-regex"): aggregation key, no free text.
func shadowTriggerKind(reason string) string {
	if i := strings.IndexByte(reason, ':'); i >= 0 {
		reason = reason[:i]
	}
	return reason
}

// shadowState builds the named-field state the question set allows
// (state_fields in lib/decisions/supervisor-prefilter.yaml). Empty fields are
// omitted. The child still redacts and previews every string.
func (h *Hook) shadowState(projectDir string, si shadowInput) ([]byte, error) {
	state := map[string]any{"tool": si.Tool}
	// Bash twin: $(...) strips trailing newlines before and after `head -c`.
	trimNL := func(x string) string { return strings.TrimRight(x, "\n") }
	preview := trimNL(si.Command)
	if preview == "" {
		preview = trimNL(si.New)
	}
	if preview == "" {
		preview = trimNL(si.Content)
	}
	if preview = trimNL(truncate(preview, shadowPreviewCap)); preview != "" {
		state["command_or_diff_preview"] = preview
	}
	if si.FilePath != "" {
		state["file_path"] = si.FilePath
	}
	if intent, ok := h.readIntent(); ok {
		state["stated_intent"] = intent
	}
	if si.FilePath != "" {
		if mentioned, known := h.planMentions(si.FilePath, projectDir); known {
			state["plan_mentions_path"] = mentioned
		}
	}
	return json.Marshal(state)
}

// readIntent is the head of decisions.md (1500 bytes, trailing newlines
// trimmed), the same text the LLM supervisor already receives.
func (h *Hook) readIntent() (string, bool) {
	data, err := os.ReadFile(filepath.Join(h.WorkCurrentDir, "decisions.md")) //nolint:gosec
	if err != nil {
		return "", false
	}
	if len(data) > decisionsHeadCap {
		data = data[:decisionsHeadCap]
	}
	s := strings.TrimRight(string(data), "\n")
	return s, s != ""
}

// planMentions reports whether decisions.md or plan.md names the file. known
// is false when neither file exists (nothing to compare against).
func (h *Hook) planMentions(filePath, projectDir string) (mentioned, known bool) {
	decisions := filepath.Join(h.WorkCurrentDir, "decisions.md")
	plan := filepath.Join(h.WorkCurrentDir, "plan.md")
	_, dErr := os.Stat(decisions)
	_, pErr := os.Stat(plan)
	if dErr != nil && pErr != nil {
		return false, false
	}
	return h.checkOutOfScope(filePath, projectDir) == "", true
}

// shadowDecision starts the detached shadow call. Every failure is swallowed:
// it can never block, alter or delay the tool call.
func (h *Hook) shadowDecision(in hooktype.HookInput, doc *yakosYMLSupervisor, projectDir string, si shadowInput) {
	defer func() { _ = recover() }()
	if h.Launch == nil || shadowProvider(in, doc) == "" || projectSurfaceOff(doc) {
		return
	}
	cli := findCLI(in.Env)
	if cli == "" {
		return
	}
	state, err := h.shadowState(projectDir, si)
	if err != nil {
		return
	}
	// The state travels in a 0600 file in the 0700 state dir, not on a pipe: a
	// pipe write before the child starts has no reader and blocks the hook once
	// it exceeds the pipe buffer (about 4 KiB on Windows). `decide` deletes the
	// file as soon as it has read it.
	stateFile, err := writeShadowStateFile(state)
	if err != nil {
		return
	}
	args := []string{"decide", shadowSurface, "--shadow", "--state-file", stateFile, "--consume-state-file", "--local", si.Verdict}
	if si.Verdict == "escalate" {
		if k := shadowTriggerKind(si.Trigger); k != "" {
			args = append(args, "--local-trigger", k)
		}
	}
	if si.Session != "" {
		args = append(args, "--session", si.Session)
	}
	if projectDir != "" {
		args = append(args, "--config", filepath.Join(projectDir, ".yakos.yml"))
	}
	if err := h.Launch(LaunchSpec{
		CLI: cli, Args: args,
		StdoutPath: os.DevNull, StderrPath: os.DevNull,
	}); err != nil {
		_ = os.Remove(stateFile)
	}
}

// shadowStatePrefix names the per-call state files; leftovers (a child that
// never ran) older than shadowStateMaxAge are swept on the next call.
const (
	shadowStatePrefix = "shadow-state-"
	shadowStateMaxAge = 10 * time.Minute
)

// writeShadowStateFile writes state to a fresh 0600 file in the state dir and
// returns its path. It never blocks: a regular file has no reader to wait for.
func writeShadowStateFile(state []byte) (string, error) {
	dir := statepath.Dir()
	if err := statepath.SecureDir(dir); err != nil {
		return "", err
	}
	sweepShadowStateFiles(dir)
	f, err := os.CreateTemp(dir, shadowStatePrefix+"*.json") // 0600
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err := f.Write(state); err != nil {
		f.Close() //nolint:errcheck,gosec
		_ = os.Remove(name)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func sweepShadowStateFiles(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, shadowStatePrefix+"*.json"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) > shadowStateMaxAge {
			_ = os.Remove(m)
		}
	}
}
