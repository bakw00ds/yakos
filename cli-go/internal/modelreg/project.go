package modelreg

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// A project's .yakos.yml may narrow what runs on its behalf and never widen it:
//
//	models:
//	  disable: [opus, gpt-5.6-sol]
//
// The file is a repository file, so an untrusted clone can write anything into it.
// That is why `disable` is the only key that does anything: there is no `enable`,
// no `add`, no `aliases` and no `providers`, and the same file cannot ask for one
// (the other keys are reported and ignored). This is the tighten-only rule
// decision.Tighten applies to the decision provider, in the one shape where it is
// true by construction: a list of ids to switch off.

// maxProjectBytes bounds how much of .yakos.yml is read (projectcfg uses the same
// cap): real files are a few hundred bytes.
const maxProjectBytes = 256 << 10

// maxProjectDisables bounds the list, so a hostile file cannot make every
// registry load slow.
const maxProjectDisables = 256

// ProjectPolicy is the registry's reading of a project's .yakos.yml.
type ProjectPolicy struct {
	// Disable lists model ids the project switches off, on every harness that
	// lists them. Only ids shaped like a model id are kept.
	Disable []string
}

// LoadProject reads <project>/.yakos.yml. It never fails: an absent file, an empty
// project path, or a file that is not a regular file below the size cap yields the
// zero policy, and anything worth telling the operator is a warning.
func LoadProject(project string) (ProjectPolicy, []string) {
	if project == "" {
		return ProjectPolicy{}, nil
	}
	path := filepath.Join(project, ".yakos.yml")
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectPolicy{}, nil
		}
		return ProjectPolicy{}, []string{projectReadWarning(err)}
	}
	// A FIFO or device named .yakos.yml would block or stream forever.
	if !fi.Mode().IsRegular() {
		return ProjectPolicy{}, []string{".yakos.yml: not a regular file; the models: key is ignored"}
	}
	if fi.Size() > maxProjectBytes {
		return ProjectPolicy{}, []string{fmt.Sprintf(".yakos.yml: larger than %d bytes; the models: key is ignored", maxProjectBytes)}
	}
	f, err := os.Open(path) //nolint:gosec // project config path, stat-checked above
	if err != nil {
		return ProjectPolicy{}, []string{projectReadWarning(err)}
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxProjectBytes+1))
	if err != nil {
		return ProjectPolicy{}, []string{projectReadWarning(err)}
	}
	if len(data) > maxProjectBytes {
		return ProjectPolicy{}, []string{fmt.Sprintf(".yakos.yml: larger than %d bytes; the models: key is ignored", maxProjectBytes)}
	}
	return ParseProject(data)
}

// projectReadWarning words a failure to read .yakos.yml without the project's path
// (an OS error carries it, and the warning reaches a terminal and, later, an API).
func projectReadWarning(err error) string {
	why := "it could not be read"
	if errors.Is(err, fs.ErrPermission) {
		why = "permission denied"
	}
	return ".yakos.yml: " + why + ", so a models: disable list in it is NOT applied"
}

// ParseProject reads the models: key of a .yakos.yml. Every other top-level key
// belongs to another reader and is ignored without comment. A document that is not
// YAML yields the zero policy and one warning.
func ParseProject(data []byte) (ProjectPolicy, []string) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ProjectPolicy{}, []string{".yakos.yml: cannot parse, so a models: disable list in it is NOT applied: " + firstLine(err.Error())}
	}
	raw, ok := doc["models"]
	if !ok || raw == nil {
		return ProjectPolicy{}, nil
	}
	var pol ProjectPolicy
	wl := &warnList{prefix: ".yakos.yml models: "}
	warn := wl.add
	m, ok := asStringMap(raw)
	if !ok {
		return ProjectPolicy{}, []string{".yakos.yml models: want a mapping with a disable: list; ignored"}
	}
	for _, k := range sortedKeys(m) {
		if k != "disable" {
			warn("%s ignored: a project can only disable models (models: {disable: [id, ...]}); it cannot enable, add or alias one", quote(k))
		}
	}
	list, ok := m["disable"].([]any)
	if _, present := m["disable"]; present && !ok {
		warn("disable: want a list of model ids; ignored")
		return pol, wl.list()
	}
	seen := map[string]bool{}
	for _, el := range list {
		if len(pol.Disable) >= maxProjectDisables {
			warn("disable: only the first %d entries are read", maxProjectDisables)
			break
		}
		id, ok := el.(string)
		if !ok || !ValidID(id) {
			warn("disable: skipping an entry that is not a model id")
			continue
		}
		if !seen[id] {
			seen[id] = true
			pol.Disable = append(pol.Disable, id)
		}
	}
	return pol, wl.list()
}
