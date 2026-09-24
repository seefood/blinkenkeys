package dispatcher

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// fakeController is reused by every test file in this package. It's
// mutex-guarded because from Task 4 on, SetKeys runs on the dispatcher
// goroutine asynchronously to the test.
type fakeController struct {
	numLEDs    uint16
	positions  map[uint16][2]uint8
	setErr     error
	numLEDsErr error

	mu           sync.Mutex
	sets         [][]hid.KeyColor
	numLEDsCalls int
}

func (f *fakeController) SetKeys(keys []hid.KeyColor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.sets = append(f.sets, append([]hid.KeyColor(nil), keys...))
	return nil
}

func (f *fakeController) GetNumberLEDs() (uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.numLEDsCalls++
	if f.numLEDsErr != nil {
		return 0, f.numLEDsErr
	}
	return f.numLEDs, nil
}

func (f *fakeController) GetLEDInfo(index uint16) (uint8, uint8, error) {
	pos := f.positions[index]
	return pos[0], pos[1], nil
}

func (f *fakeController) Close() error { return nil }

// lastSet returns the most recent SetKeys argument, or nil if none.
func (f *fakeController) lastSet() []hid.KeyColor {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sets) == 0 {
		return nil
	}
	return f.sets[len(f.sets)-1]
}

func (f *fakeController) setCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sets)
}

func (f *fakeController) capsQueries() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.numLEDsCalls
}

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

func TestRegistryDeclareClaimedByDevice(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	name := hid.BaseName(id)
	r := NewRegistry()
	r.Declare(name)

	if s := r.Summaries(); len(s) != 1 || s[0].Name != name || s[0].Connected {
		t.Fatalf("Summaries after Declare = %+v, want one untethered %s", s, name)
	}
	res := r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	if !reflect.DeepEqual(res.Reconnected, []string{name}) {
		t.Errorf("Reconnected = %v, want [%s]", res.Reconnected, name)
	}
	if len(res.Added) != 0 {
		t.Errorf("Added = %v, want none (declared slot was claimed, not created)", res.Added)
	}
}

func TestRegistryReconcileReportsAdded(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{2}}
	r := NewRegistry()
	res := r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	if !reflect.DeepEqual(res.Added, []string{hid.BaseName(id)}) {
		t.Errorf("first Reconcile Added = %v", res.Added)
	}
	res = r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	if len(res.Added) != 0 {
		t.Errorf("second Reconcile Added = %v, want none", res.Added)
	}
}

func TestRegistryOptionalNeverEvicted(t *testing.T) {
	r := NewRegistry()
	r.Declare("uid-0100000000000000")
	res := r.Reconcile(nil, time.Now().Add(48*time.Hour), UntetheredMaxAge)
	if len(res.Evicted) != 0 {
		t.Fatalf("Evicted = %v, want none", res.Evicted)
	}
	if _, _, exists := r.Caps("uid-0100000000000000"); !exists {
		t.Error("declared slot gone after 48h")
	}
}

func TestRegistryCapsOrPendAndSetCaps(t *testing.T) {
	r := NewRegistry()
	r.Declare("a")
	a := keyaddr.Address{Kind: keyaddr.RowCol, Row: 1, Col: 1}
	b := keyaddr.Address{Kind: keyaddr.LED, N: 0}

	for _, w := range []PendingWrite{{a, color.HSV{H: 1}}, {b, color.HSV{H: 2}}, {a, color.HSV{H: 3}}} {
		if _, pended, exists := r.CapsOrPend("a", w); !pended || !exists {
			t.Fatalf("CapsOrPend(%v) pended=%v exists=%v", w, pended, exists)
		}
	}
	if _, _, exists := r.CapsOrPend("missing", PendingWrite{Addr: a}); exists {
		t.Error("CapsOrPend(missing) exists = true")
	}

	caps := Capabilities{LEDCount: 2, Positions: []LEDPosition{{Index: 0, Row: 0, Col: 0}, {Index: 1, Row: 1, Col: 1}}}
	type resolvedWrite struct {
		idx uint16
		c   color.HSV
	}
	var got []resolvedWrite
	var dropped []PendingWrite
	r.SetCaps("a", caps,
		func(idx uint16, c color.HSV) { got = append(got, resolvedWrite{idx, c}) },
		func(w PendingWrite, _ error) { dropped = append(dropped, w) },
	)
	want := []resolvedWrite{{0, color.HSV{H: 2}}, {1, color.HSV{H: 3}}} // b (LED 0), then a moved to the end (RowCol 1,1 -> LED 1)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("apply got %+v, want %+v", got, want)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %+v, want none", dropped)
	}

	caps2, pended, _ := r.CapsOrPend("a", PendingWrite{Addr: a})
	if pended || caps2.LEDCount != 2 {
		t.Errorf("CapsOrPend after SetCaps = %+v pended=%v", caps2, pended)
	}
	called := false
	r.SetCaps("a", caps, func(uint16, color.HSV) { called = true }, nil)
	if called {
		t.Error("apply called with no pending writes")
	}
}

func TestRegistryCapsRetainedWhenUntethered(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{3}}
	name := hid.BaseName(id)
	r := NewRegistry()
	r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	r.SetCaps(name, Capabilities{LEDCount: 4}, nil, nil)
	r.Reconcile(nil, time.Now(), UntetheredMaxAge)

	caps, known, exists := r.Caps(name)
	if !exists || !known || caps.LEDCount != 4 {
		t.Errorf("Caps after disconnect = %+v known=%v exists=%v", caps, known, exists)
	}
}

func TestRegistryResolveDevice(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"b", "a", "c"} {
		r.Declare(n)
	}
	tests := []struct {
		ref    string
		want   string
		wantOK bool
	}{
		{"0", "a", true},
		{"2", "c", true},
		{"3", "", false},
		{"b", "b", true},
		{"zz", "", false},
		{"99999999999999999999", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := r.ResolveDevice(tt.ref)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("ResolveDevice(%q) = %q, %v; want %q, %v", tt.ref, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestRegistrySummariesSorted(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"c", "a", "b"} {
		r.Declare(n)
	}
	var names []string
	for _, s := range r.Summaries() {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, []string{"a", "b", "c"}) {
		t.Errorf("Summaries order = %v", names)
	}
}

func TestRegistryConnectedWithoutCaps(t *testing.T) {
	r := registryWithConnectedMulti(map[string]hid.Controller{"a": &fakeController{}, "b": &fakeController{}})
	r.Declare("c") // untethered: excluded
	r.SetCaps("a", Capabilities{LEDCount: 1}, nil, nil)
	if got := r.ConnectedWithoutCaps(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("ConnectedWithoutCaps = %v, want [b]", got)
	}
}

func TestRegistryRecordCapsFailureMarksUnsupportedAfterThreshold(t *testing.T) {
	r := registryWithConnected("a", &fakeController{})
	for i := 0; i < maxCapsFailures; i++ {
		r.RecordCapsFailure("a")
	}
	if got := r.ConnectedWithoutCaps(); len(got) != 0 {
		t.Errorf("ConnectedWithoutCaps = %v, want none once marked unsupported", got)
	}
	if got := r.Summaries(); len(got) != 0 {
		t.Errorf("Summaries = %v, want unsupported device hidden", got)
	}
	if _, ok := r.ResolveDevice("a"); ok {
		t.Error("ResolveDevice(\"a\"): want false for unsupported device")
	}
}

func TestRegistryRecordCapsFailureBelowThresholdStaysVisible(t *testing.T) {
	r := registryWithConnected("a", &fakeController{})
	for i := 0; i < maxCapsFailures-1; i++ {
		r.RecordCapsFailure("a")
	}
	if got := r.ConnectedWithoutCaps(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("ConnectedWithoutCaps = %v, want [a] below threshold", got)
	}
	if got := r.Summaries(); len(got) != 1 {
		t.Errorf("Summaries = %v, want device still visible below threshold", got)
	}
}

func TestRegistryResolveDeviceOrdinalSkipsUnsupported(t *testing.T) {
	r := registryWithConnectedMulti(map[string]hid.Controller{"a": &fakeController{}, "b": &fakeController{}})
	for i := 0; i < maxCapsFailures; i++ {
		r.RecordCapsFailure("a")
	}
	if name, ok := r.ResolveDevice("0"); !ok || name != "b" {
		t.Errorf(`ResolveDevice("0") = %q, %v, want "b", true`, name, ok)
	}
}
