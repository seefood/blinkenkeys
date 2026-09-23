package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeConfig(t, `
naming:
  prefer: uid
listeners:
  socket:
    path: ~/.local/state/blinkenkeys/api.sock
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listeners.Socket.Path == "" {
		t.Error("socket path not populated")
	}
	if cfg.Listeners.TCP != nil {
		t.Error("TCP should be nil when absent from YAML")
	}
}

func TestLoadTCPWithoutTokenRejected(t *testing.T) {
	path := writeConfig(t, `
listeners:
  socket:
    path: /tmp/api.sock
  tcp:
    address: 0.0.0.0:49994
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for TCP listener without a token")
	}
}

func TestLoadMissingSocketPathRejected(t *testing.T) {
	path := writeConfig(t, `listeners: {}`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for missing socket path")
	}
}
