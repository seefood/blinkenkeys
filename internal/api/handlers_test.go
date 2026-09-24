package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// fakeDispatcher, fakeWriter and fakeLibrary are reused by every test file
// in this package.
type fakeDispatcher struct {
	devices    map[string]string // ref (name or ordinal) -> name
	canonErr   error
	listResult []dispatcher.DeviceSummary
	listErr    error
	caps       dispatcher.Capabilities
	capsErr    error
	releaseErr error
	released   []string // device+"/"+name for each ReleaseClaim call
}

func (f *fakeDispatcher) ResolveDevice(ref string) (string, bool) {
	name, ok := f.devices[ref]
	return name, ok
}

func (f *fakeDispatcher) Canonical(_ context.Context, _ string, a keyaddr.Address) (keyaddr.Address, error) {
	if f.canonErr != nil {
		return keyaddr.Address{}, f.canonErr
	}
	return a, nil
}

func (f *fakeDispatcher) ListDevices(context.Context) ([]dispatcher.DeviceSummary, error) {
	return f.listResult, f.listErr
}

func (f *fakeDispatcher) GetCapabilities(context.Context, string) (dispatcher.Capabilities, error) {
	return f.caps, f.capsErr
}

func (f *fakeDispatcher) ReleaseClaim(device, name string) error {
	f.released = append(f.released, device+"/"+name)
	return f.releaseErr
}

type fakeWriter struct {
	colors []effects.Target
	gotHSV []color.HSV
	starts []effects.Target
	gotTL  []*effects.Timeline
	err    error
}

func (f *fakeWriter) SetColor(t effects.Target, c color.HSV) error {
	f.colors, f.gotHSV = append(f.colors, t), append(f.gotHSV, c)
	return f.err
}

func (f *fakeWriter) Start(t effects.Target, tl *effects.Timeline, _ time.Time) error {
	f.starts, f.gotTL = append(f.starts, t), append(f.gotTL, tl)
	return f.err
}

type fakeLibrary struct {
	tl       *effects.Timeline
	effErr   error
	gotName  string
	action   effects.Action
	stateErr error
}

func (f *fakeLibrary) Effect(name string) (*effects.Timeline, error) {
	f.gotName = name
	return f.tl, f.effErr
}

func (f *fakeLibrary) State(string) (effects.Action, error) { return f.action, f.stateErr }

func knownPad() *fakeDispatcher {
	return &fakeDispatcher{devices: map[string]string{"uid-01": "uid-01", "0": "uid-01"}}
}

func put(t *testing.T, h *Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, path, strings.NewReader(body)))
	return rec
}

func TestPutColorViaOrdinal(t *testing.T) {
	w := &fakeWriter{}
	h := NewHandler(knownPad(), w, &fakeLibrary{}, nil)
	rec := put(t, h, "/devices/0/keys/2,2", `{"color":"#ff0000"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	want := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.RowCol, Row: 2, Col: 2}}
	if len(w.colors) != 1 || w.colors[0] != want || w.gotHSV[0] != (color.HSV{H: 0, S: 255, V: 255}) {
		t.Errorf("SetColor calls = %+v %+v", w.colors, w.gotHSV)
	}
}

func TestPutEffect(t *testing.T) {
	w := &fakeWriter{}
	lib := &fakeLibrary{tl: &effects.Timeline{}}
	h := NewHandler(knownPad(), w, lib, nil)
	rec := put(t, h, "/devices/uid-01/keys/led:3", `{"effect":"timer5min"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	if len(w.starts) != 1 || w.gotTL[0] != lib.tl || lib.gotName != "timer5min" {
		t.Errorf("Start calls = %+v, effect name %q", w.starts, lib.gotName)
	}
}

func TestPutStateColorAndEffect(t *testing.T) {
	orange := color.HSV{H: 28, S: 255, V: 255}
	w := &fakeWriter{}
	h := NewHandler(knownPad(), w, &fakeLibrary{action: effects.Action{Color: &orange}}, nil)
	if rec := put(t, h, "/devices/0/keys/0,0", `{"state":"claude/waiting"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("color state: status = %d", rec.Code)
	}
	if len(w.gotHSV) != 1 || w.gotHSV[0] != orange {
		t.Errorf("SetColor = %+v", w.gotHSV)
	}

	tl := &effects.Timeline{}
	w = &fakeWriter{}
	h = NewHandler(knownPad(), w, &fakeLibrary{action: effects.Action{Timeline: tl}}, nil)
	if rec := put(t, h, "/devices/0/keys/0,0", `{"state":"claude/idle"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("effect state: status = %d", rec.Code)
	}
	if len(w.gotTL) != 1 || w.gotTL[0] != tl {
		t.Errorf("Start = %+v", w.gotTL)
	}
}

func TestPutStatusTable(t *testing.T) {
	tests := []struct {
		name string
		disp *fakeDispatcher
		lib  *fakeLibrary
		werr error
		path string
		body string
		want int
	}{
		{"unknown device", knownPad(), &fakeLibrary{}, nil, "/devices/nope/keys/0,0", `{"color":"red"}`, 404},
		{"ordinal out of range", knownPad(), &fakeLibrary{}, nil, "/devices/5/keys/0,0", `{"color":"red"}`, 404},
		{"malformed pos", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/1,x", `{"color":"red"}`, 400},
		{"named key", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/esc", `{"color":"red"}`, 204},
		{"key off matrix", &fakeDispatcher{devices: map[string]string{"0": "a"}, canonErr: dispatcher.ErrKeyNotFound}, &fakeLibrary{}, nil, "/devices/0/keys/9,9", `{"color":"red"}`, 404},
		{"malformed json", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{`, 400},
		{"unknown body field", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"colour":"red"}`, 400},
		{"empty body object", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{}`, 400},
		{"two of three", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"color":"red","state":"a/b"}`, 400},
		{"params (not in phase 3)", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"effect":"e","params":{"x":1}}`, 400},
		{"bad color", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"color":"not-a-color"}`, 400},
		{"unknown effect", knownPad(), &fakeLibrary{effErr: effects.ErrUnknownEffect}, nil, "/devices/0/keys/0,0", `{"effect":"nope"}`, 404},
		{"unknown state", knownPad(), &fakeLibrary{stateErr: effects.ErrUnknownState}, nil, "/devices/0/keys/0,0", `{"state":"a/b"}`, 404},
		{"write failure", knownPad(), &fakeLibrary{}, errors.New("boom"), "/devices/0/keys/0,0", `{"color":"red"}`, 503},
	}
	for _, tt := range tests {
		h := NewHandler(tt.disp, &fakeWriter{err: tt.werr}, tt.lib, nil)
		if rec := put(t, h, tt.path, tt.body); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d; body %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
}

func del(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, path, nil))
	return rec
}

func TestDeleteReleasesNamedKey(t *testing.T) {
	disp := knownPad()
	h := NewHandler(disp, &fakeWriter{}, &fakeLibrary{}, nil)
	rec := del(t, h, "/devices/0/keys/esc")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	if len(disp.released) != 1 || disp.released[0] != "uid-01/esc" {
		t.Errorf("ReleaseClaim calls = %v", disp.released)
	}
}

// TestDeleteBlanksKeyBeforeReleasing verifies a release cancels any running
// effect and blanks the key (via the same SetColor path that cancels an
// effects.Engine entry) before the claim is freed — otherwise a still-running
// effect (e.g. timer5min) keeps animating an LED nothing owns anymore.
func TestDeleteBlanksKeyBeforeReleasing(t *testing.T) {
	disp := knownPad()
	w := &fakeWriter{}
	h := NewHandler(disp, w, &fakeLibrary{}, nil)
	rec := del(t, h, "/devices/0/keys/esc")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	wantTarget := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}}
	if len(w.colors) != 1 || w.colors[0] != wantTarget || w.gotHSV[0] != (color.HSV{}) {
		t.Errorf("SetColor calls = %+v %+v", w.colors, w.gotHSV)
	}
	if len(disp.released) != 1 {
		t.Errorf("ReleaseClaim calls = %v", disp.released)
	}
}

func TestDeleteStatusTable(t *testing.T) {
	tests := []struct {
		name string
		disp *fakeDispatcher
		path string
		want int
	}{
		{"unknown device", knownPad(), "/devices/nope/keys/esc", 404},
		{"direct address not a name", knownPad(), "/devices/0/keys/2,2", 400},
		{"malformed pos", knownPad(), "/devices/0/keys/1,x", 400},
		{"no claim under that name", &fakeDispatcher{devices: map[string]string{"0": "a"}, releaseErr: dispatcher.ErrClaimNotFound}, "/devices/0/keys/esc", 404},
	}
	for _, tt := range tests {
		h := NewHandler(tt.disp, &fakeWriter{}, &fakeLibrary{}, nil)
		if rec := del(t, h, tt.path); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d; body %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
}

func TestUnknownEffectBodyListsKnown(t *testing.T) {
	lib := &fakeLibrary{effErr: fmt.Errorf("%w %q; known: breathe_blue, timer5min", effects.ErrUnknownEffect, "x")}
	rec := put(t, NewHandler(knownPad(), &fakeWriter{}, lib, nil), "/devices/0/keys/0,0", `{"effect":"x"}`)
	if !strings.Contains(rec.Body.String(), "timer5min") {
		t.Errorf("body %s does not list known effects", rec.Body)
	}
}
