// Package config loads blinkenkeysd's optional on-disk
// configuration (<config dir>/config.yaml, see Dir), read once at startup.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
)

// DefaultClaimIdleTimeout is claims.idle_timeout's value when config.yaml
// doesn't set one.
const DefaultClaimIdleTimeout = "8h"

// Config is blinkenkeysd's on-disk configuration.
type Config struct {
	Naming    NamingRule   `yaml:"naming"`
	Listeners Listeners    `yaml:"listeners"`
	Devices   []DeviceDecl `yaml:"devices"`
	Claims    ClaimsConfig `yaml:"claims"`
}

// ClaimsConfig configures the named-key claim pool (see
// dispatcher.RunClaimSweep).
type ClaimsConfig struct {
	IdleTimeout string `yaml:"idle_timeout"` // Go duration string, e.g. "8h"; "" = DefaultClaimIdleTimeout
}

// ClaimIdleTimeout returns Claims.IdleTimeout parsed as a time.Duration,
// falling back to DefaultClaimIdleTimeout when unset. Load has already
// validated the string parses, so the error return here is unreachable in
// practice.
func (c *Config) ClaimIdleTimeout() time.Duration {
	s := c.Claims.IdleTimeout
	if s == "" {
		s = DefaultClaimIdleTimeout
	}
	d, _ := time.ParseDuration(s)
	return d
}

// DeviceDecl is one devices: entry. ID is the device name blinkenkeysd
// assigns (hid.BaseName); Optional pre-declares the device so writes to it
// succeed before it's first seen, and exempts it from untethered eviction.
type DeviceDecl struct {
	ID       string `yaml:"id"`
	Optional bool   `yaml:"optional"`
}

// NamingRule selects blinkenkeysd's preferred device-naming strategy; it still
// falls back automatically per device if a device can't answer
// GetKeyboardUID.
type NamingRule struct {
	Prefer string `yaml:"prefer"` // "uid" (default), "path", or "vidpid"
}

// Listeners configures blinkenkeysd's listeners.
type Listeners struct {
	Socket SocketListener `yaml:"socket"`
	TCP    *TCPListener   `yaml:"tcp,omitempty"` // nil = disabled (off by default)
}

// SocketListener is the always-on, $HOME-owned Unix socket.
type SocketListener struct {
	Path string `yaml:"path"` // "" = default (~/.local/state/blinkenkeys/api.sock); "~/" is expanded
}

// TCPListener is only present in the config when explicitly enabled; Load
// rejects one with no Token, per the spec's hard requirement that TCP is
// never allowed without one. There's no in-code default — wherever a
// concrete example is needed (docs, example config.yaml), the chosen
// default port is :49994.
type TCPListener struct {
	Address string `yaml:"address"`
	Token   string `yaml:"token"`
}

// Dir returns blinkenkeysd's default config directory:
// ${XDG_CONFIG_HOME:-home/.config}/blinkenkeys. It holds config.yaml,
// effects/, and templates/. Callers wanting a non-default location use the
// --config flag instead of overriding this.
func Dir(getenv func(string) string, home string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "blinkenkeys")
}

// LoadDir loads dir/config.yaml; a missing file means all defaults.
func LoadDir(dir string) (*Config, error) {
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	return cfg, err
}

// Load reads and validates a config.yaml. Unknown keys are errors.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied config location, not untrusted network input
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.UnmarshalWithOptions(data, &cfg, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if cfg.Listeners.TCP != nil && cfg.Listeners.TCP.Token == "" {
		return nil, fmt.Errorf("config: listeners.tcp.token is required whenever listeners.tcp is set")
	}
	if cfg.Claims.IdleTimeout != "" {
		if _, err := time.ParseDuration(cfg.Claims.IdleTimeout); err != nil {
			return nil, fmt.Errorf("config: claims.idle_timeout %q: %w", cfg.Claims.IdleTimeout, err)
		}
	}
	seen := make(map[string]bool, len(cfg.Devices))
	for i, d := range cfg.Devices {
		switch {
		case d.ID == "":
			return nil, fmt.Errorf("config: devices[%d].id is required", i)
		case strings.Trim(d.ID, "0123456789") == "":
			return nil, fmt.Errorf("config: devices[%d].id %q is all digits, which is ambiguous with a device ordinal", i, d.ID)
		case seen[d.ID]:
			return nil, fmt.Errorf("config: devices[%d].id %q is duplicated", i, d.ID)
		}
		seen[d.ID] = true
	}
	return &cfg, nil
}
