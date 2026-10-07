// Package workflow implements the headless Flows DAG engine for yakOS Phase 4.
//
// A Workflow is a directed acyclic graph of agent dispatch nodes. Each node
// runs an agent via dispatch.Service (governed, identity-stamped). Nodes with
// overlapping readiness run concurrently under a per-run max_parallel semaphore
// that sits INSIDE the global dispatch governor.
//
// Artifacts live at:
//
//	<work>/current/workflows/<name>.yaml          — workflow definition
//	<work>/current/workflows/runs/<runID>/run.json — run state (debounced writes)
//	<work>/current/workflows/runs/<runID>/nodes/<id>.stdout — per-node output
//
// # Security
//
//   - name, runID, and node IDs are validated to ^[a-z0-9][a-z0-9-]{0,63}$ before
//     any filesystem path construction.
//   - All resolved paths are prefix-checked against the workflow/run root dir.
//   - ${...} variable substitution only expands declared node IDs and input keys;
//     any reference to an undeclared ID is rejected at validate time.
package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// idRe is the path-safe identifier pattern for workflow name, runID, and node IDs.
// First char alphanumeric, then alphanumeric-or-dash, max 64 chars total.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// ValidateID returns an error if id does not match the path-safe pattern.
// The label parameter names the field for error messages.
func ValidateID(label, id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("workflow: %s %q is invalid: must match ^[a-z0-9][a-z0-9-]{0,63}$ (path-traversal guard)", label, id)
	}
	return nil
}

// Auto is the value of a node's runtime or model that leaves the choice to the
// router (K-142). It means "no pin": the engine passes an empty value to
// dispatch, so the node follows the policy rules and the resolve chain, and the
// decision is recorded in run.json. A concrete runtime or model is an explicit
// pin for that node only and wins over any policy rule.
const Auto = "auto"

// Node is a single agent dispatch step in a workflow graph.
type Node struct {
	// ID is the unique node identifier within this workflow.
	// Must match ^[a-z0-9][a-z0-9-]{0,63}$.
	ID string `yaml:"id"`

	// Agent is the agent name dispatched for this node (required).
	Agent string `yaml:"agent"`

	// Runtime is the runtime override (claude|codex|agy) or "auto" (router
	// decides). Optional; resolved from agent frontmatter when absent.
	Runtime string `yaml:"runtime,omitempty"`

	// Model is the model override: a Claude tier (haiku|sonnet|opus|fable) or an
	// alias (cheap|balanced|best|reasoning|frontier) on claude; an alias or a
	// model id on codex and agy. Validated against Runtime at validate time and
	// passed to dispatch verbatim, which resolves it for the runtime that runs.
	// "auto" leaves the model to the router. Optional; resolved from agent
	// frontmatter when absent.
	Model string `yaml:"model,omitempty"`

	// Timeout is the per-node dispatch timeout in seconds. 0 means use the
	// dispatch default (600s). Set to 900 for long-running synthesis nodes.
	Timeout int `yaml:"timeout,omitempty"`

	// Prompt is the task prompt for this node. May contain ${inputs.<key>}
	// and ${nodes.<id>.output} substitution markers. Only declared input keys
	// and node IDs are valid references (enforced at validate time).
	Prompt string `yaml:"prompt"`

	// OutputLimit is the MANDATORY total tail-truncate budget in bytes applied
	// to ALL upstream outputs substituted into this node's prompt. A node with
	// no upstream substitutions still requires a non-zero value as a forward-
	// compatible declaration (validate rejects a missing/zero OutputLimit).
	OutputLimit int `yaml:"output_limit"`

	// Needs lists the node IDs that must complete before this node starts.
	// An empty slice (or absent field) means this is a root node (no dependencies).
	Needs []string `yaml:"needs,omitempty"`

	// ScanAllow (R6, K-83) is an explicit per-node allowlist of injection-scan
	// pattern IDs (see KnownScanPatternIDs) for false positives in THIS
	// node's output. It applies only when this node is the PRODUCER of the
	// output being scanned, never to any other node's output. Default off
	// (empty). Every use is logged at warn and recorded in the run's
	// scan_status.json. Validated in Validate: unknown or duplicate IDs are
	// rejected.
	ScanAllow []string `yaml:"scan_allow,omitempty"`
}

// Workflow is the top-level workflow definition loaded from a YAML file.
type Workflow struct {
	// Version is the schema version. Currently always 1.
	Version int `yaml:"version"`

	// Name is the workflow identifier, used in filesystem paths.
	// Must match ^[a-z0-9][a-z0-9-]{0,63}$.
	Name string `yaml:"name"`

	// Inputs is a map of input key → default value. Callers may override
	// defaults at run time. Keys are used in ${inputs.<key>} substitutions.
	Inputs map[string]string `yaml:"inputs,omitempty"`

	// Nodes is the ordered list of workflow nodes. Order has no semantic
	// significance; the engine derives execution order from the Needs edges.
	Nodes []Node `yaml:"nodes"`

	// Triggers (K-152) declares cron and webhook starts. A declaration never
	// fires by itself: the operator enables it in the user-level schedules file.
	Triggers *Triggers `yaml:"triggers,omitempty"`
}

// maxWorkflowYAMLBytes caps how much of a workflow YAML file we read.
// M2: prevents unbounded memory allocation from a large/crafted YAML.
const maxWorkflowYAMLBytes = 1 << 20 // 1 MiB

// maxWorkflowNodes caps the number of nodes allowed in a workflow definition.
// M2: prevents the scheduler from being flooded with an adversarially large graph.
const maxWorkflowNodes = 512

// Load reads a Workflow from a YAML file at path.
// Returns a parsed and structurally valid Workflow (but NOT semantically
// validated — call Validate separately to check acyclicity, refs, etc.).
// M2: read is capped at maxWorkflowYAMLBytes to prevent OOM on large files.
// The path must name a regular file (after following symlinks): a FIFO or
// device is refused at once instead of blocking the caller (K-152).
func Load(path string) (*Workflow, error) {
	wf, _, err := LoadFile(path)
	return wf, err
}

// LoadFile is Load that also returns the hex SHA-256 of the exact bytes that
// were parsed (the value an operator pins as workflow_sha when enabling a
// trigger; `shasum -a 256 <file>` prints the same digest). Hash and parse come
// from one read, so there is no window between "what was checked" and "what
// runs".
func LoadFile(path string) (*Workflow, string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("workflow: load %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, "", fmt.Errorf("workflow: load %s: not a regular file", path)
	}
	// O_NONBLOCK: if the path is swapped for a FIFO after the Stat, the open
	// still returns at once and the post-open check below refuses it.
	f, err := os.OpenFile(path, os.O_RDONLY|oNonblock, 0) //nolint:gosec
	if err != nil {
		return nil, "", fmt.Errorf("workflow: load %s: %w", path, err)
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !opened.Mode().IsRegular() {
		return nil, "", fmt.Errorf("workflow: load %s: not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxWorkflowYAMLBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("workflow: read %s: %w", path, err)
	}
	if len(data) > maxWorkflowYAMLBytes {
		return nil, "", fmt.Errorf("workflow: %s exceeds size limit (%d bytes)", path, maxWorkflowYAMLBytes)
	}
	var wf Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, "", fmt.Errorf("workflow: parse %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return &wf, hex.EncodeToString(sum[:]), nil
}

// Save writes the Workflow to path using an atomic temp-rename,
// matching the kanban write pattern for safe concurrent access.
func (wf *Workflow) Save(path string) error {
	data, err := yaml.Marshal(wf)
	if err != nil {
		return fmt.Errorf("workflow: marshal %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("workflow: write tmp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("workflow: rename %s: %w", path, err)
	}
	return nil
}
