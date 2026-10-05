package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// lockedSetter is a goroutine-safe frame buffer for a real effects.Engine.
type lockedSetter struct {
	mu  sync.Mutex
	buf map[keyaddr.Address]color.HSV
}

func (s *lockedSetter) Write(_ string, a keyaddr.Address, c color.HSV) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf[a] = c
	return nil
}

func (s *lockedSetter) get(a keyaddr.Address) color.HSV {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf[a]
}

func newRealEngine() (*effects.Engine, *lockedSetter) {
	out := &lockedSetter{buf: map[keyaddr.Address]color.HSV{}}
	return effects.NewEngine(out, slog.New(slog.NewTextHandler(io.Discard, nil))), out
}

// interleaving wraps a real engine and runs after once, right after the
// handler's first ownership check (Status or ClearIfOwner) returns: the
// window a racing write would hit if check and blank were separate calls.
type interleaving struct {
	*effects.Engine
	once  sync.Once
	after func()
}

func (w *interleaving) Status(t effects.Target, now time.Time) (effects.Status, bool) {
	st, ok := w.Engine.Status(t, now)
	w.once.Do(w.after)
	return st, ok
}

func (w *interleaving) ClearIfOwner(t effects.Target, owner string) (bool, error) {
	cleared, err := w.Engine.ClearIfOwner(t, owner)
	w.once.Do(w.after)
	return cleared, err
}

// F9: a write by another owner landing between the conditional DELETE's
// owner check and its blank must survive.
func TestConditionalDeleteDoesNotBlankRacingWrite(t *testing.T) {
	eng, out := newRealEngine()
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	target := effects.Target{Device: "uid-01", Addr: led}
	red, blue := color.HSV{S: 255, V: 255}, color.HSV{H: 170, S: 255, V: 255}
	if err := eng.SetColorFrom(target, red, effects.Origin{Type: "color", Ref: "red", Owner: "first"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	w := &interleaving{Engine: eng, after: func() {
		_ = eng.SetColorFrom(target, blue, effects.Origin{Type: "color", Ref: "blue", Owner: "other"}, time.Now())
	}}
	disp := knownPad()
	disp.lookupAddr = &led
	if rec := del(t, NewHandler(disp, w, &fakeLibrary{}, nil), "/devices/0/keys/idx:3?owner=first"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	st, ok := eng.Status(target, time.Now())
	if !ok || st.Origin.Owner != "other" || out.get(led) != blue {
		t.Errorf("racing write was blanked: record %+v (%v), color %+v", st, ok, out.get(led))
	}
}

// F8 ruling (spec "Collisions"): status records are in-memory, so after a
// daemon restart a conditional clear finds no record and proceeds — it
// blanks the key even if another session has since lit it (via a write the
// restarted daemon has no record of). Pinned so a change is deliberate.
func TestConditionalDeleteWithNoRecordBlanks(t *testing.T) {
	eng, out := newRealEngine() // fresh engine: what a restarted daemon has
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	out.buf[led] = color.HSV{H: 170, S: 255, V: 255}
	disp := knownPad()
	disp.lookupAddr = &led
	if rec := del(t, NewHandler(disp, eng, &fakeLibrary{}, nil), "/devices/0/keys/idx:3?owner=stale-session"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	if c := out.get(led); c != (color.HSV{}) {
		t.Errorf("color = %+v, want blanked (no record => proceed)", c)
	}
}

// Same race with real goroutines, for -race. Whichever request runs first,
// the other owner's PUT succeeded, so it must be what the key ends up with:
// DELETE-first blanks and then the PUT lands; PUT-first makes the DELETE a
// no-op. Black means the DELETE blanked a write it didn't own.
func TestConditionalDeleteConcurrentWithOtherOwner(t *testing.T) {
	eng, out := newRealEngine()
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	target := effects.Target{Device: "uid-01", Addr: led}
	blue := color.HSV{H: 170, S: 255, V: 255}
	h := NewHandler(&fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupAddr: &led}, eng, &fakeLibrary{}, nil)
	for i := 0; i < 200; i++ {
		_ = eng.SetColorFrom(target, color.HSV{V: 1}, effects.Origin{Type: "color", Ref: "x", Owner: "first"}, time.Now())
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			put(t, h, "/devices/0/keys/led:3", `{"color":"#0000ff","owner":"other"}`)
		}()
		go func() {
			defer wg.Done()
			del(t, h, "/devices/0/keys/led:3?owner=first")
		}()
		wg.Wait()
		st, ok := eng.Status(target, time.Now())
		if c := out.get(led); !ok || st.Origin.Owner != "other" || c != blue {
			t.Fatalf("round %d: other owner's write lost: record %+v (%v), color %s", i, st, ok, fmt.Sprint(c))
		}
	}
}
