package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func runDispatcher(t *testing.T, d *Dispatcher) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go d.Run(ctx)
}

func twoKeyPad() *fakeController {
	return &fakeController{numLEDs: 2, positions: map[uint16][2]uint8{0: {1, 1}, 1: {1, 2}}}
}

func TestGetCapabilitiesStoredAfterFirstFetch(t *testing.T) {
	fc := twoKeyPad()
	d := New(registryWithConnected("a", fc), NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	for i := 0; i < 2; i++ {
		caps, err := d.GetCapabilities(context.Background(), "a")
		if err != nil || caps.LEDCount != 2 || len(caps.Positions) != 2 {
			t.Fatalf("GetCapabilities #%d = %+v, %v", i, caps, err)
		}
	}
	if n := fc.capsQueries(); n != 1 {
		t.Errorf("device queried %d times, want 1", n)
	}
}

func TestGetCapabilitiesRetainedWhenUntethered(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: twoKeyPad()}}, time.Now(), UntetheredMaxAge)
	if _, err := d.GetCapabilities(context.Background(), name); err != nil {
		t.Fatalf("GetCapabilities while connected: %v", err)
	}
	reg.Reconcile(nil, time.Now(), UntetheredMaxAge)
	caps, err := d.GetCapabilities(context.Background(), name)
	if err != nil || caps.LEDCount != 2 {
		t.Errorf("GetCapabilities while untethered = %+v, %v", caps, err)
	}
}

func TestGetCapabilitiesUnknownForDeclared(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("a")
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	if _, err := d.GetCapabilities(context.Background(), "a"); !errors.Is(err, ErrCapsUnknown) {
		t.Errorf("err = %v, want ErrCapsUnknown", err)
	}
}

func TestGetCapabilitiesMarksUnsupportedAfterRepeatedFailures(t *testing.T) {
	fc := &fakeController{numLEDsErr: errors.New("not vialrgb")}
	reg := registryWithConnected("a", fc)
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	for i := 0; i < maxCapsFailures; i++ {
		if _, err := d.GetCapabilities(context.Background(), "a"); err == nil {
			t.Fatalf("GetCapabilities #%d: want error", i)
		}
	}
	if got := reg.ConnectedWithoutCaps(); len(got) != 0 {
		t.Errorf("ConnectedWithoutCaps = %v, want none after repeated caps failures", got)
	}
	if got := reg.Summaries(); len(got) != 0 {
		t.Errorf("Summaries = %v, want device hidden after repeated caps failures", got)
	}
}

func TestGetCapabilitiesUnknownDevice(t *testing.T) {
	d := New(NewRegistry(), NewCache(), 8, discardLogger()) // Run not started: must not need the queue
	if _, err := d.GetCapabilities(context.Background(), "missing"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("err = %v, want ErrDeviceNotFound", err)
	}
}

func TestGetCapabilitiesAppliesPending(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{2}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	reg.Declare(name)
	red, green := color.HSV{H: 0, S: 255, V: 255}, color.HSV{H: 85, S: 255, V: 255}
	reg.CapsOrPend(name, PendingWrite{Addr: keyaddr.Address{Kind: keyaddr.RowCol, Row: 1, Col: 1}, Color: red})
	reg.CapsOrPend(name, PendingWrite{Addr: keyaddr.Address{Kind: keyaddr.RowCol, Row: 5, Col: 9}, Color: green})

	var logs bytes.Buffer
	cache := NewCache()
	d := New(reg, cache, 8, slog.New(slog.NewTextHandler(&logs, nil)))
	runDispatcher(t, d)
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: twoKeyPad()}}, time.Now(), UntetheredMaxAge)

	if _, err := d.GetCapabilities(context.Background(), name); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	snap := cache.Snapshot(name)
	if len(snap) != 1 || snap[0] != (hid.KeyColor{Index: 0, H: 0, S: 255, V: 255}) {
		t.Errorf("cache = %+v, want only LED 0 red", snap)
	}
	// Safe to read: the warn was logged on the dispatcher goroutine before
	// it replied to GetCapabilities.
	if !strings.Contains(logs.String(), "5,9") {
		t.Errorf("log %q does not name the dropped address 5,9", logs.String())
	}
}
