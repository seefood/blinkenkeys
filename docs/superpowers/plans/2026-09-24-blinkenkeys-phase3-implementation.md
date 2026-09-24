# blinkenkeys Phase 3 (effects, templates, frame-buffer delivery) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add YAML-defined multi-stage effects and templates driven by a server-side
tick engine, on top of a revised dispatcher where the color cache is a frame buffer
written unconditionally and delivered to hardware best-effort.

**Architecture:** `internal/keyaddr` parses and resolves key addresses.
`internal/dispatcher` gains capabilities and pending writes on registry slots,
pre-declared optional devices, ordinals, and colorless flush/redraw jobs that read
the cache at dispatch time. `internal/effects` holds pure primitives, a YAML loader
that compiles effects into timelines, and an `Engine` (the only write path the HTTP
layer uses). `internal/api` gets one write route whose body picks color / effect /
state. `cmd/blinkenkeysd` wires config, effects, engine, and a `-check-config` mode.

**Tech Stack:** Go 1.27, `github.com/goccy/go-yaml` v1.19.2 (already a dependency),
stdlib `net/http`, `log/slog`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-24-blinkenkeys-phase3-effects-templates-design.md`
(read it first; this plan argues from it). Phase 1+2 background:
`docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md`.

## Global Constraints

- Go 1.27+ must be first on `PATH` (e.g. `export PATH="$HOME/go-sdk/go1.27.1/bin:$PATH"`);
  the system Go 1.18 can't parse `go.mod` and the pre-commit Go hooks fail with it.
- Run every `go build`/`go test`/`make`/`git commit` under `nice -n 10` (cgo compiles
  hidapi's C sources; the commit hooks build too) so other processes aren't starved.
- No new dependencies. `go.mod`/`go.sum` must not change.
- Colors are QMK-native HSV, 0–255 per channel, always parsed via `internal/color`.
- One dispatcher goroutine owns every `hid.Controller`; nothing else calls one.
- Commit after every task. Never `git commit --no-verify`. If a pre-commit hook fails,
  STOP and ask the user — do not work around it.
- `#nosec G304` comment on any `os.ReadFile` of a variable path, matching `config/config.go`.
- Commit message style: imperative sentence, no conventional-commit prefix, ending with
  `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Verified ground truth

- `goccy/go-yaml` v1.19.2: `yaml.UnmarshalWithOptions(data, &v, yaml.DisallowUnknownField())`
  exists. Into `any`, a positive integer decodes as `uint64`, a negative one likely as
  `int64`, and a float as `float64`.
  **Unverified:** whether `DisallowUnknownField` applies to struct values nested inside a
  `map[string]T`. Task 7 has a test for exactly this. If it fails, stop and report
  instead of loosening the test.
- VialRGB reports row = col = `0xFF` for an LED that has no matrix key
  (`vialrgb.c`, `VIALRGB_GET_LED_INFO`, lines 79–83).

## File structure

| Path | Responsibility |
|---|---|
| `internal/color/color.go` (modify) | add `HSV` value type + `ParseHSV` |
| `internal/keyaddr/keyaddr.go` (create) | `{pos}` parsing, `Position`, `Resolve` |
| `internal/dispatcher/types.go` (modify) | new errors, `PendingWrite`, `LEDPosition` alias |
| `internal/dispatcher/registry.go` (modify) | caps/pending/optional on slots, `Declare`, ordinals, `Added` |
| `internal/dispatcher/dispatcher.go` (modify) | `Write`, `Canonical`, flush/redraw jobs, caps on slots |
| `internal/dispatcher/batch.go` (modify) | group flush jobs, `dedupeFlushes` |
| `internal/dispatcher/cache.go` (modify) | `Get` |
| `internal/dispatcher/redraw.go` (modify) | `Redraw` as one job, `EnsureCapabilities` |
| `internal/effects/errors.go` (create) | `ErrInvalid`, `ErrUnknownEffect`, `ErrUnknownState` |
| `internal/effects/primitives.go` (create) | primitive registry, settings binding, frames |
| `internal/effects/model.go` (create) | YAML shapes, strict decode |
| `internal/effects/compile.go` (create) | `Timeline`, `Library.Effect`, substitution, nesting |
| `internal/effects/load.go` (create) | `Load(dir)`, `Library.State`, `Action` |
| `internal/effects/engine.go` (create) | `Engine`, `Target`, tick loop |
| `config/config.go` (modify) | strict decode, `devices:`, `Dir`, `LoadDir` |
| `internal/api/handlers.go` (rewrite) | one write route, ordinals, status mapping |
| `internal/api/caps.go`, `caps_test.go` (delete) | superseded by caps on slots |
| `cmd/blinkenkeysd/main.go` (modify) | config/effects/engine wiring, `-check-config`, `syncDevices` |
| `examples/config/...` (create) | example effects + template, loaded by tests |
| `docs/superpowers/manual-checks/phase3-effects.md` (create) | gated hardware checks |

---

### Task 1: Leaf types — `color.HSV` and `internal/keyaddr`

**Files:**
- Modify: `internal/color/color.go`, `internal/color/color_test.go`
- Create: `internal/keyaddr/keyaddr.go`, `internal/keyaddr/keyaddr_test.go`
- Modify: `internal/dispatcher/types.go` (`LEDPosition` becomes an alias)

**Interfaces:**
- Consumes: `color.Parse(s) (h, sat, v uint8, err error)` (existing).
- Produces:
  - `color.HSV{H, S, V uint8}`, `color.ParseHSV(s string) (color.HSV, error)`
  - `keyaddr.Kind` with constants `RowCol`, `LED`, `Idx`, `Name`
  - `keyaddr.Address{Kind Kind; Row, Col uint8; N uint16; Name string}` (comparable; used as a map key), `(Address) String() string`
  - `keyaddr.ErrInvalid`, `keyaddr.Parse(s string) (Address, error)`
  - `keyaddr.Position{Index uint16 json:"index"; Row uint8 json:"row"; Col uint8 json:"col"}`
  - `keyaddr.Resolve(a Address, ledCount int, positions []Position) (uint16, bool)`
  - `dispatcher.LEDPosition = keyaddr.Position` (type alias; JSON unchanged)

- [ ] **Step 1: Write the failing tests**

Append to `internal/color/color_test.go`:

```go
func TestParseHSV(t *testing.T) {
	got, err := ParseHSV("#ff0000")
	if err != nil {
		t.Fatalf("ParseHSV: %v", err)
	}
	if got != (HSV{H: 0, S: 255, V: 255}) {
		t.Errorf("ParseHSV(#ff0000) = %+v", got)
	}
	if _, err := ParseHSV("not-a-color"); err == nil {
		t.Error("ParseHSV(not-a-color): want error")
	}
}
```

Create `internal/keyaddr/keyaddr_test.go`:

```go
package keyaddr

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Address
		wantErr bool
	}{
		{"2,3", Address{Kind: RowCol, Row: 2, Col: 3}, false},
		{"led:7", Address{Kind: LED, N: 7}, false},
		{"idx:0", Address{Kind: Idx, N: 0}, false},
		{"esc", Address{Kind: Name, Name: "esc"}, false},
		{"", Address{}, true},
		{"led:", Address{}, true},
		{"led:x", Address{}, true},
		{"idx:-1", Address{}, true},
		{"led:65536", Address{}, true},
		{"1,2,3", Address{}, true},
		{"1,x", Address{}, true},
		{"256,0", Address{}, true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if tt.wantErr {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Parse(%q) error = %v, want ErrInvalid", tt.in, err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	for _, in := range []string{"2,3", "led:7", "idx:4", "esc"} {
		a, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		if a.String() != in {
			t.Errorf("Parse(%q).String() = %q", in, a.String())
		}
	}
}

func TestResolve(t *testing.T) {
	// Firmware order is not reading order (like the 12e4's serpentine), and
	// LED 3 has no matrix key (underglow): VialRGB reports it as 0xFF,0xFF.
	positions := []Position{
		{Index: 0, Row: 0, Col: 1},
		{Index: 1, Row: 0, Col: 0},
		{Index: 2, Row: 1, Col: 0},
		{Index: 3, Row: 0xFF, Col: 0xFF},
	}
	tests := []struct {
		addr   Address
		want   uint16
		wantOK bool
	}{
		{Address{Kind: RowCol, Row: 0, Col: 0}, 1, true},
		{Address{Kind: RowCol, Row: 1, Col: 0}, 2, true},
		{Address{Kind: RowCol, Row: 5, Col: 5}, 0, false},
		{Address{Kind: RowCol, Row: 0xFF, Col: 0xFF}, 0, false},
		{Address{Kind: LED, N: 3}, 3, true},
		{Address{Kind: LED, N: 4}, 0, false},
		{Address{Kind: Idx, N: 0}, 1, true}, // (0,0)
		{Address{Kind: Idx, N: 1}, 0, true}, // (0,1)
		{Address{Kind: Idx, N: 2}, 2, true}, // (1,0)
		{Address{Kind: Idx, N: 3}, 0, false},
		{Address{Kind: Name, Name: "esc"}, 0, false},
	}
	for _, tt := range tests {
		got, ok := Resolve(tt.addr, 4, positions)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Resolve(%s) = %d, %v; want %d, %v", tt.addr, got, ok, tt.want, tt.wantOK)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/color/ ./internal/keyaddr/`
Expected: FAIL to compile (`undefined: ParseHSV`, `undefined: Parse`, ...).

- [ ] **Step 3: Implement**

Append to `internal/color/color.go`:

```go
// HSV is one QMK-native color: each channel 0-255.
type HSV struct {
	H, S, V uint8
}

// ParseHSV is Parse returning an HSV value.
func ParseHSV(s string) (HSV, error) {
	h, sat, v, err := Parse(s)
	return HSV{H: h, S: sat, V: v}, err
}
```

Create `internal/keyaddr/keyaddr.go`:

```go
// Package keyaddr parses the REST API's {pos} key-address forms and resolves
// them to a VialRGB LED index against a device's capabilities. It has no
// dependency on the dispatcher, so the API, the effects engine, and pending-
// write resolution share one implementation.
package keyaddr

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind is which {pos} form an Address was written in.
type Kind int

const (
	RowCol Kind = iota // "R,C": matrix row/column
	LED                // "led:N": raw VialRGB LED index (firmware order)
	Idx                // "idx:N": reading-order index over keyed LEDs
	Name               // anything else: a key name (reserved for Phase 5)
)

// Address is one parsed {pos}. Only the fields for its Kind are set.
type Address struct {
	Kind     Kind
	Row, Col uint8
	N        uint16
	Name     string
}

// ErrInvalid is returned for a malformed {pos}.
var ErrInvalid = errors.New("keyaddr: invalid key address")

// noKey is VialRGB's row/col for an LED with no matrix key (e.g. underglow).
const noKey = 0xFF

// Parse parses s, tried in order: "led:N", "idx:N", "R,C" (any string
// containing a comma must be a valid R,C), else a Name.
func Parse(s string) (Address, error) {
	switch {
	case s == "":
		return Address{}, fmt.Errorf("%w: empty", ErrInvalid)
	case strings.HasPrefix(s, "led:"):
		n, err := strconv.ParseUint(s[len("led:"):], 10, 16)
		if err != nil {
			return Address{}, fmt.Errorf("%w: %q", ErrInvalid, s)
		}
		return Address{Kind: LED, N: uint16(n)}, nil
	case strings.HasPrefix(s, "idx:"):
		n, err := strconv.ParseUint(s[len("idx:"):], 10, 16)
		if err != nil {
			return Address{}, fmt.Errorf("%w: %q", ErrInvalid, s)
		}
		return Address{Kind: Idx, N: uint16(n)}, nil
	case strings.Contains(s, ","):
		parts := strings.Split(s, ",")
		if len(parts) != 2 {
			return Address{}, fmt.Errorf("%w: %q: want row,col", ErrInvalid, s)
		}
		r, err1 := strconv.ParseUint(parts[0], 10, 8)
		c, err2 := strconv.ParseUint(parts[1], 10, 8)
		if err1 != nil || err2 != nil {
			return Address{}, fmt.Errorf("%w: %q: want row,col", ErrInvalid, s)
		}
		return Address{Kind: RowCol, Row: uint8(r), Col: uint8(c)}, nil
	default:
		return Address{Kind: Name, Name: s}, nil
	}
}

// String renders a in the same form Parse accepts.
func (a Address) String() string {
	switch a.Kind {
	case RowCol:
		return fmt.Sprintf("%d,%d", a.Row, a.Col)
	case LED:
		return fmt.Sprintf("led:%d", a.N)
	case Idx:
		return fmt.Sprintf("idx:%d", a.N)
	default:
		return a.Name
	}
}

// Position is one LED's matrix location, as reported by VialRGB's
// VIALRGB_GET_LED_INFO.
type Position struct {
	Index uint16 `json:"index"`
	Row   uint8  `json:"row"`
	Col   uint8  `json:"col"`
}

// Resolve maps a to an LED index given a device's LED count and positions.
// LEDs with no matrix key (row and col 0xFF) never match R,C and are
// excluded from idx: numbering. Name addresses never resolve.
func Resolve(a Address, ledCount int, positions []Position) (uint16, bool) {
	switch a.Kind {
	case LED:
		return a.N, int(a.N) < ledCount
	case RowCol:
		if a.Row == noKey && a.Col == noKey {
			return 0, false
		}
		for _, p := range positions {
			if p.Row == a.Row && p.Col == a.Col {
				return p.Index, true
			}
		}
		return 0, false
	case Idx:
		keyed := make([]Position, 0, len(positions))
		for _, p := range positions {
			if p.Row != noKey || p.Col != noKey {
				keyed = append(keyed, p)
			}
		}
		sort.Slice(keyed, func(i, j int) bool {
			if keyed[i].Row != keyed[j].Row {
				return keyed[i].Row < keyed[j].Row
			}
			return keyed[i].Col < keyed[j].Col
		})
		if int(a.N) >= len(keyed) {
			return 0, false
		}
		return keyed[a.N].Index, true
	default:
		return 0, false
	}
}
```

In `internal/dispatcher/types.go`, replace the `LEDPosition` struct (and its comment) with:

```go
// LEDPosition is one LED's matrix location — see keyaddr.Position.
type LEDPosition = keyaddr.Position
```

and add `"github.com/seefood/blinkenkeys/internal/keyaddr"` to its imports (turning
`import "errors"` into an import block).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test ./internal/color/ ./internal/keyaddr/ ./internal/dispatcher/ ./internal/api/`
Expected: PASS (dispatcher and api still compile against the alias).

- [ ] **Step 5: Commit**

```bash
git add internal/color internal/keyaddr internal/dispatcher/types.go
nice -n 10 git commit -m "Add keyaddr package and color.HSV

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Registry — capabilities, pending writes, declared devices, ordinals

**Files:**
- Modify: `internal/dispatcher/types.go`, `internal/dispatcher/registry.go`
- Test: `internal/dispatcher/registry_test.go`

**Interfaces:**
- Consumes: `keyaddr.Address`, `color.HSV` (Task 1).
- Produces (all on `*dispatcher.Registry`, all mutex-guarded):
  - `type PendingWrite struct { Addr keyaddr.Address; Color color.HSV }`
  - `Declare(id string)`: creates an Untethered, optional slot, or marks an existing one optional
  - `Caps(name string) (caps Capabilities, known, exists bool)`
  - `CapsOrPend(name string, w PendingWrite) (caps Capabilities, pended, exists bool)`
  - `SetCaps(name string, caps Capabilities, apply func([]PendingWrite))`: stores caps, calls `apply` with the pending writes (if any) **while still holding the lock**, then clears them. `apply` may be nil.
  - `ResolveDevice(ref string) (string, bool)`: name, or ordinal = rank among all slots sorted by name
  - `ConnectedWithoutCaps() []string` (sorted)
  - `Summaries()` now sorted by name
  - `ReconcileResult.Added []string`: names of slots created this cycle
  - Optional slots are never evicted by `Reconcile`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/dispatcher/registry_test.go` (add `"reflect"`,
`"github.com/seefood/blinkenkeys/internal/color"` and
`"github.com/seefood/blinkenkeys/internal/keyaddr"` to its imports):

```go
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

	var got []PendingWrite
	r.SetCaps("a", Capabilities{LEDCount: 2}, func(p []PendingWrite) { got = p })
	want := []PendingWrite{{b, color.HSV{H: 2}}, {a, color.HSV{H: 3}}} // a moved to the end
	if !reflect.DeepEqual(got, want) {
		t.Errorf("apply got %+v, want %+v", got, want)
	}

	caps, pended, _ := r.CapsOrPend("a", PendingWrite{Addr: a})
	if pended || caps.LEDCount != 2 {
		t.Errorf("CapsOrPend after SetCaps = %+v pended=%v", caps, pended)
	}
	called := false
	r.SetCaps("a", Capabilities{LEDCount: 2}, func([]PendingWrite) { called = true })
	if called {
		t.Error("apply called with no pending writes")
	}
}

func TestRegistryCapsRetainedWhenUntethered(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{3}}
	name := hid.BaseName(id)
	r := NewRegistry()
	r.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	r.SetCaps(name, Capabilities{LEDCount: 4}, nil)
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
	r.SetCaps("a", Capabilities{LEDCount: 1}, nil)
	if got := r.ConnectedWithoutCaps(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("ConnectedWithoutCaps = %v, want [b]", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/dispatcher/ -run TestRegistry`
Expected: FAIL to compile (`r.Declare undefined`, `undefined: PendingWrite`, ...).

- [ ] **Step 3: Implement**

Append to `internal/dispatcher/types.go` (add `color` to its imports):

```go
// PendingWrite is a write to a device whose capabilities aren't known yet
// (pre-declared, never connected), kept by literal address until they are.
type PendingWrite struct {
	Addr  keyaddr.Address
	Color color.HSV
}
```

In `internal/dispatcher/registry.go`:

1. Extend `slot`:

```go
type slot struct {
	base           string
	state          slotState
	ctrl           hid.Controller // set only while state == stateConnected
	disconnectedAt time.Time      // set only while state == stateUntethered
	caps           *Capabilities  // nil until first fetched; survives Untethered
	optional       bool           // config-declared optional: never evicted
	pending        []PendingWrite // writes awaiting caps, in arrival order
}
```

2. Add to `ReconcileResult`:

```go
	// Added is every name whose slot was created this cycle (a device never
	// seen before) — the caller logs a first-seen hint for each.
	Added []string
```

3. In `Reconcile`, where a brand-new slot is created, record it:

```go
		usedNames[name] = true
		claimed[name] = true
		r.slots[name] = &slot{base: base, state: stateConnected, ctrl: pd.Ctrl}
		added = append(added, name)
```

(declare `var added []string` next to `var reconnected []string`); exempt optional slots from eviction:

```go
		case stateUntethered:
			if !s.optional && now.Sub(s.disconnectedAt) > maxAge {
```

and return `ReconcileResult{Devices: devices, Reconnected: reconnected, Evicted: evicted, Added: added}`.

4. Replace `Summaries` with a sorted version and add the new methods (add `"sort"`,
`"strconv"` to imports):

```go
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

// SetCaps stores caps on name's slot and, if any writes were pending, passes
// them to apply in arrival order before clearing them. apply runs while the
// registry lock is still held: any Write that resolves against the new caps
// must first acquire this lock in CapsOrPend, so it always lands in the
// cache after the pending writes it supersedes. apply may be nil.
func (r *Registry) SetCaps(name string, caps Capabilities, apply func([]PendingWrite)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.slots[name]
	if !ok {
		return
	}
	s.caps = &caps
	if len(s.pending) > 0 && apply != nil {
		apply(s.pending)
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
```

(`strconv.ParseUint` rejects `""`, signs, and non-digits, so no separate all-digits
check is needed; bit size 31 keeps `int(n)` safe on every platform.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test -race ./internal/dispatcher/`
Expected: PASS (all existing registry tests too).

- [ ] **Step 5: Commit**

```bash
git add internal/dispatcher
nice -n 10 git commit -m "Store capabilities, pending writes and optional flag on registry slots

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Dispatcher — capabilities live on the slot

**Files:**
- Modify: `internal/dispatcher/types.go`, `internal/dispatcher/dispatcher.go`, `cmd/blinkenkeysd/main.go`
- Modify tests: `internal/dispatcher/registry_test.go` (fake controller), `dispatcher_test.go`, `redraw_test.go` (`New` signature, `lastSet()`)
- Create test: `internal/dispatcher/caps_test.go`

**Interfaces:**
- Consumes: the Registry methods from Task 2, `keyaddr.Resolve` (Task 1).
- Produces:
  - `dispatcher.ErrCapsUnknown` (API maps it to 503)
  - `dispatcher.New(registry *Registry, cache *Cache, queueDepth int, logger *slog.Logger) *Dispatcher`
  - `(*Dispatcher).GetCapabilities(ctx, device) (Capabilities, error)`: returns stored caps without touching the queue. Otherwise it queries a Connected device through the dispatcher goroutine and stores the result. Returns `ErrDeviceNotFound` for no slot, and `ErrCapsUnknown` if the device isn't connected and caps were never fetched.
  - On a successful fetch, pending writes are resolved into the cache under the registry lock. Unresolvable ones are dropped with a warn log naming the address. Task 4 adds flush scheduling to this.
  - Test helper: `fakeController` gains a mutex; its fields become `sets`/`numLEDsCalls` with accessors `lastSet()`, `setCount()`, `capsQueries()`.

- [ ] **Step 1: Make the fake controller concurrency-safe**

Replace the `fakeController` type and its methods at the top of
`internal/dispatcher/registry_test.go` with (add `"sync"` to imports):

```go
// fakeController is reused by every test file in this package. It's
// mutex-guarded because from Task 4 on, SetKeys runs on the dispatcher
// goroutine asynchronously to the test.
type fakeController struct {
	numLEDs   uint16
	positions map[uint16][2]uint8
	setErr    error

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
```

Then update existing uses mechanically:

```bash
sed -i 's/\.lastSet\b/.lastSet()/g; s/New(\(reg\|registry\), \(NewCache()\|cache\), \([0-9]\+\))/New(\1, \2, \3, discardLogger())/g' \
  internal/dispatcher/dispatcher_test.go internal/dispatcher/redraw_test.go
grep -n "New(\|lastSet" internal/dispatcher/dispatcher_test.go internal/dispatcher/redraw_test.go
```

Every `New(` call must now end in `, discardLogger())`, and every `lastSet` must be
`lastSet()`. Fix any the sed missed by hand.

- [ ] **Step 2: Write the failing tests**

Create `internal/dispatcher/caps_test.go`:

```go
package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func runDispatcher(t *testing.T, d *Dispatcher) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go d.Run(ctx)
}

func twoKeyPad() *fakeController {
	return &fakeController{numLEDs: 2, positions: map[uint16][2]uint8{0: {1, 1}, 1: {1, 2}}}
}

func TestGetCapabilitiesStoredAfterFirstFetch(t *testing.T) {
	fc := twoKeyPad()
	d := New(registryWithConnected("a", fc), NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	for i := 0; i < 2; i++ {
		caps, err := d.GetCapabilities(context.Background(), "a")
		if err != nil || caps.LEDCount != 2 || len(caps.Positions) != 2 {
			t.Fatalf("GetCapabilities #%d = %+v, %v", i, caps, err)
		}
	}
	if n := fc.capsQueries(); n != 1 {
		t.Errorf("device queried %d times, want 1", n)
	}
}

func TestGetCapabilitiesRetainedWhenUntethered(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: twoKeyPad()}}, time.Now(), UntetheredMaxAge)
	if _, err := d.GetCapabilities(context.Background(), name); err != nil {
		t.Fatalf("GetCapabilities while connected: %v", err)
	}
	reg.Reconcile(nil, time.Now(), UntetheredMaxAge)
	caps, err := d.GetCapabilities(context.Background(), name)
	if err != nil || caps.LEDCount != 2 {
		t.Errorf("GetCapabilities while untethered = %+v, %v", caps, err)
	}
}

func TestGetCapabilitiesUnknownForDeclared(t *testing.T) {
	reg := NewRegistry()
	reg.Declare("a")
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	if _, err := d.GetCapabilities(context.Background(), "a"); !errors.Is(err, ErrCapsUnknown) {
		t.Errorf("err = %v, want ErrCapsUnknown", err)
	}
}

func TestGetCapabilitiesUnknownDevice(t *testing.T) {
	d := New(NewRegistry(), NewCache(), 8, discardLogger()) // Run not started: must not need the queue
	if _, err := d.GetCapabilities(context.Background(), "missing"); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("err = %v, want ErrDeviceNotFound", err)
	}
}

func TestGetCapabilitiesAppliesPending(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{2}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	reg.Declare(name)
	red, green := color.HSV{H: 0, S: 255, V: 255}, color.HSV{H: 85, S: 255, V: 255}
	reg.CapsOrPend(name, PendingWrite{Addr: keyaddr.Address{Kind: keyaddr.RowCol, Row: 1, Col: 1}, Color: red})
	reg.CapsOrPend(name, PendingWrite{Addr: keyaddr.Address{Kind: keyaddr.RowCol, Row: 5, Col: 9}, Color: green})

	var logs bytes.Buffer
	cache := NewCache()
	d := New(reg, cache, 8, slog.New(slog.NewTextHandler(&logs, nil)))
	runDispatcher(t, d)
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: twoKeyPad()}}, time.Now(), UntetheredMaxAge)

	if _, err := d.GetCapabilities(context.Background(), name); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	snap := cache.Snapshot(name)
	if len(snap) != 1 || snap[0] != (hid.KeyColor{Index: 0, H: 0, S: 255, V: 255}) {
		t.Errorf("cache = %+v, want only LED 0 red", snap)
	}
	// Safe to read: the warn was logged on the dispatcher goroutine before
	// it replied to GetCapabilities.
	if !strings.Contains(logs.String(), "5,9") {
		t.Errorf("log %q does not name the dropped address 5,9", logs.String())
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/dispatcher/`
Expected: FAIL to compile (`too many arguments in call to New`, `undefined: ErrCapsUnknown`).

- [ ] **Step 4: Implement**

Append to `internal/dispatcher/types.go`:

```go
// ErrCapsUnknown is returned by GetCapabilities for a device that isn't
// connected and whose capabilities were never fetched (a pre-declared,
// never-seen device) — internal/api maps this to a 503.
var ErrCapsUnknown = errors.New("dispatcher: device capabilities not yet known")
```

In `internal/dispatcher/dispatcher.go`:

- add `logger *slog.Logger` to `Dispatcher`, and change `New`:

```go
// New creates a Dispatcher. queueDepth bounds in-flight requests (spec:
// e.g. 64) — a full queue fails fast with ErrQueueFull rather than growing
// goroutines/memory without bound.
func New(registry *Registry, cache *Cache, queueDepth int, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{registry: registry, cache: cache, queue: make(chan job, queueDepth), logger: logger}
}
```

- replace `GetCapabilities`:

```go
// GetCapabilities returns device's LED count and matrix positions. Stored
// capabilities (kept on the registry slot, surviving Untethered) are
// returned without touching the queue; otherwise a Connected device is
// queried once through the dispatcher goroutine and the result stored.
func (d *Dispatcher) GetCapabilities(ctx context.Context, device string) (Capabilities, error) {
	caps, known, exists := d.registry.Caps(device)
	if !exists {
		return Capabilities{}, ErrDeviceNotFound
	}
	if known {
		return caps, nil
	}
	res, err := d.submit(ctx, job{kind: opGetCapabilities, device: device, reply: make(chan jobResult, 1)})
	if err != nil {
		return Capabilities{}, err
	}
	return res.caps, res.err
}
```

- in `dispatchGroup`, call the new fetch:

```go
	case opGetCapabilities:
		caps, err := d.fetchCapabilities(group[0].device)
		group[0].reply <- jobResult{caps: caps, err: err}
```

- replace `getCapabilities` with:

```go
// fetchCapabilities runs on the dispatcher goroutine. It re-checks for
// stored caps (a concurrent request may have fetched them since this job
// was queued), else queries the device and stores the result, resolving
// any pending writes into the cache.
func (d *Dispatcher) fetchCapabilities(device string) (Capabilities, error) {
	if caps, known, _ := d.registry.Caps(device); known {
		return caps, nil
	}
	ctrl, ok := d.registry.Get(device)
	if !ok {
		return Capabilities{}, ErrCapsUnknown
	}
	caps, err := queryCapabilities(ctrl)
	if err != nil {
		return Capabilities{}, err
	}
	d.registry.SetCaps(device, caps, func(pending []PendingWrite) {
		d.applyPending(device, caps, pending)
	})
	return caps, nil
}

func queryCapabilities(ctrl hid.Controller) (Capabilities, error) {
	n, err := ctrl.GetNumberLEDs()
	if err != nil {
		return Capabilities{}, err
	}
	caps := Capabilities{LEDCount: int(n)}
	for i := uint16(0); i < n; i++ {
		row, col, err := ctrl.GetLEDInfo(i)
		if err != nil {
			return Capabilities{}, err
		}
		caps.Positions = append(caps.Positions, LEDPosition{Index: i, Row: row, Col: col})
	}
	return caps, nil
}

// applyPending resolves pending writes (arrival order, so the latest write
// per key wins) into the cache. Called by Registry.SetCaps under its lock.
func (d *Dispatcher) applyPending(device string, caps Capabilities, pending []PendingWrite) {
	for _, w := range pending {
		idx, ok := keyaddr.Resolve(w.Addr, caps.LEDCount, caps.Positions)
		if !ok {
			d.logger.Warn("dropping pending write: key not on device", "device", device, "addr", w.Addr.String())
			continue
		}
		d.cache.Update(device, []hid.KeyColor{{Index: idx, H: w.Color.H, S: w.Color.S, V: w.Color.V}})
	}
}
```

(add `"log/slog"` and `keyaddr` to imports).

In `cmd/blinkenkeysd/main.go`, change the constructor call to
`dispatcher.New(registry, cache, dispatcherQueueDepth, logger)`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `nice -n 10 go test -race ./internal/dispatcher/ ./cmd/... ./internal/api/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/dispatcher cmd/blinkenkeysd/main.go
nice -n 10 git commit -m "Keep device capabilities on the registry slot

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Frame-buffer write path, flush/redraw jobs, reconnect sync

**Files:**
- Modify: `internal/dispatcher/types.go`, `dispatcher.go`, `batch.go`, `cache.go`, `redraw.go`
- Modify: `cmd/blinkenkeysd/main.go`, `cmd/blinkenkeysd/main_test.go`
- Rewrite tests: `internal/dispatcher/dispatcher_test.go`, `redraw_test.go`; modify `batch_test.go`, `cache_test.go`

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces:
  - `dispatcher.ErrKeyNotFound`, `dispatcher.ErrNamedKeyUnsupported`
  - `(*Cache).Get(device string, index uint16) (hid.KeyColor, bool)`
  - `(*Dispatcher).Write(device string, addr keyaddr.Address, c color.HSV) error`. It never blocks and never waits on hardware. Errors:
    - `ErrDeviceNotFound` for no slot (checked before anything else);
    - `ErrNamedKeyUnsupported` for Name addresses;
    - `ErrKeyNotFound` when the key isn't on the device's (known) matrix.
    - Returns nil after updating the cache, or after pending the write.
  - `(*Dispatcher).Canonical(device string, addr keyaddr.Address) (keyaddr.Address, error)`: `led:N` when caps are known, `addr` unchanged when not; same errors as `Write`.
  - `(*Dispatcher).ResolveDevice(ref string) (string, bool)` (delegates to Registry)
  - `(*Dispatcher).Redraw(device string)`: one queue job, expanded at dispatch time
  - `(*Dispatcher).EnsureCapabilities(ctx, device string) error`
  - `(*Dispatcher).RunPeriodicRedraw(ctx, interval time.Duration)` (logger param dropped)
  - **Temporary:** `(*Dispatcher).SetKey(ctx, device, index, h, s, v) error` adapter so `internal/api` compiles unchanged. Task 11 deletes it.
  - `RedrawReconnected` is deleted.
  - `cmd/blinkenkeysd`: `syncDevices(ctx, registry, disp, reconnected []string, logger)` and `logAdded(logger, names []string)`

- [ ] **Step 1: Write the failing tests**

Replace `internal/dispatcher/dispatcher_test.go` entirely:

```go
package dispatcher

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func led(n uint16) keyaddr.Address { return keyaddr.Address{Kind: keyaddr.LED, N: n} }

func rc(r, c uint8) keyaddr.Address { return keyaddr.Address{Kind: keyaddr.RowCol, Row: r, Col: c} }

// withCaps seeds name's slot with an n-LED, single-row capability set.
func withCaps(r *Registry, name string, n int) *Registry {
	caps := Capabilities{LEDCount: n}
	for i := 0; i < n; i++ {
		caps.Positions = append(caps.Positions, LEDPosition{Index: uint16(i), Row: 0, Col: uint8(i)})
	}
	r.SetCaps(name, caps, nil)
	return r
}

// waitFor polls cond every millisecond, failing the test after one second.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

// processQueued dispatches everything currently queued, synchronously, the
// way one Run iteration would.
func processQueued(d *Dispatcher) {
	d.process(d.drainAfter(<-d.queue))
}

func TestWriteUnknownDevice(t *testing.T) {
	d := New(NewRegistry(), NewCache(), 8, discardLogger())
	if err := d.Write("missing", led(0), color.HSV{}); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("err = %v, want ErrDeviceNotFound", err)
	}
	name := keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}
	if err := d.Write("missing", name, color.HSV{}); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("named key on missing device: err = %v, want ErrDeviceNotFound (device first)", err)
	}
}

func TestWriteNamedKeyUnsupported(t *testing.T) {
	d := New(withCaps(registryWithConnected("a", &fakeController{}), "a", 4), NewCache(), 8, discardLogger())
	if err := d.Write("a", keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}, color.HSV{}); !errors.Is(err, ErrNamedKeyUnsupported) {
		t.Errorf("err = %v, want ErrNamedKeyUnsupported", err)
	}
}

func TestWriteKeyOffMatrix(t *testing.T) {
	d := New(withCaps(registryWithConnected("a", &fakeController{}), "a", 4), NewCache(), 8, discardLogger())
	for _, addr := range []keyaddr.Address{led(4), rc(1, 0)} {
		if err := d.Write("a", addr, color.HSV{}); !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("Write(%s) err = %v, want ErrKeyNotFound", addr, err)
		}
	}
}

func TestWriteUpdatesCacheAndDelivers(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), cache, 8, discardLogger())
	runDispatcher(t, d)

	if err := d.Write("a", rc(0, 3), color.HSV{H: 1, S: 2, V: 3}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if k, ok := cache.Get("a", 3); !ok || k != (hid.KeyColor{Index: 3, H: 1, S: 2, V: 3}) {
		t.Errorf("cache.Get = %+v, %v (must be written synchronously)", k, ok)
	}
	waitFor(t, func() bool { return fc.lastSet() != nil })
	if got := fc.lastSet(); len(got) != 1 || got[0].Index != 3 || got[0].H != 1 {
		t.Errorf("SetKeys = %+v", got)
	}
}

func TestWriteUntetheredUpdatesCacheOnly(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	name := hid.BaseName(id)
	fc := &fakeController{}
	reg := NewRegistry()
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc}}, time.Now(), UntetheredMaxAge)
	withCaps(reg, name, 4)
	reg.Reconcile(nil, time.Now(), UntetheredMaxAge) // unplugged
	cache := NewCache()
	d := New(reg, cache, 8, discardLogger())

	if err := d.Write(name, led(1), color.HSV{H: 9}); err != nil {
		t.Fatalf("Write to untethered: %v", err)
	}
	processQueued(d)
	if _, ok := cache.Get(name, 1); !ok {
		t.Error("cache not updated for untethered device")
	}
	if fc.setCount() != 0 {
		t.Error("controller touched for untethered device")
	}
}

func TestWriteQueueFullStillSucceeds(t *testing.T) {
	cache := NewCache()
	d := New(withCaps(registryWithConnected("a", &fakeController{}), "a", 4), cache, 0, discardLogger())
	if err := d.Write("a", led(0), color.HSV{H: 5}); err != nil {
		t.Fatalf("Write with full queue: %v", err)
	}
	if k, _ := cache.Get("a", 0); k.H != 5 {
		t.Errorf("cache = %+v, want H 5", k)
	}
}

func TestFlushSendsLatestAndCollapsesDuplicates(t *testing.T) {
	fc := &fakeController{}
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), NewCache(), 8, discardLogger())

	_ = d.Write("a", led(1), color.HSV{H: 1})
	_ = d.Write("a", led(1), color.HSV{H: 2})
	processQueued(d)

	if fc.setCount() != 1 {
		t.Fatalf("SetKeys called %d times, want 1 (duplicates collapse)", fc.setCount())
	}
	if got := fc.lastSet(); len(got) != 1 || got[0].H != 2 {
		t.Errorf("SetKeys = %+v, want one key with H 2 (read at dispatch time)", got)
	}
}

func TestRedrawDoesNotOverwriteNewerWrite(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), cache, 8, discardLogger())

	_ = d.Write("a", led(0), color.HSV{H: 1})
	d.Redraw("a")
	_ = d.Write("a", led(0), color.HSV{H: 2})
	processQueued(d)

	if got := fc.lastSet(); len(got) != 1 || got[0].H != 2 {
		t.Errorf("SetKeys = %+v, want H 2", got)
	}
	if k, _ := cache.Get("a", 0); k.H != 2 {
		t.Errorf("cache H = %d, want 2 (Redraw must not write the cache)", k.H)
	}
}

func TestHIDWriteErrorIsLogged(t *testing.T) {
	var logs bytes.Buffer
	fc := &fakeController{setErr: errors.New("boom")}
	d := New(withCaps(registryWithConnected("a", fc), "a", 4), NewCache(), 8, slog.New(slog.NewTextHandler(&logs, nil)))

	if err := d.Write("a", led(0), color.HSV{}); err != nil {
		t.Fatalf("Write must not surface HID errors: %v", err)
	}
	processQueued(d)
	if !strings.Contains(logs.String(), "boom") {
		t.Errorf("log %q missing HID error", logs.String())
	}
}

func TestWritePendingResolvesOnEnsureCapabilities(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{2}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	reg.Declare(name)
	cache := NewCache()
	var logs bytes.Buffer
	d := New(reg, cache, 8, slog.New(slog.NewTextHandler(&logs, nil)))
	runDispatcher(t, d)

	red, blue := color.HSV{H: 0, S: 255, V: 255}, color.HSV{H: 170, S: 255, V: 255}
	for _, w := range []struct {
		addr keyaddr.Address
		c    color.HSV
	}{{rc(1, 1), red}, {rc(1, 1), blue}, {rc(5, 9), red}} {
		if err := d.Write(name, w.addr, w.c); err != nil {
			t.Fatalf("Write(%s) to declared device: %v", w.addr, err)
		}
	}
	if cache.Snapshot(name) != nil {
		t.Fatal("pending writes must not reach the cache before caps are known")
	}

	fc := twoKeyPad()
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc}}, time.Now(), UntetheredMaxAge)
	if err := d.EnsureCapabilities(context.Background(), name); err != nil {
		t.Fatalf("EnsureCapabilities: %v", err)
	}
	if k, _ := cache.Get(name, 0); k.H != blue.H {
		t.Errorf("cache LED 0 = %+v, want blue (latest write wins)", k)
	}
	if !strings.Contains(logs.String(), "5,9") {
		t.Errorf("log %q does not name dropped 5,9", logs.String())
	}
	waitFor(t, func() bool { return fc.lastSet() != nil })
	if got := fc.lastSet(); got[0].H != blue.H {
		t.Errorf("delivered %+v, want blue", got)
	}
}

func TestCanonical(t *testing.T) {
	reg := withCaps(registryWithConnected("a", &fakeController{}), "a", 4)
	reg.Declare("b")
	d := New(reg, NewCache(), 8, discardLogger())

	if got, err := d.Canonical("a", rc(0, 2)); err != nil || got != led(2) {
		t.Errorf("Canonical(a, 0,2) = %v, %v; want led:2", got, err)
	}
	if got, err := d.Canonical("b", rc(0, 2)); err != nil || got != rc(0, 2) {
		t.Errorf("Canonical(b, 0,2) = %v, %v; want unchanged (caps unknown)", got, err)
	}
	if _, err := d.Canonical("missing", rc(0, 0)); !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("missing: err = %v", err)
	}
	if _, err := d.Canonical("a", led(9)); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("off matrix: err = %v", err)
	}
	if _, err := d.Canonical("a", keyaddr.Address{Kind: keyaddr.Name, Name: "x"}); !errors.Is(err, ErrNamedKeyUnsupported) {
		t.Errorf("name: err = %v", err)
	}
}

func TestDispatcherListDevices(t *testing.T) {
	d := New(registryWithConnected("a", &fakeController{}), NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	devices, err := d.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "a" || !devices[0].Connected {
		t.Errorf("ListDevices = %+v", devices)
	}
}
```

Replace `internal/dispatcher/redraw_test.go` entirely:

```go
package dispatcher

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/hid"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRedrawEmptyCacheIsNoop(t *testing.T) {
	fc := &fakeController{}
	d := New(registryWithConnected("a", fc), NewCache(), 8, discardLogger())
	d.Redraw("a")
	processQueued(d)
	if fc.setCount() != 0 {
		t.Errorf("SetKeys called on empty cache: %+v", fc.lastSet())
	}
}

func TestRedrawReplaysCache(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0, H: 1, S: 2, V: 3}})
	d := New(registryWithConnected("a", fc), cache, 8, discardLogger())
	d.Redraw("a")
	processQueued(d)
	if got := fc.lastSet(); len(got) != 1 || got[0].H != 1 {
		t.Errorf("SetKeys = %+v", got)
	}
}

func TestRedrawIsOneJobRegardlessOfLEDCount(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	for i := uint16(0); i < 12; i++ {
		cache.Update("a", []hid.KeyColor{{Index: i}})
	}
	d := New(registryWithConnected("a", fc), cache, 1, discardLogger()) // room for exactly one job
	d.Redraw("a")
	processQueued(d)
	if fc.setCount() != 2 || len(fc.sets[0]) != 9 || len(fc.sets[1]) != 3 {
		t.Errorf("SetKeys calls = %d, want 2 reports of 9+3", fc.setCount())
	}
}

func TestRunPeriodicRedrawFiresOnTick(t *testing.T) {
	fc := &fakeController{}
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0}})
	d := New(registryWithConnected("a", fc), cache, 8, discardLogger())
	runDispatcher(t, d)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.RunPeriodicRedraw(ctx, 10*time.Millisecond)
	waitFor(t, func() bool { return fc.lastSet() != nil })
}

func TestReconnectRewireRegainsCache(t *testing.T) {
	id := hid.Identity{HasUID: true, UID: [8]byte{7}}
	name := hid.BaseName(id)
	reg := NewRegistry()
	d := New(reg, NewCache(), 8, discardLogger())
	runDispatcher(t, d)

	fc1 := &fakeController{numLEDs: 1, positions: map[uint16][2]uint8{0: {0, 0}}}
	reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc1}}, time.Now(), UntetheredMaxAge)
	if err := d.EnsureCapabilities(context.Background(), name); err != nil {
		t.Fatalf("EnsureCapabilities: %v", err)
	}
	if err := d.Write(name, led(0), color.HSV{H: 1}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	reg.Reconcile(nil, time.Now(), UntetheredMaxAge) // unplug
	fc2 := &fakeController{}                         // replug: fresh controller, as a real replug produces
	res := reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc2}}, time.Now(), UntetheredMaxAge)
	if len(res.Reconnected) != 1 || res.Reconnected[0] != name {
		t.Fatalf("Reconnected = %v, want [%s]", res.Reconnected, name)
	}
	if err := d.EnsureCapabilities(context.Background(), name); err != nil {
		t.Fatalf("EnsureCapabilities after rewire: %v", err)
	}
	waitFor(t, func() bool { return fc2.lastSet() != nil })
	if got := fc2.lastSet(); got[0].H != 1 {
		t.Errorf("replayed %+v, want H 1", got)
	}
	if fc2.capsQueries() != 0 {
		t.Error("caps re-queried after rewire; they should be retained on the slot")
	}
}
```

(`TestRedrawIsOneJobRegardlessOfLEDCount` reads `fc.sets` directly. That's safe
because `processQueued` ran synchronously on the test goroutine.)

In `internal/dispatcher/batch_test.go`, replace the `setKeyJob` helper and add a dedupe
test. Drop the now-unused `hid` import:

```go
func flushJob(device string, index uint16) job {
	return job{kind: opFlush, device: device, index: index}
}
```

Rename every `setKeyJob(` to `flushJob(`, rename
`TestGroupForSendNonSetKeyAlwaysSingleton` to `TestGroupForSendNonFlushAlwaysSingleton`
(message: "non-flush ops never merge"), and append:

```go
func TestDedupeFlushesKeepsFirstOccurrence(t *testing.T) {
	batch := []job{flushJob("a", 0), flushJob("a", 1), flushJob("a", 0), flushJob("b", 0)}
	got := dedupeFlushes(batch)
	if len(got) != 3 || got[0].index != 0 || got[1].index != 1 || got[2].device != "b" {
		t.Errorf("dedupeFlushes = %+v", got)
	}
}
```

Append to `internal/dispatcher/cache_test.go`:

```go
func TestCacheGet(t *testing.T) {
	c := NewCache()
	if _, ok := c.Get("a", 0); ok {
		t.Error("Get on empty cache: ok = true")
	}
	c.Update("a", []hid.KeyColor{{Index: 2, H: 7}})
	if k, ok := c.Get("a", 2); !ok || k.H != 7 {
		t.Errorf("Get = %+v, %v", k, ok)
	}
}
```

Append to `cmd/blinkenkeysd/main_test.go` (add `"context"`, `"time"`,
`dispatcher`, and `hid` imports):

```go
type stubController struct{}

func (stubController) SetKeys([]hid.KeyColor) error               { return nil }
func (stubController) GetNumberLEDs() (uint16, error)             { return 1, nil }
func (stubController) GetLEDInfo(uint16) (uint8, uint8, error)    { return 0, 0, nil }
func (stubController) Close() error                               { return nil }

func TestSyncDevicesFetchesMissingCaps(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	registry := dispatcher.NewRegistry()
	disp := dispatcher.New(registry, dispatcher.NewCache(), 8, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

	id := hid.Identity{HasUID: true, UID: [8]byte{1}}
	registry.Reconcile([]dispatcher.PresentDevice{{Identity: id, Ctrl: stubController{}}}, time.Now(), dispatcher.UntetheredMaxAge)
	syncDevices(ctx, registry, disp, nil, logger)

	if _, known, _ := registry.Caps(hid.BaseName(id)); !known {
		t.Error("caps not fetched for newly connected device")
	}
	if got := registry.ConnectedWithoutCaps(); len(got) != 0 {
		t.Errorf("ConnectedWithoutCaps = %v, want none", got)
	}
}

func TestLogAddedHintsConfig(t *testing.T) {
	var buf bytes.Buffer
	logAdded(slog.New(slog.NewTextHandler(&buf, nil)), []string{"uid-0102"})
	if !strings.Contains(buf.String(), "uid-0102") || !strings.Contains(buf.String(), "optional: true") {
		t.Errorf("log = %q", buf.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/dispatcher/ ./cmd/...`
Expected: FAIL to compile (`d.Write undefined`, `undefined: opFlush`, `undefined: syncDevices`, ...).

- [ ] **Step 3: Implement**

`internal/dispatcher/types.go`: append:

```go
// ErrKeyNotFound is returned for a key address not on the device's matrix
// (capabilities known) — internal/api maps this to a 404.
var ErrKeyNotFound = errors.New("dispatcher: key not on device")

// ErrNamedKeyUnsupported is returned for a key-name address, reserved for
// Phase 5's named-key allocation — internal/api maps this to a 501.
var ErrNamedKeyUnsupported = errors.New("dispatcher: named keys are not implemented yet")
```

`internal/dispatcher/cache.go`: append:

```go
// Get returns the cached color for one LED of device.
func (c *Cache) Get(device string, index uint16) (hid.KeyColor, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.byDevice[device][index]
	return k, ok
}
```

and update the `Cache` doc comment's first sentence to: "Cache is the frame buffer:
the desired color of each key of each device, written unconditionally by
`Dispatcher.Write` and delivered to hardware best-effort."

`internal/dispatcher/dispatcher.go`: the job model changes. Replace `opSetKey` and the
`job` struct:

```go
const (
	opFlush opKind = iota // deliver one cached LED; no reply
	opRedraw              // expanded at dispatch time into flushes of the whole cache; no reply
	opListDevices
	opGetCapabilities
)

type job struct {
	kind   opKind
	device string
	index  uint16         // valid when kind == opFlush
	reply  chan jobResult // nil for opFlush and opRedraw
}
```

Delete `SetKey`, `dispatchSetKeys`, and `failAll`. Replace `Run` and `dispatchGroup`, and
add the write path:

```go
// Run is the single dispatcher goroutine: it owns every hid.Controller in
// registry exclusively. On each cycle it takes one job, non-blockingly
// drains any others already queued, then processes them as a batch. It
// runs until ctx is canceled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case first := <-d.queue:
			d.process(d.drainAfter(first))
		case <-ctx.Done():
			return
		}
	}
}

func (d *Dispatcher) drainAfter(first job) []job {
	batch := []job{first}
	for {
		select {
		case j := <-d.queue:
			batch = append(batch, j)
		default:
			return batch
		}
	}
}

func (d *Dispatcher) process(batch []job) {
	for _, group := range groupForSend(dedupeFlushes(d.expandRedraws(batch))) {
		d.dispatchGroup(group)
	}
}

// expandRedraws replaces each redraw job with flushes of every index
// cached for its device at this moment.
func (d *Dispatcher) expandRedraws(batch []job) []job {
	out := make([]job, 0, len(batch))
	for _, j := range batch {
		if j.kind != opRedraw {
			out = append(out, j)
			continue
		}
		for _, k := range d.cache.Snapshot(j.device) {
			out = append(out, job{kind: opFlush, device: j.device, index: k.Index})
		}
	}
	return out
}

func (d *Dispatcher) dispatchGroup(group []job) {
	switch group[0].kind {
	case opFlush:
		d.dispatchFlush(group)
	case opListDevices:
		group[0].reply <- jobResult{list: d.registry.Summaries()}
	case opGetCapabilities:
		caps, err := d.fetchCapabilities(group[0].device)
		group[0].reply <- jobResult{caps: caps, err: err}
	}
}

// dispatchFlush sends one contiguous run of LEDs, reading each one's color
// from the cache now — never a color captured at enqueue time, so a flush
// can't deliver a stale frame. A non-Connected device is skipped silently;
// a HID error is logged and otherwise ignored (the next poll marks the
// device Untethered, and reconnect redraw catches it up).
func (d *Dispatcher) dispatchFlush(group []job) {
	device := group[0].device
	ctrl, ok := d.registry.Get(device)
	if !ok {
		return
	}
	keys := make([]hid.KeyColor, 0, len(group))
	for _, j := range group {
		k, ok := d.cache.Get(device, j.index)
		if !ok {
			return // device's cache was forgotten (evicted) after this flush was queued
		}
		keys = append(keys, k)
	}
	if err := ctrl.SetKeys(keys); err != nil {
		d.logger.Warn("hid write failed", "device", device, "err", err)
	}
}

// tryEnqueue queues j without blocking; a full queue drops it (the cache
// already holds the truth, and the next periodic redraw delivers it).
func (d *Dispatcher) tryEnqueue(j job) {
	select {
	case d.queue <- j:
	default:
		d.logger.Debug("dispatcher queue full, dropping job", "device", j.device, "kind", int(j.kind))
	}
}

// Write sets addr on device to c in the frame buffer and schedules delivery.
// It never blocks and never waits on hardware: the cache is updated
// synchronously (or, for a device whose capabilities aren't known yet, the
// write is kept pending on its registry slot), and a colorless flush is
// queued best-effort.
func (d *Dispatcher) Write(device string, addr keyaddr.Address, c color.HSV) error {
	if addr.Kind == keyaddr.Name {
		if _, _, exists := d.registry.Caps(device); !exists {
			return ErrDeviceNotFound
		}
		return ErrNamedKeyUnsupported
	}
	caps, pended, exists := d.registry.CapsOrPend(device, PendingWrite{Addr: addr, Color: c})
	if !exists {
		return ErrDeviceNotFound
	}
	if pended {
		return nil
	}
	idx, ok := keyaddr.Resolve(addr, caps.LEDCount, caps.Positions)
	if !ok {
		return fmt.Errorf("%w: %s", ErrKeyNotFound, addr)
	}
	d.cache.Update(device, []hid.KeyColor{{Index: idx, H: c.H, S: c.S, V: c.V}})
	d.tryEnqueue(job{kind: opFlush, device: device, index: idx})
	return nil
}

// Canonical resolves addr to led:N when device's capabilities are known, so
// every form naming one key maps to one effects-engine target. For a device
// whose capabilities aren't known yet, addr is returned unchanged.
func (d *Dispatcher) Canonical(device string, addr keyaddr.Address) (keyaddr.Address, error) {
	caps, known, exists := d.registry.Caps(device)
	switch {
	case !exists:
		return keyaddr.Address{}, ErrDeviceNotFound
	case addr.Kind == keyaddr.Name:
		return keyaddr.Address{}, ErrNamedKeyUnsupported
	case !known:
		return addr, nil
	}
	idx, ok := keyaddr.Resolve(addr, caps.LEDCount, caps.Positions)
	if !ok {
		return keyaddr.Address{}, fmt.Errorf("%w: %s", ErrKeyNotFound, addr)
	}
	return keyaddr.Address{Kind: keyaddr.LED, N: idx}, nil
}

// ResolveDevice maps a {name} path segment (name or ordinal) to a device name.
func (d *Dispatcher) ResolveDevice(ref string) (string, bool) {
	return d.registry.ResolveDevice(ref)
}

// SetKey is a temporary adapter that keeps internal/api's Phase 1+2 handler
// compiling until it switches to the effects engine; deleted then.
func (d *Dispatcher) SetKey(_ context.Context, device string, index uint16, h, s, v uint8) error {
	return d.Write(device, keyaddr.Address{Kind: keyaddr.LED, N: index}, color.HSV{H: h, S: s, V: v})
}
```

(add `"fmt"` and `color` to imports). In `applyPending`, after the `d.cache.Update(...)`
line, add `d.tryEnqueue(job{kind: opFlush, device: device, index: idx})`.

`internal/dispatcher/batch.go`: in `groupForSend`, change `j.kind != opSetKey` to
`j.kind != opFlush`, and `j.key.Index` to `j.index` (both places). Update the doc
comment ("non-flush jobs are always singleton groups; flush jobs are grouped ..."). Then
append:

```go
// dedupeFlushes drops repeat flushes of the same (device, LED) within one
// drained batch, keeping the first occurrence's position. Keeping any one
// is correct because flushes read the cache at dispatch time.
func dedupeFlushes(batch []job) []job {
	type key struct {
		device string
		index  uint16
	}
	seen := make(map[key]bool)
	out := make([]job, 0, len(batch))
	for _, j := range batch {
		if j.kind == opFlush {
			k := key{j.device, j.index}
			if seen[k] {
				continue
			}
			seen[k] = true
		}
		out = append(out, j)
	}
	return out
}
```

`internal/dispatcher/redraw.go`: replace everything below `RedrawInterval` with:

```go
// Redraw schedules delivery of device's whole cached frame as a single
// queue job, expanded at dispatch time (so it reads the newest colors and
// can't overflow the queue on large boards). It never writes to the cache.
func (d *Dispatcher) Redraw(device string) {
	d.tryEnqueue(job{kind: opRedraw, device: device})
}

// EnsureCapabilities fetches and stores device's capabilities if the slot
// lacks them (resolving pending writes), then schedules a full redraw.
// Called by the poll loop for every connected device without capabilities
// and every reconnected one.
func (d *Dispatcher) EnsureCapabilities(ctx context.Context, device string) error {
	if _, err := d.GetCapabilities(ctx, device); err != nil {
		return err
	}
	d.Redraw(device)
	return nil
}

// RunPeriodicRedraw redraws every currently Connected device's cache every
// interval, independent of any reconnect detection, until ctx is canceled.
func (d *Dispatcher) RunPeriodicRedraw(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, dev := range d.registry.Summaries() {
				if dev.Connected {
					d.Redraw(dev.Name)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
```

(drop the now-unused `log/slog` import).

`cmd/blinkenkeysd/main.go`:

- change `go disp.RunPeriodicRedraw(ctx, dispatcher.RedrawInterval, logger)` to
  `go disp.RunPeriodicRedraw(ctx, dispatcher.RedrawInterval)`
- replace the initial reconcile line with:

```go
	res := registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)
	logAdded(logger, res.Added)
	syncDevices(ctx, registry, disp, res.Reconnected, logger)
```

- change `pollForDevices` to (and update its doc comment to describe syncDevices instead of RedrawReconnected):

```go
func pollForDevices(ctx context.Context, state *deviceState, registry *dispatcher.Registry, cache *dispatcher.Cache, disp *dispatcher.Dispatcher, logger *slog.Logger) {
	ticker := time.NewTicker(enumeratePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			res := registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)
			logAdded(logger, res.Added)
			syncDevices(ctx, registry, disp, res.Reconnected, logger)
			for _, name := range res.Evicted {
				cache.Forget(name)
			}
		case <-ctx.Done():
			return
		}
	}
}

// syncDevices ensures capabilities for every connected device lacking them
// (newly seen, or whose earlier fetch failed — retried every poll) and for
// every reconnected device; EnsureCapabilities also resolves pending writes
// and redraws, which is the reconnect-triggered redraw.
func syncDevices(ctx context.Context, registry *dispatcher.Registry, disp *dispatcher.Dispatcher, reconnected []string, logger *slog.Logger) {
	seen := make(map[string]bool)
	for _, name := range append(registry.ConnectedWithoutCaps(), reconnected...) {
		if seen[name] {
			continue
		}
		seen[name] = true
		if err := disp.EnsureCapabilities(ctx, name); err != nil {
			logger.Warn("capabilities fetch failed", "device", name, "err", err)
		}
	}
}

// logAdded logs each first-seen device's name with the config.yaml snippet
// that pre-declares it (spec §5), so users don't have to query GET /devices.
func logAdded(logger *slog.Logger, names []string) {
	for _, name := range names {
		logger.Info("new device seen; to pre-declare it add under devices: in config.yaml",
			"device", name, "snippet", "- id: "+name+"\n  optional: true")
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test -race ./...`
Expected: PASS (including `internal/api`, still on the `SetKey` adapter).

- [ ] **Step 5: Commit**

```bash
git add internal/dispatcher cmd/blinkenkeysd
nice -n 10 git commit -m "Make the color cache a frame buffer with best-effort delivery

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Effects — primitives

**Files:**
- Create: `internal/effects/errors.go`, `internal/effects/primitives.go`
- Test: `internal/effects/primitives_test.go`

**Interfaces:**
- Consumes: `color.HSV`, `color.ParseHSV`.
- Produces (package-internal unless noted):
  - exported: `effects.ErrInvalid`, `effects.ErrUnknownEffect`, `effects.ErrUnknownState`
  - `bindPrimitive(name string, raw map[string]any) (func(elapsed time.Duration) color.HSV, error)`: `raw` must hold exactly the primitive's declared settings. Every setting is required; there are no defaults. Returns the primitive's frame function. Numbers are accepted as `uint64`, `int64` or `float64`, which is what YAML decoding into `any` produces. Colors are accepted as strings.

- [ ] **Step 1: Write the failing tests**

Create `internal/effects/primitives_test.go`:

```go
package effects

import (
	"errors"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

func mustBind(t *testing.T, name string, raw map[string]any) func(time.Duration) color.HSV {
	t.Helper()
	f, err := bindPrimitive(name, raw)
	if err != nil {
		t.Fatalf("bindPrimitive(%s, %v): %v", name, raw, err)
	}
	return f
}

var (
	red   = color.HSV{H: 0, S: 255, V: 255}
	green = color.HSV{H: 85, S: 255, V: 255}
	black = color.HSV{}
)

func TestBreathe(t *testing.T) {
	// "0,255,200": a color with V 200, so levels are easy to read.
	tests := []struct {
		duty  float64
		at    time.Duration
		wantV uint8
	}{
		{0.5, 0, 0},
		{0.5, 250 * time.Millisecond, 100},
		{0.5, 500 * time.Millisecond, 200},
		{0.5, 750 * time.Millisecond, 100},
		{0.5, 1000 * time.Millisecond, 0}, // next period
		{1.0, 500 * time.Millisecond, 100}, // rising sawtooth
		{0.0, 0, 200},                      // falling sawtooth
		{0.0, 500 * time.Millisecond, 100},
	}
	for _, tt := range tests {
		f := mustBind(t, "breathe", map[string]any{"color": "0,255,200", "frequency_hz": 1.0, "duty_cycle": tt.duty})
		got := f(tt.at)
		if got.V != tt.wantV || got.H != 0 || got.S != 255 {
			t.Errorf("breathe duty %.1f at %v = %+v, want V %d with H/S held", tt.duty, tt.at, got, tt.wantV)
		}
	}
}

func TestAlternateSwitchPoints(t *testing.T) {
	tests := []struct {
		duty float64
		at   time.Duration
		want color.HSV
	}{
		{0.2, 0, red},
		{0.2, 199 * time.Millisecond, red},
		{0.2, 200 * time.Millisecond, green},
		{0.2, 999 * time.Millisecond, green},
		{0.2, 1000 * time.Millisecond, red},
		{0.8, 799 * time.Millisecond, red},
		{0.8, 800 * time.Millisecond, green},
	}
	for _, tt := range tests {
		f := mustBind(t, "alternate", map[string]any{
			"color_a": "#ff0000", "color_b": "#00ff00", "frequency_hz": uint64(1), "duty_cycle": tt.duty,
		})
		if got := f(tt.at); got != tt.want {
			t.Errorf("alternate duty %.1f at %v = %+v, want %+v", tt.duty, tt.at, got, tt.want)
		}
	}
}

func TestBlinkIsAlternateWithBlack(t *testing.T) {
	blink := mustBind(t, "blink", map[string]any{"color": "#ff0000", "frequency_hz": 2.0, "duty_cycle": 0.5})
	alt := mustBind(t, "alternate", map[string]any{"color_a": "#ff0000", "color_b": "black", "frequency_hz": 2.0, "duty_cycle": 0.5})
	for ms := 0; ms < 1000; ms += 50 {
		at := time.Duration(ms) * time.Millisecond
		if blink(at) != alt(at) {
			t.Errorf("at %v blink = %+v, alternate-with-black = %+v", at, blink(at), alt(at))
		}
	}
	if blink(300*time.Millisecond) != black {
		t.Error("blink at 2Hz, 300ms should be in its black half")
	}
}

func TestBindPrimitiveErrors(t *testing.T) {
	// breathe returns a complete, valid breathe setting set, with over
	// applied and drop removed — so each case breaks exactly one thing.
	breathe := func(over map[string]any, drop ...string) map[string]any {
		m := map[string]any{"color": "red", "frequency_hz": 1.0, "duty_cycle": 0.5}
		for k, v := range over {
			m[k] = v
		}
		for _, k := range drop {
			delete(m, k)
		}
		return m
	}
	if _, err := bindPrimitive("breathe", breathe(nil)); err != nil {
		t.Fatalf("baseline breathe settings rejected: %v", err)
	}
	tests := []struct {
		name string
		prim string
		raw  map[string]any
	}{
		{"unknown primitive", "sparkle", breathe(nil)},
		{"missing color", "breathe", breathe(nil, "color")},
		{"missing frequency", "breathe", breathe(nil, "frequency_hz")},
		{"missing duty cycle", "breathe", breathe(nil, "duty_cycle")},
		{"unknown setting", "breathe", breathe(map[string]any{"speed": 1.0})},
		{"zero frequency", "breathe", breathe(map[string]any{"frequency_hz": 0.0})},
		{"negative frequency", "breathe", breathe(map[string]any{"frequency_hz": int64(-1)})},
		{"duty above 1", "breathe", breathe(map[string]any{"duty_cycle": 1.5})},
		{"bad color", "breathe", breathe(map[string]any{"color": "not-a-color"})},
		{"color not a string", "breathe", breathe(map[string]any{"color": 5.0})},
		{"number not a number", "breathe", breathe(map[string]any{"duty_cycle": "half"})},
	}
	for _, tt := range tests {
		if _, err := bindPrimitive(tt.prim, tt.raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tt.name, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/effects/`
Expected: FAIL to compile (`undefined: bindPrimitive`).

- [ ] **Step 3: Implement**

Create `internal/effects/errors.go`:

```go
// Package effects implements Phase 3's animation model: Go-coded primitives,
// YAML-defined multi-stage effects compiled into timelines, templates
// mapping application states to colors or effects, and the Engine that runs
// effects on keys.
package effects

import "errors"

// ErrInvalid marks a malformed effect or template definition — fatal at
// load time.
var ErrInvalid = errors.New("effects: invalid")

// ErrUnknownEffect is returned for a request naming an effect that doesn't
// exist — internal/api maps it to a 404.
var ErrUnknownEffect = errors.New("effects: unknown effect")

// ErrUnknownState is returned for a request naming a template state that
// doesn't exist — internal/api maps it to a 404.
var ErrUnknownState = errors.New("effects: unknown template state")
```

Create `internal/effects/primitives.go`:

```go
package effects

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

type settingKind int

const (
	kindColor settingKind = iota
	kindFrequency
	kindDuty
)

// settingSpec declares one primitive setting. Every setting is required.
type settingSpec struct {
	name string
	kind settingKind
}

// values holds bound, validated settings: colors as color.HSV, numbers as float64.
type values map[string]any

type primitive struct {
	settings []settingSpec
	frame    func(v values, elapsed time.Duration) color.HSV
}

var waveSettings = []settingSpec{
	{name: "frequency_hz", kind: kindFrequency},
	{name: "duty_cycle", kind: kindDuty},
}

// primitives is the registry of animation primitives, keyed by the name YAML
// uses. Read-only after package init; adding a primitive means adding an
// entry here — nothing else changes.
var primitives = map[string]primitive{
	"breathe": {
		settings: append([]settingSpec{{name: "color", kind: kindColor}}, waveSettings...),
		frame:    breatheFrame,
	},
	"alternate": {
		settings: append([]settingSpec{{name: "color_a", kind: kindColor}, {name: "color_b", kind: kindColor}}, waveSettings...),
		frame:    alternateFrame,
	},
	"blink": {
		settings: append([]settingSpec{{name: "color", kind: kindColor}}, waveSettings...),
		frame:    blinkFrame,
	},
}

// bindPrimitive validates raw against primitive name's declared settings —
// all required, none unknown — and returns the primitive's frame function.
func bindPrimitive(name string, raw map[string]any) (func(time.Duration) color.HSV, error) {
	p, ok := primitives[name]
	if !ok {
		known := make([]string, 0, len(primitives))
		for k := range primitives {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("%w: unknown primitive %q; known: %s", ErrInvalid, name, strings.Join(known, ", "))
	}
	vals := make(values, len(p.settings))
	for _, spec := range p.settings {
		v, ok := raw[spec.name]
		if !ok {
			return nil, fmt.Errorf("%w: primitive %s: missing setting %q", ErrInvalid, name, spec.name)
		}
		bound, err := bindValue(spec, v)
		if err != nil {
			return nil, fmt.Errorf("%w: primitive %s: setting %q: %v", ErrInvalid, name, spec.name, err)
		}
		vals[spec.name] = bound
	}
	for k := range raw {
		if _, ok := vals[k]; !ok {
			return nil, fmt.Errorf("%w: primitive %s: unknown setting %q", ErrInvalid, name, k)
		}
	}
	return func(elapsed time.Duration) color.HSV { return p.frame(vals, elapsed) }, nil
}

func bindValue(spec settingSpec, v any) (any, error) {
	if spec.kind == kindColor {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("want a color string, got %v", v)
		}
		return color.ParseHSV(s)
	}
	f, ok := toFloat(v)
	if !ok {
		return nil, fmt.Errorf("want a number, got %v", v)
	}
	switch {
	case spec.kind == kindFrequency && f <= 0:
		return nil, fmt.Errorf("must be > 0, got %v", f)
	case spec.kind == kindDuty && (f < 0 || f > 1):
		return nil, fmt.Errorf("must be between 0 and 1, got %v", f)
	}
	return f, nil
}

// toFloat accepts every numeric type YAML decoding into any produces.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case uint64:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// phase is the fraction [0,1) of the current period elapsed.
func phase(v values, elapsed time.Duration) float64 {
	p := elapsed.Seconds() * v["frequency_hz"].(float64)
	return p - math.Floor(p)
}

// breatheFrame scales color's V along a triangle wave: rising for
// duty_cycle of each period, falling for the rest; H and S held.
func breatheFrame(v values, elapsed time.Duration) color.HSV {
	c := v["color"].(color.HSV)
	d := v["duty_cycle"].(float64)
	ph := phase(v, elapsed)
	var level float64
	if ph < d {
		level = ph / d
	} else {
		level = (1 - ph) / (1 - d)
	}
	return color.HSV{H: c.H, S: c.S, V: uint8(math.Round(float64(c.V) * level))} // #nosec G115 -- level is in [0,1], so the result is <= c.V
}

// alternateFrame is a square wave: color_a for duty_cycle of each period,
// then color_b.
func alternateFrame(v values, elapsed time.Duration) color.HSV {
	if phase(v, elapsed) < v["duty_cycle"].(float64) {
		return v["color_a"].(color.HSV)
	}
	return v["color_b"].(color.HSV)
}

// blinkFrame is alternate with color_b = black.
func blinkFrame(v values, elapsed time.Duration) color.HSV {
	return alternateFrame(values{
		"color_a":      v["color"],
		"color_b":      color.HSV{},
		"frequency_hz": v["frequency_hz"],
		"duty_cycle":   v["duty_cycle"],
	}, elapsed)
}
```

(`breatheFrame` never divides by zero. With `d == 0`, `ph < d` is never true, so it
uses `(1-ph)/(1-d)`. With `d == 1`, `ph < 1` is always true, so it uses `ph/d`.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test ./internal/effects/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/effects
nice -n 10 git commit -m "Add effects primitives: breathe, alternate, blink

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Effects — compile effect definitions into timelines

**Files:**
- Create: `internal/effects/model.go`, `internal/effects/compile.go`
- Test: `internal/effects/compile_test.go`

**Interfaces:**
- Consumes: `bindPrimitive` and the errors (Task 5).
- Produces:
  - `type Timeline` (opaque, immutable, safe to share between keys and goroutines):
    - `At(elapsed time.Duration) (color.HSV, bool)`: `true` means the effect has ended, and the color is then its final state;
    - `Total() (time.Duration, bool)`: `false` means open-ended.
  - `type Action struct { Color *color.HSV; Timeline *Timeline }` (exactly one is set; Task 7 fills it from templates)
  - `type Library struct { effects map[string]*Timeline; states map[string]Action }` (fully compiled; read-only after construction)
  - `(*Library).Effect(name string) (*Timeline, error)`: a map lookup. An unknown name returns `ErrUnknownEffect`, and the message lists the known effects.
  - package-internal:
    - `effectDef`, `stageDef`, `stateDef`;
    - `decodeStrict(data []byte, v any) error`;
    - `parseColor(s string) (color.HSV, error)`, which wraps `ErrInvalid`;
    - `sortedKeys`;
    - `compileEffects(src map[string][]byte) (map[string]*Timeline, error)`: name → YAML source. Every effect is compiled. Any invalid one fails the whole set with `ErrInvalid`, and the error names `effects/<name>.yaml`.
  - test helpers in `compile_test.go`: `libFrom(t, map[string]string) *Library` and `mustEffect(t, lib, name) *Timeline` (Task 8 reuses both)

Compile rules (from the spec). There are no effect parameters: every value is literal.
- Each stage has exactly one of `color` / `primitive` / `effect`. `settings` is only valid with `primitive`.
- `duration` is a Go duration string and must be > 0.
- `duration` may be omitted only on the last stage, which makes the effect open-ended.
- A nested effect without `duration` takes that effect's total. If the nested effect is open-ended, this is allowed only in the last stage.
- `final_state` is required when the effect ends. When it's open-ended, `final_state` is optional but still validated if present.
- A nested reference to an unknown effect is `ErrInvalid`, and so are cycles.

- [ ] **Step 1: Write the failing tests**

Create `internal/effects/compile_test.go`:

```go
package effects

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

func sources(effects map[string]string) map[string][]byte {
	src := make(map[string][]byte, len(effects))
	for name, s := range effects {
		src[name] = []byte(s)
	}
	return src
}

// libFrom compiles effect name -> YAML source into a Library, exactly as
// Load does for effects/*.yaml.
func libFrom(t *testing.T, effects map[string]string) *Library {
	t.Helper()
	compiled, err := compileEffects(sources(effects))
	if err != nil {
		t.Fatalf("compileEffects: %v", err)
	}
	return &Library{effects: compiled}
}

func mustEffect(t *testing.T, l *Library, name string) *Timeline {
	t.Helper()
	tl, err := l.Effect(name)
	if err != nil {
		t.Fatalf("Effect(%s): %v", name, err)
	}
	return tl
}

const timer5min = `
stages:
  - duration: 3m
    color: "#00ff00"
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.8 }
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.2 }
final_state: "#ff0000"
`

var (
	pureGreen = color.HSV{H: 85, S: 255, V: 255}
	blue      = color.HSV{H: 170, S: 255, V: 255}
	white     = color.HSV{H: 0, S: 0, V: 255}
)

func TestTimer5minStageBoundaries(t *testing.T) {
	tl := mustEffect(t, libFrom(t, map[string]string{"timer5min": timer5min}), "timer5min")
	tests := []struct {
		at       time.Duration
		want     color.HSV
		wantDone bool
	}{
		{0, pureGreen, false},
		{179900 * time.Millisecond, pureGreen, false},
		{180 * time.Second, pureGreen, false},                // stage 2, phase 0 < 0.8
		{180*time.Second + 900*time.Millisecond, red, false}, // stage 2, phase 0.9
		{240 * time.Second, pureGreen, false},                // stage 3, phase 0 < 0.2
		{240*time.Second + 500*time.Millisecond, red, false}, // stage 3, phase 0.5
		{300 * time.Second, red, true},                       // final_state
		{time.Hour, red, true},
	}
	for _, tt := range tests {
		got, done := tl.At(tt.at)
		if got != tt.want || done != tt.wantDone {
			t.Errorf("At(%v) = %+v, %v; want %+v, %v", tt.at, got, done, tt.want, tt.wantDone)
		}
	}
	if total, finite := tl.Total(); !finite || total != 5*time.Minute {
		t.Errorf("Total = %v, %v", total, finite)
	}
}

func TestOpenEndedNeverFinishes(t *testing.T) {
	tl := mustEffect(t, libFrom(t, map[string]string{"b": `
stages:
  - primitive: breathe
    settings: { color: blue, frequency_hz: 1, duty_cycle: 0.5 }
`}), "b")
	if _, done := tl.At(24 * time.Hour); done {
		t.Error("open-ended effect reported done")
	}
	if _, finite := tl.Total(); finite {
		t.Error("open-ended effect reported finite total")
	}
}

const inner = `
stages:
  - { duration: 1s, color: red }
  - { duration: 1s, color: "#00ff00" }
final_state: blue
`

func TestNestedCutOffHoldAndDefaultDuration(t *testing.T) {
	l := libFrom(t, map[string]string{
		"inner": inner,
		"cut": `
stages:
  - { duration: 1500ms, effect: inner }
  - { duration: 1s, color: white }
final_state: black
`,
		"hold": `
stages:
  - { duration: 3s, effect: inner }
final_state: black
`,
		"auto": `
stages:
  - { effect: inner }
  - { duration: 1s, color: white }
final_state: black
`,
	})
	checks := []struct {
		effect string
		at     time.Duration
		want   color.HSV
	}{
		{"cut", 1200 * time.Millisecond, pureGreen},
		{"cut", 1500 * time.Millisecond, white}, // inner cut off
		{"hold", 2500 * time.Millisecond, blue}, // inner's final_state held
		{"auto", 1999 * time.Millisecond, pureGreen},
		{"auto", 2000 * time.Millisecond, white}, // stage took inner's 2s total
	}
	for _, c := range checks {
		if got, _ := mustEffect(t, l, c.effect).At(c.at); got != c.want {
			t.Errorf("%s.At(%v) = %+v, want %+v", c.effect, c.at, got, c.want)
		}
	}
}

func TestOpenEndedNestedAsLastStage(t *testing.T) {
	l := libFrom(t, map[string]string{
		"breathe_blue": "stages:\n  - primitive: breathe\n    settings: { color: blue, frequency_hz: 0.5, duty_cycle: 0.5 }\n",
		"flash_then_breathe": `
stages:
  - { duration: 1s, primitive: blink, settings: { color: red, frequency_hz: 3, duty_cycle: 0.5 } }
  - { effect: breathe_blue }
`,
	})
	if _, done := mustEffect(t, l, "flash_then_breathe").At(time.Hour); done {
		t.Error("outer effect should be open-ended")
	}
}

func TestUnknownEffectListsKnown(t *testing.T) {
	_, err := libFrom(t, map[string]string{"timer5min": timer5min}).Effect("timer5mn")
	if !errors.Is(err, ErrUnknownEffect) || !strings.Contains(err.Error(), "timer5min") {
		t.Errorf("err = %v, want ErrUnknownEffect listing timer5min", err)
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name    string
		effects map[string]string
	}{
		{"malformed yaml", map[string]string{"e": "stages: [\n"}},
		{"unknown effect field", map[string]string{"e": "stages:\n  - { duration: 1s, color: red }\nfinal_state: red\nloop: true\n"}},
		{"unknown stage field", map[string]string{"e": "stages:\n  - { duration: 1s, color: red, colour: blue }\nfinal_state: red\n"}},
		{"no stages", map[string]string{"e": "final_state: red\nstages: []\n"}},
		{"two kinds in a stage", map[string]string{"e": "stages:\n  - { color: red, primitive: blink }\n"}},
		{"no kind in a stage", map[string]string{"e": "stages:\n  - { duration: 1s }\nfinal_state: red\n"}},
		{"settings with color", map[string]string{"e": "stages:\n  - { duration: 1s, color: red, settings: { x: 1 } }\nfinal_state: red\n"}},
		{"settings with effect", map[string]string{
			"inner": inner,
			"e":     "stages:\n  - { effect: inner, settings: { x: 1 } }\nfinal_state: red\n",
		}},
		{"missing primitive setting", map[string]string{"e": "stages:\n  - { primitive: breathe, settings: { color: red, frequency_hz: 1 } }\n"}},
		{"missing duration mid-effect", map[string]string{"e": "stages:\n  - { color: red }\n  - { duration: 1s, color: blue }\nfinal_state: red\n"}},
		{"zero duration", map[string]string{"e": "stages:\n  - { duration: 0s, color: red }\nfinal_state: red\n"}},
		{"bad duration", map[string]string{"e": "stages:\n  - { duration: soon, color: red }\nfinal_state: red\n"}},
		{"missing final_state when finite", map[string]string{"e": "stages:\n  - { duration: 1s, color: red }\n"}},
		{"bad final_state when open-ended", map[string]string{"e": "stages:\n  - { color: red }\nfinal_state: nope\n"}},
		{"bad color", map[string]string{"e": "stages:\n  - { duration: 1s, color: nope }\nfinal_state: red\n"}},
		{"unknown nested effect", map[string]string{"e": "stages:\n  - { duration: 1s, effect: ghost }\nfinal_state: red\n"}},
		{"self reference", map[string]string{"e": "stages:\n  - { duration: 1s, effect: e }\nfinal_state: red\n"}},
		{"cycle", map[string]string{
			"a": "stages:\n  - { duration: 1s, effect: b }\nfinal_state: red\n",
			"b": "stages:\n  - { duration: 1s, effect: a }\nfinal_state: red\n",
		}},
		{"open-ended nested not last", map[string]string{
			"open": "stages:\n  - { color: red }\n",
			"e":    "stages:\n  - { effect: open }\n  - { duration: 1s, color: blue }\nfinal_state: red\n",
		}},
	}
	for _, tt := range tests {
		if _, err := compileEffects(sources(tt.effects)); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", tt.name, err)
		}
	}
}

func TestCompileErrorNamesFile(t *testing.T) {
	_, err := compileEffects(sources(map[string]string{"broken": "stages: []\n"}))
	if err == nil || !strings.Contains(err.Error(), "effects/broken.yaml") {
		t.Errorf("err = %v, want one naming effects/broken.yaml", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/effects/`
Expected: FAIL to compile (`undefined: compileEffects`, `undefined: Library`, ...).

- [ ] **Step 3: Implement**

Create `internal/effects/model.go`:

```go
package effects

import "github.com/goccy/go-yaml"

// effectDef is one effects/<name>.yaml file.
type effectDef struct {
	Stages     []stageDef `yaml:"stages"`
	FinalState string     `yaml:"final_state"`
}

// stageDef is one stage: exactly one of Color, Primitive, Effect; Settings
// only with Primitive.
type stageDef struct {
	Duration  string         `yaml:"duration"` // Go duration string; "" = none
	Color     string         `yaml:"color"`
	Primitive string         `yaml:"primitive"`
	Effect    string         `yaml:"effect"`
	Settings  map[string]any `yaml:"settings"`
}

// stateDef is one state in a templates/<program>.yaml file: exactly one of
// Color or Effect.
type stateDef struct {
	Color  string `yaml:"color"`
	Effect string `yaml:"effect"`
}

// decodeStrict decodes YAML, rejecting unknown fields so typos fail loudly.
func decodeStrict(data []byte, v any) error {
	return yaml.UnmarshalWithOptions(data, v, yaml.DisallowUnknownField())
}
```

Create `internal/effects/compile.go`:

```go
package effects

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
)

// stage is one compiled stage. dur == 0 marks an open-ended stage, which
// is only ever the last one.
type stage struct {
	dur   time.Duration
	frame func(elapsed time.Duration) color.HSV
}

// Timeline is one compiled effect. It is immutable, so a single Timeline is
// shared by every key running that effect; each run's start time is kept
// by the Engine.
type Timeline struct {
	stages []stage
	final  color.HSV
}

// At returns the effect's color at elapsed time since it started, and
// whether it has ended (the color is then its final_state). Timing comes
// from elapsed wall-clock time, never frame counts, so it doesn't drift.
func (t *Timeline) At(elapsed time.Duration) (color.HSV, bool) {
	for _, s := range t.stages {
		if s.dur == 0 || elapsed < s.dur {
			return s.frame(elapsed), false
		}
		elapsed -= s.dur
	}
	return t.final, true
}

// Total returns the effect's total duration, or false if it's open-ended.
func (t *Timeline) Total() (time.Duration, bool) {
	var total time.Duration
	for _, s := range t.stages {
		if s.dur == 0 {
			return 0, false
		}
		total += s.dur
	}
	return total, true
}

// Action is what a template state maps to: exactly one of Color or Timeline.
type Action struct {
	Color    *color.HSV
	Timeline *Timeline
}

// Library is the loaded, fully compiled set of effects and template states.
// Read-only after construction; safe for concurrent use.
type Library struct {
	effects map[string]*Timeline
	states  map[string]Action // "program/state" -> action
}

// Effect returns the compiled effect called name.
func (l *Library) Effect(name string) (*Timeline, error) {
	tl, ok := l.effects[name]
	if !ok {
		return nil, fmt.Errorf("%w %q; known: %s", ErrUnknownEffect, name, strings.Join(sortedKeys(l.effects), ", "))
	}
	return tl, nil
}

// compileEffects decodes and compiles every effect definition (name ->
// YAML source). Any invalid definition fails the whole set, naming its file.
func compileEffects(src map[string][]byte) (map[string]*Timeline, error) {
	c := &compiler{defs: make(map[string]*effectDef, len(src)), done: make(map[string]*Timeline, len(src))}
	for _, name := range sortedKeys(src) {
		var def effectDef
		if err := decodeStrict(src[name], &def); err != nil {
			return nil, fmt.Errorf("effects/%s.yaml: %w: %v", name, ErrInvalid, err)
		}
		c.defs[name] = &def
	}
	for _, name := range sortedKeys(c.defs) {
		if _, err := c.compile(name, nil); err != nil {
			return nil, fmt.Errorf("effects/%s.yaml: %w", name, err)
		}
	}
	return c.done, nil
}

// compiler memoizes compiled effects so a nested effect compiles once and
// its Timeline is shared by every effect that references it.
type compiler struct {
	defs map[string]*effectDef
	done map[string]*Timeline
}

// compile compiles effect name; stack is the chain of effects currently
// being compiled, for cycle detection.
func (c *compiler) compile(name string, stack []string) (*Timeline, error) {
	if tl, ok := c.done[name]; ok {
		return tl, nil
	}
	def, ok := c.defs[name]
	if !ok {
		return nil, fmt.Errorf("%w: unknown effect %q", ErrInvalid, name)
	}
	if slices.Contains(stack, name) {
		return nil, fmt.Errorf("%w: effect cycle %s -> %s", ErrInvalid, strings.Join(stack, " -> "), name)
	}
	stack = append(slices.Clone(stack), name)

	if len(def.Stages) == 0 {
		return nil, fmt.Errorf("%w: effect %q has no stages", ErrInvalid, name)
	}
	tl := &Timeline{}
	for i, sd := range def.Stages {
		st, err := c.compileStage(sd, i == len(def.Stages)-1, stack)
		if err != nil {
			return nil, fmt.Errorf("effect %q stage %d: %w", name, i, err)
		}
		tl.stages = append(tl.stages, st)
	}
	_, finite := tl.Total()
	switch {
	case def.FinalState != "":
		final, err := parseColor(def.FinalState)
		if err != nil {
			return nil, fmt.Errorf("effect %q final_state: %w", name, err)
		}
		tl.final = final
	case finite:
		return nil, fmt.Errorf("%w: effect %q ends but has no final_state", ErrInvalid, name)
	}
	c.done[name] = tl
	return tl, nil
}

func (c *compiler) compileStage(sd stageDef, last bool, stack []string) (stage, error) {
	kinds := 0
	for _, s := range []string{sd.Color, sd.Primitive, sd.Effect} {
		if s != "" {
			kinds++
		}
	}
	if kinds != 1 {
		return stage{}, fmt.Errorf("%w: need exactly one of color, primitive, effect", ErrInvalid)
	}
	if sd.Settings != nil && sd.Primitive == "" {
		return stage{}, fmt.Errorf("%w: settings is only valid with primitive", ErrInvalid)
	}
	var dur time.Duration
	if sd.Duration != "" {
		d, err := parseDuration(sd.Duration)
		if err != nil {
			return stage{}, err
		}
		dur = d
	}
	var frame func(time.Duration) color.HSV
	switch {
	case sd.Color != "":
		c, err := parseColor(sd.Color)
		if err != nil {
			return stage{}, err
		}
		frame = func(time.Duration) color.HSV { return c }
	case sd.Primitive != "":
		f, err := bindPrimitive(sd.Primitive, sd.Settings)
		if err != nil {
			return stage{}, err
		}
		frame = f
	default:
		nested, err := c.compile(sd.Effect, stack)
		if err != nil {
			return stage{}, err
		}
		if dur == 0 {
			if total, finite := nested.Total(); finite {
				dur = total
			}
		}
		frame = func(el time.Duration) color.HSV {
			col, _ := nested.At(el)
			return col
		}
	}
	if dur == 0 && !last {
		return stage{}, fmt.Errorf("%w: duration is required on every stage but the last (and an open-ended nested effect can only be last)", ErrInvalid)
	}
	return stage{dur: dur, frame: frame}, nil
}

// parseColor is color.ParseHSV with errors marked ErrInvalid.
func parseColor(s string) (color.HSV, error) {
	c, err := color.ParseHSV(s)
	if err != nil {
		return color.HSV{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return c, nil
}

func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: duration %q: %v (want e.g. 1500ms, 90s, 3m)", ErrInvalid, s, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%w: duration %q must be > 0", ErrInvalid, s)
	}
	return d, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test ./internal/effects/`
Expected: PASS. If `unknown stage field` fails, strict decoding isn't reaching
structs inside a slice. STOP and report it; don't delete the case.

- [ ] **Step 5: Commit**

```bash
git add internal/effects
nice -n 10 git commit -m "Compile YAML effect definitions into timelines

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Effects — `Load(dir)`, template states, example config

**Files:**
- Create: `internal/effects/load.go`, `internal/effects/load_test.go`
- Create: `examples/config/effects/timer5min.yaml`, `examples/config/effects/breathe_blue.yaml`, `examples/config/templates/claude.yaml`

**Interfaces:**
- Consumes: `compileEffects`, `decodeStrict`, `parseColor`, `sortedKeys`, `Library`, `Action` (Task 6).
- Produces:
  - `effects.Load(dir string) (*Library, error)`: reads `dir/effects/*.yaml` and `dir/templates/*.yaml`. Missing directories are fine; non-`.yaml` files are ignored. Any invalid definition fails the whole load, and the error names the file.
  - `(*Library).State(ref string) (Action, error)`: `ref` is `program/state`. An unknown one returns `ErrUnknownState`, and the message lists the known states.

- [ ] **Step 1: Write the failing tests**

Create `internal/effects/load_test.go`:

```go
package effects

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTree creates files (relative path -> content) under a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func loadExamples(t *testing.T) *Library {
	t.Helper()
	lib, err := Load(filepath.Join("..", "..", "examples", "config"))
	if err != nil {
		t.Fatalf("Load(examples/config): %v", err)
	}
	return lib
}

func TestLoadExamples(t *testing.T) {
	lib := loadExamples(t)
	working, err := lib.State("claude/working")
	if err != nil || working.Timeline == nil {
		t.Fatalf("claude/working = %+v, %v; want an effect", working, err)
	}
	if _, finite := working.Timeline.Total(); finite {
		t.Error("claude/working (breathe_blue) should be open-ended")
	}
	idle, err := lib.State("claude/idle")
	if err != nil || idle.Timeline == nil {
		t.Fatalf("claude/idle = %+v, %v", idle, err)
	}
	if total, _ := idle.Timeline.Total(); total != 5*time.Minute {
		t.Errorf("claude/idle total = %v, want 5m", total)
	}
	waiting, err := lib.State("claude/waiting")
	if err != nil || waiting.Color == nil {
		t.Fatalf("claude/waiting = %+v, %v; want a color", waiting, err)
	}
	if _, err := lib.Effect("timer5min"); err != nil {
		t.Errorf("Effect(timer5min): %v", err)
	}
}

func TestLoadMissingDirsIsEmpty(t *testing.T) {
	lib, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load(empty dir): %v", err)
	}
	if _, err := lib.Effect("x"); !errors.Is(err, ErrUnknownEffect) {
		t.Errorf("Effect on empty library: err = %v", err)
	}
	if _, err := lib.State("a/b"); !errors.Is(err, ErrUnknownState) {
		t.Errorf("State on empty library: err = %v", err)
	}
}

func TestLoadIgnoresNonYAMLFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{"effects/README.md": "not yaml", "effects/notes.yaml~": "junk"})
	if _, err := Load(dir); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestStateUnknownListsKnown(t *testing.T) {
	lib := loadExamples(t)
	for _, ref := range []string{"claude/sleeping", "nope/idle", "claude"} {
		_, err := lib.State(ref)
		if !errors.Is(err, ErrUnknownState) || !strings.Contains(err.Error(), "claude/idle") {
			t.Errorf("State(%q) err = %v, want ErrUnknownState listing claude/idle", ref, err)
		}
	}
}

func TestLoadFatalCases(t *testing.T) {
	const ok = "stages:\n  - { duration: 1s, color: red }\nfinal_state: red\n"
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"bad effect file name", map[string]string{"effects/Bad Name.yaml": ok}},
		{"bad template file name", map[string]string{"templates/UPPER.yaml": "idle: { color: red }\n"}},
		{"bad state name", map[string]string{"templates/claude.yaml": "Idle State: { color: red }\n"}},
		{"invalid effect", map[string]string{"effects/e.yaml": "stages: []\n"}},
		{"unknown state field", map[string]string{"templates/claude.yaml": "idle: { color: red, colour: blue }\n"}},
		{"state params (not in phase 3)", map[string]string{"effects/e.yaml": ok, "templates/claude.yaml": "idle: { effect: e, params: { x: 1 } }\n"}},
		{"state with color and effect", map[string]string{"effects/e.yaml": ok, "templates/claude.yaml": "idle: { color: red, effect: e }\n"}},
		{"state with neither", map[string]string{"templates/claude.yaml": "idle: {}\n"}},
		{"state bad color", map[string]string{"templates/claude.yaml": "idle: { color: nope }\n"}},
		{"state unknown effect", map[string]string{"templates/claude.yaml": "idle: { effect: ghost }\n"}},
		{"malformed template yaml", map[string]string{"templates/claude.yaml": "idle: [\n"}},
	}
	for _, tt := range tests {
		if _, err := Load(writeTree(t, tt.files)); err == nil {
			t.Errorf("%s: Load succeeded, want error", tt.name)
		}
	}
}
```

The `unknown state field` and `state params` cases check the open question from
"Verified ground truth": does strict decoding reach structs inside a
`map[string]stateDef`? If only those cases fail, STOP and report it. Don't loosen the
test.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/effects/`
Expected: FAIL to compile (`undefined: Load`).

- [ ] **Step 3: Implement**

Create `internal/effects/load.go`:

```go
package effects

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var namePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Load reads dir/effects/*.yaml (effect name = file name) and
// dir/templates/*.yaml (namespace = file name), compiling every effect and
// resolving every template state now, so an invalid definition is reported
// at startup (or by -check-config) rather than at request time. Missing
// directories are fine; non-.yaml files are ignored.
func Load(dir string) (*Library, error) {
	effectSrc, err := readYAMLDir(filepath.Join(dir, "effects"))
	if err != nil {
		return nil, err
	}
	compiled, err := compileEffects(effectSrc)
	if err != nil {
		return nil, err
	}
	templateSrc, err := readYAMLDir(filepath.Join(dir, "templates"))
	if err != nil {
		return nil, err
	}
	states, err := compileTemplates(templateSrc, compiled)
	if err != nil {
		return nil, err
	}
	return &Library{effects: compiled, states: states}, nil
}

// State returns the action for a "program/state" template reference.
func (l *Library) State(ref string) (Action, error) {
	act, ok := l.states[ref]
	if !ok {
		return Action{}, fmt.Errorf("%w %q; known: %s", ErrUnknownState, ref, strings.Join(sortedKeys(l.states), ", "))
	}
	return act, nil
}

// compileTemplates decodes every template file (program -> YAML source) and
// resolves each state against the compiled effects.
func compileTemplates(src map[string][]byte, effects map[string]*Timeline) (map[string]Action, error) {
	out := make(map[string]Action)
	for _, program := range sortedKeys(src) {
		var states map[string]stateDef
		if err := decodeStrict(src[program], &states); err != nil {
			return nil, fmt.Errorf("templates/%s.yaml: %w: %v", program, ErrInvalid, err)
		}
		for _, state := range sortedKeys(states) {
			if !namePattern.MatchString(state) {
				return nil, fmt.Errorf("templates/%s.yaml: %w: state name %q must match [a-z0-9_-]+", program, ErrInvalid, state)
			}
			act, err := resolveState(states[state], effects)
			if err != nil {
				return nil, fmt.Errorf("templates/%s.yaml: state %q: %w", program, state, err)
			}
			out[program+"/"+state] = act
		}
	}
	return out, nil
}

func resolveState(sd stateDef, effects map[string]*Timeline) (Action, error) {
	switch {
	case (sd.Color == "") == (sd.Effect == ""):
		return Action{}, fmt.Errorf("%w: need exactly one of color, effect", ErrInvalid)
	case sd.Color != "":
		c, err := parseColor(sd.Color)
		if err != nil {
			return Action{}, err
		}
		return Action{Color: &c}, nil
	default:
		tl, ok := effects[sd.Effect]
		if !ok {
			return Action{}, fmt.Errorf("%w: unknown effect %q", ErrInvalid, sd.Effect)
		}
		return Action{Timeline: tl}, nil
	}
}

// readYAMLDir returns name -> content for every *.yaml file in dir (name =
// file name without .yaml). A missing dir yields an empty map.
func readYAMLDir(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("effects: read %s: %w", dir, err)
	}
	out := make(map[string][]byte)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		if !namePattern.MatchString(name) {
			return nil, fmt.Errorf("%s: %w: file name must match [a-z0-9_-]+.yaml", filepath.Join(dir, e.Name()), ErrInvalid)
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 -- operator-supplied config dir, not untrusted network input
		if err != nil {
			return nil, fmt.Errorf("effects: read %s: %w", e.Name(), err)
		}
		out[name] = data
	}
	return out, nil
}
```

Create `examples/config/effects/timer5min.yaml`:

```yaml
# Claude Code prompt-cache timer: green for 3 minutes, then increasingly
# urgent green/red alternation, ending solid red when the 5-minute cache
# TTL has expired.
stages:
  - duration: 3m
    color: "#00ff00"
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.8 }
  - duration: 1m
    primitive: alternate
    settings: { color_a: "#00ff00", color_b: "#ff0000", frequency_hz: 1, duty_cycle: 0.2 }
final_state: "#ff0000"
```

Create `examples/config/effects/breathe_blue.yaml`:

```yaml
# Open-ended: breathes until superseded by the next command on the key.
stages:
  - primitive: breathe
    settings: { color: blue, frequency_hz: 0.5, duty_cycle: 0.5 }
```

Create `examples/config/templates/claude.yaml`:

```yaml
# Claude Code states, addressed as claude/<state>.
working:
  effect: breathe_blue
idle:
  effect: timer5min
waiting:
  color: "#ffa500"
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test ./internal/effects/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/effects examples
nice -n 10 git commit -m "Load effects and templates from YAML, with examples

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Effects — `Engine`

**Files:**
- Create: `internal/effects/engine.go`, `internal/effects/engine_test.go`

**Interfaces:**
- Consumes: `Timeline` (Task 6), `keyaddr.Address`, `color.HSV`; `libFrom` test helper (defined in `compile_test.go`, Task 6).
- Produces:
  - `type Target struct { Device string; Addr keyaddr.Address }` (comparable)
  - `type Setter interface { Write(device string, addr keyaddr.Address, c color.HSV) error }`, satisfied by `*dispatcher.Dispatcher`
  - `NewEngine(out Setter, logger *slog.Logger) *Engine`
  - `(*Engine).SetColor(t Target, c color.HSV) error`: writes, then cancels any running effect on `t`
  - `(*Engine).Start(t Target, tl *Timeline, now time.Time) error`: writes frame 0, then registers the effect, replacing any running one. A write error is returned and nothing is registered.
  - `(*Engine).Tick(now time.Time)`, `(*Engine).Run(ctx, interval time.Duration)`, `const TickInterval = 200 * time.Millisecond`

- [ ] **Step 1: Write the failing tests**

Create `internal/effects/engine_test.go`:

```go
package effects

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

type write struct {
	t Target
	c color.HSV
}

type fakeSetter struct {
	writes []write
	err    error
}

func (f *fakeSetter) Write(device string, addr keyaddr.Address, c color.HSV) error {
	if f.err != nil {
		return f.err
	}
	f.writes = append(f.writes, write{Target{device, addr}, c})
	return nil
}

var key = Target{Device: "a", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 0}}

func newTestEngine() (*Engine, *fakeSetter) {
	out := &fakeSetter{}
	return NewEngine(out, slog.New(slog.NewTextHandler(io.Discard, nil))), out
}

func engineLib(t *testing.T) *Library {
	return libFrom(t, map[string]string{
		"two": `
stages:
  - { duration: 1s, color: red }
  - { duration: 1s, color: "#00ff00" }
final_state: blue
`,
		"solid": "stages:\n  - { color: white }\n",
	})
}

func TestStartWritesFrameZeroThenTicksChanges(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	if err := e.Start(key, mustEffect(t, engineLib(t), "two"), t0); err != nil {
		t.Fatal(err)
	}
	e.Tick(t0.Add(200 * time.Millisecond)) // still red: no write
	e.Tick(t0.Add(1200 * time.Millisecond))
	if len(out.writes) != 2 || out.writes[0].c != red || out.writes[1].c != pureGreen {
		t.Errorf("writes = %+v, want [red, green]", out.writes)
	}
}

func TestNaturalEndWritesFinalAndStops(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	_ = e.Start(key, mustEffect(t, engineLib(t), "two"), t0)
	e.Tick(t0.Add(2 * time.Second))
	n := len(out.writes)
	if out.writes[n-1].c != (color.HSV{H: 170, S: 255, V: 255}) {
		t.Errorf("last write = %+v, want final blue", out.writes[n-1])
	}
	e.Tick(t0.Add(3 * time.Second))
	if len(out.writes) != n {
		t.Error("finished effect kept writing")
	}
}

func TestSetColorCancelsEffect(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	_ = e.Start(key, mustEffect(t, engineLib(t), "two"), t0)
	if err := e.SetColor(key, black); err != nil {
		t.Fatal(err)
	}
	n := len(out.writes)
	e.Tick(t0.Add(1500 * time.Millisecond))
	e.Tick(t0.Add(5 * time.Second)) // past natural end: no final_state either
	if len(out.writes) != n {
		t.Errorf("writes after SetColor: %+v", out.writes[n:])
	}
}

func TestStartSupersedesWithoutFinalState(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	lib := engineLib(t)
	_ = e.Start(key, mustEffect(t, lib, "two"), t0)
	_ = e.Start(key, mustEffect(t, lib, "solid"), t0.Add(500*time.Millisecond))
	e.Tick(t0.Add(5 * time.Second))
	for _, w := range out.writes {
		if w.c == (color.HSV{H: 170, S: 255, V: 255}) {
			t.Errorf("superseded effect wrote its final_state: %+v", out.writes)
		}
	}
	if last := out.writes[len(out.writes)-1].c; last != (color.HSV{S: 0, V: 255}) {
		t.Errorf("last write = %+v, want solid white", last)
	}
}

func TestTargetsAreIndependent(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	other := Target{Device: "a", Addr: keyaddr.Address{Kind: keyaddr.LED, N: 1}}
	lib := engineLib(t)
	_ = e.Start(key, mustEffect(t, lib, "two"), t0)
	_ = e.SetColor(other, black) // must not cancel key's effect
	e.Tick(t0.Add(1200 * time.Millisecond))
	if last := out.writes[len(out.writes)-1]; last.t != key || last.c != pureGreen {
		t.Errorf("last write = %+v, want key -> green", last)
	}
}

func TestStartWriteErrorNotRegistered(t *testing.T) {
	e, out := newTestEngine()
	out.err = errors.New("boom")
	if err := e.Start(key, mustEffect(t, engineLib(t), "two"), time.Unix(1000, 0)); err == nil {
		t.Fatal("Start: want error")
	}
	out.err = nil
	e.Tick(time.Unix(1001, 500))
	if len(out.writes) != 0 {
		t.Errorf("unregistered effect wrote: %+v", out.writes)
	}
}

func TestTickWriteErrorDropsEffect(t *testing.T) {
	e, out := newTestEngine()
	t0 := time.Unix(1000, 0)
	_ = e.Start(key, mustEffect(t, engineLib(t), "two"), t0)
	out.err = errors.New("gone")
	e.Tick(t0.Add(1200 * time.Millisecond))
	out.err = nil
	e.Tick(t0.Add(5 * time.Second))
	if len(out.writes) != 1 {
		t.Errorf("writes = %+v, want only Start's frame", out.writes)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./internal/effects/`
Expected: FAIL to compile (`undefined: NewEngine`).

- [ ] **Step 3: Implement**

Create `internal/effects/engine.go`:

```go
package effects

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// TickInterval is the engine's frame rate: 5 fps.
const TickInterval = 200 * time.Millisecond

// Target is one key effects run on. Addr is canonical (led:N) whenever the
// device's capabilities are known — see dispatcher.Canonical.
type Target struct {
	Device string
	Addr   keyaddr.Address
}

// Setter is the frame-buffer write the engine drives; *dispatcher.Dispatcher
// implements it. Write must not block.
type Setter interface {
	Write(device string, addr keyaddr.Address, c color.HSV) error
}

type running struct {
	tl    *Timeline
	start time.Time
	last  color.HSV
}

// Engine owns every running effect, keyed by target, and is the only write
// path the HTTP layer uses — so "the new command always wins" is enforced
// in one place. The mutex is held across Setter.Write calls, which is fine
// because Write never blocks.
type Engine struct {
	mu      sync.Mutex
	out     Setter
	running map[Target]*running
	logger  *slog.Logger
}

// NewEngine creates an Engine writing through out.
func NewEngine(out Setter, logger *slog.Logger) *Engine {
	return &Engine{out: out, running: make(map[Target]*running), logger: logger}
}

// SetColor writes c to t and cancels any effect running there (without its
// final_state — the new color replaces it immediately).
func (e *Engine) SetColor(t Target, c color.HSV) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.out.Write(t.Device, t.Addr, c); err != nil {
		return err
	}
	delete(e.running, t)
	return nil
}

// Start writes tl's first frame to t and runs tl there from now on,
// replacing any effect already running on t without its final_state.
func (e *Engine) Start(t Target, tl *Timeline, now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, _ := tl.At(0)
	if err := e.out.Write(t.Device, t.Addr, c); err != nil {
		return err
	}
	e.running[t] = &running{tl: tl, start: now, last: c}
	return nil
}

// Tick advances every running effect to now: writes each frame that
// changed since the last one emitted, writes final_state for effects that
// ended and removes them, and drops (with a warning) any effect whose
// write fails.
func (e *Engine) Tick(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for t, r := range e.running {
		c, done := r.tl.At(now.Sub(r.start))
		if done || c != r.last {
			if err := e.out.Write(t.Device, t.Addr, c); err != nil {
				e.logger.Warn("effect write failed; stopping effect", "device", t.Device, "addr", t.Addr.String(), "err", err)
				delete(e.running, t)
				continue
			}
			r.last = c
		}
		if done {
			delete(e.running, t)
		}
	}
}

// Run calls Tick every interval until ctx is canceled.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			e.Tick(now)
		case <-ctx.Done():
			return
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test -race ./internal/effects/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/effects
nice -n 10 git commit -m "Add effects engine with supersession and 5fps tick

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Config — strict decode, `devices:`, config dir, optional file

**Files:**
- Modify: `config/config.go`, `config/config_test.go`

**Interfaces:**
- Produces:
  - `config.Config.Devices []config.DeviceDecl`; `DeviceDecl{ID string yaml:"id"; Optional bool yaml:"optional"}`
  - `config.Dir(getenv func(string) string, home string) string`: `$BLINKENKEYS_CONFIG_DIR`, else `${XDG_CONFIG_HOME:-home/.config}/blinkenkeys`
  - `config.LoadDir(dir string) (*Config, error)`: `dir/config.yaml`; a missing file gives `&Config{}`
  - `config.Load(path)` changes:
    - decoding is now strict;
    - `listeners.socket.path` is no longer required;
    - `devices` ids must be non-empty, unique, and not all digits.

- [ ] **Step 1: Write the failing tests**

In `config/config_test.go`, replace `TestLoadMissingSocketPathRejected` with the
following, and append the rest:

```go
func TestLoadMissingSocketPathAllowed(t *testing.T) {
	cfg, err := Load(writeConfig(t, `listeners: {}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listeners.Socket.Path != "" {
		t.Errorf("socket path = %q, want empty (caller applies default)", cfg.Listeners.Socket.Path)
	}
}

func TestLoadDevices(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
devices:
  - id: uid-0123456789abcdef
    optional: true
  - id: 5754-c401
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []DeviceDecl{{ID: "uid-0123456789abcdef", Optional: true}, {ID: "5754-c401"}}
	if !reflect.DeepEqual(cfg.Devices, want) {
		t.Errorf("Devices = %+v, want %+v", cfg.Devices, want)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct{ name, yaml string }{
		{"unknown top-level key", "listners: {}\n"},
		{"unknown device key", "devices:\n  - { id: a, optinal: true }\n"},
		{"empty device id", "devices:\n  - { optional: true }\n"},
		{"all-digit device id", "devices:\n  - { id: \"0\" }\n"},
		{"duplicate device id", "devices:\n  - { id: a }\n  - { id: a }\n"},
	}
	for _, tt := range tests {
		if _, err := Load(writeConfig(t, tt.yaml)); err == nil {
			t.Errorf("%s: Load succeeded, want error", tt.name)
		}
	}
}

func TestLoadDirMissingFileGivesDefaults(t *testing.T) {
	cfg, err := LoadDir(t.TempDir())
	if err != nil || cfg == nil || len(cfg.Devices) != 0 {
		t.Errorf("LoadDir(empty) = %+v, %v", cfg, err)
	}
}

func TestLoadDirReadsConfigYAML(t *testing.T) {
	dir := filepath.Dir(writeConfig(t, "devices:\n  - { id: a, optional: true }\n"))
	cfg, err := LoadDir(dir)
	if err != nil || len(cfg.Devices) != 1 {
		t.Errorf("LoadDir = %+v, %v", cfg, err)
	}
}

func TestDir(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"BLINKENKEYS_CONFIG_DIR": "/etc/bk", "XDG_CONFIG_HOME": "/x"}, "/etc/bk"},
		{map[string]string{"XDG_CONFIG_HOME": "/x"}, "/x/blinkenkeys"},
		{map[string]string{}, "/home/u/.config/blinkenkeys"},
	}
	for _, tt := range tests {
		if got := Dir(env(tt.env), "/home/u"); got != tt.want {
			t.Errorf("Dir(%v) = %q, want %q", tt.env, got, tt.want)
		}
	}
}
```

(add `"reflect"` to the test imports).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./config/`
Expected: FAIL to compile (`undefined: DeviceDecl`, `undefined: LoadDir`, `undefined: Dir`).

- [ ] **Step 3: Implement**

In `config/config.go`:

- update the package doc to: "Package config loads blinkenkeysd's optional on-disk
  configuration (`<config dir>/config.yaml`, see Dir), read once at startup."
- add the field and type:

```go
type Config struct {
	Naming    NamingRule   `yaml:"naming"`
	Listeners Listeners    `yaml:"listeners"`
	Devices   []DeviceDecl `yaml:"devices"`
}

// DeviceDecl is one devices: entry. ID is the device name blinkenkeysd
// assigns (hid.BaseName); Optional pre-declares the device so writes to it
// succeed before it's first seen, and exempts it from untethered eviction.
type DeviceDecl struct {
	ID       string `yaml:"id"`
	Optional bool   `yaml:"optional"`
}
```

- change `SocketListener`'s field comment to `// "" = default (~/.local/state/blinkenkeys/api.sock); "~/" is expanded`
- replace `Load` and add `LoadDir`/`Dir` (add `"errors"`, `"io/fs"`, `"path/filepath"`
  to imports):

```go
// Dir returns blinkenkeysd's config directory: $BLINKENKEYS_CONFIG_DIR if
// set, else ${XDG_CONFIG_HOME:-home/.config}/blinkenkeys. It holds
// config.yaml, effects/, and templates/.
func Dir(getenv func(string) string, home string) string {
	if d := getenv("BLINKENKEYS_CONFIG_DIR"); d != "" {
		return d
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "blinkenkeys")
}

// LoadDir loads dir/config.yaml; a missing file means all defaults.
func LoadDir(dir string) (*Config, error) {
	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	return cfg, err
}

// Load reads and validates a config.yaml. Unknown keys are errors.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is an operator-supplied config location, not untrusted network input
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.UnmarshalWithOptions(data, &cfg, yaml.DisallowUnknownField()); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if cfg.Listeners.TCP != nil && cfg.Listeners.TCP.Token == "" {
		return nil, fmt.Errorf("config: listeners.tcp.token is required whenever listeners.tcp is set")
	}
	seen := make(map[string]bool, len(cfg.Devices))
	for i, d := range cfg.Devices {
		switch {
		case d.ID == "":
			return nil, fmt.Errorf("config: devices[%d].id is required", i)
		case strings.Trim(d.ID, "0123456789") == "":
			return nil, fmt.Errorf("config: devices[%d].id %q is all digits, which is ambiguous with a device ordinal", i, d.ID)
		case seen[d.ID]:
			return nil, fmt.Errorf("config: devices[%d].id %q is duplicated", i, d.ID)
		}
		seen[d.ID] = true
	}
	return &cfg, nil
}
```

(add `"strings"` to imports). `Load` wraps `os.ReadFile`'s error with `%w`, so
`errors.Is(err, fs.ErrNotExist)` works in `LoadDir`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test ./config/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config
nice -n 10 git commit -m "Make config.yaml optional and strict, add devices: section

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: `main` — config wiring, socket path, declared devices, `-check-config`

**Files:**
- Modify: `cmd/blinkenkeysd/main.go`, `cmd/blinkenkeysd/main_test.go`

**Interfaces:**
- Consumes: `config.Dir`, `config.LoadDir` (Task 9), `effects.Load` (Task 7), `Registry.Declare` (Task 2).
- Produces:
  - `loadAll(dir string) (*config.Config, *effects.Library, error)`: used by both startup and `-check-config`. The error names which part failed.
  - `resolveSocketPath(env, configured, home string) string`: env, then config (with `~/` expanded), then the default.
  - Flag: `-check-config` validates, prints `ok` or the error, and exits 0 or 1 without touching HID or the socket.
  - `socketPathFromEnv` is deleted.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/blinkenkeysd/main_test.go` (add `"os"`, `"path/filepath"`):

```go
func TestResolveSocketPath(t *testing.T) {
	tests := []struct{ env, configured, want string }{
		{"/run/env.sock", "/cfg.sock", "/run/env.sock"},
		{"", "/cfg.sock", "/cfg.sock"},
		{"", "~/bk.sock", "/home/u/bk.sock"},
		{"", "", "/home/u/.local/state/blinkenkeys/api.sock"},
	}
	for _, tt := range tests {
		if got := resolveSocketPath(tt.env, tt.configured, "/home/u"); got != tt.want {
			t.Errorf("resolveSocketPath(%q, %q) = %q, want %q", tt.env, tt.configured, got, tt.want)
		}
	}
}

func TestLoadAllExamples(t *testing.T) {
	if _, _, err := loadAll(filepath.Join("..", "..", "examples", "config")); err != nil {
		t.Errorf("loadAll(examples/config): %v", err)
	}
}

func TestLoadAllReportsBadEffect(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "effects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "effects", "bad.yaml"), []byte("stages: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := loadAll(dir)
	if err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("loadAll err = %v, want one naming bad.yaml", err)
	}
}

func TestLoadAllReportsBadConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("bogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadAll(dir); err == nil {
		t.Error("loadAll: want error for unknown config key")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nice -n 10 go test ./cmd/...`
Expected: FAIL to compile (`undefined: resolveSocketPath`, `undefined: loadAll`).

- [ ] **Step 3: Implement**

In `cmd/blinkenkeysd/main.go` (add `"flag"`, `"fmt"`, `"strings"`, `config`, and
`effects` imports):

- at the top of `main`, after `logger := ...` and `warnIfRootFallback(...)`, before
  `goHid.Init()`:

```go
	checkOnly := flag.Bool("check-config", false, "validate config.yaml, effects/ and templates/ in the config dir, then exit")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	cfgDir := config.Dir(os.Getenv, home)
	cfg, _, err := loadAll(cfgDir)
	if *checkOnly {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}
	if err != nil {
		logger.Error("config load failed", "dir", cfgDir, "err", err)
		os.Exit(1)
	}
```

  (the `_` becomes `lib` in Task 11.) Move `warnIfRootFallback` below the check-only
  exit if you prefer quiet output; keep it before HID init either way.
- after `registry := dispatcher.NewRegistry()`:

```go
	for _, d := range cfg.Devices {
		if d.Optional {
			registry.Declare(d.ID)
		}
	}
```

- replace `socketPath := socketPathFromEnv()` with
  `socketPath := resolveSocketPath(os.Getenv("BLINKENKEYS_SOCKET"), cfg.Listeners.Socket.Path, home)`.
  Watch for `err` redeclaration: `listener, err := net.Listen(...)` becomes
  `listener, err = ...` if `err` is already declared in scope. `go vet` will tell you.
- delete `socketPathFromEnv`, and add:

```go
// loadAll loads config.yaml and the effects/templates library from dir —
// everything that must be valid before the daemon starts. -check-config runs
// exactly this.
func loadAll(dir string) (*config.Config, *effects.Library, error) {
	cfg, err := config.LoadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	lib, err := effects.Load(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("effects: %w", err)
	}
	return cfg, lib, nil
}

// resolveSocketPath applies the socket path precedence: BLINKENKEYS_SOCKET
// env > config.yaml (with ~/ expanded) > ~/.local/state/blinkenkeys/api.sock.
func resolveSocketPath(env, configured, home string) string {
	switch {
	case env != "":
		return env
	case strings.HasPrefix(configured, "~/"):
		return filepath.Join(home, configured[2:])
	case configured != "":
		return configured
	default:
		return filepath.Join(home, ".local", "state", "blinkenkeys", "api.sock")
	}
}
```

- [ ] **Step 4: Run the tests and build to verify they pass**

Run: `nice -n 10 go test ./cmd/... && nice -n 10 make build && ./bin/blinkenkeysd -check-config; echo exit=$?`
Expected: tests PASS; `-check-config` prints `ok`, exit=0 (no config dir → defaults).
Then: `BLINKENKEYS_CONFIG_DIR=examples/config ./bin/blinkenkeysd -check-config` → `ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/blinkenkeysd
nice -n 10 git commit -m "Wire config, declared devices and -check-config into blinkenkeysd

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: API rewrite — body forms, ordinals, engine as the write path

**Files:**
- Rewrite: `internal/api/handlers.go`, `internal/api/handlers_test.go`, `internal/api/handlers_list_test.go`
- Delete: `internal/api/caps.go`, `internal/api/caps_test.go`
- Modify: `internal/dispatcher/dispatcher.go` (delete the `SetKey` adapter), `cmd/blinkenkeysd/main.go`

**Interfaces:**
- Consumes: `(*Dispatcher).ResolveDevice`, `Canonical`, `ListDevices`, `GetCapabilities` (Task 4); `effects.Engine`, `Target`, `Library`, `Action` (Tasks 7–8); `keyaddr.Parse`.
- Produces:
  - `api.NewHandler(disp api.Dispatcher, w api.Writer, lib api.Library) *api.Handler`
  - interfaces `api.Dispatcher`, `api.Writer` (satisfied by `*effects.Engine`), and `api.Library` (satisfied by `*effects.Library`)
  - Routes unchanged: `PUT /devices/{name}/keys/{pos}`, `GET /devices`, `GET /devices/{name}`

Status mapping (spec §1 table), checked in this order:
1. device (404);
2. `{pos}` (400 malformed, 501 name, 404 off matrix);
3. body (400);
4. effect/state (404 unknown, 400 invalid);
5. write (503 otherwise).

For GET: ErrCapsUnknown and a full queue are 503.

- [ ] **Step 1: Write the failing tests**

Replace `internal/api/handlers_test.go` entirely:

```go
package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// fakeDispatcher, fakeWriter and fakeLibrary are reused by every test file
// in this package.
type fakeDispatcher struct {
	devices    map[string]string // ref (name or ordinal) -> name
	canonErr   error
	listResult []dispatcher.DeviceSummary
	listErr    error
	caps       dispatcher.Capabilities
	capsErr    error
}

func (f *fakeDispatcher) ResolveDevice(ref string) (string, bool) {
	name, ok := f.devices[ref]
	return name, ok
}

func (f *fakeDispatcher) Canonical(_ string, a keyaddr.Address) (keyaddr.Address, error) {
	if f.canonErr != nil {
		return keyaddr.Address{}, f.canonErr
	}
	return a, nil
}

func (f *fakeDispatcher) ListDevices(context.Context) ([]dispatcher.DeviceSummary, error) {
	return f.listResult, f.listErr
}

func (f *fakeDispatcher) GetCapabilities(context.Context, string) (dispatcher.Capabilities, error) {
	return f.caps, f.capsErr
}

type fakeWriter struct {
	colors []effects.Target
	gotHSV []color.HSV
	starts []effects.Target
	gotTL  []*effects.Timeline
	err    error
}

func (f *fakeWriter) SetColor(t effects.Target, c color.HSV) error {
	f.colors, f.gotHSV = append(f.colors, t), append(f.gotHSV, c)
	return f.err
}

func (f *fakeWriter) Start(t effects.Target, tl *effects.Timeline, _ time.Time) error {
	f.starts, f.gotTL = append(f.starts, t), append(f.gotTL, tl)
	return f.err
}

type fakeLibrary struct {
	tl       *effects.Timeline
	effErr   error
	gotName  string
	action   effects.Action
	stateErr error
}

func (f *fakeLibrary) Effect(name string) (*effects.Timeline, error) {
	f.gotName = name
	return f.tl, f.effErr
}

func (f *fakeLibrary) State(string) (effects.Action, error) { return f.action, f.stateErr }

func knownPad() *fakeDispatcher {
	return &fakeDispatcher{devices: map[string]string{"uid-01": "uid-01", "0": "uid-01"}}
}

func put(t *testing.T, h *Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, path, strings.NewReader(body)))
	return rec
}

func TestPutColorViaOrdinal(t *testing.T) {
	w := &fakeWriter{}
	h := NewHandler(knownPad(), w, &fakeLibrary{})
	rec := put(t, h, "/devices/0/keys/2,2", `{"color":"#ff0000"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	want := effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.RowCol, Row: 2, Col: 2}}
	if len(w.colors) != 1 || w.colors[0] != want || w.gotHSV[0] != (color.HSV{H: 0, S: 255, V: 255}) {
		t.Errorf("SetColor calls = %+v %+v", w.colors, w.gotHSV)
	}
}

func TestPutEffect(t *testing.T) {
	w := &fakeWriter{}
	lib := &fakeLibrary{tl: &effects.Timeline{}}
	h := NewHandler(knownPad(), w, lib)
	rec := put(t, h, "/devices/uid-01/keys/led:3", `{"effect":"timer5min"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	if len(w.starts) != 1 || w.gotTL[0] != lib.tl || lib.gotName != "timer5min" {
		t.Errorf("Start calls = %+v, effect name %q", w.starts, lib.gotName)
	}
}

func TestPutStateColorAndEffect(t *testing.T) {
	orange := color.HSV{H: 28, S: 255, V: 255}
	w := &fakeWriter{}
	h := NewHandler(knownPad(), w, &fakeLibrary{action: effects.Action{Color: &orange}})
	if rec := put(t, h, "/devices/0/keys/0,0", `{"state":"claude/waiting"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("color state: status = %d", rec.Code)
	}
	if len(w.gotHSV) != 1 || w.gotHSV[0] != orange {
		t.Errorf("SetColor = %+v", w.gotHSV)
	}

	tl := &effects.Timeline{}
	w = &fakeWriter{}
	h = NewHandler(knownPad(), w, &fakeLibrary{action: effects.Action{Timeline: tl}})
	if rec := put(t, h, "/devices/0/keys/0,0", `{"state":"claude/idle"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("effect state: status = %d", rec.Code)
	}
	if len(w.gotTL) != 1 || w.gotTL[0] != tl {
		t.Errorf("Start = %+v", w.gotTL)
	}
}

func TestPutStatusTable(t *testing.T) {
	tests := []struct {
		name string
		disp *fakeDispatcher
		lib  *fakeLibrary
		werr error
		path string
		body string
		want int
	}{
		{"unknown device", knownPad(), &fakeLibrary{}, nil, "/devices/nope/keys/0,0", `{"color":"red"}`, 404},
		{"ordinal out of range", knownPad(), &fakeLibrary{}, nil, "/devices/5/keys/0,0", `{"color":"red"}`, 404},
		{"malformed pos", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/1,x", `{"color":"red"}`, 400},
		{"named key", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/esc", `{"color":"red"}`, 501},
		{"key off matrix", &fakeDispatcher{devices: map[string]string{"0": "a"}, canonErr: dispatcher.ErrKeyNotFound}, &fakeLibrary{}, nil, "/devices/0/keys/9,9", `{"color":"red"}`, 404},
		{"malformed json", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{`, 400},
		{"unknown body field", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"colour":"red"}`, 400},
		{"empty body object", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{}`, 400},
		{"two of three", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"color":"red","state":"a/b"}`, 400},
		{"params (not in phase 3)", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"effect":"e","params":{"x":1}}`, 400},
		{"bad color", knownPad(), &fakeLibrary{}, nil, "/devices/0/keys/0,0", `{"color":"not-a-color"}`, 400},
		{"unknown effect", knownPad(), &fakeLibrary{effErr: effects.ErrUnknownEffect}, nil, "/devices/0/keys/0,0", `{"effect":"nope"}`, 404},
		{"unknown state", knownPad(), &fakeLibrary{stateErr: effects.ErrUnknownState}, nil, "/devices/0/keys/0,0", `{"state":"a/b"}`, 404},
		{"write failure", knownPad(), &fakeLibrary{}, errors.New("boom"), "/devices/0/keys/0,0", `{"color":"red"}`, 503},
	}
	for _, tt := range tests {
		h := NewHandler(tt.disp, &fakeWriter{err: tt.werr}, tt.lib)
		if rec := put(t, h, tt.path, tt.body); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d; body %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
}

func TestUnknownEffectBodyListsKnown(t *testing.T) {
	lib := &fakeLibrary{effErr: fmt.Errorf("%w %q; known: breathe_blue, timer5min", effects.ErrUnknownEffect, "x")}
	rec := put(t, NewHandler(knownPad(), &fakeWriter{}, lib), "/devices/0/keys/0,0", `{"effect":"x"}`)
	if !strings.Contains(rec.Body.String(), "timer5min") {
		t.Errorf("body %s does not list known effects", rec.Body)
	}
}
```

(add `"fmt"` to the imports, and put `"context"` in the stdlib group sorted, which
`gofmt`/`goimports` will do for you).

Replace `internal/api/handlers_list_test.go` entirely:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seefood/blinkenkeys/internal/dispatcher"
)

func get(h *Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestListDevices(t *testing.T) {
	disp := &fakeDispatcher{listResult: []dispatcher.DeviceSummary{{Name: "uid-01", Connected: true}}}
	rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}), "/devices")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var result []dispatcher.DeviceSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || len(result) != 1 || result[0].Name != "uid-01" {
		t.Errorf("result = %+v, %v", result, err)
	}
}

func TestListDevicesQueueFull(t *testing.T) {
	disp := &fakeDispatcher{listErr: dispatcher.ErrQueueFull}
	if rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}), "/devices"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestGetCapabilitiesByOrdinal(t *testing.T) {
	disp := knownPad()
	disp.caps = dispatcher.Capabilities{LEDCount: 12}
	rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}), "/devices/0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var caps dispatcher.Capabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil || caps.LEDCount != 12 {
		t.Errorf("caps = %+v, %v", caps, err)
	}
}

func TestGetCapabilitiesStatuses(t *testing.T) {
	tests := []struct {
		name string
		disp *fakeDispatcher
		path string
		want int
	}{
		{"unknown device", knownPad(), "/devices/nope", 404},
		{"caps never fetched", &fakeDispatcher{devices: map[string]string{"0": "a"}, capsErr: dispatcher.ErrCapsUnknown}, "/devices/0", 503},
		{"queue full", &fakeDispatcher{devices: map[string]string{"0": "a"}, capsErr: dispatcher.ErrQueueFull}, "/devices/0", 503},
	}
	for _, tt := range tests {
		if rec := get(NewHandler(tt.disp, &fakeWriter{}, &fakeLibrary{}), tt.path); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, rec.Code, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `git rm internal/api/caps.go internal/api/caps_test.go && nice -n 10 go test ./internal/api/`
Expected: FAIL to compile (`too many arguments in call to NewHandler`, ...).

- [ ] **Step 3: Implement**

Replace `internal/api/handlers.go` entirely:

```go
// Package api implements blinkenkeysd's HTTP surface: the PUT/GET routes from
// the design specs, backed directly by an in-process dispatcher and effects
// engine (no RPC layer — single binary, single process).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// Dispatcher is the subset of *dispatcher.Dispatcher the handlers need.
type Dispatcher interface {
	ResolveDevice(ref string) (string, bool)
	Canonical(device string, addr keyaddr.Address) (keyaddr.Address, error)
	ListDevices(ctx context.Context) ([]dispatcher.DeviceSummary, error)
	GetCapabilities(ctx context.Context, device string) (dispatcher.Capabilities, error)
}

// Writer is the effects engine: the only write path, so supersession is
// enforced in one place. *effects.Engine implements it.
type Writer interface {
	SetColor(t effects.Target, c color.HSV) error
	Start(t effects.Target, tl *effects.Timeline, now time.Time) error
}

// Library resolves effect and template-state requests. *effects.Library
// implements it.
type Library interface {
	Effect(name string) (*effects.Timeline, error)
	State(ref string) (effects.Action, error)
}

// Handler holds blinkenkeysd's HTTP dependencies and builds its route table.
type Handler struct {
	disp Dispatcher
	w    Writer
	lib  Library
}

// NewHandler constructs a Handler.
func NewHandler(disp Dispatcher, w Writer, lib Library) *Handler {
	return &Handler{disp: disp, w: w, lib: lib}
}

// Routes builds blinkenkeysd's route table (Go 1.22+ ServeMux method+wildcard
// patterns — no external router dependency needed).
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /devices/{name}/keys/{pos}", h.writeKey)
	mux.HandleFunc("GET /devices", h.listDevices)
	mux.HandleFunc("GET /devices/{name}", h.getCapabilities)
	return mux
}

// errBadRequest marks request-content errors that aren't already typed by
// the package that detected them.
var errBadRequest = errors.New("api: bad request")

// writeBody is the PUT body: exactly one of Color, Effect, State.
type writeBody struct {
	Color  *string `json:"color"`
	Effect *string `json:"effect"`
	State  *string `json:"state"`
}

func (h *Handler) writeKey(w http.ResponseWriter, r *http.Request) {
	device, ok := h.disp.ResolveDevice(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("%w %q", dispatcher.ErrDeviceNotFound, r.PathValue("name")))
		return
	}
	addr, err := keyaddr.Parse(r.PathValue("pos"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if addr.Kind == keyaddr.Name {
		writeError(w, http.StatusNotImplemented, fmt.Errorf("api: named keys (%q) are not implemented yet; use R,C, led:N or idx:N", addr.Name))
		return
	}
	addr, err = h.disp.Canonical(device, addr)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	body, err := decodeWriteBody(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.apply(effects.Target{Device: device, Addr: addr}, body); err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeWriteBody(r io.Reader) (writeBody, error) {
	var b writeBody
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("%w: invalid request body: %v", errBadRequest, err)
	}
	n := 0
	for _, p := range []*string{b.Color, b.Effect, b.State} {
		if p != nil {
			n++
		}
	}
	if n != 1 {
		return b, fmt.Errorf("%w: body needs exactly one of color, effect, state", errBadRequest)
	}
	return b, nil
}

func (h *Handler) apply(t effects.Target, b writeBody) error {
	switch {
	case b.Color != nil:
		c, err := color.ParseHSV(*b.Color)
		if err != nil {
			return fmt.Errorf("%w: %v", errBadRequest, err)
		}
		return h.w.SetColor(t, c)
	case b.Effect != nil:
		tl, err := h.lib.Effect(*b.Effect)
		if err != nil {
			return err
		}
		return h.w.Start(t, tl, time.Now())
	default:
		act, err := h.lib.State(*b.State)
		if err != nil {
			return err
		}
		if act.Color != nil {
			return h.w.SetColor(t, *act.Color)
		}
		return h.w.Start(t, act.Timeline, time.Now())
	}
}

// statusFor maps typed errors to the Phase 3 spec's status table; anything
// untyped (a full queue, a HID error, a canceled context) is a 503.
func statusFor(err error) int {
	switch {
	case errors.Is(err, dispatcher.ErrDeviceNotFound),
		errors.Is(err, dispatcher.ErrKeyNotFound),
		errors.Is(err, effects.ErrUnknownEffect),
		errors.Is(err, effects.ErrUnknownState):
		return http.StatusNotFound
	case errors.Is(err, dispatcher.ErrNamedKeyUnsupported):
		return http.StatusNotImplemented
	case errors.Is(err, errBadRequest),
		errors.Is(err, keyaddr.ErrInvalid):
		return http.StatusBadRequest
	default:
		return http.StatusServiceUnavailable
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	result, err := h.disp.ListDevices(r.Context())
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request) {
	device, ok := h.disp.ResolveDevice(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("%w %q", dispatcher.ErrDeviceNotFound, r.PathValue("name")))
		return
	}
	caps, err := h.disp.GetCapabilities(r.Context(), device)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, caps)
}
```

In `internal/dispatcher/dispatcher.go`, delete the temporary `SetKey` adapter.

In `cmd/blinkenkeysd/main.go`:

- change `cfg, _, err := loadAll(cfgDir)` to `cfg, lib, err := loadAll(cfgDir)`
- replace

```go
	caps := api.NewCapabilitiesCache(disp)
	handler := api.NewHandler(disp, caps)
```

  with

```go
	engine := effects.NewEngine(disp, logger)
	go engine.Run(ctx, effects.TickInterval)
	handler := api.NewHandler(disp, engine, lib)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nice -n 10 go test -race ./... && nice -n 10 go vet ./...`
Expected: PASS, no vet findings. `grep -rn "SetKey(\|CapabilitiesCache\|RedrawReconnected" --include=*.go .`
must return nothing.

- [ ] **Step 5: Commit**

```bash
git add -A internal/api internal/dispatcher cmd/blinkenkeysd
nice -n 10 git commit -m "Route key writes through the effects engine; add effect and state bodies

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Documentation

**Files:**
- Modify: `README.md` (Status paragraph + Roadmap items 3, 4, 6), `CHANGELOG.md`, `CLAUDE.md` (dispatcher bullet)
- Create: `docs/superpowers/manual-checks/phase3-effects.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: README**

Replace the Status paragraph's second sentence onward with:

```markdown
Language/implementation: **Go**. Specs:
[Phases 1–2](docs/superpowers/specs/2026-09-21-blinkenkeys-phase1-2-design.md) (POC → MVP)
and [Phase 3](docs/superpowers/specs/2026-09-24-blinkenkeys-phase3-effects-templates-design.md)
(effects, templates, server-owned timers). Phase 5 is roadmap only; see
`CHANGELOG.md` for design revisions.
```

Replace roadmap items 3, 4 and 6 (keep their numbers so "Phase 5" keeps its number):

```markdown
3. **Effects, abstraction templates, server-owned timers** — Go-coded animation
   primitives (breathe, blink, two-color alternation) composed into named
   multi-stage effects in `~/.config/blinkenkeys/effects/*.yaml`, and templates in
   `~/.config/blinkenkeys/templates/*.yaml` mapping semantic states
   (`claude/idle`, mic-mute, build-status, ...) to colors or effects. A
   multi-stage effect with timed stages *is* a server-owned timer: a Claude Code
   hook says "went idle" once, and the key animates toward "cache about to
   expire" over the next 5 minutes entirely server-side. See
   `examples/config/`.
4. *(Folded into Phase 3.)* Radius-based "explosion" / multi-key effects are on
   hold.
```

and

```markdown
6. *(Folded into Phase 3 — see item 3.)*
```

- [ ] **Step 2: CHANGELOG**

Append to `CHANGELOG.md`:

```markdown

## Phase 3 design (`docs/superpowers/specs/2026-09-24-blinkenkeys-phase3-effects-templates-design.md`)

- **2026-09-24**: Supersedes two Phase 1+2 behaviors. (1) The color cache is now a
  frame buffer written unconditionally on every accepted write, with hardware
  delivery best-effort and decoupled from the HTTP response — Phase 1+2 updated
  the cache only after a successful hardware write. (2) Error table: "named
  device currently disconnected → 503" is removed for `PUT`; a known (seen or
  config-declared) device returns 204 whether connected or not, and only a device
  with no registry slot returns 404. Capabilities now live on the registry slot
  and survive disconnects, so `GET /devices/{name}` answers for untethered
  devices too (503 only if capabilities were never fetched).
- **2026-09-24**: Planning-review revisions to the Phase 3 spec itself: `duration`
  Go duration strings instead of `duration_ms`; `final_state` optional on
  open-ended effects; an open-ended nested effect is allowed as the last stage;
  no effect parameters (deferred to a much later phase: every value in an effect
  file is literal); primitive settings are all required, under the stage key
  `settings`; strict `config.yaml` decoding; `0xFF` LEDs excluded from
  `R,C`/`idx:`; `ConnectedWithoutCaps` drives the capabilities retry (`Added` only
  feeds the first-seen log hint); single-job redraw; `-check-config`; unknown
  device/effect/state 404s list the known names; `duration: 3m` strings.
  Accepted known gap: an effect started on a pre-declared device before it first
  connects keeps its literal-address target, so a later command on the same key in another form doesn't supersede it.
```

- [ ] **Step 3: CLAUDE.md**

Replace the "All access to the open HID handles goes through one dispatcher goroutine"
bullet with:

```markdown
- **All access to the open HID handles goes through one dispatcher goroutine** —
  the "traffic cop" that batches flushes into `SetKeys` calls (VialRGB's packet
  ceiling is 9 LEDs per report). The color cache is a frame buffer: `Dispatcher.Write`
  updates it synchronously and queues a colorless flush that reads the cache at
  dispatch time; periodic/reconnect redraw replays it (colors live in the
  keyboard's RAM only and don't survive a reset). HTTP writes go through
  `effects.Engine`, never straight to the dispatcher, so effect supersession is
  enforced in one place. Don't let other goroutines call a `hid.Controller`
  directly.
```

- [ ] **Step 4: Manual check**

Create `docs/superpowers/manual-checks/phase3-effects.md`:

````markdown
# Phase 3 effects/templates hardware check

Manual, gated on physical hardware — not run in CI. Run after any change touching
`internal/effects`, `internal/dispatcher`, `internal/api`, or `cmd/blinkenkeysd`.

Prerequisites: as in `phase1-2-hardware-roundtrip.md` (`cxt_studio/12e4` attached,
`personal/vialrgb-direct/001-enable` firmware). No other process may hold the HID
device — stop any running `blinkenkeysd` first.

Shorthand used below:

```bash
SOCK=~/.local/state/blinkenkeys/api.sock
bk() { curl -s -w '%{http_code}\n' --unix-socket "$SOCK" -X PUT -d "$2" "http://localhost/devices/0/keys/$1"; }
```

1. `make build`, then
   `BLINKENKEYS_CONFIG_DIR=examples/config ./bin/blinkenkeysd -check-config` → `ok`.
2. Start `BLINKENKEYS_CONFIG_DIR=examples/config ./bin/blinkenkeysd`. Confirm a
   "new device seen" log line naming the board, with a `devices:` snippet.
3. **Timer stages:** `bk 0,0 '{"state":"claude/idle"}'` → `204`. Key is green for
   3 minutes, then alternates mostly-green, then mostly-red after minute 4, then
   solid red at 5:00.
4. **Supersession:** while it runs, `bk 0,0 '{"state":"claude/working"}'` → key
   breathes blue immediately, with no red flash from the old timer's final state.
5. **Addressing:** `bk led:0 '{"color":"#ffffff"}'` and `bk idx:0 '{"color":"#ff00ff"}'`
   → 204 and visible; `bk esc '{"color":"red"}'` → `501`; `bk 9,9 '{"color":"red"}'` → `404`.
6. **Unplug mid-timer:** start `claude/idle`, unplug before 3:00, replug after
   4:00. Within ≤5 s the key shows the *current* stage (alternating), not green.
7. **Pre-declared device:** stop the daemon, put the logged snippet from step 2 into
   `examples/config`-copy `config.yaml` (use a temp copy, e.g.
   `cp -r examples/config /tmp/bkcfg`), unplug the board, and start with
   `BLINKENKEYS_CONFIG_DIR=/tmp/bkcfg`. `bk 0,0 '{"color":"#00ffff"}'` → `204`;
   `GET /devices/0` → `503`. Plug in → key turns cyan within ~1 s.
````

- [ ] **Step 5: Commit**

```bash
git add README.md CHANGELOG.md CLAUDE.md docs/superpowers/manual-checks/phase3-effects.md
nice -n 10 git commit -m "Document phase 3 in README, CHANGELOG, CLAUDE.md and manual checks

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Integration verification

**Files:** none unless a check fails.

- [ ] **Step 1: Full build, tests, lint**

Run:

```bash
nice -n 10 make build && nice -n 10 go test -race ./... && nice -n 10 make lint
```

Expected: all pass. If lint fails, fix the finding in the relevant file and commit it
separately ("Fix lint findings in ..."). Never disable a linter or add `//nolint`
without asking.

- [ ] **Step 2: Confirm no leftovers**

Run: `grep -rn "duration_ms\|RedrawReconnected\|CapabilitiesCache\|socketPathFromEnv\|opSetKey" --include=*.go --include=*.yaml . ; git status --short`
Expected: no matches; clean tree.

- [ ] **Step 3: Hardware smoke test (gated — needs the user)**

The user's machine may have a `blinkenkeysd` running that holds the HID device
exclusively. **Ask the user before stopping it.** Then run
`docs/superpowers/manual-checks/phase3-effects.md` steps 1–5 at minimum, and report
exactly which steps ran and what was observed. Steps 6–7 need physical unplugging;
leave them to the user if they're not present.
