package dispatcher

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// DefaultClaimIdleTimeout is how long a named claim survives with no write
// under that name before RunClaimSweep releases it, when config.yaml
// doesn't set claims.idle_timeout.
const DefaultClaimIdleTimeout = 8 * time.Hour

// ClaimSweepInterval is how often RunClaimSweep checks for idle claims.
const ClaimSweepInterval = 5 * time.Minute

// noMatrixKeyRowCol is VialRGB's row/col for an LED with no matrix key (e.g.
// underglow) — see keyaddr's noKey. Excluded from the named-key pool, same
// as it's excluded from idx: numbering.
const noMatrixKeyRowCol = 0xFF

// ErrNoUnclaimedKeys is returned when a name's first write can't be
// auto-assigned a key because every eligible key (row >= 1) is already
// owned by another name or by a direct write.
var ErrNoUnclaimedKeys = errors.New("dispatcher: no unclaimed keys available")

// ErrClaimNotFound is returned by ReleaseClaim for a name with no claim to
// release — internal/api maps this to a 404.
var ErrClaimNotFound = errors.New("dispatcher: no claim under that name")

// keyOwner is one LED index's claim state within a device's slot; absent
// from slot.owners means unclaimed. Direct is set by a direct (R,C/led:/
// idx:) write and is permanent — only a name claim (Name != "") can be
// released back to the pool, via ReleaseClaim or idle timeout.
type keyOwner struct {
	Name   string
	Direct bool
}

// namedClaim is one name's assignment: which index it holds and when it was
// last written to, for SweepIdleClaims.
type namedClaim struct {
	index     uint16
	lastWrite time.Time
}

// ClaimOrGet resolves name to its claimed LED index on device: an existing
// claim's index is returned (refreshing its idle-timeout clock), otherwise
// the next unclaimed key is auto-claimed — row >= 1, ascending row/col
// order; row 0 is reserved for direct addressing. device's capabilities
// must already be known (the caller checks this via Caps/CapsOrPend before
// calling in).
func (r *Registry) ClaimOrGet(device, name string, now time.Time) (uint16, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[device]
	if !ok {
		return 0, ErrDeviceNotFound
	}
	return claimOrGetLocked(s, name, now)
}

// claimOrGetLocked is ClaimOrGet's core, callers must already hold r.mu —
// used directly by SetCaps, which resolves pending writes while still
// holding the lock it took to store the new capabilities.
func claimOrGetLocked(s *slot, name string, now time.Time) (uint16, error) {
	if s.caps == nil {
		return 0, ErrCapsUnknown
	}
	if c, ok := s.claims[name]; ok {
		c.lastWrite = now
		s.claims[name] = c
		return c.index, nil
	}
	idx, ok := nextUnclaimedLocked(s)
	if !ok {
		return 0, ErrNoUnclaimedKeys
	}
	if s.claims == nil {
		s.claims = make(map[string]namedClaim)
	}
	if s.owners == nil {
		s.owners = make(map[uint16]keyOwner)
	}
	s.claims[name] = namedClaim{index: idx, lastWrite: now}
	s.owners[idx] = keyOwner{Name: name}
	return idx, nil
}

// resolveLocked maps addr to an LED index given s's already-known
// capabilities: a Name addr claims (or reuses) a pooled key; any other form
// resolves against the matrix. Callers hold r.mu.
func resolveLocked(s *slot, addr keyaddr.Address, now time.Time) (uint16, error) {
	if addr.Kind == keyaddr.Name {
		return claimOrGetLocked(s, addr.Name, now)
	}
	idx, ok := keyaddr.Resolve(addr, s.caps.LEDCount, s.caps.Positions)
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrKeyNotFound, addr)
	}
	return idx, nil
}

// nextUnclaimedLocked returns the lowest-index eligible key (row >= 1,
// ascending row/col order, excluding LEDs with no matrix key) not already
// owned by a name or a direct claim. Callers hold r.mu.
func nextUnclaimedLocked(s *slot) (uint16, bool) {
	positions := append([]LEDPosition(nil), s.caps.Positions...)
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].Row != positions[j].Row {
			return positions[i].Row < positions[j].Row
		}
		return positions[i].Col < positions[j].Col
	})
	for _, p := range positions {
		if p.Row == 0 || p.Row == noMatrixKeyRowCol {
			continue
		}
		if _, owned := s.owners[p.Index]; owned {
			continue
		}
		return p.Index, true
	}
	return 0, false
}

// MarkDirect records idx on device as claimed-by-direct, releasing any name
// claim that held it (that name's next write claims a fresh key instead of
// contesting this one). Called once per freshly resolved direct (R,C/led:/
// idx:) address, never for the same address's repeated effect-tick writes —
// see Dispatcher.Canonical and applyPending, the two places a raw address's
// original kind is still known.
func (r *Registry) MarkDirect(device string, idx uint16) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[device]
	if !ok {
		return
	}
	markDirectLocked(s, idx)
}

// markDirectLocked is MarkDirect's core, callers must already hold r.mu.
func markDirectLocked(s *slot, idx uint16) {
	if owner, ok := s.owners[idx]; ok && owner.Name != "" {
		delete(s.claims, owner.Name)
	}
	if s.owners == nil {
		s.owners = make(map[uint16]keyOwner)
	}
	s.owners[idx] = keyOwner{Direct: true}
}

// ReleaseClaim releases name's claim on device. ErrClaimNotFound if name has
// no claim there; ErrDeviceNotFound if device is unknown. There is no way to
// release a direct claim through this call — a direct write has no name to
// address it by.
func (r *Registry) ReleaseClaim(device, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[device]
	if !ok {
		return ErrDeviceNotFound
	}
	c, ok := s.claims[name]
	if !ok {
		return ErrClaimNotFound
	}
	delete(s.claims, name)
	delete(s.owners, c.index)
	return nil
}

// SweepIdleClaims releases every named claim, across all devices, whose
// last write is older than maxAge as of now. Direct claims never expire.
func (r *Registry) SweepIdleClaims(now time.Time, maxAge time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.slots {
		for name, c := range s.claims {
			if now.Sub(c.lastWrite) > maxAge {
				delete(s.claims, name)
				delete(s.owners, c.index)
			}
		}
	}
}
