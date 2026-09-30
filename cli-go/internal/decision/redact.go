package decision

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/hooks/fnmatch"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
)

// Egress levels (ADR-0009 §6). Redaction and the never_paths rule apply at
// every level; the level only changes how much of a string may leave.
//
// Redaction is pattern-based and therefore BEST EFFORT: a secret that matches
// no pattern and sits under no never_paths entry leaves verbatim. The allowlist
// (state_fields), the previews, and the size cap are the primary controls.
const (
	EgressStrict   = "strict"   // previews: <= 2 KiB per string (default)
	EgressPreviews = "previews" // <= 8 KiB per string
	EgressFull     = "full"     // no per-string cap (redaction and the hard cap still apply)
)

const (
	strictPreviewBytes   = 2 * 1024
	previewsPreviewBytes = 8 * 1024
	pathStringCap        = 8 * 1024
	// preRedactCap bounds the text the regexes see. It is far above the state
	// cap, so a token cut here is cut well beyond anything that can leave.
	preRedactCap = 256 * 1024
	// HardMaxStateBytes caps the serialized state regardless of level or
	// schema. The vendor limit is 32k tokens for state plus the longest
	// single question (https://docs.typesafe.ai/models); 64 KiB stays under it
	// even at ~3 bytes/token for code.
	HardMaxStateBytes = 64 * 1024

	withheldMarker = "[content withheld: secret path]"
	truncMarker    = "…[truncated]"
)

// EgressRank orders levels from most to least restrictive; unknown is strict.
func EgressRank(level string) int {
	switch level {
	case EgressFull:
		return 2
	case EgressPreviews:
		return 1
	default:
		return 0
	}
}

// pathKey reports whether a state key names a file path or command target
// that never_paths applies to, at any depth.
func pathKey(k string) bool {
	if pureFilePathKey(k) {
		return true
	}
	l := strings.ToLower(k)
	return l == "cmd" || l == "command" || strings.HasPrefix(l, "command_") || strings.HasPrefix(l, "command-")
}

// pureFilePathKey is a key whose value IS a path. Only these values still
// leave when a secret path is present; a command may carry file content
// (heredocs), so it is withheld like any other sibling.
func pureFilePathKey(k string) bool {
	l := strings.ToLower(k)
	if strings.Contains(l, "path") {
		return true
	}
	switch l {
	case "file", "filename", "notebook":
		return true
	}
	return false
}

// Patterns beyond the secret-scan table. The AWS/GitHub/Slack/Stripe/
// Anthropic/Google/PEM-header patterns are reused from secretscan (one shared
// table); the patterns below are egress-only additions, because widening the
// blocking hook's table is a separate, behaviour-changing decision.
var (
	// A whole PEM private key block; when the preview cut off the footer the
	// rest of the string is consumed.
	pemBlockRE = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----|$)`)
	// name=value / name: value credential assignments, any case.
	credAssignRE = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]*(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|token|credential|authorization|webhook|dsn)[A-Za-z0-9_.-]*)(\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`)
	// Env-style lines: any UPPER_SNAKE name containing a credential word has
	// the rest of the line redacted (DB_PASS, MY_SERVICE_PWD, STRIPE_KEY...).
	envLineRE = regexp.MustCompile(`(?m)^([ \t]*(?:export[ \t]+)?[A-Z0-9_]*(?:PASS|PWD|SECRET|TOKEN|KEY|AUTH|CRED|DSN|WEBHOOK)[A-Z0-9_]*[ \t]*[=:][ \t]*)[^\r\n]+`)
	// An Authorization header: the rest of the line (or quoted span) is the
	// credential whatever the scheme.
	authHeaderRE = regexp.MustCompile(`(?i)(\bauthorization\s*[:=]\s*)[^\r\n']+`)
	// scheme://user:password@host userinfo.
	urlCredsRE = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@]*:[^@\s/]+@`)
	jwtRE      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	// Authorization schemes: Bearer / Basic / Token / Digest.
	authSchemeRE = regexp.MustCompile(`(?i)\b(bearer|basic|token|digest)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// Short Stripe keys and webhook secrets (the shared table needs 24+).
	stripeShortRE = regexp.MustCompile(`\b(?:[sr]k_(?:live|test)|whsec)_[A-Za-z0-9]{6,}`)
	slackHookRE   = regexp.MustCompile(`https://hooks\.slack\.com/services/[A-Za-z0-9/_-]+`)
	base64RunRE   = regexp.MustCompile(`[A-Za-z0-9+/]{400,}={0,2}`)
	// A standalone 40-char base64 token: the shape of a bare AWS secret key.
	// Confirmed by mixed case + digit so 40-hex git SHAs are left alone.
	awsSecretRE = regexp.MustCompile(`[A-Za-z0-9/+]{40}`)

	// K-111 P2b: credentials that ride on a command line as FLAGS. The shadow
	// call is the first surface to ship raw shell commands, so these shapes
	// matter now. Each rule keeps its prefix (group 1) and drops the value.
	//
	// TypeSafe's own key shape, whatever its length.
	apikeyTokenRE = regexp.MustCompile(`\bapikey_[A-Za-z0-9_-]{6,}`)
	// --password X, --api-key=X, --client-secret X ... (the name may be
	// prefixed or suffixed with dash-separated words; "--author" is not one).
	longSecretFlagRE = regexp.MustCompile(`(?i)(--(?:[a-z0-9]+-)*(?:password|passwd|pass|pwd|secret|api-?key|access-?key|token|auth|authorization)(?:-[a-z0-9]+)*[\s=])("[^"]*"|'[^']*'|\S+)`)
	// Short password flags of specific tools (the flag letter alone is too
	// generic to match everywhere). Tool names are case-insensitive, the flag
	// letter is not (mysql -P is a port).
	mysqlPassRE = regexp.MustCompile(`((?i:\b(?:mysql|mysqldump|mariadb|mysqladmin)\b)[^\n;|&]*?\s-p)("[^"]*"|'[^']*'|\S+)`)
	loginPassRE = regexp.MustCompile(`((?i:\b(?:docker|podman|helm|oras|buildah|skopeo)\b)[^\n;|&]*?\blogin\b[^\n;|&]*?\s-p[\s=]?)("[^"]*"|'[^']*'|\S+)`)
	sshpassRE   = regexp.MustCompile(`(\bsshpass\b[^\n;|&]*?\s-p[\s=]?)("[^"]*"|'[^']*'|\S+)`)
	redisAuthRE = regexp.MustCompile(`(\bredis-cli\b[^\n;|&]*?\s-a[\s=]?)("[^"]*"|'[^']*'|\S+)`)
	htpasswdRE  = regexp.MustCompile(`(\bhtpasswd\b[^\n;|&]*?\s-[a-zA-Z]*b[a-zA-Z]*\s+\S+\s+\S+\s+)("[^"]*"|'[^']*'|\S+)`)
	// X-Api-Key / X-Auth-Token style headers (whole rest of the line or quoted span).
	xHeaderRE = regexp.MustCompile(`(?i)(\bx-[a-z0-9-]*(?:api-?key|token|secret|auth|password)[a-z0-9-]*\s*:\s*)[^\r\n']+`)
)

// redactPrefixed keeps group 1 (the flag) and replaces the rest of the match
// with a marker, unless the match is already redacted.
func redactPrefixed(s string, re *regexp.Regexp, kind string, bump func()) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Contains(m, "[REDACTED") {
			return m
		}
		sub := re.FindStringSubmatch(m)
		bump()
		return sub[1] + "[REDACTED:" + kind + "]"
	})
}

// sensitiveKeyRE matches JSON object keys whose value is redacted wholesale.
var sensitiveKeyRE = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|api[_-]?key|access[_-]?key|private[_-]?key|token|credential|authorization|webhook|dsn)|(^|[_.-])(pass|auth|key)($|[_.-])`)

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

type sanitizer struct {
	perString int
	never     []string
	st        *SanitizeStats
	withhold  bool
}

// Sanitize turns raw state into the redacted, capped, allowlisted state that
// may leave the machine. It fails with ClassOversize when the result still
// exceeds the cap, and with ClassBadRequest for a state that is not a JSON
// object (state is always named fields, never prose).
func Sanitize(state any, o SanitizeOptions) (any, SanitizeStats, error) {
	var st SanitizeStats
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
	s := &sanitizer{
		never: append(append([]string{}, DefaultNeverPaths...), o.NeverPaths...),
		st:    &st,
	}
	switch o.Level {
	case EgressFull:
		s.perString = 0
	case EgressPreviews:
		s.perString = previewsPreviewBytes
	default: // strict, and any unrecognised level fails closed to strict
		s.perString = strictPreviewBytes
	}

	allowed := map[string]bool{}
	for _, f := range o.AllowedFields {
		allowed[f] = true
	}
	kept := map[string]any{}
	for k, v := range obj {
		if allowed[k] {
			kept[k] = v
		}
	}
	// A secret path ANYWHERE in the allowed state withholds every content
	// string in it: the path names the file, the other fields may be its body.
	if hasSecretPath("", kept, s.never) {
		s.withhold = true
		st.Withheld = true
	}
	out := s.walk("", kept).(map[string]any)

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

// hasSecretPath walks the whole value for a path-named key (or command target)
// that matches never_paths.
func hasSecretPath(key string, v any, never []string) bool {
	switch t := v.(type) {
	case string:
		if !pathKey(key) {
			return false
		}
		if pathIsNever(t, never) {
			return true
		}
		for _, tok := range strings.FieldsFunc(t, func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\'' || r == '=' || r == ';' || r == '|' || r == '<' || r == '>' || r == '(' || r == ')'
		}) {
			if pathIsNever(tok, never) {
				return true
			}
		}
	case map[string]any:
		for k, x := range t {
			if hasSecretPath(k, x, never) {
				return true
			}
		}
	case []any:
		for _, x := range t {
			if hasSecretPath(key, x, never) {
				return true
			}
		}
	}
	return false
}

func pathIsNever(p string, patterns []string) bool {
	// Case-insensitive: macOS and Windows filesystems are, so a/b/.ENV is .env.
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	for _, pat := range patterns {
		pat = strings.ToLower(pat)
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

// walk redacts a value. Map keys are redacted too (a secret can be a key).
func (s *sanitizer) walk(key string, v any) any {
	switch t := v.(type) {
	case string:
		return s.str(key, t)
	case map[string]any:
		names := make([]string, 0, len(t))
		for k := range t {
			names = append(names, k)
		}
		sort.Strings(names)
		out := make(map[string]any, len(t))
		for _, k := range names {
			// Redact the whole key first, then truncate (as for values), so a long
			// key cannot cut a token in half.
			nk := RedactText(truncateUTF8(k, preRedactCap), &s.st.Redactions)
			if len(nk) > 256 {
				nk = truncateUTF8(nk, 256)
			}
			for i := 2; ; i++ { // keep redacted keys from colliding
				if _, dup := out[nk]; !dup {
					break
				}
				nk = fmt.Sprintf("%s#%d", nk, i)
			}
			out[nk] = s.walk(k, t[k])
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = s.walk(key, t[i])
		}
		return out
	default:
		return v
	}
}

func (s *sanitizer) str(key, v string) string {
	isPath := pureFilePathKey(key)
	if s.withhold && !isPath {
		return withheldMarker
	}
	if sensitiveKeyRE.MatchString(key) && v != "" {
		s.st.Redactions++
		return "[REDACTED:credential-field]"
	}
	cap := s.perString
	if isPath && (cap == 0 || cap < pathStringCap) {
		cap = pathStringCap
	}
	return redactString(v, cap, s.st)
}

// redactString redacts the WHOLE string first (so a secret straddling the
// preview cut is matched intact), then truncates, then redacts again (the
// cut can leave a fresh partial PEM header or marker fragment).
func redactString(s string, perString int, st *SanitizeStats) string {
	s = truncateUTF8(s, preRedactCap)
	s = RedactText(s, &st.Redactions)
	if perString > 0 && len(s) > perString {
		s = truncateUTF8(s, perString) + truncMarker
		s = RedactText(s, &st.Redactions)
	}
	return s
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
	// Whitespace is normalised BEFORE any credential rule runs, so a flag and its
	// value cannot be separated by anything the rules do not expect (two spaces,
	// tabs, a backslash-newline continuation, CRLF, U+00A0). Pass 1 keeps
	// newlines, so the line-aware rules (env files, PEM) stay exact. Pass 2
	// joins the lines too, and its output is what is returned and sent: the
	// provider only needs the gist of a command or diff.
	s = redactPass(normalizeWhitespace(s, true), count)
	return redactPass(normalizeWhitespace(s, false), count)
}

// normalizeWhitespace joins shell line continuations (backslash, optional CR,
// LF) and collapses every run of whitespace, Unicode spaces included, to one
// space. With keepNewlines a line feed stays a line feed (runs of horizontal
// whitespace still collapse).
func normalizeWhitespace(s string, keepNewlines bool) string {
	s = lineContinuationRE.ReplaceAllString(s, " ")
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if keepNewlines && r == '\n' {
			b.WriteByte('\n')
			space = false
			continue
		}
		if unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

var lineContinuationRE = regexp.MustCompile(`\\\r?\n`)

func redactPass(s string, count *int) string {
	n := 0
	bump := func() { n++ }
	// Order matters: whole PEM blocks first, then the shared secret-scan
	// table, then the egress-only shapes, then base64 runs.
	s = pemBlockRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:pem-private-key]" })
	s = apikeyTokenRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:apikey]" })
	for _, p := range secretscan.DefaultPatterns {
		name := kindName(p.Name)
		s = p.Regex.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:" + name + "]" })
	}
	s = slackHookRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:slack-webhook]" })
	s = stripeShortRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:stripe-key]" })
	s = urlCredsRE.ReplaceAllStringFunc(s, func(m string) string {
		bump()
		return urlCredsRE.FindStringSubmatch(m)[1] + "[REDACTED:url-credentials]@"
	})
	{
		var c int
		s, c = secretscan.RedactKeep(s, "[REDACTED:basic-auth]")
		n += c
	}
	s = redactPrefixed(s, longSecretFlagRE, "flag-secret", bump)
	s = redactPrefixed(s, mysqlPassRE, "password", bump)
	s = redactPrefixed(s, loginPassRE, "password", bump)
	s = redactPrefixed(s, sshpassRE, "password", bump)
	s = redactPrefixed(s, redisAuthRE, "password", bump)
	s = redactPrefixed(s, htpasswdRE, "password", bump)
	s = redactPrefixed(s, xHeaderRE, "header", bump)
	s = jwtRE.ReplaceAllStringFunc(s, func(string) string { bump(); return "[REDACTED:jwt]" })
	s = authSchemeRE.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Contains(m, "[REDACTED") {
			return m
		}
		bump()
		return authSchemeRE.FindStringSubmatch(m)[1] + " [REDACTED:auth]"
	})
	s = authHeaderRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := authHeaderRE.FindStringSubmatch(m)
		if strings.Contains(m[len(sub[1]):], "[REDACTED:authorization]") {
			return m
		}
		bump()
		return sub[1] + "[REDACTED:authorization]"
	})
	s = envLineRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := envLineRE.FindStringSubmatch(m)
		if strings.HasPrefix(strings.TrimSpace(m[len(sub[1]):]), "[REDACTED") {
			return m
		}
		bump()
		return sub[1] + "[REDACTED:env-value]"
	})
	s = credAssignRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := credAssignRE.FindStringSubmatch(m)
		if strings.HasPrefix(sub[3], "[REDACTED") {
			return m // already redacted by a more specific pattern
		}
		bump()
		return sub[1] + sub[2] + "[REDACTED:credential]"
	})
	s = awsSecretRE.ReplaceAllStringFunc(s, func(m string) string {
		if !mixedSecretShape(m) {
			return m
		}
		bump()
		return "[REDACTED:secret-key-shape]"
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

func mixedSecretShape(m string) bool {
	var up, low, dig bool
	for _, r := range m {
		switch {
		case r >= 'A' && r <= 'Z':
			up = true
		case r >= 'a' && r <= 'z':
			low = true
		case r >= '0' && r <= '9':
			dig = true
		}
	}
	return up && low && dig
}

func kindName(n string) string {
	return strings.ToLower(strings.NewReplacer(" ", "-", "(", "", ")", "").Replace(n))
}
