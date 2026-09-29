// Package fnmatch implements bash `case` glob matching, shared by
// path-allowlist and the hook-bypass scope matcher.
package fnmatch

import (
	"unicode"
)

// Match reports whether name matches the shell glob pattern, using the
// same semantics as bash's `case "$name" in $pattern)` on a parameter-
// expanded (unquoted) pattern with no shopt options set:
//
//   - '*' matches any run of characters INCLUDING '/' and a leading '.'
//     (no FNM_PATHNAME, no FNM_PERIOD). This is the load-bearing
//     difference from Go's path.Match/filepath.Match, where '*' stops at
//     '/': under those, a deny of "api/migrations/**" would not match
//     "api/migrations/2026/x.sql" and would silently under-block.
//   - '?' matches exactly one character.
//   - '[...]' is a bracket expression: leading '!' or '^' negates, a
//     leading ']' is literal, 'a-z' is a range, '[:alpha:]'-style POSIX
//     classes are supported, and a backslash quotes the next character. An
//     unterminated '[' matches a literal '['.
//   - '\c' matches the literal character c (a trailing lone backslash
//     matches a literal backslash).
//
// Matching is on runes. Case folding is the caller's job (path-allowlist
// lowercases both sides, H5b).
func Match(pattern, name string) bool {
	return fnmatchRunes([]rune(pattern), []rune(name))
}

func fnmatchRunes(p, s []rune) bool {
	// Iterative matcher with single-star backtracking: O(len(p)*len(s))
	// worst case, no exponential blowup on adversarial "*a*a*a*b" patterns.
	pi, si := 0, 0
	starP, starS := -1, -1
	for si < len(s) {
		if pi < len(p) {
			switch p[pi] {
			case '*':
				// Collapse consecutive stars.
				for pi < len(p) && p[pi] == '*' {
					pi++
				}
				if pi == len(p) {
					return true
				}
				starP, starS = pi, si
				continue
			case '?':
				pi++
				si++
				continue
			case '[':
				if matched, next, ok := matchBracket(p, pi, s[si]); ok {
					if matched {
						pi = next
						si++
						continue
					}
				} else if s[si] == '[' {
					// Unterminated bracket: literal '['.
					pi++
					si++
					continue
				}
			case '\\':
				lit := '\\'
				adv := 1
				if pi+1 < len(p) {
					lit = p[pi+1]
					adv = 2
				}
				if s[si] == lit {
					pi += adv
					si++
					continue
				}
			default:
				if p[pi] == s[si] {
					pi++
					si++
					continue
				}
			}
		}
		// Mismatch: backtrack to the last star, consuming one more char.
		if starP >= 0 {
			starS++
			si = starS
			pi = starP
			continue
		}
		return false
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// matchBracket evaluates the bracket expression starting at p[start] ('[')
// against c. ok is false when the expression is unterminated (the caller
// then treats '[' as a literal). next is the index just past the closing
// ']' when ok.
func matchBracket(p []rune, start int, c rune) (matched bool, next int, ok bool) {
	i := start + 1
	negate := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		negate = true
		i++
	}
	first := true
	found := false
	for {
		if i >= len(p) {
			return false, 0, false
		}
		ch := p[i]
		if ch == ']' && !first {
			i++
			break
		}
		first = false

		// POSIX class: [:name:]
		if ch == '[' && i+1 < len(p) && p[i+1] == ':' {
			if end := indexClassEnd(p, i+2); end >= 0 {
				name := string(p[i+2 : end])
				if classMatch(name, c) {
					found = true
				}
				i = end + 2
				continue
			}
		}

		lo := ch
		if ch == '\\' && i+1 < len(p) {
			i++
			lo = p[i]
		}
		i++
		// Range lo-hi (a '-' right before the closing ']' is literal).
		if i+1 < len(p) && p[i] == '-' && p[i+1] != ']' {
			hi := p[i+1]
			adv := 2
			if hi == '\\' && i+2 < len(p) {
				hi = p[i+2]
				adv = 3
			}
			if lo <= c && c <= hi {
				found = true
			}
			i += adv
			continue
		}
		if c == lo {
			found = true
		}
	}
	return found != negate, i, true
}

func indexClassEnd(p []rune, from int) int {
	for j := from; j+1 < len(p); j++ {
		if p[j] == ':' && p[j+1] == ']' {
			return j
		}
	}
	return -1
}

func classMatch(name string, c rune) bool {
	switch name {
	case "alpha":
		return unicode.IsLetter(c)
	case "digit":
		return c >= '0' && c <= '9'
	case "alnum":
		return unicode.IsLetter(c) || unicode.IsDigit(c)
	case "upper":
		return unicode.IsUpper(c)
	case "lower":
		return unicode.IsLower(c)
	case "space":
		return unicode.IsSpace(c)
	case "blank":
		return c == ' ' || c == '\t'
	case "punct":
		return unicode.IsPunct(c) || unicode.IsSymbol(c)
	case "print":
		return unicode.IsPrint(c)
	case "graph":
		return unicode.IsGraphic(c) && !unicode.IsSpace(c)
	case "cntrl":
		return unicode.IsControl(c)
	case "xdigit":
		return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
	}
	return false
}
