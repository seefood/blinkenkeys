package dispatcher

import (
	"context"
	"errors"
	"testing"
)

func TestDispatcherSetKeyQueueFull(t *testing.T) {
	reg := registryWithConnected("a", &fakeController{})
	d := New(reg, NewCache(), 0, discardLogger()) // zero-depth queue: never has room, and Run is never even started

	err := d.SetKey(context.Background(), "a", 0, 0, 255, 255)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("SetKey error = %v, want ErrQueueFull", err)
	}
}

func TestDispatcherSetKeyUnknownDevice(t *testing.T) {
	reg := NewRegistry()
	d := New(reg, NewCache(), 8, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	err := d.SetKey(context.Background(), "missing", 0, 0, 255, 255)
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("SetKey error = %v, want ErrDeviceNotFound", err)
	}
}

func TestDispatcherSetKeyUpdatesCache(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	cache := NewCache()
	d := New(reg, cache, 8, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	if err := d.SetKey(context.Background(), "a", 3, 1, 2, 3); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if len(fc.lastSet()) != 1 || fc.lastSet()[0].Index != 3 {
		t.Errorf("controller.SetKeys called with %+v", fc.lastSet())
	}
	if snap := cache.Snapshot("a"); len(snap) != 1 || snap[0].Index != 3 {
		t.Errorf("cache.Snapshot = %+v", snap)
	}
}

func TestDispatcherListDevices(t *testing.T) {
	reg := registryWithConnected("a", &fakeController{})
	d := New(reg, NewCache(), 8, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	devices, err := d.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "a" || !devices[0].Connected {
		t.Errorf("ListDevices = %+v", devices)
	}
}

func TestDispatcherGetCapabilities(t *testing.T) {
	reg := registryWithConnected("a", &fakeController{
		numLEDs: 2, positions: map[uint16][2]uint8{0: {1, 1}, 1: {2, 2}},
	})
	d := New(reg, NewCache(), 8, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	caps, err := d.GetCapabilities(context.Background(), "a")
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if caps.LEDCount != 2 || len(caps.Positions) != 2 {
		t.Errorf("caps = %+v", caps)
	}
}
