package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// badSecretConfigs are invalid blincli.yaml files whose token must never
// appear in the resulting error (goccy's default Error() quotes the source).
var badSecretConfigs = map[string]string{
	"unknown key":   "token: SECRETTOKEN123\nurll: x\ndevice: d\n",
	"slots type":    "url: http://h:1\ntoken: SECRETTOKEN123\nslots: x\n",
	"syntax":        "token: SECRETTOKEN123\nurl: [unterminated\n",
	"bad indent":    "token: SECRETTOKEN123\n  device: d\n",
	"duplicate key": "token: SECRETTOKEN123\ntoken: SECRETTOKEN123\n",
	"open quote":    "token: \"SECRETTOKEN123\nurl: x\n",
	"token as list": "token: [SECRETTOKEN123]\n",
	"token as map":  "token: {SECRETTOKEN123: 1}\n",
	"bare scalar":   "SECRETTOKEN123\n",
	"bad tab":       "token: SECRETTOKEN123\n\turl: x\n",
}

func TestLoadFileErrorsDoNotLeakSource(t *testing.T) {
	dir := t.TempDir()
	for name, body := range badSecretConfigs {
		p := filepath.Join(dir, "c.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := LoadFile(p)
		if err == nil {
			t.Errorf("%s: expected an error", name)
			continue
		}
		if strings.Contains(err.Error(), "SECRETTOKEN123") || strings.Contains(fmt.Sprintf("%+v", err), "SECRETTOKEN123") {
			t.Errorf("%s: error leaks the token:\n%v", name, err)
		}
		if !strings.Contains(err.Error(), p) {
			t.Errorf("%s: error should name the file: %v", name, err)
		}
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
