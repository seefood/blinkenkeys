package api

import (
	"errors"
	"net/http"
	"testing"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func displacedPad() (*fakeDispatcher, effects.Target, *fakeWriter) {
	disp := knownPad()
	disp.moved = &dispatcher.Moved{Name: "x", From: 3, To: 4}
	from := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 3}}
	w := &fakeWriter{status: map[effects.Target]effects.Status{
		from: {Origin: effects.Origin{Type: "color", Ref: "red"}},
	}}
	return disp, from, w
}

// Semantic validation (color parse, effect/state lookup) must precede Place so
// a rejected request displaces nothing.
func TestPutInvalidRequestNeverPlaces(t *testing.T) {
	cases := []struct {
		name, body string
		lib        *fakeLibrary
		want       int
	}{
		{"bad color", `{"color":"notacolor"}`, &fakeLibrary{}, http.StatusBadRequest},
		{"unknown effect", `{"effect":"nope"}`, &fakeLibrary{effErr: effects.ErrUnknownEffect}, http.StatusNotFound},
		{"unknown state", `{"state":"nope"}`, &fakeLibrary{stateErr: effects.ErrUnknownState}, http.StatusNotFound},
	}
	for _, c := range cases {
		disp, _, w := displacedPad()
		placed := false
		disp.onPlace = func() { placed = true }
		h := NewHandler(disp, w, c.lib, nil)
		if rec := put(t, h, "/devices/0/keys/led:3", c.body); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
		if placed {
			t.Errorf("%s: Place ran for a request that was going to be rejected", c.name)
		}
	}
}

// A failure that can only happen after Place (queue full, HID error) must not
// strand the displaced claim: its carry is still replayed on the new key.
func TestPutWriteFailureAfterPlaceStillReplaysCarry(t *testing.T) {
	disp, from, w := displacedPad()
	w.err = errors.New("queue full")
	h := NewHandler(disp, w, &fakeLibrary{}, nil)
	if rec := put(t, h, "/devices/0/keys/led:3", `{"color":"blue"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	to := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 4}}
	if len(w.colors) != 2 || w.colors[0] != from || w.colors[1] != to {
		t.Fatalf("writes = %+v, want incoming attempt on 3 then carry on 4", w.colors)
	}
}
