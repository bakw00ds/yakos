package serve

// openai_gateway.go: starts the OpenAI-compatible endpoint (K-150) when the
// operator turned it on, either with `yakos serve --openai-endpoint` or with
// `openai_endpoint: true` in the trusted ~/.yakos-state/router-policy.yml. A
// project .yakos.yml cannot enable it. Off otherwise: nothing binds.

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/bakw00ds/yakos/internal/consoleui"
	"github.com/bakw00ds/yakos/internal/dispatch"
	"github.com/bakw00ds/yakos/internal/gateway/openai"
	"github.com/bakw00ds/yakos/internal/perfdash"
	"github.com/bakw00ds/yakos/internal/routerpolicy"
	"github.com/bakw00ds/yakos/internal/statepath"
)

// openAIEndpointEnabled says whether the endpoint should start: the flag, or the
// trusted user policy under policyDir ("" means the real state dir). A policy file
// that is untrusted or unreadable counts as off, with a warning that names no path.
func openAIEndpointEnabled(flag bool, policyDir string) bool {
	if flag {
		return true
	}
	if policyDir == "" {
		policyDir = routerpolicy.StateDir()
	}
	p, err := routerpolicy.Load(policyDir)
	if err != nil {
		slog.Warn("serve: router policy not read; the OpenAI-compatible endpoint stays off")
		return false
	}
	return p.OpenAIEndpoint()
}

// startOpenAIGateway binds the endpoint and serves it in the background. A failed
// bind is a loud warning and the daemon continues without it (the same rule as
// the MCP listener: the port is fixed, so a squatter must be visible).
func startOpenAIGateway(ctx context.Context, cfg Config, svc *dispatch.Service, errCh chan error) error {
	// The endpoint has its own token (K-174), not the REST write token. It is
	// minted on first start and then kept; `yakos serve --rotate-openai-token`
	// replaces it, and the endpoint reads the file per request.
	tokenDir := cfg.OpenAITokenDir
	if tokenDir == "" {
		tokenDir = statepath.Dir()
	}
	if _, err := openai.LoadOrCreateToken(tokenDir); err != nil {
		msg := fmt.Sprintf("the OpenAI-compatible endpoint token could not be prepared: %v; the endpoint is DISABLED for this run", err)
		slog.Error("serve: " + msg)
		fmt.Fprintln(os.Stderr, "yakos serve: WARNING: "+msg)
		close(errCh)
		return nil
	}
	srv, err := openai.New(openai.Config{
		Addr:        cfg.OpenAIAddr,
		Token:       openai.FileToken(tokenDir),
		Service:     svc,
		Transcripts: consoleui.NewTranscripts(perfdash.DefaultWorkDir(cfg.WorkspaceRoot)),
		YakosRoot:   cfg.YakosRoot,
		Workspace:   cfg.WorkspaceRoot,
	})
	if err != nil {
		close(errCh)
		return fmt.Errorf("serve: openai endpoint: %w", err)
	}
	ln, err := srv.Listen()
	if err != nil {
		msg := fmt.Sprintf("the OpenAI-compatible endpoint could not bind: %v. Clients configured for that address may be talking to another process that holds the port and could capture their bearer token; the endpoint is DISABLED for this run", err)
		slog.Error("serve: " + msg)
		fmt.Fprintln(os.Stderr, "yakos serve: WARNING: "+msg)
		close(errCh)
		return nil
	}
	fmt.Fprintf(os.Stderr, "yakos serve: openai-compatible endpoint: http://%s/v1 (bearer: the token in %s)\n", ln.Addr(), openai.TokenPath(tokenDir))
	go func() { errCh <- srv.ServeListener(ctx, ln) }()
	return nil
}
