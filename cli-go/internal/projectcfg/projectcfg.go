// Package projectcfg reads the routing keys of a project's .yakos.yml:
//
//	default-runtime: codex          # runtime for agents with no pin
//	default-fallback: [claude]      # appended to every dispatch chain
//	per-domain:                     # agent.domain -> runtime
//	  code-review: codex
//
// It is the Go port of the readers in cli/lib/project-config.sh
// (yk_pcfg_get, yk_pcfg_get_list, yk_pcfg_resolve_runtime). The bash reader is
// an awk subset parser; this one decodes real YAML, so it also accepts block
// lists and trailing comments, but it returns the same values for every
// document the bash reader understands (see the differential test).
//
// The decode is deliberately tolerant. .yakos.yml is a repository file, so an
// untrusted clone must never be able to break dispatch with a bad document:
// a file that cannot be read or parsed yields an empty Config plus a warning
// (the same posture as decision.LoadConfig), and a mistyped or malformed key
// is dropped on its own without discarding its valid siblings. Every other
// top-level key in the file (decisions:, model-aliases:, yakos:, ...) is
// ignored here; other readers own them.
//
// Nothing in this package grants anything: it only names runtimes. The
// dispatch layer still resolves each name through the closed adapter registry
// and the auth probe before anything is executed.
package projectcfg

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the project config file, relative to the project root.
const FileName = ".yakos.yml"

// maxFileBytes bounds how much of .yakos.yml is read. Real files are a few
// hundred bytes; the cap keeps a hostile repository from making every dispatch
// read and parse an enormous document.
const maxFileBytes = 256 << 10

// Sources reported by Config.RuntimeFor, mirrored into the dispatch log's
// runtime_chosen_by field.
const (
	SourcePerDomain      = "per-domain"
	SourceProjectDefault = "project-default"
)

// idRe is the shape of a runtime identifier or a domain tag accepted from the
// file. It is intentionally narrower than "anything YAML allows": the values
// end up in log lines and error messages.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// domainRe is the shape of a per-domain key (an agent's free-form domain tag).
var domainRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Config is the routing subset of a project's .yakos.yml. The zero value means
// "no project preference".
type Config struct {
	// DefaultRuntime is the runtime for agents with no frontmatter pin and no
	// per-domain entry ("" when unset).
	DefaultRuntime string

	// DefaultFallback are runtimes appended after an agent's own
	// runtime-fallback list when the preferred runtime is unavailable.
	DefaultFallback []string

	// PerDomain maps an agent's domain to a runtime. Nil when unset.
	PerDomain map[string]string

	// DisableRuntimes and DisableModels come from the router: block
	//
	//	router:
	//	  disable_runtimes: [codex]
	//	  disable_models: [gpt-5.6-sol]
	//
	// A project can only switch things off: there is no enable, no add and no
	// provider list, so a cloned repository cannot widen where a task is sent.
	DisableRuntimes []string
	DisableModels   []string
	// NeverPaths come from router.never_paths (K-140): extra credential-file globs
	// that make a request sensitive. They ADD to the built-in never-paths the
	// router always applies and can never remove one; a project can only narrow
	// where a request is routed.
	NeverPaths []string
}

// RuntimeDisabled reports whether the project switches runtime name off.
func (c Config) RuntimeDisabled(name string) bool { return contains(c.DisableRuntimes, name) }

// ModelDisabled reports whether the project switches model id off.
func (c Config) ModelDisabled(id string) bool { return contains(c.DisableModels, id) }

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// neverPathRe is the shape of a router.never_paths glob: path characters and
// glob metacharacters, no control characters, no space.
var neverPathRe = regexp.MustCompile(`^[A-Za-z0-9_.*?\[\]!^@~+/-]{1,256}$`)

// maxDisables bounds each router disable list.
const maxDisables = 64

// MaxNeverPaths bounds router.never_paths: every entry is matched against every
// token of every request, so the list is kept short (K-140 fixup F3).
const MaxNeverPaths = 32

// maxGlobMeta and maxGlobStars bound the work one never_paths glob can cost a
// match: at most 6 of `*?[` (plus the stars of a `**`) and two `**` runs. A glob of 64 chained `[!b]`
// classes is a CPU sink, not a credential name.
const (
	maxGlobMeta  = 6
	maxGlobStars = 2
)

// NeverPathSimpleEnough reports whether a never_paths glob is cheap enough to
// match against every token of a request. The router applies the same bound to
// any project glob it is handed.
func NeverPathSimpleEnough(glob string) bool {
	if len(glob) > 256 {
		return false
	}
	return strings.Count(glob, "**") <= maxGlobStars && strings.Count(glob, "*")+strings.Count(glob, "?")+strings.Count(glob, "[") <= maxGlobMeta+maxGlobStars
}

// modelIDRe is the shape of a model id (runtime.ModelIDPattern).
var modelIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,63}$`)

// RuntimeFor returns the runtime this file selects for an agent in domain and
// which key chose it: per-domain[domain] first, then default-runtime. It
// returns ("", "") when the file selects nothing. The agent's own frontmatter
// pin outranks both and is the caller's concern (see yk_pcfg_resolve_runtime).
func (c Config) RuntimeFor(domain string) (name, source string) {
	if domain != "" {
		if rt := c.PerDomain[domain]; rt != "" {
			return rt, SourcePerDomain
		}
	}
	if c.DefaultRuntime != "" {
		return c.DefaultRuntime, SourceProjectDefault
	}
	return "", ""
}

// Load reads <project>/.yakos.yml. It never fails: an absent file, an empty
// project path, or a file that is not a regular file below the size cap all
// yield the zero Config, and any problem worth telling the operator about is
// returned as a warning (already prefixed with the file path).
func Load(project string) (Config, []string) {
	if project == "" {
		return Config{}, nil
	}
	path := filepath.Join(project, FileName)

	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	// A FIFO or device named .yakos.yml would block or stream forever.
	if !fi.Mode().IsRegular() {
		return Config{}, []string{path + ": not a regular file; ignored"}
	}
	if fi.Size() > maxFileBytes {
		return Config{}, []string{fmt.Sprintf("%s: larger than %d bytes; ignored", path, maxFileBytes)}
	}

	f, err := os.Open(path) //nolint:gosec // project config path, stat-checked above
	if err != nil {
		return Config{}, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Config{}, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	if len(data) > maxFileBytes {
		return Config{}, []string{fmt.Sprintf("%s: larger than %d bytes; ignored", path, maxFileBytes)}
	}

	cfg, warns := Parse(data)
	for i, w := range warns {
		warns[i] = path + ": " + w
	}
	return cfg, warns
}

// Parse decodes the routing keys from YAML bytes. On a document that is not
// valid YAML (or whose top level is not a mapping) it returns the zero Config
// and one warning. Otherwise it returns every key that decoded cleanly and a
// warning for each key that did not.
func Parse(data []byte) (Config, []string) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Config{}, []string{"cannot parse: " + oneLine(err.Error())}
	}

	var (
		cfg   Config
		warns []string
	)

	if v, ok := doc["default-runtime"]; ok && v != nil {
		if s, ok := scalarID(v); ok {
			cfg.DefaultRuntime = s
		} else {
			warns = append(warns, "default-runtime: want a runtime id like \"claude\"; ignored")
		}
	}

	if v, ok := doc["default-fallback"]; ok && v != nil {
		list, ok := v.([]any)
		if !ok {
			warns = append(warns, "default-fallback: want a list like [claude]; ignored")
		} else {
			for _, el := range list {
				s, ok := scalarID(el)
				if !ok {
					warns = append(warns, "default-fallback: skipping an entry that is not a runtime id")
					continue
				}
				cfg.DefaultFallback = append(cfg.DefaultFallback, s)
			}
		}
	}

	if v, ok := doc["per-domain"]; ok && v != nil {
		m, ok := stringMap(v)
		if !ok {
			warns = append(warns, "per-domain: want a mapping of domain to runtime; ignored")
		} else {
			// Sorted so the warning order is stable run to run.
			doms := make([]string, 0, len(m))
			for dom := range m {
				doms = append(doms, dom)
			}
			sort.Strings(doms)
			for _, dom := range doms {
				rt, ok := scalarID(m[dom])
				if !domainRe.MatchString(dom) || !ok {
					warns = append(warns, "per-domain: skipping an entry that is not domain: runtime")
					continue
				}
				if cfg.PerDomain == nil {
					cfg.PerDomain = make(map[string]string)
				}
				cfg.PerDomain[dom] = rt
			}
		}
	}

	if v, ok := doc["router"]; ok && v != nil {
		m, ok := stringMap(v)
		if !ok {
			warns = append(warns, "router: want a mapping with disable_runtimes, disable_models and never_paths lists; ignored")
		} else {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				switch k {
				case "disable_runtimes":
					var w []string
					cfg.DisableRuntimes, w = disableList(k, m[k], idRe)
					warns = append(warns, w...)
				case "disable_models":
					var w []string
					cfg.DisableModels, w = disableList(k, m[k], modelIDRe)
					warns = append(warns, w...)
				case "never_paths":
					var w []string
					cfg.NeverPaths, w = neverPathList(m[k])
					warns = append(warns, w...)
				default:
					warns = append(warns, "router: a project can only disable runtimes and models or add never_paths; ignoring that key")
				}
			}
		}
	}

	return cfg, warns
}

// neverPathList reads router.never_paths: disableList's shape check, then the
// complexity bound, then at most MaxNeverPaths entries.
func neverPathList(v any) ([]string, []string) {
	all, warns := disableList("never_paths", v, neverPathRe)
	var out []string
	for _, g := range all {
		if !NeverPathSimpleEnough(g) {
			warns = append(warns, "router.never_paths: skipping a glob with too many wildcards")
			continue
		}
		if len(out) >= MaxNeverPaths {
			warns = append(warns, fmt.Sprintf("router.never_paths: only the first %d entries are read", MaxNeverPaths))
			break
		}
		out = append(out, g)
	}
	return out, warns
}

// disableList reads one router disable list: strings that match re, deduplicated,
// at most maxDisables of them.
func disableList(key string, v any, re *regexp.Regexp) ([]string, []string) {
	list, ok := v.([]any)
	if !ok {
		return nil, []string{"router." + key + ": want a list; ignored"}
	}
	var out, warns []string
	seen := map[string]bool{}
	for _, el := range list {
		s, ok := el.(string)
		s = strings.TrimSpace(s)
		if !ok || !re.MatchString(s) {
			warns = append(warns, "router."+key+": skipping an entry that is not an id")
			continue
		}
		if len(out) >= maxDisables {
			warns = append(warns, fmt.Sprintf("router.%s: only the first %d entries are read", key, maxDisables))
			break
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out, warns
}

// scalarID reports v as a runtime identifier. Only YAML strings qualify, which
// also rejects numbers, booleans, lists and mappings.
func scalarID(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if !idRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// stringMap normalizes a decoded mapping. yaml.v3 yields map[string]any when
// every key is a string and map[any]any otherwise.
func stringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	}
	return nil, false
}

// oneLine collapses a parser error to its first line so a warning never
// spans the log.
func oneLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}
