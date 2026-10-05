package client

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/config"
)

// Sentinels the command layer maps to exit codes.
var (
	ErrNoEndpoint  = errors.New("no blinkenkeysd endpoint configured") // exit 78
	ErrUnreachable = errors.New("blinkenkeysd unreachable")             // exit 69
	ErrAuth        = errors.New("bearer token missing or rejected")     // exit 77
	ErrUsage       = errors.New("usage error")                          // exit 64
)

// Options are the endpoint-related command-line values.
type Options struct {
	Socket, URL, Token, TokenFile, ConfigPath string
}

// Endpoint is a resolved daemon address.
type Endpoint struct {
	Kind    string // "unix" or "http"
	Socket  string
	BaseURL string
	Token   string
	Source  string // where it came from, for -v
}

// Remote reports whether the endpoint is a network address (not the local socket).
func (e Endpoint) Remote() bool { return e.Kind == "http" }

// String renders the endpoint for -v/show output; the token is never included.
func (e Endpoint) String() string {
	addr := e.BaseURL
	if e.Kind == "unix" {
		addr = e.Socket
	}
	return fmt.Sprintf("%s %s (from %s)", e.Kind, addr, e.Source)
}

// NoEndpointError carries what was looked at, so its message can say how to fix it.
type NoEndpointError struct {
	ConfigPath   string
	ConfigExists bool
	SocketPath   string
}

func (e *NoEndpointError) Is(target error) bool { return target == ErrNoEndpoint }

func (e *NoEndpointError) Error() string {
	cfg := "no config at " + e.ConfigPath
	if e.ConfigExists {
		cfg = "config " + e.ConfigPath + " sets no url or socket"
	}
	return fmt.Sprintf(`blincli: no blinkenkeysd endpoint: no --url/--socket, %s, and no daemon socket at %s

Create a config with one of:
  blincli config init                          write an annotated template, then edit it
  blincli config init --url http://HOST:49994 --token-file FILE [--device NAME]
  blincli config init --interactive            prompt for the values (requires a terminal)
or pass --url/--socket on each call, or set BLINKENKEYS_URL / BLINKENKEYS_SOCKET.`, cfg, e.SocketPath)
}

// Resolver finds the daemon: flags > environment > client config > local socket.
type Resolver struct {
	Getenv func(string) string
	Home   string
}

// ConfigPath is the client config path in effect.
func (r Resolver) ConfigPath(o Options) string {
	if o.ConfigPath != "" {
		return o.ConfigPath
	}
	return DefaultConfigPath(r.Getenv, r.Home)
}

// Resolve returns the endpoint and the loaded client config (zero if none).
func (r Resolver) Resolve(o Options) (Endpoint, FileConfig, error) {
	path := r.ConfigPath(o)
	fc, exists, err := LoadFile(path)
	if err != nil {
		return Endpoint{}, fc, err
	}
	layers := []struct{ url, socket, src string }{
		{o.URL, o.Socket, "command line"},
		{r.Getenv("BLINKENKEYS_URL"), r.Getenv("BLINKENKEYS_SOCKET"), "environment"},
		{fc.URL, fc.Socket, path},
	}
	for _, l := range layers {
		switch {
		case l.url != "" && l.socket != "":
			return Endpoint{}, fc, fmt.Errorf("%w: %s sets both a url and a socket", ErrUsage, l.src)
		case l.url != "":
			ep, err := r.httpEndpoint(l.url, l.src, o, fc)
			return ep, fc, err
		case l.socket != "":
			return Endpoint{Kind: "unix", Socket: ExpandHome(l.socket, r.Home), Source: l.src}, fc, nil
		}
	}
	return r.probeLocal(path, exists, fc)
}

func (r Resolver) httpEndpoint(raw, src string, o Options, fc FileConfig) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Endpoint{}, fmt.Errorf("%w: %q is not an http(s) URL", ErrUsage, raw)
	}
	tok, err := r.token(o, fc)
	if err != nil {
		return Endpoint{}, err
	}
	if tok == "" {
		return Endpoint{}, fmt.Errorf("%w: a token is required for %s (use --token-file, $BLINKENKEYS_TOKEN, or token/token_file in the config)", ErrAuth, raw)
	}
	return Endpoint{Kind: "http", BaseURL: strings.TrimRight(raw, "/"), Token: tok, Source: src}, nil
}

func (r Resolver) token(o Options, fc FileConfig) (string, error) {
	if o.Token != "" {
		return o.Token, nil
	}
	if o.TokenFile != "" {
		return r.readToken(o.TokenFile)
	}
	if t := r.Getenv("BLINKENKEYS_TOKEN"); t != "" {
		return t, nil
	}
	if fc.Token != "" {
		return fc.Token, nil
	}
	if fc.TokenFile != "" {
		return r.readToken(fc.TokenFile)
	}
	return "", nil
}

func (r Resolver) readToken(path string) (string, error) {
	b, err := os.ReadFile(ExpandHome(path, r.Home)) // #nosec G304 -- operator-supplied token file
	if err != nil {
		return "", fmt.Errorf("%w: reading token file: %v", ErrAuth, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// localSocketPath is the daemon's socket: its config.yaml's
// listeners.socket.path if a readable one exists, else the default. (Mirrors
// cmd/blinkenkeysd's resolveSocketPath.)
func (r Resolver) localSocketPath() string {
	if cfg, err := config.LoadDir(config.Dir(r.Getenv, r.Home)); err == nil && cfg.Listeners.Socket.Path != "" {
		return ExpandHome(cfg.Listeners.Socket.Path, r.Home)
	}
	return filepath.Join(r.Home, ".local", "state", "blinkenkeys", "api.sock")
}

func (r Resolver) probeLocal(cfgPath string, cfgExists bool, fc FileConfig) (Endpoint, FileConfig, error) {
	sock := r.localSocketPath()
	if conn, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		_ = conn.Close()
		return Endpoint{Kind: "unix", Socket: sock, Source: "local socket"}, fc, nil
	}
	if _, err := os.Stat(sock); err == nil {
		return Endpoint{}, fc, fmt.Errorf("%w: socket %s exists but nothing answers (is blinkenkeysd running?)", ErrUnreachable, sock)
	}
	return Endpoint{}, fc, &NoEndpointError{ConfigPath: cfgPath, ConfigExists: cfgExists, SocketPath: sock}
}
