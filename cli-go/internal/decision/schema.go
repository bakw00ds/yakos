package decision

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// MaxSchemaBytes bounds a question-set file.
const MaxSchemaBytes = 64 * 1024

// Question-shape limits from https://docs.typesafe.ai/api: a Choice has at
// most 255 options, a Score 2-10 levels.
const (
	maxChoiceOptions = 255
	minScoreLevels   = 2
	maxScoreLevels   = 10
)

var (
	surfaceRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	schemaIDRE = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,63})@([1-9][0-9]*)$`)
	// A pinned model is name-MAJOR.MINOR.PATCH; aliases (jev-latest,
	// jev-preview) move on release "potentially changing answers without code
	// changes" (https://docs.typesafe.ai/models), so they are rejected.
	pinnedModelRE = regexp.MustCompile(`^[a-z][a-z0-9]*-[0-9]+\.[0-9]+\.[0-9]+$`)
	fieldRE       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// ThresholdConfig gates an answer. Both fields are optional.
type ThresholdConfig struct {
	MinConfidence  float64 `yaml:"min_confidence"`
	MinProbability float64 `yaml:"min_probability"`
}

// QuestionSet is one reviewed lib/decisions/<surface>.yaml. It is source, not
// configuration: the file's sha256 rides with every decision so a threshold is
// never silently applied to a different question or model version.
type QuestionSet struct {
	SchemaID      string                     `yaml:"schema_id"`
	Version       int                        `yaml:"version"`
	Surface       string                     `yaml:"surface"`
	Model         string                     `yaml:"model"`
	MayBlock      bool                       `yaml:"may_block"`
	MaxStateBytes int                        `yaml:"max_state_bytes"`
	StateFields   []string                   `yaml:"state_fields"`
	Questions     map[string]Question        `yaml:"questions"`
	Thresholds    map[string]ThresholdConfig `yaml:"thresholds"`

	Hash string `yaml:"-"` // sha256 hex of the file bytes
	Path string `yaml:"-"`
}

// ValidSurface reports whether s is a legal surface name. Surface names become
// file names, so this is also the path-traversal guard.
func ValidSurface(s string) bool { return surfaceRE.MatchString(s) }

// IsPinnedModel reports whether model is a pinned version id, not an alias.
func IsPinnedModel(model string) bool { return pinnedModelRE.MatchString(model) }

// HashBytes returns the sha256 hex of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ParseSet decodes and validates a question-set file body. name is the file's
// base name without extension and must equal the declared surface. Unknown
// fields are errors so a typo cannot silently drop a threshold.
func ParseSet(name string, data []byte) (*QuestionSet, []error) {
	if len(data) > MaxSchemaBytes {
		return nil, []error{fmt.Errorf("question set is %d bytes (max %d)", len(data), MaxSchemaBytes)}
	}
	var qs QuestionSet
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&qs); err != nil {
		return nil, []error{fmt.Errorf("yaml: %w", err)}
	}
	qs.Hash = HashBytes(data)
	var errs []error
	add := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }

	m := schemaIDRE.FindStringSubmatch(qs.SchemaID)
	switch {
	case m == nil:
		add("schema_id %q must look like <surface>@<version> (e.g. supervisor-prefilter@1)", qs.SchemaID)
	default:
		if m[1] != qs.Surface {
			add("schema_id %q does not match surface %q", qs.SchemaID, qs.Surface)
		}
		if fmt.Sprint(qs.Version) != m[2] {
			add("version %d does not match schema_id %q", qs.Version, qs.SchemaID)
		}
	}
	if !ValidSurface(qs.Surface) {
		add("surface %q is not a valid surface name", qs.Surface)
	} else if qs.Surface != name {
		add("surface %q does not match file name %q", qs.Surface, name)
	}
	switch {
	case qs.Model == "":
		add("model is required and must be a pinned version (e.g. jev-1.13.0)")
	case !IsPinnedModel(qs.Model):
		add("model %q is an alias or unpinned; pin an exact version such as jev-1.13.0 (aliases like jev-latest change answers without code changes)", qs.Model)
	}
	if qs.MaxStateBytes <= 0 || qs.MaxStateBytes > HardMaxStateBytes {
		add("max_state_bytes must be in 1..%d", HardMaxStateBytes)
	}
	if len(qs.StateFields) == 0 {
		add("state_fields must list the named state fields allowed to leave the machine")
	}
	seen := map[string]bool{}
	for _, f := range qs.StateFields {
		if !fieldRE.MatchString(f) {
			add("state_fields entry %q is not a plain field name", f)
		}
		if seen[f] {
			add("state_fields entry %q is duplicated", f)
		}
		seen[f] = true
	}
	if len(qs.Questions) == 0 {
		add("questions must not be empty")
	}
	ids := make([]string, 0, len(qs.Questions))
	for id := range qs.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !fieldRE.MatchString(id) {
			add("question id %q is not a plain identifier", id)
		}
		if e := validateQuestion(qs.Questions[id]); e != "" {
			add("question %q: %s", id, e)
		}
	}
	tids := make([]string, 0, len(qs.Thresholds))
	for id := range qs.Thresholds {
		tids = append(tids, id)
	}
	sort.Strings(tids)
	for _, id := range tids {
		if _, ok := qs.Questions[id]; !ok {
			add("threshold %q names no question", id)
		}
		t := qs.Thresholds[id]
		if t.MinConfidence < 0 || t.MinConfidence > 1 || t.MinProbability < 0 || t.MinProbability > 1 {
			add("threshold %q values must be in [0,1]", id)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	// Normalise criteria to concrete types so the wire body is deterministic.
	for id, q := range qs.Questions {
		q.Criteria = normalizeCriteria(q.Type, q.Criteria)
		qs.Questions[id] = q
	}
	return &qs, nil
}

func validateQuestion(q Question) string {
	if strings.TrimSpace(q.Instructions) == "" {
		return "instructions are required"
	}
	switch q.Type {
	case "noul":
		if q.Criteria == nil {
			return ""
		}
		m, ok := asStringMap(q.Criteria)
		if !ok {
			return "noul criteria must be a map with keys true and/or false"
		}
		for k := range m {
			if k != "true" && k != "false" {
				return fmt.Sprintf("noul criteria key %q must be true or false", k)
			}
		}
	case "choice":
		m, ok := asStringMap(q.Criteria)
		if !ok || len(m) < 2 {
			return "choice criteria must be a map of at least 2 options to descriptions"
		}
		if len(m) > maxChoiceOptions {
			return fmt.Sprintf("choice has %d options (max %d)", len(m), maxChoiceOptions)
		}
	case "score":
		l, ok := asStringList(q.Criteria)
		if !ok || len(l) < minScoreLevels || len(l) > maxScoreLevels {
			return fmt.Sprintf("score criteria must be a list of %d-%d levels", minScoreLevels, maxScoreLevels)
		}
	default:
		return fmt.Sprintf("type %q must be noul, choice, or score", q.Type)
	}
	return ""
}

func asStringMap(v any) (map[string]string, bool) {
	switch t := v.(type) {
	case map[string]string:
		return t, true
	case map[string]any:
		out := make(map[string]string, len(t))
		for k, x := range t {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out[k] = s
		}
		return out, true
	}
	return nil, false
}

func asStringList(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

func normalizeCriteria(typ string, v any) any {
	if v == nil {
		return nil
	}
	if typ == "score" {
		l, _ := asStringList(v)
		return l
	}
	m, _ := asStringMap(v)
	return m
}

// LoadSet loads and validates lib/decisions/<surface>.yaml from dir.
func LoadSet(dir, surface string) (*QuestionSet, error) {
	if !ValidSurface(surface) {
		return nil, fmt.Errorf("invalid surface name %q", surface)
	}
	path := filepath.Join(dir, surface+".yaml")
	data, err := readCapped(path)
	if err != nil {
		return nil, err
	}
	qs, errs := ParseSet(surface, data)
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s: %v", path, errs[0])
	}
	qs.Path = path
	return qs, nil
}

func readCapped(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", path)
	}
	if fi.Size() > MaxSchemaBytes {
		return nil, fmt.Errorf("%s: %d bytes exceeds %d", path, fi.Size(), MaxSchemaBytes)
	}
	return os.ReadFile(path) //nolint:gosec // path is constructed from a validated surface name
}

// SetFile is one file's validation outcome from ValidateDir.
type SetFile struct {
	Path string
	Set  *QuestionSet
	Errs []error
}

// ValidateDir validates every *.yaml under dir (sorted, deterministic). A
// missing dir yields no files. promotions is the path of the promotions log
// consulted for may_block: true (empty disables the promotion lookup, which
// then always fails a may_block: true set).
func ValidateDir(dir, promotions string) []SetFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SetFile
	for _, e := range entries { // os.ReadDir is filename-sorted
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		path := filepath.Join(dir, e.Name())
		sf := SetFile{Path: path}
		data, rerr := readCapped(path)
		if rerr != nil {
			sf.Errs = []error{rerr}
			out = append(out, sf)
			continue
		}
		qs, errs := ParseSet(name, data)
		if len(errs) == 0 && qs.MayBlock && !HasPromotion(promotions, qs.Surface, qs.Hash) {
			errs = append(errs, fmt.Errorf("may_block: true requires a recorded promotion for this exact question-set hash (yakos decide promote); none found"))
		}
		sf.Set, sf.Errs = qs, errs
		if qs != nil {
			qs.Path = path
		}
		out = append(out, sf)
	}
	return out
}

// HasPromotion reports whether the promotions log records a promotion of
// surface at exactly this question-set hash. A missing or unreadable log means
// no promotion (fail closed).
func HasPromotion(path, surface, hash string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path) //nolint:gosec // state-dir file
	if err != nil {
		return false
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var p struct {
			Surface    string `json:"surface"`
			SchemaHash string `json:"schema_hash"`
		}
		if json.Unmarshal(line, &p) == nil && p.Surface == surface && p.SchemaHash == hash && hash != "" {
			return true
		}
	}
	return false
}

// CheckAgentFrontmatter enforces the routing guardrail (ADR-0009): Jev is a
// decision provider, not a runtime, so no agent may name it as one, and no
// agent that can write (Edit/Write/Bash/MultiEdit/NotebookEdit) may reference
// a decision provider at all. fm holds the raw frontmatter values; name is
// used only in messages. Returned strings are complete error messages.
func CheckAgentFrontmatter(name string, fm map[string]any) []string {
	var out []string
	refs := false
	for _, key := range []string{"runtime", "runtime-fallback", "decision-provider", "decision_provider", "decisions"} {
		v, ok := fm[key]
		if !ok {
			continue
		}
		flat := strings.ToLower(flattenValue(v))
		if key == "decision-provider" || key == "decision_provider" || key == "decisions" {
			if strings.TrimSpace(flat) != "" && flat != "none" && flat != "false" {
				refs = true
			}
			continue
		}
		for _, tok := range splitTokens(flat) {
			if tok == ProviderJev {
				out = append(out, fmt.Sprintf("agent %s: %s names jev; jev is a decision provider, not a runtime; see ADR-0009", name, key))
				refs = true
			}
		}
	}
	if refs && hasWriteTool(fm["tools"]) {
		out = append(out, fmt.Sprintf("agent %s: has Edit/Write/Bash tools and references a decision provider; decision providers are never attached to agents that can change files or run commands (ADR-0009)", name))
	}
	return dedupe(out)
}

func flattenValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []string:
		return strings.Join(t, ",")
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			parts = append(parts, flattenValue(x))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(t)
	}
}

func splitTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '[' || r == ']' || r == '"' || r == '\'' || r == '\t'
	})
}

func hasWriteTool(v any) bool {
	for _, tok := range splitTokens(flattenValue(v)) {
		switch strings.ToLower(tok) {
		case "edit", "write", "bash", "multiedit", "notebookedit":
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
