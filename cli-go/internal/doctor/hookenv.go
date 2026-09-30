package doctor

import (
	"strconv"
	"strings"
)

// Bounds and default of YAKOS_HOOK_JQ_TIMEOUT. They mirror _hi_jq_limit_parse
// in lib/hooks/lib/hook-input.sh: whole seconds, base 10, clamped to 1..25,
// default 5. The ceiling stays below the 30 s timeout `yakos refresh` writes
// into settings.json so a blocking hook can still block.
const (
	hookJQTimeoutDefault = 5
	hookJQTimeoutMin     = 1
	hookJQTimeoutMax     = 25
)

// checkHookEnv reports the hook-runtime environment knobs that change hook
// behavior, so an operator can see what the hooks will actually use.
func (r *runner) checkHookEnv() {
	writeln(r, "Hook environment")
	raw := strings.TrimSpace(r.env("YAKOS_HOOK_JQ_TIMEOUT"))
	if raw == "" {
		r.info(SectionHookEnv, "YAKOS_HOOK_JQ_TIMEOUT: unset (jq is bounded at %d s; set whole seconds %d..%d to change)",
			hookJQTimeoutDefault, hookJQTimeoutMin, hookJQTimeoutMax)
		writeln(r, "")
		return
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	switch {
	case err != nil && isAllDigits(raw):
		// Longer than a uint64: hooks clamp to the ceiling.
		r.info(SectionHookEnv, "YAKOS_HOOK_JQ_TIMEOUT=%s: clamped to %d s", raw, hookJQTimeoutMax)
	case err != nil:
		r.warn(SectionHookEnv, "YAKOS_HOOK_JQ_TIMEOUT=%q is not a whole number of seconds; hooks fall back to %d s",
			raw, hookJQTimeoutDefault)
	case n < hookJQTimeoutMin:
		r.info(SectionHookEnv, "YAKOS_HOOK_JQ_TIMEOUT=%s: clamped to %d s", raw, hookJQTimeoutMin)
	case n > hookJQTimeoutMax:
		r.info(SectionHookEnv, "YAKOS_HOOK_JQ_TIMEOUT=%s: clamped to %d s", raw, hookJQTimeoutMax)
	default:
		r.ok(SectionHookEnv, "YAKOS_HOOK_JQ_TIMEOUT=%d s", n)
	}
	writeln(r, "")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
