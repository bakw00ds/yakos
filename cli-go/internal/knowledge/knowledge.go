// Package knowledge builds the knowledge block a non-claude harness gets in
// place of the rules claude loads natively (K-149): the always-loaded rules of
// the framework and of the project, then the agent body.
//
// The block is composed once per conversation and stored by the caller; every
// later turn re-sends the stored bytes, so the prefix stays byte-stable
// (rule:cache-stability). Nothing volatile (time, ids, paths) goes into it.
//
// # Truncation order
//
// The block is capped at MaxBytes. When the sections do not fit, whole
// sections are dropped, lowest priority first: framework rules from the last
// by name back to the first, then project rules the same way. The agent body
// is never dropped for size; when it alone does not fit it is cut at a rune
// boundary and ends with TruncationMark. Every part, kept or dropped, is
// listed in Pack.Parts.
//
// A file holding a secret pattern (the secret-scan table) is refused from the
// pack with a warning that names the file's stem, never its content.
package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bakw00ds/yakos/internal/hooks/secretscan"
)

const (
	// MaxBytes caps the composed block.
	MaxBytes = 24 << 10
	// MaxFileBytes caps one source file that is read.
	MaxFileBytes = 64 << 10
	// MaxSkillBytes caps the skill text appended to a user turn.
	MaxSkillBytes = 32 << 10
	// TruncationMark ends an agent body that was cut to fit.
	TruncationMark = "\n[truncated]\n"
)

// Part kinds.
const (
	KindRule        = "rule"
	KindProjectRule = "project-rule"
	KindAgent       = "agent"
)

// Part describes one source of the pack for the context drawer.
type Part struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Bytes     int    `json:"bytes"`
	Included  bool   `json:"included"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Pack is a composed knowledge block.
type Pack struct {
	Text     string
	SHA      string
	Parts    []Part
	Warnings []string
}

// Options names the inputs of Compose.
type Options struct {
	YakosRoot string
	Project   string
	Agent     string // agent name, shown as the section title
	AgentBody string // the agent's prompt body
}

// ErrSecret is returned by SkillText for a file holding a secret pattern.
var ErrSecret = errors.New("knowledge: file holds a secret pattern")

// ErrNotFound is returned by SkillText for a skill that does not exist.
var ErrNotFound = errors.New("knowledge: skill not found")

var (
	stemRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

type section struct {
	part Part
	text string
}

// Compose builds the pack. It never fails: a source that cannot be read or is
// refused is left out and named in Warnings.
func Compose(o Options) Pack {
	var warns []string
	fw, w := readRules(filepath.Join(o.YakosRoot, "lib", "rules"), KindRule, o.YakosRoot != "", false)
	warns = append(warns, w...)
	var pr []section
	if o.Project != "" {
		var w2 []string
		pr, w2 = readRules(filepath.Join(o.Project, ".claude", "rules"), KindProjectRule, true, true)
		warns = append(warns, w2...)
	}
	// A project rule replaces the framework rule of the same name.
	over := map[string]bool{}
	for _, s := range pr {
		over[s.part.Name] = true
	}
	kept := fw[:0:0]
	for _, s := range fw {
		if !over[s.part.Name] {
			kept = append(kept, s)
		}
	}
	fw = kept

	var agent *section
	if strings.TrimSpace(o.AgentBody) != "" {
		name := o.Agent
		if !stemRe.MatchString(name) {
			name = "agent"
		}
		if secretscan.Redact(o.AgentBody) != o.AgentBody {
			warns = append(warns, "agent "+name+": refused from the knowledge pack (secret pattern)")
		} else {
			body := clean(o.AgentBody)
			agent = &section{
				part: Part{Name: name, Kind: KindAgent},
				text: "## agent: " + name + "\n" + strings.TrimSpace(body) + "\n\n",
			}
		}
	}

	all := make([]*section, 0, len(fw)+len(pr)+1)
	for i := range fw {
		all = append(all, &fw[i])
	}
	for i := range pr {
		all = append(all, &pr[i])
	}
	for _, s := range all {
		s.part.Bytes = len(s.text)
		s.part.Included = true
	}
	if agent != nil {
		agent.part.Bytes = len(agent.text)
		agent.part.Included = true
		all = append(all, agent)
	}

	total := func() int {
		n := 0
		for _, s := range all {
			if s.part.Included {
				n += len(s.text)
			}
		}
		return n
	}
	// Drop lowest priority first: the last framework rule, ..., then the last
	// project rule. The agent (last in all) is never dropped.
	order := make([]*section, 0, len(fw)+len(pr))
	for i := len(fw) - 1; i >= 0; i-- {
		order = append(order, &fw[i])
	}
	for i := len(pr) - 1; i >= 0; i-- {
		order = append(order, &pr[i])
	}
	for _, s := range order {
		if total() <= MaxBytes {
			break
		}
		s.part.Included = false
	}
	if agent != nil && total() > MaxBytes {
		room := MaxBytes - (total() - len(agent.text)) - len(TruncationMark)
		if room < 0 {
			room = 0
		}
		cut := agent.text[:room]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		agent.text = cut + TruncationMark
		agent.part.Truncated = true
	}

	var b strings.Builder
	parts := make([]Part, 0, len(all))
	for _, s := range all {
		if s.part.Included {
			b.WriteString(s.text)
		}
		parts = append(parts, s.part)
	}
	text := b.String()
	sum := sha256.Sum256([]byte(text))
	return Pack{Text: text, SHA: hex.EncodeToString(sum[:]), Parts: parts, Warnings: warns}
}

// SHA returns the hex sha256 of text, the hash Compose reports.
func SHA(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// readRules reads the markdown rules of dir that have no paths: frontmatter,
// sorted by file name. A project directory that is, or sits under, a symlink is
// not read.
func readRules(dir, kind string, enabled, project bool) ([]section, []string) {
	if !enabled {
		return nil, nil
	}
	if project && (isLink(filepath.Dir(dir)) || isLink(dir)) {
		return nil, []string{"project rules directory is a symlink; skipped"}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = root.Close() }()
	ents, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, nil
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	var out []section
	var warns []string
	for _, e := range ents {
		fname := e.Name()
		if !strings.HasSuffix(fname, ".md") || !e.Type().IsRegular() {
			continue
		}
		stem := strings.TrimSuffix(fname, ".md")
		if !stemRe.MatchString(stem) || stem == "INDEX" {
			continue
		}
		data, err := readRooted(root, fname, MaxFileBytes)
		if err != nil {
			warns = append(warns, kind+" "+stem+": cannot be read; skipped")
			continue
		}
		raw := string(data)
		if secretscan.Redact(raw) != raw {
			warns = append(warns, kind+" "+stem+": refused from the knowledge pack (secret pattern)")
			continue
		}
		body, paths, ok := splitFront(raw)
		if !ok {
			warns = append(warns, kind+" "+stem+": malformed frontmatter; skipped")
			continue
		}
		if paths {
			continue // path-scoped: loads on file match, not at session start
		}
		body = strings.TrimSpace(clean(body))
		if body == "" {
			continue
		}
		out = append(out, section{
			part: Part{Name: stem, Kind: kind},
			text: "## " + kind + ": " + stem + "\n" + body + "\n\n",
		})
	}
	return out, warns
}

func isLink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

func readRooted(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("too large")
	}
	return data, nil
}

// splitFront separates a leading --- frontmatter block from the body and
// reports whether it carries a paths: key. ok is false for an opener without a
// closer.
func splitFront(s string) (body string, hasPaths, ok bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return s, false, true
	}
	rest := s[4:]
	end := -1
	pos := 0
	for pos <= len(rest) {
		nl := strings.IndexByte(rest[pos:], '\n')
		line := rest[pos:]
		if nl >= 0 {
			line = rest[pos : pos+nl]
		}
		if strings.TrimRight(line, " \t") == "---" {
			end = pos
			break
		}
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	if end < 0 {
		return "", false, false
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if strings.HasPrefix(line, "paths:") || strings.HasPrefix(line, "paths :") {
			hasPaths = true
		}
	}
	after := rest[end:]
	if i := strings.IndexByte(after, '\n'); i >= 0 {
		after = after[i+1:]
	} else {
		after = ""
	}
	return after, hasPaths, true
}

// clean makes text safe for a command line: valid UTF-8, no carriage returns
// and no control characters but newline and tab, so the TOML encoding codex
// needs stays within twice the size.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "?")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
}

// SkillText returns the SKILL.md of slug (project override first, then the
// framework) as the tail to append to a user turn. It refuses a slug that is
// not a plain name, a file that is not a regular file, one over MaxSkillBytes
// and one holding a secret pattern.
func SkillText(yakosRoot, project, slug string) (string, error) {
	if !slugRe.MatchString(slug) {
		return "", ErrNotFound
	}
	var dirs []string
	if project != "" {
		p := filepath.Join(project, ".claude", "skills")
		if !isLink(filepath.Join(project, ".claude")) && !isLink(p) {
			dirs = append(dirs, p)
		}
	}
	if yakosRoot != "" {
		dirs = append(dirs, filepath.Join(yakosRoot, "lib", "skills"))
	}
	for _, d := range dirs {
		root, err := os.OpenRoot(d)
		if err != nil {
			continue
		}
		data, err := readRooted(root, slug+"/SKILL.md", MaxSkillBytes)
		_ = root.Close()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("knowledge: skill %s: %w", slug, err)
		}
		raw := string(data)
		if secretscan.Redact(raw) != raw {
			return "", ErrSecret
		}
		body, _, ok := splitFront(raw)
		if !ok {
			body = raw
		}
		return "\n\n---\nSkill /" + slug + " (SKILL.md):\n" + strings.TrimSpace(clean(body)) + "\n", nil
	}
	return "", ErrNotFound
}

// ReadSoul returns the global soul text under home, bounded; "" when absent.
func ReadSoul(home string) string {
	if home == "" {
		return ""
	}
	root, err := os.OpenRoot(filepath.Join(home, ".yakos-state", "soul"))
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	data, err := readRooted(root, "global.md", MaxFileBytes)
	if err != nil {
		return ""
	}
	return clean(string(data))
}
