package serve

// anthropic_gateway.go: starts the Anthropic pass-through gateway (K-151) when
// the operator turned it on, with `yakos serve --gateway` or `anthropic_gateway:
// true` in the trusted ~/.yakos-state/router-policy.yml. A project .yakos.yml
// cannot enable it. Off otherwise: nothing binds. The gateway checks a yakOS
// gateway token (~/.yakos-state/gateway-token, minted fresh here on every start) on
// every request; the only other secret it touches is the operator's
// ANTHROPIC_API_KEY, read from the daemon's environment here and handed to the
// proxy as a value. The bound address is reported over the daemon's owner-only
// socket (yakos.version) so `yakos start --routed` can prove the listener is
// this daemon's before it sends anything.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/bakw00ds/yakos/internal/gateway/anthropic"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// anthropicGatewayEnabled says whether the gateway should start: the flag, or
// the trusted user policy under policyDir ("" means the real state dir).
func anthropicGatewayEnabled(flag bool, policyDir string) bool {
	if flag {
		return true
	}
	if policyDir == "" {
		policyDir = routerpolicy.StateDir()
	}
	p, err := routerpolicy.Load(policyDir)
	if err != nil {
		slog.Warn("serve: router policy not read; the Anthropic gateway stays off")
		return false
	}
	return p.AnthropicGateway()
}

// classTable reads gateway_classes from the trusted policy at most every few
// seconds, so a `yakos router policy` edit applies without a restart.
func classTable(policyDir string) func() routerpolicy.GatewayClasses {
	var (
		mu   sync.Mutex
		at   time.Time
		last routerpolicy.GatewayClasses
	)
	return func() routerpolicy.GatewayClasses {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && time.Since(at) < 3*time.Second {
			return last
		}
		dir := policyDir
		if dir == "" {
			dir = routerpolicy.StateDir()
		}
		at, last = time.Now(), nil
		if f, err := routerpolicy.Load(dir); err == nil {
			last, _ = f.Classes()
		}
		return last
	}
}

// startAnthropicGateway binds the gateway and serves it in the background. A
// failed bind is a loud warning and the daemon continues without it.
func startAnthropicGateway(ctx context.Context, cfg Config, errCh chan error) (string, error) {
	srv, err := anthropic.New(anthropic.Config{
		DeferToken:              true,
		Addr:                    cfg.GatewayAddr,
		PassthroughSubscription: cfg.GatewayPassthroughSubscription,
		APIKey:                  os.Getenv("ANTHROPIC_API_KEY"),
		Classes:                 classTable(cfg.GatewayPolicyDir),
	})
	if err != nil {
		close(errCh)
		return "", fmt.Errorf("serve: anthropic gateway: %w", err)
	}
	ln, err := srv.Listen()
	if err != nil {
		msg := fmt.Sprintf("the Anthropic gateway could not bind: %v. Claude Code launched with --routed may be talking to another process that holds the port and could capture its API key; the gateway is DISABLED for this run", err)
		slog.Error("serve: " + msg)
		fmt.Fprintln(os.Stderr, "yakos serve: WARNING: "+msg)
		close(errCh)
		return "", nil
	}
	// Rotate only now that the port is ours: a daemon that lost the bind must
	// not invalidate the token of the gateway that holds it.
	tok, err := anthropic.RotateToken(statepath.Dir())
	if err != nil {
		_ = ln.Close()
		close(errCh)
		return "", fmt.Errorf("serve: anthropic gateway: %w", err)
	}
	srv.SetToken(tok)
	fmt.Fprintf(os.Stderr, "yakos serve: anthropic gateway: http://%s (launch claude through it with the routed flag of yakos start)\n", ln.Addr())
	if cfg.GatewayPassthroughSubscription {
		fmt.Fprintln(os.Stderr, "yakos serve: WARNING: --gateway-passthrough-subscription: subscription OAuth tokens are forwarded through the gateway (see ADR-0011)")
	}
	go func() { errCh <- srv.ServeListener(ctx, ln) }()
	return ln.Addr().String(), nil
}
