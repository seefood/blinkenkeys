package dispatcher

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRedrawEmptyCacheIsNoop(t *testing.T) {
	fc := &fakeController{}
	d := New(registryWithConnected("a", fc), NewCache(), 8, discardLogger())
	d.Redraw("a")
	processQueued(d)
	if fc.setCount() != 0 {
		t.Errorf("SetKeys called on empty cache: %+v", fc.lastSet())
	}
}

func TestRedrawReplaysCache(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0, H: 1, S: 2, V: 3}})
	d := New(registryWithConnected("a", fc), cache, 8, discardLogger())
	d.Redraw("a")
	processQueued(d)
	if got := fc.lastSet(); len(got) != 1 || got[0].H != 1 {
		t.Errorf("SetKeys = %+v", got)
	}
}

func TestRedrawIsOneJobRegardlessOfLEDCount(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	for i := uint16(0); i < 12; i++ {
		cache.Update("a", []hid.KeyColor{{Index: i}})
	}
	d := New(registryWithConnected("a", fc), cache, 1, discardLogger()) // room for exactly one job
	d.Redraw("a")
	processQueued(d)
	if fc.setCount() != 2 || len(fc.sets[0]) != 9 || len(fc.sets[1]) != 3 {
		t.Errorf("SetKeys calls = %d, want 2 reports of 9+3", fc.setCount())
	}
}

func TestRunPeriodicRedrawFiresOnTick(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0}})
	d := New(registryWithConnected("a", fc), cache, 8, discardLogger())
	runDispatcher(t, d)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.RunPeriodicRedraw(ctx, 10*time.Millisecond)
	waitFor(t, func() bool { return fc.lastSet() != nil })
}

func TestReconnectRewireRegainsCache(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{7}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	fc1 := &fakeController{numLEDs: 1, positions: map[uint16][2]uint8{0: {0, 0}}}
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc1}}, time.Now(), UntetheredMaxAge)
	if err := d.EnsureCapabilities(context.Background(), name); err != nil {
		t.Fatalf("EnsureCapabilities: %v", err)
	}
	if err := d.Write(name, led(0), color.HSV{H: 1}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	reg.Reconcile(nil, time.Now(), UntetheredMaxAge) // unplug
	fc2 := &fakeController{}                         // replug: fresh controller, as a real replug produces
	res := reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc2}}, time.Now(), UntetheredMaxAge)
	if len(res.Reconnected) != 1 || res.Reconnected[0] != name {
		t.Fatalf("Reconnected = %v, want [%s]", res.Reconnected, name)
	}
	if err := d.EnsureCapabilities(context.Background(), name); err != nil {
		t.Fatalf("EnsureCapabilities after rewire: %v", err)
	}
	waitFor(t, func() bool { return fc2.lastSet() != nil })
	if got := fc2.lastSet(); got[0].H != 1 {
		t.Errorf("replayed %+v, want H 1", got)
	}
	if fc2.capsQueries() != 0 {
		t.Error("caps re-queried after rewire; they should be retained on the slot")
	}
}
