package client

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resolverFor(t *testing.T, env map[string]string) (Resolver, string) {
	t.Helper()
	home := t.TempDir()
	return Resolver{Getenv: func(k string) string { return env[k] }, Home: home}, home
}

func writeCfg(t *testing.T, home, body string) {
	t.Helper()
	p := DefaultConfigPath(func(string) string { return "" }, home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	r, home := resolverFor(t, map[string]string{"BLINKENKEYS_URL": "http://env:1", "BLINKENKEYS_TOKEN": "envtok"})
	writeCfg(t, home, "url: http://file:2\ntoken: filetok\n")

	ep, _, err := r.Resolve(Options{URL: "http://flag:3", Token: "flagtok"})
	if err != nil || ep.BaseURL != "http://flag:3" || ep.Token != "flagtok" {
		t.Errorf("flags: %+v %v", ep, err)
	}
	ep, _, err = r.Resolve(Options{})
	if err != nil || ep.BaseURL != "http://env:1" || ep.Token != "envtok" {
		t.Errorf("env: %+v %v", ep, err)
	}
	r.Getenv = func(string) string { return "" }
	ep, fc, err := r.Resolve(Options{})
	if err != nil || ep.BaseURL != "http://file:2" || ep.Token != "filetok" || fc.URL != "http://file:2" || !ep.Remote() {
		t.Errorf("file: %+v %v", ep, err)
	}
}

func TestResolveTokenFileAndMissingToken(t *testing.T) {
	r, home := resolverFor(t, nil)
	tok := filepath.Join(home, "tok")
	_ = os.WriteFile(tok, []byte("  secret\n"), 0o600)
	ep, _, err := r.Resolve(Options{URL: "http://h:1", TokenFile: tok})
	if err != nil || ep.Token != "secret" {
		t.Errorf("token file: %+v %v", ep, err)
	}
	if _, _, err := r.Resolve(Options{URL: "http://h:1"}); !errors.Is(err, ErrAuth) {
		t.Errorf("no token for http: err = %v, want ErrAuth", err)
	}
	if _, _, err := r.Resolve(Options{URL: "not a url"}); !errors.Is(err, ErrUsage) {
		t.Errorf("bad url: err = %v, want ErrUsage", err)
	}
	if _, _, err := r.Resolve(Options{URL: "http://h:1", Socket: "/s"}); !errors.Is(err, ErrUsage) {
		t.Errorf("both url and socket: err = %v, want ErrUsage", err)
	}
}

// Review Focus 5: a remote URL without a token is rejected at resolve time
// (before any request can be built), including https, and an unreadable token
// file is also ErrAuth; error text must not leak a token.
func TestResolveRemoteWithoutTokenRejected(t *testing.T) {
	r, home := resolverFor(t, nil)
	for _, u := range []string{"http://h:1", "https://h.example/api"} {
		ep, _, err := r.Resolve(Options{URL: u})
		if !errors.Is(err, ErrAuth) || ep != (Endpoint{}) {
			t.Errorf("%s: ep=%+v err=%v, want zero endpoint and ErrAuth", u, ep, err)
		}
	}
	if _, _, err := r.Resolve(Options{URL: "http://h:1", TokenFile: filepath.Join(home, "missing")}); !errors.Is(err, ErrAuth) {
		t.Errorf("missing token file: err = %v, want ErrAuth", err)
	}
	// file-level url without any token is rejected too
	writeCfg(t, home, "url: http://file:2\n")
	if _, _, err := r.Resolve(Options{}); !errors.Is(err, ErrAuth) {
		t.Errorf("config url without token: err = %v, want ErrAuth", err)
	}
}

func TestResolveTokenPrecedenceAndNoLeak(t *testing.T) {
	r, home := resolverFor(t, map[string]string{"BLINKENKEYS_TOKEN": "envtok"})
	tokf := filepath.Join(home, "tok")
	_ = os.WriteFile(tokf, []byte("filetok\n"), 0o600)
	writeCfg(t, home, "token: cfgtok\ntoken_file: "+tokf+"\n")
	// flag token-file beats env beats config token beats config token_file
	ep, _, err := r.Resolve(Options{URL: "http://h:1", TokenFile: tokf})
	if err != nil || ep.Token != "filetok" {
		t.Errorf("flag token file: %+v %v", ep, err)
	}
	ep, _, err = r.Resolve(Options{URL: "http://h:1"})
	if err != nil || ep.Token != "envtok" {
		t.Errorf("env: %+v %v", ep, err)
	}
	r.Getenv = func(string) string { return "" }
	ep, _, err = r.Resolve(Options{URL: "http://h:1"})
	if err != nil || ep.Token != "cfgtok" {
		t.Errorf("cfg token: %+v %v", ep, err)
	}
	writeCfg(t, home, "token_file: ~/tok\n")
	ep, _, err = r.Resolve(Options{URL: "http://h:1"})
	if err != nil || ep.Token != "filetok" {
		t.Errorf("cfg token_file with ~: %+v %v", ep, err)
	}
	// a token passed with a bad URL must not appear in the error
	_, _, err = r.Resolve(Options{URL: "bogus", Token: "TOPSECRET"})
	if err == nil || strings.Contains(err.Error(), "TOPSECRET") {
		t.Errorf("error leaks token or is nil: %v", err)
	}
	// Endpoint's default formatting must not print the token either
	if s := (Endpoint{Kind: "http", BaseURL: "http://h:1", Token: "TOPSECRET"}).String(); strings.Contains(s, "TOPSECRET") {
		t.Errorf("Endpoint.String leaks token: %s", s)
	}
}

func TestRedactionAllVerbs(t *testing.T) {
	const secret = "TOPSECRET"
	ep := Endpoint{Kind: "http", BaseURL: "http://u:" + secret + "pw@h:1", Token: secret, Source: "x"}
	fc := FileConfig{URL: "http://h:1", Token: secret, Device: "d"}
	for _, v := range []any{ep, &ep, fc, &fc} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
			if s := fmt.Sprintf(verb, v); strings.Contains(s, secret) {
				t.Errorf("%s of %T leaks secret: %s", verb, v, s)
			}
		}
	}
}

func TestResolveErrorsRedactURLUserinfo(t *testing.T) {
	r, _ := resolverFor(t, nil)
	for _, raw := range []string{"http://user:PASSWORD@h:1", "ftp://user:PASSWORD@h:1", "http://user:PASSWORD@"} {
		_, _, err := r.Resolve(Options{URL: raw})
		if err == nil || strings.Contains(err.Error(), "PASSWORD") {
			t.Errorf("%s: err = %v", raw, err)
		}
	}
}

func TestResolveTokenFileErrorKeepsCause(t *testing.T) {
	r, home := resolverFor(t, nil)
	_, _, err := r.Resolve(Options{URL: "http://h:1", TokenFile: filepath.Join(home, "missing")})
	if !errors.Is(err, ErrAuth) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want ErrAuth and fs.ErrNotExist", err)
	}
}

func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "bk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func TestResolveExplicitSocketNoToken(t *testing.T) {
	r, _ := resolverFor(t, nil)
	ep, _, err := r.Resolve(Options{Socket: "/x/api.sock"})
	if err != nil || ep.Kind != "unix" || ep.Socket != "/x/api.sock" || ep.Remote() {
		t.Errorf("%+v %v", ep, err)
	}
}

func TestResolveLocalSocketProbe(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on unix socket: %v", err)
	}
	defer ln.Close()
	r, home := resolverFor(t, nil)
	// the daemon's own config.yaml names the socket
	cfgDir := filepath.Join(home, ".config", "blinkenkeys")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("listeners:\n  socket:\n    path: "+sock+"\n"), 0o600)
	ep, _, err := r.Resolve(Options{})
	if err != nil || ep.Kind != "unix" || ep.Socket != sock {
		t.Errorf("probe: %+v %v", ep, err)
	}
}

func TestResolveDeadSocketIsUnreachable(t *testing.T) {
	r, home := resolverFor(t, nil)
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on unix socket: %v", err)
	}
	_ = ln.Close() // file remains (Go removes it on Close for listeners it created; recreate as plain file)
	_ = os.WriteFile(sock, nil, 0o600)
	cfgDir := filepath.Join(home, ".config", "blinkenkeys")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("listeners:\n  socket:\n    path: "+sock+"\n"), 0o600)
	if _, _, err := r.Resolve(Options{}); !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestResolveNothingConfigured(t *testing.T) {
	r, home := resolverFor(t, nil)
	_, _, err := r.Resolve(Options{})
	if !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("err = %v, want ErrNoEndpoint", err)
	}
	msg := err.Error()
	for _, want := range []string{"blincli config init", "--interactive", "--url", filepath.Join(home, ".config", "blinkenkeys", "blincli.yaml")} {
		if !strings.Contains(msg, want) {
			t.Errorf("help text lacks %q:\n%s", want, msg)
		}
	}
	// a config file that exists but names no endpoint is still "no endpoint"
	writeCfg(t, home, "# nothing set\n")
	if _, _, err := r.Resolve(Options{}); !errors.Is(err, ErrNoEndpoint) {
		t.Errorf("empty config: err = %v", err)
	}
}

func TestRedactURLExported(t *testing.T) {
	if got := RedactURL("http://u:pw@h:1"); strings.Contains(got, "pw") {
		t.Errorf("RedactURL leaked: %q", got)
	}
}
