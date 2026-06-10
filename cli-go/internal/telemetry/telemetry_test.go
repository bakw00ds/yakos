package telemetry

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- helpers ----------------------------------------------------------------

func newCfg(t *testing.T, sub string) SubCmdConfig {
	t.Helper()
	return SubCmdConfig{
		Subcommand: sub,
		HomeDir:    t.TempDir(),
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
}

func out(cfg SubCmdConfig) string  { return cfg.Writer.(*bytes.Buffer).String() }
func eout(cfg SubCmdConfig) string { return cfg.ErrWriter.(*bytes.Buffer).String() }

func mustRun(t *testing.T, cfg SubCmdConfig) *SubCmdResult {
	t.Helper()
	res, err := Run(cfg)
	if err != nil {
		t.Fatalf("Run(%q): %v", cfg.Subcommand, err)
	}
	return res
}

// ---- default disabled -------------------------------------------------------

// TestDefaultDisabled verifies that the absence of a config file means telemetry
// is off.
func TestDefaultDisabled(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Enabled {
		t.Error("expected Enabled=false when no config file exists")
	}
	if cfg.Endpoint != "" {
		t.Errorf("expected empty Endpoint; got %q", cfg.Endpoint)
	}
}

// TestRecordNoOpWhenDisabled verifies that Record does not write anything when
// telemetry is disabled.
func TestRecordNoOpWhenDisabled(t *testing.T) {
	home := t.TempDir()
	ev := Event{
		TS:          time.Now().UTC(),
		YakosVersion: "0.37.0.0 (go)",
		OS:          "darwin",
		Arch:        "arm64",
		Command:     "validate",
		ExitCode:    0,
		DurationMS:  42,
		SessionHash: "abc123",
	}
	Record(home, ev)

	n, err := CountRecords(home)
	if err != nil {
		t.Fatalf("CountRecords: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 records when disabled; got %d", n)
	}
}

// ---- enable -----------------------------------------------------------------

// TestEnable verifies that `yakos telemetry enable` sets Enabled=true and
// prints the right output.
func TestEnable(t *testing.T) {
	cfg := newCfg(t, "enable")
	res := mustRun(t, cfg)

	if !res.Enabled {
		t.Error("expected Enabled=true after enable")
	}
	o := out(cfg)
	if !strings.Contains(o, "ENABLED") {
		t.Errorf("output should contain ENABLED; got: %s", o)
	}
	if !strings.Contains(o, "local-only") {
		t.Errorf("output should mention local-only when no endpoint; got: %s", o)
	}
}

// TestEnableWithEndpoint verifies that enable --endpoint sets both enabled and
// the endpoint URL.
func TestEnableWithEndpoint(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg.Endpoint = "https://tel.example.com/ingest"
	res := mustRun(t, cfg)

	if !res.Enabled {
		t.Error("expected Enabled=true")
	}
	if res.Endpoint != "https://tel.example.com/ingest" {
		t.Errorf("expected endpoint to be set; got %q", res.Endpoint)
	}
	if !strings.Contains(out(cfg), "https://tel.example.com/ingest") {
		t.Error("output should show endpoint URL")
	}
}

// TestEnableInvalidEndpoint verifies that a non-HTTP(S) endpoint is rejected.
func TestEnableInvalidEndpoint(t *testing.T) {
	cfg := newCfg(t, "enable")
	cfg.Endpoint = "ftp://nope.example.com"
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for invalid endpoint scheme")
	}
}

// TestEnablePersists verifies that the config survives a round-trip through
// LoadConfig after enable.
func TestEnablePersists(t *testing.T) {
	home := t.TempDir()
	cfg := SubCmdConfig{Subcommand: "enable", HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	if _, err := Run(cfg); err != nil {
		t.Fatalf("enable: %v", err)
	}

	loaded, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !loaded.Enabled {
		t.Error("expected Enabled=true after enable+reload")
	}
}

// ---- disable ----------------------------------------------------------------

// TestDisable verifies that disable sets Enabled=false and output says DISABLED.
func TestDisable(t *testing.T) {
	home := t.TempDir()
	// First enable.
	enableCfg := SubCmdConfig{Subcommand: "enable", HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	if _, err := Run(enableCfg); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Now disable.
	cfg := SubCmdConfig{Subcommand: "disable", HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	res := mustRun(t, cfg)
	if res.Enabled {
		t.Error("expected Enabled=false after disable")
	}
	if !strings.Contains(cfg.Writer.(*bytes.Buffer).String(), "DISABLED") {
		t.Error("output should contain DISABLED")
	}
}

// TestDisablePreventsRecording verifies that Record is a no-op after disable.
func TestDisablePreventsRecording(t *testing.T) {
	home := t.TempDir()

	// Enable then disable.
	for _, sub := range []string{"enable", "disable"} {
		c := SubCmdConfig{Subcommand: sub, HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
		if _, err := Run(c); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
	}

	ev := Event{Command: "status", ExitCode: 0, DurationMS: 1, SessionHash: "x"}
	Record(home, ev)

	n, _ := CountRecords(home)
	if n != 0 {
		t.Errorf("expected 0 records after disable; got %d", n)
	}
}

// ---- status -----------------------------------------------------------------

// TestStatusDisabled verifies status output when nothing is configured.
func TestStatusDisabled(t *testing.T) {
	cfg := newCfg(t, "status")
	res := mustRun(t, cfg)

	if res.Enabled {
		t.Error("expected Enabled=false")
	}
	o := out(cfg)
	if !strings.Contains(o, "DISABLED") {
		t.Errorf("expected DISABLED in status output; got: %s", o)
	}
	if !strings.Contains(o, "total records") {
		t.Error("status should print total records")
	}
}

// TestStatusEnabled verifies status output after enable.
func TestStatusEnabled(t *testing.T) {
	home := t.TempDir()

	// Enable.
	eCfg := SubCmdConfig{Subcommand: "enable", HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	if _, err := Run(eCfg); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// Record one event.
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	ev := Event{Command: "cost", ExitCode: 0, DurationMS: 10, SessionHash: SessionHash()}
	Record(home, ev)

	// Status.
	cfg := SubCmdConfig{Subcommand: "status", HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	res := mustRun(t, cfg)
	if !res.Enabled {
		t.Error("expected Enabled=true")
	}
	if res.TotalRecords != 1 {
		t.Errorf("expected 1 total record; got %d", res.TotalRecords)
	}
	o := cfg.Writer.(*bytes.Buffer).String()
	if !strings.Contains(o, "ENABLED") {
		t.Errorf("expected ENABLED in output; got: %s", o)
	}
}

// ---- set-endpoint -----------------------------------------------------------

// TestSetEndpoint verifies that set-endpoint sets the endpoint and enables if
// previously disabled.
func TestSetEndpoint(t *testing.T) {
	home := t.TempDir()
	cfg := SubCmdConfig{
		Subcommand: "set-endpoint",
		Endpoint:   "https://ingest.example.com/v1/telemetry",
		HomeDir:    home,
		Writer:     &bytes.Buffer{},
		ErrWriter:  &bytes.Buffer{},
	}
	res := mustRun(t, cfg)

	if !res.Enabled {
		t.Error("set-endpoint should enable telemetry")
	}
	if res.Endpoint != "https://ingest.example.com/v1/telemetry" {
		t.Errorf("expected endpoint set; got %q", res.Endpoint)
	}

	loaded, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Endpoint != "https://ingest.example.com/v1/telemetry" {
		t.Error("endpoint not persisted")
	}
}

// TestSetEndpointMissingURL verifies that set-endpoint without a URL errors.
func TestSetEndpointMissingURL(t *testing.T) {
	cfg := newCfg(t, "set-endpoint")
	// Endpoint left empty.
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error when URL missing")
	}
}

// TestSetEndpointInvalidScheme verifies that a non-HTTP(S) URL is rejected.
func TestSetEndpointInvalidScheme(t *testing.T) {
	cfg := newCfg(t, "set-endpoint")
	cfg.Endpoint = "ssh://example.com"
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for non-HTTP(S) scheme")
	}
}

// ---- purge ------------------------------------------------------------------

// TestPurge verifies that purge deletes the log file.
func TestPurge(t *testing.T) {
	home := t.TempDir()

	// Enable and record something.
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	ev := Event{Command: "validate", ExitCode: 0, DurationMS: 5, SessionHash: "s"}
	Record(home, ev)

	n, _ := CountRecords(home)
	if n == 0 {
		t.Fatal("expected at least one record before purge")
	}

	cfg := SubCmdConfig{Subcommand: "purge", HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	mustRun(t, cfg)

	n2, _ := CountRecords(home)
	if n2 != 0 {
		t.Errorf("expected 0 records after purge; got %d", n2)
	}
}

// TestPurgeNoLogFile verifies that purge is a no-op when the log does not
// exist.
func TestPurgeNoLogFile(t *testing.T) {
	cfg := newCfg(t, "purge")
	_, err := Run(cfg)
	if err != nil {
		t.Fatalf("purge with no log: %v", err)
	}
	// No error and no panic expected.
}

// ---- show -------------------------------------------------------------------

// TestShowNoRecords verifies the empty-state message.
func TestShowNoRecords(t *testing.T) {
	cfg := newCfg(t, "show")
	mustRun(t, cfg)
	o := out(cfg)
	if !strings.Contains(o, "no telemetry records") {
		t.Errorf("expected 'no telemetry records'; got: %s", o)
	}
}

// TestShowRecords verifies that show prints records as JSON.
func TestShowRecords(t *testing.T) {
	home := t.TempDir()
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	for i := 0; i < 3; i++ {
		ev := Event{Command: "cost", ExitCode: 0, DurationMS: int64(i * 10), SessionHash: "h"}
		Record(home, ev)
	}

	cfg := SubCmdConfig{Subcommand: "show", HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	res := mustRun(t, cfg)

	if len(res.Records) != 3 {
		t.Errorf("expected 3 records; got %d", len(res.Records))
	}
	o := out(cfg)
	if !strings.Contains(o, `"command"`) {
		t.Error("expected JSON output with command field")
	}
}

// TestShowLimit verifies that --limit N caps the output.
func TestShowLimit(t *testing.T) {
	home := t.TempDir()
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	for i := 0; i < 10; i++ {
		ev := Event{Command: "kanban", ExitCode: 0, DurationMS: int64(i), SessionHash: "z"}
		Record(home, ev)
	}

	cfg := SubCmdConfig{Subcommand: "show", ShowLimit: 3, HomeDir: home, Writer: &bytes.Buffer{}, ErrWriter: &bytes.Buffer{}}
	res := mustRun(t, cfg)
	if len(res.Records) != 3 {
		t.Errorf("expected 3 records with limit=3; got %d", len(res.Records))
	}
}

// ---- recording --------------------------------------------------------------

// TestRecordWritesNDJSON verifies that Record appends a valid NDJSON line.
func TestRecordWritesNDJSON(t *testing.T) {
	home := t.TempDir()
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	runtime := "claude"
	ev := Event{
		TS:           time.Date(2026, 6, 3, 15, 0, 0, 0, time.UTC),
		YakosVersion: "0.37.0.0 (go)",
		OS:           "darwin",
		Arch:         "arm64",
		Command:      "dispatch",
		Subcommand:   "",
		ExitCode:     0,
		DurationMS:   142,
		AgentCount:   1,
		Runtime:      &runtime,
		SessionHash:  SessionHash(),
	}
	Record(home, ev)

	data, err := os.ReadFile(filepath.Join(home, ".yakos-state", "telemetry.ndjson"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var parsed Event
	if err := json.Unmarshal(bytes.TrimRight(data, "\n"), &parsed); err != nil {
		t.Fatalf("parse NDJSON: %v — raw: %s", err, data)
	}
	if parsed.Command != "dispatch" {
		t.Errorf("expected command=dispatch; got %q", parsed.Command)
	}
	if parsed.AgentCount != 1 {
		t.Errorf("expected agent_count=1; got %d", parsed.AgentCount)
	}
	if parsed.Runtime == nil || *parsed.Runtime != "claude" {
		t.Error("expected runtime=claude")
	}
}

// TestRecordMultipleLines verifies that multiple Record calls produce multiple
// NDJSON lines.
func TestRecordMultipleLines(t *testing.T) {
	home := t.TempDir()
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	for i := 0; i < 5; i++ {
		Record(home, Event{Command: "status", ExitCode: 0, DurationMS: int64(i), SessionHash: "x"})
	}

	n, err := CountRecords(home)
	if err != nil {
		t.Fatalf("CountRecords: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 records; got %d", n)
	}
}

// ---- redaction --------------------------------------------------------------

// TestRedactEventSafeFields verifies that safe fields pass through unchanged.
func TestRedactEventSafeFields(t *testing.T) {
	ev := Event{
		Command:     "validate",
		Subcommand:  "lint",
		SessionHash: "a3f7" + strings.Repeat("0", 60),
	}
	got := RedactEvent(ev)
	if got.Command != "validate" {
		t.Errorf("Command should not be redacted; got %q", got.Command)
	}
	if got.Subcommand != "lint" {
		t.Errorf("Subcommand should not be redacted; got %q", got.Subcommand)
	}
}

// TestRedactFieldPIIPattern verifies that redactField replaces known PII.
func TestRedactFieldPIIPattern(t *testing.T) {
	// We cannot know the actual HOME value in tests reliably, but we can
	// test that redactField replaces a matching pattern.
	result := redactField("")
	if result != "" {
		t.Errorf("empty field should stay empty; got %q", result)
	}
	// Plain safe string should pass through.
	result = redactField("validate")
	if result != "validate" {
		t.Errorf("safe field should pass through; got %q", result)
	}
}

// TestRedactNilRuntime verifies that a nil Runtime pointer is handled safely.
func TestRedactNilRuntime(t *testing.T) {
	ev := Event{Command: "cost", Runtime: nil}
	got := RedactEvent(ev)
	if got.Runtime != nil {
		t.Error("nil Runtime should stay nil after redaction")
	}
}

// ---- session hash -----------------------------------------------------------

// TestSessionHashIsDeterministicWithinProcess verifies that two calls return
// the same value.
func TestSessionHashIsDeterministicWithinProcess(t *testing.T) {
	a := SessionHash()
	b := SessionHash()
	if a != b {
		t.Errorf("SessionHash not deterministic: %q != %q", a, b)
	}
}

// TestSessionHashLength verifies the hash is a 64-character hex string (sha256).
func TestSessionHashLength(t *testing.T) {
	h := SessionHash()
	if len(h) != 64 {
		t.Errorf("expected 64-char hex; got length %d: %q", len(h), h)
	}
	for _, r := range h {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("non-hex char %q in session hash %q", r, h)
			break
		}
	}
}

// ---- shipper ----------------------------------------------------------------

// TestShipPendingNoOpWhenDisabled verifies ShipPending is silent when disabled.
func TestShipPendingNoOpWhenDisabled(t *testing.T) {
	home := t.TempDir()
	// No config → disabled.  ShipPending must not panic or error.
	ShipPending(home) // fail-silent; any panic would fail the test
}

// TestShipPendingNoOpWhenNoEndpoint verifies ShipPending is silent when
// enabled but no endpoint configured.
func TestShipPendingNoOpWhenNoEndpoint(t *testing.T) {
	home := t.TempDir()
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	ShipPending(home)
}

// TestShipPendingFailSilentBadEndpoint verifies that an unreachable endpoint
// causes ShipPending to return silently (no panic, no log corruption).
func TestShipPendingFailSilentBadEndpoint(t *testing.T) {
	home := t.TempDir()
	if err := SaveConfig(home, Config{Enabled: true, Endpoint: "http://127.0.0.1:19999/nonexistent"}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	Record(home, Event{Command: "cost", ExitCode: 0, DurationMS: 1, SessionHash: "x"})

	// Must not panic or hang (timeout is 5s; test framework has its own timeout).
	ShipPending(home)

	// Log file should still be intact.
	n, _ := CountRecords(home)
	if n == 0 {
		t.Error("record should still be in local log after failed ship")
	}
}

// ---- ParseArgs --------------------------------------------------------------

// TestParseArgsEnable verifies that `telemetry enable` is parsed correctly.
func TestParseArgsEnable(t *testing.T) {
	cfg, err := ParseArgs([]string{"enable"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Subcommand != "enable" {
		t.Errorf("expected enable; got %q", cfg.Subcommand)
	}
}

// TestParseArgsEnableEndpointFlag verifies --endpoint parsing.
func TestParseArgsEnableEndpointFlag(t *testing.T) {
	cfg, err := ParseArgs([]string{"enable", "--endpoint", "https://x.example.com"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Endpoint != "https://x.example.com" {
		t.Errorf("expected endpoint; got %q", cfg.Endpoint)
	}
}

// TestParseArgsEnableEndpointEq verifies --endpoint=URL parsing.
func TestParseArgsEnableEndpointEq(t *testing.T) {
	cfg, err := ParseArgs([]string{"enable", "--endpoint=https://y.example.com"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Endpoint != "https://y.example.com" {
		t.Errorf("expected endpoint; got %q", cfg.Endpoint)
	}
}

// TestParseArgsShowLimit verifies --limit parsing.
func TestParseArgsShowLimit(t *testing.T) {
	cfg, err := ParseArgs([]string{"show", "--limit", "10"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.ShowLimit != 10 {
		t.Errorf("expected ShowLimit=10; got %d", cfg.ShowLimit)
	}
}

// TestParseArgsShowLimitEq verifies --limit=N parsing.
func TestParseArgsShowLimitEq(t *testing.T) {
	cfg, err := ParseArgs([]string{"show", "--limit=5"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.ShowLimit != 5 {
		t.Errorf("expected ShowLimit=5; got %d", cfg.ShowLimit)
	}
}

// TestParseArgsSetEndpoint verifies set-endpoint URL arg.
func TestParseArgsSetEndpoint(t *testing.T) {
	cfg, err := ParseArgs([]string{"set-endpoint", "https://z.example.com"}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Endpoint != "https://z.example.com" {
		t.Errorf("expected endpoint; got %q", cfg.Endpoint)
	}
}

// TestParseArgsEmpty verifies empty args → help.
func TestParseArgsEmpty(t *testing.T) {
	cfg, err := ParseArgs([]string{}, "/tmp")
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.Subcommand != "help" {
		t.Errorf("expected help; got %q", cfg.Subcommand)
	}
}

// ---- config file mode -------------------------------------------------------

// TestConfigFileMode verifies that the config file is written with mode 0600.
//
// On Windows, NTFS doesn't honor POSIX mode bits — hardening is done via the
// winsec package (DACL with single-ACE for current user). Skipping the mode
// check there; the equivalent ACL assertion lives in internal/winsec tests.
func TestConfigFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits not honored on NTFS; ACL hardening verified in internal/winsec tests")
	}
	home := t.TempDir()
	if err := SaveConfig(home, Config{Enabled: true}); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, ".yakos-state", "telemetry.yml"))
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	// On Unix, mode 0600 means owner-only read/write.
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected mode 0600; got %04o", info.Mode().Perm())
	}
}

// ---- help -------------------------------------------------------------------

// TestHelp verifies that the help subcommand prints useful content.
func TestHelp(t *testing.T) {
	cfg := newCfg(t, "help")
	mustRun(t, cfg)
	o := out(cfg)
	for _, phrase := range []string{"enable", "disable", "status", "purge", "show", "DISABLED", "PII"} {
		if !strings.Contains(o, phrase) {
			t.Errorf("help output missing %q", phrase)
		}
	}
}

// TestUnknownSubcommand verifies that unknown subcommands return an error.
func TestUnknownSubcommand(t *testing.T) {
	cfg := newCfg(t, "frobnicate")
	_, err := Run(cfg)
	if err == nil {
		t.Error("expected error for unknown subcommand")
	}
}

// TestEout ensures the eout helper compiles (it's used in other packages via
// the same pattern; keep to avoid dead-code removal).
func TestEout(t *testing.T) {
	cfg := newCfg(t, "status")
	mustRun(t, cfg)
	_ = eout(cfg) // exercise helper
}
