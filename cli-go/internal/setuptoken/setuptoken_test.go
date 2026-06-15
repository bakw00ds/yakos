package setuptoken_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/setuptoken"
)

// ---- Generate / Validate / Consume ------------------------------------------

func TestGenerate_ProducesToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	tok, err := st.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if tok == "" {
		t.Fatal("Generate returned empty token")
	}
}

func TestGenerate_WritesMarkerFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "setup-token")
	st := setuptoken.New(filePath, nil)
	_, err := st.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	fi, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("marker file missing: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("marker file perms = %o, want 0600", fi.Mode().Perm())
	}
}

func TestValidate_ValidToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	tok, _ := st.Generate()
	if !st.Validate(tok) {
		t.Error("Validate: valid token should return true")
	}
}

func TestValidate_WrongToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	st.Generate() //nolint:errcheck
	if st.Validate("wrong-token") {
		t.Error("Validate: wrong token should return false")
	}
}

func TestValidate_EmptyToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	st.Generate() //nolint:errcheck
	if st.Validate("") {
		t.Error("Validate: empty token should return false")
	}
}

func TestValidate_ExpiredToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// We use two separate State instances sharing the same file path.
	// st1 generates with a clock 31 minutes in the past; st2 validates with
	// the current real time, so the token appears expired to st2.
	past := time.Now().Add(-(setuptoken.TokenTTL + time.Minute))
	filePath := filepath.Join(dir, "setup-token")

	st1 := setuptoken.New(filePath, func() time.Time { return past })
	tok, err := st1.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// st2 uses real-time clock — the generated token's issuedAt is in the past
	// so it should be expired.
	st2 := setuptoken.New(filePath, nil)
	// Load the persisted token into st2's memory.
	_, err = st2.LoadOrGenerate()
	// LoadOrGenerate itself may generate a new token if it finds the file expired;
	// what matters is that the token st1 generated is no longer valid in st2.
	// But actually: LoadOrGenerate with an expired file generates a NEW token,
	// so tok (from st1) won't match st2's new token either.
	// Either way, tok should not validate.
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}

	// The original token from st1 must not validate on st2 (either it expired or
	// st2 generated a different token).
	if st2.Validate(tok) {
		t.Error("Validate: expired/replaced token should return false")
	}
}

func TestConsume_InvalidatesToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "setup-token")
	st := setuptoken.New(filePath, nil)
	tok, _ := st.Generate()

	st.Consume()

	// Token should no longer validate.
	if st.Validate(tok) {
		t.Error("Validate: consumed token should return false")
	}
}

func TestConsume_DeletesMarkerFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "setup-token")
	st := setuptoken.New(filePath, nil)
	st.Generate() //nolint:errcheck

	st.Consume()

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("Consume: marker file should be deleted")
	}
}

func TestConsume_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	st.Generate() //nolint:errcheck
	// Double-consume must not panic.
	st.Consume()
	st.Consume()
}

// ---- Replay / single-use ----------------------------------------------------

func TestValidate_ReplayAfterConsume(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	tok, _ := st.Generate()

	// First validation succeeds.
	if !st.Validate(tok) {
		t.Fatal("pre-consume: token should validate")
	}
	// Consume (simulating successful setup).
	st.Consume()
	// Replay the same token value → must fail.
	if st.Validate(tok) {
		t.Error("Validate: replayed/consumed token should return false")
	}
}

// ---- IsActive ---------------------------------------------------------------

func TestIsActive_BeforeGenerate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	if st.IsActive() {
		t.Error("IsActive: should be false before any Generate call")
	}
}

func TestIsActive_AfterGenerate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	st.Generate() //nolint:errcheck
	if !st.IsActive() {
		t.Error("IsActive: should be true after Generate")
	}
}

func TestIsActive_AfterConsume(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	st.Generate() //nolint:errcheck
	st.Consume()
	if st.IsActive() {
		t.Error("IsActive: should be false after Consume")
	}
}

// ---- LoadOrGenerate (restart survival) --------------------------------------

func TestLoadOrGenerate_ReusesValidToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "setup-token")

	// First call: generate.
	st1 := setuptoken.New(filePath, nil)
	tok1, err := st1.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Simulate daemon restart: create a new State pointing to the same file.
	st2 := setuptoken.New(filePath, nil)
	tok2, err := st2.LoadOrGenerate()
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}

	if tok1 != tok2 {
		t.Errorf("LoadOrGenerate after restart: got different token (want=%q, got=%q)", tok1, tok2)
	}
}

func TestLoadOrGenerate_GeneratesFreshWhenExpired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "setup-token")

	// Write a file with an expired issuedAt (31 min ago).
	past := time.Now().Add(-(setuptoken.TokenTTL + time.Minute))
	st1 := setuptoken.New(filePath, func() time.Time { return past })
	tok1, err := st1.Generate()
	if err != nil {
		t.Fatalf("Generate (past): %v", err)
	}

	// LoadOrGenerate with real time: the persisted token is expired → fresh one.
	st2 := setuptoken.New(filePath, nil)
	tok2, err := st2.LoadOrGenerate()
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}

	if tok1 == tok2 {
		t.Error("LoadOrGenerate: expected a fresh token when persisted one is expired")
	}
}

func TestLoadOrGenerate_WhenNoFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// File does not exist: LoadOrGenerate should generate a fresh token.
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	tok, err := st.LoadOrGenerate()
	if err != nil {
		t.Fatalf("LoadOrGenerate: %v", err)
	}
	if tok == "" {
		t.Error("LoadOrGenerate: expected non-empty token")
	}
}

// ---- Concurrency smoke test -------------------------------------------------

func TestGenerate_ConcurrentCallsSafe(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st := setuptoken.New(filepath.Join(dir, "setup-token"), nil)
	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func() {
			st.Generate() //nolint:errcheck
			st.Validate("some-token")
			st.IsActive()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
