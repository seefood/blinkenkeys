// Command blincli is a client for blinkenkeysd: it finds the daemon, works
// out which key belongs to the calling terminal tab, and wraps the REST API
// in GNU-style options. It is built to run from hooks: it never prompts
// (except `config init --interactive`) and never exits with code 2.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/client"
	"github.com/seefood/blinkenkeys/internal/termid"
)

// Exit codes follow sysexits.h and deliberately skip 2, which Claude Code
// hooks treat as a blocking error.
const (
	exitOK          = 0
	exitFail        = 1
	exitUsage       = 64
	exitNotFound    = 66
	exitUnavailable = 69
	exitNoPerm      = 77
	exitConfig      = 78
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const requestTimeout = 10 * time.Second

type app struct {
	getenv         func(string) string
	home, host     string
	stdin          io.Reader
	stdout, stderr io.Writer
	isTTY          func() bool
	termEnv        termid.Env
	pre            *globals // global options given before the command name
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	a := &app{
		getenv: os.Getenv, home: home, host: host,
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		isTTY: func() bool {
			fi, err := os.Stdin.Stat()
			return err == nil && fi.Mode()&os.ModeCharDevice != 0
		},
		termEnv: termid.Env{Getenv: os.Getenv, Run: termid.ExecRun},
	}
	os.Exit(a.run(os.Args[1:]))
}

var commands = map[string]func(*app, []string) int{
	"version": (*app).cmdVersion,
	"detect":  (*app).cmdDetect,
}

// run executes one invocation. A panic becomes exit 1 rather than the Go
// runtime's exit 2, which Claude Code hooks would treat as blocking.
func (a *app) run(args []string) (code int) {
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprintf(a.stderr, "blincli: internal error: %v\n", r)
			code = exitFail
		}
	}()
	fs, g := a.newFlags("blincli")
	if code, done := a.parse(fs, args); done {
		return code
	}
	a.pre = g
	rest := fs.Args()
	if len(rest) == 0 {
		a.printUsage(a.stderr)
		return exitUsage
	}
	cmd, ok := commands[rest[0]]
	if !ok {
		_, _ = fmt.Fprintf(a.stderr, "blincli: unknown command %q\n", rest[0])
		a.printUsage(a.stderr)
		return exitUsage
	}
	return cmd(a, rest[1:])
}

func (a *app) cmdVersion(args []string) int {
	_, _ = fmt.Fprintf(a.stdout, "blincli %s\n", version)
	return exitOK
}

// exitCode maps an error to the exit code in the table above.
func exitCode(err error) int {
	var ae *client.APIError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, client.ErrNoEndpoint), errors.Is(err, client.ErrBadConfig):
		return exitConfig
	case errors.Is(err, client.ErrUnreachable):
		return exitUnavailable
	case errors.Is(err, client.ErrAuth):
		return exitNoPerm
	case errors.Is(err, client.ErrUsage):
		return exitUsage
	case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
		return exitNotFound
	default:
		return exitFail
	}
}

// fail prints err and returns its exit code.
func (a *app) fail(err error) int {
	if err == nil {
		return exitOK
	}
	_, _ = fmt.Fprintln(a.stderr, err)
	return exitCode(err)
}

func (a *app) vlog(g *globals, format string, args ...any) {
	if g.Verbose {
		_, _ = fmt.Fprintf(a.stderr, "blincli: "+format+"\n", args...)
	}
}

// resolver is the endpoint resolver; config-permission warnings go to stderr.
func (a *app) resolver() client.Resolver {
	return client.Resolver{Getenv: a.getenv, Home: a.home, Warn: func(w string) { _, _ = fmt.Fprintln(a.stderr, w) }}
}

// session resolves the endpoint and builds a client.
func (a *app) session(g *globals) (*client.Client, client.Endpoint, client.FileConfig, error) {
	r := a.resolver()
	ep, fc, err := r.Resolve(client.Options{Socket: g.Socket, URL: g.URL, Token: g.Token, TokenFile: g.TokenFile, ConfigPath: g.Config})
	if err != nil {
		return nil, ep, fc, err
	}
	where := ep.Socket
	if ep.Remote() {
		where = client.RedactURL(ep.BaseURL)
	}
	a.vlog(g, "endpoint %s (%s)", where, ep.Source)
	return client.New(ep), ep, fc, nil
}

// device picks the device: -d, $BLINKENKEYS_DEVICE, config, or the only one.
func (a *app) device(ctx context.Context, g *globals, fc client.FileConfig, cl *client.Client) (string, error) {
	for _, d := range []string{g.Device, a.getenv("BLINKENKEYS_DEVICE"), fc.Device} {
		if d != "" {
			return d, nil
		}
	}
	devs, err := cl.Devices(ctx)
	if err != nil {
		return "", err
	}
	switch len(devs) {
	case 0:
		return "", errors.New("blincli: the daemon reports no devices")
	case 1:
		return devs[0].Name, nil
	}
	names := make([]string, len(devs))
	for i, d := range devs {
		names[i] = d.Name
	}
	return "", fmt.Errorf("%w: %d devices; choose one with -d: %s", client.ErrUsage, len(devs), strings.Join(names, ", "))
}

// planKey works out the key strategy from -k/-n/env/terminal (no network).
func (a *app) planKey(ctx context.Context, key, name string) (client.Plan, termid.Identity, error) {
	if key == "" {
		key = a.getenv("BLINKENKEYS_KEY")
	}
	id := termid.Detect(ctx, a.termEnv)
	fallback, _ := termid.FallbackName(a.getenv)
	plan, err := client.PlanKey(client.KeyInput{Key: key, Name: name, ID: id, Fallback: fallback})
	return plan, id, err
}

func (a *app) cmdDetect(args []string) int {
	fs, g := a.newFlags("detect")
	if code, done := a.parse(fs, args); done {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	plan, id, perr := a.planKey(ctx, "", "")
	if id.Terminal == "" {
		_, _ = fmt.Fprintln(a.stdout, "terminal   none recognized")
	} else {
		tab := "unknown"
		if id.Tab > 0 {
			tab = fmt.Sprint(id.Tab)
		}
		_, _ = fmt.Fprintf(a.stdout, "terminal   %s (instance %q, tab %s)\n", id.Terminal, id.InstanceID, tab)
	}
	switch {
	case perr != nil:
		_, _ = fmt.Fprintf(a.stdout, "key        none (%v)\n", perr)
	case plan.Mode == client.ModeSlot:
		_, _ = fmt.Fprintf(a.stdout, "key        tab %d -> a tab slot (owner %s)\n", plan.Tab, plan.Owner)
	default:
		_, _ = fmt.Fprintf(a.stdout, "key        name %s (claimed from the pool; shared slot if it is full)\n", plan.Name)
	}
	r := a.resolver()
	ep, _, err := r.Resolve(client.Options{Socket: g.Socket, URL: g.URL, Token: g.Token, TokenFile: g.TokenFile, ConfigPath: g.Config})
	if err != nil {
		_, _ = fmt.Fprintf(a.stdout, "transport  none (%v)\n", firstLine(err.Error()))
		return exitOK
	}
	where := ep.Socket
	if ep.Remote() {
		where = client.RedactURL(ep.BaseURL)
	}
	_, _ = fmt.Fprintf(a.stdout, "transport  %s %s  (%s)\n", ep.Kind, where, ep.Source)
	return exitOK
}

func firstLine(s string) string { line, _, _ := strings.Cut(s, "\n"); return line }
