package workflow

// triggers.go — workflow `triggers:` declarations and the trusted user-level
// file that enables them (K-152).
//
// A workflow YAML may DECLARE triggers, but a declaration alone never fires:
// a cloned repository must not be able to schedule agents on the operator's
// machine. A trigger fires only when the operator enabled it in
//
//	~/.yakos-state/schedules/<project-slug>.yaml
//
// read through statepath.ReadTrustedPrivate (a regular file, not a symlink,
// owned by the operator, mode 0600, in an operator-owned non-writable
// directory). The state dir comes from the home directory only, never from
// YAKOS_DISPATCH_LOG, which a project can set (statepath.TrustedDir).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// Triggers is the optional `triggers:` block of a workflow.
type Triggers struct {
	// Cron is a 5-field cron expression (see ParseCron).
	Cron string `yaml:"cron,omitempty"`
	// Webhook declares that POST /flows/api/trigger/<name> may start the run.
	Webhook *WebhookTrigger `yaml:"webhook,omitempty"`
}

// WebhookTrigger names the environment variable holding the shared secret.
type WebhookTrigger struct {
	SecretEnv string `yaml:"secret_env"`
}

// secretEnvRe is the shape of an environment variable name accepted as a
// webhook secret source.
var secretEnvRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// ValidSecretEnvName reports whether name can name a webhook secret variable.
func ValidSecretEnvName(name string) bool { return secretEnvRe.MatchString(name) }

// validateTriggers checks the declaration; it does not consult the user file.
func validateTriggers(t *Triggers) error {
	if t == nil {
		return nil
	}
	if t.Cron != "" {
		if _, err := ParseCron(t.Cron); err != nil {
			return fmt.Errorf("workflow: triggers.cron: %w", err)
		}
	}
	if t.Webhook != nil && !ValidSecretEnvName(t.Webhook.SecretEnv) {
		return fmt.Errorf("workflow: triggers.webhook.secret_env must match %s", secretEnvRe)
	}
	return nil
}

// ScheduleEntry is one workflow's enablement in the user-level file.
type ScheduleEntry struct {
	// Cron enables the workflow's triggers.cron.
	Cron bool `yaml:"cron"`
	// Webhook enables triggers.webhook. SecretEnv must repeat the variable
	// name the workflow declares, so the operator confirms which environment
	// variable a (possibly cloned) workflow may make the daemon read.
	Webhook   bool   `yaml:"webhook"`
	SecretEnv string `yaml:"secret_env"`
}

// Schedules is the parsed user-level enablement file.
type Schedules struct {
	Version int `yaml:"version"`
	// Timezone is the IANA zone cron expressions are read in. Empty means the
	// machine's local zone.
	Timezone  string                   `yaml:"timezone"`
	Workflows map[string]ScheduleEntry `yaml:"workflows"`
}

// Location resolves Timezone.
func (s Schedules) Location() (*time.Location, error) {
	if s.Timezone == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("workflow: schedules: unknown timezone")
	}
	return loc, nil
}

// maxSchedulesBytes caps the enablement file.
const maxSchedulesBytes = 64 << 10

// slugRe matches the characters a project slug may keep.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// ProjectSlug derives the schedules file stem from the workspace root: the
// lowercased base name with every run of other characters folded to "-". It
// returns "" when nothing usable is left.
func ProjectSlug(workspaceRoot string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(workspaceRoot)))
	s := strings.Trim(slugRe.ReplaceAllString(base, "-"), "-")
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-")
	}
	return s
}

// SchedulesPath is where the enablement file for slug lives; "" when there is
// no home directory or the slug is empty.
func SchedulesPath(slug string) string {
	dir := statepath.TrustedDir()
	if dir == "" || slug == "" {
		return ""
	}
	return filepath.Join(dir, "schedules", slug+".yaml")
}

// ErrSchedulesUntrusted marks an enablement file that exists but fails the
// trust check. Its message carries the reason, never the path.
var ErrSchedulesUntrusted = errors.New("schedules file is not trusted")

// LoadSchedules reads the enablement file for slug. A missing file (or no home
// directory) is not an error: it returns empty Schedules, so nothing is
// enabled. An untrusted or malformed file returns an error and the caller must
// treat every trigger as disabled.
func LoadSchedules(slug string) (Schedules, error) {
	path := SchedulesPath(slug)
	if path == "" {
		return Schedules{}, nil
	}
	data, err := statepath.ReadTrustedPrivate(path, maxSchedulesBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Schedules{}, nil
		}
		var ue *statepath.UntrustedError
		if errors.As(err, &ue) {
			return Schedules{}, fmt.Errorf("%w: %s", ErrSchedulesUntrusted, ue.Reason)
		}
		return Schedules{}, fmt.Errorf("workflow: schedules: unreadable")
	}
	return parseSchedules(data)
}

func parseSchedules(data []byte) (Schedules, error) {
	var s Schedules
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if errors.Is(err, io.EOF) {
			return Schedules{}, nil
		}
		return Schedules{}, fmt.Errorf("workflow: schedules: malformed file")
	}
	if s.Version != 1 {
		return Schedules{}, fmt.Errorf("workflow: schedules: version must be 1")
	}
	for name, e := range s.Workflows {
		if err := ValidateID("schedules workflow name", name); err != nil {
			return Schedules{}, err
		}
		if e.SecretEnv != "" && !ValidSecretEnvName(e.SecretEnv) {
			return Schedules{}, fmt.Errorf("workflow: schedules: invalid secret_env for %q", name)
		}
	}
	if _, err := s.Location(); err != nil {
		return Schedules{}, err
	}
	return s, nil
}
