// Package daemonclient implements the CLI-side half of the CLI↔daemon build
// handshake: querying a running daemon's build identity over the JSON-RPC
// socket and comparing it to the calling binary's own buildinfo.BuildID().
//
// This is the single shared implementation every CLI connect site (the
// version-mismatch check in `yakos start`, the generic daemon-routing path,
// and `yakos events`) uses, so the comparison logic — and its failure modes —
// live in exactly one place.
package daemonclient

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bakw00ds/yakos/internal/jsonrpc"
)

// VersionInfo mirrors the yakos.version JSON-RPC result (and the REST
// GET /v1/version and gRPC Version.Get responses, which share this shape).
//
// Version is the pre-existing legacy display string (internal/version.Read's
// output) and is kept first and byte-for-byte unchanged so an older CLI
// talking to a newer daemon still parses this response — see
// internal/serve/methods.go's handleVersion doc comment. Commit, LibHash,
// and BuildID are additive fields; an older CLI that only reads Version
// simply ignores them.
type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	LibHash string `json:"lib_hash"`
	BuildID string `json:"build_id"`
}

// ErrStaleDaemon is returned by Check when the connected daemon's build id
// does not match the caller's, or when the daemon's build id could not be
// determined (empty — an old, pre-handshake daemon, or an unreachable
// version query with no more specific error to report).  It carries both
// build ids so callers can compose an actionable message.
type ErrStaleDaemon struct {
	// DaemonBuildID is the running daemon's reported build id, or "" when
	// unknown (pre-handshake daemon, or the version query failed softly).
	DaemonBuildID string
	// WantBuildID is the calling binary's own buildinfo.BuildID().
	WantBuildID string
}

func (e *ErrStaleDaemon) Error() string {
	if e.DaemonBuildID == "" {
		return fmt.Sprintf("daemonclient: daemon build id unknown (want %s)", e.WantBuildID)
	}
	return fmt.Sprintf("daemonclient: daemon build mismatch: daemon=%s want=%s", e.DaemonBuildID, e.WantBuildID)
}

// QueryVersion calls yakos.version over c and decodes the result.
//
// A JSON-RPC transport error or an undecodable response is returned as a
// plain wrapped error (not *ErrStaleDaemon) — those are query failures, not
// a build-identity determination. A successfully decoded response with an
// empty BuildID (an old daemon that predates this field) is NOT an error
// here; the caller (Check) is what decides an empty BuildID means "stale".
func QueryVersion(ctx context.Context, c *jsonrpc.Client) (VersionInfo, error) {
	raw, err := c.Call(ctx, "yakos.version", nil)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("daemonclient: yakos.version: %w", err)
	}
	var info VersionInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return VersionInfo{}, fmt.Errorf("daemonclient: decoding yakos.version response: %w", err)
	}
	return info, nil
}

// Check queries the daemon's build id over c and compares it to want (the
// caller's own buildinfo.BuildID()).
//
// Returns nil when the daemon's reported BuildID matches want exactly.
// Returns *ErrStaleDaemon when the daemon is reachable but its BuildID is
// empty or differs from want. Returns a plain (non-ErrStaleDaemon) error
// when the query itself failed (RPC error, malformed response) — that is a
// connectivity/protocol failure, not a build-identity determination, and
// callers should not print it as a mismatch.
func Check(ctx context.Context, c *jsonrpc.Client, want string) error {
	info, err := QueryVersion(ctx, c)
	if err != nil {
		return err
	}
	if info.BuildID == "" || info.BuildID != want {
		return &ErrStaleDaemon{DaemonBuildID: info.BuildID, WantBuildID: want}
	}
	return nil
}
