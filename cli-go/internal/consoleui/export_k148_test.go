package consoleui

// export_k148_test.go: test-only exports for the routing helpers (K-148).

// BuildHandoffDigestForTest renders the digest of entries.
func BuildHandoffDigestForTest(entries []TranscriptEntry, from string) (string, int, int) {
	return buildHandoffDigest(entries, from)
}

// ScanSecretsForTest runs the secret scan.
func ScanSecretsForTest(s string) (string, int) { return scanSecrets(s) }

// ApplyRouteOverrideForTest applies the request's override and returns the
// runtime, model and pinned-by it leaves.
func ApplyRouteOverrideForTest(req DispatchRequest) (rt, model, pinned string, err error) {
	pinned, err = applyRouteOverride(&req)
	return req.Runtime, req.Model, pinned, err
}
func CapCardTextForTest(s string) (string, bool) { return capCardText(s) }
