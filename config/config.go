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

	"github.com/goccy/go-yaml"
)

// Config is blinkenkeysd's on-disk configuration.
type Config struct {
	Naming    NamingRule   `yaml:"naming"`
	Listeners Listeners    `yaml:"listeners"`
	Devices   []DeviceDecl `yaml:"devices"`
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
// never allowed without one. There's no in-code default — actually binding
// this listener is deferred past this plan (see "Explicitly deferred past
// this plan" below) — but wherever a concrete example is needed (docs,
// example config.yaml), the chosen default port is :49994.
type TCPListener struct {
	Address string `yaml:"address"`
	Token   string `yaml:"token"`
}

// Dir returns blinkenkeysd's config directory: $BLINKENKEYS_CONFIG_DIR if
// set, else ${XDG_CONFIG_HOME:-home/.config}/blinkenkeys. It holds
// config.yaml, effects/, and templates/.
func Dir(getenv func(string) string, home string) string {
	if d := getenv("BLINKENKEYS_CONFIG_DIR"); d != "" {
		return d
	}
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
