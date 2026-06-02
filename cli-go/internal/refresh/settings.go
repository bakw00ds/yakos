package refresh

// settings.go — JSON smart-merge logic for settings.json.
//
// Phase A: Remove superseded — for each hook in deployed whose (event, command)
//          is in template with a DIFFERENT matcher, remove it. Template matcher wins.
// Phase B: Add missing — for each (event, matcher, command) in template not yet
//          in deployed (after Phase A), add it, preserving _doc/_doc_hook_order fields.
// Phase C: Preserve deployed-only (implicit) — hooks whose (event, command) do NOT
//          appear in the template at all are never removed.
// Phase D: Drop empty event keys that are not present in the template.
//
// Atomic write: the updated JSON is written to a temp file in the same directory,
// then os.Rename'd over the original (per Decision Q8 / Decision #13).
// No file locking in Phase 1 (daemon-era locking is Phase 2).

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MergeStats records how many hook registrations were added / removed.
type MergeStats struct {
	Added   int
	Removed int
}

// MergeSettingsFiles reads template and deployed, performs the four-phase merge,
// validates the result, and writes it back atomically if anything changed.
//
// dryRun == true: computes stats and reports changes but writes nothing.
// w is used for dry-run reporting lines.
//
// Returns MergeStats and any error. A non-nil error means the merge was aborted;
// the original deployed file is always preserved on error.
func MergeSettingsFiles(templateFile, deployedFile string, dryRun bool, w io.Writer) (MergeStats, error) {
	// Read and parse both files.
	templateData, err := os.ReadFile(templateFile) //nolint:gosec
	if err != nil {
		return MergeStats{}, fmt.Errorf("reading template %s: %w", templateFile, err)
	}
	deployedData, err := os.ReadFile(deployedFile) //nolint:gosec
	if err != nil {
		return MergeStats{}, fmt.Errorf("reading deployed %s: %w", deployedFile, err)
	}

	var tmpl map[string]any
	if err := json.Unmarshal(templateData, &tmpl); err != nil {
		return MergeStats{}, fmt.Errorf("template JSON invalid at %s: %w", templateFile, err)
	}
	var deployed map[string]any
	if err := json.Unmarshal(deployedData, &deployed); err != nil {
		return MergeStats{}, fmt.Errorf("deployed settings invalid JSON at %s: %w", deployedFile, err)
	}

	stats, err := performMerge(tmpl, deployed)
	if err != nil {
		return MergeStats{}, err
	}

	if dryRun {
		if stats.Removed > 0 || stats.Added > 0 {
			_, _ = fmt.Fprintf(w, "    [dry-run] settings: would remove %d superseded, add %d missing registrations\n",
				stats.Removed, stats.Added)
		}
		return stats, nil
	}

	// Nothing changed — skip the write to preserve original formatting.
	if stats.Added == 0 && stats.Removed == 0 {
		return stats, nil
	}

	// Marshal with 2-space indent to match python's json.dumps default.
	out, err := json.MarshalIndent(deployed, "", "  ")
	if err != nil {
		return MergeStats{}, fmt.Errorf("marshalling merged JSON: %w", err)
	}
	// Append a trailing newline (python json.dumps + print() appends one).
	out = append(out, '\n')

	// Atomic write: temp file in same dir + os.Rename.
	dir := filepath.Dir(deployedFile)
	tmp, err := os.CreateTemp(dir, ".yakos-refresh-tmp-*")
	if err != nil {
		return MergeStats{}, fmt.Errorf("creating temp file for settings write: %w", err)
	}
	tmpPath := tmp.Name()

	_, writeErr := tmp.Write(out)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if writeErr != nil {
			return MergeStats{}, fmt.Errorf("writing temp settings file: %w", writeErr)
		}
		return MergeStats{}, fmt.Errorf("closing temp settings file: %w", closeErr)
	}

	// Validate the temp file is parseable before replacing the original.
	checkData, err := os.ReadFile(tmpPath) //nolint:gosec
	if err != nil || !isValidJSON(checkData) {
		_ = os.Remove(tmpPath)
		return MergeStats{}, fmt.Errorf("temp file validation failed — original settings.json preserved")
	}

	if err := os.Rename(tmpPath, deployedFile); err != nil {
		_ = os.Remove(tmpPath)
		return MergeStats{}, fmt.Errorf("atomic rename of settings.json failed: %w", err)
	}

	return stats, nil
}

// isValidJSON returns true when data is non-empty valid JSON.
func isValidJSON(data []byte) bool {
	var v any
	return json.Unmarshal(data, &v) == nil
}

// performMerge runs phases A–D against the deployed map in-place.
// Returns MergeStats. The deployed map is mutated.
func performMerge(tmpl, deployed map[string]any) (MergeStats, error) {
	var stats MergeStats

	// Build lookup: (event, command) → matcher in template.
	// Used by Phase A to detect superseded matchers.
	type eventCmd struct{ event, command string }
	templateCmdMatcher := make(map[eventCmd]string)

	tmplHooks := hooksMap(tmpl)
	for event, entries := range tmplHooks {
		for _, rawEntry := range asList(entries) {
			entry := toMap(rawEntry)
			m := matcherOf(entry)
			for _, rawH := range asList(hooksList(entry)) {
				h := toMap(rawH)
				if cmd := commandOf(h); cmd != "" {
					templateCmdMatcher[eventCmd{event, cmd}] = m
				}
			}
		}
	}

	// Build set of (event, matcher, command) triples present in template.
	type eMC struct{ event, matcher, command string }
	templateTriples := make(map[eMC]bool)
	// Also record template entries by event for Phase B.
	tmplEntriesByEvent := make(map[string][]map[string]any)
	for event, entries := range tmplHooks {
		for _, rawEntry := range asList(entries) {
			entry := toMap(rawEntry)
			m := matcherOf(entry)
			tmplEntriesByEvent[event] = append(tmplEntriesByEvent[event], entry)
			for _, rawH := range asList(hooksList(entry)) {
				h := toMap(rawH)
				if cmd := commandOf(h); cmd != "" {
					templateTriples[eMC{event, m, cmd}] = true
				}
			}
		}
	}

	// Ensure deployed has a hooks map.
	deployedHooks := hooksMap(deployed)
	if deployedHooks == nil {
		deployedHooks = make(map[string][]any)
		deployed["hooks"] = deployedHooks
	}

	// ---- Phase A: remove superseded -----------------------------------------
	// For each hook in deployed whose (event, command) appears in template with
	// a DIFFERENT matcher, remove that hook.
	for event, rawEntries := range deployedHooks {
		entries := asList(rawEntries)
		newEntries := make([]any, 0, len(entries))
		for _, rawEntry := range entries {
			entry := toMap(rawEntry)
			m := matcherOf(entry)
			var keptHooks []any
			for _, rawH := range asList(hooksList(entry)) {
				h := toMap(rawH)
				cmd := commandOf(h)
				key := eventCmd{event, cmd}
				if cmd != "" {
					if tmplMatcher, inTemplate := templateCmdMatcher[key]; inTemplate && tmplMatcher != m {
						stats.Removed++
						continue
					}
				}
				keptHooks = append(keptHooks, rawH)
			}
			if len(keptHooks) > 0 {
				entry["hooks"] = keptHooks
				newEntries = append(newEntries, entry)
			}
			// else: entire entry block dropped (all hooks removed)
		}
		deployedHooks[event] = newEntries
	}
	deployed["hooks"] = deployedHooks

	// ---- Phase D: drop empty event keys that are NOT in template -----------
	// (Template-present empty arrays like "TeammateIdle": [] are intentional.)
	tmplEvents := make(map[string]bool)
	for event := range tmplHooks {
		tmplEvents[event] = true
	}
	for event, entries := range deployedHooks {
		if len(asList(entries)) == 0 && !tmplEvents[event] {
			delete(deployedHooks, event)
			// Count as removed so the write is not skipped.
			stats.Removed++
		}
	}
	deployed["hooks"] = deployedHooks

	// Re-compute deployed triples after Phase A so Phase B is accurate.
	deployedTriplesAfterA := make(map[eMC]bool)
	for event, rawEntries := range deployedHooks {
		for _, rawEntry := range asList(rawEntries) {
			entry := toMap(rawEntry)
			m := matcherOf(entry)
			for _, rawH := range asList(hooksList(entry)) {
				h := toMap(rawH)
				if cmd := commandOf(h); cmd != "" {
					deployedTriplesAfterA[eMC{event, m, cmd}] = true
				}
			}
		}
	}

	// ---- Phase B: add missing -----------------------------------------------
	// For each (event, matcher, command) in template not yet in deployed (after
	// Phase A), add it.
	for event, tEntries := range tmplEntriesByEvent {
		for _, tEntry := range tEntries {
			tMatcher := matcherOf(tEntry)
			var missingHooks []any
			for _, rawH := range asList(hooksList(tEntry)) {
				h := toMap(rawH)
				cmd := commandOf(h)
				if cmd != "" && !deployedTriplesAfterA[eMC{event, tMatcher, cmd}] {
					missingHooks = append(missingHooks, rawH)
					stats.Added++
				}
			}
			if len(missingHooks) == 0 {
				continue
			}
			// Find or create a matching entry block in deployed.
			if deployedHooks[event] == nil {
				deployedHooks[event] = []any{}
			}
			var foundEntry map[string]any
			for _, rawDE := range asList(deployedHooks[event]) {
				de := toMap(rawDE)
				if matcherOf(de) == tMatcher {
					foundEntry = de
					break
				}
			}
			if foundEntry != nil {
				// Append missing hooks to existing entry block.
				existing := asList(foundEntry["hooks"])
				foundEntry["hooks"] = append(existing, missingHooks...)
			} else {
				// Create a new entry block, copying _doc/_doc_hook_order from template.
				newEntry := make(map[string]any)
				if _, hasMatcher := tEntry["matcher"]; hasMatcher {
					newEntry["matcher"] = tEntry["matcher"]
				}
				if doc, ok := tEntry["_doc"]; ok {
					newEntry["_doc"] = doc
				}
				if docOrder, ok := tEntry["_doc_hook_order"]; ok {
					newEntry["_doc_hook_order"] = docOrder
				}
				newEntry["hooks"] = missingHooks
				deployedHooks[event] = append(asList(deployedHooks[event]), newEntry)
			}
		}
	}
	deployed["hooks"] = deployedHooks

	// Phase C is implicit: deployed-only triples (project-local hooks like
	// kanban-stop.sh) are preserved because we only operate on template-keyed
	// (event, command) pairs.

	return stats, nil
}

// ---- JSON navigation helpers ------------------------------------------------

// hooksMap extracts the "hooks" field as map[string][]any. Returns nil if absent.
func hooksMap(m map[string]any) map[string][]any {
	raw, ok := m["hooks"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case map[string]any:
		result := make(map[string][]any, len(v))
		for k, val := range v {
			result[k] = asList(val)
		}
		return result
	case map[string][]any:
		return v
	}
	return nil
}

// asList coerces a value to []any. Returns nil for nil input.
func asList(v any) []any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []any:
		return t
	case []map[string]any:
		result := make([]any, len(t))
		for i, m := range t {
			result[i] = m
		}
		return result
	}
	return nil
}

// toMap coerces a value to map[string]any. Returns empty map for nil/non-map.
func toMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// matcherOf extracts the "matcher" field from an entry, defaulting to "*".
func matcherOf(entry map[string]any) string {
	if m, ok := entry["matcher"].(string); ok {
		return m
	}
	return "*"
}

// hooksList extracts the "hooks" list from an entry map.
func hooksList(entry map[string]any) any {
	return entry["hooks"]
}

// commandOf extracts the "command" field from a hook map.
func commandOf(h map[string]any) string {
	if cmd, ok := h["command"].(string); ok {
		return cmd
	}
	return ""
}
