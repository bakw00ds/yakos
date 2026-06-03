// Package skill is the Go port of cli/lib/skill.sh (rank 26).
//
// It manages skill candidates produced by the librarian — listing them,
// promoting a candidate to a real SKILL.md, rejecting it (with graveyard
// tracking), deferring it for N more cycles, and reporting lifetime stats.
//
// # Synopsis
//
//	yakos skill candidates [--review]
//	yakos skill promote <slug> [--global]
//	yakos skill reject  <slug> [--reason "<text>"]
//	yakos skill defer   <slug> <N>
//	yakos skill stats
//
// # State files
//
//	<work>/current/skill-candidates.md    — librarian-written candidate list
//	<work>/current/skill-deferrals.ndjson — deferred candidate annotations
//	~/.yakos-state/promotion-log.ndjson   — lifetime action log
//	~/.yakos-state/skill-graveyard.ndjson — rejected candidates with fingerprints
//
// # On promote
//
//	<project>/.claude/skills/<slug>/SKILL.md  (local, default)
//	<yakos-root>/lib/skills/<slug>/SKILL.md   (--global; rare)
//
// # Atomic writes
//
// Every file modification goes through atomicWriteFile (temp-rename, Q8/Decision A).
// The graveyard and promotion log are append-only NDJSON; appends use
// atomicAppend (read-lock-free append via a temporary file renamed over a
// O_APPEND fd is not idiomatic in Go; we use a simple os.OpenFile with
// O_APPEND|O_CREATE|O_WRONLY which is safe for single-process use).
//
// # Evidence fingerprinting (Plan 3 §16.1)
//
// On promote and reject, the candidate's source-evidence cycle numbers are
// hashed into a 12-hex-char fingerprint. This fingerprint is stored alongside
// the slug in the graveyard so that a renamed-but-identical candidate
// (e.g. "cleanup-files" → "clean-up-files") can be identified as a repeat
// and the operator warned before promotion.
package skill

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- public types -----------------------------------------------------------

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: candidates, promote, reject, defer, stats, help.
	Subcommand string

	// Slug is the candidate identifier for promote/reject/defer.
	Slug string

	// Global when true promotes to lib/skills/<slug>/ instead of the project's
	// .claude/skills/<slug>/.
	Global bool

	// Reason is the human-readable rejection reason for reject.
	Reason string

	// DeferCycles is the number of cycles to defer (for defer subcommand).
	DeferCycles int

	// Review when true enables the interactive review loop in candidates
	// (M2 follow-up; currently prints an advisory).
	Review bool

	// HomeDir overrides os.Getenv("HOME") for path resolution.
	HomeDir string

	// ProjectDir overrides the work/current path resolution.
	// When non-empty, this is used directly as the current dir.
	ProjectDir string

	// ProjectPath is the project root (for locating .claude/skills/).
	// When empty, it is read from <work>/current/../../.project-path.
	ProjectPath string

	// YakosRoot is the framework repo root (for --global promote).
	YakosRoot string

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer

	// PromptFn is called when the operator must confirm a repeat-rejection
	// promotion. Returns true to proceed. Defaults to a stdin readline.
	PromptFn func(prompt string) (bool, error)

	// ValidateFn is called after a skill file is written to verify the project
	// still validates. Returns an error to abort and revert. When nil, no
	// validation is run (matches the bash "WARN and continue if validate
	// binary absent" behaviour).
	ValidateFn func(projectPath string) error
}

// Result summarises what Run did.
type Result struct {
	// Subcommand is the subcommand that was executed.
	Subcommand string

	// Candidates is the list of slugs found (candidates subcommand).
	Candidates []CandidateSummary

	// PromotedSlug is set on a successful promote.
	PromotedSlug string

	// SkillDir is the directory written to on promote.
	SkillDir string

	// RejectedSlug is set on a successful reject.
	RejectedSlug string

	// DeferredSlug is set on a successful defer.
	DeferredSlug string

	// Stats is set by the stats subcommand.
	Stats *Stats
}

// CandidateSummary is a parsed row from skill-candidates.md.
type CandidateSummary struct {
	Slug          string
	Confidence    string
	EvidenceCount int
}

// Stats holds the lifetime promotion/rejection ratios from the promotion log.
type Stats struct {
	Proposed      int
	Promoted      int
	Rejected      int
	Deferred      int
	PromotionRate float64
}

// PromotionEntry is a single NDJSON record written to the promotion log.
type PromotionEntry struct {
	TS     string `json:"ts"`
	Action string `json:"action"`
	Slug   string `json:"slug"`
	// Optional extra fields carried as a raw JSON object.
	Extra map[string]interface{} `json:"-"`
}

// GraveyardEntry is a single NDJSON record in the skill graveyard.
type GraveyardEntry struct {
	TS                  string `json:"ts"`
	Slug                string `json:"slug"`
	Reason              string `json:"reason"`
	ByUser              string `json:"by_user"`
	EvidenceFingerprint string `json:"evidence_fingerprint"`
}

// ---- entry point ------------------------------------------------------------

// Run executes the skill command according to cfg.
func Run(cfg Config) (*Result, error) {
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	if cfg.ErrWriter == nil {
		cfg.ErrWriter = os.Stderr
	}
	if cfg.PromptFn == nil {
		cfg.PromptFn = stdinPrompt
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}

	promotionLog := filepath.Join(home, ".yakos-state", "promotion-log.ndjson")
	graveyard := filepath.Join(home, ".yakos-state", "skill-graveyard.ndjson")

	switch cfg.Subcommand {
	case "candidates":
		return runCandidates(cfg)
	case "promote":
		return runPromote(cfg, home, promotionLog, graveyard)
	case "reject":
		return runReject(cfg, home, promotionLog, graveyard)
	case "defer":
		return runDefer(cfg, home, promotionLog)
	case "stats":
		return runStats(cfg, promotionLog)
	default:
		return nil, fmt.Errorf("skill: unknown subcommand %q (try 'yakos skill help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos skill — manage skill candidates produced by the librarian

  yakos skill candidates [--review]
  yakos skill promote <slug> [--global]
  yakos skill reject <slug> [--reason "<text>"]
  yakos skill defer <slug> <N>
  yakos skill stats

See lib/agents/librarian.md and lib/skills/skill-promote/SKILL.md.
`)
}

// ---- subcommand implementations ---------------------------------------------

func runCandidates(cfg Config) (*Result, error) {
	res := &Result{Subcommand: "candidates"}

	file, err := resolveCandidatesFile(cfg)
	if err != nil {
		return nil, err
	}
	if file == "" {
		_, _ = fmt.Fprintln(cfg.Writer, "(no pending candidates)")
		return res, nil
	}

	data, err := os.ReadFile(file) //nolint:gosec
	if os.IsNotExist(err) {
		_, _ = fmt.Fprintln(cfg.Writer, "(no pending candidates)")
		return res, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skill candidates: read %s: %w", file, err)
	}

	candidates := parseCandidateList(string(data))
	res.Candidates = candidates

	if len(candidates) == 0 {
		_, _ = fmt.Fprintln(cfg.Writer, "(no pending candidates)")
		return res, nil
	}

	for _, c := range candidates {
		_, _ = fmt.Fprintf(cfg.Writer, "  %s (conf=%s, evidence=%d)\n",
			c.Slug, c.Confidence, c.EvidenceCount)
	}

	if cfg.Review {
		// M2 follow-up: interactive review loop.
		_, _ = fmt.Fprintln(cfg.ErrWriter, "skill: interactive review not yet implemented (M2 follow-up)")
	}

	return res, nil
}

func runPromote(cfg Config, home, promotionLog, graveyard string) (*Result, error) {
	res := &Result{Subcommand: "promote"}

	if cfg.Slug == "" {
		return nil, fmt.Errorf("skill promote: <slug> required")
	}

	file, err := resolveCandidatesFile(cfg)
	if err != nil {
		return nil, err
	}
	if file == "" {
		return nil, fmt.Errorf("skill promote: no skill-candidates.md found")
	}

	data, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("skill promote: read %s: %w", file, err)
	}
	content := string(data)

	body := extractCandidate(cfg.Slug, content)
	if body == "" {
		return nil, fmt.Errorf("skill promote: candidate %q not found in %s", cfg.Slug, file)
	}

	// Check graveyard for repeat-rejection (§16.1).
	fingerprint := evidenceFingerprint(cfg.Slug, content)
	rejectedCount := graveyardCount(graveyard, cfg.Slug, fingerprint)
	if rejectedCount >= 3 {
		_, _ = fmt.Fprintf(cfg.ErrWriter,
			"WARN: %q (or equivalent-evidence variant) has been rejected %d times before.\n",
			cfg.Slug, rejectedCount)
		_, _ = fmt.Fprintf(cfg.ErrWriter, "      Evidence fingerprint: %s\n", fingerprint)

		ok, err := cfg.PromptFn("Promote anyway? [y/N] ")
		if err != nil {
			return nil, fmt.Errorf("skill promote: prompt: %w", err)
		}
		if !ok {
			_, _ = fmt.Fprintln(cfg.ErrWriter, "skill: aborted")
			return res, nil
		}
	}

	// Resolve project path.
	projectPath, err := resolveProjectPath(cfg, file)
	if err != nil {
		return nil, fmt.Errorf("skill promote: resolve project: %w", err)
	}

	// Determine skill dir.
	var skillDir string
	if cfg.Global && cfg.YakosRoot != "" {
		skillDir = filepath.Join(cfg.YakosRoot, "lib", "skills", cfg.Slug)
	} else if projectPath != "" {
		skillDir = filepath.Join(projectPath, ".claude", "skills", cfg.Slug)
	} else {
		return nil, fmt.Errorf("skill promote: cannot determine skill directory (no project path or --global)")
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("skill promote: mkdir %s: %w", skillDir, err)
	}

	// Write SKILL.md.
	skillContent := buildSkillMD(cfg.Slug, body)
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := atomicWriteFile(skillFile, []byte(skillContent), 0644); err != nil {
		return nil, fmt.Errorf("skill promote: write %s: %w", skillFile, err)
	}

	// Validate if a validator is wired.
	if cfg.ValidateFn != nil && projectPath != "" {
		if err := cfg.ValidateFn(projectPath); err != nil {
			// Revert.
			_ = os.RemoveAll(skillDir)
			_ = appendPromotionLog(promotionLog, "promote_failed", cfg.Slug, map[string]interface{}{
				"reason": "validate_failed",
			})
			return nil, fmt.Errorf("skill promote: validation failed; skill reverted: %w", err)
		}
	}

	// Append promotion log.
	isGlobal := 0
	if cfg.Global {
		isGlobal = 1
	}
	byUser := currentUser()
	if err := appendPromotionLog(promotionLog, "promoted", cfg.Slug, map[string]interface{}{
		"global":   isGlobal,
		"by_user":  byUser,
	}); err != nil {
		// Non-fatal: log failure shouldn't roll back a successful promote.
		_, _ = fmt.Fprintf(cfg.ErrWriter, "skill promote: warn: failed to write promotion log: %v\n", err)
	}

	// Strip candidate from the file.
	updated := stripCandidate(cfg.Slug, content)
	if err := atomicWriteFile(file, []byte(updated), 0644); err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "skill promote: warn: failed to update candidates file: %v\n", err)
	}

	_, _ = fmt.Fprintf(cfg.ErrWriter, "skill: promoted: %s\n", skillFile)

	res.PromotedSlug = cfg.Slug
	res.SkillDir = skillDir
	return res, nil
}

func runReject(cfg Config, home, promotionLog, graveyard string) (*Result, error) {
	res := &Result{Subcommand: "reject"}

	if cfg.Slug == "" {
		return nil, fmt.Errorf("skill reject: <slug> required")
	}

	file, err := resolveCandidatesFile(cfg)
	if err != nil {
		return nil, err
	}
	if file == "" {
		return nil, fmt.Errorf("skill reject: no skill-candidates.md found")
	}

	data, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("skill reject: read %s: %w", file, err)
	}
	content := string(data)

	body := extractCandidate(cfg.Slug, content)
	if body == "" {
		return nil, fmt.Errorf("skill reject: candidate %q not found", cfg.Slug)
	}

	// Compute fingerprint BEFORE stripping (§16.1).
	fingerprint := evidenceFingerprint(cfg.Slug, content)

	// Append to graveyard.
	if err := os.MkdirAll(filepath.Dir(graveyard), 0755); err != nil { //nolint:gosec
		return nil, fmt.Errorf("skill reject: mkdir graveyard dir: %w", err)
	}
	entry := GraveyardEntry{
		TS:                  isoNowZ(),
		Slug:                cfg.Slug,
		Reason:              cfg.Reason,
		ByUser:              currentUser(),
		EvidenceFingerprint: fingerprint,
	}
	if err := appendGraveyard(graveyard, entry); err != nil {
		return nil, fmt.Errorf("skill reject: graveyard: %w", err)
	}

	// Promotion log.
	if err := appendPromotionLog(promotionLog, "rejected", cfg.Slug, map[string]interface{}{
		"reason":               cfg.Reason,
		"evidence_fingerprint": fingerprint,
	}); err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "skill reject: warn: failed to write promotion log: %v\n", err)
	}

	// Strip candidate from file.
	updated := stripCandidate(cfg.Slug, content)
	if err := atomicWriteFile(file, []byte(updated), 0644); err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "skill reject: warn: failed to update candidates file: %v\n", err)
	}

	_, _ = fmt.Fprintf(cfg.ErrWriter, "skill: rejected: %s (fingerprint %s)\n", cfg.Slug, fingerprint)

	res.RejectedSlug = cfg.Slug
	return res, nil
}

func runDefer(cfg Config, home, promotionLog string) (*Result, error) {
	res := &Result{Subcommand: "defer"}

	if cfg.Slug == "" {
		return nil, fmt.Errorf("skill defer: <slug> required")
	}
	if cfg.DeferCycles <= 0 {
		return nil, fmt.Errorf("skill defer: <N> must be a positive integer")
	}

	// Resolve work/current for the deferrals sidecar file and cycle counter.
	curDir, err := resolveCurrentDir(cfg)
	if err != nil {
		return nil, err
	}
	if curDir == "" {
		return nil, fmt.Errorf("skill defer: no active session detected")
	}

	currentCycle := readCycleCount(curDir)

	deferralsFile := filepath.Join(curDir, "skill-deferrals.ndjson")
	entry := map[string]interface{}{
		"slug":              cfg.Slug,
		"deferred_at_cycle": currentCycle,
		"defer_until_cycle": currentCycle + cfg.DeferCycles,
	}
	entryBytes, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("skill defer: marshal entry: %w", err)
	}

	if err := appendNDJSON(deferralsFile, entryBytes); err != nil {
		return nil, fmt.Errorf("skill defer: write deferrals: %w", err)
	}

	// Promotion log.
	if err := appendPromotionLog(promotionLog, "deferred", cfg.Slug, map[string]interface{}{
		"cycles": cfg.DeferCycles,
	}); err != nil {
		_, _ = fmt.Fprintf(cfg.ErrWriter, "skill defer: warn: failed to write promotion log: %v\n", err)
	}

	_, _ = fmt.Fprintf(cfg.ErrWriter, "skill: deferred %q for %d more cycles\n", cfg.Slug, cfg.DeferCycles)

	res.DeferredSlug = cfg.Slug
	return res, nil
}

func runStats(cfg Config, promotionLog string) (*Result, error) {
	res := &Result{Subcommand: "stats"}

	if _, err := os.Stat(promotionLog); os.IsNotExist(err) {
		_, _ = fmt.Fprintln(cfg.Writer, "no promotion log yet")
		res.Stats = &Stats{}
		return res, nil
	}

	data, err := os.ReadFile(promotionLog) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("skill stats: read %s: %w", promotionLog, err)
	}

	var proposed, promoted, rejected, deferred int
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		action, _ := rec["action"].(string)
		switch action {
		case "proposed":
			proposed++
		case "promoted":
			promoted++
		case "rejected":
			rejected++
		case "deferred":
			deferred++
		}
	}

	rate := 0.0
	if proposed > 0 {
		rate = float64(promoted) / float64(proposed) * 100
	}

	user := currentUser()
	_, _ = fmt.Fprintf(cfg.Writer,
		"Skill candidate stats (lifetime, user %s):\n  Proposed:  %d\n  Promoted:  %d\n  Rejected:  %d\n  Deferred:  %d\n  Promotion rate: %.1f%%\n",
		user, proposed, promoted, rejected, deferred, rate)

	// §16.2 calibration warnings.
	if proposed >= 100 {
		rateInt := int(rate + 0.5)
		if rateInt < 5 {
			_, _ = fmt.Fprintln(cfg.Writer, "")
			_, _ = fmt.Fprintf(cfg.ErrWriter, "[warn] promotion rate <5%% over %d candidates — librarian may be over-eager\n", proposed)
			_, _ = fmt.Fprintln(cfg.ErrWriter, "       Tune: lower 'skill_candidates.min_confidence' in ~/.yakos-state/settings.json")
			_, _ = fmt.Fprintln(cfg.ErrWriter, "       or 'skill_candidates.min_evidence_count' to slow proposals")
		} else if rateInt > 40 {
			_, _ = fmt.Fprintln(cfg.Writer, "")
			_, _ = fmt.Fprintf(cfg.ErrWriter, "[warn] promotion rate >40%% over %d candidates — librarian may be under-skeptical\n", proposed)
			_, _ = fmt.Fprintln(cfg.ErrWriter, "       Tune: raise thresholds in ~/.yakos-state/settings.json")
		}
	} else if proposed > 0 && proposed < 20 {
		_, _ = fmt.Fprintln(cfg.Writer, "")
		_, _ = fmt.Fprintf(cfg.ErrWriter, "[info] sample size %d; promotion rate not yet calibration-meaningful (need >=100)\n", proposed)
	}

	res.Stats = &Stats{
		Proposed:      proposed,
		Promoted:      promoted,
		Rejected:      rejected,
		Deferred:      deferred,
		PromotionRate: rate,
	}
	return res, nil
}

// ---- candidate file parsing -------------------------------------------------

// candidateSectionRe matches a candidate section header.
// Format: "## candidate: <slug> (<iso-date>)"
var candidateSectionRe = regexp.MustCompile(`^## candidate:\s+(\S+)`)

// parseCandidateList parses the candidates.md format and returns summary rows.
// Mirrors the awk block in cmd_candidates.
func parseCandidateList(content string) []CandidateSummary {
	var results []CandidateSummary
	var current *CandidateSummary

	confRe := regexp.MustCompile(`\*\*Confidence\*\*:\s*(\S+)`)
	cycleRe := regexp.MustCompile(`^- Cycle [0-9]+`)
	evidenceHeaderRe := regexp.MustCompile(`\*\*Source evidence\*\*`)

	inEvidenceSection := false

	flush := func() {
		if current != nil {
			results = append(results, *current)
			current = nil
		}
		inEvidenceSection = false
	}

	for _, line := range strings.Split(content, "\n") {
		if m := candidateSectionRe.FindStringSubmatch(line); m != nil {
			flush()
			current = &CandidateSummary{Slug: m[1]}
			continue
		}
		if current == nil {
			continue
		}
		// A different ## heading closes the current section.
		if strings.HasPrefix(line, "## ") && !candidateSectionRe.MatchString(line) {
			flush()
			continue
		}
		if m := confRe.FindStringSubmatch(line); m != nil {
			current.Confidence = m[1]
		}
		if evidenceHeaderRe.MatchString(line) {
			inEvidenceSection = true
		}
		if inEvidenceSection && cycleRe.MatchString(line) {
			current.EvidenceCount++
		}
	}
	flush()
	return results
}

// extractCandidate returns the full body of the candidate section for slug,
// or "" if not found. Mirrors _skill_extract_candidate.
func extractCandidate(slug, content string) string {
	var lines []string
	inSection := false
	prefix := fmt.Sprintf("## candidate: %s ", slug)
	// Also accept exact slug at EOL (no trailing space) for robust matching.

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## candidate:") {
			if strings.HasPrefix(line, prefix) ||
				line == fmt.Sprintf("## candidate: %s", slug) ||
				strings.HasPrefix(line, "## candidate: "+slug+"(") {
				inSection = true
				lines = append(lines, line)
				continue
			}
			// Different candidate section — stop if we were in our section.
			if inSection {
				break
			}
			continue
		}
		if inSection {
			if strings.HasPrefix(line, "## ") {
				break
			}
			lines = append(lines, line)
		}
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// stripCandidate removes the section for slug from content and returns the
// modified content. Mirrors _skill_strip_candidate.
func stripCandidate(slug, content string) string {
	var lines []string
	skip := false
	prefix := "## candidate: " + slug

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "## candidate:") {
			if strings.HasPrefix(line, prefix+" ") ||
				line == prefix ||
				strings.HasPrefix(line, prefix+"(") {
				skip = true
				continue
			}
			skip = false
		} else if skip && strings.HasPrefix(line, "## ") {
			skip = false
		}
		if !skip {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// ---- evidence fingerprinting ------------------------------------------------

// evidenceFingerprint extracts source-evidence cycle numbers from the
// candidate body and hashes them to a 12-hex-char string (§16.1).
func evidenceFingerprint(slug, content string) string {
	body := extractCandidate(slug, content)
	if body == "" {
		return "no-evidence"
	}

	cycleRe := regexp.MustCompile(`Cycle\s+([0-9]+)`)
	matches := cycleRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return "no-evidence"
	}

	seen := make(map[string]struct{})
	var cycles []string
	for _, m := range matches {
		if _, dup := seen[m[1]]; !dup {
			seen[m[1]] = struct{}{}
			cycles = append(cycles, m[1])
		}
	}
	sort.Strings(cycles)
	input := strings.Join(cycles, ",") + ","

	sum := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", sum[:6]) // 12 hex chars
}

// ---- graveyard helpers ------------------------------------------------------

// graveyardCount returns the number of graveyard entries matching slug OR
// the evidence fingerprint (whichever is non-empty). Matches §16.1 semantics.
func graveyardCount(graveyardPath, slug, fingerprint string) int {
	data, err := os.ReadFile(graveyardPath) //nolint:gosec
	if err != nil {
		return 0
	}

	count := 0
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry GraveyardEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Slug == slug {
			count++
			continue
		}
		if fingerprint != "" && fingerprint != "no-evidence" &&
			entry.EvidenceFingerprint == fingerprint {
			count++
		}
	}
	return count
}

// appendGraveyard appends a GraveyardEntry to the graveyard NDJSON file.
func appendGraveyard(path string, entry GraveyardEntry) error {
	b, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	return appendNDJSON(path, b)
}

// ---- promotion log helpers --------------------------------------------------

// appendPromotionLog appends an action entry to the promotion log.
func appendPromotionLog(path, action, slug string, extra map[string]interface{}) error {
	rec := map[string]interface{}{
		"ts":     isoNowZ(),
		"action": action,
		"slug":   slug,
	}
	for k, v := range extra {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	return appendNDJSON(path, b)
}

// ---- path resolution --------------------------------------------------------

// resolveCandidatesFile returns the absolute path to skill-candidates.md,
// or "" when it cannot be resolved (no error) or doesn't exist (no error).
func resolveCandidatesFile(cfg Config) (string, error) {
	curDir, err := resolveCurrentDir(cfg)
	if err != nil {
		return "", err
	}
	if curDir == "" {
		return "", nil
	}
	p := filepath.Join(curDir, "skill-candidates.md")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return "", nil
	}
	return p, nil
}

// resolveCurrentDir returns the work/current directory.
func resolveCurrentDir(cfg Config) (string, error) {
	if cfg.ProjectDir != "" {
		return cfg.ProjectDir, nil
	}

	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}

	slug := activeSlug(cfg, home)
	if slug == "" {
		return "", nil
	}

	return filepath.Join(home, "agent-control", slug, "work", "current"), nil
}

// activeSlug returns the project slug for the active session.
func activeSlug(cfg Config, home string) string {
	if v := os.Getenv("YAKOS_PROJECT_NAME"); v != "" {
		return v
	}

	acRoot := filepath.Join(home, "agent-control")
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return ""
	}

	cwd, _ := os.Getwd()
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ppFile := filepath.Join(acRoot, e.Name(), ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(data))
		if p == "" {
			continue
		}
		pr, err := filepath.EvalSymlinks(p)
		if err != nil {
			pr = p
		}
		if cwd == pr || strings.HasPrefix(cwd, pr+string(filepath.Separator)) {
			return e.Name()
		}
	}
	return ""
}

// resolveProjectPath resolves the project root from cfg or the .project-path
// sidecar relative to the candidates file.
func resolveProjectPath(cfg Config, candidatesFile string) (string, error) {
	if cfg.ProjectPath != "" {
		return cfg.ProjectPath, nil
	}

	// Try reading .project-path from two levels up from work/current/.
	// Typical layout: ~/agent-control/<slug>/work/current/skill-candidates.md
	// So: current/../../../.project-path
	// That is: <acRoot>/<slug>/.project-path
	if candidatesFile != "" {
		// Go up from current dir to agent-control/<slug>/.
		curDir := filepath.Dir(candidatesFile)
		ppFile := filepath.Join(curDir, "..", "..", ".project-path")
		data, err := os.ReadFile(ppFile) //nolint:gosec
		if err == nil {
			p := strings.TrimSpace(string(data))
			if p != "" && isDir(p) {
				return p, nil
			}
		}
	}

	// No project path found; caller decides if this is fatal.
	return "", nil
}

// ---- SKILL.md builder -------------------------------------------------------

// buildSkillMD produces a minimal SKILL.md from a candidate body.
// Matches the bash promote template (TODO(M2): real template rendering).
func buildSkillMD(slug, body string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("name: " + slug + "\n")
	sb.WriteString(`description: "<filled-by-template-on-promote>"` + "\n")
	sb.WriteString("allowed-tools: Read Edit Bash Grep\n")
	sb.WriteString(`argument-hint: "<filled>"` + "\n")
	sb.WriteString("mode: [implement]\n")
	sb.WriteString("---\n\n")
	sb.WriteString("# " + slug + "\n\n")
	sb.WriteString(body + "\n")
	return sb.String()
}

// ---- utilities --------------------------------------------------------------

// readCycleCount reads .cycle-count from curDir. Returns 0 on error/absent.
func readCycleCount(curDir string) int {
	data, err := os.ReadFile(filepath.Join(curDir, ".cycle-count")) //nolint:gosec
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

// isoNowZ returns the current UTC time in RFC3339 format.
func isoNowZ() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// currentUser returns the current OS username, or "unknown" on error.
func currentUser() string {
	// os/user.Current() is heavier; try env first (matches bash $(id -un)).
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return "unknown"
}

// isDir returns true when path is an existing directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// atomicWriteFile writes data to path using a temp-rename pattern (Q8/Decision A).
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".yakos-skill-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp→%s: %w", path, err)
	}
	return nil
}

// appendNDJSON appends a JSON line to an NDJSON file (O_APPEND|O_CREATE).
// Safe for single-process use; not safe for concurrent writers.
func appendNDJSON(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// stdinPrompt is the default PromptFn: writes prompt to stderr and reads a
// line from stdin. Returns true iff the user entered "y".
func stdinPrompt(prompt string) (bool, error) {
	_, _ = fmt.Fprint(os.Stderr, prompt)
	reader := bufio.NewReader(os.Stdin)
	ans, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimSpace(ans) == "y", nil
}
