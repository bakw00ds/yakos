package telemetry

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the operator-controlled telemetry configuration stored at
// ~/.yakos-state/telemetry.yml.
type Config struct {
	// Enabled is the master gate.  Default: false (off).
	Enabled bool `yaml:"enabled"`

	// Endpoint is the optional HTTP(S) URL to which records are POSTed.
	// Empty string means local-only mode (no network calls).
	// Operators must set this explicitly — there is no default endpoint.
	Endpoint string `yaml:"endpoint,omitempty"`
}

// configPath returns the canonical path to the telemetry config file.
func configPath(home string) string {
	return filepath.Join(home, ".yakos-state", "telemetry.yml")
}

// LoadConfig reads and unmarshals the telemetry config from disk.
// When the file does not exist a zero Config (Enabled: false) is returned
// without an error — this preserves the default-off guarantee.
func LoadConfig(home string) (Config, error) {
	path := configPath(home)
	data, err := os.ReadFile(path) //nolint:gosec
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("telemetry: read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("telemetry: parse config %s: %w", path, err)
	}
	return cfg, nil
}

// SaveConfig marshals cfg and writes it atomically to the telemetry.yml path
// under home.  The file is created with mode 0600 (owner-only).
//
// Atomic write: temp-rename strategy (Q8 from port plan).
func SaveConfig(home string, cfg Config) error {
	dir := filepath.Join(home, ".yakos-state")
	if err := os.MkdirAll(dir, 0700); err != nil { //nolint:gosec
		return fmt.Errorf("telemetry: mkdir %s: %w", dir, err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("telemetry: marshal config: %w", err)
	}

	path := configPath(home)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil { //nolint:gosec
		return fmt.Errorf("telemetry: write tmp config: %w", err)
	}
	if err := SecureConfigFile(tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("telemetry: secure tmp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("telemetry: rename config: %w", err)
	}
	return nil
}
