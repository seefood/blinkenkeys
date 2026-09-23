package api

import (
	"context"
	"errors"
	"testing"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
)

func TestCapabilitiesCacheIndexFor(t *testing.T) {
	disp := &fakeDispatcher{caps: dispatcher.Capabilities{
		LEDCount:  2,
		Positions: []dispatcher.LEDPosition{{Index: 0, Row: 1, Col: 1}, {Index: 1, Row: 2, Col: 2}},
	}}
	cache := NewCapabilitiesCache(disp)

	index, found, err := cache.IndexFor(context.Background(), "cxt12e4-0", 2, 2)
	if err != nil || !found || index != 1 {
		t.Fatalf("IndexFor = %d,%v,%v; want 1,true,nil", index, found, err)
	}
	if _, _, err := cache.IndexFor(context.Background(), "cxt12e4-0", 1, 1); err != nil {
		t.Fatalf("second IndexFor: %v", err)
	}
	if disp.capsCalls != 1 {
		t.Errorf("GetCapabilities called %d times, want 1 (second call should hit the cache)", disp.capsCalls)
	}
}

func TestCapabilitiesCacheUnknownDevice(t *testing.T) {
	disp := &fakeDispatcher{capsErr: dispatcher.ErrDeviceNotFound}
	cache := NewCapabilitiesCache(disp)

	_, _, err := cache.IndexFor(context.Background(), "nope", 0, 0)
	if !errors.Is(err, dispatcher.ErrDeviceNotFound) {
		t.Fatalf("IndexFor error = %v, want ErrDeviceNotFound", err)
	}
}

func TestCapabilitiesCacheNotPresent(t *testing.T) {
	disp := &fakeDispatcher{caps: dispatcher.Capabilities{
		LEDCount: 1, Positions: []dispatcher.LEDPosition{{Index: 0, Row: 0, Col: 0}},
	}}
	cache := NewCapabilitiesCache(disp)

	_, found, err := cache.IndexFor(context.Background(), "cxt12e4-0", 9, 9)
	if err != nil || found {
		t.Fatalf("IndexFor = _,%v,%v; want false,nil", found, err)
	}
}
