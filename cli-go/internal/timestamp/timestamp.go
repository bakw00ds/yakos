// Package timestamp provides a Windows-safe timestamp helper for filenames.
//
// time.RFC3339 emits colons (e.g. "2006-01-02T15:04:05Z"), which are illegal
// in Windows filenames. WinSafe returns the same instants in a colon-free,
// lexicographically-sortable layout: "20060102T150405Z".
//
// Operator-visible displays (e.g. `yakos soul history` output) continue to
// show the timestamp extracted from the filename as-is, so the display changes
// from "2026-06-02T12:00:00Z" (old) to "20260602T120000Z" (new). This is a
// parity break with the bash soul.sh / teach.sh equivalents, which still emit
// RFC3339-named files; document in the commit message.
package timestamp

import "time"

// Layout is the colon-free, Windows-safe format used for all snapshot and
// backup filenames produced by yakOS.
const Layout = "20060102T150405Z"

// WinSafe formats t in UTC using Layout.
func WinSafe(t time.Time) string {
	return t.UTC().Format(Layout)
}
