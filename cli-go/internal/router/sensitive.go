package router

// sensitive.go is the K-140 classifier: it marks a dispatch "sensitive" when
// any text that would reach the model holds a secret-shaped string or names a
// never-path (a credential file), and the router then restricts the candidate
// chain to the primary runtime or a local one (dispatch.buildChain).
//
// What it is: a routing class. It keeps a secret-bearing request off a vendor
// the operator did not choose to trust with it. What it is not: an egress
// guarantee. A harness can still read a file it is told to, and a secret the
// scan cannot recognise passes; the sandbox's read denial is the control that
// stops a file leaving the machine (docs/routing.md, "Sensitive class").
//
// Detection reuses the secret-scan hook's patterns (secretscan.DefaultPatterns)
// and the decision egress layer's DefaultNeverPaths. A project may ADD
// never_paths (Input.NeverPaths); nothing a project or request carries can
// remove a default, because the defaults are merged in here, every time.
//
// Every failure mode is the safe one: a scan that times out, finds too much
// text to scan, or panics classifies the request sensitive. Reasons are a
// fixed vocabulary (never the matched text or path) so they can reach the
// ledger and explain output.

import (
	"strings"
	"time"

	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/hooks/fnmatch"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
)

// ClassSensitive is the route class of a request that holds a secret-shaped
// string or a never-path reference.
const ClassSensitive = "sensitive"

// Reasons a request was classified sensitive. A fixed vocabulary: the matched
// text and path are never part of a reason.
const (
	ReasonSecret    = "secret-pattern"
	ReasonNeverPath = "never-path"
	ReasonTimeout   = "scan-timeout"
	ReasonOversize  = "scan-oversize"
	ReasonError     = "classifier-error"
	// ReasonDeclared is a class the caller set (`yakos router explain --class
	// sensitive`) rather than one the scan found.
	ReasonDeclared = "declared"
)

// Scan limits. Variables so tests can shrink them.
var (
	// scanTimeout bounds one classification. RE2 is linear, so this is a guard
	// against a pathological input size, not a regex.
	scanTimeout = 2 * time.Second
	// maxScanBytes is the most text scanned; more fails closed.
	maxScanBytes = 16 << 20
	scanChunk    = 64 << 10
	// scanOverlap lets a token that straddles a chunk edge be seen whole. It is
	// longer than every default pattern and path token.
	scanOverlap = 1024
	maxToken    = 1024
)

// SensitiveClassifier is the Classifier that finds the sensitive class: the
// default for ActiveClassifier.
func SensitiveClassifier(in Input) string {
	if SensitiveReason(in) != "" {
		return ClassSensitive
	}
	return ClassDefault
}

// SensitiveReason scans in.Material and returns why it is sensitive, "" when
// nothing in it is. It fails closed: a timeout, too much text or a panic is a
// reason, not a pass.
func SensitiveReason(in Input) (reason string) {
	defer func() {
		if recover() != nil {
			reason = ReasonError
		}
	}()
	total := 0
	for _, m := range in.Material {
		total += len(m)
	}
	if total == 0 {
		return ""
	}
	if total > maxScanBytes {
		return ReasonOversize
	}
	deadline := time.Now().Add(scanTimeout)
	never := mergedNeverPaths(in.NeverPaths)
	for _, m := range in.Material {
		if r := scanText(m, never, deadline); r != "" {
			return r
		}
	}
	return ""
}

// mergedNeverPaths is the default never-paths plus the project's additions,
// lower-cased (the match is case-insensitive, as the filesystems are).
func mergedNeverPaths(extra []string) []string {
	out := make([]string, 0, len(decision.DefaultNeverPaths)+len(extra))
	for _, p := range decision.DefaultNeverPaths {
		out = append(out, strings.ToLower(p))
	}
	for _, p := range extra {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.ToLower(p))
		}
	}
	return out
}

func scanText(s string, never []string, deadline time.Time) string {
	for start := 0; start < len(s); start += scanChunk {
		if !time.Now().Before(deadline) {
			return ReasonTimeout
		}
		end := start + scanChunk + scanOverlap
		if end > len(s) {
			end = len(s)
		}
		chunk := s[start:end]
		for i := range secretscan.DefaultPatterns {
			if secretscan.DefaultPatterns[i].Regex.MatchString(chunk) {
				return ReasonSecret
			}
		}
		if chunkNamesNeverPath(chunk, never) {
			return ReasonNeverPath
		}
	}
	return ""
}

func isTokenSep(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', '=', ';', '|', '<', '>', '(', ')', '[', ']', '{', '}', ',', ':':
		return true
	}
	return false
}

// chunkNamesNeverPath reports whether any token of text is a path under a
// never-path pattern. Every token is tried, so a prose mention of ".env" counts:
// over-triggering only narrows the chain to the primary runtime.
func chunkNamesNeverPath(text string, never []string) bool {
	for _, tok := range strings.FieldsFunc(text, isTokenSep) {
		tok = strings.TrimRight(tok, ".!?")
		if tok == "" || len(tok) > maxToken {
			continue
		}
		if pathIsNever(tok, never) {
			return true
		}
	}
	return false
}

// pathIsNever mirrors decision.pathIsNever: case-insensitive, either slash,
// the whole path, the path anchored at the root, and its base name.
func pathIsNever(p string, patterns []string) bool {
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	rooted := "/" + strings.TrimPrefix(p, "/")
	for _, pat := range patterns {
		if fnmatch.Match(pat, p) || fnmatch.Match(pat, rooted) || (base != p && fnmatch.Match(pat, base)) {
			return true
		}
	}
	return false
}
