package dispatcher

import (
	"errors"
	"testing"
	"time"
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

func TestPoolKeyOwnedDirectlyIsSkipped(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4, 5}})
	r.MarkDirect("a", 4)
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 5 {
		t.Errorf("claim = %d, %v; want LED 5 (4 is direct-owned)", got, err)
	}
}
