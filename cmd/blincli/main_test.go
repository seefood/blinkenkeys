package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seefood/blinkenkeys/internal/client"
	"github.com/seefood/blinkenkeys/internal/termid"
)

// testApp builds an app with a fake environment; stdout/stderr are captured.
func testApp(env map[string]string) (*app, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	return &app{
		getenv: func(k string) string { return env[k] },
		home:   "/nonexistent-home",
		host:   "testhost",
		stdin:  strings.NewReader(""),
		stdout: out, stderr: errb,
		isTTY: func() bool { return false },
		termEnv: termid.Env{
			Getenv: func(k string) string { return env[k] },
			Run:    func(context.Context, string, ...string) (string, error) { return "", errors.New("no helper") },
		},
	}, out, errb
}

func TestExitCodeMapping(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{fmt.Errorf("x: %w", client.ErrNoEndpoint), 78},
		{&client.NoEndpointError{}, 78},
		{fmt.Errorf("x: %w", client.ErrBadConfig), 78},
		{fmt.Errorf("x: %w", client.ErrUnreachable), 69},
		{fmt.Errorf("x: %w", client.ErrAuth), 77},
		{&client.APIError{Status: 401}, 77},
		{fmt.Errorf("x: %w", client.ErrUsage), 64},
		{client.ErrNoKey, 64},
		{&client.APIError{Status: 404}, 66},
		{&client.APIError{Status: 503}, 1},
		{errors.New("boom"), 1},
	}
	for _, tt := range tests {
		if got := exitCode(tt.err); got != tt.want {
			t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestNeverExitsTwo(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"set", "--bogus"}, {"nosuchcommand"}, {}, {"set", "-c"}} {
		a, _, _ := testApp(nil)
		if code := a.run(args); code == 2 {
			t.Errorf("%v exited 2 (Claude Code hooks treat 2 as blocking)", args)
		}
	}
}

func TestPanicExitsOneNotTwo(t *testing.T) {
	commands["test-panic"] = func(*app, []string) int { panic("boom SECRET") }
	t.Cleanup(func() { delete(commands, "test-panic") })
	a, _, errb := testApp(nil)
	if code := a.run([]string{"test-panic"}); code != exitFail {
		t.Errorf("code %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "internal error") || strings.Contains(errb.String(), "goroutine") {
		t.Errorf("stderr %q: want a short message, no stack trace", errb)
	}
}

func TestUsageErrorsExit64(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"nosuchcommand"}, {}} {
		a, _, errb := testApp(nil)
		if code := a.run(args); code != exitUsage || errb.Len() == 0 {
			t.Errorf("%v: code %d, stderr %q", args, code, errb)
		}
	}
}

func TestFlagParseErrorExits64(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"-S"}, {"detect", "--bogus"}, {"detect", "-S"}} {
		a, _, errb := testApp(nil)
		if code := a.run(args); code != exitUsage || errb.Len() == 0 {
			t.Errorf("%v: code %d, stderr %q", args, code, errb)
		}
	}
}

func TestAuthDecidedBeforeAnyRequest(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer srv.Close()
	a, _, _ := testApp(map[string]string{"BLINKENKEYS_URL": srv.URL})
	_, _, _, err := a.session(&globals{})
	if code := exitCode(err); code != exitNoPerm || hit {
		t.Errorf("code %d (err %v), server contacted: %v", code, err, hit)
	}
}

func TestHelpAndVersion(t *testing.T) {
	a, out, _ := testApp(nil)
	if code := a.run([]string{"--help"}); code != 0 || !strings.Contains(out.String(), "Usage: blincli") {
		t.Errorf("--help: %d %q", code, out)
	}
	a, out, _ = testApp(nil)
	if code := a.run([]string{"version"}); code != 0 || !strings.HasPrefix(out.String(), "blincli ") {
		t.Errorf("version: %d %q", code, out)
	}
}

func TestDetectCommand(t *testing.T) {
	a, out, _ := testApp(map[string]string{"KITTY_WINDOW_ID": "4", "BLINKENKEYS_URL": "http://h:1", "BLINKENKEYS_TOKEN": "t"})
	if code := a.run([]string{"detect"}); code != 0 {
		t.Fatalf("code %d; %s", code, out)
	}
	for _, want := range []string{"terminal", "kitty", "key", "kitty-4", "transport", "http://h:1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("detect output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "token  t") {
		t.Error("detect must not print the token")
	}
}

func TestGlobalsBeforeCommandAreHonored(t *testing.T) {
	f := &setFakeDaemon{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	tokFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokFile, []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No BLINKENKEYS_URL/TOKEN: endpoint and token can only come from the
	// pre-command -u and --token-file.
	a, out, errb := testApp(nil)
	code := a.run([]string{"-u", srv.URL, "--token-file", tokFile, "-d", "dev", "-v", "set", "-k", "idx:3", "-c", "red"})
	if code != 0 {
		t.Fatalf("code %d: %s", code, errb)
	}
	if w := f.writes(); len(w) != 1 || w[0].Path != "/devices/dev/keys/idx:3" {
		t.Errorf("writes = %+v", w)
	}
	if !strings.Contains(errb.String(), "endpoint") {
		t.Errorf("-v before the command was ignored; stderr %q", errb)
	}

	gf := &getFake{keys: map[string]string{"/devices/d/keys/idx:1": keyJSON}}
	gsrv := httptest.NewServer(gf)
	defer gsrv.Close()
	a, out, errb = testApp(map[string]string{"BLINKENKEYS_TOKEN": "tok"})
	if code := a.run([]string{"-q", "-u", gsrv.URL, "get", "-k", "idx:1"}); code != 0 || out.Len() != 0 {
		t.Errorf("-q before the command: code %d, stdout %q, stderr %q", code, out, errb)
	}
}

func TestCommandGlobalsOverridePreCommand(t *testing.T) {
	f := &setFakeDaemon{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	a, _, errb := testApp(map[string]string{"BLINKENKEYS_TOKEN": "tok"})
	code := a.run([]string{"-u", "http://127.0.0.1:9", "-d", "x", "set", "-u", srv.URL, "-d", "dev", "-k", "idx:0", "-c", "red"})
	if code != 0 {
		t.Fatalf("code %d: %s", code, errb)
	}
	if w := f.writes(); len(w) != 1 || w[0].Path != "/devices/dev/keys/idx:0" {
		t.Errorf("writes = %+v", w)
	}
}

func TestPreCommandParseErrorExits64(t *testing.T) {
	for _, args := range [][]string{{"-u"}, {"--token-file"}, {"-x", "set"}, {"-d"}} {
		a, _, errb := testApp(nil)
		if code := a.run(args); code != exitUsage || errb.Len() == 0 {
			t.Errorf("%v: code %d, stderr %q", args, code, errb)
		}
	}
}

func TestURLUserinfoNeverPrinted(t *testing.T) {
	const u = "http://user:PASSWD@127.0.0.1:9"
	a, out, errb := testApp(map[string]string{"BLINKENKEYS_TOKEN": "tok"})
	a.run([]string{"-v", "-u", u, "set", "-d", "d", "-k", "idx:0", "-c", "red"})
	if !strings.Contains(errb.String(), "endpoint") || strings.Contains(out.String()+errb.String(), "PASSWD") {
		t.Errorf("-v set leaked userinfo or logged nothing:\nstdout %s\nstderr %s", out, errb)
	}
	a, out, errb = testApp(map[string]string{"BLINKENKEYS_URL": u, "BLINKENKEYS_TOKEN": "tok", "KITTY_WINDOW_ID": "4"})
	if code := a.run([]string{"detect"}); code != 0 || !strings.Contains(out.String(), "127.0.0.1:9") ||
		strings.Contains(out.String()+errb.String(), "PASSWD") {
		t.Errorf("detect leaked userinfo (code %d):\nstdout %s\nstderr %s", code, out, errb)
	}
}

func TestDetectWithNothingStillReports(t *testing.T) {
	a, out, _ := testApp(map[string]string{"BLINKENKEYS_SOCKET": "/x.sock"})
	if code := a.run([]string{"detect"}); code != 0 || !strings.Contains(out.String(), "none") {
		t.Errorf("code %d:\n%s", code, out)
	}
}
