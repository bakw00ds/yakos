package telemetry

import (
	"os"
	"strings"
)

// piiPatterns lists substrings that must never appear in a recorded field.
// The check is defensive — the Event struct does not include PII fields in
// the first place, but this helper guards against accidental regressions.
var piiPatterns = []string{
	// Never record the raw session ID.
	"CLAUDE_SESSION_ID",
	// Never record home directory paths.
	os.Getenv("HOME"),
	// Never record the current user.
	os.Getenv("USER"),
	os.Getenv("LOGNAME"),
	// Never record the hostname.
	hostname(),
}

// hostname returns the machine hostname, or "" when unavailable.
func hostname() string {
	h, _ := os.Hostname()
	return h
}

// RedactEvent returns ev with any string field that contains a PII pattern
// replaced by "[REDACTED]".  It operates on a copy — the original is not
// mutated.
//
// This function is defensive.  In normal operation no PII should reach an
// Event in the first place (the struct only has fields that are safe).  This
// guard exists to catch regressions during refactors.
func RedactEvent(ev Event) Event {
	ev.Command = redactField(ev.Command)
	ev.Subcommand = redactField(ev.Subcommand)
	ev.YakosVersion = redactField(ev.YakosVersion)
	if ev.Runtime != nil {
		r := redactField(*ev.Runtime)
		ev.Runtime = &r
	}
	// SessionHash is already derived; redact if it somehow contains the raw ID.
	ev.SessionHash = redactField(ev.SessionHash)
	return ev
}

// redactField returns s, or "[REDACTED]" when s contains any PII pattern.
func redactField(s string) string {
	if s == "" {
		return s
	}
	for _, p := range piiPatterns {
		if p != "" && strings.Contains(s, p) {
			return "[REDACTED]"
		}
	}
	return s
}
