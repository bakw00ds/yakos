// Package standards is the Go port of cli/lib/standards.sh (rank 30).
//
// It manages cross-project standards opt-ins (Plan 4 M4). Operators
// enable or disable standards in <project>/.yakos.yml under the
// `profile.standards.*` keys.
//
// # Synopsis
//
//	yakos standards list                # show all 6 standards + state
//	yakos standards enable  <name>      # set profile.standards.<name> = true
//	yakos standards disable <name>      # set profile.standards.<name> = false
//	yakos standards check               # preview what active standards catch
//	yakos standards init                # interactive profile + standards selection
//
// # Standards inventory
//
// Six standards are defined:
//
//	logging            changelog-ui    monitors
//	feedback           architecture-viz    about-page
//
// # .yakos.yml shape
//
//	profile:
//	  type: web-app
//	  standards:
//	    logging: true
//	    changelog-ui: false
//
// # Atomic writes
//
// Standards enable/disable rewrites the .yakos.yml atomically using
// temp-rename (Q8 pattern).  The rewrite uses a line-oriented awk-style
// approach: we parse the YAML with gopkg.in/yaml.v3, update the targeted
// boolean field in the in-memory map, and marshal back.  This is safer
// than the bash awk string-substitution and avoids mangling unrelated
// YAML structure (within the limits of the marshaller's ordering).
//
// # Interactive mode
//
// `init` is interactive; it reads a project-type choice and enables
// suggested standards.  For tests the PromptFn field in Config is
// injectable.
package standards

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// knownStandards is the full inventory, in display order.
var knownStandards = []string{
	"logging",
	"changelog-ui",
	"monitors",
	"feedback",
	"architecture-viz",
	"about-page",
}

// validTypes are the project types accepted by `standards init`.
var validTypes = []string{"web-app", "service", "library", "cli-tool", "data-pipeline"}

// defaultsForType returns the standards that are suggested-on for a given
// project type. Mirrors bash _standards_for_type().
var defaultsForType = map[string][]string{
	"web-app":       {"logging", "changelog-ui", "monitors", "feedback", "architecture-viz", "about-page"},
	"service":       {"logging", "monitors", "architecture-viz"},
	"library":       {"architecture-viz", "about-page"},
	"cli-tool":      {"logging", "about-page"},
	"data-pipeline": {"logging", "monitors"},
}

// playbooks describes what each standard checks.
// Mirrors bash cmd_check output.
var playbooks = map[string][3]string{
	"logging": {
		"lib/playbooks/02-code-quality.md §Logging discipline",
		"bare print/console.log/println! in production; missing structured fields; secrets in logs",
		"yakos invokes lib/skills/logging-scaffold/SKILL.md",
	},
	"changelog-ui": {
		"lib/playbooks/04-docs-architecture.md §Changelog UI presence",
		"missing UI component; version mismatch with VERSION file",
		"lib/skills/changelog-ui-scaffold/SKILL.md",
	},
	"monitors": {
		"lib/playbooks/08-infra-deploy-deps.md §Monitor presence",
		"missing supervisor/healthz/runbook; no-op healthchecks; missing restart policy",
		"lib/skills/monitor-scaffold/SKILL.md",
	},
	"feedback": {
		"lib/playbooks/02-code-quality.md §Feedback wiring",
		"missing feedback table/API/widget; cite-without-data + resolved-without-citation orphans",
		"lib/skills/feedback-scaffold/SKILL.md",
	},
	"architecture-viz": {
		"lib/playbooks/04-docs-architecture.md §Architecture viz presence",
		"missing docs/architecture/*.mmd; ADRs missing; stale diagrams; PNG-only diagrams",
		"lib/skills/architecture-viz-scaffold/SKILL.md",
	},
	"about-page": {
		"lib/playbooks/04-docs-architecture.md §About page presence",
		"missing about route; placeholder text remaining; 90-day staleness",
		"lib/skills/about-page-scaffold/SKILL.md",
	},
}

// Config carries everything Run needs.
type Config struct {
	// Subcommand is one of: list, enable, disable, check, init, help.
	Subcommand string

	// StandardName is the standard argument for enable/disable.
	StandardName string

	// ProjectDir overrides automatic project directory resolution.
	// When empty, resolution uses YAKOS_PROJECT_DIR env, cwd .yakos.yml,
	// or agent-control walk.
	ProjectDir string

	// HomeDir overrides os.Getenv("HOME") for path resolution.
	// When empty, os.Getenv("HOME") is used.
	HomeDir string

	// PromptFn is called by `init` to read a line of user input.
	// Signature: func(prompt string) (string, error).
	// When nil, os.Stdin is used.
	PromptFn func(prompt string) (string, error)

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives advisory/warning messages (default: os.Stderr).
	ErrWriter io.Writer
}

// Result summarises what Run did.
type Result struct {
	// Subcommand that was executed.
	Subcommand string

	// ProjectDir is the resolved project directory.
	ProjectDir string

	// ProfileType is the project's profile.type field (list subcommand).
	ProfileType string

	// States maps standard name → enabled/disabled/unknown (list subcommand).
	States map[string]string

	// Suggested maps standard name → bool (suggested for the profile type).
	Suggested map[string]bool

	// ActiveStandards lists enabled standards (check subcommand).
	ActiveStandards []string

	// TypeSet is true when init updated the profile type.
	TypeSet bool

	// StandardToggled is the name of the standard that was enabled/disabled.
	StandardToggled string
}

// Run executes the standards subcommand described by cfg.
func Run(cfg Config) (*Result, error) {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = os.Stderr
	}

	res := &Result{Subcommand: cfg.Subcommand}

	switch cfg.Subcommand {
	case "list":
		return runList(cfg, res, w, ew)
	case "enable":
		return runToggle(cfg, res, w, ew, true)
	case "disable":
		return runToggle(cfg, res, w, ew, false)
	case "check":
		return runCheck(cfg, res, w, ew)
	case "init":
		return runInit(cfg, res, w, ew)
	case "help", "--help", "-h", "":
		PrintHelp(w)
		return res, nil
	default:
		return nil, fmt.Errorf("yakos standards: unknown subcommand %q (try 'yakos standards help')", cfg.Subcommand)
	}
}

// PrintHelp writes the help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos standards — manage cross-project standards opt-ins (Plan 4)

  yakos standards list           # show all 6 standards + state
  yakos standards enable  <name>   # opt in
  yakos standards disable <name>   # opt out
  yakos standards check            # preview what active standards catch
  yakos standards init             # interactive profile + standards selection

Standards: logging | changelog-ui | monitors | feedback | architecture-viz | about-page

State lives in <project>/.yakos.yml under profile.standards.*. Edit
the file directly or use this CLI. See docs/cross-project-standards.md
for the full standards model.
`)
}

// KnownStandards returns the immutable list of standard names.
// Exposed for use in parity tests.
func KnownStandards() []string {
	out := make([]string, len(knownStandards))
	copy(out, knownStandards)
	return out
}

// ---- subcommand implementations ---------------------------------------------

func runList(cfg Config, res *Result, w, ew io.Writer) (*Result, error) {
	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	ymlPath := filepath.Join(projectDir, ".yakos.yml")
	if _, err := os.Stat(ymlPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found (run 'yakos init <name> --project %s' first)", ymlPath, projectDir)
	}

	doc, err := readYML(ymlPath)
	if err != nil {
		return nil, err
	}

	profileType := ""
	if p, ok := doc["profile"].(map[string]interface{}); ok {
		if t, ok := p["type"].(string); ok {
			profileType = t
		}
	}
	if profileType == "" {
		profileType = "(unset)"
	}
	res.ProfileType = profileType

	suggested := suggestedSet(profileType)
	res.Suggested = make(map[string]bool, len(knownStandards))
	res.States = make(map[string]string, len(knownStandards))

	_, _ = fmt.Fprintf(w, "\nProject: %s\nProfile type: %s\n\n", projectDir, profileType)
	_, _ = fmt.Fprintf(w, "  %-18s  %-9s  %s\n", "STANDARD", "STATE", "SUGGESTED FOR TYPE?")
	_, _ = fmt.Fprintf(w, "  %-18s  %-9s  %s\n", "--------", "-----", "-------------------")

	standards := standardsMap(doc)
	for _, name := range knownStandards {
		state := "disabled"
		if v, ok := standards[name]; ok {
			if v {
				state = "enabled"
			}
		}
		res.States[name] = state

		sugg := suggested[name]
		res.Suggested[name] = sugg
		suggStr := "no"
		if sugg {
			suggStr = "yes"
		}
		_, _ = fmt.Fprintf(w, "  %-18s  %-9s  %s\n", name, state, suggStr)
	}
	_, _ = fmt.Fprintf(w, "\nEdit .yakos.yml directly or use:\n")
	_, _ = fmt.Fprintf(w, "  yakos standards enable <name>\n")
	_, _ = fmt.Fprintf(w, "  yakos standards disable <name>\n\n")
	_ = ew
	return res, nil
}

func runToggle(cfg Config, res *Result, w, ew io.Writer, enable bool) (*Result, error) {
	name := cfg.StandardName
	if err := validateStandardName(name); err != nil {
		return nil, err
	}

	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	ymlPath := filepath.Join(projectDir, ".yakos.yml")
	if _, err := os.Stat(ymlPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found", ymlPath)
	}

	doc, err := readYML(ymlPath)
	if err != nil {
		return nil, err
	}

	setStandard(doc, name, enable)

	if err := writeYML(ymlPath, doc); err != nil {
		return nil, fmt.Errorf("write %s: %w", ymlPath, err)
	}

	verb := "disable"
	if enable {
		verb = "enable"
	}
	_, _ = fmt.Fprintf(ew, "standards: set %s = %v in %s\n", name, enable, ymlPath)
	res.StandardToggled = name
	_ = w
	_ = verb
	return res, nil
}

func runCheck(cfg Config, res *Result, w, ew io.Writer) (*Result, error) {
	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	ymlPath := filepath.Join(projectDir, ".yakos.yml")
	if _, err := os.Stat(ymlPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found", ymlPath)
	}

	doc, err := readYML(ymlPath)
	if err != nil {
		return nil, err
	}

	_, _ = fmt.Fprintf(w, "\nStandards audit preview for: %s\n\n", projectDir)

	standards := standardsMap(doc)
	anyActive := false
	for _, name := range knownStandards {
		if !standards[name] {
			continue
		}
		anyActive = true
		res.ActiveStandards = append(res.ActiveStandards, name)

		pb := playbooks[name]
		_, _ = fmt.Fprintf(w, "== %s ==\n", name)
		_, _ = fmt.Fprintf(w, "  playbook: %s\n", pb[0])
		_, _ = fmt.Fprintf(w, "  detects: %s\n", pb[1])
		_, _ = fmt.Fprintf(w, "  scaffold: %s\n", pb[2])
		_, _ = fmt.Fprintln(w)
	}

	if !anyActive {
		_, _ = fmt.Fprintln(w, "  (no standards enabled in this project)")
		_, _ = fmt.Fprintln(w, "\n  Enable with: yakos standards enable <name>")
		_, _ = fmt.Fprintln(w)
	} else {
		_, _ = fmt.Fprintln(w, "To run the full audit (all domains + standards), invoke:")
		_, _ = fmt.Fprintln(w, "  Skill: release-audit (skill:release-audit; see lib/skills/release-audit/SKILL.md)")
		_, _ = fmt.Fprintln(w)
	}
	_ = ew
	return res, nil
}

func runInit(cfg Config, res *Result, w, ew io.Writer) (*Result, error) {
	projectDir, err := resolveProjectDir(cfg)
	if err != nil {
		return nil, err
	}
	res.ProjectDir = projectDir

	ymlPath := filepath.Join(projectDir, ".yakos.yml")
	if _, err := os.Stat(ymlPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%s not found (run 'yakos init' first)", ymlPath)
	}

	doc, err := readYML(ymlPath)
	if err != nil {
		return nil, err
	}

	// Read current type.
	currentType := ""
	if p, ok := doc["profile"].(map[string]interface{}); ok {
		if t, ok := p["type"].(string); ok {
			currentType = t
		}
	}

	_, _ = fmt.Fprintf(w, "\nInteractive profile selection for: %s\n", projectDir)
	_, _ = fmt.Fprintf(w, "Project types: web-app | service | library | cli-tool | data-pipeline\n\n")
	_, _ = fmt.Fprintf(w, "Current profile.type: %s\n", currentType)

	picked, err := prompt(cfg, "Pick a project type: ")
	if err != nil {
		return nil, fmt.Errorf("read type: %w", err)
	}
	picked = strings.TrimSpace(picked)
	if !isValidType(picked) {
		return nil, fmt.Errorf("unknown type %q (valid: %s)", picked, strings.Join(validTypes, "|"))
	}

	// Set profile.type.
	setProfileType(doc, picked)
	if err := writeYML(ymlPath, doc); err != nil {
		return nil, fmt.Errorf("write %s: %w", ymlPath, err)
	}
	_, _ = fmt.Fprintf(ew, "standards: set profile.type = %s\n", picked)
	res.TypeSet = true

	// Offer to enable suggested standards.
	suggested := defaultsForType[picked]
	if len(suggested) > 0 {
		_, _ = fmt.Fprintf(w, "\nEnable suggested standards for type %s? [Y/n] ", picked)
		reply, err := prompt(cfg, "")
		if err != nil {
			return nil, fmt.Errorf("read reply: %w", err)
		}
		reply = strings.TrimSpace(strings.ToLower(reply))
		if reply == "n" || reply == "no" {
			_, _ = fmt.Fprintln(ew, "standards: skipped suggested-standards enable")
		} else {
			// Read doc again after type write.
			doc2, err := readYML(ymlPath)
			if err != nil {
				return nil, err
			}
			for _, s := range suggested {
				setStandard(doc2, s, true)
			}
			if err := writeYML(ymlPath, doc2); err != nil {
				return nil, fmt.Errorf("write %s: %w", ymlPath, err)
			}
			for _, s := range suggested {
				_, _ = fmt.Fprintf(ew, "standards: set %s = true in %s\n", s, ymlPath)
			}
		}
	}

	_, _ = fmt.Fprintln(w, "\nDone. Run \"yakos standards list\" to verify.")
	return res, nil
}

// ---- YAML helpers -----------------------------------------------------------

// readYML reads and unmarshals a .yakos.yml file into a map.
func readYML(path string) (map[string]interface{}, error) {
	raw, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]interface{}{}
	}
	return doc, nil
}

// writeYML marshals doc and writes it atomically (temp-rename, Q8).
func writeYML(path string, doc map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { //nolint:gosec
		return fmt.Errorf("mkdir: %w", err)
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// standardsMap extracts the profile.standards map from a doc.
// Returns empty map when absent.
func standardsMap(doc map[string]interface{}) map[string]bool {
	result := map[string]bool{}
	p, ok := doc["profile"].(map[string]interface{})
	if !ok {
		return result
	}
	s, ok := p["standards"].(map[string]interface{})
	if !ok {
		return result
	}
	for k, v := range s {
		if b, ok := v.(bool); ok {
			result[k] = b
		}
	}
	return result
}

// setStandard sets profile.standards.<name> = value in doc, creating
// the profile/standards keys as needed.
func setStandard(doc map[string]interface{}, name string, value bool) {
	profile, ok := doc["profile"].(map[string]interface{})
	if !ok {
		profile = map[string]interface{}{}
		doc["profile"] = profile
	}
	standards, ok := profile["standards"].(map[string]interface{})
	if !ok {
		standards = map[string]interface{}{}
		profile["standards"] = standards
	}
	standards[name] = value
}

// setProfileType sets profile.type = t in doc.
func setProfileType(doc map[string]interface{}, t string) {
	profile, ok := doc["profile"].(map[string]interface{})
	if !ok {
		profile = map[string]interface{}{}
		doc["profile"] = profile
	}
	profile["type"] = t
}

// suggestedSet returns a bool map of standards suggested for a profile type.
func suggestedSet(profileType string) map[string]bool {
	out := make(map[string]bool, len(knownStandards))
	for _, s := range defaultsForType[profileType] {
		out[s] = true
	}
	return out
}

// validateStandardName returns an error if name is not a known standard.
func validateStandardName(name string) error {
	if name == "" {
		return fmt.Errorf("standard name is required")
	}
	for _, s := range knownStandards {
		if s == name {
			return nil
		}
	}
	return fmt.Errorf("unknown standard %q (valid: %s)", name, strings.Join(knownStandards, " "))
}

// isValidType returns true if t is a known project type.
func isValidType(t string) bool {
	for _, v := range validTypes {
		if v == t {
			return true
		}
	}
	return false
}

// ---- project dir resolution -------------------------------------------------

// resolveProjectDir resolves the project directory using:
//  1. cfg.ProjectDir (explicit override)
//  2. YAKOS_PROJECT_DIR env
//  3. cwd contains .yakos.yml
//  4. agent-control walk (~/agent-control/*/.project-path)
func resolveProjectDir(cfg Config) (string, error) {
	if cfg.ProjectDir != "" {
		return cfg.ProjectDir, nil
	}
	if v := os.Getenv("YAKOS_PROJECT_DIR"); v != "" {
		return v, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if _, err := os.Stat(filepath.Join(cwd, ".yakos.yml")); err == nil {
		return cwd, nil
	}

	// Agent-control walk.
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	acRoot := filepath.Join(home, "agent-control")
	entries, err := os.ReadDir(acRoot)
	if err != nil {
		return "", fmt.Errorf("could not resolve project dir (run from project root or set YAKOS_PROJECT_DIR)")
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
		if cwd == p || (len(cwd) > len(p)+1 && cwd[:len(p)] == p && cwd[len(p)] == '/') {
			return p, nil
		}
	}
	return "", fmt.Errorf("could not resolve project dir (run from project root or set YAKOS_PROJECT_DIR)")
}

// ---- prompt helper ----------------------------------------------------------

// prompt calls cfg.PromptFn when set, otherwise reads from os.Stdin.
// The prompt string is written to the Writer before reading.
func prompt(cfg Config, p string) (string, error) {
	if cfg.PromptFn != nil {
		return cfg.PromptFn(p)
	}
	if p != "" {
		if cfg.Writer != nil {
			_, _ = fmt.Fprint(cfg.Writer, p)
		} else {
			_, _ = fmt.Fprint(os.Stdout, p)
		}
	}
	var line string
	_, err := fmt.Fscanln(os.Stdin, &line)
	if err != nil {
		return "", err
	}
	return line, nil
}
