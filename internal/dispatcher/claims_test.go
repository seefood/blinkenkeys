package dispatcher

import (
	"context"
	"errors"
	"testing"
	"time"
)

// threeRowPad has one row-0 key (reserved for direct addressing) and two
// row-1+ keys (eligible for the named-key pool), in matrix (row, col) order:
// idx 2 (row 1, col 0) before idx 1 (row 2, col 0).
func threeRowPad() *fakeController {
	return &fakeController{numLEDs: 3, positions: map[uint16][2]uint8{
		0: {0, 0}, // row 0: excluded from the pool
		1: {2, 0}, // row 2
		2: {1, 0}, // row 1: first eligible key in row/col order
	}}
}

func registryWithCaps(t *testing.T, device string, fc *fakeController) *Registry {
	t.Helper()
	d := New(registryWithConnected(device, fc), NewCache(), 8, discardLogger())
	runDispatcher(t, d)
	if _, err := d.GetCapabilities(context.Background(), device); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	return d.registry
}

func TestClaimOrGetAssignsRowOneOnwardInOrder(t *testing.T) {
	r := registryWithCaps(t, "a", threeRowPad())

	idx, err := r.ClaimOrGet("a", "esc", time.Now())
	if err != nil || idx != 2 {
		t.Fatalf("ClaimOrGet(esc) = %d, %v; want idx 2 (row 1, first eligible)", idx, err)
	}
	idx2, err := r.ClaimOrGet("a", "tab", time.Now())
	if err != nil || idx2 != 1 {
		t.Fatalf("ClaimOrGet(tab) = %d, %v; want idx 1 (row 2, next eligible)", idx2, err)
	}
}

func TestClaimOrGetReusesSameNameAndRefreshesClock(t *testing.T) {
	r := registryWithCaps(t, "a", threeRowPad())
	t0 := time.Now()

	first, err := r.ClaimOrGet("a", "esc", t0)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	again, err := r.ClaimOrGet("a", "esc", t0.Add(time.Hour))
	if err != nil || again != first {
		t.Fatalf("ClaimOrGet(esc) again = %d, %v; want same idx %d", again, err, first)
	}

	// Idle timeout measured from the refreshed clock, not t0: not yet expired.
	r.SweepIdleClaims(t0.Add(time.Hour+DefaultClaimIdleTimeout-time.Minute), DefaultClaimIdleTimeout)
	if idx, err := r.ClaimOrGet("a", "esc", t0.Add(time.Hour)); err != nil || idx != first {
		t.Errorf("claim survived sweep before timeout: idx=%d err=%v, want %d", idx, err, first)
	}
}

func TestClaimOrGetPoolExhausted(t *testing.T) {
	r := registryWithCaps(t, "a", threeRowPad())
	now := time.Now()
	if _, err := r.ClaimOrGet("a", "esc", now); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := r.ClaimOrGet("a", "tab", now); err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if _, err := r.ClaimOrGet("a", "shift", now); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("third claim err = %v, want ErrNoUnclaimedKeys", err)
	}
}

func TestClaimOrGetUnknownDevice(t *testing.T) {
	r := NewRegistry()
	if _, err := r.ClaimOrGet("missing", "esc", time.Now()); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("err = %v, want ErrDeviceNotFound", err)
	}
}

func TestClaimOrGetCapsUnknown(t *testing.T) {
	r := NewRegistry()
	r.Declare("a")
	if _, err := r.ClaimOrGet("a", "esc", time.Now()); !errors.Is(err, ErrCapsUnknown) {
		t.Errorf("err = %v, want ErrCapsUnknown", err)
	}
}

func TestMarkDirectReleasesNameClaimAndIsPermanent(t *testing.T) {
	r := registryWithCaps(t, "a", threeRowPad())
	now := time.Now()

	idx, err := r.ClaimOrGet("a", "esc", now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	r.MarkDirect("a", idx)

	// esc's name claim was released; the next claim under that name gets a
	// fresh key rather than contesting the reclaimed one.
	fresh, err := r.ClaimOrGet("a", "esc", now)
	if err != nil || fresh == idx {
		t.Fatalf("ClaimOrGet(esc) after MarkDirect = %d, %v; want a different, freshly claimed idx (not %d)", fresh, err, idx)
	}

	// The directly-claimed index is excluded from the pool permanently, even
	// after a sweep interval that leaves esc's own (still-fresh) claim alone.
	r.SweepIdleClaims(now.Add(time.Minute), DefaultClaimIdleTimeout)
	if _, err := r.ClaimOrGet("a", "shift", now); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("pool err = %v, want ErrNoUnclaimedKeys (direct claim never released)", err)
	}
}

func TestReleaseClaim(t *testing.T) {
	r := registryWithCaps(t, "a", threeRowPad())
	now := time.Now()
	idx, err := r.ClaimOrGet("a", "esc", now)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := r.ReleaseClaim("a", "esc"); err != nil {
		t.Fatalf("ReleaseClaim: %v", err)
	}
	if got, err := r.ClaimOrGet("a", "tab", now); err != nil || got != idx {
		t.Errorf("ClaimOrGet(tab) after release = %d, %v; want the released idx %d back", got, err, idx)
	}

	if err := r.ReleaseClaim("a", "esc"); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("re-release err = %v, want ErrClaimNotFound", err)
	}
	if err := r.ReleaseClaim("missing", "esc"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("release on missing device err = %v, want ErrDeviceNotFound", err)
	}
}

func TestSweepIdleClaimsReleasesOnlyExpired(t *testing.T) {
	r := registryWithCaps(t, "a", threeRowPad())
	t0 := time.Now()
	oldIdx, err := r.ClaimOrGet("a", "esc", t0)
	if err != nil {
		t.Fatalf("claim esc: %v", err)
	}
	if _, err := r.ClaimOrGet("a", "tab", t0.Add(time.Hour)); err != nil {
		t.Fatalf("claim tab: %v", err)
	}

	r.SweepIdleClaims(t0.Add(DefaultClaimIdleTimeout+time.Minute), DefaultClaimIdleTimeout)

	if err := r.ReleaseClaim("a", "esc"); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("esc still claimed after sweep: err = %v, want ErrClaimNotFound (idle expired)", err)
	}
	if err := r.ReleaseClaim("a", "tab"); err != nil {
		t.Errorf("tab claim swept too early: %v", err)
	}

	// esc's index is back in the pool.
	if got, err := r.ClaimOrGet("a", "shift", t0); err != nil || got != oldIdx {
		t.Errorf("ClaimOrGet(shift) = %d, %v; want swept idx %d back in the pool", got, err, oldIdx)
	}
}
