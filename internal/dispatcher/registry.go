package dispatcher

import (
	"fmt"
	"sync"
	"time"

	"github.com/seefood/blinkenkeys/internal/hid"
)

// UntetheredMaxAge is how long a disconnected device's name/cache slot stays
// available for reconnect-rewire before Reconcile deletes it outright, per
// the design spec's "State persistence & refresh" untethered-rewire note.
const UntetheredMaxAge = 24 * time.Hour

type slotState int

const (
	stateConnected slotState = iota
	stateUntethered
)

// slot is one name's assignment history: the base identity it was assigned
// from (for matching future Reconcile calls), its current state, and — only
// meaningful in the corresponding state — its live controller or the time it
// went untethered.
type slot struct {
	base           string
	state          slotState
	ctrl           hid.Controller // set only while state == stateConnected
	disconnectedAt time.Time      // set only while state == stateUntethered
}

// PresentDevice pairs one currently-open device's resolved identity with its
// controller for a single Reconcile call. Reconcile — not the caller —
// decides its name.
type PresentDevice struct {
	Identity hid.Identity
	Ctrl     hid.Controller
}

// ReconcileResult is Reconcile's outcome for one poll cycle.
type ReconcileResult struct {
	// Devices is every slot now Connected: name -> controller.
	Devices map[string]hid.Controller
	// Reconnected is every name rewired from an Untethered slot back to
	// Connected this cycle — the caller should redraw these from Cache.
	Reconnected []string
	// Evicted is every name whose Untethered slot just aged past maxAge and
	// was deleted this cycle — the caller should also call Cache.Forget on
	// each of these.
	Evicted []string
}

// Registry is blinkenkeysd's persistent name-assignment and presence state: name
// -> slot. Unlike a stateless per-poll naming pass, Registry remembers slots
// across polls so a device that disconnects doesn't lose its name or cached
// colors immediately — see Reconcile.
type Registry struct {
	mu    sync.Mutex
	slots map[string]*slot
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{slots: make(map[string]*slot)}
}

// Get returns the open controller for name, if currently Connected.
func (r *Registry) Get(name string) (hid.Controller, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[name]
	if !ok || s.state != stateConnected {
		return nil, false
	}
	return s.ctrl, true
}

// Summaries lists every known device, Connected or Untethered.
func (r *Registry) Summaries() []DeviceSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]DeviceSummary, 0, len(r.slots))
	for name, s := range r.slots {
		out = append(out, DeviceSummary{Name: name, Connected: s.state == stateConnected})
	}
	return out
}

// Reconcile resolves present's identities to names for one poll cycle: a
// present device matching an existing Untethered slot's base identity
// rewires into it (reported in Reconnected, so the caller replays its
// retained Cache entry), a present device matching an already-Connected
// slot's identity never steals it and instead gets a fresh dedup-suffixed
// name with an implicitly empty cache, and a present device matching no
// existing slot gets a brand-new one. Dedup suffixing (-0, -1, ...) is scoped
// across every name Registry has ever assigned, not just this cycle's
// present set, so a name freed by eviction can be reused but a still-live
// name never collides.
//
// Every existing slot not claimed by a PresentDevice this cycle transitions
// from Connected to Untethered (disconnectedAt = now); every Untethered slot
// already older than maxAge is deleted and reported in Evicted.
func (r *Registry) Reconcile(present []PresentDevice, now time.Time, maxAge time.Duration) ReconcileResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	claimed := make(map[string]bool, len(r.slots))
	usedNames := make(map[string]bool, len(r.slots))
	for name := range r.slots {
		usedNames[name] = true
	}
	nextSuffix := make(map[string]int)

	var reconnected []string
	for _, pd := range present {
		base := hid.BaseName(pd.Identity)

		if name, ok := r.findUnclaimedByBase(base, claimed); ok {
			s := r.slots[name]
			claimed[name] = true
			s.ctrl = pd.Ctrl
			if s.state == stateUntethered {
				s.state = stateConnected
				s.disconnectedAt = time.Time{}
				reconnected = append(reconnected, name)
			}
			continue
		}

		name := base
		if usedNames[name] {
			for {
				candidate := fmt.Sprintf("%s-%d", base, nextSuffix[base])
				nextSuffix[base]++
				if !usedNames[candidate] {
					name = candidate
					break
				}
			}
		}
		usedNames[name] = true
		claimed[name] = true
		r.slots[name] = &slot{base: base, state: stateConnected, ctrl: pd.Ctrl}
	}

	var evicted []string
	for name, s := range r.slots {
		if claimed[name] {
			continue
		}
		switch s.state {
		case stateConnected:
			s.state = stateUntethered
			s.ctrl = nil
			s.disconnectedAt = now
		case stateUntethered:
			if now.Sub(s.disconnectedAt) > maxAge {
				delete(r.slots, name)
				evicted = append(evicted, name)
			}
		}
	}

	devices := make(map[string]hid.Controller)
	for name, s := range r.slots {
		if s.state == stateConnected {
			devices[name] = s.ctrl
		}
	}

	return ReconcileResult{Devices: devices, Reconnected: reconnected, Evicted: evicted}
}

// findUnclaimedByBase returns the name of an existing, not-yet-claimed-this-
// cycle slot whose base identity matches base, if any. When more than one
// unclaimed slot shares an identical base string — only possible via the
// VID/PID-alone identity tier's inherent ambiguity (see Task 5) — which one
// matches is unspecified; this is that tier's pre-existing limitation, not a
// property Reconcile adds.
func (r *Registry) findUnclaimedByBase(base string, claimed map[string]bool) (string, bool) {
	for name, s := range r.slots {
		if claimed[name] || s.base != base {
			continue
		}
		return name, true
	}
	return "", false
}
