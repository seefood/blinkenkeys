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
// The globals start from those parsed before the command name (a.pre), so a
// value given after the command overrides one given before it.
func (a *app) newFlags(name string) (*flag.FlagSet, *globals) {
	g := &globals{}
	if a.pre != nil {
		*g = *a.pre
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	str(fs, &g.Socket, "socket", "S", "Unix socket of a local blinkenkeysd")
	str(fs, &g.URL, "url", "u", "remote blinkenkeysd, e.g. http://nas:49994")
	str(fs, &g.Token, "token", "t", "bearer token (visible in ps; prefer --token-file or $BLINKENKEYS_TOKEN)")
	fs.StringVar(&g.TokenFile, "token-file", g.TokenFile, "read the bearer token from this file")
	str(fs, &g.Device, "device", "d", "device name (default: configured, or the only device)")
	str(fs, &g.Config, "config", "C", "client config file (default ~/.config/blinkenkeys/blincli.yaml)")
	boolean(fs, &g.Verbose, "verbose", "v", "print resolved endpoint/device/key to stderr")
	boolean(fs, &g.Quiet, "quiet", "q", "suppress non-error output")
	return fs, g
}

// str and boolean keep *p's current value as the default.
func str(fs *flag.FlagSet, p *string, long, short, usage string) {
	fs.StringVar(p, long, *p, usage)
	fs.StringVar(p, short, *p, usage)
}

func boolean(fs *flag.FlagSet, p *bool, long, short, usage string) {
	fs.BoolVar(p, long, *p, usage)
	fs.BoolVar(p, short, *p, usage)
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
		if _, ok := cmdSynopsis[fs.Name()]; ok {
			printCmdUsage(a.stdout, fs)
		} else {
			a.printUsage(a.stdout)
		}
		return exitOK, true
	default:
		_, _ = fmt.Fprintf(a.stderr, "blincli: %v\n", err)
		_, _ = fmt.Fprintln(a.stderr, "try 'blincli --help'")
		return exitUsage, true
	}
}

// cmdSynopsis is each command's usage line and one-line description, keyed
// by FlagSet name; a command listed here gets its own -h output.
var cmdSynopsis = map[string][2]string{
	"set":         {"[options] (-c COLOR | -e EFFECT | -s STATE)", "write a color / effect / template state to a key"},
	"clear":       {"[options]", "blank a key and release it"},
	"get":         {"[options]", "show a key's registration and current state"},
	"devices":     {"[options] [DEVICE]", "list devices, or show one device's layout"},
	"detect":      {"[options]", "show what blincli would use (terminal, key, endpoint); sends nothing"},
	"config init": {"[options]", "write blincli.yaml from the global options, or a template"},
	"config show": {"[options]", "print the client config with secrets masked"},
	"config path": {"[options]", "print the client config path in effect"},
}

// globalFlagNames are left out of per-command help (blincli --help lists them).
var globalFlagNames = map[string]bool{
	"socket": true, "S": true, "url": true, "u": true, "token": true, "t": true, "token-file": true,
	"device": true, "d": true, "config": true, "C": true, "verbose": true, "v": true, "quiet": true, "q": true,
}

// printCmdUsage prints fs's command-specific options. A long and a short
// flag registered with the same usage text are one option ("-c, --color").
func printCmdUsage(w io.Writer, fs *flag.FlagSet) {
	syn := cmdSynopsis[fs.Name()]
	_, _ = fmt.Fprintf(w, "Usage: blincli %s %s\n\n%s\n", fs.Name(), syn[0], syn[1])
	type opt struct{ short, long, arg, usage string }
	var opts []*opt
	byUsage := map[string]*opt{}
	fs.VisitAll(func(f *flag.Flag) {
		if globalFlagNames[f.Name] {
			return
		}
		o := byUsage[f.Usage]
		if o == nil {
			arg, usage := flag.UnquoteUsage(f)
			o = &opt{arg: arg, usage: usage}
			byUsage[f.Usage] = o
			opts = append(opts, o)
		}
		if len(f.Name) == 1 {
			o.short = "-" + f.Name
		} else {
			o.long = "--" + f.Name
		}
	})
	if len(opts) > 0 {
		_, _ = fmt.Fprintln(w, "\nOptions:")
	}
	for _, o := range opts {
		names := "    " + o.long
		switch {
		case o.long == "":
			names = o.short
		case o.short != "":
			names = o.short + ", " + o.long
		}
		if o.arg != "" {
			names += " " + o.arg
		}
		_, _ = fmt.Fprintf(w, "  %-24s %s\n", names, o.usage)
	}
	_, _ = fmt.Fprintln(w, "\nGlobal options and exit codes: see 'blincli --help'.")
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
69 daemon unreachable, 77 auth, 78 no endpoint or invalid config.
`)
}
