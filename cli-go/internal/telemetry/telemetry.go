// Package telemetry implements `yakos telemetry` and the opt-in recording API.
//
// All recording is gated behind an explicit operator opt-in.  No data is
// collected or transmitted by default.
//
// # Sub-subcommands
//
//	yakos telemetry enable [--endpoint URL]   enable recording; optional endpoint
//	yakos telemetry disable                   disable recording
//	yakos telemetry status                    print enabled?, endpoint, counts
//	yakos telemetry set-endpoint <url>        set/change endpoint (enables if off)
//	yakos telemetry purge                     delete the local NDJSON log
//	yakos telemetry show [--limit N]          print last N records
//
// See README.md for the full schema, privacy guarantees, and opt-in flow.
package telemetry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// SubCmdConfig carries everything Run needs.
type SubCmdConfig struct {
	// Subcommand is one of: enable, disable, status, set-endpoint, purge, show.
	Subcommand string

	// Endpoint is the optional --endpoint URL argument for enable / set-endpoint.
	Endpoint string

	// ShowLimit is the --limit N argument for show (0 = all).
	ShowLimit int

	// HomeDir overrides os.Getenv("HOME").  When empty os.Getenv("HOME") is used.
	HomeDir string

	// Writer receives normal output (default: os.Stdout).
	Writer io.Writer

	// ErrWriter receives warning/advisory messages (default: os.Stderr).
	ErrWriter io.Writer
}

// SubCmdResult summarises what Run did.
type SubCmdResult struct {
	// Subcommand executed.
	Subcommand string

	// Enabled is the post-operation enabled state.
	Enabled bool

	// Endpoint is the post-operation endpoint (may be "").
	Endpoint string

	// TotalRecords is the local record count (status / show only).
	TotalRecords int

	// ShippedRecords is the shipped record count (status only).
	ShippedRecords int

	// Records are the events returned by show.
	Records []Event
}

// Run executes the telemetry sub-subcommand described by cfg.
func Run(cfg SubCmdConfig) (*SubCmdResult, error) {
	home := cfg.HomeDir
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}

	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	ew := cfg.ErrWriter
	if ew == nil {
		ew = os.Stderr
	}

	res := &SubCmdResult{Subcommand: cfg.Subcommand}

	switch cfg.Subcommand {
	case "enable":
		return runEnable(cfg, res, home, w, ew)
	case "disable":
		return runDisable(res, home, w)
	case "status":
		return runStatus(res, home, w)
	case "set-endpoint":
		return runSetEndpoint(cfg, res, home, w, ew)
	case "purge":
		return runPurge(res, home, w, ew)
	case "show":
		return runShow(cfg, res, home, w)
	case "help", "--help", "-h", "":
		PrintHelp(w)
		return res, nil
	default:
		return nil, fmt.Errorf("unknown telemetry subcommand %q (try 'yakos telemetry help')", cfg.Subcommand)
	}
}

// ParseArgs parses os.Args-style arguments for `yakos telemetry <args...>`
// and returns a populated SubCmdConfig.  It does not call os.Exit; callers
// handle the returned error.
func ParseArgs(args []string, homeDir string) (SubCmdConfig, error) {
	cfg := SubCmdConfig{HomeDir: homeDir}

	if len(args) == 0 {
		cfg.Subcommand = "help"
		return cfg, nil
	}

	cfg.Subcommand = args[0]
	rest := args[1:]

	switch cfg.Subcommand {
	case "enable":
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--endpoint":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("telemetry enable: --endpoint requires a URL")
				}
				cfg.Endpoint = rest[i]
			case strings.HasPrefix(rest[i], "--endpoint="):
				cfg.Endpoint = rest[i][len("--endpoint="):]
			default:
				return cfg, fmt.Errorf("telemetry enable: unknown flag %q", rest[i])
			}
		}

	case "set-endpoint":
		if len(rest) == 0 {
			return cfg, fmt.Errorf("telemetry set-endpoint: URL required")
		}
		cfg.Endpoint = rest[0]

	case "show":
		for i := 0; i < len(rest); i++ {
			switch {
			case rest[i] == "--limit":
				i++
				if i >= len(rest) {
					return cfg, fmt.Errorf("telemetry show: --limit requires a number")
				}
				n, err := strconv.Atoi(rest[i])
				if err != nil || n < 0 {
					return cfg, fmt.Errorf("telemetry show: --limit must be a non-negative integer")
				}
				cfg.ShowLimit = n
			case strings.HasPrefix(rest[i], "--limit="):
				n, err := strconv.Atoi(rest[i][len("--limit="):])
				if err != nil || n < 0 {
					return cfg, fmt.Errorf("telemetry show: --limit must be a non-negative integer")
				}
				cfg.ShowLimit = n
			default:
				return cfg, fmt.Errorf("telemetry show: unknown flag %q", rest[i])
			}
		}

	case "disable", "status", "purge", "help", "--help", "-h":
		// no additional flags

	default:
		// Unknown subcommand — let Run handle the error message.
	}

	return cfg, nil
}

// PrintHelp writes the telemetry help text to w.
func PrintHelp(w io.Writer) {
	_, _ = fmt.Fprint(w, `yakos telemetry — opt-in anonymised command telemetry

Default: DISABLED. No data is collected or transmitted without explicit opt-in.

Sub-subcommands:
  enable [--endpoint URL]    Enable recording; optional HTTP(S) endpoint for shipping.
  disable                    Disable recording. Future invocations do not record.
  status                     Print configuration: enabled?, endpoint, record counts.
  set-endpoint <url>         Set the shipping endpoint (also enables if currently off).
  purge                      Delete the local telemetry.ndjson log.
  show [--limit N]           Print last N records (default: all).

What is recorded (when enabled):
  - Timestamp, yakos version, OS, CPU arch
  - Top-level subcommand + second-level subcommand (if applicable)
  - Exit code, wall-clock duration (ms)
  - Agent count and runtime name (for dispatch commands)
  - SHA-256 of CLAUDE_SESSION_ID (irreversible)

What is NEVER recorded:
  - File paths, file contents, project names
  - Usernames, hostnames, or any PII
  - Tool inputs or outputs

Local log: ~/.yakos-state/telemetry.ndjson
Config:    ~/.yakos-state/telemetry.yml
`)
}

// ---- subcommand implementations ---------------------------------------------

func runEnable(cfg SubCmdConfig, res *SubCmdResult, home string, w, ew io.Writer) (*SubCmdResult, error) {
	if cfg.Endpoint != "" {
		if err := validateEndpoint(cfg.Endpoint); err != nil {
			return nil, fmt.Errorf("telemetry enable: %w", err)
		}
	}

	existing, err := LoadConfig(home)
	if err != nil {
		existing = Config{}
	}

	existing.Enabled = true
	if cfg.Endpoint != "" {
		existing.Endpoint = cfg.Endpoint
	}

	if err := SaveConfig(home, existing); err != nil {
		return nil, fmt.Errorf("telemetry enable: save config: %w", err)
	}

	_, _ = fmt.Fprintln(w, "telemetry: ENABLED")
	if existing.Endpoint != "" {
		_, _ = fmt.Fprintf(w, "  endpoint: %s\n", existing.Endpoint)
	} else {
		_, _ = fmt.Fprintln(w, "  mode: local-only (no endpoint configured)")
		_, _ = fmt.Fprintln(w, "  use 'yakos telemetry set-endpoint <url>' to add an endpoint")
	}

	res.Enabled = true
	res.Endpoint = existing.Endpoint
	return res, nil
}

func runDisable(res *SubCmdResult, home string, w io.Writer) (*SubCmdResult, error) {
	existing, err := LoadConfig(home)
	if err != nil {
		existing = Config{}
	}

	existing.Enabled = false

	if err := SaveConfig(home, existing); err != nil {
		return nil, fmt.Errorf("telemetry disable: save config: %w", err)
	}

	_, _ = fmt.Fprintln(w, "telemetry: DISABLED")
	_, _ = fmt.Fprintln(w, "  No data will be recorded or transmitted on future invocations.")

	res.Enabled = false
	res.Endpoint = existing.Endpoint
	return res, nil
}

func runStatus(res *SubCmdResult, home string, w io.Writer) (*SubCmdResult, error) {
	cfg, err := LoadConfig(home)
	if err != nil {
		// Treat a missing/corrupt config as disabled.
		cfg = Config{}
	}

	total, _ := CountRecords(home)
	shipped, _ := ReadShippedCount(home)

	if cfg.Enabled {
		_, _ = fmt.Fprintln(w, "telemetry: ENABLED")
	} else {
		_, _ = fmt.Fprintln(w, "telemetry: DISABLED")
	}
	if cfg.Endpoint != "" {
		_, _ = fmt.Fprintf(w, "  endpoint:        %s\n", cfg.Endpoint)
	} else {
		_, _ = fmt.Fprintln(w, "  endpoint:        (none — local-only)")
	}
	_, _ = fmt.Fprintf(w, "  total records:   %d\n", total)
	_, _ = fmt.Fprintf(w, "  shipped records: %d\n", shipped)

	res.Enabled = cfg.Enabled
	res.Endpoint = cfg.Endpoint
	res.TotalRecords = total
	res.ShippedRecords = shipped
	return res, nil
}

func runSetEndpoint(cfg SubCmdConfig, res *SubCmdResult, home string, w, _ io.Writer) (*SubCmdResult, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("telemetry set-endpoint: URL required")
	}
	if err := validateEndpoint(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("telemetry set-endpoint: %w", err)
	}

	existing, err := LoadConfig(home)
	if err != nil {
		existing = Config{}
	}

	existing.Endpoint = cfg.Endpoint
	if !existing.Enabled {
		existing.Enabled = true
		_, _ = fmt.Fprintln(w, "telemetry: enabling (set-endpoint implies enable)")
	}

	if err := SaveConfig(home, existing); err != nil {
		return nil, fmt.Errorf("telemetry set-endpoint: save config: %w", err)
	}

	_, _ = fmt.Fprintf(w, "telemetry: endpoint set to %s\n", cfg.Endpoint)

	res.Enabled = existing.Enabled
	res.Endpoint = cfg.Endpoint
	return res, nil
}

func runPurge(res *SubCmdResult, home string, w, ew io.Writer) (*SubCmdResult, error) {
	if err := PurgeLog(home); err != nil {
		_, _ = fmt.Fprintf(ew, "telemetry purge: warning: %v\n", err)
	} else {
		_, _ = fmt.Fprintln(w, "telemetry: local log purged")
	}

	cfg, _ := LoadConfig(home)
	res.Enabled = cfg.Enabled
	res.Endpoint = cfg.Endpoint
	res.TotalRecords = 0
	return res, nil
}

func runShow(cfg SubCmdConfig, res *SubCmdResult, home string, w io.Writer) (*SubCmdResult, error) {
	limit := cfg.ShowLimit
	if limit == 0 {
		limit = -1 // ReadRecentRecords: 0 = all; we pass -1 → normalise below
	}
	if limit < 0 {
		limit = 0 // ReadRecentRecords uses 0 for "all"
	}

	records, err := ReadRecentRecords(home, cfg.ShowLimit)
	if err != nil {
		return nil, fmt.Errorf("telemetry show: %w", err)
	}

	if len(records) == 0 {
		_, _ = fmt.Fprintln(w, "(no telemetry records)")
		return res, nil
	}

	for _, ev := range records {
		b, err := json.MarshalIndent(ev, "", "  ")
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "%s\n", b)
	}

	res.Records = records
	res.TotalRecords = len(records)
	return res, nil
}
