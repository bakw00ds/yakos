// render.go — TUI and HTML rendering for kanban boards.
//
// RenderTUI produces the 3-column ASCII box layout matching bash cmd_render_tui.
// RenderHTML produces the static HTML snapshot matching bash cmd_render_html.
//
// Both renderers read from the board's RawLines (not from the typed TODO/
// InProgress/Done slices), performing an awk-equivalent scan to build the
// display view. This matches the bash behavior which also re-reads the file.
package kanban

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// taskDisplay holds the display-layer view of a single task extracted from
// a column section for TUI/HTML rendering.
type taskDisplay struct {
	headerLine string // the raw "- [ ] K-N — title" line
	category   string
	notes      string // the notes text (empty means no notes)
	hasNotes   bool   // true when notes is non-empty
}

// extractColumnDisplay scans RawLines for tasks under the named column.
// This mirrors the awk extraction + formatting pass in bash cmd_render_tui.
func (b *Board) extractColumnDisplay(col string) []taskDisplay {
	var result []taskDisplay
	inSection := false

	var cur *taskDisplay
	flush := func() {
		if cur != nil {
			result = append(result, *cur)
			cur = nil
		}
	}

	for _, line := range b.RawLines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			flush()
			heading := strings.TrimSpace(trimmed[3:])
			inSection = heading == col
			continue
		}

		if !inSection {
			continue
		}

		// Top-level bullet (not indented sub-field like "  - category:").
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			flush()
			cur = &taskDisplay{headerLine: line}
			continue
		}

		if cur != nil && strings.HasPrefix(line, "  - category:") {
			cur.category = strings.TrimSpace(strings.TrimPrefix(line, "  - category:"))
			continue
		}
		if cur != nil && strings.HasPrefix(line, "  - notes:") {
			notesText := strings.TrimSpace(strings.TrimPrefix(line, "  - notes:"))
			cur.notes = notesText
			cur.hasNotes = notesText != ""
			continue
		}
		// Other sub-field lines: skip for display purposes.
	}
	flush()
	return result
}

// formatTUIColumn formats tasks for the TUI display.
// Each task is rendered as: [category] <header> *
// where [category] prefix is omitted when category is "other" or empty,
// and " *" suffix indicates notes are present.
// Maximum 20 tasks shown (matching bash's `head -20`).
func formatTUIColumn(tasks []taskDisplay) string {
	var lines []string
	max := 20
	for i, t := range tasks {
		if i >= max {
			break
		}
		prefix := ""
		if t.category != "" && t.category != "other" {
			prefix = "[" + t.category + "] "
		}
		suffix := ""
		if t.hasNotes {
			suffix = " *"
		}
		lines = append(lines, prefix+t.headerLine+suffix)
	}
	return strings.Join(lines, "\n")
}

// RenderTUI writes the 3-column TUI box layout to w.
// This matches bash cmd_render_tui output exactly.
func (b *Board) RenderTUI(w io.Writer) error {
	todoTasks := b.extractColumnDisplay(ColTODO)
	inProgTasks := b.extractColumnDisplay(ColInProgress)
	doneTasks := b.extractColumnDisplay(ColDone)

	todo := formatTUIColumn(todoTasks)
	inProg := formatTUIColumn(inProgTasks)
	done := formatTUIColumn(doneTasks)

	if todo == "" {
		todo = "  (empty)"
	}
	if inProg == "" {
		inProg = "  (empty)"
	}
	if done == "" {
		done = "  (empty)"
	}

	_, err := fmt.Fprintf(w, `
╭─ TODO ──────────────────────────────────────────╮
%s
╰─────────────────────────────────────────────────╯

╭─ IN PROGRESS ───────────────────────────────────╮
%s
╰─────────────────────────────────────────────────╯

╭─ DONE ──────────────────────────────────────────╮
%s
╰─────────────────────────────────────────────────╯

`, todo, inProg, done)
	return err
}

// RenderHTML writes a static HTML snapshot to w (or to outPath if outPath != "").
// When outPath is set, the file is written atomically (temp-rename).
// The rendered_at timestamp uses time.Now() in UTC ISO format.
//
// This matches bash cmd_render_html output.
func (b *Board) RenderHTML(w io.Writer, kanbanFilePath string) error {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>yakOS kanban</title>
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCAxNiAxNiIgd2lkdGg9IjE2IiBoZWlnaHQ9IjE2Ij48cmVjdCB4PSIxIiB5PSIzIiB3aWR0aD0iNCIgaGVpZ2h0PSIxMCIgcng9IjEiIGZpbGw9ImhzbCgyMTEsOTAlLDQyJSkiLz48cmVjdCB4PSI2IiB5PSIzIiB3aWR0aD0iNCIgaGVpZ2h0PSIxMCIgcng9IjEiIGZpbGw9ImhzbCgyMTEsOTAlLDQyJSkiLz48cmVjdCB4PSIxMSIgeT0iMyIgd2lkdGg9IjQiIGhlaWdodD0iMTAiIHJ4PSIxIiBmaWxsPSJoc2woMjExLDkwJSw0MiUpIi8+PC9zdmc+">
<style>
body { font-family: -apple-system, sans-serif; margin: 2em; }
.cols { display: flex; gap: 1em; }
.col { flex: 1; border: 1px solid #ccc; border-radius: 4px; padding: 1em; min-height: 200px; }
.col h2 { margin-top: 0; }
.col ul { list-style: none; padding: 0; }
.col li { background: #f7f7f7; padding: 0.5em; margin-bottom: 0.5em; border-radius: 2px; }
.col li .cat { font-size: 0.75em; color: #555; background: #e0e0ff;
               border-radius: 3px; padding: 0.1em 0.3em; margin-right: 0.3em; }
.col li .notes { font-size: 0.8em; color: #666; margin-top: 0.2em; }
</style></head>
<body><h1>yakOS kanban</h1>
<div class="cols">
`)

	for _, col := range []string{ColTODO, ColInProgress, ColDone} {
		tasks := b.extractColumnDisplay(col)
		fmt.Fprintf(&sb, "<div class=\"col\"><h2>%s</h2><ul>\n", col)
		for _, t := range tasks {
			title := extractHTMLTitle(t.headerLine)
			catSpan := ""
			if t.category != "" {
				catSpan = `<span class="cat">` + htmlEscape(t.category) + `</span>`
			}
			notesDiv := ""
			if t.notes != "" {
				notesDiv = `<div class="notes">` + htmlEscape(t.notes) + `</div>`
			}
			fmt.Fprintf(&sb, "<li>%s%s%s</li>\n", catSpan, htmlEscape(title), notesDiv)
		}
		sb.WriteString("</ul></div>\n")
	}

	fmt.Fprintf(&sb, `</div>
<p style="color:#888;font-size:0.8em">Rendered %s from %s</p>
</body></html>
`, now, kanbanFilePath)

	_, err := io.WriteString(w, sb.String())
	return err
}

// extractHTMLTitle extracts the task title from a header line for HTML rendering.
// Strips the "- [ ] K-N — " prefix, matching bash's awk in cmd_render_html.
func extractHTMLTitle(line string) string {
	// Strip leading "- [ ] K-N — " or "- [x] K-N — " prefix.
	// The em-dash separator is U+2014.
	trimmed := strings.TrimSpace(line)
	// Remove "- [<x>] " prefix.
	if strings.HasPrefix(trimmed, "- [") {
		end := strings.Index(trimmed, "] ")
		if end >= 0 {
			rest := strings.TrimSpace(trimmed[end+2:])
			// Remove "K-N" id token if present.
			if len(rest) > 2 && rest[:2] == "K-" {
				j := 2
				for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
					j++
				}
				if j > 2 {
					rest = strings.TrimSpace(rest[j:])
					// Remove leading em-dash or ASCII hyphen separator.
					rest = strings.TrimPrefix(rest, emDash)
					rest = strings.TrimPrefix(rest, "-")
					rest = strings.TrimSpace(rest)
				}
			} else {
				// Freeform: may start with em-dash or hyphen separator.
				rest = strings.TrimPrefix(rest, emDash)
				rest = strings.TrimPrefix(rest, "- ")
				rest = strings.TrimSpace(rest)
			}
			return rest
		}
	}
	return trimmed
}

// htmlEscape escapes HTML special characters.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
