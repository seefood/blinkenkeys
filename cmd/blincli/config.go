package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/seefood/blinkenkeys/internal/client"
)

func init() { commands["config"] = (*app).cmdConfig }

func (a *app) cmdConfig(args []string) int {
	if len(args) == 0 {
		return a.fail(fmt.Errorf("%w: config needs a subcommand: init, show or path", client.ErrUsage))
	}
	switch args[0] {
	case "init":
		return a.configInit(args[1:])
	case "show":
		return a.configShow(args[1:])
	case "path":
		return a.configPath(args[1:])
	default:
		return a.fail(fmt.Errorf("%w: unknown config subcommand %q", client.ErrUsage, args[0]))
	}
}

func (a *app) configPathFor(g *globals) string {
	return client.Resolver{Getenv: a.getenv, Home: a.home}.ConfigPath(client.Options{ConfigPath: g.Config})
}

func (a *app) configPath(args []string) int {
	fs, g := a.newFlags("config path")
	if code, done := a.parse(fs, args); done {
		return code
	}
	_, _ = fmt.Fprintln(a.stdout, a.configPathFor(g))
	return exitOK
}

func (a *app) configShow(args []string) int {
	fs, g := a.newFlags("config show")
	if code, done := a.parse(fs, args); done {
		return code
	}
	path := a.configPathFor(g)
	fc, exists, err := client.LoadFile(path)
	if err != nil {
		return a.fail(err)
	}
	if !exists {
		_, _ = fmt.Fprintf(a.stdout, "no config at %s\n", path)
		return exitOK
	}
	if w := client.PermWarning(path, fc); w != "" {
		_, _ = fmt.Fprintln(a.stderr, w)
	}
	_, _ = fmt.Fprintf(a.stdout, "# %s\n%s", path, renderConfig(client.FileConfig{
		URL: client.RedactURL(fc.URL), Socket: fc.Socket, Token: maskIf(fc.Token), TokenFile: fc.TokenFile, Device: fc.Device, Slots: fc.Slots,
	}))
	return exitOK
}

func maskIf(s string) string {
	if s == "" {
		return ""
	}
	return "****"
}

// renderConfig writes every key; unset ones are commented-out examples, so
// an all-unset config is the template and still parses as "unconfigured".
func renderConfig(fc client.FileConfig) string {
	var b strings.Builder
	line := func(key, val, example string) {
		if val != "" {
			fmt.Fprintf(&b, "%s: %s\n", key, strconv.Quote(val))
		} else {
			fmt.Fprintf(&b, "# %s: %s\n", key, example)
		}
	}
	b.WriteString("# blincli client config. Set url (remote daemon) OR socket (local daemon);\n")
	b.WriteString("# with neither, blincli probes the local daemon socket.\n")
	line("url", fc.URL, `"http://HOST:49994"`)
	line("socket", fc.Socket, `"~/.local/state/blinkenkeys/api.sock"`)
	line("token_file", fc.TokenFile, `"~/.config/blinkenkeys/token"   # required for url; or token: "..."`)
	line("token", fc.Token, `"..."`)
	line("device", fc.Device, `"uid-xxxxxxxxxxxxxxxx"   # default: the only device`)
	if fc.Slots > 0 {
		fmt.Fprintf(&b, "slots: %d\n", fc.Slots)
	} else {
		b.WriteString("# slots: 6   # tab-slot count override; default comes from the daemon's keys.tabs, else 6\n")
	}
	return b.String()
}

func (a *app) configInit(args []string) int {
	fs, g := a.newFlags("config init")
	var interactive, force bool
	var slots int
	boolean(fs, &interactive, "interactive", "i", "prompt for the values (requires a terminal)")
	boolean(fs, &force, "force", "f", "overwrite an existing config")
	fs.IntVar(&slots, "slots", 0, "tab-slot count override")
	fs.IntVar(&slots, "m", 0, "tab-slot count override")
	if code, done := a.parse(fs, args); done {
		return code
	}
	path := a.configPathFor(g)
	// Lstat: a symlink (even a dangling one) counts as existing.
	switch _, err := os.Lstat(path); {
	case err == nil && !force:
		return a.fail(fmt.Errorf("%w: %s already exists (use --force to overwrite)", client.ErrUsage, path))
	case err != nil && !errors.Is(err, iofs.ErrNotExist):
		return a.fail(err)
	}
	fc := client.FileConfig{URL: g.URL, Socket: g.Socket, Token: g.Token, TokenFile: g.TokenFile, Device: g.Device, Slots: slots}
	if interactive {
		if !a.isTTY() {
			return a.fail(fmt.Errorf("%w: --interactive needs a terminal on stdin", client.ErrUsage))
		}
		a.prompt(&fc)
	}
	if fc.Device == "" && (fc.URL != "" || fc.Socket != "") {
		fc.Device = a.probeDevice(fc)
	}
	if err := writeConfig(path, renderConfig(fc), force); errors.Is(err, iofs.ErrExist) {
		return a.fail(fmt.Errorf("%w: %s already exists (use --force to overwrite)", client.ErrUsage, path))
	} else if err != nil {
		return a.fail(err)
	}
	if !g.Quiet {
		_, _ = fmt.Fprintf(a.stderr, "wrote %s\n", path)
		if fc == (client.FileConfig{}) {
			_, _ = fmt.Fprintln(a.stderr, "it is a template: edit it to set url (or socket) and a token, then run 'blincli devices'")
		}
	}
	return exitOK
}

// prompt fills fc from stdin lines: endpoint, token file (remote only), device.
func (a *app) prompt(fc *client.FileConfig) {
	sc := bufio.NewScanner(a.stdin)
	ask := func(q string) string {
		_, _ = fmt.Fprint(a.stderr, q)
		if sc.Scan() {
			return strings.TrimSpace(sc.Text())
		}
		return ""
	}
	if fc.URL == "" && fc.Socket == "" {
		switch ans := ask("daemon URL (http://host:49994) or socket path, blank for the local socket: "); {
		case strings.HasPrefix(ans, "http://") || strings.HasPrefix(ans, "https://"):
			fc.URL = ans
		case ans != "":
			fc.Socket = ans
		}
	}
	if fc.URL != "" && fc.Token == "" && fc.TokenFile == "" {
		fc.TokenFile = ask("file containing the bearer token: ")
	}
	if fc.Device == "" {
		fc.Device = ask("device name, blank to auto-detect the only device: ")
	}
}

// probeDevice asks the daemon for its devices; exactly one is returned, else "".
// Failures only produce a note: init must still write the file.
func (a *app) probeDevice(fc client.FileConfig) string {
	r := client.Resolver{Getenv: a.getenv, Home: a.home}
	ep, _, err := r.Resolve(client.Options{Socket: fc.Socket, URL: fc.URL, Token: fc.Token, TokenFile: fc.TokenFile, ConfigPath: os.DevNull})
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "note: could not probe the daemon for devices: %s\n", firstLine(err.Error()))
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	devs, err := client.New(ep).Devices(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "note: could not list devices: %s\n", firstLine(err.Error()))
		return ""
	}
	if len(devs) == 1 {
		return devs[0].Name
	}
	return ""
}

// writeConfig writes content to path via a temp file (created 0600 by
// CreateTemp), so the final mode is always 0600 and an existing symlink is
// replaced rather than written through. With force it renames over path;
// without, it hard-links the temp file into place, which fails with
// fs.ErrExist if anything (even a dangling symlink) appeared at path since
// the caller checked. The temp file never outlives the call.
func writeConfig(path, content string, force bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".blincli-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after a successful rename
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if force {
		return os.Rename(tmp, path)
	}
	return os.Link(tmp, path)
}
