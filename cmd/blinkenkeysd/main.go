package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	goHid "github.com/sstallion/go-hid"

	"github.com/seefood/blinkenkeys/internal/api"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/hid"
)

const (
	dispatcherQueueDepth  = 64
	enumeratePollInterval = 1 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	warnIfRootFallback(runtime.GOOS, os.Geteuid(), logger)

	if err := goHid.Init(); err != nil {
		logger.Error("hid init failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = goHid.Exit() }()

	registry := dispatcher.NewRegistry()
	cache := dispatcher.NewCache()
	disp := dispatcher.New(registry, cache, dispatcherQueueDepth, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

	state := newDeviceState()
	// Initial enumeration, synchronous, so devices are known before HTTP
	// traffic starts.
	registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)

	go pollForDevices(ctx, state, registry, cache, disp, logger)
	go disp.RunPeriodicRedraw(ctx, dispatcher.RedrawInterval, logger)

	caps := api.NewCapabilitiesCache(disp)
	handler := api.NewHandler(disp, caps)

	socketPath := socketPathFromEnv()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		logger.Error("could not create socket dir", "err", err)
		os.Exit(1)
	}
	_ = os.Remove(socketPath) // clear a stale socket left by a previous crashed run
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		logger.Error("could not listen on unix socket", "err", err)
		os.Exit(1)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
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
// registry (rewiring reconnected devices, evicting stale-untethered ones per
// Task 6), immediately redraws any device Reconcile reports as Reconnected —
// the design spec's reconnect-triggered redraw, independent of (and faster
// than) RunPeriodicRedraw's unconditional 5s sweep started separately in
// main() — and forgets cache's entry for any device Reconcile reports as
// Evicted, so a later device presenting that identity starts fresh.
func pollForDevices(ctx context.Context, state *deviceState, registry *dispatcher.Registry, cache *dispatcher.Cache, disp *dispatcher.Dispatcher, logger *slog.Logger) {
	ticker := time.NewTicker(enumeratePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			res := registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)
			disp.RedrawReconnected(ctx, res.Reconnected, logger)
			for _, name := range res.Evicted {
				cache.Forget(name)
			}
		case <-ctx.Done():
			return
		}
	}
}

func socketPathFromEnv() string {
	if p := os.Getenv("BLINKENKEYS_SOCKET"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "state", "blinkenkeys", "api.sock")
}
