package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
)

func TestListDevices(t *testing.T) {
	disp := &fakeDispatcher{listResult: []dispatcher.DeviceSummary{{Name: "cxt12e4-0", Connected: true}}}
	h := NewHandler(disp, &fakeCaps{})

	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var result []dispatcher.DeviceSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 1 || result[0].Name != "cxt12e4-0" {
		t.Errorf("result = %+v", result)
	}
}

func TestGetCapabilitiesUnknownDevice(t *testing.T) {
	disp := &fakeDispatcher{capsErr: dispatcher.ErrDeviceNotFound}
	h := NewHandler(disp, &fakeCaps{})

	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
