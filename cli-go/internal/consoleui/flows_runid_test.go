package consoleui_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/netid"
	"github.com/bakw00ds/yakos/internal/workflow"
)

// K-86 item 2 (k82-security-review-2026-09-23.md K3): minted run IDs carried
// 24 random bits over a second-resolution timestamp, with a clock-derived
// fallback when crypto/rand failed.

var mintedRunIDRe = regexp.MustCompile(`^run-\d{8}-\d{6}-[0-9a-f]{32}$`)

func TestMintRunID_FormatAndEntropy(t *testing.T) {
	id, err := consoleui.MintRunIDForTest()
	if err != nil {
		t.Fatal(err)
	}
	if !mintedRunIDRe.MatchString(id) {
		t.Fatalf("run id %q does not match %s", id, mintedRunIDRe)
	}
	// Must stay valid for every existing consumer (path-safety pattern).
	if err := workflow.ValidateID("run_id", id); err != nil {
		t.Fatalf("minted id rejected by workflow.ValidateID: %v", err)
	}
	// Uniqueness within one second bucket: 24 bits would collide in ~4k draws
	// (birthday); 128 bits never will at this scale.
	seen := make(map[string]bool, 20000)
	for i := 0; i < 20000; i++ {
		id, err := consoleui.MintRunIDForTest()
		if err != nil {
			t.Fatal(err)
		}
		suffix := id[strings.LastIndex(id, "-")+1:]
		if seen[suffix] {
			t.Fatalf("random suffix collision after %d draws: %s", i, suffix)
		}
		seen[suffix] = true
	}
}

// The suffix must come from the crypto randomness source, all 16 bytes of it.
func TestMintRunID_UsesCryptoSource(t *testing.T) {
	var asked int
	restore := consoleui.SetCryptoReadForTest(func(b []byte) (int, error) {
		asked = len(b)
		for i := range b {
			b[i] = 0xab
		}
		return len(b), nil
	})
	defer restore()
	id, err := consoleui.MintRunIDForTest()
	if err != nil {
		t.Fatal(err)
	}
	if asked != 16 {
		t.Errorf("requested %d random bytes; want 16 (128 bits)", asked)
	}
	if !strings.HasSuffix(id, "-"+strings.Repeat("ab", 16)) {
		t.Errorf("run id %q suffix not derived from the injected crypto source", id)
	}
}

// A randomness failure must be an error, never a clock-derived weak ID.
func TestMintRunID_RandFailure_NoWeakFallback(t *testing.T) {
	restore := consoleui.SetCryptoReadForTest(func(b []byte) (int, error) {
		return 0, errors.New("entropy exhausted")
	})
	defer restore()
	id, err := consoleui.MintRunIDForTest()
	if err == nil {
		t.Fatalf("mintRunID returned %q with a failing entropy source; want an error", id)
	}
	if id != "" {
		t.Errorf("mintRunID returned non-empty id %q alongside error", id)
	}
}

// End to end: with no entropy, run and resume refuse (500) and create no run.
func TestFlows_RunAndResume_RandFailure_FailClosed(t *testing.T) {
	fn := func(ctx context.Context, _ dispatch.Params) ([]byte, dispatch.Result, error) {
		t.Error("node dispatch must not run when a run id cannot be minted")
		return nil, dispatch.Result{}, nil
	}
	workDir, doAs := newProductionEngineTestServer(t, fn)
	writeWorkflow(t, workDir, "my-flow", minimalYAML)
	id := netid.Identity{OperatorID: "mallory", Role: netid.RoleAdmin, Authenticated: true, Resolved: true}

	restore := consoleui.SetCryptoReadForTest(func(b []byte) (int, error) {
		return 0, errors.New("entropy exhausted")
	})
	defer restore()

	resp := doAs(id, http.MethodPost, "/flows/api/run?name=my-flow", `{}`)
	if body := bodyStr(t, resp); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("run: status=%d; want 500; body=%s", resp.StatusCode, body)
	}
	resp = doAs(id, http.MethodPost, "/flows/api/resume", `{"run_id":"run-19700101-000000-aaaaaaaa"}`)
	if body := bodyStr(t, resp); resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("resume: status=%d; want 500; body=%s", resp.StatusCode, body)
	}
	entries, _ := os.ReadDir(filepath.Join(workDir, "workflows", "runs"))
	if len(entries) != 0 {
		t.Errorf("run directories created despite entropy failure: %v", entries)
	}
}
