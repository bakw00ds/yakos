package pathallowlist_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/hooks/hooktype"
	"github.com/bakw00ds/yakos/internal/hooks/pathallowlist"
)

var fixedTime = time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return fixedTime }

func readLastLog(t *testing.T, logFile string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var last map[string]any
	start := 0
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			if i > start {
				var rec map[string]any
				if err := json.Unmarshal(data[start:i], &rec); err == nil {
					last = rec
				}
			}
			start = i + 1
		}
	}
	if last == nil {
		t.Fatal("no log entries")
	}
	return last
}

func writeAllowlist(t *testing.T, dir string, content map[string]any) {
	t.Helper()
	claudeDir := filepath.Join(dir, ".claude")
	_ = os.MkdirAll(claudeDir, 0755)
	data, _ := json.Marshal(content)
	_ = os.WriteFile(filepath.Join(claudeDir, "path-allowlist.json"), data, 0644)
}

func makeHook(work, project string) *pathallowlist.Hook {
	return &pathallowlist.Hook{WorkCurrentDir: work, ProjectDir: project, NowFn: fixedNow}
}

func makeInput(tool, filePath, agentRole string) hooktype.HookInput {
	env := map[string]string{}
	if agentRole != "" {
		env["YAKOS_AGENT_ROLE"] = agentRole
	}
	payload := map[string]any{}
	if filePath != "" {
		payload["path"] = filePath
	}
	return hooktype.HookInput{Tool: tool, Payload: payload, Env: env}
}

func TestPathAllowlist_NoAllowlistFilePass(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	h := makeHook(work, proj)
	out, err := h.Run(context.Background(), makeInput("Edit", "main.go", "lead"))
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("err=%v code=%d", err, out.ExitCode)
	}
}

func TestPathAllowlist_NonEditToolSkipped(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Read", "main.go", "lead"))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d for Read tool", out.ExitCode)
	}
}

func TestPathAllowlist_DenyMatchBlocks(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"lead": map[string]any{"deny": []string{"*.env", "secrets/*"}},
	})
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Edit", ".env", "lead"))
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 (block) for deny match", out.ExitCode)
	}
}

func TestPathAllowlist_AllowMatchPasses(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"backend": map[string]any{"allow": []string{"cli-go/*", "*.go"}},
	})
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Write", "main.go", "backend"))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 for allowed path", out.ExitCode)
	}
}

func TestPathAllowlist_OutsideAllowBlocks(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"backend": map[string]any{"allow": []string{"cli-go/*"}},
	})
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Edit", "lib/hooks/foo.sh", "backend"))
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 (block) for outside allow", out.ExitCode)
	}
}

func TestPathAllowlist_NoPolicyForAgentPass(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"other-agent": map[string]any{"deny": []string{"*.go"}},
	})
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Edit", "main.go", "lead"))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 (no policy for agent)", out.ExitCode)
	}
}

func TestPathAllowlist_DenyBypassActive(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"lead": map[string]any{"deny": []string{"*.env"}},
	})
	_ = os.WriteFile(filepath.Join(work, "hook-bypass.md"),
		[]byte("Hook: path-allowlist\nScope: .env\n"), 0644)
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Edit", ".env", "lead"))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 (bypass active)", out.ExitCode)
	}
}

func TestPathAllowlist_OutsideAllowBypassActive(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"backend": map[string]any{"allow": []string{"cli-go/*"}},
	})
	_ = os.WriteFile(filepath.Join(work, "hook-bypass.md"),
		[]byte("Hook: path-allowlist\nScope: lib/foo.sh\n"), 0644)
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Edit", "lib/foo.sh", "backend"))
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 (bypass active)", out.ExitCode)
	}
}

func TestPathAllowlist_LogEntryOnPass(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"backend": map[string]any{"allow": []string{"*.go"}},
	})
	h := makeHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Edit", "main.go", "backend"))
	rec := readLastLog(t, filepath.Join(work, "logs", "path-allowlist.ndjson"))
	if rec["severity"] != "REPORT" {
		t.Errorf("severity=%v", rec["severity"])
	}
}

func TestPathAllowlist_LogEntryOnBlock(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"lead": map[string]any{"deny": []string{"*.env"}},
	})
	h := makeHook(work, proj)
	_, _ = h.Run(context.Background(), makeInput("Edit", ".env", "lead"))
	rec := readLastLog(t, filepath.Join(work, "logs", "path-allowlist.ndjson"))
	if rec["severity"] != "BLOCK" {
		t.Errorf("severity=%v, want BLOCK", rec["severity"])
	}
}

func TestPathAllowlist_NoFilePathLogs(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"lead": map[string]any{"deny": []string{"*.env"}},
	})
	h := makeHook(work, proj)
	in := hooktype.HookInput{Tool: "Edit", Payload: map[string]any{}, Env: map[string]string{"YAKOS_AGENT_ROLE": "lead"}}
	out, _ := h.Run(context.Background(), in)
	if out.ExitCode != 0 {
		t.Errorf("exit code %d, want 0 (no file path)", out.ExitCode)
	}
}

func TestPathAllowlist_MultiEditSameAsEdit(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"backend": map[string]any{"deny": []string{"go.sum"}},
	})
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("MultiEdit", "go.sum", "backend"))
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 for MultiEdit deny", out.ExitCode)
	}
}

func TestPathAllowlist_HookName(t *testing.T) {
	h := pathallowlist.New("/tmp/work", "/tmp/proj")
	if h.Name() != "path-allowlist" {
		t.Errorf("Name()=%q", h.Name())
	}
}

func TestPathAllowlist_DefaultLeadRole(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"lead": map[string]any{"allow": []string{"docs/*"}},
	})
	h := makeHook(work, proj)
	// No YAKOS_AGENT_ROLE → defaults to "lead".
	in := hooktype.HookInput{
		Tool:    "Edit",
		Payload: map[string]any{"path": "cli-go/main.go"},
		Env:     map[string]string{},
	}
	out, _ := h.Run(context.Background(), in)
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 (lead outside allow)", out.ExitCode)
	}
}

func TestPathAllowlist_ProjectRelativeStripped(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	writeAllowlist(t, proj, map[string]any{
		"lead": map[string]any{"deny": []string{"secrets/key.pem"}},
	})
	h := makeHook(work, proj)
	absPath := filepath.Join(proj, "secrets", "key.pem")
	in := hooktype.HookInput{
		Tool:    "Write",
		Payload: map[string]any{"path": absPath},
		Env:     map[string]string{"YAKOS_AGENT_ROLE": "lead"},
	}
	out, _ := h.Run(context.Background(), in)
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 for absolute path matching deny glob", out.ExitCode)
	}
}

func TestPathAllowlist_DenyBeforeAllow(t *testing.T) {
	work := t.TempDir()
	proj := t.TempDir()
	// Both allow and deny match the same file; deny wins.
	writeAllowlist(t, proj, map[string]any{
		"lead": map[string]any{
			"allow": []string{"*.go"},
			"deny":  []string{"bad.go"},
		},
	})
	h := makeHook(work, proj)
	out, _ := h.Run(context.Background(), makeInput("Edit", "bad.go", "lead"))
	if out.ExitCode != 2 {
		t.Errorf("exit code %d, want 2 (deny before allow)", out.ExitCode)
	}
}
