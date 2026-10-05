package dispatcher

import (
	"errors"
	"testing"
	"time"
)

func TestDisplaceMovesNamedClaimToNextPoolKey(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4, 5}, Displace: true})
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 4 {
		t.Fatalf("setup claim = %d, %v", got, err)
	}
	m := r.MarkDirectDisplacing("a", 4)
	if want := (Moved{Name: "x", From: 4, To: 5}); m != want {
		t.Fatalf("Moved = %+v, want %+v", m, want)
	}
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 5 {
		t.Errorf("x now at %d, %v; want 5", got, err)
	}
	// key 4 is direct-owned now: the pool (4, 5) has nothing left for a new name.
	if _, err := r.ClaimOrGet("a", "y", time.Now()); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("err = %v, want ErrNoUnclaimedKeys", err)
	}
}

func TestDisplaceWithFullPoolDropsTheClaim(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4}, Displace: true})
	if _, err := r.ClaimOrGet("a", "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	m := r.MarkDirectDisplacing("a", 4)
	if want := (Moved{Name: "x", From: 4, Dropped: true}); m != want {
		t.Fatalf("Moved = %+v, want %+v", m, want)
	}
	if _, err := r.ClaimOrGet("a", "x", time.Now()); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("claim must be gone and key 4 direct-owned: err = %v", err)
	}
}

// Every pool slot claimed: the displaced claim is released, the other claim is
// untouched, and nothing is moved onto an owned key (no half-update).
func TestDisplaceWithEveryPoolSlotClaimedLeavesOthersIntact(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4, 5}, Displace: true})
	now := time.Now()
	if got, err := r.ClaimOrGet("a", "x", now); err != nil || got != 4 {
		t.Fatalf("x = %d, %v", got, err)
	}
	if got, err := r.ClaimOrGet("a", "y", now); err != nil || got != 5 {
		t.Fatalf("y = %d, %v", got, err)
	}
	m := r.MarkDirectDisplacing("a", 4)
	if want := (Moved{Name: "x", From: 4, Dropped: true}); m != want {
		t.Fatalf("Moved = %+v, want %+v", m, want)
	}
	if got, err := r.ClaimOrGet("a", "y", now); err != nil || got != 5 {
		t.Errorf("y must keep key 5: %d, %v", got, err)
	}
	if _, err := r.ClaimOrGet("a", "x", now); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("x released and pool full: err = %v", err)
	}
	// Displacing y while everything is owned is the same drop, no panic.
	if m := r.MarkDirectDisplacing("a", 5); m != (Moved{Name: "y", From: 5, Dropped: true}) {
		t.Errorf("second Moved = %+v", m)
	}
}

func TestDisplaceOnUnknownDeviceIsNoop(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	if m := r.MarkDirectDisplacing("nope", 4); m != (Moved{}) {
		t.Errorf("Moved = %+v, want zero", m)
	}
}

func TestLastWinsReleasesTheClaim(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4, 5}}) // Displace unset
	if _, err := r.ClaimOrGet("a", "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	m := r.MarkDirectDisplacing("a", 4)
	if m.Name != "x" || !m.Dropped {
		t.Errorf("Moved = %+v, want x dropped", m)
	}
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 5 {
		t.Errorf("x reclaims the next free key: %d, %v; want 5", got, err)
	}
}

func TestDisplaceOnUnclaimedOrDirectKeyJustMarksDirect(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4, 5}, Displace: true})
	if m := r.MarkDirectDisplacing("a", 4); m.Name != "" {
		t.Errorf("unclaimed key: Moved = %+v, want zero", m)
	}
	if m := r.MarkDirectDisplacing("a", 4); m.Name != "" {
		t.Errorf("direct key (two direct writers are last-wins): Moved = %+v, want zero", m)
	}
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 5 {
		t.Errorf("claim = %d, %v; want 5 (4 is direct-owned)", got, err)
	}
}
