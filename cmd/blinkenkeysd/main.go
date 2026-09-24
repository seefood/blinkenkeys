package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	goHid "github.com/sstallion/go-hid"

	"github.com/seefood/blinkenkeys/config"
	"github.com/seefood/blinkenkeys/internal/api"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/hid"
)

const (
	dispatcherQueueDepth  = 64
	enumeratePollInterval = 1 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	warnIfRootFallback(runtime.GOOS, os.Geteuid(), logger)

	checkOnly := flag.Bool("check-config", false, "validate config.yaml, effects/ and templates/ in the config dir, then exit")
	var configDirFlag string
	const configUsage = "path to the config directory (config.yaml, effects/, templates/); default: ${XDG_CONFIG_HOME:-~/.config}/blinkenkeys"
	flag.StringVar(&configDirFlag, "config", "", configUsage)
	flag.StringVar(&configDirFlag, "c", "", configUsage)
	flag.Usage = gnuUsage
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	cfgDir, dirErr := resolveConfigDir(configDirFlag, os.Getenv, home)
	if dirErr != nil {
		if *checkOnly {
			fmt.Fprintln(os.Stderr, dirErr)
		} else {
			logger.Error("config dir resolution failed", "err", dirErr)
		}
		os.Exit(1)
	}
	cfg, lib, err := loadAll(cfgDir)
	if *checkOnly {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}
	if err != nil {
		logger.Error("config load failed", "dir", cfgDir, "err", err)
		os.Exit(1)
	}

	if err := goHid.Init(); err != nil {
		logger.Error("hid init failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = goHid.Exit() }()

	registry := dispatcher.NewRegistry()
	for _, d := range cfg.Devices {
		if d.Optional {
			registry.Declare(d.ID)
		}
	}
	cache := dispatcher.NewCache()
	disp := dispatcher.New(registry, cache, dispatcherQueueDepth, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

	state := newDeviceState()
	// Initial enumeration, synchronous, so devices are known before HTTP
	// traffic starts.
	res := registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)
	logAdded(logger, res.Added)
	syncDevices(ctx, registry, disp, res.Reconnected, logger)

	go pollForDevices(ctx, state, registry, cache, disp, logger)
	go disp.RunPeriodicRedraw(ctx, dispatcher.RedrawInterval)

	engine := effects.NewEngine(disp, logger)
	go engine.Run(ctx, effects.TickInterval)
	handler := api.NewHandler(disp, engine, lib)

	// socketPath comes from config.yaml's listeners.socket.path or the
	// $HOME-derived default — gosec's taint analysis treats config files as
	// untrusted input reaching a filesystem path, but the only party who can
	// set it here is the same local user running the daemon, so there is no
	// privilege boundary being crossed.
	socketPath := resolveSocketPath(cfg.Listeners.Socket.Path, home)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil { // #nosec G703 -- socketPath is config.yaml or the $HOME-derived default, not attacker-controlled
		logger.Error("could not create socket dir", "err", err)
		os.Exit(1)
	}
	// Only remove a stale socket left by a previous crashed run — never an
	// arbitrary file a misconfigured listeners.socket.path happens to name.
	if fi, statErr := os.Lstat(socketPath); statErr == nil && fi.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(socketPath) // #nosec G703 -- socketPath is config.yaml or the $HOME-derived default, not attacker-controlled; Mode() check above confirms it's a socket
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		logger.Error("could not listen on unix socket", "err", err)
		os.Exit(1)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil { // #nosec G703 -- socketPath is config.yaml or the $HOME-derived default, not attacker-controlled
		logger.Error("could not chmod socket", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Handler:           handler.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Info("blinkenkeysd listening", "socket", socketPath)
	if err := srv.Serve(listener); err != nil {
		logger.Error("http server exited", "err", err)
		os.Exit(1)
	}
}

// flagAliases maps a flag's canonical multi-letter name to its single-letter
// GNU shorthand. gnuUsage prints the pair as one combined "-x, --name" entry
// instead of two separate flags.
var flagAliases = map[string]string{"config": "c"}

// gnuUsage replaces flag's default Usage, which always renders a single
// leading dash regardless of name length: GNU convention reserves that for
// single-letter names ("-c") and uses a double dash for multi-letter ones
// ("--config", "--check-config").
func gnuUsage() {
	out := flag.CommandLine.Output()
	_, _ = fmt.Fprintf(out, "Usage of %s:\n", os.Args[0])
	shorthands := make(map[string]bool, len(flagAliases))
	for _, short := range flagAliases {
		shorthands[short] = true
	}
	flag.VisitAll(func(f *flag.Flag) {
		if shorthands[f.Name] {
			return // printed alongside its long form below
		}
		name := "--" + f.Name
		if len(f.Name) == 1 {
			name = "-" + f.Name
		}
		if short, ok := flagAliases[f.Name]; ok {
			name = "-" + short + ", --" + f.Name
		}
		_, _ = fmt.Fprintf(out, "  %s\n    \t%s\n", name, f.Usage)
	})
}

// warnIfRootFallback logs a prominent warning when blinkenkeysd is running as
// root on Linux — the design spec's fallback path for when no udev rule can
// be installed. Unlike the old connectord/restd split, there is no
// unprivileged process left to isolate the HTTP surface behind: root here
// means the whole daemon, including any future TCP listener, runs as root.
// Per the design spec's ban on syscall.Setuid/Setgid on a running process
// (golang/go#1435), blinkenkeysd cannot safely de-escalate itself even if it
// wanted to — this function only warns, it never attempts to drop privilege.
func warnIfRootFallback(goos string, euid int, logger *slog.Logger) {
	if goos != "linux" || euid != 0 {
		return
	}
	logger.Warn("blinkenkeysd is running as root — this is the udev-rule-unavailable " +
		"fallback and runs the entire HTTP surface (including any future TCP " +
		"listener) as root too; install a udev rule granting the logged-in user " +
		"access to the device instead, per the design spec's Background section")
}

// openDevice pairs an open hid.Controller with the Identity it was opened
// with, so deviceState can rebuild its dispatcher.PresentDevice set on every
// poll without re-probing (and therefore re-opening) a path it already holds
// open — HID opens are exclusive on macOS.
type openDevice struct {
	identity hid.Identity
	ctrl     hid.Controller
}

// deviceState is cmd/blinkenkeysd's bookkeeping of currently open HID paths,
// independent of internal/dispatcher.Registry's name-keyed view (a single
// physical device's path is stable across polls; its assigned name is not
// recomputed unless the whole present-device set changes composition).
type deviceState struct {
	openByPath map[string]openDevice
}

func newDeviceState() *deviceState {
	return &deviceState{openByPath: make(map[string]openDevice)}
}

// refresh re-enumerates raw HID paths, opens newly-appeared ones, closes
// controllers for paths that disappeared, and returns every currently open
// device as a dispatcher.PresentDevice, ready for Registry.Reconcile — naming
// itself (including dedup suffixing and reconnect/"untethered" rewire
// matching) is entirely Reconcile's job now (Task 6), not this function's. A
// transient enumerate error leaves the previous open set untouched rather
// than treating every device as disconnected.
func (ds *deviceState) refresh(logger *slog.Logger) []dispatcher.PresentDevice {
	infos, err := hid.Enumerate()
	if err != nil {
		logger.Warn("enumerate failed", "err", err)
		infos = nil
	}

	present := make(map[string]bool, len(infos))
	for _, info := range infos {
		present[info.Path] = true
		if _, alreadyOpen := ds.openByPath[info.Path]; alreadyOpen {
			continue
		}
		uid, hasUID := probeUID(info.Path, logger)
		dev, err := hid.Open(info.Path)
		if err != nil {
			logger.Warn("could not open device", "path", info.Path, "err", err)
			continue
		}
		if err := dev.SetDirectMode(); err != nil {
			logger.Warn("could not set direct mode", "path", info.Path, "err", err)
		}
		ds.openByPath[info.Path] = openDevice{
			identity: hid.Identity{
				Path: info.Path, VendorID: info.VendorID, ProductID: info.ProductID,
				UID: uid, HasUID: hasUID,
			},
			ctrl: dev,
		}
	}
	for path, od := range ds.openByPath {
		if !present[path] {
			_ = od.ctrl.Close()
			delete(ds.openByPath, path)
		}
	}

	devices := make([]openDevice, 0, len(ds.openByPath))
	for _, od := range ds.openByPath {
		devices = append(devices, od)
	}
	// Map iteration order is randomized; sort by Path so Registry.Reconcile's
	// dedup-suffix assignment (-0, -1, ...) for any never-before-seen
	// colliding identity is stable across polls, instead of depending on Go's
	// randomized map iteration order.
	sort.Slice(devices, func(i, j int) bool { return devices[i].identity.Path < devices[j].identity.Path })

	out := make([]dispatcher.PresentDevice, len(devices))
	for i, od := range devices {
		out[i] = dispatcher.PresentDevice{Identity: od.identity, Ctrl: od.ctrl}
	}
	return out
}

// probeUID opens its own short-lived handle to query the Vial keyboard UID
// and closes it before the caller opens the real long-lived handle on the
// same path — sequential, not concurrent, so it doesn't violate macOS's
// exclusive-HID-open semantics.
func probeUID(path string, logger *slog.Logger) (uid [8]byte, hasUID bool) {
	dev, err := hid.Open(path)
	if err != nil {
		logger.Warn("could not open candidate device", "path", path, "err", err)
		return uid, false
	}
	defer func() { _ = dev.Close() }()
	uid, err = dev.GetKeyboardUID()
	if err != nil {
		return uid, false
	}
	return uid, true
}

// pollForDevices re-enumerates every enumeratePollInterval, reconciles
// registry (rewiring reconnected devices, evicting stale-untethered ones),
// calls syncDevices to fetch capabilities for any device that needs
// them (which also redraws reconnected devices from cache, faster than
// RunPeriodicRedraw's unconditional 5s sweep started separately in main()),
// and forgets cache's entry for any device Reconcile reports as Evicted, so
// a later device presenting that identity starts fresh.
func pollForDevices(ctx context.Context, state *deviceState, registry *dispatcher.Registry, cache *dispatcher.Cache, disp *dispatcher.Dispatcher, logger *slog.Logger) {
	ticker := time.NewTicker(enumeratePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			res := registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)
			logAdded(logger, res.Added)
			syncDevices(ctx, registry, disp, res.Reconnected, logger)
			for _, name := range res.Evicted {
				cache.Forget(name)
			}
		case <-ctx.Done():
			return
		}
	}
}

// syncDevices ensures capabilities for every connected device lacking them
// (newly seen, or whose earlier fetch failed — retried every poll) and for
// every reconnected device; EnsureCapabilities also resolves pending writes
// and redraws, which is the reconnect-triggered redraw.
func syncDevices(ctx context.Context, registry *dispatcher.Registry, disp *dispatcher.Dispatcher, reconnected []string, logger *slog.Logger) {
	seen := make(map[string]bool)
	for _, name := range append(registry.ConnectedWithoutCaps(), reconnected...) {
		if seen[name] {
			continue
		}
		seen[name] = true
		if err := disp.EnsureCapabilities(ctx, name); err != nil {
			logger.Warn("capabilities fetch failed", "device", name, "err", err)
		}
	}
}

// logAdded logs each first-seen device's name with the config.yaml snippet
// that pre-declares it (spec §5), so users don't have to query GET /devices.
func logAdded(logger *slog.Logger, names []string) {
	for _, name := range names {
		logger.Info("new device seen; to pre-declare it add under devices: in config.yaml",
			"device", name, "snippet", "- id: "+name+"\n  optional: true")
	}
}

// loadAll loads config.yaml and the effects/templates library from dir —
// everything that must be valid before the daemon starts. --check-config runs
// exactly this.
func loadAll(dir string) (*config.Config, *effects.Library, error) {
	cfg, err := config.LoadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	lib, err := effects.Load(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("effects: %w", err)
	}
	return cfg, lib, nil
}

// resolveConfigDir applies -c/--config's precedence over the default
// (${XDG_CONFIG_HOME:-~/.config}/blinkenkeys, with ~/ expanded): an explicit
// flag value must name an existing directory, so a typo fails loudly instead
// of --check-config silently falling through to "ok" with an empty library.
func resolveConfigDir(flagVal string, getenv func(string) string, home string) (string, error) {
	if flagVal == "" {
		return config.Dir(getenv, home), nil
	}
	dir := flagVal
	if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return "", fmt.Errorf("config dir %q: %w", flagVal, err)
	case !info.IsDir():
		return "", fmt.Errorf("config dir %q: not a directory", flagVal)
	}
	return dir, nil
}

// resolveSocketPath applies the socket path precedence: config.yaml (with
// ~/ expanded) > ~/.local/state/blinkenkeys/api.sock.
func resolveSocketPath(configured, home string) string {
	switch {
	case strings.HasPrefix(configured, "~/"):
		return filepath.Join(home, configured[2:])
	case configured != "":
		return configured
	default:
		return filepath.Join(home, ".local", "state", "blinkenkeys", "api.sock")
	}
}
