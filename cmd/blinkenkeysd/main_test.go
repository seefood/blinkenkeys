package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/hid"
)

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
