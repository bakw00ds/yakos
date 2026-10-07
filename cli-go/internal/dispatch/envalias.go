package dispatch

import "github.com/bakw00ds/yakos/internal/runtime"

// stampEnvAlias records on req that a claude dispatch ran with router-policy
// class aliases in its environment (K-141): route_reason=env-alias and the
// alias table's digest in policy_sha. yakOS logs what it set, not what Claude
// Code chose. It leaves a route record the router already wrote untouched.
func stampEnvAlias(req *Request, runtimeName string) {
	if runtimeName != "claude" || req.RouteReason != "" || req.PolicySHA != "" {
		return
	}
	g := runtime.ClaudeGatewayAliases()
	if len(g.Set) == 0 {
		return
	}
	req.RouteReason = runtime.RouteReasonEnvAlias
	req.PolicySHA = g.SHA
}
