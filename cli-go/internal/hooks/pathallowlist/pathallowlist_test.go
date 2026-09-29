package pathallowlist_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/pathallowlist"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

// env is one test sandbox: a project dir with a policy file, a work dir for
// logs/bypass, and an outside dir standing in for "somewhere else on disk".
type env struct {
	t       *testing.T
	proj    string
	work    string
	outside string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	// Resolve symlinks in the temp roots up front (macOS /var -> /private/var)
	// so expectations about "resolved" paths are stable.
	resolve := func(d string) string {
		r, err := filepath.EvalSymlinks(d)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	return &env{t: t, proj: resolve(t.TempDir()), work: resolve(t.TempDir()), outside: resolve(t.TempDir())}
}

// policy writes .claude/path-allowlist.json from raw JSON text.
func (e *env) policyRaw(raw string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Join(e.proj, ".claude"), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.proj, ".claude", "path-allowlist.json"), []byte(raw), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) policy(v map[string]any) {
	e.t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		e.t.Fatal(err)
	}
	e.policyRaw(string(data))
}

func (e *env) bypass(scope string) {
	e.t.Helper()
	body := "# Active hook bypasses\n\n## Active entries\n\n## bypass:t\n\n**Hook:** path-allowlist\n**Scope:** " + scope + "\n"
	if err := os.WriteFile(filepath.Join(e.work, "hook-bypass.md"), []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) symlink(target, link string) {
	e.t.Helper()
	if runtime.GOOS == "windows" {
		e.t.Skip("symlink creation needs privileges on windows")
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		e.t.Fatal(err)
	}
}

// input builds a Claude-Code-shaped HookInput (tool_input.file_path, the
// shape hookio.Decode produces) with CLAUDE_PROJECT_DIR in the env snapshot.
func (e *env) input(tool, file, agent string) hooktype.HookInput {
	ti := map[string]any{}
	if file != "" {
		ti["file_path"] = file
	}
	p := map[string]any{"tool_input": ti, "session_id": "s-1"}
	if agent != "" {
		p["agent_type"] = agent
	}
	return hooktype.HookInput{
		Event:   "PreToolUse",
		Tool:    tool,
		Payload: p,
		Env:     map[string]string{"CLAUDE_PROJECT_DIR": e.proj},
	}
}

func (e *env) run(in hooktype.HookInput) hooktype.HookOutput {
	e.t.Helper()
	h := &pathallowlist.Hook{WorkCurrentDir: e.work, NowFn: fixedNow}
	out, err := h.Run(context.Background(), in)
	if err != nil {
		e.t.Fatalf("Run error: %v", err)
	}
	return out
}

func (e *env) lastLog() map[string]any {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.work, "logs", "path-allowlist.ndjson"))
	if err != nil {
		e.t.Fatalf("read log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		e.t.Fatalf("parse log: %v", err)
	}
	return rec
}

func (e *env) expect(name string, in hooktype.HookInput, wantRC int, wantReason string) {
	e.t.Helper()
	out := e.run(in)
	if out.ExitCode != wantRC {
		e.t.Fatalf("%s: exit=%d want %d (stderr=%q)", name, out.ExitCode, wantRC, out.Stderr)
	}
	if wantReason != "" {
		if got, _ := e.lastLog()["reason"].(string); got != wantReason {
			e.t.Fatalf("%s: log reason=%q want %q", name, got, wantReason)
		}
	}
	if wantRC == 2 && !strings.HasPrefix(string(out.Stderr), "path-allowlist: ") {
		e.t.Fatalf("%s: block stderr must start with hook name, got %q", name, out.Stderr)
	}
}

var goAPI = map[string]any{
	"lead":   map[string]any{"allow": []string{"**"}, "deny": []string{".env", ".env.*"}},
	"go-api": map[string]any{"allow": []string{"api/**", "internal/**"}, "deny": []string{"api/migrations/**"}},
}

func TestBasicAllowDeny(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("allowed", e.input("Edit", "api/handler.go", "go-api"), 0, "allow matched")
	e.expect("outside allow", e.input("Edit", "web/index.js", "go-api"), 2, "path outside agent's allow-list")
	e.expect("deny wins over allow", e.input("Edit", "api/migrations/001.sql", "go-api"), 2, "deny pattern matched")
	e.expect("absolute in-root", e.input("Write", filepath.Join(e.proj, "api", "ok.go"), "go-api"), 0, "allow matched")
	e.expect("namespaced agent", e.input("Edit", "api/x.go", "yakos:go-api"), 0, "allow matched")
	e.expect("padded agent", e.input("Edit", "api/x.go", "  go-api  "), 0, "allow matched")
	e.expect("no policy for agent", e.input("Edit", "anything", "other"), 0, "no policy for agent_type")
	e.expect("lead default", e.input("Edit", ".env", ""), 2, "deny pattern matched")
}

func TestToolGate(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	if out := e.run(e.input("Read", "web/x", "go-api")); out.ExitCode != 0 {
		t.Fatalf("Read must be ignored, exit=%d", out.ExitCode)
	}
	if _, err := os.Stat(filepath.Join(e.work, "logs")); err == nil {
		t.Fatal("ignored tool must not log")
	}
	for _, tool := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
		e.expect(tool, e.input(tool, "web/x", "go-api"), 2, "")
	}
}

func TestNotebookPathFallback(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	in := e.input("NotebookEdit", "", "go-api")
	in.Payload["tool_input"] = map[string]any{"notebook_path": "web/n.ipynb"}
	e.expect("notebook_path outside allow", in, 2, "path outside agent's allow-list")
	in.Payload["tool_input"] = map[string]any{"notebook_path": "api/n.ipynb"}
	e.expect("notebook_path allowed", in, 0, "allow matched")
}

func TestNoFilePath(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("no path", e.input("Edit", "", "go-api"), 0, "no file_path in tool_input")
}

func TestNoPolicyFile(t *testing.T) {
	e := newEnv(t)
	e.expect("missing file", e.input("Edit", "web/x", "go-api"), 0, "no allowlist file at .claude/path-allowlist.json")
}

// ---- K-81: allow semantics --------------------------------------------------

func TestAllowEmptyArrayIsDenyAll(t *testing.T) {
	e := newEnv(t)
	e.policyRaw(`{"go-api":{"allow":[]}}`)
	e.expect("empty allow blocks", e.input("Edit", "api/x.go", "go-api"), 2, "path outside agent's allow-list")
	if note := e.lastLog()["note"]; note != "allow-list is empty (deny-all)" {
		t.Fatalf("note=%v", note)
	}
	e.bypass("api/x.go")
	e.expect("empty allow honors bypass", e.input("Edit", "api/x.go", "go-api"), 0, "outside allow but bypass active")
}

func TestAllowMissingOrNullIsUnconstrained(t *testing.T) {
	e := newEnv(t)
	e.policyRaw(`{"go-api":{"deny":["x"]},"a":{"allow":null},"b":{}}`)
	e.expect("no allow key", e.input("Edit", "api/y.go", "go-api"), 0, "no allow/deny match")
	e.expect("allow null", e.input("Edit", "api/y.go", "a"), 0, "no allow/deny match")
	e.expect("empty policy object", e.input("Edit", "api/y.go", "b"), 0, "no allow/deny match")
}

func TestAllowNonArrayBlocks(t *testing.T) {
	for _, raw := range []string{`"api/**"`, `0`, `5`, `true`, `false`, `{"a":"api/**"}`, `""`} {
		e := newEnv(t)
		e.policyRaw(`{"go-api":{"allow":` + raw + `}}`)
		e.expect("allow="+raw, e.input("Edit", "api/x.go", "go-api"), 2, "policy 'allow' for agent_type is not an array")
	}
}

func TestAllowArrayOfEmptyStringsIsDenyAll(t *testing.T) {
	e := newEnv(t)
	e.policyRaw(`{"go-api":{"allow":[""]}}`)
	e.expect("empty-string glob", e.input("Edit", "api/x.go", "go-api"), 2, "path outside agent's allow-list")
}

func TestDenyNonArrayBlocks(t *testing.T) {
	for _, raw := range []string{`"api/**"`, `1`, `true`, `{"a":"api/**"}`} {
		e := newEnv(t)
		e.policyRaw(`{"go-api":{"deny":` + raw + `,"allow":["**"]}}`)
		e.expect("deny="+raw, e.input("Edit", "api/x.go", "go-api"), 2, "policy 'deny' for agent_type is not an array")
	}
	e := newEnv(t)
	e.policyRaw(`{"go-api":{"deny":null,"allow":["**"]}}`)
	e.expect("deny null ok", e.input("Edit", "api/x.go", "go-api"), 0, "allow matched")
}

// ---- glob semantics ---------------------------------------------------------

func TestDenyIsCaseInsensitive(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect(".ENV", e.input("Write", ".ENV", ""), 2, "deny pattern matched")
	e.expect(".Env.Local", e.input("Write", ".Env.Local", ""), 2, "deny pattern matched")
	e.policyRaw(`{"go-api":{"deny":["*.pem"]}}`)
	e.expect("PEM upper", e.input("Write", "certs/KEY.PEM", "go-api"), 2, "deny pattern matched")
	e.policyRaw(`{"go-api":{"allow":["API/**"]}}`)
	e.expect("allow case-insensitive", e.input("Write", "api/x.go", "go-api"), 0, "allow matched")
}

func TestStarSpansSlash(t *testing.T) {
	// Regression: path.Match's '*' stops at '/', which made a deny of
	// "api/migrations/**" miss "api/migrations/2026/x.sql".
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("deep deny", e.input("Edit", "api/migrations/2026/09/x.sql", "go-api"), 2, "deny pattern matched")
	e.expect("deep allow", e.input("Edit", "api/a/b/c/d.go", "go-api"), 0, "allow matched")
}

func TestDenyBasenameFallbackButAllowHasNone(t *testing.T) {
	e := newEnv(t)
	e.policyRaw(`{"go-api":{"deny":["*.pem"],"allow":["api/*.go"]}}`)
	e.expect("deny basename", e.input("Edit", "api/deep/x.pem", "go-api"), 2, "deny pattern matched")
	// "api/*.go" spans '/', so a nested go file is allowed; a file OUTSIDE
	// api/ whose basename would match must not be rescued by a basename
	// fallback on the allow side.
	e.expect("no allow basename fallback", e.input("Edit", "malicious/api/x.go", "go-api"), 2, "path outside agent's allow-list")
}

func TestGlobsWithNewlinesSplit(t *testing.T) {
	e := newEnv(t)
	e.policyRaw("{\"go-api\":{\"deny\":[\"nothing\\nsecret.txt\"]}}")
	e.expect("newline-split deny", e.input("Edit", "secret.txt", "go-api"), 2, "deny pattern matched")
}

// ---- policy file integrity (N4.1) ------------------------------------------

func TestCorruptPolicyBlocks(t *testing.T) {
	for name, raw := range map[string]string{
		"truncated":      `{"go-api": {"allow"`,
		"top-level-list": `[1,2]`,
		"top-level-null": `null`,
		"empty-file":     ``,
		"garbage":        `not json`,
	} {
		e := newEnv(t)
		e.policyRaw(raw)
		e.expect(name, e.input("Edit", "web/x", "go-api"), 2, "path-allowlist.json unreadable or not a JSON object")
	}
}

func TestNonObjectAgentPolicyBlocks(t *testing.T) {
	for _, raw := range []string{`"oops"`, `[]`, `5`, `true`} {
		e := newEnv(t)
		e.policyRaw(`{"go-api":` + raw + `}`)
		e.expect("policy="+raw, e.input("Edit", "api/x", "go-api"), 2, "policy value for agent_type is not a JSON object")
	}
	// jq's // treats null/false as absent: same as bash, "no policy".
	for _, raw := range []string{`null`, `false`} {
		e := newEnv(t)
		e.policyRaw(`{"go-api":` + raw + `}`)
		e.expect("policy="+raw, e.input("Edit", "api/x", "go-api"), 0, "no policy for agent_type")
	}
}

func TestPolicyFileIsDirectoryTreatedAsAbsent(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Join(e.proj, ".claude", "path-allowlist.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.expect("dir policy", e.input("Edit", "web/x", "go-api"), 0, "no allowlist file at .claude/path-allowlist.json")
}

// ---- path escapes -----------------------------------------------------------

func TestAbsoluteOutOfRootBlocks(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("etc passwd", e.input("Write", "/etc/passwd", "go-api"), 2, "absolute path outside project root")
	e.expect("sibling with shared prefix", e.input("Write", e.proj+"-evil/api/x.go", "go-api"), 2, "absolute path outside project root")
	// Lead's allow-everything policy must not rescue an out-of-root path.
	e.expect("lead absolute", e.input("Write", "/etc/passwd", ""), 2, "absolute path outside project root")
	e.bypass("/etc/passwd")
	e.expect("absolute bypass", e.input("Write", "/etc/passwd", "go-api"), 0, "absolute out-of-root path but bypass active")
}

func TestRootEqualBlocks(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("root itself", e.input("Write", e.proj, "go-api"), 2, "file_path is the project root itself")
	// Trailing-slash env form must behave identically (R2-1).
	in := e.input("Write", e.proj, "go-api")
	in.Env["CLAUDE_PROJECT_DIR"] = e.proj + "/"
	e.expect("root, slash env", in, 2, "file_path is the project root itself")
	// The hook matches bypass scopes against the "/"-normalized path (a no-op on
	// POSIX), so the scope must be written in that form on Windows too.
	e.bypass(filepath.ToSlash(e.proj))
	e.expect("root bypass", e.input("Write", e.proj, "go-api"), 0, "file_path is the project root but bypass active")
}

func TestTrailingSlashProjectDirEnv(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	for _, suffix := range []string{"/", "//", "///"} {
		in := e.input("Write", filepath.Join(e.proj, "api", "ok.go"), "go-api")
		in.Env["CLAUDE_PROJECT_DIR"] = e.proj + suffix
		e.expect("suffix "+suffix, in, 0, "allow matched")
	}
}

func TestUnsetProjectDirEnvBlocksAbsolute(t *testing.T) {
	// Bash reads $CLAUDE_PROJECT_DIR only; unset means nothing gets
	// stripped, so an absolute path is "absolute and outside the root".
	e := newEnv(t)
	e.policy(goAPI)
	in := e.input("Write", filepath.Join(e.proj, "api", "ok.go"), "go-api")
	in.Env = map[string]string{}
	t.Chdir(e.proj) // policy is read from ./.claude when the env var is unset
	out := e.run(in)
	if out.ExitCode != 2 {
		t.Fatalf("exit=%d want 2 (nothing to strip with CLAUDE_PROJECT_DIR unset)", out.ExitCode)
	}
}

func TestLexicalTraversalBlocks(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("classic", e.input("Write", "api/../../../../etc/cron.d/pwn", "go-api"), 2, "path lexically escapes project root")
	e.expect("bare dotdot", e.input("Write", "..", "go-api"), 2, "path lexically escapes project root")
	e.expect("leading dotdot", e.input("Write", "../x", "go-api"), 2, "path lexically escapes project root")
	e.expect("in-root dotdot ok", e.input("Write", "api/x/../y.go", "go-api"), 0, "allow matched")
	if got := e.lastLog()["file_path"]; got != "api/y.go" {
		t.Fatalf("logged file_path=%v want normalized api/y.go", got)
	}
	e.bypass("../x")
	e.expect("traversal bypass", e.input("Write", "../x", "go-api"), 0, "path traversal detected but bypass active")
}

func TestSymlinkEscapeBlocks(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.symlink(e.outside, filepath.Join(e.proj, "api", "lnk"))
	e.symlink("/etc/hosts", filepath.Join(e.proj, "api", "escape-link"))
	e.expect("dir symlink", e.input("Write", "api/lnk/x.go", "go-api"), 2, "path resolves outside project root via symlink")
	e.expect("file symlink", e.input("Write", "api/escape-link", "go-api"), 2, "path resolves outside project root via symlink")
	rec := e.lastLog()
	if rec["resolved"] == nil || rec["project_root"] != e.proj {
		t.Fatalf("log must carry resolved + project_root, got %v", rec)
	}
	e.bypass("api/lnk/x.go")
	e.expect("symlink bypass", e.input("Write", "api/lnk/x.go", "go-api"), 0, "symlink escape detected but bypass active")
}

func TestDanglingAndChainedSymlinks(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.symlink(filepath.Join(e.outside, "nope"), filepath.Join(e.proj, "api", "dangling"))
	e.symlink(filepath.Join(e.proj, "api", "hop1"), filepath.Join(e.proj, "api", "hop2"))
	e.symlink(e.outside, filepath.Join(e.proj, "api", "hop1"))
	e.expect("dangling outside", e.input("Write", "api/dangling", "go-api"), 2, "path resolves outside project root via symlink")
	e.expect("chain outside", e.input("Write", "api/hop2/x", "go-api"), 2, "path resolves outside project root via symlink")
}

func TestSymlinkCycleFailsClosed(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.symlink(filepath.Join(e.proj, "api", "b"), filepath.Join(e.proj, "api", "a"))
	e.symlink(filepath.Join(e.proj, "api", "a"), filepath.Join(e.proj, "api", "b"))
	e.expect("cycle", e.input("Write", "api/a/x", "go-api"), 2, "path resolves outside project root via symlink")
}

func TestInRootSymlinkAllowed(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	if err := os.MkdirAll(filepath.Join(e.proj, "internal", "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.symlink(filepath.Join(e.proj, "internal", "real"), filepath.Join(e.proj, "api", "inl"))
	e.expect("in-root symlink", e.input("Write", "api/inl/x.go", "go-api"), 0, "allow matched")
}

// DotDotAfterSymlink: "api/lnk/sub/../../secret.go" collapses lexically to
// "api/secret.go" (allowed), but the OS applies ".." to the RESOLVED prefix
// and writes to <outside>/../secret.go. The bash hook checks only the
// lexical form and passes this; the Go port must block it.
func TestDotDotAfterSymlinkBlocks(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.symlink(e.outside, filepath.Join(e.proj, "api", "lnk"))
	if err := os.MkdirAll(filepath.Join(e.outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.expect("dotdot after symlink", e.input("Write", "api/lnk/sub/../../secret.go", "go-api"), 2, "path resolves outside project root via symlink")
	e.expect("single dotdot after symlink", e.input("Write", "api/lnk/../secret.go", "go-api"), 2, "path resolves outside project root via symlink")
	// Same shape but the symlink stays in-root: harmless, allowed.
	if err := os.MkdirAll(filepath.Join(e.proj, "internal", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.symlink(filepath.Join(e.proj, "internal", "deep"), filepath.Join(e.proj, "api", "inl"))
	e.expect("in-root dotdot after symlink", e.input("Write", "api/inl/../z.go", "go-api"), 0, "allow matched")
}

func TestNULByteBlocks(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("nul", e.input("Write", "api/ok.go\x00../../etc/passwd", "go-api"), 2, "file_path contains a NUL or newline byte")
	e.bypass("api/ok.go")
	e.expect("nul ignores bypass", e.input("Write", "api/ok.go\x00", "go-api"), 2, "file_path contains a NUL or newline byte")
}

func TestNewlineInPathBlocksOutright(t *testing.T) {
	// bash's normalizer only saw the first line, so "api/ok.go<LF>/../../etc/x"
	// was checked as "api/ok.go". Both sides now refuse any newline.
	e := newEnv(t)
	e.policy(goAPI)
	for _, p := range []string{"api/ok\n/../../../../etc/x", "api/ok.go\n/../x.env", "api/a\nb.go", "\n", "api/ok.go\n"} {
		e.expect("newline "+p, e.input("Write", p, "go-api"), 2, "file_path contains a NUL or newline byte")
	}
	e.bypass("api/ok.go")
	e.expect("newline ignores bypass", e.input("Write", "api/ok.go\n", "go-api"), 2, "file_path contains a NUL or newline byte")
}

func TestNonStringFilePathRenderedLikeJQ(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	in := e.input("Write", "", "go-api")
	in.Payload["tool_input"] = map[string]any{"file_path": float64(5)}
	e.expect("numeric path", in, 2, "path outside agent's allow-list")
	if got := e.lastLog()["file_path"]; got != "5" {
		t.Fatalf("file_path=%v", got)
	}
	// false/null fall through to notebook_path via jq's //.
	in.Payload["tool_input"] = map[string]any{"file_path": false, "notebook_path": "api/n.ipynb"}
	e.expect("false falls through", in, 0, "allow matched")
}

// ---- bypass -----------------------------------------------------------------

func TestBypassDenyAndAllow(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.bypass("web/index.js")
	e.expect("outside allow bypass", e.input("Edit", "web/index.js", "go-api"), 0, "outside allow but bypass active")
	e.expect("other file still blocked", e.input("Edit", "web/other.js", "go-api"), 2, "path outside agent's allow-list")
	e.bypass("api/migrations/001.sql")
	e.expect("deny bypass", e.input("Edit", "api/migrations/001.sql", "go-api"), 0, "deny matched but bypass active")
}

func TestBypassHonorsRealFormatOnly(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	// A bare "Hook:/Scope:" mention outside a real "## Active entries"
	// section must not count (the old ad hoc strings.Contains did).
	if err := os.WriteFile(filepath.Join(e.work, "hook-bypass.md"), []byte("Hook: path-allowlist\nScope: web/index.js\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.expect("informal mention", e.input("Edit", "web/index.js", "go-api"), 2, "path outside agent's allow-list")
}

// ---- log shape --------------------------------------------------------------

func TestLogRecordShape(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.run(e.input("Edit", "api/x.go", "yakos:go-api"))
	rec := e.lastLog()
	want := map[string]any{
		"hook": "path-allowlist", "severity": "REPORT", "decision": "pass", "reason": "allow matched",
		"agent": "go-api", "session_id": "s-1", "event": "PreToolUse",
		"agent_type": "go-api", "file_path": "api/x.go", "matched_allow": "api/**",
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("log[%s]=%v want %v", k, rec[k], v)
		}
	}
	if _, has := rec["action"]; has {
		t.Error("legacy 'action' field must be gone")
	}
}

func TestNameAndProjectDirFallback(t *testing.T) {
	h := pathallowlist.New("/tmp/w", "/tmp/p")
	if h.Name() != "path-allowlist" {
		t.Fatalf("Name=%q", h.Name())
	}
	// in.Env == nil: fall back to Hook.ProjectDir (embedders/unit tests).
	e := newEnv(t)
	e.policy(goAPI)
	hk := &pathallowlist.Hook{WorkCurrentDir: e.work, ProjectDir: e.proj, NowFn: fixedNow}
	in := e.input("Edit", "web/x", "go-api")
	in.Env = nil
	out, _ := hk.Run(context.Background(), in)
	if out.ExitCode != 2 {
		t.Fatalf("exit=%d", out.ExitCode)
	}
}

// K-99: the root/absolute/".."/symlink escape guards accept only an EXACT
// bypass Scope. A glob entry must never waive them.
func TestEscapeGuardsRejectGlobBypass(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.symlink(e.outside, filepath.Join(e.proj, "api", "lnk"))
	probes := []struct{ name, file, exact string }{
		{"traversal", "api/../../../../etc/cron.d/pwn", "api/../../../../etc/cron.d/pwn"},
		{"absolute", "/etc/passwd", "/etc/passwd"},
		{"symlink", "api/lnk/x.go", "api/lnk/x.go"},
		{"root", e.proj, e.proj},
	}
	for _, p := range probes {
		for _, glob := range []string{"*", "**", "api/**", "/etc/*", e.proj + "*"} {
			e.bypass(glob)
			e.expect(p.name+" glob "+glob, e.input("Write", p.file, "go-api"), 2, "")
		}
		e.bypass(p.exact)
		e.expect(p.name+" exact", e.input("Write", p.file, "go-api"), 0, "")
	}
}

// K-99: the symlink guard probes the path AS WRITTEN. Normalization turns
// "api/lnk/../secret.go" into "api/secret.go", so an exact entry for the
// benign normalized path must not waive an escape that exists only as written.
func TestSymlinkGuardProbesAsWrittenPath(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.symlink(e.outside, filepath.Join(e.proj, "api", "lnk"))
	in := e.input("Write", "api/lnk/../secret.go", "go-api")
	e.bypass("api/secret.go")
	e.expect("normalized entry", in, 2, "path resolves outside project root via symlink")
	e.bypass("api/lnk/../secret.go")
	e.expect("as-written entry", in, 0, "symlink escape detected but bypass active")
}

// K-107 item 1: an agent_type that is only newlines is the lead role (bash
// hi_field strips trailing newlines before its "lead" fallback), so .env is
// denied. A spaces-only agent_type trims to an empty role with no policy.
func TestNewlineAgentTypeIsLead(t *testing.T) {
	e := newEnv(t)
	e.policy(goAPI)
	e.expect("newline agent is lead", e.input("Write", ".env", "\n"), 2, "deny pattern matched")
	e.expect("crlf agent trims to empty (only LF is stripped first)", e.input("Write", ".env", "\r\n"), 0, "no policy for agent_type")
	e.expect("spaces agent has no policy", e.input("Write", ".env", "  "), 0, "no policy for agent_type")
}
