package workflow_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/workflow"
)

const hookWF = `version: 1
name: hooked
triggers:
  cron: "0 9 * * *"
  webhook:
    secret_env: YAKOS_EDIT_TEST_SECRET
nodes:
  - id: a
    agent: reviewer
    prompt: "p"
    output_limit: 100
`

func loadWF(t *testing.T, body string) (*workflow.Workflow, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "w.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, sha, err := workflow.LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return wf, sha
}

func TestEnableSchedule_WritesPrivateFileLoadableByTheDaemon(t *testing.T) {
	path := schedHome(t, "proj")
	ws := wsOf(t)
	wf, sha := loadWF(t, hookWF)
	res, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true, Webhook: true})
	if err != nil || !res.Changed {
		t.Fatalf("enable: %v %+v", err, res)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
	s, err := workflow.LoadSchedules(ws)
	if err != nil {
		t.Fatalf("the daemon cannot load what the CLI wrote: %v", err)
	}
	e := s.Workflows["hooked"]
	if !e.Cron || !e.Webhook || e.SecretEnv != "YAKOS_EDIT_TEST_SECRET" || e.WorkflowSHA != sha {
		t.Fatalf("entry = %+v", e)
	}
	// Keyed by the canonical path: the file repeats it.
	canon, _ := filepath.EvalSymlinks(ws)
	if !strings.Contains(readFile(t, path), "workspace: "+canon) {
		t.Errorf("file lacks the canonical workspace:\n%s", readFile(t, path))
	}
	// Idempotent: a second identical enable writes nothing.
	if res2, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true, Webhook: true}); err != nil || res2.Changed {
		t.Fatalf("second enable: %v %+v", err, res2)
	}
	// No temporary files left beside it.
	ents, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".edit-") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Re-enabling after the workflow changed re-pins; the old pin is not kept.
func TestEnableSchedule_ReEnableRepins(t *testing.T) {
	schedHome(t, "proj")
	ws := wsOf(t)
	wf, sha := loadWF(t, hookWF)
	if _, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true}); err != nil {
		t.Fatal(err)
	}
	wf2, sha2 := loadWF(t, strings.Replace(hookWF, `"p"`, `"changed"`, 1))
	if _, err := workflow.EnableSchedule(ws, wf2, sha2, workflow.ScheduleChange{Cron: true}); err != nil {
		t.Fatal(err)
	}
	s, _ := workflow.LoadSchedules(ws)
	if got := s.Workflows["hooked"].WorkflowSHA; got != sha2 || got == sha {
		t.Fatalf("pin = %s, want %s", got, sha2)
	}
}

func TestEnableSchedule_RefusesUndeclaredTrigger(t *testing.T) {
	schedHome(t, "proj")
	ws := wsOf(t)
	cronOnly, sha := loadWF(t, cronWF)
	if _, err := workflow.EnableSchedule(ws, cronOnly, sha, workflow.ScheduleChange{Webhook: true}); err == nil {
		t.Fatal("enabled a webhook the workflow does not declare")
	}
	if _, err := workflow.EnableSchedule(ws, cronOnly, sha, workflow.ScheduleChange{}); err == nil {
		t.Fatal("enabled nothing without error")
	}
	if s, _ := workflow.LoadSchedules(ws); len(s.Workflows) != 0 {
		t.Fatalf("a refused enable wrote %v", s.Workflows)
	}
}

func TestEnableSchedule_KeepsOtherEntriesAndRefusesForeignWorkspace(t *testing.T) {
	path := schedHome(t, "proj")
	ws := wsOf(t)
	writeSched(t, path, enabledSched, 0o600) // enables "nightly"
	wf, sha := loadWF(t, hookWF)
	if _, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true}); err != nil {
		t.Fatal(err)
	}
	s, err := workflow.LoadSchedules(ws)
	if err != nil || !s.Workflows["nightly"].Cron || !s.Workflows["hooked"].Cron || s.Timezone != "UTC" {
		t.Fatalf("other entries lost: %v %+v", err, s)
	}
	// A file that names another workspace is refused, never retargeted.
	other := t.TempDir()
	writeSched(t, path, "version: 1\nworkspace: "+other+"\nworkflows: {}\n", 0o600)
	before := readFile(t, path)
	if _, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true}); err == nil {
		t.Fatal("wrote into another workspace's file")
	}
	if readFile(t, path) != before {
		t.Fatal("a refused write changed the file")
	}
}

func TestEnableSchedule_RefusesUntrustedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	path := schedHome(t, "proj")
	ws := wsOf(t)
	writeSched(t, path, enabledSched, 0o666) // group/world writable
	wf, sha := loadWF(t, hookWF)
	if _, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true}); err == nil {
		t.Fatal("overwrote a file others can write")
	}
	// A merely readable file is accepted and comes back private.
	writeSched(t, path, enabledSched, 0o644)
	if _, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v after the write, want 0600", fi.Mode().Perm())
	}
	victim := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(victim, []byte("keep\n"), 0o600)
	_ = os.Remove(path)
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if _, err := workflow.EnableSchedule(ws, wf, sha, workflow.ScheduleChange{Cron: true}); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep\n" {
		t.Fatalf("symlink target changed: %q", b)
	}
}

func TestDisableSchedule(t *testing.T) {
	path := schedHome(t, "proj")
	ws := wsOf(t)
	if _, err := workflow.DisableSchedule(ws, "nightly"); err != workflow.ErrScheduleNoop {
		t.Fatalf("no file: err = %v, want ErrScheduleNoop", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("disable created a file")
	}
	writeSched(t, path, enabledSched, 0o600)
	if _, err := workflow.DisableSchedule(ws, "other"); err != workflow.ErrScheduleNoop {
		t.Fatalf("unknown entry: err = %v", err)
	}
	res, err := workflow.DisableSchedule(ws, "nightly")
	if err != nil || !res.Changed {
		t.Fatalf("disable: %v %+v", err, res)
	}
	if s, err := workflow.LoadSchedules(ws); err != nil || len(s.Workflows) != 0 {
		t.Fatalf("after disable: %v %+v", err, s)
	}
}

// Two workspaces that share a folder name get different files, and the CLI
// writer keeps them apart.
func TestEnableSchedule_SameFolderNameStaysSeparate(t *testing.T) {
	schedHome(t, "proj")
	a := wsOf(t)
	b := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(b, 0o755); err != nil {
		t.Fatal(err)
	}
	wf, sha := loadWF(t, hookWF)
	if _, err := workflow.EnableSchedule(a, wf, sha, workflow.ScheduleChange{Cron: true}); err != nil {
		t.Fatal(err)
	}
	if workflow.SchedulesPath(a) == workflow.SchedulesPath(b) {
		t.Fatal("same-name workspaces share a schedules file")
	}
	if s, err := workflow.LoadSchedules(b); err != nil || len(s.Workflows) != 0 {
		t.Fatalf("the other workspace sees %v (%v)", s.Workflows, err)
	}
}

// ---- webhook secret file ----------------------------------------------------

func secretFile(t *testing.T, env, body string, mode os.FileMode) string {
	t.Helper()
	p := workflow.WebhookSecretPath(env)
	if p == "" {
		t.Fatal("no secret path")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, mode)
	return p
}

func TestLookupWebhookSecret(t *testing.T) {
	schedHome(t, "proj")
	const env = "YAKOS_EDIT_TEST_SECRET"
	t.Setenv(env, "from-the-environment-0123")
	if got := workflow.LookupWebhookSecret(env); got != "from-the-environment-0123" {
		t.Fatalf("no file: got %q, want the environment value", got)
	}
	secretFile(t, env, "from-the-file-0123456789\n", 0o600)
	if got := workflow.LookupWebhookSecret(env); got != "from-the-file-0123456789" {
		t.Fatalf("file and env both set: got %q, want the file value, newline trimmed", got)
	}
	t.Setenv(env, "")
	if got := workflow.LookupWebhookSecret(env); got != "from-the-file-0123456789" {
		t.Fatalf("file only: got %q", got)
	}
}

func TestLookupWebhookSecret_UntrustedFileFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits")
	}
	schedHome(t, "proj")
	const env = "YAKOS_EDIT_TEST_SECRET"
	t.Setenv(env, "from-the-environment-0123")
	secretFile(t, env, "from-the-file-0123456789", 0o644) // group/world readable
	if got := workflow.LookupWebhookSecret(env); got != "" {
		t.Fatalf("a non-private file fell back or was accepted: %q", got)
	}
}

func TestWebhookSecretPath_RefusesBadNames(t *testing.T) {
	schedHome(t, "proj")
	for _, n := range []string{"", "../x", "PATH", "GITHUB_TOKEN", "yakos_lower", "YAKOS_/../X"} {
		if p := workflow.WebhookSecretPath(n); p != "" {
			t.Errorf("%q gave a path %q", n, p)
		}
	}
}
