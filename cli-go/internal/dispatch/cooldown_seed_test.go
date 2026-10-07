package dispatch

import (
	"context"
	"testing"
	"time"
)

// seedCooldownForTest puts runtime into the router cooldown (three failed runs)
// on a fake clock. It is the one place the explain tests touch the cooldown's
// construction and noteRun's signature, so a change to either (K-139c) only
// edits this body.
func seedCooldownForTest(t *testing.T, project, runtime string, now func() time.Time) {
	t.Helper()
	routerCooldown = newCooldownSet(now)
	for i := 0; i < 3; i++ {
		noteRun(context.Background(), project, runtime, 1, nil)
	}
}
