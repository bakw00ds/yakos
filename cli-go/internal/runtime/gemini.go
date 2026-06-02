package runtime

import (
	"context"
	"fmt"
	"os"
	"time"
)

// removalDate is the date after which the gemini shim hard-fails.
// Mirrors gemini.sh:_YK_RT_GEMINI_REMOVAL_DATE.
const removalDate = "2026-09-01"

// GeminiAdapter is a deprecation shim. It delegates to AgyAdapter and emits a
// deprecation warning. After removalDate it returns an error on every call.
//
// This matches the behavior of cli/lib/runtimes/gemini.sh: the 'id()' stays
// "gemini" so existing configs continue to resolve, but every verb goes through
// agy. Removal date: 2026-09-01.
//
// To migrate:
//   - .yakos.yml: set `default-runtime: agy`
//   - Agent frontmatter: change `runtime: gemini` to `runtime: agy`
type GeminiAdapter struct {
	warned bool
	agy    AgyAdapter
}

func (a *GeminiAdapter) Name() string { return "gemini" }

func (a *GeminiAdapter) Available(ctx context.Context) bool {
	if err := a.checkRemoval(); err != nil {
		return false
	}
	a.warnOnce()
	return a.agy.Available(ctx)
}

func (a *GeminiAdapter) Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error) {
	if err := a.checkRemoval(); err != nil {
		return nil, err
	}
	a.warnOnce()
	return a.agy.Dispatch(ctx, req)
}

func (a *GeminiAdapter) checkRemoval() error {
	today := time.Now().UTC().Format("2006-01-02")
	if today >= removalDate {
		return fmt.Errorf(
			"runtime gemini: deprecation shim disabled past removal date %s; "+
				"Gemini CLI stopped serving requests on 2026-06-18; agy is the successor; "+
				"set 'default-runtime: agy' in .yakos.yml and update agent frontmatter",
			removalDate,
		)
	}
	return nil
}

func (a *GeminiAdapter) warnOnce() {
	if a.warned {
		return
	}
	a.warned = true
	// The warning goes to stderr. We use fmt.Fprintf since adapters don't have
	// a logger. The dispatch orchestrator passes stderr through to the terminal.
	fmt.Fprintf(os.Stderr, "NOTE: --runtime gemini is deprecated. Gemini CLI stops serving requests 2026-06-18.\n"+
		"  Routing to 'agy' (Antigravity CLI; successor). Update to 'agy' before "+removalDate+".\n")
}
