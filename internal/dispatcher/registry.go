package dispatcher

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
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
	caps           *Capabilities  // nil until first fetched; survives Untethered
	optional       bool           // config-declared optional: never evicted
	pending        []PendingWrite // writes awaiting caps, in arrival order

	// claims and owners are the named-key claim model (see claims.go): claims
	// is name -> assignment, owners is index -> current owner. Kept in sync
	// with each other under r.mu.
	claims map[string]namedClaim
	owners map[uint16]keyOwner
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
	// Added is every name whose slot was created this cycle (a device never
	// seen before) — the caller logs a first-seen hint for each.
	Added []string
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

// Summaries lists every known device, Connected or Untethered (including
// declared, never-seen ones), sorted by name — list position is the
// device's ordinal (see ResolveDevice).
func (r *Registry) Summaries() []DeviceSummary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]DeviceSummary, 0, len(r.slots))
	for _, name := range r.sortedNamesLocked() {
		out = append(out, DeviceSummary{Name: name, Connected: r.slots[name].state == stateConnected})
	}
	return out
}

func (r *Registry) sortedNamesLocked() []string {
	names := make([]string, 0, len(r.slots))
	for name := range r.slots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Declare creates an Untethered, eviction-exempt slot named id with base
// identity id — a config.yaml devices: entry with optional: true — so writes
// to it succeed before the device is first seen, and Reconcile's existing
// base-identity matching claims it when it enumerates. If id is already a
// slot, Declare only marks it optional.
func (r *Registry) Declare(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.slots[id]; ok {
		s.optional = true
		return
	}
	r.slots[id] = &slot{base: id, state: stateUntethered, optional: true}
}

// Caps returns name's stored capabilities. known is false if they were
// never fetched; exists is false if name has no slot.
func (r *Registry) Caps(name string) (caps Capabilities, known, exists bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[name]
	if !ok {
		return Capabilities{}, false, false
	}
	if s.caps == nil {
		return Capabilities{}, false, true
	}
	return *s.caps, true, true
}

// CapsOrPend atomically either returns name's capabilities or, if they
// aren't known yet, records w as pending (replacing any earlier pending
// write to the same literal address, which moves to the end). Atomicity is
// what keeps a write from slipping between "caps unknown" and SetCaps.
func (r *Registry) CapsOrPend(name string, w PendingWrite) (caps Capabilities, pended, exists bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[name]
	if !ok {
		return Capabilities{}, false, false
	}
	if s.caps != nil {
		return *s.caps, false, true
	}
	for i, p := range s.pending {
		if p.Addr == w.Addr {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			break
		}
	}
	s.pending = append(s.pending, w)
	return Capabilities{}, true, true
}

// SetCaps stores caps on name's slot and, if any writes were pending,
// resolves each one (in arrival order) against the new caps before clearing
// them: a resolved write is passed to apply as its (index, color); one that
// can't resolve (e.g. now off the matrix) goes to dropped instead. A
// resolved direct-form write is marked claimed-by-direct here, same as a
// live Canonical call, releasing any name claim that held its index. Both
// callbacks run while the registry lock is still held: any Write that
// resolves against the new caps must first acquire this lock in CapsOrPend,
// so it always lands in the cache after the pending writes it supersedes.
// apply/dropped may be nil.
func (r *Registry) SetCaps(name string, caps Capabilities, apply func(idx uint16, c color.HSV), dropped func(w PendingWrite, err error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[name]
	if !ok {
		return
	}
	s.caps = &caps
	now := time.Now()
	for _, w := range s.pending {
		idx, err := resolveLocked(s, w.Addr, now)
		if err != nil {
			if dropped != nil {
				dropped(w, err)
			}
			continue
		}
		if w.Addr.Kind != keyaddr.Name {
			markDirectLocked(s, idx)
		}
		if apply != nil {
			apply(idx, w.Color)
		}
	}
	s.pending = nil
}

// ResolveDevice maps a {name} path segment to a slot name: either an exact
// name, or a numeric ordinal — the rank among all slots sorted by name,
// computed fresh on each call (so not stable across topology changes).
func (r *Registry) ResolveDevice(ref string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.slots[ref]; ok {
		return ref, true
	}
	n, err := strconv.ParseUint(ref, 10, 31)
	if err != nil {
		return "", false
	}
	names := r.sortedNamesLocked()
	if int(n) >= len(names) {
		return "", false
	}
	return names[n], true
}

// ConnectedWithoutCaps lists Connected slots whose capabilities were never
// fetched (new, or whose earlier fetch failed), sorted by name — the poll
// loop retries EnsureCapabilities for each on every cycle.
func (r *Registry) ConnectedWithoutCaps() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, name := range r.sortedNamesLocked() {
		s := r.slots[name]
		if s.state == stateConnected && s.caps == nil {
			out = append(out, name)
		}
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
	var added []string
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
		added = append(added, name)
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
			if !s.optional && now.Sub(s.disconnectedAt) > maxAge {
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

	return ReconcileResult{Devices: devices, Reconnected: reconnected, Evicted: evicted, Added: added}
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
