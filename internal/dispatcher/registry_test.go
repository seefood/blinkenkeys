package dispatcher

import (
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/hid"
)

// fakeController is reused by every test file in this package.
type fakeController struct {
	numLEDs   uint16
	positions map[uint16][2]uint8
	lastSet   []hid.KeyColor
	setErr    error
}

func (f *fakeController) SetKeys(keys []hid.KeyColor) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.lastSet = keys
	return nil
}

func (f *fakeController) GetNumberLEDs() (uint16, error) { return f.numLEDs, nil }

func (f *fakeController) GetLEDInfo(index uint16) (uint8, uint8, error) {
	pos := f.positions[index]
	return pos[0], pos[1], nil
}

func (f *fakeController) Close() error { return nil }

// registryWithConnected builds a Registry with one pre-seeded Connected slot
// under exactly the given name, bypassing Reconcile's identity-based naming.
// Used by dispatcher_test.go and redraw_test.go (Tasks 7 and 9), which only
// care about dispatch/redraw behavior for a known device name — this file is
// what actually tests Reconcile's naming/rewire/eviction logic.
func registryWithConnected(name string, ctrl hid.Controller) *Registry {
	return &Registry{slots: map[string]*slot{name: {base: name, state: stateConnected, ctrl: ctrl}}}
}

// registryWithConnectedMulti is registryWithConnected for more than one
// pre-seeded device at once.
func registryWithConnectedMulti(devices map[string]hid.Controller) *Registry {
	r := &Registry{slots: make(map[string]*slot, len(devices))}
	for name, ctrl := range devices {
		r.slots[name] = &slot{base: name, state: stateConnected, ctrl: ctrl}
	}
	return r
}

func TestRegistryReconcileAssignsNameAndDetectsReconnect(t *testing.T) {
	r := NewRegistry()
	id := hid.Identity{HasUID: true, UID: [8]byte{1}}

	res := r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	if len(res.Devices) != 1 {
		t.Fatalf("first Reconcile: Devices = %+v, want 1 entry", res.Devices)
	}
	var name string
	for n := range res.Devices {
		name = n
	}
	if len(res.Reconnected) != 0 {
		t.Fatalf("first Reconcile: Reconnected = %v, want none (fresh slot, not a rewire — nothing to redraw from an empty cache)", res.Reconnected)
	}

	res = r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	if len(res.Reconnected) != 0 {
		t.Fatalf("second Reconcile (still present): Reconnected = %v, want none", res.Reconnected)
	}

	res = r.Reconcile(nil, time.Now(), UntetheredMaxAge)
	if len(res.Devices) != 0 {
		t.Fatalf("Reconcile after disconnect: Devices = %+v, want none (untethered, not gone)", res.Devices)
	}

	res = r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	if len(res.Reconnected) != 1 || res.Reconnected[0] != name {
		t.Fatalf("Reconcile after reconnect: Reconnected = %v, want [%s] (rewire)", res.Reconnected, name)
	}
}

func TestRegistryReconcileDoesNotStealConnectedSlot(t *testing.T) {
	r := NewRegistry()
	id := hid.Identity{VendorID: 0x5754, ProductID: 0xc401, Path: "same-path-edge-case"}

	res := r.Reconcile([]PresentDevice{
		{Identity: id, Ctrl: &fakeController{}},
		{Identity: id, Ctrl: &fakeController{}},
	}, time.Now(), UntetheredMaxAge)

	if len(res.Devices) != 2 {
		t.Fatalf("Devices = %+v, want 2 entries (one per simultaneous device)", res.Devices)
	}
	names := make([]string, 0, 2)
	for n := range res.Devices {
		names = append(names, n)
	}
	if names[0] == names[1] {
		t.Fatalf("both devices got name %q, want distinct names (second must not steal the first's slot)", names[0])
	}
}

func TestRegistryReconcileEvictsStaleUntethered(t *testing.T) {
	r := NewRegistry()
	id := hid.Identity{HasUID: true, UID: [8]byte{9}}
	t0 := time.Now()

	res := r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, t0, UntetheredMaxAge)
	var name string
	for n := range res.Devices {
		name = n
	}

	r.Reconcile(nil, t0.Add(time.Minute), UntetheredMaxAge) // disconnect

	res = r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, t0.Add(time.Hour), UntetheredMaxAge)
	if len(res.Reconnected) != 1 || res.Reconnected[0] != name {
		t.Fatalf("reconnect before eviction: Reconnected = %v, want [%s] (still within 24h)", res.Reconnected, name)
	}

	r.Reconcile(nil, t0.Add(2*time.Hour), UntetheredMaxAge) // disconnect again

	evictRes := r.Reconcile(nil, t0.Add(2*time.Hour+UntetheredMaxAge+time.Minute), UntetheredMaxAge)
	if len(evictRes.Evicted) != 1 || evictRes.Evicted[0] != name {
		t.Fatalf("Evicted = %v, want [%s] (untethered past UntetheredMaxAge)", evictRes.Evicted, name)
	}

	freshRes := r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, t0.Add(3*time.Hour), UntetheredMaxAge)
	if len(freshRes.Reconnected) != 0 {
		t.Errorf("Reconnected after eviction = %v, want none (fresh slot, not a rewire — old cache must not be reused)", freshRes.Reconnected)
	}
	if len(freshRes.Devices) != 1 {
		t.Errorf("Devices after post-eviction reconnect = %+v, want 1 entry", freshRes.Devices)
	}
}

func TestRegistryGetAndSummaries(t *testing.T) {
	r := NewRegistry()
	r.Reconcile([]PresentDevice{{Identity: hid.Identity{HasUID: true, UID: [8]byte{1}}, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)

	if _, ok := r.Get("missing"); ok {
		t.Error("Get(missing) = true, want false")
	}
	var name string
	for _, s := range r.Summaries() {
		name = s.Name
	}
	if _, ok := r.Get(name); !ok {
		t.Errorf("Get(%s) = false, want true", name)
	}
	summaries := r.Summaries()
	if len(summaries) != 1 || summaries[0].Name != name || !summaries[0].Connected {
		t.Errorf("Summaries = %+v", summaries)
	}
}
