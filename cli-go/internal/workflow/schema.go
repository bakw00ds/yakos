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

// Node is a single agent dispatch step in a workflow graph.
type Node struct {
	// ID is the unique node identifier within this workflow.
	// Must match ^[a-z0-9][a-z0-9-]{0,63}$.
	ID string `yaml:"id"`

	// Agent is the agent name dispatched for this node (required).
	Agent string `yaml:"agent"`

	// Runtime is the runtime override (claude|codex|agy|gemini). Optional;
	// resolved from agent frontmatter when absent.
	Runtime string `yaml:"runtime,omitempty"`

	// Model is the model tier override (haiku|sonnet|opus|fable) or alias.
	// Validated via runtime.ResolveAlias + runtime.ValidateTier at validate time.
	// Optional; resolved from agent frontmatter when absent.
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
func Load(path string) (*Workflow, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("workflow: load %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxWorkflowYAMLBytes+1))
	if err != nil {
		return nil, fmt.Errorf("workflow: read %s: %w", path, err)
	}
	if len(data) > maxWorkflowYAMLBytes {
		return nil, fmt.Errorf("workflow: %s exceeds size limit (%d bytes)", path, maxWorkflowYAMLBytes)
	}
	var wf Workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("workflow: parse %s: %w", path, err)
	}
	return &wf, nil
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
