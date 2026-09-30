package supervisorstream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
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

// shadowProvider resolves the configured provider exactly as `yakos decide`
// does, minus the flag: kill switch, then $YAKOS_DECISION_PROVIDER, then
// decisions.provider in .yakos.yml. Only "jev" and "mock" start a call.
func shadowProvider(in hooktype.HookInput, doc *yakosYMLSupervisor) string {
	if in.Env["YAKOS_DECISION_DISABLE"] == "1" {
		return ""
	}
	name := in.Env["YAKOS_DECISION_PROVIDER"]
	if name == "" && doc != nil && doc.Decisions.Kind == yaml.MappingNode {
		var d struct {
			Provider string `yaml:"provider"`
		}
		if err := doc.Decisions.Decode(&d); err == nil {
			name = d.Provider
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
	if h.Launch == nil || shadowProvider(in, doc) == "" {
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
	args := []string{"decide", shadowSurface, "--shadow", "--local", si.Verdict}
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
	_ = h.Launch(LaunchSpec{
		CLI: cli, Args: args, Stdin: state,
		StdoutPath: os.DevNull, StderrPath: os.DevNull,
	})
}
