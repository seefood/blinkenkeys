package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func ledTarget(n uint16) effects.Target {
	return effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: n}}
}

func TestGetKeyNamedClaim(t *testing.T) {
	led := keyaddr.Address{Kind: keyaddr.LED, N: 5}
	orange := color.HSV{H: 21, S: 255, V: 255}
	setAt := time.Now().Add(-12 * time.Second)
	lastWrite := time.Now().Add(-time.Minute)
	disp := knownPad()
	disp.lookupAddr = &led
	disp.curColor = &orange
	disp.info = dispatcher.KeyInfo{Name: "wezterm-17", LastWrite: lastWrite, Row: 1, Col: 2, HasPos: true}
	w := &fakeWriter{status: map[effects.Target]effects.Status{ledTarget(5): {
		Origin: effects.Origin{Type: "state", Ref: "claude/working", Owner: "wezterm-17"},
		SetAt:  setAt,
		Effect: &effects.EffectStatus{Name: "breathe_orange", Running: true, Elapsed: 12 * time.Second},
	}}}
	h := NewHandler(disp, w, &fakeLibrary{}, nil)
	h.SetClaimIdleTimeout(8 * time.Hour)

	rec := get(h, "/devices/0/keys/wezterm-17")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	var v struct {
		Key, Kind string
		LED       uint16
		Row, Col  *uint8
		Connected bool
		Color     struct {
			H, S, V uint8
			Hex     string
		}
		Source struct {
			Type, Ref, Owner string
			AgeMS            int64 `json:"age_ms"`
		}
		Effect struct {
			Name       string
			Running    bool
			ElapsedMS  int64  `json:"elapsed_ms"`
			DurationMS *int64 `json:"duration_ms"`
		}
		Claim struct {
			ExpiresAt time.Time `json:"expires_at"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v; %s", err, rec.Body)
	}
	if v.Key != "wezterm-17" || v.Kind != "name" || v.LED != 5 || v.Row == nil || *v.Row != 1 || !v.Connected {
		t.Errorf("identity fields wrong: %+v", v)
	}
	if v.Color.Hex == "" || v.Color.H != 21 {
		t.Errorf("color = %+v", v.Color)
	}
	if v.Source.Type != "state" || v.Source.Ref != "claude/working" || v.Source.Owner != "wezterm-17" || v.Source.AgeMS < 11000 {
		t.Errorf("source = %+v", v.Source)
	}
	if !v.Effect.Running || v.Effect.Name != "breathe_orange" || v.Effect.ElapsedMS != 12000 || v.Effect.DurationMS != nil {
		t.Errorf("effect = %+v (duration must be null when open-ended)", v.Effect)
	}
	if want := lastWrite.Add(8 * time.Hour); v.Claim.ExpiresAt.Sub(want).Abs() > time.Second {
		t.Errorf("expires_at = %v, want ~%v", v.Claim.ExpiresAt, want)
	}
}

func TestGetKeyRegistration(t *testing.T) {
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	tests := []struct {
		name string
		disp *fakeDispatcher
		w    *fakeWriter
		path string
		want int
	}{
		{"unclaimed name", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupErr: dispatcher.ErrClaimNotFound}, &fakeWriter{}, "/devices/0/keys/esc", 404},
		{"direct key never written", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupAddr: &led}, &fakeWriter{}, "/devices/0/keys/idx:3", 404},
		{"direct key owned", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupAddr: &led, info: dispatcher.KeyInfo{Direct: true}}, &fakeWriter{}, "/devices/0/keys/idx:3", 200},
		{"direct key with status only", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupAddr: &led}, &fakeWriter{status: map[effects.Target]effects.Status{ledTarget(3): {}}}, "/devices/0/keys/idx:3", 200},
		{"unknown device", knownPad(), &fakeWriter{}, "/devices/zzz/keys/idx:3", 404},
		{"off matrix", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupErr: dispatcher.ErrKeyNotFound}, &fakeWriter{}, "/devices/0/keys/idx:99", 404},
		{"malformed pos", knownPad(), &fakeWriter{}, "/devices/0/keys/1,x", 400},
	}
	for _, tt := range tests {
		h := NewHandler(tt.disp, tt.w, &fakeLibrary{}, nil)
		if rec := get(h, tt.path); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d; %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
}

// A GET must be read-only: Canonical would claim the name / mark the key direct.
func TestGetKeyNeverClaims(t *testing.T) {
	disp := &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupErr: dispatcher.ErrClaimNotFound}
	h := NewHandler(disp, &fakeWriter{}, &fakeLibrary{}, nil)
	if rec := get(h, "/devices/0/keys/never-seen"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if disp.canonCalls != 0 || len(disp.released) != 0 {
		t.Errorf("GET of unknown name touched claims: Canonical calls = %d", disp.canonCalls)
	}
	ok := knownPad()
	ok.info = dispatcher.KeyInfo{Name: "a", LastWrite: time.Now()}
	get(NewHandler(ok, &fakeWriter{}, &fakeLibrary{}, nil), "/devices/0/keys/a")
	get(NewHandler(ok, &fakeWriter{}, &fakeLibrary{}, nil), "/devices/0/keys")
	if ok.canonCalls != 0 {
		t.Errorf("successful GETs called Canonical %d times", ok.canonCalls)
	}
}

func TestListKeysSortedByLED(t *testing.T) {
	w := &fakeWriter{status: map[effects.Target]effects.Status{
		ledTarget(7): {Origin: effects.Origin{Type: "color", Ref: "red"}},
		ledTarget(2): {Origin: effects.Origin{Type: "color", Ref: "blue"}},
	}}
	rec := get(NewHandler(knownPad(), w, &fakeLibrary{}, nil), "/devices/0/keys")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []struct{ LED uint16 }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 2 || got[0].LED != 2 || got[1].LED != 7 {
		t.Errorf("list = %+v, %v", got, err)
	}
	empty := get(NewHandler(knownPad(), &fakeWriter{}, &fakeLibrary{}, nil), "/devices/0/keys")
	if empty.Body.String() != "[]\n" {
		t.Errorf("empty list body = %q, want []", empty.Body)
	}
}

func TestCapabilitiesIncludeLayout(t *testing.T) {
	disp := knownPad()
	disp.caps = dispatcher.Capabilities{LEDCount: 12}
	disp.layout = dispatcher.Layout{Tabs: []uint16{0, 1, 2}}
	rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}, nil), "/devices/0")
	var got struct {
		LEDCount int `json:"led_count"`
		Layout   struct{ Tabs []uint16 }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.LEDCount != 12 || len(got.Layout.Tabs) != 3 {
		t.Errorf("caps = %+v, %v; body %s", got, err, rec.Body)
	}
}
