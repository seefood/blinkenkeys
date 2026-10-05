// Package client is blincli's engine: client-config loading, endpoint
// resolution, the HTTP client for blinkenkeysd, and key planning. It is pure
// Go and must not import the daemon's cgo packages.
package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// FileConfig is blincli.yaml. Every field is optional.
type FileConfig struct {
	URL       string `yaml:"url"`
	Socket    string `yaml:"socket"`
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	Device    string `yaml:"device"`
	Slots     int    `yaml:"slots"`
}

// DefaultConfigPath is ${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/blincli.yaml.
func DefaultConfigPath(getenv func(string) string, home string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "blinkenkeys", "blincli.yaml")
}

// LoadFile reads path. A missing file is not an error (exists is false);
// unknown keys are.
func LoadFile(path string) (fc FileConfig, exists bool, err error) {
	data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied config location
	if errors.Is(err, fs.ErrNotExist) {
		return FileConfig{}, false, nil
	}
	if err != nil {
		return FileConfig{}, false, fmt.Errorf("blincli config: %w", err)
	}
	if err := yaml.UnmarshalWithOptions(data, &fc, yaml.DisallowUnknownField()); err != nil {
		return FileConfig{}, true, fmt.Errorf("blincli config %s: %w", path, err)
	}
	return fc, true, nil
}

// ExpandHome expands a leading "~/".
func ExpandHome(p, home string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
