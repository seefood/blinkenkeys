package dispatcher

import (
	"slices"

	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// Layout is a device's key roles, by idx: (reading-order) index — see
// config.KeyLayout. Tabs are the slots blincli maps a terminal's tab number
// onto; Pool is where named claims are auto-assigned from. DefaultPool means
// Pool is unset: use every key with row >= 1 that is not a tab slot (the
// pre-layout behavior).
type Layout struct {
	Tabs        []uint16
	Pool        []uint16
	DefaultPool bool
	// Displace selects the collision policy: a direct write onto a named
	// claim moves the claim to the next unclaimed pool key (see
	// MarkDirectDisplacing) instead of releasing it.
	Displace bool
}

// DefaultLayout is what a device with no configured layout gets.
var DefaultLayout = Layout{DefaultPool: true}

// SetLayout records device's layout. It may be called before the device
// exists, so config can be applied ahead of enumeration. l's slices are
// copied.
func (r *Registry) SetLayout(device string, l Layout) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.layouts == nil {
		r.layouts = make(map[string]Layout)
	}
	r.layouts[device] = l.clone()
}

// Layout returns a copy of device's layout, or DefaultLayout if none was set.
func (r *Registry) Layout(device string) Layout {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.layoutLocked(device).clone()
}

// clone copies l's slices; slices.Clone keeps nil nil and empty empty, which
// matters for Pool (nil vs empty are different policies).
func (l Layout) clone() Layout {
	l.Tabs, l.Pool = slices.Clone(l.Tabs), slices.Clone(l.Pool)
	return l
}

func (r *Registry) layoutLocked(device string) Layout {
	if l, ok := r.layouts[device]; ok {
		return l
	}
	return DefaultLayout
}

// ledsOf resolves idx: numbers against s's capabilities to the set of LED
// indexes they name; numbers off the matrix are dropped. Callers hold r.mu
// and have already checked s.caps != nil.
func ledsOf(s *slot, idxs []uint16) map[uint16]bool {
	out := make(map[uint16]bool, len(idxs))
	for _, n := range idxs {
		if led, ok := keyaddr.Resolve(keyaddr.Address{Kind: keyaddr.Idx, N: n}, s.caps.LEDCount, s.caps.Positions); ok {
			out[led] = true
		}
	}
	return out
}
