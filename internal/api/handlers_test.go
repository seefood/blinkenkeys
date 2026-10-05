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
	lookupErr  error
	moved      *dispatcher.Moved // returned by Place
	onPlace    func()
	lookupAddr *keyaddr.Address // if set, Lookup returns this instead of echoing the address

	info         dispatcher.KeyInfo
	curColor     *color.HSV
	layout       dispatcher.Layout
	disconnected bool
	canonCalls   int // Canonical claims/marks as a side effect; read paths must leave this 0
	owned        []uint16
	infoCalls    int
}

func (f *fakeDispatcher) KeyInfo(string, uint16) dispatcher.KeyInfo {
	f.infoCalls++
	return f.info
}
func (f *fakeDispatcher) OwnedLEDs(string) []uint16                 { return f.owned }
func (f *fakeDispatcher) CurrentColor(string, uint16) (color.HSV, bool) {
	if f.curColor == nil {
		return color.HSV{}, false
	}
	return *f.curColor, true
}
func (f *fakeDispatcher) Layout(string) dispatcher.Layout { return f.layout }
func (f *fakeDispatcher) Connected(string) bool           { return !f.disconnected }

func (f *fakeDispatcher) ResolveDevice(ref string) (string, bool) {
	name, ok := f.devices[ref]
	return name, ok
}

func (f *fakeDispatcher) Canonical(_ context.Context, _ string, a keyaddr.Address) (keyaddr.Address, error) {
	f.canonCalls++
	if f.canonErr != nil {
		return keyaddr.Address{}, f.canonErr
	}
	return a, nil
}

func (f *fakeDispatcher) Place(ctx context.Context, device string, a keyaddr.Address) (keyaddr.Address, *dispatcher.Moved, error) {
	if f.onPlace != nil {
		f.onPlace()
	}
	a, err := f.Canonical(ctx, device, a)
	return a, f.moved, err
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

func (f *fakeDispatcher) Lookup(_ context.Context, _ string, a keyaddr.Address) (keyaddr.Address, error) {
	if f.lookupErr != nil {
		return keyaddr.Address{}, f.lookupErr
	}
	if f.lookupAddr != nil {
		return *f.lookupAddr, nil
	}
	return a, nil
}

type fakeWriter struct {
	colors  []effects.Target
	gotHSV  []color.HSV
	starts  []effects.Target
	gotTL   []*effects.Timeline
	origins []effects.Origin // origin of each SetColorFrom/StartFrom call
	status  map[effects.Target]effects.Status
	err     error
}

func (f *fakeWriter) SetColor(t effects.Target, c color.HSV) error {
	f.colors, f.gotHSV = append(f.colors, t), append(f.gotHSV, c)
	return f.err
}

// ClearIfOwner mirrors effects.Engine.ClearIfOwner over f.status; a clear is
// recorded as a SetColor to black.
func (f *fakeWriter) ClearIfOwner(t effects.Target, owner string) (bool, error) {
	if st, ok := f.status[t]; ok && st.Origin.Owner != owner {
		return false, nil
	}
	if err := f.SetColor(t, color.HSV{}); err != nil {
		return false, err
	}
	delete(f.status, t)
	return true, nil
}

func (f *fakeWriter) SetColorFrom(t effects.Target, c color.HSV, o effects.Origin, _ time.Time) error {
	f.origins = append(f.origins, o)
	return f.SetColor(t, c)
}

func (f *fakeWriter) StartFrom(t effects.Target, tl *effects.Timeline, _ time.Time, o effects.Origin) error {
	f.origins = append(f.origins, o)
	f.starts, f.gotTL = append(f.starts, t), append(f.gotTL, tl)
	return f.err
}

func (f *fakeWriter) Status(t effects.Target, _ time.Time) (effects.Status, bool) {
	st, ok := f.status[t]
	return st, ok
}

func (f *fakeWriter) Statuses(device string, _ time.Time) map[effects.Target]effects.Status {
	out := map[effects.Target]effects.Status{}
	for t, st := range f.status {
		if t.Device == device {
			out[t] = st
		}
	}
	return out
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
		{"direct address now blanks", knownPad(), "/devices/0/keys/2,2", 204},
		{"malformed pos", knownPad(), "/devices/0/keys/1,x", 400},
		{"unclaimed name", &fakeDispatcher{devices: map[string]string{"0": "a"}, lookupErr: dispatcher.ErrClaimNotFound}, "/devices/0/keys/esc", 404},
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

func TestPutRecordsOriginAndOwner(t *testing.T) {
	w := &fakeWriter{}
	h := NewHandler(knownPad(), w, &fakeLibrary{tl: &effects.Timeline{}}, nil)
	if rec := put(t, h, "/devices/0/keys/idx:1", `{"color":"red","owner":"laptop.iterm-ab12"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	if rec := put(t, h, "/devices/0/keys/idx:1", `{"effect":"timer5min"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	want := []effects.Origin{
		{Type: "color", Ref: "red", Owner: "laptop.iterm-ab12"},
		{Type: "effect", Ref: "timer5min"},
	}
	if len(w.origins) != 2 || w.origins[0] != want[0] || w.origins[1] != want[1] {
		t.Errorf("origins = %+v, want %+v", w.origins, want)
	}
}

func TestPutRejectsBadOwner(t *testing.T) {
	h := NewHandler(knownPad(), &fakeWriter{}, &fakeLibrary{}, nil)
	for _, body := range []string{`{"color":"red","owner":""}`, `{"color":"red","owner":"` + strings.Repeat("x", 129) + `"}`} {
		if rec := put(t, h, "/devices/0/keys/0,0", body); rec.Code != 400 {
			t.Errorf("%.40s: status = %d, want 400", body, rec.Code)
		}
	}
	// owner alone is not a write
	if rec := put(t, h, "/devices/0/keys/0,0", `{"owner":"x"}`); rec.Code != 400 {
		t.Errorf("owner-only body: status = %d, want 400", rec.Code)
	}
}

func TestDeleteDirectAddressBlanks(t *testing.T) {
	disp := knownPad()
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	disp.lookupAddr = &led
	w := &fakeWriter{}
	rec := del(t, NewHandler(disp, w, &fakeLibrary{}, nil), "/devices/0/keys/idx:3")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	want := effects.Target{Device: "uid-01", Addr: led}
	if len(w.colors) != 1 || w.colors[0] != want || w.gotHSV[0] != (color.HSV{}) {
		t.Errorf("SetColor = %+v %+v", w.colors, w.gotHSV)
	}
	if len(disp.released) != 0 {
		t.Errorf("direct key has no claim to release, got %v", disp.released)
	}
}

func TestDeleteOwnerMismatchIsNoOp(t *testing.T) {
	disp := knownPad()
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	disp.lookupAddr = &led
	target := effects.Target{Device: "uid-01", Addr: led}
	w := &fakeWriter{status: map[effects.Target]effects.Status{target: {Origin: effects.Origin{Owner: "other"}}}}
	h := NewHandler(disp, w, &fakeLibrary{}, nil)

	if rec := del(t, h, "/devices/0/keys/idx:3?owner=mine"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(w.colors) != 0 || len(disp.released) != 0 {
		t.Errorf("mismatched owner must not blank or release: %+v %v", w.colors, disp.released)
	}
	if rec := del(t, h, "/devices/0/keys/idx:3?owner=other"); rec.Code != http.StatusNoContent || len(w.colors) != 1 {
		t.Errorf("matching owner must blank: status %d, colors %+v", rec.Code, w.colors)
	}
	w.colors = nil
	if rec := del(t, h, "/devices/0/keys/idx:3"); rec.Code != http.StatusNoContent || len(w.colors) != 1 {
		t.Errorf("no owner param must blank unconditionally: status %d", rec.Code)
	}
}

func TestDeleteOwnerWithNoRecordProceeds(t *testing.T) {
	disp := knownPad()
	w := &fakeWriter{}
	if rec := del(t, NewHandler(disp, w, &fakeLibrary{}, nil), "/devices/0/keys/esc?owner=mine"); rec.Code != 204 || len(disp.released) != 1 {
		t.Errorf("status %d, released %v", rec.Code, disp.released)
	}
}

func TestDeleteBlankFailureSkipsRelease(t *testing.T) {
	disp := knownPad()
	w := &fakeWriter{err: errors.New("queue full")}
	rec := del(t, NewHandler(disp, w, &fakeLibrary{}, nil), "/devices/0/keys/esc")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503; %s", rec.Code, rec.Body)
	}
	if len(disp.released) != 0 {
		t.Errorf("claim must not be released when blank failed, got %v", disp.released)
	}
}

func TestDeleteOwnerWithEmptyOwnerRecordIsNoOp(t *testing.T) {
	disp := knownPad()
	target := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}}
	w := &fakeWriter{status: map[effects.Target]effects.Status{target: {}}}
	rec := del(t, NewHandler(disp, w, &fakeLibrary{}, nil), "/devices/0/keys/esc?owner=mine")
	if rec.Code != http.StatusNoContent || len(w.colors) != 0 || len(disp.released) != 0 {
		t.Errorf("status %d colors %v released %v", rec.Code, w.colors, disp.released)
	}
}
