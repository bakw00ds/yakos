package decision

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/hooks/fnmatch"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
)

// Egress levels (ADR-0009 §6). Redaction and the never_paths rule apply at
// every level; the level only changes how much of a string may leave.
const (
	EgressStrict   = "strict"   // previews: <= 2 KiB per string (default)
	EgressPreviews = "previews" // <= 8 KiB per string
	EgressFull     = "full"     // no per-string cap (redaction and the hard cap still apply)
)

const (
	strictPreviewBytes   = 2 * 1024
	previewsPreviewBytes = 8 * 1024
	// HardMaxStateBytes caps the serialized state regardless of level or
	// schema. The vendor limit is 32k tokens for state plus the longest
	// question (https://docs.typesafe.ai/models); 64 KiB stays under it even
	// at ~3 bytes/token for code.
	HardMaxStateBytes = 64 * 1024

	withheldMarker = "[content withheld: secret path]"
	truncMarker    = "…[truncated]"
)

// pathKeys are state field names treated as file paths for never_paths.
var pathKeys = map[string]bool{"file_path": true, "path": true, "filepath": true, "file": true, "notebook_path": true}

// Patterns beyond the secret-scan table. The AWS/GitHub/Slack/Stripe/
// Anthropic/Google/PEM-header patterns are reused from secretscan.
var (
	// A whole PEM private key block; when the preview cut off the footer the
	// rest of the string is consumed.
	pemBlockRE = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)
	// name=value / name: value credential assignments (the supervisor
	// stream's own shape, widened to common env-style names).
	credAssignRE = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:password|passwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|token|credential)[A-Za-z0-9_.-]*)(\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`)
	// scheme://user:password@host userinfo.
	urlCredsRE  = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@]+:[^@\s/]+@`)
	jwtRE       = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	bearerRE    = regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=-]{16,}`)
	base64RunRE = regexp.MustCompile(`[A-Za-z0-9+/]{400,}={0,2}`)
)

// sensitiveKeyRE matches JSON object keys whose value is redacted wholesale.
var sensitiveKeyRE = regexp.MustCompile(`(?i)(password|passwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|token|credential|authorization)`)

// SanitizeOptions configures Sanitize.
type SanitizeOptions struct {
	Level         string
	NeverPaths    []string // merged with DefaultNeverPaths
	AllowedFields []string // top-level state fields allowed to leave; empty means none
	MaxBytes      int      // schema cap; clamped to HardMaxStateBytes
}

// SanitizeStats reports what happened, never the content.
type SanitizeStats struct {
	Redactions int
	Withheld   bool
	Bytes      int
}

// Sanitize turns raw state into the redacted, capped, allowlisted state that
// may leave the machine. It fails with ClassOversize when the result still
// exceeds the cap, and with ClassBadRequest for a state that is not a JSON
// object (state is always named fields, never prose).
func Sanitize(state any, o SanitizeOptions) (any, SanitizeStats, error) {
	var st SanitizeStats
	// Normalise through JSON so any Go value becomes generic maps/slices.
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, st, newErr(ClassBadRequest, "state is not JSON-serialisable")
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, st, newErr(ClassBadRequest, "state is not valid JSON")
	}
	obj, ok := generic.(map[string]any)
	if !ok {
		return nil, st, newErr(ClassBadRequest, "state must be a JSON object of named fields")
	}

	limit := o.MaxBytes
	if limit <= 0 || limit > HardMaxStateBytes {
		limit = HardMaxStateBytes
	}
	perString := 0
	switch o.Level {
	case EgressFull:
		perString = 0
	case EgressPreviews:
		perString = previewsPreviewBytes
	default: // strict, and any unrecognised level fails closed to strict
		perString = strictPreviewBytes
	}
	never := append(append([]string{}, DefaultNeverPaths...), o.NeverPaths...)

	allowed := map[string]bool{}
	for _, f := range o.AllowedFields {
		allowed[f] = true
	}
	secretPath := false
	out := map[string]any{}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowed[k] {
			continue // not an allowlisted egress field
		}
		if pathKeys[k] {
			if s, ok := obj[k].(string); ok && pathIsNever(s, never) {
				secretPath = true
			}
		}
	}
	for _, k := range keys {
		if !allowed[k] {
			continue
		}
		v := obj[k]
		if pathKeys[k] {
			// Paths leave (redacted, not truncated below a useful length),
			// even for secret paths: the path is the only thing that does.
			out[k] = redactValue(k, v, 0, &st)
			continue
		}
		if secretPath {
			out[k] = withholdValue(v)
			st.Withheld = true
			continue
		}
		out[k] = redactValue(k, v, perString, &st)
	}

	b, err := json.Marshal(out)
	if err != nil {
		return nil, st, newErr(ClassBadRequest, "state is not JSON-serialisable")
	}
	st.Bytes = len(b)
	if len(b) > limit {
		return nil, st, newErr(ClassOversize, "sanitized state is %d bytes (cap %d)", len(b), limit)
	}
	return out, st, nil
}

func pathIsNever(p string, patterns []string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	for _, pat := range patterns {
		if fnmatch.Match(pat, p) || fnmatch.Match(pat, "/"+strings.TrimPrefix(p, "/")) {
			return true
		}
		// Also test the base name so "*.pem" style patterns catch any depth.
		if i := strings.LastIndex(p, "/"); i >= 0 && fnmatch.Match(pat, p[i+1:]) {
			return true
		}
	}
	return false
}

// withholdValue replaces every string leaf with the withheld marker,
// preserving shape (booleans and numbers carry no secrets we can name).
func withholdValue(v any) any {
	switch t := v.(type) {
	case string:
		return withheldMarker
	case map[string]any:
		out := make(map[string]any, len(t))
		for k := range t {
			out[k] = withholdValue(t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = withholdValue(t[i])
		}
		return out
	default:
		return v
	}
}

func redactValue(key string, v any, perString int, st *SanitizeStats) any {
	switch t := v.(type) {
	case string:
		if sensitiveKeyRE.MatchString(key) && t != "" {
			st.Redactions++
			return "[REDACTED:credential-field]"
		}
		return redactString(t, perString, st)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k := range t {
			out[k] = redactValue(k, t[k], perString, st)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = redactValue(key, t[i], perString, st)
		}
		return out
	default:
		return v
	}
}

// redactString truncates to perString bytes FIRST (a secret straddling the cut
// is then either whole and redacted, or partial and harmless), then redacts.
// It re-checks after redaction so a truncated prefix of a PEM header is caught
// by the block pattern's "$" alternative.
func redactString(s string, perString int, st *SanitizeStats) string {
	if perString > 0 && len(s) > perString {
		s = truncateUTF8(s, perString) + truncMarker
	}
	return RedactText(s, &st.Redactions)
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// RedactText replaces secret-shaped substrings with [REDACTED:<kind>] and adds
// the number of replacements to *count (if non-nil).
func RedactText(s string, count *int) string {
	n := 0
	bump := func() { n++ }
	// Order matters: whole PEM blocks first, then the shared secret-scan
	// table, then assignment/JWT/Bearer shapes, then base64 runs.
	s = pemBlockRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:pem-private-key]" })
	for _, p := range secretscan.DefaultPatterns {
		name := kindName(p.Name)
		s = p.Regex.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:" + name + "]" })
	}
	s = urlCredsRE.ReplaceAllStringFunc(s, func(m string) string {
		bump()
		return urlCredsRE.FindStringSubmatch(m)[1] + "[REDACTED:url-credentials]@"
	})
	s = jwtRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:jwt]" })
	s = bearerRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "Bearer [REDACTED:bearer]" })
	s = credAssignRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := credAssignRE.FindStringSubmatch(m)
		if strings.HasPrefix(sub[3], "[REDACTED") {
			return m // already redacted by a more specific pattern
		}
		bump()
		return sub[1] + sub[2] + "[REDACTED:credential]"
	})
	s = base64RunRE.ReplaceAllStringFunc(s, func(m string) string {
		bump()
		return fmt.Sprintf("[BASE64:%d bytes]", len(m))
	})
	if count != nil {
		*count += n
	}
	return s
}

func kindName(n string) string {
	return strings.ToLower(strings.NewReplacer(" ", "-", "(", "", ")", "").Replace(n))
}
