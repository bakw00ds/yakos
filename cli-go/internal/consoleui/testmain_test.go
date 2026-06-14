package consoleui_test

// testmain_test.go — package-level test setup for the consoleui test binary.
//
// TestMain reduces argon2id cost parameters for the entire test binary so that
// tests that call userstore.Create (argon2id hash) complete in milliseconds
// rather than seconds.  This mirrors the pattern used in userstore_test.go.
//
// The reduced params are (t=1, m=64KiB, p=1) — still a valid argon2id
// derivation, just not production-strength.  VerifyPassword parses params from
// the stored PHC, so round-trip tests are unaffected.
//
// DO NOT call SetArgon2ParamsForTest inside individual tests — it mutates
// package-level globals and races when tests run in parallel.  Set them once
// here.

import (
	"os"
	"testing"

	"github.com/bakw00ds/yakos/internal/userstore"
)

func TestMain(m *testing.M) {
	restore := userstore.SetArgon2ParamsForTest(1, 64, 1)
	defer restore()
	os.Exit(m.Run())
}
