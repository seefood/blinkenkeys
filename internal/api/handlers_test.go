package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
)

// fakeDispatcher and fakeCaps are reused by every test file in this package.
type fakeDispatcher struct {
	setKey     func(ctx context.Context, device string, index uint16, h, s, v uint8) error
	listResult []dispatcher.DeviceSummary
	listErr    error
	caps       dispatcher.Capabilities
	capsErr    error
	capsCalls  int
}

func (f *fakeDispatcher) SetKey(ctx context.Context, device string, index uint16, h, s, v uint8) error {
	return f.setKey(ctx, device, index, h, s, v)
}

func (f *fakeDispatcher) ListDevices(context.Context) ([]dispatcher.DeviceSummary, error) {
	return f.listResult, f.listErr
}

func (f *fakeDispatcher) GetCapabilities(context.Context, string) (dispatcher.Capabilities, error) {
	f.capsCalls++
	return f.caps, f.capsErr
}

type fakeCaps struct {
	index uint16
	found bool
	err   error
}

func (f *fakeCaps) IndexFor(context.Context, string, uint8, uint8) (uint16, bool, error) {
	return f.index, f.found, f.err
}

func TestSetKeySuccess(t *testing.T) {
	var gotDevice string
	var gotIndex uint16
	var gotH, gotS, gotV uint8
	disp := &fakeDispatcher{setKey: func(_ context.Context, device string, index uint16, h, s, v uint8) error {
		gotDevice, gotIndex, gotH, gotS, gotV = device, index, h, s, v
		return nil
	}}
	h := NewHandler(disp, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if gotDevice != "cxt12e4-0" || gotIndex != 5 || gotH != 0 || gotS != 255 || gotV != 255 {
		t.Errorf("SetKey called with device=%q index=%d h=%d s=%d v=%d", gotDevice, gotIndex, gotH, gotS, gotV)
	}
}

func TestSetKeyBadColor(t *testing.T) {
	disp := &fakeDispatcher{setKey: func(context.Context, string, uint16, uint8, uint8, uint8) error {
		t.Fatal("dispatcher should not be called for a malformed color")
		return nil
	}}
	h := NewHandler(disp, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"not-a-color"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSetKeyUnknownKeyPosition(t *testing.T) {
	disp := &fakeDispatcher{setKey: func(context.Context, string, uint16, uint8, uint8, uint8) error {
		t.Fatal("dispatcher should not be called when the key position isn't found")
		return nil
	}}
	h := NewHandler(disp, &fakeCaps{found: false})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/99,99", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSetKeyUnknownDevice(t *testing.T) {
	h := NewHandler(&fakeDispatcher{}, &fakeCaps{err: dispatcher.ErrDeviceNotFound})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/nope/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSetKeyDispatcherUnavailable(t *testing.T) {
	disp := &fakeDispatcher{setKey: func(context.Context, string, uint16, uint8, uint8, uint8) error {
		return errors.New("boom")
	}}
	h := NewHandler(disp, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
