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
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bakw00ds/yakos/internal/decision"
	"github.com/bakw00ds/yakos/internal/hooks/fnmatch"
	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
	"github.com/bakw00ds/yakos/internal/projectcfg"
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

// extraNeverPaths are credential files the shared decision.DefaultNeverPaths
// lacks (K-140 fixup F6). Kept here, not in the shared egress list, so this
// change does not alter what the egress layer withholds.
var extraNeverPaths = []string{
	"**/id_ecdsa*", "**/id_dsa*", "**/id_ed25519*", "**/id_rsa*",
	"**/.kube/config", ".kube/config", "**/.pypirc", ".pypirc",
	"**/.config/gcloud/application_default_credentials.json",
	"**/*.tfvars", "*.tfvars",
}

// SensitiveClassifier is the Classifier that finds the sensitive class: the
// default for ActiveClassifier.
func SensitiveClassifier(in Input) string {
	if SensitiveReason(in) != "" {
		return ClassSensitive
	}
	return ClassDefault
}

// SensitiveReason scans the input and returns why it is sensitive, "" when
// nothing in it is. It fails closed: a timeout, too much text or a panic is a
// reason, not a pass. Material is scanned for secret patterns and credential-file
// names; SecretOnly (the agent's own prompt, which may legitimately say "never
// edit .env") for secret patterns only.
//
// A result is remembered by content hash for a short while (reasonCache), so the
// chat pre-check and the dispatch that follows it, and Classify followed by
// ClassifyReason, scan the same text once.
func SensitiveReason(in Input) (reason string) {
	total := 0
	for _, m := range in.Material {
		total += len(m)
	}
	for _, m := range in.SecretOnly {
		total += len(m)
	}
	if total == 0 {
		return ""
	}
	key := reasonKey(in)
	if r, ok := reasonCache.get(key); ok {
		return r
	}
	defer func() {
		if recover() != nil {
			reason = ReasonError
		}
	}()
	reason = scanInput(in, total)
	if reason != ReasonTimeout && reason != ReasonError {
		reasonCache.put(key, reason)
	}
	return reason
}

func scanInput(in Input, total int) string {
	if total > maxScanBytes {
		return ReasonOversize
	}
	deadline := time.Now().Add(scanTimeout)
	never := compileNever(mergedNeverPaths(in.NeverPaths))
	for _, m := range in.Material {
		if r := scanText(m, never, deadline, true); r != "" {
			return r
		}
	}
	for _, m := range in.SecretOnly {
		if r := scanText(m, never, deadline, false); r != "" {
			return r
		}
	}
	return ""
}

// reasonKey hashes everything the result depends on: the texts, the project
// globs and the limits (tests shrink them). Length-prefixed, so two inputs that
// concatenate alike do not collide.
func reasonKey(in Input) [sha256.Size]byte {
	h := sha256.New()
	var n [8]byte
	put := func(s string) {
		binary.LittleEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	putInt := func(v int64) {
		binary.LittleEndian.PutUint64(n[:], uint64(v))
		h.Write(n[:])
	}
	putInt(int64(scanTimeout))
	putInt(int64(maxScanBytes))
	putInt(int64(len(in.Material)))
	for _, m := range in.Material {
		put(m)
	}
	putInt(int64(len(in.SecretOnly)))
	for _, m := range in.SecretOnly {
		put(m)
	}
	putInt(int64(len(in.NeverPaths)))
	for _, m := range in.NeverPaths {
		put(m)
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// reasonCache holds recent results, keyed by content hash: no text, only a fixed
// vocabulary word. Small and short-lived, because it only has to span the
// handful of calls one request makes.
var reasonCache = &reasonMemo{m: map[[sha256.Size]byte]reasonEntry{}}

type reasonEntry struct {
	reason string
	at     time.Time
}

type reasonMemo struct {
	mu sync.Mutex
	m  map[[sha256.Size]byte]reasonEntry
}

const (
	reasonCacheMax = 32
	reasonCacheTTL = 30 * time.Second
)

func (c *reasonMemo) get(k [sha256.Size]byte) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Since(e.at) > reasonCacheTTL {
		return "", false
	}
	return e.reason, true
}

func (c *reasonMemo) put(k [sha256.Size]byte, reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= reasonCacheMax {
		for kk, e := range c.m { // drop the expired, else everything: it is only a memo
			if time.Since(e.at) > reasonCacheTTL {
				delete(c.m, kk)
			}
		}
		if len(c.m) >= reasonCacheMax {
			c.m = map[[sha256.Size]byte]reasonEntry{}
		}
	}
	c.m[k] = reasonEntry{reason: reason, at: time.Now()}
}

func (c *reasonMemo) reset() {
	c.mu.Lock()
	c.m = map[[sha256.Size]byte]reasonEntry{}
	c.mu.Unlock()
}

// mergedNeverPaths is the default never-paths plus the project's additions,
// lower-cased (the match is case-insensitive, as the filesystems are). A project
// glob past the count or complexity bound is dropped (projectcfg warns): the
// defaults always stay.
func mergedNeverPaths(extra []string) []string {
	out := make([]string, 0, len(decision.DefaultNeverPaths)+len(extraNeverPaths)+len(extra))
	for _, p := range decision.DefaultNeverPaths {
		out = append(out, strings.ToLower(p))
	}
	for _, p := range extraNeverPaths {
		out = append(out, strings.ToLower(p))
	}
	added := 0
	for _, p := range extra {
		p = strings.TrimSpace(p)
		if p == "" || !projectcfg.NeverPathSimpleEnough(p) {
			continue
		}
		if added++; added > projectcfg.MaxNeverPaths {
			break
		}
		out = append(out, strings.ToLower(p))
	}
	return out
}

// invisible are format characters that split or hide a token from a pattern but
// not from a model reading it.
const invisible = "\u200b\u200c\u200d\u2060\ufeff\u00ad"

// stripInvisible drops the invisible format characters and maps every non-ASCII
// space (NBSP, ideographic, line separator) to a plain space, so a PEM header
// or a token split by one is seen as the model reads it.
func stripInvisible(s string) string {
	plain := true
	for _, r := range s {
		if r >= 0x80 && (unicode.IsSpace(r) || strings.ContainsRune(invisible, r)) {
			plain = false
			break
		}
	}
	if plain {
		return s
	}
	return strings.Map(func(r rune) rune {
		if r >= 0x80 && strings.ContainsRune(invisible, r) {
			return -1
		}
		if r >= 0x80 && unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
}

// scanText checks one text. names is false for the agent's own prompt: secret
// patterns only. The deadline is checked before every chunk and every
// token inside one, so a text cannot run past it by a whole chunk.
func scanText(s string, never *neverSet, deadline time.Time, names bool) string {
	s = stripInvisible(s)
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
		if names {
			if r := chunkNamesNeverPath(chunk, never, deadline); r != "" {
				return r
			}
		}
	}
	return ""
}

// isTokenSep splits a chunk into path-like tokens: ASCII shell and prose
// delimiters, and every non-ASCII space, punctuation, symbol or format
// character (NBSP, an em dash, a full-width bracket).
func isTokenSep(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', '=', ';', '|', '<', '>', '(', ')', '[', ']', '{', '}', ',', ':', '&':
		return true
	}
	if r < 0x20 || r == 0x7f {
		return true
	}
	if r < 0x80 {
		return false
	}
	return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// Characters that decorate a path in prose or markup without being part of it:
// `@.env` (a file mention), `**.env**` and `_.env_` (emphasis), a trailing `.`.
const (
	decorLeft  = "@*_~&+-!?^%$#"
	decorRight = ".!?*_~&+-^%$#@"
)

// chunkNamesNeverPath reports ReasonNeverPath when any token of text is a path
// under a never-path pattern, ReasonTimeout when the deadline passes first, ""
// otherwise. Every token is tried, so a prose mention of ".env" counts:
// over-triggering only narrows the chain to the primary runtime.
func chunkNamesNeverPath(text string, never *neverSet, deadline time.Time) string {
	for _, tok := range strings.FieldsFunc(text, isTokenSep) {
		if !time.Now().Before(deadline) {
			return ReasonTimeout
		}
		if tokenNamesNeverPath(tok, never) {
			return ReasonNeverPath
		}
	}
	return ""
}

// tokenNamesNeverPath tries the token as written and with its decoration
// trimmed. A token longer than maxToken is matched by its first and its last
// maxToken bytes (the prefix, and the base name), not skipped.
func tokenNamesNeverPath(tok string, never *neverSet) bool {
	right := strings.TrimRight(tok, decorRight)
	left := strings.TrimLeft(tok, decorLeft)
	both := strings.TrimLeft(right, decorLeft)
	last := ""
	for _, v := range [...]string{tok, right, left, both} {
		if v == "" || v == last {
			continue
		}
		last = v
		if len(v) > maxToken {
			if never.pathIsNever(v[:maxToken]) || never.pathIsNever(v[len(v)-maxToken:]) {
				return true
			}
			continue
		}
		if never.pathIsNever(v) {
			return true
		}
	}
	return false
}

// neverSet is the never-paths compiled once per scan, with a bounded memo of
// the tokens already judged (prose repeats its words).
type neverSet struct {
	pats []neverPat
	memo map[string]bool
}

// neverPat is one glob with the literal text any match must contain, so most
// (pattern, name) pairs are rejected by string compares before the matcher runs.
type neverPat struct {
	runes  []rune
	prefix string // literal before the first wildcard
	suffix string // literal after the last wildcard or bracket
	lit    string // longest literal run; "" when the glob has a bracket or `\`
	exact  bool   // no wildcard at all: the name must equal the glob
}

const maxMemo = 8192

func compileNever(patterns []string) *neverSet {
	n := &neverSet{memo: map[string]bool{}}
	for _, p := range patterns {
		n.pats = append(n.pats, compilePat(p))
	}
	return n
}

func compilePat(p string) neverPat {
	np := neverPat{runes: []rune(p)}
	if strings.ContainsRune(p, '\\') {
		return np
	}
	const specials = "*?[]"
	first := strings.IndexAny(p, specials)
	if first < 0 {
		np.exact = true
		np.prefix = p
		return np
	}
	np.prefix = p[:first]
	np.suffix = p[strings.LastIndexAny(p, specials)+1:]
	if !strings.ContainsAny(p, "[]") {
		for _, run := range strings.FieldsFunc(p, func(r rune) bool { return r == '*' || r == '?' }) {
			if len(run) > len(np.lit) {
				np.lit = run
			}
		}
	}
	return np
}

// can reports whether name could match: false is certain, true is not.
func (np *neverPat) can(name string) bool {
	if np.exact {
		return name == np.prefix
	}
	return strings.HasPrefix(name, np.prefix) && strings.HasSuffix(name, np.suffix) && (np.lit == "" || strings.Contains(name, np.lit))
}

// pathIsNever mirrors decision.pathIsNever: case-insensitive, either slash,
// the whole path, the path anchored at the root, and its base name.
func (n *neverSet) pathIsNever(p string) bool {
	if v, ok := n.memo[p]; ok {
		return v
	}
	v := n.match(p)
	if len(n.memo) < maxMemo {
		n.memo[p] = v
	}
	return v
}

func (n *neverSet) match(p string) bool {
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	rooted := "/" + strings.TrimPrefix(p, "/")
	var pr, rr, br []rune // converted on first need
	for i := range n.pats {
		pat := &n.pats[i]
		if pat.can(p) {
			if pr == nil {
				pr = []rune(p)
			}
			if fnmatch.MatchRunes(pat.runes, pr) {
				return true
			}
		}
		if pat.can(rooted) {
			if rr == nil {
				rr = []rune(rooted)
			}
			if fnmatch.MatchRunes(pat.runes, rr) {
				return true
			}
		}
		if base != p && pat.can(base) {
			if br == nil {
				br = []rune(base)
			}
			if fnmatch.MatchRunes(pat.runes, br) {
				return true
			}
		}
	}
	return false
}
