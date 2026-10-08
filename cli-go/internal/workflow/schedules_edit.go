package workflow

// schedules_edit.go: the writer of the trusted schedules file and the reader of
// the optional webhook secret file (K-172).
//
// `yakos flows schedule enable|disable` is the one writer of the file that
// enables triggers. It goes through statepath.EditYAML: a trust-checked read
// (a symlink, another user's file or a group/world-accessible file is refused,
// never overwritten), keys the edit does not touch are kept, and the result
// replaces the file with a 0600 temporary file and a rename.
//
// The file is keyed by the workspace's canonical (symlink-resolved) path, not
// by its folder name: SchedulesPath hashes the canonical root into the file
// name and the file repeats the path in `workspace:`. Two projects that share a
// folder name therefore never share a file (K-152, sec-348 finding 1).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bakw00ds/yakos/internal/statepath"
)

// ErrScheduleNoop is returned by DisableSchedule when the workflow was not
// enabled, so nothing was written.
var ErrScheduleNoop = errors.New("workflow: that workflow is not enabled in the schedules file")

// ScheduleChange says what EnableSchedule turns on.
type ScheduleChange struct {
	Cron, Webhook bool
}

// EnableSchedule enables the triggers in ch for the workflow wf (whose file
// hashed to sha) in the schedules file of the workspace at root. The pin is
// replaced, so enabling again after a review re-pins a changed workflow. It
// fails when ch asks for a trigger the workflow does not declare.
func EnableSchedule(root string, wf *Workflow, sha string, ch ScheduleChange) (statepath.EditResult, error) {
	if !ch.Cron && !ch.Webhook {
		return statepath.EditResult{}, errors.New("workflow: nothing to enable")
	}
	if ch.Cron && (wf.Triggers == nil || wf.Triggers.Cron == "") {
		return statepath.EditResult{}, errors.New("workflow: the workflow declares no triggers.cron")
	}
	if ch.Webhook && (wf.Triggers == nil || wf.Triggers.Webhook == nil) {
		return statepath.EditResult{}, errors.New("workflow: the workflow declares no triggers.webhook")
	}
	if err := ValidateID("workflow name", wf.Name); err != nil {
		return statepath.EditResult{}, err
	}
	path, canon, err := schedulesTarget(root)
	if err != nil {
		return statepath.EditResult{}, err
	}
	return statepath.EditYAML(path, maxSchedulesBytes, func(top *yaml.Node) error {
		if err := claimWorkspace(top, canon); err != nil {
			return err
		}
		statepath.YAMLSet(top, "version", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
		wfs, ok := statepath.YAMLMap(top, "workflows")
		if !ok {
			return errors.New("workflow: schedules: workflows: is not a mapping; fix it by hand first")
		}
		ent := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		statepath.YAMLSet(ent, "cron", boolNode(ch.Cron))
		statepath.YAMLSet(ent, "webhook", boolNode(ch.Webhook))
		if ch.Webhook {
			statepath.YAMLSet(ent, "secret_env", strNode(wf.Triggers.Webhook.SecretEnv))
		}
		statepath.YAMLSet(ent, "workflow_sha", strNode(sha))
		statepath.YAMLSet(wfs, wf.Name, ent)
		return nil
	}, checkSchedules(canon))
}

// DisableSchedule removes the workflow's entry. It returns ErrScheduleNoop,
// writing nothing, when the file has no such entry.
func DisableSchedule(root, name string) (statepath.EditResult, error) {
	if err := ValidateID("workflow name", name); err != nil {
		return statepath.EditResult{}, err
	}
	path, canon, err := schedulesTarget(root)
	if err != nil {
		return statepath.EditResult{}, err
	}
	return statepath.EditYAML(path, maxSchedulesBytes, func(top *yaml.Node) error {
		wfs := statepath.YAMLGet(top, "workflows")
		if wfs == nil || wfs.Kind != yaml.MappingNode || statepath.YAMLGet(wfs, name) == nil {
			return ErrScheduleNoop
		}
		statepath.YAMLDelete(wfs, name)
		return nil
	}, checkSchedules(canon))
}

// schedulesTarget resolves the file path and the canonical root for root.
func schedulesTarget(root string) (path, canon string, err error) {
	path = SchedulesPath(root)
	if path == "" {
		return "", "", errors.New("workflow: no home directory or unresolvable workspace, so no schedules file to write")
	}
	canon, err = canonicalRoot(root)
	if err != nil {
		return "", "", errors.New("workflow: cannot resolve the workspace path")
	}
	return path, canon, nil
}

// claimWorkspace sets `workspace:` to the canonical root. A file that already
// names a different directory is refused, never retargeted.
func claimWorkspace(top *yaml.Node, canon string) error {
	if n := statepath.YAMLGet(top, "workspace"); n != nil && n.Value != "" && !sameWorkspace(n.Value, canon) {
		return ErrSchedulesWrongWorkspace
	}
	statepath.YAMLSet(top, "workspace", strNode(canon))
	return nil
}

// checkSchedules refuses a result the daemon would not load for this workspace.
func checkSchedules(canon string) func([]byte) error {
	return func(data []byte) error {
		s, err := parseSchedules(data)
		if err != nil {
			return err
		}
		if (len(s.Workflows) > 0 || s.Workspace != "") && !sameWorkspace(s.Workspace, canon) {
			return ErrSchedulesWrongWorkspace
		}
		return nil
	}
}

func boolNode(b bool) *yaml.Node {
	v := "false"
	if b {
		v = "true"
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: v}
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

// ---- webhook secret file ----------------------------------------------------

const (
	// webhookSecretDir is the folder, under the trusted state directory, that
	// holds one file per secret_env name.
	webhookSecretDir = "webhook-secrets"
	// maxWebhookSecretBytes bounds a secret file read.
	maxWebhookSecretBytes = 4 << 10
)

// WebhookSecretPath is where the secret for the variable name envName may be
// kept: <state>/webhook-secrets/<envName>. It returns "" without a home
// directory or for a name that is not a valid, YAKOS_-prefixed secret_env.
func WebhookSecretPath(envName string) string {
	if !ValidSecretEnvName(envName) || !strings.HasPrefix(envName, secretEnvPrefix) || credentialEnvNames[envName] {
		return ""
	}
	dir := statepath.TrustedDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, webhookSecretDir, envName)
}

// LookupWebhookSecret returns the secret for envName. A 0600 file in the
// trusted state directory wins over the daemon's environment; the environment
// is used only when the file does not exist. A file that exists but is not
// trusted (a symlink, another user's, group or world accessible, in an
// unsafe directory) or cannot be read yields "": the webhook stays off rather
// than falling back to a weaker source. A trailing newline is trimmed.
func LookupWebhookSecret(envName string) string {
	if p := WebhookSecretPath(envName); p != "" {
		data, err := statepath.ReadTrustedPrivate(p, maxWebhookSecretBytes+1)
		switch {
		case err == nil:
			if len(data) > maxWebhookSecretBytes {
				return ""
			}
			return strings.TrimRight(string(data), "\r\n")
		case !errors.Is(err, os.ErrNotExist):
			return ""
		}
	}
	return os.Getenv(envName)
}

// String for debugging a ScheduleChange.
func (c ScheduleChange) String() string {
	var on []string
	if c.Cron {
		on = append(on, "cron")
	}
	if c.Webhook {
		on = append(on, "webhook")
	}
	return strings.Join(on, "+")
}
