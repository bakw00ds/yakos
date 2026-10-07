package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/bakw00ds/yakos/internal/router"
)

// seedCooldownForTest puts runtime into the router cooldown (three failed runs)
// on a fake clock. It is the one place the explain tests touch the cooldown's
// construction and noteRun's signature, so a change to either (K-139c) only
// edits this body.
func seedCooldownForTest(t *testing.T, project, runtime string, now func() time.Time) {
	t.Helper()
	_ = project // the cooldown is process-wide until K-139c scopes it per project
	routerCooldown = router.NewCooldown(now)
	for i := 0; i < 3; i++ {
		noteRun(context.Background(), runtime, 1, nil)
	}
}
