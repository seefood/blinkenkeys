package dispatcher

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// sixLEDPad has LED i at matrix (row i/2, col i%2): reading-order idx == LED index.
func sixLEDPad() *fakeController {
	return &fakeController{numLEDs: 6, positions: map[uint16][2]uint8{
		0: {0, 0}, 1: {0, 1}, 2: {1, 0}, 3: {1, 1}, 4: {2, 0}, 5: {2, 1},
	}}
}

func TestExplicitPoolIsUsedInListedOrder(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{5, 4}})
	for i, want := range []uint16{5, 4} {
		got, err := r.ClaimOrGet("a", string(rune('x'+i)), time.Now())
		if err != nil || got != want {
			t.Fatalf("claim %d = %d, %v; want %d", i, got, err, want)
		}
	}
	if _, err := r.ClaimOrGet("a", "z", time.Now()); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("exhausted pool: err = %v, want ErrNoUnclaimedKeys", err)
	}
}

func TestEmptyExplicitPoolNeverClaims(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Tabs: []uint16{0, 1}, Pool: []uint16{}})
	if _, err := r.ClaimOrGet("a", "x", time.Now()); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("err = %v, want ErrNoUnclaimedKeys", err)
	}
}

func TestDefaultPoolSkipsTabs(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	// default pool = row >= 1 keys: LEDs 2..5; tab slot idx 2 removes LED 2.
	r.SetLayout("a", Layout{Tabs: []uint16{2}, DefaultPool: true})
	got, err := r.ClaimOrGet("a", "x", time.Now())
	if err != nil || got != 3 {
		t.Errorf("claim = %d, %v; want LED 3 (LED 2 is a tab slot)", got, err)
	}
}

func TestPoolIndexesOffTheMatrixAreSkipped(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{99, 4}})
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 4 {
		t.Errorf("claim = %d, %v; want LED 4", got, err)
	}
}

func TestNoLayoutKeepsLegacyPool(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 2 {
		t.Errorf("claim = %d, %v; want LED 2 (first row>=1 key)", got, err)
	}
	if l := r.Layout("a"); !l.DefaultPool {
		t.Errorf("Layout with none set = %+v, want DefaultPool", l)
	}
}

// Pending writes resolved by SetCaps honor the layout: a pending name claims
// from the configured pool in listed order, and a later pending direct write
// onto that key is last-wins (the claim is released, never displaced), even
// with collision: displace — see the spec's applyPending note.
func TestSetCapsResolvesPendingNamedWritesUnderLayout(t *testing.T) {
	r := NewRegistry()
	r.Declare("a")
	r.SetLayout("a", Layout{Tabs: []uint16{0, 1}, Pool: []uint16{5, 4}, Displace: true})
	x := keyaddr.Address{Kind: keyaddr.Name, Name: "x"}
	y := keyaddr.Address{Kind: keyaddr.Name, Name: "y"}
	r.CapsOrPend("a", PendingWrite{Addr: x, Color: color.HSV{H: 1}})
	r.CapsOrPend("a", PendingWrite{Addr: y, Color: color.HSV{H: 2}})
	r.CapsOrPend("a", PendingWrite{Addr: keyaddr.Address{Kind: keyaddr.Idx, N: 5}, Color: color.HSV{H: 3}})

	caps := Capabilities{LEDCount: 6}
	for i := uint16(0); i < 6; i++ {
		caps.Positions = append(caps.Positions, LEDPosition{Index: i, Row: uint8(i / 2), Col: uint8(i % 2)})
	}
	var applied []uint16
	var hues []uint8
	r.SetCaps("a", caps, func(idx uint16, c color.HSV) {
		applied, hues = append(applied, idx), append(hues, c.H)
	}, func(w PendingWrite, err error) { t.Errorf("dropped %s: %v", w.Addr, err) })

	if !slices.Equal(applied, []uint16{5, 4, 5}) || !slices.Equal(hues, []uint8{1, 2, 3}) {
		t.Fatalf("applied idx %v hues %v, want [5 4 5] [1 2 3]", applied, hues)
	}
	if _, _, err := r.LookupClaim("a", "x"); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("x must be released by the pending direct write (last-wins), err = %v", err)
	}
	if idx, _, err := r.LookupClaim("a", "y"); err != nil || idx != 4 {
		t.Errorf("y = %d, %v; want 4", idx, err)
	}
	if info := r.KeyInfo("a", 5); !info.Direct || info.Name != "" {
		t.Errorf("LED 5 = %+v, want direct-owned", info)
	}
}

// SetLayout and Layout copy Tabs/Pool: neither the caller's slice nor a
// returned one aliases the registry's copy. nil vs empty is preserved.
func TestLayoutSlicesAreCopied(t *testing.T) {
	r := NewRegistry()
	tabs, pool := []uint16{0, 1}, []uint16{4, 5}
	r.SetLayout("a", Layout{Tabs: tabs, Pool: pool})
	tabs[0], pool[0] = 9, 9
	got := r.Layout("a")
	if got.Tabs[0] != 0 || got.Pool[0] != 4 {
		t.Fatalf("SetLayout aliased the caller's slices: %+v", got)
	}
	got.Tabs[1], got.Pool[1] = 9, 9
	if again := r.Layout("a"); again.Tabs[1] != 1 || again.Pool[1] != 5 {
		t.Errorf("Layout returned the registry's own slices: %+v", again)
	}
	r.SetLayout("b", Layout{Pool: []uint16{}})
	if l := r.Layout("b"); l.Pool == nil || l.Tabs != nil {
		t.Errorf("nil/empty not preserved: Tabs %#v Pool %#v", l.Tabs, l.Pool)
	}
}

func TestPoolKeyOwnedDirectlyIsSkipped(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4, 5}})
	r.MarkDirect("a", 4)
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 5 {
		t.Errorf("claim = %d, %v; want LED 5 (4 is direct-owned)", got, err)
	}
}
