package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// globals are the options every command accepts, before or after the command name.
type globals struct {
	Socket, URL, Token, TokenFile, Device, Config string
	Verbose, Quiet                                bool
}

// newFlags returns a FlagSet with the global options registered (each as
// --long and -x). Errors are not printed by the flag package; parse reports them.
func (a *app) newFlags(name string) (*flag.FlagSet, *globals) {
	g := &globals{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	str(fs, &g.Socket, "socket", "S", "Unix socket of a local blinkenkeysd")
	str(fs, &g.URL, "url", "u", "remote blinkenkeysd, e.g. http://nas:49994")
	str(fs, &g.Token, "token", "t", "bearer token (visible in ps; prefer --token-file or $BLINKENKEYS_TOKEN)")
	fs.StringVar(&g.TokenFile, "token-file", "", "read the bearer token from this file")
	str(fs, &g.Device, "device", "d", "device name (default: configured, or the only device)")
	str(fs, &g.Config, "config", "C", "client config file (default ~/.config/blinkenkeys/blincli.yaml)")
	boolean(fs, &g.Verbose, "verbose", "v", "print resolved endpoint/device/key to stderr")
	boolean(fs, &g.Quiet, "quiet", "q", "suppress non-error output")
	return fs, g
}

func str(fs *flag.FlagSet, p *string, long, short, usage string) {
	fs.StringVar(p, long, "", usage)
	fs.StringVar(p, short, "", usage)
}

func boolean(fs *flag.FlagSet, p *bool, long, short, usage string) {
	fs.BoolVar(p, long, false, usage)
	fs.BoolVar(p, short, false, usage)
}

// parse parses args. done is true when the caller should return code
// immediately: -h/--help (code 0, usage on stdout) or a parse error (usage
// error, code 64 — never the flag package's own exit status of 2).
func (a *app) parse(fs *flag.FlagSet, args []string) (code int, done bool) {
	err := fs.Parse(args)
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, flag.ErrHelp):
		a.printUsage(a.stdout)
		return exitOK, true
	default:
		_, _ = fmt.Fprintf(a.stderr, "blincli: %v\n", err)
		_, _ = fmt.Fprintln(a.stderr, "try 'blincli --help'")
		return exitUsage, true
	}
}

func (a *app) printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: blincli [options] <command> [command options]

Commands:
  set       write a color / effect / template state to a key
  clear     blank a key and release it
  get       show a key's registration and current state
  devices   list devices, or show one device's layout
  detect    show what blincli would use (terminal, key, endpoint); sends nothing
  config    init | show | path
  version

Global options (accepted before or after the command):
  -S, --socket PATH      Unix socket of a local blinkenkeysd
  -u, --url URL          remote blinkenkeysd, e.g. http://nas:49994
  -t, --token TOKEN      bearer token (prefer --token-file or $BLINKENKEYS_TOKEN)
      --token-file FILE  read the bearer token from FILE
  -d, --device NAME      device (default: configured, or the only device)
  -C, --config FILE      client config (default ~/.config/blinkenkeys/blincli.yaml)
  -v, --verbose          print resolved endpoint/device/key to stderr
  -q, --quiet            suppress non-error output
  -h, --help

Exit codes: 0 ok, 1 daemon error, 64 usage, 66 key not registered,
69 daemon unreachable, 77 auth, 78 no config/endpoint.
`)
}
