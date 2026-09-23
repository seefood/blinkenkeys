// Package config loads blinkenkeysd's on-disk configuration
// (~/.config/blinkenkeys/config.yaml), read once at startup, per the
// design spec's Config section.
package config

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// Config is blinkenkeysd's on-disk configuration.
type Config struct {
	Naming    NamingRule `yaml:"naming"`
	Listeners Listeners  `yaml:"listeners"`
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
	Path string `yaml:"path"`
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

// Load reads and validates a config.yaml.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if cfg.Listeners.Socket.Path == "" {
		return nil, fmt.Errorf("config: listeners.socket.path is required")
	}
	if cfg.Listeners.TCP != nil && cfg.Listeners.TCP.Token == "" {
		return nil, fmt.Errorf("config: listeners.tcp.token is required whenever listeners.tcp is set")
	}
	return &cfg, nil
}
