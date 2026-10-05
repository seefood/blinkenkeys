package dispatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func padDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	d := New(registryWithConnected("a", sixLEDPad()), NewCache(), 8, discardLogger())
	runDispatcher(t, d)
	if _, err := d.GetCapabilities(context.Background(), "a"); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	return d
}

func TestLookupNameDoesNotClaim(t *testing.T) {
	d := padDispatcher(t)
	name := keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}
	if _, err := d.Lookup(context.Background(), "a", name); !errors.Is(err, ErrClaimNotFound) {
		t.Fatalf("Lookup(unclaimed) err = %v, want ErrClaimNotFound", err)
	}
	if _, _, err := d.registry.LookupClaim("a", "esc"); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("Lookup created a claim (LookupClaim err = %v)", err)
	}
	idx, err := d.registry.ClaimOrGet("a", "esc", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Lookup(context.Background(), "a", name)
	if err != nil || got != (keyaddr.Address{Kind: keyaddr.LED, N: idx}) {
		t.Errorf("Lookup(claimed) = %+v, %v; want led:%d", got, err, idx)
	}
}

// Lookup is read-only: it must not refresh the claim's idle clock, or a GET
// would keep an abandoned claim alive past claims.idle_timeout.
func TestLookupLeavesIdleClockAndKeyInfoOnNamedClaim(t *testing.T) {
	d := padDispatcher(t)
	claimedAt := time.Now().Add(-time.Hour)
	idx, err := d.registry.ClaimOrGet("a", "esc", claimedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Lookup(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}); err != nil {
		t.Fatal(err)
	}
	if _, last, _ := d.registry.LookupClaim("a", "esc"); !last.Equal(claimedAt) {
		t.Errorf("idle clock = %v, want unchanged %v", last, claimedAt)
	}
	info := d.KeyInfo("a", idx)
	if info.Name != "esc" || info.Direct || !info.LastWrite.Equal(claimedAt) || !info.HasPos {
		t.Errorf("KeyInfo = %+v, want name esc, last write %v, with position", info, claimedAt)
	}
}

// Pins the documented behavior: a non-name Lookup on a device whose
// capabilities are not yet known fetches them once, through the dispatcher
// goroutine, and stores them. Claims and ownership stay untouched.
func TestLookupDirectFetchesUnknownCapsOnce(t *testing.T) {
	fc := sixLEDPad()
	d := New(registryWithConnected("a", fc), NewCache(), 8, discardLogger())
	runDispatcher(t, d)
	for i := 0; i < 2; i++ {
		if _, err := d.Lookup(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Idx, N: 2}); err != nil {
			t.Fatalf("Lookup #%d: %v", i, err)
		}
	}
	if n := fc.capsQueries(); n != 1 {
		t.Errorf("capability queries = %d, want 1", n)
	}
	if info := d.KeyInfo("a", 2); info.Direct || info.Name != "" {
		t.Errorf("Lookup changed ownership: %+v", info)
	}
}

func TestLookupUnknownDevice(t *testing.T) {
	d := padDispatcher(t)
	for _, addr := range []keyaddr.Address{{Kind: keyaddr.Name, Name: "esc"}, {Kind: keyaddr.Idx, N: 0}} {
		if _, err := d.Lookup(context.Background(), "nope", addr); !errors.Is(err, ErrDeviceNotFound) {
			t.Errorf("Lookup(nope, %s) err = %v, want ErrDeviceNotFound", addr, err)
		}
	}
	if info := d.KeyInfo("nope", 0); info != (KeyInfo{}) {
		t.Errorf("KeyInfo on unknown device = %+v, want zero", info)
	}
}

func TestLookupDirectDoesNotMarkDirect(t *testing.T) {
	d := padDispatcher(t)
	got, err := d.Lookup(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Idx, N: 3})
	if err != nil || got != (keyaddr.Address{Kind: keyaddr.LED, N: 3}) {
		t.Fatalf("Lookup(idx:3) = %+v, %v", got, err)
	}
	if info := d.KeyInfo("a", 3); info.Direct {
		t.Error("Lookup marked the key direct-owned")
	}
	if _, err := d.Lookup(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Idx, N: 99}); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("off-matrix err = %v, want ErrKeyNotFound", err)
	}
}

func TestKeyInfoCurrentColorLayoutConnected(t *testing.T) {
	d := padDispatcher(t)
	if _, err := d.Canonical(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Idx, N: 1}); err != nil {
		t.Fatal(err)
	}
	info := d.KeyInfo("a", 1)
	if !info.Direct || !info.HasPos || info.Row != 0 || info.Col != 1 {
		t.Errorf("KeyInfo = %+v", info)
	}
	if err := d.Write("a", keyaddr.Address{Kind: keyaddr.LED, N: 1}, color.HSV{H: 1, S: 2, V: 3}); err != nil {
		t.Fatal(err)
	}
	if c, ok := d.CurrentColor("a", 1); !ok || c != (color.HSV{H: 1, S: 2, V: 3}) {
		t.Errorf("CurrentColor = %+v, %v", c, ok)
	}
	if _, ok := d.CurrentColor("a", 4); ok {
		t.Error("CurrentColor for never-written LED reported ok")
	}
	if !d.Connected("a") || d.Connected("nope") {
		t.Error("Connected wrong")
	}
	if !d.Layout("a").DefaultPool {
		t.Error("Layout default expected")
	}
}
