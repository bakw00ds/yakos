// Package buildinfo provides a build identity that actually distinguishes
// one compiled yakos binary from another — unlike internal/version, whose
// VERSION-file-derived string is identical across every binary built from
// the same commit-less source tree (see the CLI↔daemon handshake design at
// work/current/reports/s6-structural-plan-2026-09-23.md §4.1).
//
// Version and Commit are set at release-build time via:
//
//	-X github.com/bakw00ds/yakos/internal/buildinfo.Version=<VERSION file contents>
//	-X github.com/bakw00ds/yakos/internal/buildinfo.Commit=<git rev-parse --short=12 HEAD>
//
// A bare `go build` (no -X flags — dev builds, `go test`, a source tarball
// with no git metadata) leaves both empty; BuildID falls back to "dev" for
// each so the identifier is still well-formed and still distinguishes
// lib-only changes via the LibHash component.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sync"

	"github.com/bakw00ds/yakos/internal/framework"
)

// Version is the release version string, injected via -ldflags at build
// time.  Empty on dev builds.
var Version = ""

// Commit is the short (12-hex-char) git commit SHA, injected via -ldflags
// at build time.  Empty on dev builds or source checkouts with no git
// metadata available at build time.
var Commit = ""

var (
	libHashOnce sync.Once
	libHashVal  string
)

// LibHash returns a deterministic, memoized sha256 (hex-encoded) over the
// embedded framework filesystem's file paths and contents.
//
// fs.WalkDir visits entries in lexical order — a documented guarantee, not
// an implementation detail — so this hash is stable across repeated calls
// and across processes built from the same embedded content, independent of
// any directory-iteration order. Per rule:cache-stability, no volatile
// input (timestamps, PIDs, run-ids) is mixed in.
func LibHash() string {
	libHashOnce.Do(func() {
		h := sha256.New()
		libFS := framework.LibFS()
		_ = fs.WalkDir(libFS, "embedded", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			data, rerr := fs.ReadFile(libFS, path)
			if rerr != nil {
				return rerr
			}
			h.Write([]byte(path))
			h.Write([]byte{0})
			h.Write(data)
			return nil
		})
		libHashVal = hex.EncodeToString(h.Sum(nil))
	})
	return libHashVal
}

// BuildID returns a stable identifier composed of Version, Commit, and a
// 12-hex-char prefix of LibHash, joined with "+":
//
//	0.57.0.0+a1b2c3d4e5f6+9f8e7d6c5b4a
//
// This is the value CLI and daemon compare to detect a stale daemon: two
// binaries built from different commits (even at the same VERSION) produce
// different BuildIDs, and a lib-only change (no Go recompile) is still
// caught via the LibHash component.
//
// Empty Version/Commit (dev builds) fall back to the literal "dev" so the
// ID stays well-formed; two dev builds with different embedded lib content
// still compare unequal via the hash component.
func BuildID() string {
	v := Version
	if v == "" {
		v = "dev"
	}
	c := Commit
	if c == "" {
		c = "dev"
	}
	lh := LibHash()
	if len(lh) > 12 {
		lh = lh[:12]
	}
	return v + "+" + c + "+" + lh
}
