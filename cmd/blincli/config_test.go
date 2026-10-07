package main

import (
	"errors"
	iofs "io/fs"
	"net/http/httptest"
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
	f := &setFakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, errs := setDaemonApp(t, f, setItermEnv())
	if code := a.run([]string{"set", "-c", "red", "-v"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	if errs.Len() == 0 {
		t.Fatal("expected verbose output")
	}
	if strings.Contains(errs.String(), a.getenv("BLINKENKEYS_TOKEN")) || strings.Contains(errs.String(), "Bearer") {
		t.Errorf("verbose leaked the token:\n%s", errs)
	}
}

func TestConfigShowRedactsURLUserinfo(t *testing.T) {
	a, path, out, _ := cfgApp(t, nil)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("url: http://u:pw@h:1\n"), 0o600)
	if code := a.run([]string{"config", "show", "-C", path}); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(out.String(), "pw") || !strings.Contains(out.String(), "h:1") {
		t.Errorf("show leaked userinfo:\n%s", out)
	}
}

func TestConfigInitForceTightensModeAndWritesToken(t *testing.T) {
	a, path, _, _ := cfgApp(t, nil)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("old\n"), 0o644)
	if code := a.run([]string{"config", "init", "-C", path, "--force", "--token", "s3cr3t"}); code != 0 {
		t.Fatal(code)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `token: "s3cr3t"`) {
		t.Errorf("token not written:\n%s", b)
	}
}

func TestConfigInitForceReplacesSymlinkNotTarget(t *testing.T) {
	a, path, _, _ := cfgApp(t, nil)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	target := filepath.Join(t.TempDir(), "victim")
	_ = os.WriteFile(target, []byte("victim\n"), 0o644)
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if code := a.run([]string{"config", "init", "-C", path, "--token", "t"}); code != exitUsage {
		t.Errorf("symlink without --force: %d, want 64", code)
	}
	if code := a.run([]string{"config", "init", "-C", path, "--force", "--token", "t"}); code != 0 {
		t.Fatal(code)
	}
	if b, _ := os.ReadFile(target); string(b) != "victim\n" {
		t.Errorf("symlink target modified: %q", b)
	}
	if fi, err := os.Lstat(path); err != nil || fi.Mode()&os.ModeSymlink != 0 || fi.Mode().Perm() != 0o600 {
		t.Errorf("link not replaced by 0600 file: %v %v", fi, err)
	}
}

func noTempLeft(t *testing.T, dir string) {
	t.Helper()
	if m, _ := filepath.Glob(filepath.Join(dir, ".blincli-*.tmp")); len(m) != 0 {
		t.Errorf("temp files left behind: %v", m)
	}
}

func TestConfigInitForceOnDirectoryFailsCleanly(t *testing.T) {
	a, path, _, errs := cfgApp(t, nil)
	if err := os.MkdirAll(filepath.Join(path, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	code := a.run([]string{"config", "init", "-C", path, "--force", "--token", "t"})
	if code == 0 || code == 2 || errs.Len() == 0 {
		t.Errorf("--force onto a directory: code %d, stderr %q", code, errs)
	}
	if fi, err := os.Stat(filepath.Join(path, "inner")); err != nil || !fi.IsDir() {
		t.Errorf("directory target damaged: %v %v", fi, err)
	}
	noTempLeft(t, filepath.Dir(path))
}

// Without --force, writeConfig itself must refuse an existing target (the
// Lstat check in configInit is only the friendly message; a file created
// after it must still not be clobbered).
func TestWriteConfigNoClobber(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blincli.yaml")
	if err := os.WriteFile(path, []byte("mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(path, "new\n", false); !errors.Is(err, iofs.ErrExist) {
		t.Errorf("err = %v, want ErrExist", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "mine\n" {
		t.Errorf("existing file clobbered: %q", b)
	}
	noTempLeft(t, dir)
	fresh := filepath.Join(dir, "fresh.yaml")
	if err := writeConfig(fresh, "new\n", false); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(fresh); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("fresh file: %v %v", fi, err)
	}
	if err := writeConfig(path, "new\n", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new\n" {
		t.Errorf("force did not replace: %q", b)
	}
	noTempLeft(t, dir)
}

func TestConfigInitDanglingSymlinkNeedsForce(t *testing.T) {
	a, path, _, _ := cfgApp(t, nil)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	target := filepath.Join(t.TempDir(), "nope")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if code := a.run([]string{"config", "init", "-C", path}); code != exitUsage {
		t.Errorf("code %d, want 64", code)
	}
	if _, err := os.Lstat(target); err == nil {
		t.Error("dangling target was created")
	}
}

func TestConfigInitPermsAndShowNeverLeaksToken(t *testing.T) {
	a, path, out, _ := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path, "-t", "s3cretvalue"}); code != 0 {
		t.Fatal(code)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v", fi, err)
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

func TestBadConfigDoesNotPrintToken(t *testing.T) {
	for _, body := range []string{
		"token: SECRETTOKEN123\nurll: x\n",
		"url: http://h:1\ntoken: SECRETTOKEN123\nslots: x\n",
		"token: SECRETTOKEN123\nurl: [unterminated\n",
	} {
		for _, args := range [][]string{{"config", "show"}, {"set", "-k", "idx:0", "-c", "red"}, {"get", "-k", "idx:0"}, {"detect"}} {
			a, path, out, errs := cfgApp(t, map[string]string{"BLINKENKEYS_SOCKET": "/nonexistent/api.sock"})
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			code := a.run(append([]string{"-C", path}, args...))
			if code == 0 && args[0] != "detect" {
				t.Errorf("%v on a bad config exited 0", args)
			}
			if strings.Contains(out.String()+errs.String(), "SECRETTOKEN123") {
				t.Errorf("%v leaked the token:\nstdout %s\nstderr %s", args, out, errs)
			}
		}
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

func TestOpenConfigWithTokenWarns(t *testing.T) {
	f := &setFakeDaemon{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	for _, tc := range []struct {
		mode os.FileMode
		warn bool
	}{{0o644, true}, {0o600, false}} {
		a, path, out, errs := cfgApp(t, nil)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("url: "+srv.URL+"\ntoken: SECRETTOKEN123\ndevice: d\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, tc.mode); err != nil {
			t.Fatal(err)
		}
		code := a.run([]string{"-C", path, "set", "-k", "idx:0", "-c", "red"})
		if code != 0 {
			t.Errorf("%o: code %d (a warning must not change the exit code): %s", tc.mode, code, errs)
		}
		all := out.String() + errs.String()
		if strings.Contains(all, "SECRETTOKEN123") {
			t.Errorf("%o: leaked the token: %s", tc.mode, all)
		}
		if got := strings.Contains(errs.String(), "readable"); got != tc.warn {
			t.Errorf("%o: warned=%v, want %v; stderr %q", tc.mode, got, tc.warn, errs)
		}
		if tc.warn && strings.Count(errs.String(), "\n") != 1 {
			t.Errorf("warning must be one line: %q", errs)
		}
		errs.Reset()
		if code := a.run([]string{"config", "show", "-C", path}); code != 0 || strings.Contains(errs.String(), "readable") != tc.warn {
			t.Errorf("%o: config show code %d, stderr %q", tc.mode, code, errs)
		}
	}
}

func TestMalformedConfigExits78WithoutContent(t *testing.T) {
	for _, body := range []string{
		"token: SECRETTOKEN123\nurll: x\n",
		"url: http://h:1\ntoken: SECRETTOKEN123\nslots: x\n",
		"token: SECRETTOKEN123\nurl: [unterminated\n",
	} {
		for _, args := range [][]string{{"config", "show"}, {"set", "-k", "idx:0", "-c", "red"}, {"get", "-k", "idx:0"}, {"clear", "-k", "idx:0"}, {"devices"}} {
			a, path, out, errs := cfgApp(t, map[string]string{"BLINKENKEYS_SOCKET": "/nonexistent/api.sock"})
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if code := a.run(append([]string{"-C", path}, args...)); code != exitConfig {
				t.Errorf("%v on %q: code %d, want 78; stderr %s", args, body, code, errs)
			}
			all := out.String() + errs.String()
			for _, frag := range []string{"SECRETTOKEN123", "unterminated", "urll: x", "slots: x"} {
				if strings.Contains(all, frag) {
					t.Errorf("%v: output quotes file content %q:\n%s", args, frag, all)
				}
			}
		}
	}
}
