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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	return MergeSettingsFilesImpl(templateFile, deployedFile, dryRun, w, HooksImplBash)
}

// MergeSettingsFilesImpl is MergeSettingsFiles with a hook-implementation
// selector (see hooksimpl.go). HooksImplBash (or "") leaves the template
// untouched, so output is byte-identical to MergeSettingsFiles. For go and
// hybrid it validates against the Go registry first and returns an error
// (writing nothing) when a hook has no registered Go implementation.
func MergeSettingsFilesImpl(templateFile, deployedFile string, dryRun bool, w io.Writer, impl HooksImpl) (MergeStats, error) {
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

	if impl != "" && impl != HooksImplBash {
		if err := ValidateHooksImpl(impl, tmpl); err != nil {
			return MergeStats{}, err
		}
		applyHooksImpl(tmpl, impl)
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

	// Marshal with 2-space indent and sorted map keys, WITHOUT HTML
	// escaping, so the bytes match the bash implementation's
	// json.dumps(indent=2, ensure_ascii=False, sort_keys=True): a doc string
	// containing <plan_id> must not become \u003cplan_id\u003e. Encode
	// appends the trailing newline itself.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(deployed); err != nil {
		return MergeStats{}, fmt.Errorf("marshalling merged JSON: %w", err)
	}
	out := buf.Bytes()

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

	// Build lookup: (event, canonical hook name) → the template's own
	// {matcher, command} for that hook.
	//
	// Keying on the CANONICAL hook name (canonicalHookName — the script's
	// path relative to scripts/hooks/) rather than the exact command string is deliberate: a
	// deployed registration and the template's registration for the SAME
	// hook can carry different command-string PREFIXES (an absolute
	// checkout path like /Users/tw/github/yakOS/scripts/hooks/x.sh vs the
	// template's ${CLAUDE_PROJECT_DIR}/scripts/hooks/x.sh macro form) while
	// still being the same hook. Keying Phase A/B on the raw command string
	// made this prefix drift invisible to "is this hook already
	// registered?", so `yakos refresh` on a settings.json with the
	// absolute-path form added the template's macro-form registration
	// ALONGSIDE the existing one instead of replacing it — every hook in
	// that settings.json then fired twice. See K-91 follow-up (live
	// duplicate-registration incident, 2026-09-28).
	type eventName struct{ event, name string }
	type desired struct{ matcher, command string }
	templateDesired := make(map[eventName]desired)

	tmplHooks := hooksMap(tmpl)
	for event, entries := range tmplHooks {
		for _, rawEntry := range asList(entries) {
			entry := toMap(rawEntry)
			m := matcherOf(entry)
			for _, rawH := range asList(hooksList(entry)) {
				h := toMap(rawH)
				if cmd := commandOf(h); cmd != "" {
					if name := canonicalHookName(cmd); name != "" {
						templateDesired[eventName{event, name}] = desired{matcher: m, command: cmd}
					}
				}
			}
		}
	}

	// Also record template entries by event for Phase B (which entry block
	// — i.e. which matcher grouping — a newly-added hook belongs in).
	tmplEntriesByEvent := make(map[string][]map[string]any)
	for event, entries := range tmplHooks {
		for _, rawEntry := range asList(entries) {
			entry := toMap(rawEntry)
			tmplEntriesByEvent[event] = append(tmplEntriesByEvent[event], entry)
		}
	}

	// Ensure deployed has a hooks map.
	deployedHooks := hooksMap(deployed)
	if deployedHooks == nil {
		deployedHooks = make(map[string][]any)
		deployed["hooks"] = deployedHooks
	}

	// ---- Phase A: remove superseded -----------------------------------------
	// For each hook in deployed whose canonical (event, name) appears in the
	// template with a DIFFERENT matcher OR a DIFFERENT exact command string
	// (path-prefix drift — see the templateDesired comment above), remove
	// it. Phase B re-adds it in the template's own form, so the net effect
	// is REPLACE, never duplicate.
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
				if cmd != "" {
					name := canonicalHookName(cmd)
					if d, inTemplate := templateDesired[eventName{event, name}]; inTemplate {
						if d.matcher == m && d.command != cmd && isGoCommand(d.command) != isGoCommand(cmd) {
							// Implementation switch (bash <-> go) for the
							// same hook in the same matcher block: replace
							// the command IN PLACE so hook order within the
							// block is preserved. Counted as one removal
							// plus one addition, like any other replace.
							h["command"] = d.command
							stats.Removed++
							stats.Added++
							keptHooks = append(keptHooks, rawH)
							continue
						}
						if d.matcher != m || d.command != cmd {
							stats.Removed++
							continue
						}
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

	// Re-compute which (event, canonical name) hooks are present in deployed
	// after Phase A, so Phase B knows what's still missing.
	deployedPresentAfterA := make(map[eventName]bool)
	for event, rawEntries := range deployedHooks {
		for _, rawEntry := range asList(rawEntries) {
			entry := toMap(rawEntry)
			for _, rawH := range asList(hooksList(entry)) {
				h := toMap(rawH)
				if cmd := commandOf(h); cmd != "" {
					if name := canonicalHookName(cmd); name != "" {
						deployedPresentAfterA[eventName{event, name}] = true
					}
				}
			}
		}
	}

	// ---- Phase B: add missing -----------------------------------------------
	// For each (event, canonical name) in template not yet present in
	// deployed (after Phase A), add the template's own hook entry.
	for event, tEntries := range tmplEntriesByEvent {
		for _, tEntry := range tEntries {
			tMatcher := matcherOf(tEntry)
			var missingHooks []any
			for _, rawH := range asList(hooksList(tEntry)) {
				h := toMap(rawH)
				cmd := commandOf(h)
				name := canonicalHookName(cmd)
				if cmd != "" && name != "" && !deployedPresentAfterA[eventName{event, name}] {
					missingHooks = append(missingHooks, rawH)
					stats.Added++
					// Mark present immediately so a hook appearing in more
					// than one template entry for the same event is never
					// added twice.
					deployedPresentAfterA[eventName{event, name}] = true
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

	// Phase C is implicit: deployed-only hooks (project-local hooks like
	// kanban-stop.sh, whose canonical name never appears in
	// templateDesired for that event) are preserved because Phase A only
	// removes hooks the template has an entry for.

	return stats, nil
}

// hooksDirMarker is the path segment every deployed hook command passes
// through, regardless of the prefix in front of it.
const hooksDirMarker = "/scripts/hooks/"

// canonicalHookName extracts a hook registration's identity from its
// "command" string, independent of the path-PREFIX form used to reach the
// script (${CLAUDE_PROJECT_DIR}/scripts/hooks/x.sh, an absolute checkout
// path, a relative path, ...). Two commands that invoke the same script
// under different prefixes are the SAME hook and must be recognized as
// such by the merge, or refresh duplicates rather than replaces the
// registration when the deployed prefix form differs from the template's
// (see performMerge's templateDesired comment).
//
// The identity is the script's path RELATIVE to scripts/hooks/ (for
// example "x.sh" or "per-domain/x.sh"), not its basename: two hooks that
// share a basename in different subdirectories are different hooks and
// must not collapse into one merge key (K-94). When the command does not
// route through a scripts/hooks/ directory at all, the basename is used.
//
// Hook commands are not expected to carry arguments (every hook script
// reads its input from stdin), but this takes only the first
// whitespace-separated field to be robust if one ever does.
func canonicalHookName(command string) string {
	// `yakos hook run <name>` (--hooks-impl go) is the same hook as
	// scripts/hooks/<name>.sh, so a switch replaces rather than duplicates.
	if n, ok := goHookName(command); ok {
		return n + ".sh"
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	p := "/" + strings.ReplaceAll(fields[0], "\\", "/")
	if i := strings.Index(p, hooksDirMarker); i >= 0 {
		if rel := p[i+len(hooksDirMarker):]; rel != "" {
			return rel
		}
	}
	return filepath.Base(fields[0])
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
