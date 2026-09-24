package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
)

func get(h *Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestListDevices(t *testing.T) {
	disp := &fakeDispatcher{listResult: []dispatcher.DeviceSummary{{Name: "uid-01", Connected: true}}}
	rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}), "/devices")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var result []dispatcher.DeviceSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result) != 1 || result[0].Name != "uid-01" {
		t.Errorf("result = %+v, %v", result, err)
	}
}

func TestListDevicesQueueFull(t *testing.T) {
	disp := &fakeDispatcher{listErr: dispatcher.ErrQueueFull}
	if rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}), "/devices"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestGetCapabilitiesByOrdinal(t *testing.T) {
	disp := knownPad()
	disp.caps = dispatcher.Capabilities{LEDCount: 12}
	rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}), "/devices/0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var caps dispatcher.Capabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil || caps.LEDCount != 12 {
		t.Errorf("caps = %+v, %v", caps, err)
	}
}

func TestGetCapabilitiesStatuses(t *testing.T) {
	tests := []struct {
		name string
		disp *fakeDispatcher
		path string
		want int
	}{
		{"unknown device", knownPad(), "/devices/nope", 404},
		{"caps never fetched", &fakeDispatcher{devices: map[string]string{"0": "a"}, capsErr: dispatcher.ErrCapsUnknown}, "/devices/0", 503},
		{"queue full", &fakeDispatcher{devices: map[string]string{"0": "a"}, capsErr: dispatcher.ErrQueueFull}, "/devices/0", 503},
	}
	for _, tt := range tests {
		if rec := get(NewHandler(tt.disp, &fakeWriter{}, &fakeLibrary{}), tt.path); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, rec.Code, tt.want)
		}
	}
}
