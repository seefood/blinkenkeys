package dispatcher

import (
	"context"
	"fmt"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// KeyInfo describes one LED's ownership and matrix position, for read-only
// status reporting.
type KeyInfo struct {
	Name      string    // claimed name; "" if not name-claimed
	Direct    bool      // claimed by a direct (R,C/led:/idx:) write
	LastWrite time.Time // idle-clock reference; only meaningful when Name != ""
	Row, Col  uint8
	HasPos    bool // false for LEDs with no matrix key (e.g. underglow)
}

// LookupClaim returns name's claimed LED index and last-write time on
// device. Unlike ClaimOrGet it never claims and never refreshes the idle
// clock, so it is safe for read-only requests.
func (r *Registry) LookupClaim(device, name string) (uint16, time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[device]
	if !ok {
		return 0, time.Time{}, ErrDeviceNotFound
	}
	c, ok := s.claims[name]
	if !ok {
		return 0, time.Time{}, ErrClaimNotFound
	}
	return c.index, c.lastWrite, nil
}

// KeyInfo reports who holds LED idx on device and where it sits on the
// matrix. An unknown device or unowned LED yields the zero-owner KeyInfo.
func (r *Registry) KeyInfo(device string, idx uint16) KeyInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ki KeyInfo
	s, ok := r.slots[device]
	if !ok {
		return ki
	}
	if o, ok := s.owners[idx]; ok {
		ki.Name, ki.Direct = o.Name, o.Direct
		if o.Name != "" {
			ki.LastWrite = s.claims[o.Name].lastWrite
		}
	}
	if s.caps != nil {
		for _, p := range s.caps.Positions {
			if p.Index == idx {
				ki.Row, ki.Col = p.Row, p.Col
				ki.HasPos = p.Row != noMatrixKeyRowCol || p.Col != noMatrixKeyRowCol
				break
			}
		}
	}
	return ki
}

// Lookup resolves addr to led:N without side effects: a Name must already
// be claimed (ErrClaimNotFound otherwise) and is neither claimed nor
// refreshed; any other form resolves against the matrix without being
// marked claimed-by-direct. Contrast Canonical, which does both.
//
// "Without side effects" covers claims and ownership only: a non-name addr
// on a device whose capabilities are not yet known fetches them first
// (GetCapabilities: one HID query via the dispatcher goroutine, stored on
// the slot, and resolving any pending writes), exactly as a write would.
// That is deliberate: resolving R,C/idx: needs the matrix, and the fetch is
// idempotent. A name never triggers it.
func (d *Dispatcher) Lookup(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, error) {
	if addr.Kind == keyaddr.Name {
		idx, _, err := d.registry.LookupClaim(device, addr.Name)
		if err != nil {
			return keyaddr.Address{}, err
		}
		return keyaddr.Address{Kind: keyaddr.LED, N: idx}, nil
	}
	caps, err := d.GetCapabilities(ctx, device)
	if err != nil {
		return keyaddr.Address{}, err
	}
	idx, ok := keyaddr.Resolve(addr, caps.LEDCount, caps.Positions)
	if !ok {
		return keyaddr.Address{}, fmt.Errorf("%w: %s", ErrKeyNotFound, addr)
	}
	return keyaddr.Address{Kind: keyaddr.LED, N: idx}, nil
}

// KeyInfo is Registry.KeyInfo.
func (d *Dispatcher) KeyInfo(device string, led uint16) KeyInfo {
	return d.registry.KeyInfo(device, led)
}

// CurrentColor returns the frame buffer's color for one LED. It is the
// desired color: VialRGB gives no way to read hardware state back.
func (d *Dispatcher) CurrentColor(device string, led uint16) (color.HSV, bool) {
	k, ok := d.cache.Get(device, led)
	return color.HSV{H: k.H, S: k.S, V: k.V}, ok
}

// Layout is Registry.Layout.
func (d *Dispatcher) Layout(device string) Layout { return d.registry.Layout(device) }

// Connected reports whether device currently has an open controller.
func (d *Dispatcher) Connected(device string) bool {
	_, ok := d.registry.Get(device)
	return ok
}
