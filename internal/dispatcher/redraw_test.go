package dispatcher

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/hid"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRedrawEmptyCacheIsNoop(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	d := New(reg, NewCache(), 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	if err := d.Redraw(context.Background(), "a"); err != nil {
		t.Fatalf("Redraw: %v", err)
	}
	if fc.lastSet != nil {
		t.Errorf("SetKeys called on empty cache: %+v", fc.lastSet)
	}
}

func TestRedrawReplaysCache(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0, H: 1, S: 2, V: 3}})
	d := New(reg, cache, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	if err := d.Redraw(context.Background(), "a"); err != nil {
		t.Fatalf("Redraw: %v", err)
	}
	if len(fc.lastSet) != 1 || fc.lastSet[0].H != 1 {
		t.Errorf("SetKeys called with %+v", fc.lastSet)
	}
}

func TestRedrawReconnectedOnlyTouchesGivenDevices(t *testing.T) {
	fcA, fcB := &fakeController{}, &fakeController{}
	reg := registryWithConnectedMulti(map[string]hid.Controller{"a": fcA, "b": fcB})
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0}})
	cache.Update("b", []hid.KeyColor{{Index: 0}})
	d := New(reg, cache, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	d.RedrawReconnected(context.Background(), []string{"a"}, discardLogger())

	if fcA.lastSet == nil {
		t.Error("device a (reconnected) was not redrawn")
	}
	if fcB.lastSet != nil {
		t.Error("device b (not in reconnected list) should not have been redrawn")
	}
}

func TestRunPeriodicRedrawFiresOnTick(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0}})
	d := New(reg, cache, 8)
	dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
	defer cancelDispatch()
	go d.Run(dispatchCtx)

	redrawCtx, cancelRedraw := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelRedraw()
	d.RunPeriodicRedraw(redrawCtx, 10*time.Millisecond, discardLogger())

	if fc.lastSet == nil {
		t.Error("periodic redraw never fired within the test window")
	}
}

func TestReconnectRewireRegainsCache(t *testing.T) {
	reg := NewRegistry()
	cache := NewCache()
	d := New(reg, cache, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	id := hid.Identity{HasUID: true, UID: [8]byte{7}}
	res := reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	var name string
	for n := range res.Devices {
		name = n
	}

	if err := d.SetKey(context.Background(), name, 0, 1, 2, 3); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	reg.Reconcile(nil, time.Now(), UntetheredMaxAge) // disconnect: slot goes untethered, cache retained

	// Replug: same identity, a fresh hid.Controller instance, as a real
	// replug would produce.
	fc2 := &fakeController{}
	res = reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc2}}, time.Now(), UntetheredMaxAge)
	if len(res.Reconnected) != 1 || res.Reconnected[0] != name {
		t.Fatalf("Reconnected = %v, want [%s] (rewire)", res.Reconnected, name)
	}

	d.RedrawReconnected(context.Background(), res.Reconnected, discardLogger())

	if len(fc2.lastSet) != 1 || fc2.lastSet[0].H != 1 {
		t.Errorf("new controller's SetKeys = %+v, want the pre-disconnect color replayed via rewire", fc2.lastSet)
	}
}
