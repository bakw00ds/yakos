package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// shipTimeout is the maximum time allowed for a single POST to the endpoint.
// Kept short so the CLI never meaningfully blocks on a network call.
const shipTimeout = 5 * time.Second

// ShipPending reads un-shipped records from the local log, POSTs each to the
// configured endpoint, and marks them as shipped in the sidecar log.
//
// ShipPending is:
//   - A no-op when telemetry is disabled.
//   - A no-op when no endpoint is configured.
//   - Fail-silent: any error (network, parse, write) is swallowed.
//   - Non-blocking: the caller should invoke it in a goroutine when latency
//     matters.  The function itself uses a per-request context with a short
//     timeout.
//
// ShipPending must never be called with the request context from the parent
// CLI invocation.  Use context.Background() so telemetry shipping outlives
// the parent command's context.
func ShipPending(home string) {
	cfg, err := LoadConfig(home)
	if err != nil || !cfg.Enabled || cfg.Endpoint == "" {
		return
	}

	// Read all records.
	records, err := ReadRecentRecords(home, 0)
	if err != nil || len(records) == 0 {
		return
	}

	// Read already-shipped count to know where to start.
	shippedCount, err := countLines(shippedLogPath(home))
	if err != nil {
		return
	}

	if shippedCount >= len(records) {
		return // nothing new to ship
	}

	toShip := records[shippedCount:]

	client := &http.Client{Timeout: shipTimeout}

	for _, ev := range toShip {
		if err := postEvent(client, cfg.Endpoint, ev); err != nil {
			// Fail-silent: stop shipping on first failure to avoid hammering
			// a temporarily unreachable endpoint.
			return
		}
		// Mark as shipped by appending to the sidecar.
		_ = appendNDJSON(shippedLogPath(home), ev)
	}
}

// postEvent POSTs a single telemetry event to endpoint as JSON.
// Returns an error on non-2xx or network failure.
func postEvent(client *http.Client, endpoint string, ev Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), shipTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "yakos-telemetry/1")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("endpoint returned %d", resp.StatusCode)
	}
	return nil
}

// validateEndpoint performs a lightweight sanity check on a proposed endpoint
// URL.  It rejects empty strings and non-HTTP(S) schemes.
func validateEndpoint(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("endpoint URL must not be empty")
	}
	lower := strings.ToLower(rawURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("endpoint must be an http:// or https:// URL; got %q", rawURL)
	}
	return nil
}

// ReadShippedCount returns the number of records that have been successfully
// shipped to the configured endpoint.
func ReadShippedCount(home string) (int, error) {
	n, err := countLines(shippedLogPath(home))
	if os.IsNotExist(err) {
		return 0, nil
	}
	return n, err
}
