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
}

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

	return cfg, warns
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
