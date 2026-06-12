package workflow

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bakw00ds/yakos/internal/runtime"
)

// varRefRe matches ${inputs.<key>} and ${nodes.<id>.output} substitution markers.
// Group 1: "inputs" or "nodes"
// Group 2: the key or id
// Group 3: (nodes only) ".output" suffix
var varRefRe = regexp.MustCompile(`\$\{(inputs|nodes)\.([^}]+?)(?:\.(output))?\}`)

// Validate performs full semantic validation of a parsed Workflow:
//
//  1. Name is a valid path-safe ID.
//  2. All node IDs are valid path-safe IDs and unique within the workflow.
//  3. Every node has a non-empty Agent and a non-zero OutputLimit.
//  4. Every Needs reference resolves to a declared node ID.
//  5. Every ${inputs.<key>} reference resolves to a declared input key.
//  6. Every ${nodes.<id>.output} reference resolves to a declared node ID.
//  7. The dependency graph is acyclic (Kahn's algorithm).
//  8. Model (when non-empty) is valid after alias resolution.
//  9. Runtime (when non-empty) is a known runtime name.
//
// Returns the first error found; does not accumulate all errors.
func Validate(wf *Workflow) error {
	// --- 1. Name ---
	if err := ValidateID("name", wf.Name); err != nil {
		return err
	}

	// --- M2. Node count cap ---
	if len(wf.Nodes) > maxWorkflowNodes {
		return fmt.Errorf("workflow: %q has %d nodes, exceeds limit of %d", wf.Name, len(wf.Nodes), maxWorkflowNodes)
	}

	// --- 2. Unique node IDs ---
	nodeSet := make(map[string]struct{}, len(wf.Nodes))
	for _, n := range wf.Nodes {
		if err := ValidateID("node id", n.ID); err != nil {
			return err
		}
		if _, exists := nodeSet[n.ID]; exists {
			return fmt.Errorf("workflow: duplicate node id %q", n.ID)
		}
		nodeSet[n.ID] = struct{}{}
	}

	// Build input key set.
	inputSet := make(map[string]struct{}, len(wf.Inputs))
	for k := range wf.Inputs {
		inputSet[k] = struct{}{}
	}

	for _, n := range wf.Nodes {
		// --- 3. Required fields ---
		if n.Agent == "" {
			return fmt.Errorf("workflow: node %q: agent is required", n.ID)
		}
		if n.OutputLimit <= 0 {
			return fmt.Errorf("workflow: node %q: output_limit is required and must be > 0 (safety: prevents unbounded prompt inflation)", n.ID)
		}
		if n.Prompt == "" {
			return fmt.Errorf("workflow: node %q: prompt is required", n.ID)
		}

		// --- 4. Needs references ---
		for _, dep := range n.Needs {
			if _, ok := nodeSet[dep]; !ok {
				return fmt.Errorf("workflow: node %q: needs %q which is not a declared node id", n.ID, dep)
			}
		}

		// --- 5 & 6. Variable reference validation ---
		if err := validatePromptRefs(n.ID, n.Prompt, nodeSet, inputSet); err != nil {
			return err
		}

		// --- 8. Model validation ---
		if n.Model != "" {
			resolved := runtime.ResolveAlias(n.Model)
			if !runtime.ValidateTier(resolved) {
				return fmt.Errorf("workflow: node %q: model %q is not a valid tier or alias (resolved: %q)", n.ID, n.Model, resolved)
			}
		}

		// --- 9. Runtime validation ---
		if n.Runtime != "" {
			known := false
			for _, r := range runtime.Known {
				if r == n.Runtime {
					known = true
					break
				}
			}
			if !known {
				return fmt.Errorf("workflow: node %q: runtime %q is not known (known: %s)", n.ID, n.Runtime, strings.Join(runtime.Known, ", "))
			}
		}
	}

	// --- 7. Acyclicity (Kahn's algorithm) ---
	if err := checkAcyclic(wf); err != nil {
		return err
	}

	return nil
}

// validatePromptRefs checks that all ${...} references in prompt resolve to
// declared node IDs or input keys. This is the injection guard: any reference
// to an undeclared identifier is rejected so that attacker-controlled workflow
// YAML cannot reference file paths or unknown substitution targets.
func validatePromptRefs(nodeID, prompt string, nodeSet, inputSet map[string]struct{}) error {
	matches := varRefRe.FindAllStringSubmatch(prompt, -1)
	for _, m := range matches {
		kind := m[1]   // "inputs" or "nodes"
		key := m[2]    // the id or key
		suffix := m[3] // ".output" for nodes, empty for inputs

		switch kind {
		case "inputs":
			if suffix != "" {
				return fmt.Errorf("workflow: node %q: invalid ref ${inputs.%s.%s} — inputs refs must be ${inputs.<key>}", nodeID, key, suffix)
			}
			if _, ok := inputSet[key]; !ok {
				return fmt.Errorf("workflow: node %q: prompt references undeclared input key %q (injection guard: only declared inputs.<key> refs are allowed)", nodeID, key)
			}
		case "nodes":
			if suffix != "output" {
				return fmt.Errorf("workflow: node %q: invalid ref ${nodes.%s} — node refs must be ${nodes.<id>.output}", nodeID, key)
			}
			if _, ok := nodeSet[key]; !ok {
				return fmt.Errorf("workflow: node %q: prompt references undeclared node id %q (injection guard: only declared nodes.<id>.output refs are allowed)", nodeID, key)
			}
		}
	}
	return nil
}

// checkAcyclic detects cycles in the workflow dependency graph using Kahn's
// algorithm. Returns an error naming the cycle if one is found.
func checkAcyclic(wf *Workflow) error {
	// Build adjacency and in-degree maps.
	inDegree := make(map[string]int, len(wf.Nodes))
	dependents := make(map[string][]string, len(wf.Nodes)) // id → list of nodes that depend on id
	for _, n := range wf.Nodes {
		if _, ok := inDegree[n.ID]; !ok {
			inDegree[n.ID] = 0
		}
		for _, dep := range n.Needs {
			inDegree[n.ID]++
			dependents[dep] = append(dependents[dep], n.ID)
		}
	}

	// Collect roots (in-degree 0).
	queue := make([]string, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}

	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, dep := range dependents[cur] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if visited != len(wf.Nodes) {
		// Find a node still in the cycle for a useful error message.
		for _, n := range wf.Nodes {
			if inDegree[n.ID] > 0 {
				return fmt.Errorf("workflow: dependency graph contains a cycle (detected at node %q)", n.ID)
			}
		}
		return fmt.Errorf("workflow: dependency graph contains a cycle")
	}
	return nil
}

// TopoOrder returns nodes in a valid topological execution order (Kahn's
// algorithm). The workflow MUST be validated (acyclic) before calling this.
// Returns the order as a slice of node IDs.
func TopoOrder(wf *Workflow) []string {
	inDegree := make(map[string]int, len(wf.Nodes))
	dependents := make(map[string][]string, len(wf.Nodes))
	for _, n := range wf.Nodes {
		if _, ok := inDegree[n.ID]; !ok {
			inDegree[n.ID] = 0
		}
		for _, dep := range n.Needs {
			inDegree[n.ID]++
			dependents[dep] = append(dependents[dep], n.ID)
		}
	}

	queue := make([]string, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}

	result := make([]string, 0, len(wf.Nodes))
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		result = append(result, cur)
		for _, dep := range dependents[cur] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}
	return result
}
