package config

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestLoadMissingSocketPathAllowed(t *testing.T) {
	cfg, err := Load(writeConfig(t, `listeners: {}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listeners.Socket.Path != "" {
		t.Errorf("socket path = %q, want empty (caller applies default)", cfg.Listeners.Socket.Path)
	}
}

func TestLoadDevices(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
devices:
  - id: uid-0123456789abcdef
    optional: true
  - id: 5754-c401
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []DeviceDecl{{ID: "uid-0123456789abcdef", Optional: true}, {ID: "5754-c401"}}
	if !reflect.DeepEqual(cfg.Devices, want) {
		t.Errorf("Devices = %+v, want %+v", cfg.Devices, want)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct{ name, yaml string }{
		{"unknown top-level key", "listners: {}\n"},
		{"unknown device key", "devices:\n  - { id: a, optinal: true }\n"},
		{"empty device id", "devices:\n  - { optional: true }\n"},
		{"all-digit device id", "devices:\n  - { id: \"0\" }\n"},
		{"duplicate device id", "devices:\n  - { id: a }\n  - { id: a }\n"},
	}
	for _, tt := range tests {
		if _, err := Load(writeConfig(t, tt.yaml)); err == nil {
			t.Errorf("%s: Load succeeded, want error", tt.name)
		}
	}
}

func TestLoadDirMissingFileGivesDefaults(t *testing.T) {
	cfg, err := LoadDir(t.TempDir())
	if err != nil || cfg == nil || len(cfg.Devices) != 0 {
		t.Errorf("LoadDir(empty) = %+v, %v", cfg, err)
	}
}

func TestLoadDirReadsConfigYAML(t *testing.T) {
	dir := filepath.Dir(writeConfig(t, "devices:\n  - { id: a, optional: true }\n"))
	cfg, err := LoadDir(dir)
	if err != nil || len(cfg.Devices) != 1 {
		t.Errorf("LoadDir = %+v, %v", cfg, err)
	}
}

func TestDir(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"BLINKENKEYS_CONFIG_DIR": "/etc/bk", "XDG_CONFIG_HOME": "/x"}, "/etc/bk"},
		{map[string]string{"XDG_CONFIG_HOME": "/x"}, "/x/blinkenkeys"},
		{map[string]string{}, "/home/u/.config/blinkenkeys"},
	}
	for _, tt := range tests {
		if got := Dir(env(tt.env), "/home/u"); got != tt.want {
			t.Errorf("Dir(%v) = %q, want %q", tt.env, got, tt.want)
		}
	}
}
