package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigPath(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := DefaultConfigPath(get(nil), "/home/u"); got != "/home/u/.config/blinkenkeys/blincli.yaml" {
		t.Errorf("got %q", got)
	}
	if got := DefaultConfigPath(get(map[string]string{"XDG_CONFIG_HOME": "/x"}), "/home/u"); got != "/x/blinkenkeys/blincli.yaml" {
		t.Errorf("got %q", got)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	if fc, exists, err := LoadFile(filepath.Join(dir, "none.yaml")); err != nil || exists || fc != (FileConfig{}) {
		t.Errorf("missing file: %+v %v %v", fc, exists, err)
	}
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte("url: http://nas:49994\ntoken_file: ~/t\ndevice: d\nslots: 6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fc, exists, err := LoadFile(p)
	if err != nil || !exists || fc.URL != "http://nas:49994" || fc.TokenFile != "~/t" || fc.Device != "d" || fc.Slots != 6 {
		t.Errorf("got %+v %v %v", fc, exists, err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("urll: x\n"), 0o600)
	if _, _, err := LoadFile(bad); err == nil {
		t.Error("unknown key must be an error")
	}
	empty := filepath.Join(dir, "empty.yaml")
	_ = os.WriteFile(empty, []byte("# only comments\n"), 0o600)
	if fc, exists, err := LoadFile(empty); err != nil || !exists || fc != (FileConfig{}) {
		t.Errorf("comment-only file: %+v %v %v", fc, exists, err)
	}
}

func TestExpandHome(t *testing.T) {
	if got := ExpandHome("~/a/b", "/h"); got != "/h/a/b" {
		t.Errorf("got %q", got)
	}
	if got := ExpandHome("/abs", "/h"); got != "/abs" {
		t.Errorf("got %q", got)
	}
}
