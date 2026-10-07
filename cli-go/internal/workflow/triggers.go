package workflow

// triggers.go — workflow `triggers:` declarations and the trusted user-level
// file that enables them (K-152).
//
// A workflow YAML may DECLARE triggers, but a declaration alone never fires:
// a cloned repository must not be able to schedule agents on the operator's
// machine. A trigger fires only when the operator enabled it in
//
//	~/.yakos-state/schedules/<slug>-<hash12>.yaml
//
// where <hash12> is the first 12 hex digits of the SHA-256 of the workspace's
// canonical (symlink-resolved) path, so two workspaces that share a folder
// name never share a file. The file must also name its workspace
// (`workspace: <path>`) and the daemon refuses it unless that path is the
// daemon's own workspace root (os.SameFile). Each enabled entry pins the
// workflow file's SHA-256 (`workflow_sha`), so changing the workflow after it
// was enabled stops the trigger until the operator re-enables it. It is read through statepath.ReadTrustedPrivate (a regular file, not a symlink,
// owned by the operator, mode 0600, in an operator-owned non-writable
// directory). The state dir comes from the home directory only, never from
// YAKOS_DISPATCH_LOG, which a project can set (statepath.TrustedDir).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

// credentialEnvNames are well-known credential variables. A webhook secret is
// shared with every sender, so naming one of these as secret_env would hand a
// real credential to callers; it is refused.
var credentialEnvNames = map[string]bool{
	"GITHUB_TOKEN": true, "GH_TOKEN": true, "GITLAB_TOKEN": true, "NPM_TOKEN": true,
	"AWS_SECRET_ACCESS_KEY": true, "AWS_ACCESS_KEY_ID": true, "AWS_SESSION_TOKEN": true,
	"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "OPENAI_API_KEY": true,
	"GOOGLE_API_KEY": true, "GEMINI_API_KEY": true, "AZURE_OPENAI_API_KEY": true,
	"HF_TOKEN": true, "SLACK_BOT_TOKEN": true, "STRIPE_SECRET_KEY": true,
	"DATABASE_URL": true, "CLAUDE_CODE_OAUTH_TOKEN": true,
}

// errCredentialEnv is the clear refusal for a credential name as secret_env.
func errCredentialEnv(name string) error {
	return fmt.Errorf("secret_env %q is a well-known credential variable and must not be shared as a webhook secret; use a dedicated variable such as YAKOS_WEBHOOK_SECRET", name)
}

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
	if t.Webhook != nil && credentialEnvNames[t.Webhook.SecretEnv] {
		return fmt.Errorf("workflow: triggers.webhook: %w", errCredentialEnv(t.Webhook.SecretEnv))
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
	// WorkflowSHA is the hex SHA-256 of the workflow file's bytes when the
	// operator enabled it (`shasum -a 256 <work>/workflows/<name>.yaml`). A
	// trigger fires only while the file still hashes to this value, so editing
	// the workflow (a git pull, the console editor, an agent) cannot change
	// what an enabled trigger runs without the operator re-enabling it.
	WorkflowSHA string `yaml:"workflow_sha"`
}

// PinError is returned by CheckPin when the workflow file no longer matches
// the hash the operator enabled. Its message carries the current hash (never a
// path) so the operator can review the file and paste the hash to re-enable.
type PinError struct{ Current string }

func (e *PinError) Error() string {
	return "workflow file changed since it was enabled; review it, then set workflow_sha to " + e.Current + " in the schedules entry to re-enable"
}

// CheckPin returns a *PinError unless current (the hash LoadFile returned)
// equals the entry's workflow_sha.
func CheckPin(ent ScheduleEntry, current string) error {
	if ent.WorkflowSHA == "" || !strings.EqualFold(strings.TrimSpace(ent.WorkflowSHA), current) {
		return &PinError{Current: current}
	}
	return nil
}

// Schedules is the parsed user-level enablement file.
type Schedules struct {
	Version int `yaml:"version"`
	// Workspace is the absolute path of the workspace this file enables
	// triggers for. It must be the daemon's own workspace root.
	Workspace string `yaml:"workspace"`
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

// maxSchedulesBytes caps the enablement file; a larger file is refused.
const maxSchedulesBytes = 64 << 10

// slugRe matches the characters a project slug may keep.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// ProjectSlug derives the readable part of the schedules file name from the
// workspace root: the lowercased base name with every run of other characters
// folded to "-". It is NOT an identity (two workspaces can share it); the
// file name also carries a hash of the canonical path (SchedulesPath).
func ProjectSlug(workspaceRoot string) string {
	base := strings.ToLower(filepath.Base(filepath.Clean(workspaceRoot)))
	s := strings.Trim(slugRe.ReplaceAllString(base, "-"), "-")
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-")
	}
	return s
}

// canonicalRoot returns the absolute, symlink-resolved form of root.
func canonicalRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("empty workspace root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// SchedulesPath is where the enablement file for the workspace at root lives:
// <state>/schedules/<slug>-<first 12 hex of sha256(canonical root)>.yaml. It
// returns "" when there is no home directory or the root cannot be resolved.
func SchedulesPath(root string) string {
	dir := statepath.TrustedDir()
	if dir == "" {
		return ""
	}
	canon, err := canonicalRoot(root)
	if err != nil {
		return ""
	}
	key := canon
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		key = strings.ToLower(key) // case-insensitive volumes: one name per directory
	}
	sum := sha256.Sum256([]byte(key))
	slug := ProjectSlug(canon)
	if slug == "" {
		slug = "workspace"
	}
	return filepath.Join(dir, "schedules", slug+"-"+hex.EncodeToString(sum[:])[:12]+".yaml")
}

// ErrSchedulesUntrusted marks an enablement file that exists but fails the
// trust check. Its message carries the reason, never the path.
var ErrSchedulesUntrusted = errors.New("schedules file is not trusted")

// ErrSchedulesWrongWorkspace marks a file whose `workspace:` is not this
// daemon's workspace root (for example a copy of another workspace's file).
var ErrSchedulesWrongWorkspace = errors.New("schedules file belongs to a different workspace")

// LoadSchedules reads the enablement file for the workspace at root. A missing file (or no home
// directory) is not an error: it returns empty Schedules, so nothing is
// enabled. An untrusted or malformed file returns an error and the caller must
// treat every trigger as disabled.
func LoadSchedules(root string) (Schedules, error) {
	path := SchedulesPath(root)
	if path == "" {
		return Schedules{}, nil
	}
	// One byte over the cap so an oversize file is refused, not truncated.
	data, err := statepath.ReadTrustedPrivate(path, maxSchedulesBytes+1)
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
	if len(data) > maxSchedulesBytes {
		return Schedules{}, fmt.Errorf("workflow: schedules: file exceeds %d bytes", maxSchedulesBytes)
	}
	s, err := parseSchedules(data)
	if err != nil {
		return Schedules{}, err
	}
	if len(s.Workflows) > 0 || s.Workspace != "" {
		if !sameWorkspace(s.Workspace, root) {
			return Schedules{}, ErrSchedulesWrongWorkspace
		}
	}
	return s, nil
}

// sameWorkspace reports whether claimed names the same directory as root
// (os.SameFile, so symlinks and case aliases of one directory match). An empty
// or relative claim never matches.
func sameWorkspace(claimed, root string) bool {
	if claimed == "" || !filepath.IsAbs(claimed) {
		return false
	}
	a, err := os.Stat(claimed)
	if err != nil || !a.IsDir() {
		return false
	}
	canon, err := canonicalRoot(root)
	if err != nil {
		return false
	}
	b, err := os.Stat(canon)
	if err != nil {
		return false
	}
	return os.SameFile(a, b)
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
		if credentialEnvNames[e.SecretEnv] {
			return Schedules{}, fmt.Errorf("workflow: schedules: %q: %w", name, errCredentialEnv(e.SecretEnv))
		}
	}
	if _, err := s.Location(); err != nil {
		return Schedules{}, err
	}
	return s, nil
}
