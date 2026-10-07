package dispatch

import (
	"strings"

	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/runtime"
)

// envAliasSuffix is what stampEnvAlias adds to the router's route_reason.
const envAliasSuffix = "; " + runtime.RouteReasonEnvAlias

// stampEnvAlias records on req that a claude dispatch ran with router-policy
// class aliases in its environment (K-141). route_reason is owned by the
// router and always set; the stamp appends "; env-alias" to it (and sets
// "env-alias" when it is empty). policy_sha is the sha of the one trusted
// router-policy file, read through routerpolicy.FileSHA like the router's own;
// a value the router already set (the same file) is kept. yakOS logs what it
// set, not what Claude Code chose.
func stampEnvAlias(req *Request, runtimeName string) {
	if runtimeName != "claude" {
		return
	}
	if g := runtime.ClaudeGatewayAliases(); len(g.Set) == 0 {
		return
	}
	switch {
	case req.RouteReason == "":
		req.RouteReason = runtime.RouteReasonEnvAlias
	case !strings.HasSuffix(req.RouteReason, envAliasSuffix):
		req.RouteReason += envAliasSuffix
	}
	if req.PolicySHA == "" {
		req.PolicySHA = routerpolicy.FileSHA(routerpolicy.StateDir())
	}
}
