package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func led(n uint16) keyaddr.Address { return keyaddr.Address{Kind: keyaddr.LED, N: n} }

func rc(r, c uint8) keyaddr.Address { return keyaddr.Address{Kind: keyaddr.RowCol, Row: r, Col: c} }

// withCaps seeds name's slot with an n-LED, single-row capability set.
func withCaps(r *Registry, name string, n int) *Registry {
	caps := Capabilities{LEDCount: n}
	for i := 0; i < n; i++ {
		caps.Positions = append(caps.Positions, LEDPosition{Index: uint16(i), Row: 0, Col: uint8(i)})
	}
	r.SetCaps(name, caps, nil, nil)
	return r
}

// waitFor polls cond every millisecond, failing the test after one second.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

// processQueued dispatches everything currently queued, synchronously, the
// way one Run iteration would.
func processQueued(d *Dispatcher) {
	d.process(d.drainAfter(<-d.queue))
}

func TestWriteUnknownDevice(t *testing.T) {
	d := New(NewRegistry(), NewCache(), 8, discardLogger())
	if err := d.Write("missing", led(0), color.HSV{}); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("err = %v, want ErrDeviceNotFound", err)
	}
	name := keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}
	if err := d.Write("missing", name, color.HSV{}); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("named key on missing device: err = %v, want ErrDeviceNotFound (device first)", err)
	}
}

func TestWriteNamedKeyClaimsAndReuses(t *testing.T) {
	fc := twoKeyPad()
	cache := NewCache()
	d := New(registryWithConnected("a", fc), cache, 8, discardLogger())
	runDispatcher(t, d)
	if _, err := d.GetCapabilities(context.Background(), "a"); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}

	esc := keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}
	if err := d.Write("a", esc, color.HSV{H: 1}); err != nil {
		t.Fatalf("Write(esc): %v", err)
	}
	if k, ok := cache.Get("a", 0); !ok || k.H != 1 {
		t.Errorf("cache LED 0 = %+v, %v, want claimed esc key H=1", k, ok)
	}
	if err := d.Write("a", esc, color.HSV{H: 2}); err != nil {
		t.Fatalf("Write(esc) again: %v", err)
	}
	if k, ok := cache.Get("a", 0); !ok || k.H != 2 {
		t.Errorf("cache LED 0 = %+v, %v, want reused esc key H=2 (same index)", k, ok)
	}
	if _, ok := cache.Get("a", 1); ok {
		t.Error("LED 1 written, want repeated esc write to reuse the same key")
	}
}

func TestWriteKeyOffMatrix(t *testing.T) {
	d := New(withCaps(registryWithConnected("a", &fakeController{}), "a", 4), NewCache(), 8, discardLogger())
	for _, addr := range []keyaddr.Address{led(4), rc(1, 0)} {
		if err := d.Write("a", addr, color.HSV{}); !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("Write(%s) err = %v, want ErrKeyNotFound", addr, err)
		}
	}
}

func TestWriteUpdatesCacheAndDelivers(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), cache, 8, discardLogger())
	runDispatcher(t, d)

	if err := d.Write("a", rc(0, 3), color.HSV{H: 1, S: 2, V: 3}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if k, ok := cache.Get("a", 3); !ok || k != (hid.KeyColor{Index: 3, H: 1, S: 2, V: 3}) {
		t.Errorf("cache.Get = %+v, %v (must be written synchronously)", k, ok)
	}
	waitFor(t, func() bool { return fc.lastSet() != nil })
	if got := fc.lastSet(); len(got) != 1 || got[0].Index != 3 || got[0].H != 1 {
		t.Errorf("SetKeys = %+v", got)
	}
}

func TestWriteUntetheredUpdatesCacheOnly(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	name := hid.BaseName(id)
	fc := &fakeController{}
	reg := NewRegistry()
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc}}, time.Now(), UntetheredMaxAge)
	withCaps(reg, name, 4)
	reg.Reconcile(nil, time.Now(), UntetheredMaxAge) // unplugged
	cache := NewCache()
	d := New(reg, cache, 8, discardLogger())

	if err := d.Write(name, led(1), color.HSV{H: 9}); err != nil {
		t.Fatalf("Write to untethered: %v", err)
	}
	processQueued(d)
	if _, ok := cache.Get(name, 1); !ok {
		t.Error("cache not updated for untethered device")
	}
	if fc.setCount() != 0 {
		t.Error("controller touched for untethered device")
	}
}

func TestWriteQueueFullStillSucceeds(t *testing.T) {
	cache := NewCache()
	d := New(withCaps(registryWithConnected("a", &fakeController{}), "a", 4), cache, 0, discardLogger())
	if err := d.Write("a", led(0), color.HSV{H: 5}); err != nil {
		t.Fatalf("Write with full queue: %v", err)
	}
	if k, _ := cache.Get("a", 0); k.H != 5 {
		t.Errorf("cache = %+v, want H 5", k)
	}
}

func TestFlushSendsLatestAndCollapsesDuplicates(t *testing.T) {
	fc := &fakeController{}
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), NewCache(), 8, discardLogger())

	_ = d.Write("a", led(1), color.HSV{H: 1})
	_ = d.Write("a", led(1), color.HSV{H: 2})
	processQueued(d)

	if fc.setCount() != 1 {
		t.Fatalf("SetKeys called %d times, want 1 (duplicates collapse)", fc.setCount())
	}
	if got := fc.lastSet(); len(got) != 1 || got[0].H != 2 {
		t.Errorf("SetKeys = %+v, want one key with H 2 (read at dispatch time)", got)
	}
}

func TestRedrawDoesNotOverwriteNewerWrite(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), cache, 8, discardLogger())

	_ = d.Write("a", led(0), color.HSV{H: 1})
	d.Redraw("a")
	_ = d.Write("a", led(0), color.HSV{H: 2})
	processQueued(d)

	if got := fc.lastSet(); len(got) != 1 || got[0].H != 2 {
		t.Errorf("SetKeys = %+v, want H 2", got)
	}
	if k, _ := cache.Get("a", 0); k.H != 2 {
		t.Errorf("cache H = %d, want 2 (Redraw must not write the cache)", k.H)
	}
}

func TestHIDWriteErrorIsLogged(t *testing.T) {
	var logs bytes.Buffer
	fc := &fakeController{setErr: errors.New("boom")}
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), NewCache(), 8, slog.New(slog.NewTextHandler(&logs, nil)))

	if err := d.Write("a", led(0), color.HSV{}); err != nil {
		t.Fatalf("Write must not surface HID errors: %v", err)
	}
	processQueued(d)
	if !strings.Contains(logs.String(), "boom") {
		t.Errorf("log %q missing HID error", logs.String())
	}
}

func TestWritePendingResolvesOnEnsureCapabilities(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{2}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	reg.Declare(name)
	cache := NewCache()
	var logs bytes.Buffer
	d := New(reg, cache, 8, slog.New(slog.NewTextHandler(&logs, nil)))
	runDispatcher(t, d)

	red, blue := color.HSV{H: 0, S: 255, V: 255}, color.HSV{H: 170, S: 255, V: 255}
	for _, w := range []struct {
		addr keyaddr.Address
		c    color.HSV
	}{{rc(1, 1), red}, {rc(1, 1), blue}, {rc(5, 9), red}} {
		if err := d.Write(name, w.addr, w.c); err != nil {
			t.Fatalf("Write(%s) to declared device: %v", w.addr, err)
		}
	}
	if cache.Snapshot(name) != nil {
		t.Fatal("pending writes must not reach the cache before caps are known")
	}

	fc := twoKeyPad()
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc}}, time.Now(), UntetheredMaxAge)
	if err := d.EnsureCapabilities(context.Background(), name); err != nil {
		t.Fatalf("EnsureCapabilities: %v", err)
	}
	if k, _ := cache.Get(name, 0); k.H != blue.H {
		t.Errorf("cache LED 0 = %+v, want blue (latest write wins)", k)
	}
	if !strings.Contains(logs.String(), "5,9") {
		t.Errorf("log %q does not name dropped 5,9", logs.String())
	}
	waitFor(t, func() bool { return fc.lastSet() != nil })
	if got := fc.lastSet(); got[0].H != blue.H {
		t.Errorf("delivered %+v, want blue", got)
	}
}

func TestCanonical(t *testing.T) {
	reg := withCaps(registryWithConnected("a", &fakeController{}), "a", 4)
	reg.Declare("b")
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)
	ctx := context.Background()

	if got, err := d.Canonical(ctx, "a", rc(0, 2)); err != nil || got != led(2) {
		t.Errorf("Canonical(a, 0,2) = %v, %v; want led:2", got, err)
	}
	// device "b" is declared but never connected: caps are unknown and can
	// never become known, so Canonical must report that rather than silently
	// returning an unresolved address — a caller keying an effects.Target off
	// that unresolved form would collide with a later, resolved write to the
	// same physical key once caps do arrive.
	if _, err := d.Canonical(ctx, "b", rc(0, 2)); !errors.Is(err, ErrCapsUnknown) {
		t.Errorf("Canonical(b, 0,2) = %v; want ErrCapsUnknown (declared, never connected)", err)
	}
	if _, err := d.Canonical(ctx, "missing", rc(0, 0)); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("missing: err = %v", err)
	}
	if _, err := d.Canonical(ctx, "a", led(9)); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("off matrix: err = %v", err)
	}
	if _, err := d.Canonical(ctx, "a", keyaddr.Address{Kind: keyaddr.Name, Name: "x"}); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("name with no eligible keys (row 0 only): err = %v, want ErrNoUnclaimedKeys", err)
	}
}

func TestCanonicalNamedKey(t *testing.T) {
	reg := registryWithConnected("a", twoKeyPad())
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	// Caps deliberately not pre-fetched: Canonical must resolve them itself
	// (blocking on the dispatcher, same as GetCapabilities) rather than
	// returning the name unchanged.
	esc := keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}
	got, err := d.Canonical(context.Background(), "a", esc)
	if err != nil || got != led(0) {
		t.Fatalf("Canonical(a, esc) = %v, %v; want led:0 (first eligible key)", got, err)
	}
	if got2, err := d.Canonical(context.Background(), "a", esc); err != nil || got2 != got {
		t.Errorf("Canonical(a, esc) again = %v, %v; want same claim %v", got2, err, got)
	}
}

func TestDispatcherListDevices(t *testing.T) {
	d := New(registryWithConnected("a", &fakeController{}), NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	devices, err := d.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "a" || !devices[0].Connected {
		t.Errorf("ListDevices = %+v", devices)
	}
}
