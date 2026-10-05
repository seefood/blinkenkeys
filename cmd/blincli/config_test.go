package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seefood/blinkenkeys/internal/client"
)

func cfgApp(t *testing.T, env map[string]string) (*app, string, *strings.Builder, *strings.Builder) {
	t.Helper()
	a, _, _ := testApp(env)
	path := filepath.Join(t.TempDir(), "blinkenkeys", "blincli.yaml")
	var out, errs strings.Builder
	a.stdout, a.stderr = &out, &errs
	return a, path, &out, &errs
}

func TestConfigInitTemplateIsUnconfigured(t *testing.T) {
	a, path, _, errs := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", fi, err)
	}
	fc, exists, err := client.LoadFile(path)
	if err != nil || !exists || fc != (client.FileConfig{}) {
		t.Errorf("template must parse as an empty (unconfigured) config: %+v %v %v", fc, exists, err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"# url:", "# token_file:", "# device:"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("template lacks %q:\n%s", want, b)
		}
	}
	if !strings.Contains(errs.String(), "edit") {
		t.Errorf("no edit hint: %q", errs)
	}
}

func TestConfigInitFromFlagsAndRefusesOverwrite(t *testing.T) {
	a, path, _, errs := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path, "--url", "http://nas:49994", "--token-file", "~/tok", "-d", "uid-1"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	fc, _, err := client.LoadFile(path)
	if err != nil || fc.URL != "http://nas:49994" || fc.TokenFile != "~/tok" || fc.Device != "uid-1" {
		t.Errorf("%+v %v", fc, err)
	}
	if code := a.run([]string{"config", "init", "-C", path}); code != exitUsage {
		t.Errorf("overwrite without --force: code %d, want 64", code)
	}
	if code := a.run([]string{"config", "init", "-C", path, "--force", "--socket", "/s"}); code != 0 {
		t.Errorf("--force: code %d", code)
	}
	if fc, _, _ := client.LoadFile(path); fc.Socket != "/s" || fc.URL != "" {
		t.Errorf("after --force: %+v", fc)
	}
}

func TestConfigInitProbesForSingleDevice(t *testing.T) {
	f := &setFakeDaemon{}
	a, path, _, _ := cfgApp(t, map[string]string{})
	srvApp, _ := setDaemonApp(t, f, nil)
	url := srvApp.getenv("BLINKENKEYS_URL")
	if code := a.run([]string{"config", "init", "-C", path, "--url", url, "--token", "tok"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	if fc, _, _ := client.LoadFile(path); fc.Device != "d" {
		t.Errorf("single device not written: %+v", fc)
	}
}

func TestConfigInitInteractive(t *testing.T) {
	a, path, _, errs := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path, "-i"}); code != exitUsage {
		t.Errorf("non-tty -i: code %d, want 64; %s", code, errs)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("nothing may be written when -i is refused")
	}
	a.isTTY = func() bool { return true }
	a.stdin = strings.NewReader("http://nas:49994\n~/tok\nuid-9\n")
	if code := a.run([]string{"config", "init", "-C", path, "-i"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	if fc, _, _ := client.LoadFile(path); fc.URL != "http://nas:49994" || fc.TokenFile != "~/tok" || fc.Device != "uid-9" {
		t.Errorf("%+v", fc)
	}
}

func TestConfigShowMasksToken(t *testing.T) {
	a, path, out, _ := cfgApp(t, nil)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("url: http://h:1\ntoken: supersecret\n"), 0o600)
	if code := a.run([]string{"config", "show", "-C", path}); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(out.String(), "supersecret") || !strings.Contains(out.String(), "****") || !strings.Contains(out.String(), "http://h:1") {
		t.Errorf("show output:\n%s", out)
	}
	a2, missing, out2, _ := cfgApp(t, nil)
	if code := a2.run([]string{"config", "show", "-C", missing}); code != 0 || !strings.Contains(out2.String(), "no config") {
		t.Errorf("missing: %d %s", code, out2)
	}
}

func TestVerboseNeverPrintsToken(t *testing.T) {
	f := &setFakeDaemon{}
	a, errs := setDaemonApp(t, f, setItermEnv())
	a.run([]string{"set", "-c", "red", "-v"})
	if strings.Contains(errs.String(), "tok") && strings.Contains(errs.String(), "token tok") || strings.Contains(errs.String(), "Bearer") {
		t.Errorf("verbose leaked the token:\n%s", errs)
	}
}

func TestConfigInitPermsAndShowNeverLeaksToken(t *testing.T) {
	a, path, out, _ := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path, "-t", "s3cretvalue"}); code != 0 {
		t.Fatal(code)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode: %v %v", di, err)
	}
	if code := a.run([]string{"config", "show", "-C", path}); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(out.String(), "s3cretvalue") || !strings.Contains(out.String(), "****") {
		t.Errorf("show leaked or did not mask:\n%s", out)
	}
}

func TestConfigBadFlagIs64(t *testing.T) {
	a, _, _, _ := cfgApp(t, nil)
	for _, sub := range []string{"init", "show", "path"} {
		if code := a.run([]string{"config", sub, "--bogus"}); code != exitUsage {
			t.Errorf("%s: code %d, want 64", sub, code)
		}
	}
	if code := a.run([]string{"config"}); code != exitUsage {
		t.Errorf("no subcommand: %d", code)
	}
}

func TestConfigPath(t *testing.T) {
	a, path, out, _ := cfgApp(t, nil)
	a.run([]string{"config", "path", "-C", path})
	if strings.TrimSpace(out.String()) != path {
		t.Errorf("path = %q", out)
	}
}
