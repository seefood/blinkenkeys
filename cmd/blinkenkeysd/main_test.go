package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/config"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/hid"
)

func TestLayoutFor(t *testing.T) {
	if _, ok := layoutFor(config.DeviceDecl{ID: "a"}); ok {
		t.Error("device without keys: must report no layout")
	}
	l, ok := layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Tabs: config.KeyList{0, 1}, Pool: config.KeyList{6}}})
	if !ok || l.DefaultPool || !reflect.DeepEqual(l.Tabs, []uint16{0, 1}) || !reflect.DeepEqual(l.Pool, []uint16{6}) {
		t.Errorf("explicit pool: %+v, %v", l, ok)
	}
	l, _ = layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Tabs: config.KeyList{0}}})
	if !l.DefaultPool {
		t.Errorf("omitted pool must give DefaultPool: %+v", l)
	}
	l, _ = layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Pool: config.KeyList{}}})
	if l.DefaultPool || len(l.Pool) != 0 {
		t.Errorf("explicit empty pool must not be DefaultPool: %+v", l)
	}
	l, _ = layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Collision: "displace"}})
	if !l.Displace {
		t.Errorf("collision: displace must set Displace: %+v", l)
	}
	if l, _ = layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Tabs: config.KeyList{0}}}); l.Displace {
		t.Error("collision omitted must not displace")
	}
}

func TestDeclareDevicesAppliesLayout(t *testing.T) {
	registry := dispatcher.NewRegistry()
	declareDevices(registry, []config.DeviceDecl{
		{ID: "a", Optional: true, Keys: &config.KeyLayout{Tabs: config.KeyList{0, 1}, Pool: config.KeyList{6}, Collision: "displace"}},
		{ID: "b"},
	})
	l := registry.Layout("a")
	if !reflect.DeepEqual(l.Tabs, []uint16{0, 1}) || !reflect.DeepEqual(l.Pool, []uint16{6}) || !l.Displace {
		t.Errorf("layout a = %+v", l)
	}
	if got := registry.Layout("b"); !reflect.DeepEqual(got, registry.Layout("unconfigured")) {
		t.Errorf("device without keys: layout = %+v, want the default", got)
	}
}

func TestLoadAllValidatesKeysSection(t *testing.T) {
	dir := t.TempDir()
	write := func(yaml string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("devices:\n  - id: a\n    keys: {tabs: [0-5], pool: [6-11], collision: displace}\n")
	if _, _, err := loadAll(dir); err != nil {
		t.Errorf("valid keys section rejected: %v", err)
	}
	write("devices:\n  - id: a\n    keys: {collision: bogus}\n")
	if _, _, err := loadAll(dir); err == nil {
		t.Error("bad collision value accepted")
	}
}

func TestNewAPIHandlerUsesConfiguredClaimTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	registry := dispatcher.NewRegistry()
	disp := dispatcher.New(registry, dispatcher.NewCache(), 8, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	registry.Reconcile([]dispatcher.PresentDevice{{Identity: id, Ctrl: stubController{}}}, time.Now(), dispatcher.UntetheredMaxAge)
	dev := hid.BaseName(id)
	declareDevices(registry, []config.DeviceDecl{{ID: dev, Keys: &config.KeyLayout{Pool: config.KeyList{0}}}})
	syncDevices(ctx, registry, disp, nil, logger)
	last := time.Now().Add(-time.Hour)
	if _, err := registry.ClaimOrGet(dev, "n1", last); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Claims: config.ClaimsConfig{IdleTimeout: "2h"}}
	h := newAPIHandler(disp, effects.NewEngine(disp, logger), &effects.Library{}, cfg, logger)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices/"+dev+"/keys/n1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	var v struct {
		Claim struct {
			ExpiresAt time.Time `json:"expires_at"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if want := last.Add(2 * time.Hour); v.Claim.ExpiresAt.Sub(want).Abs() > time.Second {
		t.Errorf("expires_at = %v, want ~%v (configured 2h, not default 8h)", v.Claim.ExpiresAt, want)
	}
}

func TestWarnIfRootFallback_NonLinux(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("darwin", 0, slog.New(slog.NewTextHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("expected no log output on non-Linux, got %q", buf.String())
	}
}

func TestWarnIfRootFallback_LinuxNonRoot(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("linux", 1000, slog.New(slog.NewTextHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("expected no log output for non-root, got %q", buf.String())
	}
}

func TestWarnIfRootFallback_LinuxRoot(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("linux", 0, slog.New(slog.NewTextHandler(&buf, nil)))
	if !strings.Contains(buf.String(), "running as root") {
		t.Errorf("expected a root-fallback warning, got %q", buf.String())
	}
}

type stubController struct{}

func (stubController) SetKeys([]hid.KeyColor) error            { return nil }
func (stubController) GetNumberLEDs() (uint16, error)          { return 1, nil }
func (stubController) GetLEDInfo(uint16) (uint8, uint8, error) { return 0, 0, nil }
func (stubController) Close() error                            { return nil }

func TestSyncDevicesFetchesMissingCaps(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	registry := dispatcher.NewRegistry()
	disp := dispatcher.New(registry, dispatcher.NewCache(), 8, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	registry.Reconcile([]dispatcher.PresentDevice{{Identity: id, Ctrl: stubController{}}}, time.Now(), dispatcher.UntetheredMaxAge)
	syncDevices(ctx, registry, disp, nil, logger)

	if _, known, _ := registry.Caps(hid.BaseName(id)); !known {
		t.Error("caps not fetched for newly connected device")
	}
	if got := registry.ConnectedWithoutCaps(); len(got) != 0 {
		t.Errorf("ConnectedWithoutCaps = %v, want none", got)
	}
}

func TestLogAddedHintsConfig(t *testing.T) {
	var buf bytes.Buffer
	logAdded(slog.New(slog.NewTextHandler(&buf, nil)), []string{"uid-0102"})
	if !strings.Contains(buf.String(), "uid-0102") || !strings.Contains(buf.String(), "optional: true") {
		t.Errorf("log = %q", buf.String())
	}
}

func TestResolveSocketPath(t *testing.T) {
	tests := []struct{ configured, want string }{
		{"/cfg.sock", "/cfg.sock"},
		{"~/bk.sock", "/home/u/bk.sock"},
		{"", "/home/u/.local/state/blinkenkeys/api.sock"},
	}
	for _, tt := range tests {
		if got := resolveSocketPath(tt.configured, "/home/u"); got != tt.want {
			t.Errorf("resolveSocketPath(%q) = %q, want %q", tt.configured, got, tt.want)
		}
	}
}

func TestResolveConfigDir(t *testing.T) {
	unset := func(string) string { return "" }

	if got, err := resolveConfigDir("", unset, "/home/u"); err != nil || got != "/home/u/.config/blinkenkeys" {
		t.Errorf("resolveConfigDir(\"\", ...) = %q, %v, want /home/u/.config/blinkenkeys, nil", got, err)
	}

	dir := t.TempDir()
	if got, err := resolveConfigDir(dir, unset, "/home/u"); err != nil || got != dir {
		t.Errorf("resolveConfigDir(%q, ...) = %q, %v, want %q, nil", dir, got, err, dir)
	}

	tilde := filepath.Join(dir, "bkcfg")
	if err := os.Mkdir(tilde, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveConfigDir("~/bkcfg", unset, dir); err != nil || got != tilde {
		t.Errorf("resolveConfigDir(\"~/bkcfg\", ...) = %q, %v, want %q, nil", got, err, tilde)
	}

	if _, err := resolveConfigDir(filepath.Join(dir, "missing"), unset, "/home/u"); err == nil {
		t.Error("resolveConfigDir: want error for missing dir")
	}

	file := filepath.Join(dir, "notadir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveConfigDir(file, unset, "/home/u"); err == nil {
		t.Error("resolveConfigDir: want error for a file, not a directory")
	}
}

func TestLoadAllExamples(t *testing.T) {
	if _, _, err := loadAll(filepath.Join("..", "..", "examples", "config")); err != nil {
		t.Errorf("loadAll(examples/config): %v", err)
	}
}

func TestLoadAllReportsBadEffect(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "effects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "effects", "bad.yaml"), []byte("stages: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadAll(dir)
	if err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("loadAll err = %v, want one naming bad.yaml", err)
	}
}

func TestLoadAllReportsBadConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("bogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadAll(dir); err == nil {
		t.Error("loadAll: want error for unknown config key")
	}
}

func TestRemoveStaleSocketRefusesLiveSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	if err := removeStaleSocket(path); err == nil {
		t.Error("removeStaleSocket: want error for a live socket, got nil")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("live socket file was removed: %v", err)
	}
}

func TestRemoveStaleSocketRemovesDeadSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Simulate a crashed previous run: the socket file survives, but nothing
	// is listening on it anymore. SetUnlinkOnClose(false) keeps Close() from
	// cleaning up the file itself, so it's left behind exactly like a kill -9 would.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := removeStaleSocket(path); err != nil {
		t.Errorf("removeStaleSocket: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("stale socket file was not removed: err = %v", err)
	}
}

func TestRemoveStaleSocketNoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "api.sock")
	if err := removeStaleSocket(path); err != nil {
		t.Errorf("removeStaleSocket on missing file: %v", err)
	}
}

func TestNewTCPServerEnforcesBearerToken(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tcpCfg := &config.TCPListener{Address: "127.0.0.1:0", Token: "s3cr3t"}
	srv, listener, err := newTCPServer(tcpCfg, okHandler)
	if err != nil {
		t.Fatalf("newTCPServer: %v", err)
	}
	go func() { _ = srv.Serve(listener) }()
	defer func() { _ = srv.Close() }()

	url := "http://" + listener.Addr().String() + "/"

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET without token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: status = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with wrong token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("wrong token: status = %d, want 403", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer s3cr3t")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with correct token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("correct token: status = %d, want 200", resp.StatusCode)
	}
}
