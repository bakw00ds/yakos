package consolecmd_test

// testmain_test.go — package-level test setup for the consolecmd test binary.
//
// TestMain reduces argon2id cost parameters for the entire test binary so that
// tests involving userstore.Create complete quickly.  DO NOT call
// SetArgon2ParamsForTest inside individual tests — it mutates package-level
// globals and races when tests run in parallel.

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
