# blincli Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `blincli`, a GNU-style client for `blinkenkeysd`'s HTTP API that finds the daemon, derives the right key from the calling terminal, and can read a key's state back — plus the daemon changes (key layout config, owner tags, status records, read endpoints) that needs.

**Architecture:** The daemon gains a per-device key layout (`tabs`/`pool` lists in `config.yaml`), a status record per key kept by `effects.Engine` (the only write path), an optional `owner` tag on writes, conditional `DELETE`, and read-only `GET /devices/{name}/keys[/{pos}]`. `blincli` is a pure-Go (no cgo, never imports `internal/hid`/`dispatcher`) client in `cmd/blincli` + `internal/termid` (terminal → tab number/instance id) + `internal/client` (endpoint resolution, HTTP, key planning). Stdlib `flag` only.

**Tech Stack:** Go 1.27.1, stdlib `flag`/`net/http`, `github.com/goccy/go-yaml` (already a dependency). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-05-blincli-design.md` (read it first; this plan implements it).

## Global Constraints

- Go 1.27.1 (`go.mod`); **no new module dependencies** (ask before adding any).
- `blincli` must build with `CGO_ENABLED=0`: nothing under `cmd/blincli`, `internal/client`, `internal/termid` may import `internal/hid`, `internal/dispatcher`, `internal/effects`, or `internal/api` (they pull in cgo `go-hid`). `config` and `internal/keyaddr` are fine.
- `blincli` never prompts except under explicit `config init --interactive`/`-i`, and that exits 64 when stdin is not a TTY.
- Exit codes (sysexits; **never 2** — Claude Code treats hook exit 2 as blocking on `Stop`/`UserPromptSubmit`, verified in the hooks docs): `0` ok, `1` daemon error, `64` usage / no key or device determinable, `66` key not registered (`get`), `69` daemon unreachable, `77` auth missing/rejected, `78` no config/endpoint.
- Option style: GNU — every option has a `--long` form, common ones also a one-letter form; dual registration with stdlib `flag` (one `FlagSet` per subcommand; no bundled short flags; flags precede positionals).
- Colors are QMK native HSV (0–255 per channel); the shared parser is `internal/color`.
- Tab-slot default modulus is **6** (`idx:0..5`) when the daemon reports no layout; named-claim fallback names must not contain `,` or `/`.
- A token is required for `http(s)://` endpoints, optional for the Unix socket.
- Daemon rules from `CLAUDE.md`: one process; all HID access through the dispatcher goroutine; HTTP writes go through `effects.Engine`, never straight to the dispatcher; no global/shared mutable state outside the existing mutex-guarded types.
- Quality gate for every task: `make test` green, `make lint` clean (`prek run --all-files`), `mcp__ide__getDiagnostics` clean on touched files, commit with the `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>` trailer. Never `--no-verify`; if a hook fails, stop and ask.
- When running `go build`/`go test` with many packages on this machine, run under `nice` (e.g. `nice make test`).

## Verified ground truth (checked 2026-10-05 against docs and this machine)

- Claude Code hooks: exit code 2 blocks on `Stop` and `UserPromptSubmit`; other non-zero codes are non-blocking errors.
- iTerm2: `ITERM_SESSION_ID` is `w<window>t<tab>p<pane>:<UUID>`, numbers zero-based (docs/search summary). The variable is set when the shell starts and never updates, so after tab reorder/close it is **stale** (accepted, documented limitation).
- WezTerm: `wezterm cli list --format json` returns an array of objects with `window_id`, `tab_id`, `pane_id` (tested on this machine, `WEZTERM_PANE=1` ↔ `pane_id` 1). There is **no tab-position field**; tab number = rank of the pane's `tab_id` among that window's distinct `tab_id`s in list order (list order = tab order is **unverified** — manual check in Task 15).
- tmux: `tmux display-message -p -t "$TMUX_PANE" '#{window_index}'` and `tmux show-options -gv base-index` both work (tested locally: `1`, `0`).
- kitty: `KITTY_WINDOW_ID` is a window id, not a tab number; kitty was not running here, so its env var is **unverified** and it is used only as an instance id.
- Other session-id env vars (`ZELLIJ_PANE_ID`, `STY`, `WT_SESSION`, `TERM_SESSION_ID`) are **unverified** best-effort fallbacks; `CLAUDE_CODE_SESSION_ID` is verified (used by `integrations/claude/hooks-basic.json`).

## Review Focus

Failure modes the spec implies that the per-feature tests would not otherwise cover; each has a pinning test in the named task.

1. `pool: []` (no pool) vs `pool` omitted (default pool) must survive YAML decoding — both are valid and mean different things. (Task 1)
2. `GET` on a never-claimed key name must not create a claim as a side effect (the existing `Canonical` path does). (Tasks 3, 6)
3. A conditional `clear` whose owner no longer matches must leave the other session's key lit and still exit 0. (Task 5, Task 12)
4. A terminal helper that is missing, hangs, or prints garbage (`wezterm cli`, `tmux` with no server) must degrade to the named fallback, never hang the hook. (Task 8)
5. A remote URL with no token must exit 77 *before* any request is sent, and a token must never appear in `-v` output or `config show`. (Tasks 9, 11, 14)
6. A flag parse error must exit 64, never Go's default 2. (Task 11)

## File Structure

**Create**
- `config/keylist.go`, `config/keylist_test.go` — `KeyList` YAML type (`[0-4, 6, 8-10]` / `"0-4,6"`), `KeyLayout`.
- `internal/dispatcher/layout.go`, `internal/dispatcher/layout_test.go` — `Layout`, registry layout storage, layout-aware pool.
- `internal/dispatcher/lookup.go`, `internal/dispatcher/lookup_test.go` — read-only `LookupClaim`, `KeyInfo`, dispatcher `Lookup`/`KeyInfo`/`CurrentColor`/`Layout`/`Connected`.
- `internal/effects/status.go`, `internal/effects/status_test.go` — `Origin`, `Status`, engine records.
- `internal/api/keyview.go`, `internal/api/keyview_test.go` — JSON view of a key + `GET` handlers.
- `internal/termid/termid.go`, `internal/termid/termid_test.go`
- `internal/client/config.go`, `resolve.go`, `http.go`, `key.go` + `_test.go` each
- `cmd/blincli/main.go`, `flags.go`, `cmd_set.go`, `cmd_get.go`, `cmd_config.go`, `render.go` + tests
- `integrations/claude/hooks-blincli.json`
- `docs/superpowers/manual-checks/blincli.md`

**Modify**
- `config/config.go` (`DeviceDecl.Keys`, validation), `internal/dispatcher/registry.go`, `internal/dispatcher/claims.go`, `internal/dispatcher/dispatcher.go`
- `internal/effects/engine.go`, `internal/effects/compile.go` (`Timeline.Name`), `internal/color/color.go` (`HSV.Hex`)
- `internal/api/handlers.go`, `internal/api/handlers_test.go`
- `cmd/blinkenkeysd/main.go`, `cmd/blinkenkeysd/main_test.go`
- `examples/config/config.yaml`, `examples/config/README.md`, `integrations/claude/README.md`, `README.md`, `CHANGELOG.md`, `CLAUDE.md`, `Makefile`
- `packaging/linux/install.sh`, `packaging/linux/uninstall.sh`, `packaging/macos/install.sh`, `packaging/macos/uninstall.sh`

---

## Phase A — daemon

### Task 1: Key-list syntax and per-device `keys:` config

**Files:**
- Create: `config/keylist.go`
- Test: `config/keylist_test.go`
- Modify: `config/config.go` (`DeviceDecl`, validation in `Load`)

**Interfaces:**
- Produces: `config.KeyList` (`[]uint16`, YAML-decodable from `[0-4, 6, 8-10]` or `"0-4,6,8-10"`; an explicit empty list decodes to a **non-nil** empty slice, an omitted key stays nil); `config.KeyLayout{Tabs, Pool KeyList}`; `DeviceDecl.Keys *KeyLayout` (`yaml:"keys"`).

- [ ] **Step 1: Write the failing tests** — `config/keylist_test.go`:

```go
package config

import (
	"reflect"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestKeyListDecodes(t *testing.T) {
	tests := []struct {
		name, src string
		want      KeyList
	}{
		{"flow list with ranges", "k: [0-4, 6, 8-10]", KeyList{0, 1, 2, 3, 4, 6, 8, 9, 10}},
		{"single range in list", "k: [0-5]", KeyList{0, 1, 2, 3, 4, 5}},
		{"plain ints", "k: [3, 1, 2]", KeyList{3, 1, 2}},
		{"string form", `k: "0-4,6,8-10"`, KeyList{0, 1, 2, 3, 4, 6, 8, 9, 10}},
		{"order preserved", `k: "5,1-2"`, KeyList{5, 1, 2}},
		{"explicit empty list", "k: []", KeyList{}},
		{"empty string", `k: ""`, KeyList{}},
	}
	for _, tt := range tests {
		var got struct {
			K KeyList `yaml:"k"`
		}
		if err := yaml.Unmarshal([]byte(tt.src), &got); err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got.K == nil || !reflect.DeepEqual(got.K, tt.want) {
			t.Errorf("%s: got %#v, want %#v (non-nil)", tt.name, got.K, tt.want)
		}
	}
}

func TestKeyListOmittedStaysNil(t *testing.T) {
	var got struct {
		K KeyList `yaml:"k"`
	}
	if err := yaml.Unmarshal([]byte("other: 1"), &got); err == nil && got.K != nil {
		t.Errorf("omitted key decoded to %#v, want nil", got.K)
	}
}

func TestKeyListRejects(t *testing.T) {
	for _, src := range []string{`k: "5-2"`, `k: "x"`, `k: "-1"`, `k: [a]`, `k: "70000"`, "k: {a: 1}", "k: [1.5]"} {
		var got struct {
			K KeyList `yaml:"k"`
		}
		if err := yaml.Unmarshal([]byte(src), &got); err == nil {
			t.Errorf("%s: want error, got %#v", src, got.K)
		}
	}
}

func TestLoadKeyLayout(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
devices:
  - id: macropad
    keys:
      tabs: [0-5]
      pool: [6-11]
  - id: other
    keys:
      tabs: [0-1]
      pool: []
  - id: plain
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d := cfg.Devices
	if !reflect.DeepEqual(d[0].Keys.Tabs, KeyList{0, 1, 2, 3, 4, 5}) || !reflect.DeepEqual(d[0].Keys.Pool, KeyList{6, 7, 8, 9, 10, 11}) {
		t.Errorf("macropad keys = %+v", d[0].Keys)
	}
	if d[1].Keys.Pool == nil || len(d[1].Keys.Pool) != 0 {
		t.Errorf("pool: [] must decode non-nil empty, got %#v", d[1].Keys.Pool)
	}
	if d[2].Keys != nil {
		t.Errorf("device without keys: must have nil Keys, got %+v", d[2].Keys)
	}
	cfg2, err := Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      tabs: [0-1]\n"))
	if err != nil || cfg2.Devices[0].Keys.Pool != nil {
		t.Errorf("omitted pool must stay nil (default pool): %+v, %v", cfg2.Devices[0].Keys, err)
	}
}

func TestLoadKeyLayoutRejectsOverlap(t *testing.T) {
	for _, body := range []string{
		"tabs: [0-5]\n      pool: [5-6]",
		"tabs: [1, 1]",
		"pool: [2-3, 3]",
	} {
		if _, err := Load(writeConfig(t, "devices:\n  - id: a\n    keys:\n      "+body+"\n")); err == nil {
			t.Errorf("%q: want error", body)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `nice go test ./config/ -run 'KeyList|KeyLayout' -v`
Expected: FAIL (`undefined: KeyList`).

- [ ] **Step 3: Implement** — `config/keylist.go`:

```go
package config

import (
	"fmt"
	"strconv"
	"strings"
)

// KeyList is an ordered list of idx: (reading-order) key indexes. In YAML it
// is either a list whose items are integers or ranges ("[0-4, 6, 8-10]") or
// one string ("0-4,6,8-10"). Order is preserved as written. A key present
// but empty decodes to a non-nil empty KeyList; an omitted key stays nil, so
// callers can tell "no keys" from "unset".
type KeyList []uint16

// UnmarshalYAML implements goccy/go-yaml's InterfaceUnmarshaler.
func (k *KeyList) UnmarshalYAML(unmarshal func(any) error) error {
	var raw any
	if err := unmarshal(&raw); err != nil {
		return err
	}
	list, err := parseKeyList(raw)
	if err != nil {
		return err
	}
	*k = list
	return nil
}

func parseKeyList(raw any) (KeyList, error) {
	out := KeyList{}
	switch v := raw.(type) {
	case nil:
		return out, nil
	case string:
		return parseKeyString(v)
	case []any:
		for _, el := range v {
			switch e := el.(type) {
			case string:
				part, err := parseKeyString(e)
				if err != nil {
					return nil, err
				}
				out = append(out, part...)
			case uint64:
				if e > 0xFFFF {
					return nil, fmt.Errorf("config: key index %d out of range", e)
				}
				out = append(out, uint16(e))
			case int64:
				if e < 0 || e > 0xFFFF {
					return nil, fmt.Errorf("config: key index %d out of range", e)
				}
				out = append(out, uint16(e))
			default:
				return nil, fmt.Errorf("config: key list element %v (%T): want an index or a range like 0-4", el, el)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("config: key list must be a list or a string like \"0-4,6\", got %T", raw)
	}
}

func parseKeyString(s string) (KeyList, error) {
	out := KeyList{}
	if strings.TrimSpace(s) == "" {
		return out, nil
	}
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		a, err := strconv.ParseUint(strings.TrimSpace(lo), 10, 16)
		if err != nil {
			return nil, fmt.Errorf("config: key list %q: bad index %q", s, lo)
		}
		b := a
		if isRange {
			if b, err = strconv.ParseUint(strings.TrimSpace(hi), 10, 16); err != nil {
				return nil, fmt.Errorf("config: key list %q: bad index %q", s, hi)
			}
			if b < a {
				return nil, fmt.Errorf("config: key list %q: range %d-%d runs backwards", s, a, b)
			}
		}
		for n := a; n <= b; n++ {
			out = append(out, uint16(n))
		}
	}
	return out, nil
}
```

In `config/config.go`, change `DeviceDecl` and add the layout type and validation:

```go
type DeviceDecl struct {
	ID       string     `yaml:"id"`
	Optional bool       `yaml:"optional"`
	Keys     *KeyLayout `yaml:"keys,omitempty"`
}

// KeyLayout assigns roles to a device's keys, by idx: (reading-order) index.
// Tabs are the slots a terminal's tab number maps onto, in order (tab N ->
// Tabs[(N-1) mod len]). Pool is where the daemon auto-assigns named claims
// from; nil (omitted) means "the default pool" (every key with row >= 1 not
// in Tabs), an explicit empty list means no pool. Keys in neither list are
// never touched by tab slots or the pool.
type KeyLayout struct {
	Tabs KeyList `yaml:"tabs"`
	Pool KeyList `yaml:"pool"`
}

func (l *KeyLayout) validate() error {
	owner := make(map[uint16]string)
	for _, role := range []struct {
		name string
		keys KeyList
	}{{"tabs", l.Tabs}, {"pool", l.Pool}} {
		for _, k := range role.keys {
			if prev, dup := owner[k]; dup {
				if prev == role.name {
					return fmt.Errorf("idx %d listed twice in %s", k, role.name)
				}
				return fmt.Errorf("idx %d is in both %s and %s", k, prev, role.name)
			}
			owner[k] = role.name
		}
	}
	return nil
}
```

In `Load`, inside the `for i, d := range cfg.Devices` loop, after the existing `switch` and before `seen[d.ID] = true`, add:

```go
		if d.Keys != nil {
			if err := d.Keys.validate(); err != nil {
				return nil, fmt.Errorf("config: devices[%d].keys: %w", i, err)
			}
		}
```

- [ ] **Step 4: Run to verify pass**

Run: `nice go test ./config/ -v`
Expected: PASS. If `UnmarshalYAML(func(any) error)` is not picked up by goccy (the `[]` / ranges tests fail with a type error), switch to the `BytesUnmarshaler` form `UnmarshalYAML([]byte) error` that calls `yaml.Unmarshal(b, &raw)` and keep the rest identical.

- [ ] **Step 5: Commit**

```bash
git add config/keylist.go config/keylist_test.go config/config.go
git commit -m "Add per-device key layout (tabs/pool) with list/range syntax"
```

---

### Task 2: Dispatcher key layout and layout-aware claim pool

**Files:**
- Create: `internal/dispatcher/layout.go`, `internal/dispatcher/layout_test.go`
- Modify: `internal/dispatcher/registry.go` (`Registry.layouts`, `SetCaps` call), `internal/dispatcher/claims.go` (`ClaimOrGet`, `claimOrGetLocked`, `resolveLocked`, `nextUnclaimedLocked`)

**Interfaces:**
- Consumes: Task 1's shape (daemon wiring comes in Task 7; this task only needs the `Layout` type).
- Produces: `dispatcher.Layout{Tabs, Pool []uint16; DefaultPool bool}`, `dispatcher.DefaultLayout`, `(*Registry).SetLayout(device string, l Layout)`, `(*Registry).Layout(device string) Layout`. `nextUnclaimedLocked(s *slot, l Layout)`, `claimOrGetLocked(s *slot, l Layout, name string, now time.Time)`, `resolveLocked(s *slot, l Layout, addr keyaddr.Address, now time.Time)` (unexported; the only callers are in `claims.go` and `registry.go`'s `SetCaps`).

- [ ] **Step 1: Write the failing tests** — `internal/dispatcher/layout_test.go`:

```go
package dispatcher

import (
	"errors"
	"testing"
	"time"
)

// sixLEDPad has LED i at matrix (row i/2, col i%2): reading-order idx == LED index.
func sixLEDPad() *fakeController {
	return &fakeController{numLEDs: 6, positions: map[uint16][2]uint8{
		0: {0, 0}, 1: {0, 1}, 2: {1, 0}, 3: {1, 1}, 4: {2, 0}, 5: {2, 1},
	}}
}

func TestExplicitPoolIsUsedInListedOrder(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{5, 4}})
	for i, want := range []uint16{5, 4} {
		got, err := r.ClaimOrGet("a", string(rune('x'+i)), time.Now())
		if err != nil || got != want {
			t.Fatalf("claim %d = %d, %v; want %d", i, got, err, want)
		}
	}
	if _, err := r.ClaimOrGet("a", "z", time.Now()); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("exhausted pool: err = %v, want ErrNoUnclaimedKeys", err)
	}
}

func TestEmptyExplicitPoolNeverClaims(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Tabs: []uint16{0, 1}, Pool: []uint16{}})
	if _, err := r.ClaimOrGet("a", "x", time.Now()); !errors.Is(err, ErrNoUnclaimedKeys) {
		t.Errorf("err = %v, want ErrNoUnclaimedKeys", err)
	}
}

func TestDefaultPoolSkipsTabs(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	// default pool = row >= 1 keys: LEDs 2..5; tab slot idx 2 removes LED 2.
	r.SetLayout("a", Layout{Tabs: []uint16{2}, DefaultPool: true})
	got, err := r.ClaimOrGet("a", "x", time.Now())
	if err != nil || got != 3 {
		t.Errorf("claim = %d, %v; want LED 3 (LED 2 is a tab slot)", got, err)
	}
}

func TestPoolIndexesOffTheMatrixAreSkipped(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{99, 4}})
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 4 {
		t.Errorf("claim = %d, %v; want LED 4", got, err)
	}
}

func TestNoLayoutKeepsLegacyPool(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 2 {
		t.Errorf("claim = %d, %v; want LED 2 (first row>=1 key)", got, err)
	}
	if l := r.Layout("a"); !l.DefaultPool {
		t.Errorf("Layout with none set = %+v, want DefaultPool", l)
	}
}

func TestPoolKeyOwnedDirectlyIsSkipped(t *testing.T) {
	r := registryWithCaps(t, "a", sixLEDPad())
	r.SetLayout("a", Layout{Pool: []uint16{4, 5}})
	r.MarkDirect("a", 4)
	if got, err := r.ClaimOrGet("a", "x", time.Now()); err != nil || got != 5 {
		t.Errorf("claim = %d, %v; want LED 5 (4 is direct-owned)", got, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `nice go test ./internal/dispatcher/ -run 'Pool|Layout' -v`
Expected: FAIL (`r.SetLayout undefined`).

- [ ] **Step 3: Implement.**

`internal/dispatcher/layout.go`:

```go
package dispatcher

import "github.com/seefood/blinkenkeys/internal/keyaddr"

// Layout is a device's key roles, by idx: (reading-order) index — see
// config.KeyLayout. Tabs are the slots blincli maps a terminal's tab number
// onto; Pool is where named claims are auto-assigned from. DefaultPool means
// Pool is unset: use every key with row >= 1 that is not a tab slot (the
// pre-layout behavior).
type Layout struct {
	Tabs        []uint16
	Pool        []uint16
	DefaultPool bool
}

// DefaultLayout is what a device with no configured layout gets.
var DefaultLayout = Layout{DefaultPool: true}

// SetLayout records device's layout. It may be called before the device
// exists, so config can be applied ahead of enumeration.
func (r *Registry) SetLayout(device string, l Layout) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.layouts == nil {
		r.layouts = make(map[string]Layout)
	}
	r.layouts[device] = l
}

// Layout returns device's layout, or DefaultLayout if none was set.
func (r *Registry) Layout(device string) Layout {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.layoutLocked(device)
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
```

`internal/dispatcher/registry.go`: add the field to `Registry`:

```go
type Registry struct {
	mu      sync.Mutex
	slots   map[string]*slot
	layouts map[string]Layout // device name -> key roles; see SetLayout
}
```

and in `SetCaps` change `resolveLocked(s, w.Addr, now)` to `resolveLocked(s, r.layoutLocked(name), w.Addr, now)`.

`internal/dispatcher/claims.go`:
- `ClaimOrGet`: `return claimOrGetLocked(s, r.layoutLocked(device), name, now)`
- `func claimOrGetLocked(s *slot, l Layout, name string, now time.Time) (uint16, error)` — inside, `nextUnclaimedLocked(s, l)`.
- `func resolveLocked(s *slot, l Layout, addr keyaddr.Address, now time.Time) (uint16, error)` — inside, `claimOrGetLocked(s, l, addr.Name, now)`.
- Replace `nextUnclaimedLocked` with:

```go
// nextUnclaimedLocked returns the next key a new name may claim: with an
// explicit pool, the first listed idx: key that is on the matrix and not
// already owned; with the default pool, the lowest-index row >= 1 key (row/
// col order, excluding LEDs with no matrix key) that is neither owned nor a
// tab slot. Callers hold r.mu.
func nextUnclaimedLocked(s *slot, l Layout) (uint16, bool) {
	if !l.DefaultPool {
		for _, n := range l.Pool {
			led, ok := keyaddr.Resolve(keyaddr.Address{Kind: keyaddr.Idx, N: n}, s.caps.LEDCount, s.caps.Positions)
			if !ok {
				continue
			}
			if _, owned := s.owners[led]; owned {
				continue
			}
			return led, true
		}
		return 0, false
	}
	tabs := ledsOf(s, l.Tabs)
	positions := append([]LEDPosition(nil), s.caps.Positions...)
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].Row != positions[j].Row {
			return positions[i].Row < positions[j].Row
		}
		return positions[i].Col < positions[j].Col
	})
	for _, p := range positions {
		if p.Row == 0 || p.Row == noMatrixKeyRowCol || tabs[p.Index] {
			continue
		}
		if _, owned := s.owners[p.Index]; owned {
			continue
		}
		return p.Index, true
	}
	return 0, false
}
```

- [ ] **Step 4: Run the whole package**

Run: `nice go test ./internal/dispatcher/ -v 2>&1 | tail -20`
Expected: PASS (existing claims/registry tests unchanged — `DefaultLayout` preserves legacy behavior).

- [ ] **Step 5: Commit**

```bash
git add internal/dispatcher
git commit -m "Dispatcher: per-device layout restricts the named-claim pool"
```

---

### Task 3: Read-only dispatcher lookups

**Files:**
- Create: `internal/dispatcher/lookup.go`, `internal/dispatcher/lookup_test.go`

**Interfaces:**
- Consumes: Task 2's `Layout`, `Registry.layoutLocked`.
- Produces:
  - `(*Registry).LookupClaim(device, name string) (idx uint16, lastWrite time.Time, err error)` — `ErrDeviceNotFound` / `ErrClaimNotFound`; never claims, never refreshes the idle clock.
  - `type KeyInfo struct{ Name string; Direct bool; LastWrite time.Time; Row, Col uint8; HasPos bool }`, `(*Registry).KeyInfo(device string, idx uint16) KeyInfo`.
  - `(*Dispatcher).Lookup(ctx, device string, addr keyaddr.Address) (keyaddr.Address, error)` → `led:N`; **never marks direct, never claims**.
  - `(*Dispatcher).KeyInfo(device string, led uint16) KeyInfo`, `(*Dispatcher).CurrentColor(device string, led uint16) (color.HSV, bool)`, `(*Dispatcher).Layout(device string) Layout`, `(*Dispatcher).Connected(device string) bool`.

- [ ] **Step 1: Write the failing tests** — `internal/dispatcher/lookup_test.go`:

```go
package dispatcher

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func padDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	d := New(registryWithConnected("a", sixLEDPad()), NewCache(), 8, discardLogger())
	runDispatcher(t, d)
	if _, err := d.GetCapabilities(context.Background(), "a"); err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	return d
}

func TestLookupNameDoesNotClaim(t *testing.T) {
	d := padDispatcher(t)
	name := keyaddr.Address{Kind: keyaddr.Name, Name: "esc"}
	if _, err := d.Lookup(context.Background(), "a", name); !errors.Is(err, ErrClaimNotFound) {
		t.Fatalf("Lookup(unclaimed) err = %v, want ErrClaimNotFound", err)
	}
	if _, _, err := d.registry.LookupClaim("a", "esc"); !errors.Is(err, ErrClaimNotFound) {
		t.Errorf("Lookup created a claim (LookupClaim err = %v)", err)
	}
	idx, err := d.registry.ClaimOrGet("a", "esc", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.Lookup(context.Background(), "a", name)
	if err != nil || got != (keyaddr.Address{Kind: keyaddr.LED, N: idx}) {
		t.Errorf("Lookup(claimed) = %+v, %v; want led:%d", got, err, idx)
	}
}

func TestLookupDirectDoesNotMarkDirect(t *testing.T) {
	d := padDispatcher(t)
	got, err := d.Lookup(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Idx, N: 3})
	if err != nil || got != (keyaddr.Address{Kind: keyaddr.LED, N: 3}) {
		t.Fatalf("Lookup(idx:3) = %+v, %v", got, err)
	}
	if info := d.KeyInfo("a", 3); info.Direct {
		t.Error("Lookup marked the key direct-owned")
	}
	if _, err := d.Lookup(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Idx, N: 99}); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("off-matrix err = %v, want ErrKeyNotFound", err)
	}
}

func TestKeyInfoCurrentColorLayoutConnected(t *testing.T) {
	d := padDispatcher(t)
	if _, err := d.Canonical(context.Background(), "a", keyaddr.Address{Kind: keyaddr.Idx, N: 1}); err != nil {
		t.Fatal(err)
	}
	info := d.KeyInfo("a", 1)
	if !info.Direct || !info.HasPos || info.Row != 0 || info.Col != 1 {
		t.Errorf("KeyInfo = %+v", info)
	}
	if err := d.Write("a", keyaddr.Address{Kind: keyaddr.LED, N: 1}, color.HSV{H: 1, S: 2, V: 3}); err != nil {
		t.Fatal(err)
	}
	if c, ok := d.CurrentColor("a", 1); !ok || c != (color.HSV{H: 1, S: 2, V: 3}) {
		t.Errorf("CurrentColor = %+v, %v", c, ok)
	}
	if _, ok := d.CurrentColor("a", 4); ok {
		t.Error("CurrentColor for never-written LED reported ok")
	}
	if !d.Connected("a") || d.Connected("nope") {
		t.Error("Connected wrong")
	}
	if !d.Layout("a").DefaultPool {
		t.Error("Layout default expected")
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./internal/dispatcher/ -run 'Lookup|KeyInfo' -v` → FAIL (`d.Lookup undefined`).

- [ ] **Step 3: Implement** — `internal/dispatcher/lookup.go`:

```go
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
```

- [ ] **Step 4: Run** — `nice go test ./internal/dispatcher/ -v 2>&1 | tail -20` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dispatcher/lookup.go internal/dispatcher/lookup_test.go
git commit -m "Dispatcher: read-only key lookup, key info and current color"
```

---

### Task 4: Engine status records, `Timeline.Name`, `HSV.Hex`

**Files:**
- Create: `internal/effects/status.go`, `internal/effects/status_test.go`
- Modify: `internal/effects/engine.go`, `internal/effects/compile.go`, `internal/color/color.go`
- Test: `internal/color/color_test.go` (append)

**Interfaces:**
- Produces:
  - `effects.Origin{Type, Ref, Owner string}` (`Type` is `"color"`, `"effect"` or `"state"`).
  - `effects.Status{Origin Origin; SetAt time.Time; Effect *EffectStatus}`, `effects.EffectStatus{Name string; Running bool; Elapsed, Total time.Duration; Finite bool}`.
  - `(*Engine).SetColorFrom(t Target, c color.HSV, o Origin, now time.Time) error`, `(*Engine).StartFrom(t Target, tl *Timeline, now time.Time, o Origin) error`, `(*Engine).Status(t Target, now time.Time) (Status, bool)`, `(*Engine).Statuses(device string, now time.Time) map[Target]Status`.
  - Existing `SetColor`/`Start` keep their signatures; they now **drop** the target's status record (internal blanking/tests, no origin to record).
  - `Timeline.Name string` (the effect's name; shared timelines keep one name).
  - `color.HSV.Hex() string` (`"#rrggbb"`).

- [ ] **Step 1: Write the failing tests.**

Append to `internal/color/color_test.go`:

```go
func TestHSVHex(t *testing.T) {
	tests := []struct {
		in   HSV
		want string
	}{
		{HSV{H: 0, S: 255, V: 255}, "#ff0000"},
		{HSV{H: 0, S: 0, V: 255}, "#ffffff"},
		{HSV{H: 123, S: 77, V: 0}, "#000000"},
		{HSV{H: 0, S: 0, V: 128}, "#808080"},
	}
	for _, tt := range tests {
		if got := tt.in.Hex(); got != tt.want {
			t.Errorf("%+v.Hex() = %q, want %q", tt.in, got, tt.want)
		}
	}
}
```

`internal/effects/status_test.go`:

```go
package effects

import (
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

type statusSetter struct{ writes int }

func (s *statusSetter) Write(string, keyaddr.Address, color.HSV) error { s.writes++; return nil }

func statusTimeline(t *testing.T, name, src string) *Timeline {
	t.Helper()
	tls, err := compileEffects(map[string][]byte{name: []byte(src)})
	if err != nil {
		t.Fatalf("compileEffects: %v", err)
	}
	return tls[name]
}

const tenSecondRed = "stages:\n  - color: red\n    duration: 10s\nfinal_state: \"#000000\"\n"

func tgt(dev string, n uint16) Target {
	return Target{Device: dev, Addr: keyaddr.Address{Kind: keyaddr.LED, N: n}}
}

func TestTimelineHasName(t *testing.T) {
	if tl := statusTimeline(t, "pulse", tenSecondRed); tl.Name != "pulse" {
		t.Errorf("Name = %q", tl.Name)
	}
}

func TestSetColorFromRecordsOrigin(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	t0 := time.Unix(1000, 0)
	o := Origin{Type: "color", Ref: "#ff0000", Owner: "laptop.iterm-ab12"}
	if err := e.SetColorFrom(tgt("a", 1), color.HSV{S: 255, V: 255}, o, t0); err != nil {
		t.Fatal(err)
	}
	st, ok := e.Status(tgt("a", 1), t0.Add(3*time.Second))
	if !ok || st.Origin != o || !st.SetAt.Equal(t0) || st.Effect != nil {
		t.Errorf("Status = %+v, %v", st, ok)
	}
	if _, ok := e.Status(tgt("a", 2), t0); ok {
		t.Error("status for never-written target")
	}
}

func TestStartFromTracksEffectProgress(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	tl := statusTimeline(t, "pulse", tenSecondRed)
	t0 := time.Unix(1000, 0)
	o := Origin{Type: "state", Ref: "claude/working"}
	if err := e.StartFrom(tgt("a", 1), tl, t0, o); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status(tgt("a", 1), t0.Add(4*time.Second))
	if st.Effect == nil || !st.Effect.Running || st.Effect.Name != "pulse" ||
		st.Effect.Elapsed != 4*time.Second || !st.Effect.Finite || st.Effect.Total != 10*time.Second {
		t.Errorf("running status = %+v", st.Effect)
	}
	e.Tick(t0.Add(11 * time.Second)) // finishes, writes final_state, drops running
	st, ok := e.Status(tgt("a", 1), t0.Add(12*time.Second))
	if !ok || st.Effect == nil || st.Effect.Running || st.Effect.Elapsed != 10*time.Second {
		t.Errorf("finished status = %+v, %v (record must outlive the effect)", st.Effect, ok)
	}
}

func TestPlainSetColorAndStartDropRecord(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	tl := statusTimeline(t, "pulse", tenSecondRed)
	t0 := time.Unix(1000, 0)
	_ = e.StartFrom(tgt("a", 1), tl, t0, Origin{Type: "effect", Ref: "pulse"})
	if err := e.SetColor(tgt("a", 1), color.HSV{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Status(tgt("a", 1), t0); ok {
		t.Error("SetColor must drop the record")
	}
	_ = e.SetColorFrom(tgt("a", 1), color.HSV{}, Origin{Type: "color", Ref: "x"}, t0)
	_ = e.Start(tgt("a", 1), tl, t0)
	if _, ok := e.Status(tgt("a", 1), t0); ok {
		t.Error("Start must drop the record")
	}
}

func TestSetColorFromCancelsRunningEffect(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	tl := statusTimeline(t, "pulse", tenSecondRed)
	t0 := time.Unix(1000, 0)
	_ = e.StartFrom(tgt("a", 1), tl, t0, Origin{Type: "effect", Ref: "pulse"})
	_ = e.SetColorFrom(tgt("a", 1), color.HSV{}, Origin{Type: "color", Ref: "black"}, t0.Add(time.Second))
	st, _ := e.Status(tgt("a", 1), t0.Add(2*time.Second))
	if st.Origin.Type != "color" || st.Effect != nil {
		t.Errorf("status after supersede = %+v", st)
	}
}

func TestStatusesFiltersByDevice(t *testing.T) {
	e := NewEngine(&statusSetter{}, nil)
	t0 := time.Unix(1000, 0)
	_ = e.SetColorFrom(tgt("a", 1), color.HSV{}, Origin{Type: "color", Ref: "x"}, t0)
	_ = e.SetColorFrom(tgt("a", 2), color.HSV{}, Origin{Type: "color", Ref: "y"}, t0)
	_ = e.SetColorFrom(tgt("b", 1), color.HSV{}, Origin{Type: "color", Ref: "z"}, t0)
	if got := e.Statuses("a", t0); len(got) != 2 {
		t.Errorf("Statuses(a) = %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./internal/color/ ./internal/effects/ -run 'Hex|Status|Timeline|SetColorFrom|StartFrom' -v` → FAIL (undefined).

- [ ] **Step 3: Implement.**

`internal/color/color.go` (append):

```go
// Hex renders c as "#rrggbb" using QMK's integer HSV->RGB conversion, so the
// result matches what the keyboard shows.
func (c HSV) Hex() string {
	r, g, b := c.rgb()
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

func (c HSV) rgb() (r, g, b uint8) {
	if c.S == 0 {
		return c.V, c.V, c.V
	}
	region := c.H / 43
	rem := (uint16(c.H) - uint16(region)*43) * 6
	v, s := uint16(c.V), uint16(c.S)
	p := uint8((v * (255 - s)) >> 8)
	q := uint8((v * (255 - ((s * rem) >> 8))) >> 8)
	t := uint8((v * (255 - ((s * (255 - rem)) >> 8))) >> 8)
	switch region {
	case 0:
		return c.V, t, p
	case 1:
		return q, c.V, p
	case 2:
		return p, c.V, t
	case 3:
		return p, q, c.V
	case 4:
		return t, p, c.V
	default:
		return c.V, p, q
	}
}
```
(add `"fmt"` to the file's imports if not already present).

`internal/effects/compile.go`: add `Name string` as the first field of `Timeline` (comment: "the effect's name; set by the compiler") and change `tl := &Timeline{}` in `compile` to `tl := &Timeline{Name: name}`.

`internal/effects/status.go`:

```go
package effects

import "time"

// Origin is what a write asked for. Type is "color", "effect" or "state";
// Ref is the requested color string, effect name or program/state; Owner is
// an optional caller-supplied tag (blincli sends its terminal identity) used
// for conditional clears.
type Origin struct {
	Type  string
	Ref   string
	Owner string
}

// record is the engine's memory of the last API write to a target.
type record struct {
	origin Origin
	setAt  time.Time
	tl     *Timeline // nil unless the write started an effect
	start  time.Time
}

// Status is a key's last request and, if it started an effect, progress.
type Status struct {
	Origin Origin
	SetAt  time.Time
	Effect *EffectStatus
}

// EffectStatus is an effect's progress. Total is meaningful only if Finite.
type EffectStatus struct {
	Name    string
	Running bool
	Elapsed time.Duration
	Total   time.Duration
	Finite  bool
}

// statusLocked builds t's Status from rec; callers hold e.mu.
func (e *Engine) statusLocked(t Target, rec *record, now time.Time) Status {
	st := Status{Origin: rec.origin, SetAt: rec.setAt}
	if rec.tl == nil {
		return st
	}
	total, finite := rec.tl.Total()
	es := &EffectStatus{Name: rec.tl.Name, Total: total, Finite: finite}
	if _, running := e.running[t]; running {
		es.Running = true
		if es.Elapsed = now.Sub(rec.start); es.Elapsed < 0 {
			es.Elapsed = 0
		}
	} else if finite {
		es.Elapsed = total
	}
	st.Effect = es
	return st
}

// Status returns t's last-write record, if any. A record outlives its effect
// (Running turns false) and is dropped by the next plain SetColor/Start.
func (e *Engine) Status(t Target, now time.Time) (Status, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rec, ok := e.records[t]
	if !ok {
		return Status{}, false
	}
	return e.statusLocked(t, rec, now), true
}

// Statuses returns every record for device.
func (e *Engine) Statuses(device string, now time.Time) map[Target]Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(map[Target]Status)
	for t, rec := range e.records {
		if t.Device == device {
			out[t] = e.statusLocked(t, rec, now)
		}
	}
	return out
}
```

`internal/effects/engine.go`: add `records map[Target]*record` to `Engine`; initialize in `NewEngine` (`records: make(map[Target]*record)`); then replace `SetColor` and `Start` with:

```go
// SetColor writes c to t and cancels any effect running there (without its
// final_state — the new color replaces it immediately). It records nothing:
// it is the internal blanking path, so it also drops t's status record.
func (e *Engine) SetColor(t Target, c color.HSV) error {
	return e.setColor(t, c, nil)
}

// SetColorFrom is SetColor that remembers o as t's last request.
func (e *Engine) SetColorFrom(t Target, c color.HSV, o Origin, now time.Time) error {
	return e.setColor(t, c, &record{origin: o, setAt: now})
}

func (e *Engine) setColor(t Target, c color.HSV, rec *record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.out.Write(t.Device, t.Addr, c); err != nil {
		return err
	}
	delete(e.running, t)
	e.putRecord(t, rec)
	return nil
}

// Start writes tl's first frame to t and runs tl there from now on,
// replacing any effect already running on t without its final_state. It
// records nothing (and drops t's status record); see StartFrom.
func (e *Engine) Start(t Target, tl *Timeline, now time.Time) error {
	return e.start(t, tl, now, nil)
}

// StartFrom is Start that remembers o as t's last request.
func (e *Engine) StartFrom(t Target, tl *Timeline, now time.Time, o Origin) error {
	return e.start(t, tl, now, &record{origin: o, setAt: now, tl: tl, start: now})
}

func (e *Engine) start(t Target, tl *Timeline, now time.Time, rec *record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, _ := tl.At(0)
	if err := e.out.Write(t.Device, t.Addr, c); err != nil {
		return err
	}
	e.running[t] = &running{tl: tl, start: now, last: c}
	e.putRecord(t, rec)
	return nil
}

// putRecord stores rec as t's record, or drops t's record if rec is nil.
// Callers hold e.mu.
func (e *Engine) putRecord(t Target, rec *record) {
	if rec == nil {
		delete(e.records, t)
		return
	}
	e.records[t] = rec
}
```

- [ ] **Step 4: Run** — `nice go test ./internal/color/ ./internal/effects/ -v 2>&1 | tail -30` → PASS (existing engine tests still pass).

- [ ] **Step 5: Commit**

```bash
git add internal/effects internal/color
git commit -m "Engine: per-key status records; Timeline.Name; HSV.Hex"
```

---

### Task 5: API write path — `owner` tag, conditional/extended `DELETE`

**Files:**
- Modify: `internal/api/handlers.go`, `internal/api/handlers_test.go`

**Interfaces:**
- Consumes: Task 3 `Dispatcher.Lookup`; Task 4 `SetColorFrom`, `StartFrom`, `Status`, `Origin`.
- Produces: `api.Dispatcher` gains `Lookup(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, error)`. `api.Writer` becomes:
  ```go
  type Writer interface {
  	SetColor(t effects.Target, c color.HSV) error
  	SetColorFrom(t effects.Target, c color.HSV, o effects.Origin, now time.Time) error
  	StartFrom(t effects.Target, tl *effects.Timeline, now time.Time, o effects.Origin) error
  	Status(t effects.Target, now time.Time) (effects.Status, bool)
  }
  ```
  PUT body gains optional `"owner": "<tag>"` (1–128 bytes). `DELETE /devices/{name}/keys/{pos}[?owner=TAG]`: accepts **any** `{pos}` form; blanks the key; releases the claim iff `{pos}` is a name; with `?owner=` it is a **no-op (204)** when the recorded owner differs.

- [ ] **Step 1: Update the fakes and write failing tests** in `internal/api/handlers_test.go`.

Replace `fakeWriter` and extend `fakeDispatcher`:

```go
// in fakeDispatcher: add fields
//	lookupErr  error
//	lookupAddr *keyaddr.Address // if set, Lookup returns this instead of echoing the address
func (f *fakeDispatcher) Lookup(_ context.Context, _ string, a keyaddr.Address) (keyaddr.Address, error) {
	if f.lookupErr != nil {
		return keyaddr.Address{}, f.lookupErr
	}
	if f.lookupAddr != nil {
		return *f.lookupAddr, nil
	}
	return a, nil
}

type fakeWriter struct {
	colors  []effects.Target
	gotHSV  []color.HSV
	starts  []effects.Target
	gotTL   []*effects.Timeline
	origins []effects.Origin // origin of each SetColorFrom/StartFrom call
	status  map[effects.Target]effects.Status
	err     error
}

func (f *fakeWriter) SetColor(t effects.Target, c color.HSV) error {
	f.colors, f.gotHSV = append(f.colors, t), append(f.gotHSV, c)
	return f.err
}

func (f *fakeWriter) SetColorFrom(t effects.Target, c color.HSV, o effects.Origin, _ time.Time) error {
	f.origins = append(f.origins, o)
	return f.SetColor(t, c)
}

func (f *fakeWriter) StartFrom(t effects.Target, tl *effects.Timeline, _ time.Time, o effects.Origin) error {
	f.origins = append(f.origins, o)
	f.starts, f.gotTL = append(f.starts, t), append(f.gotTL, tl)
	return f.err
}

func (f *fakeWriter) Status(t effects.Target, _ time.Time) (effects.Status, bool) {
	st, ok := f.status[t]
	return st, ok
}
```

Add tests:

```go
func TestPutRecordsOriginAndOwner(t *testing.T) {
	w := &fakeWriter{}
	h := NewHandler(knownPad(), w, &fakeLibrary{tl: &effects.Timeline{}}, nil)
	if rec := put(t, h, "/devices/0/keys/idx:1", `{"color":"red","owner":"laptop.iterm-ab12"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	if rec := put(t, h, "/devices/0/keys/idx:1", `{"effect":"timer5min"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	want := []effects.Origin{
		{Type: "color", Ref: "red", Owner: "laptop.iterm-ab12"},
		{Type: "effect", Ref: "timer5min"},
	}
	if len(w.origins) != 2 || w.origins[0] != want[0] || w.origins[1] != want[1] {
		t.Errorf("origins = %+v, want %+v", w.origins, want)
	}
}

func TestPutRejectsBadOwner(t *testing.T) {
	h := NewHandler(knownPad(), &fakeWriter{}, &fakeLibrary{}, nil)
	for _, body := range []string{`{"color":"red","owner":""}`, `{"color":"red","owner":"` + strings.Repeat("x", 129) + `"}`} {
		if rec := put(t, h, "/devices/0/keys/0,0", body); rec.Code != 400 {
			t.Errorf("%.40s: status = %d, want 400", body, rec.Code)
		}
	}
	// owner alone is not a write
	if rec := put(t, h, "/devices/0/keys/0,0", `{"owner":"x"}`); rec.Code != 400 {
		t.Errorf("owner-only body: status = %d, want 400", rec.Code)
	}
}

func TestDeleteDirectAddressBlanks(t *testing.T) {
	disp := knownPad()
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	disp.lookupAddr = &led
	w := &fakeWriter{}
	rec := del(t, NewHandler(disp, w, &fakeLibrary{}, nil), "/devices/0/keys/idx:3")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	want := effects.Target{Device: "uid-01", Addr: led}
	if len(w.colors) != 1 || w.colors[0] != want || w.gotHSV[0] != (color.HSV{}) {
		t.Errorf("SetColor = %+v %+v", w.colors, w.gotHSV)
	}
	if len(disp.released) != 0 {
		t.Errorf("direct key has no claim to release, got %v", disp.released)
	}
}

func TestDeleteOwnerMismatchIsNoOp(t *testing.T) {
	disp := knownPad()
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	disp.lookupAddr = &led
	target := effects.Target{Device: "uid-01", Addr: led}
	w := &fakeWriter{status: map[effects.Target]effects.Status{target: {Origin: effects.Origin{Owner: "other"}}}}
	h := NewHandler(disp, w, &fakeLibrary{}, nil)

	if rec := del(t, h, "/devices/0/keys/idx:3?owner=mine"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(w.colors) != 0 || len(disp.released) != 0 {
		t.Errorf("mismatched owner must not blank or release: %+v %v", w.colors, disp.released)
	}
	if rec := del(t, h, "/devices/0/keys/idx:3?owner=other"); rec.Code != http.StatusNoContent || len(w.colors) != 1 {
		t.Errorf("matching owner must blank: status %d, colors %+v", rec.Code, w.colors)
	}
	w.colors = nil
	if rec := del(t, h, "/devices/0/keys/idx:3"); rec.Code != http.StatusNoContent || len(w.colors) != 1 {
		t.Errorf("no owner param must blank unconditionally: status %d", rec.Code)
	}
}

func TestDeleteOwnerWithNoRecordProceeds(t *testing.T) {
	disp := knownPad()
	w := &fakeWriter{}
	if rec := del(t, NewHandler(disp, w, &fakeLibrary{}, nil), "/devices/0/keys/esc?owner=mine"); rec.Code != 204 || len(disp.released) != 1 {
		t.Errorf("status %d, released %v", rec.Code, disp.released)
	}
}
```

In the existing `TestDeleteStatusTable`, change the row `{"direct address not a name", ..., 400}` to `{"direct address now blanks", knownPad(), "/devices/0/keys/2,2", 204}` and add `{"unclaimed name", &fakeDispatcher{devices: map[string]string{"0": "a"}, lookupErr: dispatcher.ErrClaimNotFound}, "/devices/0/keys/esc", 404}`.

- [ ] **Step 2: Run to verify failure** — `nice go test ./internal/api/ -v 2>&1 | tail -20` → FAIL (compile errors: `Writer` methods, `Lookup`).

- [ ] **Step 3: Implement** in `internal/api/handlers.go`.

Interfaces (replace the existing `Dispatcher` and `Writer` definitions' relevant lines): add `Lookup(ctx context.Context, device string, addr keyaddr.Address) (keyaddr.Address, error)` to `Dispatcher`; replace `Writer` as in **Interfaces** above (drop `Start`).

`writeBody` and decoding:

```go
type writeBody struct {
	Color  *string `json:"color"`
	Effect *string `json:"effect"`
	State  *string `json:"state"`
	Owner  *string `json:"owner"` // optional tag recorded with the write; see releaseKey
}
```
In `decodeWriteBody`, keep counting only the three action fields and, before `return b, nil`:

```go
	if b.Owner != nil && (len(*b.Owner) == 0 || len(*b.Owner) > 128) {
		return b, fmt.Errorf("%w: owner must be 1-128 bytes", errBadRequest)
	}
```

`apply` takes `now` and records origins:

```go
func (h *Handler) apply(t effects.Target, b writeBody, now time.Time) error {
	owner := ""
	if b.Owner != nil {
		owner = *b.Owner
	}
	switch {
	case b.Color != nil:
		c, err := color.ParseHSV(*b.Color)
		if err != nil {
			return fmt.Errorf("%w: %v", errBadRequest, err)
		}
		return h.w.SetColorFrom(t, c, effects.Origin{Type: "color", Ref: *b.Color, Owner: owner}, now)
	case b.Effect != nil:
		tl, err := h.lib.Effect(*b.Effect)
		if err != nil {
			return err
		}
		return h.w.StartFrom(t, tl, now, effects.Origin{Type: "effect", Ref: *b.Effect, Owner: owner})
	default:
		act, err := h.lib.State(*b.State)
		if err != nil {
			return err
		}
		o := effects.Origin{Type: "state", Ref: *b.State, Owner: owner}
		if act.Color != nil {
			return h.w.SetColorFrom(t, *act.Color, o, now)
		}
		return h.w.StartFrom(t, act.Timeline, now, o)
	}
}
```
and in `writeKey` call `h.apply(effects.Target{Device: device, Addr: addr}, body, time.Now())`.

Replace `releaseKey` and its doc comment:

```go
// releaseKey blanks a key (cancelling any running effect) and, if {pos} is a
// name, frees its claim. Any {pos} form is accepted: a direct (R,C/led:/idx:)
// key has no claim, so it is only blanked. With ?owner=TAG the call is a
// no-op (204) when the key's recorded owner is someone else, so a session
// ending never blanks a key another session has since taken over; with no
// recorded owner, or no ?owner=, it proceeds.
//
// The key is resolved read-only (Lookup): resolving a name through Canonical
// would claim it first, then blank and release the claim it just made.
func (h *Handler) releaseKey(w http.ResponseWriter, r *http.Request) {
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
	led, err := h.disp.Lookup(r.Context(), device, addr)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	target := effects.Target{Device: device, Addr: led}
	if owner := r.URL.Query().Get("owner"); owner != "" {
		if st, ok := h.w.Status(target, time.Now()); ok && st.Origin.Owner != owner {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	// Blank before releasing: a still-running effect (e.g. timer5min) would
	// otherwise keep animating an LED nothing owns anymore.
	if err := h.w.SetColor(target, color.HSV{}); err != nil && h.logger != nil {
		h.logger.Warn("blank-on-release failed", "device", device, "key", addr.String(), "err", err)
	}
	if addr.Kind == keyaddr.Name {
		if err := h.disp.ReleaseClaim(device, addr.Name); err != nil {
			writeError(w, statusFor(err), err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run** — `nice go test ./internal/api/ -v 2>&1 | tail -20` → PASS. Fix any remaining compile use of the old `Writer.Start` (`grep -rn "\.Start(" internal/api cmd/blinkenkeysd`).

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "API: owner tag on writes; DELETE accepts direct keys and ?owner= condition"
```

---

### Task 6: API read path — `GET` key / keys, capabilities layout

**Files:**
- Create: `internal/api/keyview.go`, `internal/api/keyview_test.go`
- Modify: `internal/api/handlers.go` (interfaces, routes, `getCapabilities`, claim timeout), `internal/api/handlers_test.go` (fakes)

**Interfaces:**
- Consumes: Tasks 2–5.
- Produces: `api.Dispatcher` gains `KeyInfo(device string, led uint16) dispatcher.KeyInfo`, `CurrentColor(device string, led uint16) (color.HSV, bool)`, `Layout(device string) dispatcher.Layout`, `Connected(device string) bool`; `api.Writer` gains `Statuses(device string, now time.Time) map[effects.Target]effects.Status`; `(*Handler).SetClaimIdleTimeout(d time.Duration)`.
  Routes: `GET /devices/{name}/keys/{pos}` → `keyView` JSON, 404 if not registered (name without claim, or direct key with no owner and no status); `GET /devices/{name}/keys` → `[]keyView` sorted by `led`. `GET /devices/{name}` additionally returns `"layout":{"tabs":[...]}` (omitted when no tabs).

  `keyView` JSON shape (field names are the wire contract blincli's `internal/client` mirrors in Task 10):
  ```json
  {"device":"uid-…","key":"wezterm-17","kind":"name","led":5,"row":1,"col":2,
   "connected":true,
   "color":{"h":21,"s":255,"v":255,"hex":"#ff8000"},
   "source":{"type":"state","ref":"claude/working","owner":"…","set_at":"RFC3339","age_ms":12400},
   "effect":{"name":"breathe_orange","running":true,"elapsed_ms":12400,"duration_ms":null},
   "claim":{"last_write":"RFC3339","expires_at":"RFC3339"}}
  ```
  (`kind` is `"name"` or `"direct"`; `row`/`col` omitted for LEDs with no matrix key; `source`/`effect`/`claim`/`color` omitted when absent; `duration_ms` is `null` for open-ended effects.)

- [ ] **Step 1: Extend the fakes and write failing tests.**

In `handlers_test.go`, add to `fakeDispatcher` fields `info dispatcher.KeyInfo`, `curColor *color.HSV`, `layout dispatcher.Layout`, `disconnected bool` and methods:

```go
func (f *fakeDispatcher) KeyInfo(string, uint16) dispatcher.KeyInfo { return f.info }
func (f *fakeDispatcher) CurrentColor(string, uint16) (color.HSV, bool) {
	if f.curColor == nil {
		return color.HSV{}, false
	}
	return *f.curColor, true
}
func (f *fakeDispatcher) Layout(string) dispatcher.Layout { return f.layout }
func (f *fakeDispatcher) Connected(string) bool          { return !f.disconnected }
```
and to `fakeWriter`:

```go
func (f *fakeWriter) Statuses(device string, _ time.Time) map[effects.Target]effects.Status {
	out := map[effects.Target]effects.Status{}
	for t, st := range f.status {
		if t.Device == device {
			out[t] = st
		}
	}
	return out
}
```

`internal/api/keyview_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

func ledTarget(n uint16) effects.Target {
	return effects.Target{Device: "uid-01", Addr: keyaddr.Address{Kind: keyaddr.LED, N: n}}
}

func TestGetKeyNamedClaim(t *testing.T) {
	led := keyaddr.Address{Kind: keyaddr.LED, N: 5}
	orange := color.HSV{H: 21, S: 255, V: 255}
	setAt := time.Now().Add(-12 * time.Second)
	lastWrite := time.Now().Add(-time.Minute)
	disp := knownPad()
	disp.lookupAddr = &led
	disp.curColor = &orange
	disp.info = dispatcher.KeyInfo{Name: "wezterm-17", LastWrite: lastWrite, Row: 1, Col: 2, HasPos: true}
	w := &fakeWriter{status: map[effects.Target]effects.Status{ledTarget(5): {
		Origin: effects.Origin{Type: "state", Ref: "claude/working", Owner: "wezterm-17"},
		SetAt:  setAt,
		Effect: &effects.EffectStatus{Name: "breathe_orange", Running: true, Elapsed: 12 * time.Second},
	}}}
	h := NewHandler(disp, w, &fakeLibrary{}, nil)
	h.SetClaimIdleTimeout(8 * time.Hour)

	rec := get(h, "/devices/0/keys/wezterm-17")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; %s", rec.Code, rec.Body)
	}
	var v struct {
		Key, Kind string
		LED       uint16
		Row, Col  *uint8
		Connected bool
		Color     struct {
			H, S, V uint8
			Hex     string
		}
		Source struct {
			Type, Ref, Owner string
			AgeMS            int64 `json:"age_ms"`
		}
		Effect struct {
			Name       string
			Running    bool
			ElapsedMS  int64  `json:"elapsed_ms"`
			DurationMS *int64 `json:"duration_ms"`
		}
		Claim struct {
			ExpiresAt time.Time `json:"expires_at"`
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v; %s", err, rec.Body)
	}
	if v.Key != "wezterm-17" || v.Kind != "name" || v.LED != 5 || v.Row == nil || *v.Row != 1 || !v.Connected {
		t.Errorf("identity fields wrong: %+v", v)
	}
	if v.Color.Hex == "" || v.Color.H != 21 {
		t.Errorf("color = %+v", v.Color)
	}
	if v.Source.Type != "state" || v.Source.Ref != "claude/working" || v.Source.Owner != "wezterm-17" || v.Source.AgeMS < 11000 {
		t.Errorf("source = %+v", v.Source)
	}
	if !v.Effect.Running || v.Effect.Name != "breathe_orange" || v.Effect.ElapsedMS != 12000 || v.Effect.DurationMS != nil {
		t.Errorf("effect = %+v (duration must be null when open-ended)", v.Effect)
	}
	if want := lastWrite.Add(8 * time.Hour); v.Claim.ExpiresAt.Sub(want).Abs() > time.Second {
		t.Errorf("expires_at = %v, want ~%v", v.Claim.ExpiresAt, want)
	}
}

func TestGetKeyRegistration(t *testing.T) {
	led := keyaddr.Address{Kind: keyaddr.LED, N: 3}
	tests := []struct {
		name string
		disp *fakeDispatcher
		w    *fakeWriter
		path string
		want int
	}{
		{"unclaimed name", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupErr: dispatcher.ErrClaimNotFound}, &fakeWriter{}, "/devices/0/keys/esc", 404},
		{"direct key never written", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupAddr: &led}, &fakeWriter{}, "/devices/0/keys/idx:3", 404},
		{"direct key owned", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupAddr: &led, info: dispatcher.KeyInfo{Direct: true}}, &fakeWriter{}, "/devices/0/keys/idx:3", 200},
		{"direct key with status only", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupAddr: &led}, &fakeWriter{status: map[effects.Target]effects.Status{ledTarget(3): {}}}, "/devices/0/keys/idx:3", 200},
		{"unknown device", knownPad(), &fakeWriter{}, "/devices/zzz/keys/idx:3", 404},
		{"off matrix", &fakeDispatcher{devices: map[string]string{"0": "uid-01"}, lookupErr: dispatcher.ErrKeyNotFound}, &fakeWriter{}, "/devices/0/keys/idx:99", 404},
		{"malformed pos", knownPad(), &fakeWriter{}, "/devices/0/keys/1,x", 400},
	}
	for _, tt := range tests {
		h := NewHandler(tt.disp, tt.w, &fakeLibrary{}, nil)
		if rec := get(h, tt.path); rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d; %s", tt.name, rec.Code, tt.want, rec.Body)
		}
	}
}

func TestListKeysSortedByLED(t *testing.T) {
	w := &fakeWriter{status: map[effects.Target]effects.Status{
		ledTarget(7): {Origin: effects.Origin{Type: "color", Ref: "red"}},
		ledTarget(2): {Origin: effects.Origin{Type: "color", Ref: "blue"}},
	}}
	rec := get(NewHandler(knownPad(), w, &fakeLibrary{}, nil), "/devices/0/keys")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got []struct{ LED uint16 }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 2 || got[0].LED != 2 || got[1].LED != 7 {
		t.Errorf("list = %+v, %v", got, err)
	}
	empty := get(NewHandler(knownPad(), &fakeWriter{}, &fakeLibrary{}, nil), "/devices/0/keys")
	if empty.Body.String() != "[]\n" {
		t.Errorf("empty list body = %q, want []", empty.Body)
	}
}

func TestCapabilitiesIncludeLayout(t *testing.T) {
	disp := knownPad()
	disp.caps = dispatcher.Capabilities{LEDCount: 12}
	disp.layout = dispatcher.Layout{Tabs: []uint16{0, 1, 2}}
	rec := get(NewHandler(disp, &fakeWriter{}, &fakeLibrary{}, nil), "/devices/0")
	var got struct {
		LEDCount int `json:"led_count"`
		Layout   struct{ Tabs []uint16 }
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.LEDCount != 12 || len(got.Layout.Tabs) != 3 {
		t.Errorf("caps = %+v, %v; body %s", got, err, rec.Body)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./internal/api/ 2>&1 | tail` → FAIL (compile).

- [ ] **Step 3: Implement.**

`internal/api/handlers.go`:
- Add the four methods to `Dispatcher` and `Statuses` to `Writer` (see **Interfaces**).
- `Handler` gets `claimIdle time.Duration`; `NewHandler` sets it to `dispatcher.DefaultClaimIdleTimeout`; add:

```go
// SetClaimIdleTimeout sets the claim idle timeout used to report a named
// claim's expiry; it should match config's claims.idle_timeout.
func (h *Handler) SetClaimIdleTimeout(d time.Duration) { h.claimIdle = d }
```
- Routes: add
```go
	mux.HandleFunc("GET /devices/{name}/keys", h.listKeys)
	mux.HandleFunc("GET /devices/{name}/keys/{pos}", h.getKey)
```
- `getCapabilities`: replace `writeJSON(w, http.StatusOK, caps)` with:
```go
	view := capsView{Capabilities: caps}
	view.Layout.Tabs = h.disp.Layout(device).Tabs
	writeJSON(w, http.StatusOK, view)
```

`internal/api/keyview.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/dispatcher"
	"github.com/seefood/blinkenkeys/internal/effects"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

// capsView is GET /devices/{name}: capabilities plus the key layout hint
// blincli uses to map tab numbers to keys.
type capsView struct {
	dispatcher.Capabilities
	Layout struct {
		Tabs []uint16 `json:"tabs,omitempty"`
	} `json:"layout"`
}

type colorView struct {
	H   uint8  `json:"h"`
	S   uint8  `json:"s"`
	V   uint8  `json:"v"`
	Hex string `json:"hex"`
}

type sourceView struct {
	Type  string    `json:"type"`
	Ref   string    `json:"ref"`
	Owner string    `json:"owner,omitempty"`
	SetAt time.Time `json:"set_at"`
	AgeMS int64     `json:"age_ms"`
}

type effectView struct {
	Name       string `json:"name"`
	Running    bool   `json:"running"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	DurationMS *int64 `json:"duration_ms"`
}

type claimView struct {
	LastWrite time.Time `json:"last_write"`
	ExpiresAt time.Time `json:"expires_at"`
}

// keyView is one key's registration and state, as returned by the GET key
// routes. Color is the frame-buffer (desired) value, not a hardware read-back.
type keyView struct {
	Device    string      `json:"device"`
	Key       string      `json:"key"`
	Kind      string      `json:"kind"` // "name" or "direct"
	LED       uint16      `json:"led"`
	Row       *uint8      `json:"row,omitempty"`
	Col       *uint8      `json:"col,omitempty"`
	Connected bool        `json:"connected"`
	Color     *colorView  `json:"color,omitempty"`
	Source    *sourceView `json:"source,omitempty"`
	Effect    *effectView `json:"effect,omitempty"`
	Claim     *claimView  `json:"claim,omitempty"`
}

// buildView assembles led's view. label is how the caller addressed it; a
// name-claimed key is always labelled with its claimed name.
func (h *Handler) buildView(device string, led uint16, label string, now time.Time) keyView {
	info := h.disp.KeyInfo(device, led)
	v := keyView{Device: device, Key: label, Kind: "direct", LED: led, Connected: h.disp.Connected(device)}
	if info.Name != "" {
		v.Kind, v.Key = "name", info.Name
		v.Claim = &claimView{LastWrite: info.LastWrite, ExpiresAt: info.LastWrite.Add(h.claimIdle)}
	}
	if info.HasPos {
		row, col := info.Row, info.Col
		v.Row, v.Col = &row, &col
	}
	if c, ok := h.disp.CurrentColor(device, led); ok {
		v.Color = &colorView{H: c.H, S: c.S, V: c.V, Hex: color.HSV(c).Hex()}
	}
	if st, ok := h.w.Status(effects.Target{Device: device, Addr: keyaddr.Address{Kind: keyaddr.LED, N: led}}, now); ok {
		v.Source = &sourceView{Type: st.Origin.Type, Ref: st.Origin.Ref, Owner: st.Origin.Owner, SetAt: st.SetAt, AgeMS: now.Sub(st.SetAt).Milliseconds()}
		if st.Effect != nil {
			ev := &effectView{Name: st.Effect.Name, Running: st.Effect.Running, ElapsedMS: st.Effect.Elapsed.Milliseconds()}
			if st.Effect.Finite {
				ms := st.Effect.Total.Milliseconds()
				ev.DurationMS = &ms
			}
			v.Effect = ev
		}
	}
	return v
}

func (h *Handler) getKey(w http.ResponseWriter, r *http.Request) {
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
	// Read-only: Canonical would claim a name / mark a direct key as a side effect.
	led, err := h.disp.Lookup(r.Context(), device, addr)
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	now := time.Now()
	view := h.buildView(device, led.N, addr.String(), now)
	if addr.Kind != keyaddr.Name && view.Source == nil && view.Kind == "direct" {
		if info := h.disp.KeyInfo(device, led.N); !info.Direct {
			writeError(w, http.StatusNotFound, fmt.Errorf("%w: %s is not registered", dispatcher.ErrKeyNotFound, addr))
			return
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) listKeys(w http.ResponseWriter, r *http.Request) {
	device, ok := h.disp.ResolveDevice(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("%w %q", dispatcher.ErrDeviceNotFound, r.PathValue("name")))
		return
	}
	now := time.Now()
	views := []keyView{}
	for t := range h.w.Statuses(device, now) {
		if t.Addr.Kind == keyaddr.LED {
			views = append(views, h.buildView(device, t.Addr.N, t.Addr.String(), now))
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].LED < views[j].LED })
	writeJSON(w, http.StatusOK, views)
}
```

(`color.HSV(c)` is a no-op conversion — drop it if the linter flags `unconvert`; `c` is already `color.HSV`.)

- [ ] **Step 4: Run** — `nice go test ./internal/api/ -v 2>&1 | tail -30` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "API: GET key/keys status endpoints; layout hint in device capabilities"
```

---

### Task 7: Daemon wiring, example config, daemon docs, manual check

**Files:**
- Modify: `cmd/blinkenkeysd/main.go`, `cmd/blinkenkeysd/main_test.go`, `examples/config/config.yaml`, `examples/config/README.md`
- Create: `docs/superpowers/manual-checks/blincli.md` (daemon half; the client half is added in Task 15)

**Interfaces:**
- Consumes: Tasks 1, 2, 6. Produces: `layoutFor(d config.DeviceDecl) (dispatcher.Layout, bool)` in package main; `registry.SetLayout` and `handler.SetClaimIdleTimeout` wired.

- [ ] **Step 1: Write the failing test** — append to `cmd/blinkenkeysd/main_test.go` (add imports `reflect`, `config`, `dispatcher` as needed; check the file's existing imports first):

```go
func TestLayoutFor(t *testing.T) {
	if _, ok := layoutFor(config.DeviceDecl{ID: "a"}); ok {
		t.Error("device without keys: must report no layout")
	}
	l, ok := layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Tabs: config.KeyList{0, 1}, Pool: config.KeyList{6}}})
	if !ok || l.DefaultPool || !reflect.DeepEqual(l.Tabs, []uint16{0, 1}) || !reflect.DeepEqual(l.Pool, []uint16{6}) {
		t.Errorf("explicit pool: %+v, %v", l, ok)
	}
	l, _ = layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Tabs: config.KeyList{0}}})
	if !l.DefaultPool {
		t.Errorf("omitted pool must give DefaultPool: %+v", l)
	}
	l, _ = layoutFor(config.DeviceDecl{ID: "a", Keys: &config.KeyLayout{Pool: config.KeyList{}}})
	if l.DefaultPool || len(l.Pool) != 0 {
		t.Errorf("explicit empty pool must not be DefaultPool: %+v", l)
	}
	_ = dispatcher.Layout{} // keep the import honest if the file didn't already use it
}
```

- [ ] **Step 2: Run** — `nice go test ./cmd/blinkenkeysd/ -run LayoutFor` → FAIL (`undefined: layoutFor`).

- [ ] **Step 3: Implement** in `cmd/blinkenkeysd/main.go`:

```go
// layoutFor converts a config device entry's keys: block into the
// dispatcher's Layout. ok is false when the entry has no keys: block (the
// device then keeps dispatcher.DefaultLayout).
func layoutFor(d config.DeviceDecl) (dispatcher.Layout, bool) {
	if d.Keys == nil {
		return dispatcher.Layout{}, false
	}
	return dispatcher.Layout{Tabs: d.Keys.Tabs, Pool: d.Keys.Pool, DefaultPool: d.Keys.Pool == nil}, true
}
```
Change the declare loop to:

```go
	for _, d := range cfg.Devices {
		if d.Optional {
			registry.Declare(d.ID)
		}
		if l, ok := layoutFor(d); ok {
			registry.SetLayout(d.ID, l)
		}
	}
```
and after `handler := api.NewHandler(disp, engine, lib, logger)` add `handler.SetClaimIdleTimeout(cfg.ClaimIdleTimeout())`.

Docs:
- `examples/config/config.yaml`: under the `devices:` entry add (commented, keeps the sample valid):
```yaml
    # keys:                 # optional per-device key roles, by idx (reading-order) index
    #   tabs: [0-5]         # blincli maps terminal tab N -> tabs[(N-1) mod len]
    #   pool: [6-11]        # named claims are auto-assigned from here; [] = none
```
- `examples/config/README.md`: in the `config.yaml` schema block add the same three `keys:` lines under `devices:` with these comment lines: `# tabs: tab-number slots, in order; list items are idx numbers or ranges, e.g. [0-4, 6, 8-10] or "0-4,6"`, `# pool: keys named claims may be auto-assigned from; omitted = every row>=1 key not in tabs; [] = no pool`, `# keys in neither list are never touched`. Replace the last paragraph of "Addressing a key" (`DELETE ... only works on a key name`) with:
```markdown
`DELETE /devices/{name}/keys/{pos}` blanks the key (cancelling any running effect)
and, if `{pos}` is a **name**, releases its claim. Any `{pos}` form is accepted.
Add `?owner=TAG` to make it conditional: it does nothing (204) if the key's
recorded owner (the `owner` given on the last write) is a different tag.

`PUT` bodies accept an optional `"owner": "<tag>"` (1–128 bytes) next to the
one of `color`/`effect`/`state`. It is only recorded, for the conditional
`DELETE` and for `GET`.

`GET /devices/{name}/keys/{pos}` returns the key's registration and state
(404 if it was never written/claimed); `GET /devices/{name}/keys` lists all
of them. The `color` it reports is the daemon's frame buffer (the desired
value) — VialRGB cannot be read back. `GET /devices/{name}` now also reports
`layout.tabs` when configured. Status is in-memory only and resets on restart.
```
- `docs/superpowers/manual-checks/blincli.md` (create), daemon half:

````markdown
# blincli / daemon read-API manual check

Requires a running `bin/blinkenkeysd` with a device attached and, in `config.yaml`:

```yaml
devices:
  - id: <your device name>
    keys: {tabs: [0-5], pool: [6-11]}
```

Set `S=$HOME/.local/state/blinkenkeys/api.sock` and `D=<device name>`.

## Daemon half

1. `curl -s --unix-socket $S http://localhost/devices/$D | jq .layout` → `{"tabs":[0,1,2,3,4,5]}`.
2. `curl -s --unix-socket $S -X PUT http://localhost/devices/$D/keys/idx:0 -d '{"state":"claude/working","owner":"manual"}'` → 204; key 0 breathes orange.
3. `curl -s --unix-socket $S http://localhost/devices/$D/keys/idx:0 | jq` → `kind:"direct"`, `source.type:"state"`, `source.owner:"manual"`, `effect.running:true`, `color` moving between polls.
4. `curl -s --unix-socket $S http://localhost/devices/$D/keys/neverclaimed -w '%{http_code}\n'` → 404, and a following `GET /devices/$D/keys` does **not** list it (GET must not claim).
5. `curl -s --unix-socket $S -X DELETE "http://localhost/devices/$D/keys/idx:0?owner=someone-else" -w '%{http_code}\n'` → 204 and key 0 **stays lit**.
6. Same with `?owner=manual` → 204 and the key goes dark; `GET` of it → 404-or-blank-state per the engine record being dropped (it is dropped: 404 only if also not direct-owned; direct ownership persists, so expect 200 with no `source`).
7. `curl ... -X PUT .../keys/named1 -d '{"color":"red"}'` → lands on idx 6+ (pool), not 0–5.
````

- [ ] **Step 4: Run** — `nice make test && nice go run ./cmd/blinkenkeysd --check-config -c examples/config` → tests PASS; `ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/blinkenkeysd examples/config docs/superpowers/manual-checks/blincli.md
git commit -m "Daemon: wire key layouts and claim timeout; document read API"
```

---

## Phase B — client

### Task 8: `internal/termid` — terminal detection

**Files:**
- Create: `internal/termid/termid.go`, `internal/termid/termid_test.go`

**Interfaces:**
- Produces:
  ```go
  type Identity struct{ Terminal string; Tab int; InstanceID string } // Tab is 1-based, 0 = unknown
  func (i Identity) Name() string // "wezterm-17"; "" if Terminal == ""
  type Env struct {
  	Getenv func(string) string
  	Run    func(ctx context.Context, name string, args ...string) (string, error)
  }
  func Detect(ctx context.Context, env Env) Identity
  func ExecRun(ctx context.Context, name string, args ...string) (string, error)
  func FallbackName(getenv func(string) string) (name, source string)
  func Sanitize(s string) string
  ```
  Detection order: tmux, iTerm2, WezTerm, kitty. A helper (`tmux`, `wezterm cli`) that fails, times out (shared 500 ms budget) or returns garbage leaves `Tab == 0` but still yields the instance id.

- [ ] **Step 1: Write the failing tests** — `internal/termid/termid_test.go`:

```go
package termid

import (
	"context"
	"errors"
	"testing"
	"time"
)

func envOf(vars map[string]string, run func(ctx context.Context, name string, args ...string) (string, error)) Env {
	if run == nil {
		run = func(context.Context, string, ...string) (string, error) { return "", errors.New("no helper") }
	}
	return Env{Getenv: func(k string) string { return vars[k] }, Run: run}
}

func TestDetectITerm(t *testing.T) {
	id := Detect(context.Background(), envOf(map[string]string{"ITERM_SESSION_ID": "w0t2p1:C3D91F33-3805-47E2-A3F6-B8AED6EC2209"}, nil))
	if id.Terminal != "iterm" || id.Tab != 3 || id.InstanceID != "C3D91F33" || id.Name() != "iterm-C3D91F33" {
		t.Errorf("got %+v (%q)", id, id.Name())
	}
	bare := Detect(context.Background(), envOf(map[string]string{"ITERM_SESSION_ID": "w1t0p0"}, nil))
	if bare.Tab != 1 || bare.InstanceID != "w1t0p0" {
		t.Errorf("no-UUID form: %+v", bare)
	}
	odd := Detect(context.Background(), envOf(map[string]string{"ITERM_SESSION_ID": "garbage/value"}, nil))
	if odd.Terminal != "iterm" || odd.Tab != 0 || odd.InstanceID != "garbage-value" {
		t.Errorf("unparseable: %+v", odd)
	}
}

func TestDetectTmuxTabIsWindowIndexMinusBase(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) (string, error) {
		if name != "tmux" {
			return "", errors.New("unexpected " + name)
		}
		if args[0] == "display-message" {
			return "3\n", nil
		}
		return "1\n", nil // show-options -gv base-index
	}
	id := Detect(context.Background(), envOf(map[string]string{"TMUX_PANE": "%7", "ITERM_SESSION_ID": "w0t0p0:X"}, run))
	if id.Terminal != "tmux" || id.Tab != 3 || id.InstanceID != "7" {
		t.Errorf("got %+v; tmux must win over the outer terminal and 3-1+1 = 3", id)
	}
}

func TestDetectTmuxHelperFailureKeepsInstance(t *testing.T) {
	id := Detect(context.Background(), envOf(map[string]string{"TMUX_PANE": "%7"}, nil))
	if id.Terminal != "tmux" || id.Tab != 0 || id.InstanceID != "7" {
		t.Errorf("got %+v", id)
	}
}

const weztermList = `[
 {"window_id":0,"tab_id":0,"pane_id":0},
 {"window_id":0,"tab_id":4,"pane_id":9},
 {"window_id":0,"tab_id":4,"pane_id":10},
 {"window_id":1,"tab_id":2,"pane_id":5},
 {"window_id":0,"tab_id":7,"pane_id":12}
]`

func TestDetectWezTermTabIsRankWithinWindow(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) (string, error) { return weztermList, nil }
	for pane, want := range map[string]int{"0": 1, "9": 2, "10": 2, "12": 3, "5": 1} {
		id := Detect(context.Background(), envOf(map[string]string{"WEZTERM_PANE": pane}, run))
		if id.Terminal != "wezterm" || id.Tab != want || id.InstanceID != pane {
			t.Errorf("pane %s: got %+v, want tab %d", pane, id, want)
		}
	}
}

func TestDetectWezTermBadOutputDegrades(t *testing.T) {
	for _, out := range []string{"not json", "[]", `[{"window_id":0,"tab_id":0,"pane_id":3}]`} {
		run := func(context.Context, string, ...string) (string, error) { return out, nil }
		id := Detect(context.Background(), envOf(map[string]string{"WEZTERM_PANE": "9"}, run))
		if id.Terminal != "wezterm" || id.Tab != 0 || id.InstanceID != "9" {
			t.Errorf("output %q: got %+v", out, id)
		}
	}
}

func TestDetectHangingHelperIsBounded(t *testing.T) {
	run := func(ctx context.Context, _ string, _ ...string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	start := time.Now()
	id := Detect(context.Background(), envOf(map[string]string{"WEZTERM_PANE": "9"}, run))
	if time.Since(start) > 2*time.Second || id.Tab != 0 || id.InstanceID != "9" {
		t.Errorf("hang not bounded: %v, %+v", time.Since(start), id)
	}
}

func TestDetectKittyAndNone(t *testing.T) {
	id := Detect(context.Background(), envOf(map[string]string{"KITTY_WINDOW_ID": "4"}, nil))
	if id.Terminal != "kitty" || id.Tab != 0 || id.Name() != "kitty-4" {
		t.Errorf("kitty: %+v", id)
	}
	if id := Detect(context.Background(), envOf(nil, nil)); id != (Identity{}) || id.Name() != "" {
		t.Errorf("none: %+v", id)
	}
}

func TestFallbackNameOrderAndSanitize(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	name, src := FallbackName(get(map[string]string{"CLAUDE_CODE_SESSION_ID": "abc-123", "STY": "x"}))
	if name != "claude-abc-123" || src != "CLAUDE_CODE_SESSION_ID" {
		t.Errorf("got %q from %q", name, src)
	}
	name, _ = FallbackName(get(map[string]string{"BLINKENKEYS_NAME": "my/key,1", "CLAUDE_CODE_SESSION_ID": "z"}))
	if name != "my-key-1" {
		t.Errorf("BLINKENKEYS_NAME first and sanitized: got %q", name)
	}
	if name, src := FallbackName(get(nil)); name != "" || src != "" {
		t.Errorf("empty env: %q %q", name, src)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./internal/termid/ -v` → FAIL (package has no code).

- [ ] **Step 3: Implement** — `internal/termid/termid.go`:

```go
// Package termid works out which terminal tab/pane the process is running in,
// from environment variables and, where needed, a short-lived helper command
// (tmux, wezterm cli). It is pure Go with no dependency on the daemon.
package termid

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Identity is what the environment says about the calling terminal.
type Identity struct {
	Terminal   string // "tmux", "iterm", "wezterm", "kitty"; "" if none recognized
	Tab        int    // 1-based tab number in its window; 0 if unknown
	InstanceID string // short id of this pane/window; "" if none
}

// Name is the identity as a key name, e.g. "wezterm-17".
func (i Identity) Name() string {
	switch {
	case i.Terminal == "":
		return ""
	case i.InstanceID == "":
		return i.Terminal
	default:
		return i.Terminal + "-" + i.InstanceID
	}
}

// Env is the process environment and helper execution, injectable for tests.
type Env struct {
	Getenv func(string) string
	Run    func(ctx context.Context, name string, args ...string) (string, error)
}

// helperTimeout bounds all helper commands of one Detect call together: a
// hook must never hang on a missing tmux server or a stuck wezterm mux.
const helperTimeout = 500 * time.Millisecond

// ExecRun is the real Env.Run.
func ExecRun(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output() // #nosec G204 -- name/args are literals chosen by this package's resolvers
	return string(out), err
}

// Detect identifies the calling terminal, most specific first: tmux (whose
// panes share the outer terminal's env), then iTerm2, WezTerm, kitty.
func Detect(ctx context.Context, env Env) Identity {
	ctx, cancel := context.WithTimeout(ctx, helperTimeout)
	defer cancel()
	for _, d := range []func(context.Context, Env) (Identity, bool){detectTmux, detectITerm, detectWezTerm, detectKitty} {
		if id, ok := d(ctx, env); ok {
			return id
		}
	}
	return Identity{}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Sanitize makes s safe as a key name: names must not contain "," (it would
// parse as row,col) or "/" (a single URL path segment).
func Sanitize(s string) string { return unsafeName.ReplaceAllString(s, "-") }

func detectTmux(ctx context.Context, env Env) (Identity, bool) {
	pane := env.Getenv("TMUX_PANE")
	if pane == "" {
		return Identity{}, false
	}
	id := Identity{Terminal: "tmux", InstanceID: Sanitize(strings.TrimPrefix(pane, "%"))}
	idx, err1 := env.Run(ctx, "tmux", "display-message", "-p", "-t", pane, "#{window_index}")
	base, err2 := env.Run(ctx, "tmux", "show-options", "-gv", "base-index")
	if err1 == nil && err2 == nil {
		w, e1 := strconv.Atoi(strings.TrimSpace(idx))
		b, e2 := strconv.Atoi(strings.TrimSpace(base))
		if e1 == nil && e2 == nil && w-b+1 >= 1 {
			id.Tab = w - b + 1
		}
	}
	return id, true
}

// itermRE matches ITERM_SESSION_ID: w<window>t<tab>p<pane>[:<UUID>]. iTerm
// numbers tabs from 0; the variable is fixed when the shell starts, so it
// goes stale if tabs are later reordered or closed.
var itermRE = regexp.MustCompile(`^w(\d+)t(\d+)p(\d+)(?::(.+))?$`)

func detectITerm(_ context.Context, env Env) (Identity, bool) {
	raw := env.Getenv("ITERM_SESSION_ID")
	if raw == "" {
		return Identity{}, false
	}
	id := Identity{Terminal: "iterm", InstanceID: Sanitize(raw)}
	m := itermRE.FindStringSubmatch(raw)
	if m == nil {
		return id, true
	}
	t, _ := strconv.Atoi(m[2])
	id.Tab = t + 1
	if m[4] != "" {
		uuid := m[4]
		if len(uuid) > 8 {
			uuid = uuid[:8]
		}
		id.InstanceID = Sanitize(uuid)
	} else {
		id.InstanceID = Sanitize(m[0])
	}
	return id, true
}

type weztermPane struct {
	WindowID int `json:"window_id"`
	TabID    int `json:"tab_id"`
	PaneID   int `json:"pane_id"`
}

func detectWezTerm(ctx context.Context, env Env) (Identity, bool) {
	pane := env.Getenv("WEZTERM_PANE")
	if pane == "" {
		return Identity{}, false
	}
	id := Identity{Terminal: "wezterm", InstanceID: Sanitize(pane)}
	if out, err := env.Run(ctx, "wezterm", "cli", "list", "--format", "json"); err == nil {
		id.Tab = weztermTab(out, pane)
	}
	return id, true
}

// weztermTab ranks pane's tab among the distinct tab_ids of its window, in
// `wezterm cli list` order. WezTerm exposes no tab-position field, so this
// relies on the list being in tab order (checked manually; see
// docs/superpowers/manual-checks/blincli.md). 0 if pane isn't found.
func weztermTab(listJSON, pane string) int {
	want, err := strconv.Atoi(pane)
	if err != nil {
		return 0
	}
	var panes []weztermPane
	if json.Unmarshal([]byte(listJSON), &panes) != nil {
		return 0
	}
	win, tab, found := 0, 0, false
	for _, p := range panes {
		if p.PaneID == want {
			win, tab, found = p.WindowID, p.TabID, true
			break
		}
	}
	if !found {
		return 0
	}
	var seen []int
	for _, p := range panes {
		if p.WindowID == win && !slices.Contains(seen, p.TabID) {
			seen = append(seen, p.TabID)
		}
	}
	return slices.Index(seen, tab) + 1
}

// detectKitty reports kitty by window id only: KITTY_WINDOW_ID is not a tab
// number, and tab info would need remote control enabled.
func detectKitty(_ context.Context, env Env) (Identity, bool) {
	w := env.Getenv("KITTY_WINDOW_ID")
	if w == "" {
		return Identity{}, false
	}
	return Identity{Terminal: "kitty", InstanceID: Sanitize(w)}, true
}

// fallbackVars are checked, in order, when no terminal instance id exists.
// Only CLAUDE_CODE_SESSION_ID is verified (hooks-basic.json uses it); the
// rest are best-effort.
var fallbackVars = []struct{ env, prefix string }{
	{"BLINKENKEYS_NAME", ""},
	{"CLAUDE_CODE_SESSION_ID", "claude-"},
	{"ZELLIJ_PANE_ID", "zellij-"},
	{"STY", "screen-"},
	{"WT_SESSION", "wt-"},
	{"TERM_SESSION_ID", "termsession-"},
}

// FallbackName returns a key name built from the first unique id found in
// the environment, and which variable it came from; "" if none.
func FallbackName(getenv func(string) string) (name, source string) {
	for _, v := range fallbackVars {
		if val := getenv(v.env); val != "" {
			return Sanitize(v.prefix + val), v.env
		}
	}
	return "", ""
}
```

- [ ] **Step 4: Run** — `nice go test ./internal/termid/ -v` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/termid
git commit -m "Add termid: terminal tab/instance detection with bounded helpers"
```

---

### Task 9: `internal/client` — config file and endpoint resolution

**Files:**
- Create: `internal/client/config.go`, `internal/client/config_test.go`, `internal/client/resolve.go`, `internal/client/resolve_test.go`

**Interfaces:**
- Produces:
  ```go
  type FileConfig struct{ URL, Socket, Token, TokenFile, Device string; Slots int }  // yaml: url, socket, token, token_file, device, slots
  func DefaultConfigPath(getenv func(string) string, home string) string
  func LoadFile(path string) (fc FileConfig, exists bool, err error) // missing file => zero, false, nil
  func ExpandHome(p, home string) string
  type Options struct{ Socket, URL, Token, TokenFile, ConfigPath string }
  type Endpoint struct{ Kind string /*"unix"|"http"*/; Socket, BaseURL, Token, Source string }
  func (e Endpoint) Remote() bool
  type Resolver struct{ Getenv func(string) string; Home string }
  func (r Resolver) ConfigPath(o Options) string
  func (r Resolver) Resolve(o Options) (Endpoint, FileConfig, error)
  var ErrNoEndpoint, ErrUnreachable, ErrAuth, ErrUsage error  // sentinels; see below
  type NoEndpointError struct{ ConfigPath string; ConfigExists bool; SocketPath string } // Is(ErrNoEndpoint)
  ```
  Precedence: flags > env (`BLINKENKEYS_URL`/`_SOCKET`) > config file > local socket probe. Within a layer, setting both url and socket is `ErrUsage`. Token for http: `Options.Token` > `Options.TokenFile` > `$BLINKENKEYS_TOKEN` > file `token` > file `token_file`; none → `ErrAuth` (wrapped), **before any request**. A socket path that exists but refuses connection → `ErrUnreachable`.

- [ ] **Step 1: Write the failing tests.**

`internal/client/config_test.go`:

```go
package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigPath(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := DefaultConfigPath(get(nil), "/home/u"); got != "/home/u/.config/blinkenkeys/blincli.yaml" {
		t.Errorf("got %q", got)
	}
	if got := DefaultConfigPath(get(map[string]string{"XDG_CONFIG_HOME": "/x"}), "/home/u"); got != "/x/blinkenkeys/blincli.yaml" {
		t.Errorf("got %q", got)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	if fc, exists, err := LoadFile(filepath.Join(dir, "none.yaml")); err != nil || exists || fc != (FileConfig{}) {
		t.Errorf("missing file: %+v %v %v", fc, exists, err)
	}
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte("url: http://nas:49994\ntoken_file: ~/t\ndevice: d\nslots: 6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fc, exists, err := LoadFile(p)
	if err != nil || !exists || fc.URL != "http://nas:49994" || fc.TokenFile != "~/t" || fc.Device != "d" || fc.Slots != 6 {
		t.Errorf("got %+v %v %v", fc, exists, err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("urll: x\n"), 0o600)
	if _, _, err := LoadFile(bad); err == nil {
		t.Error("unknown key must be an error")
	}
	empty := filepath.Join(dir, "empty.yaml")
	_ = os.WriteFile(empty, []byte("# only comments\n"), 0o600)
	if fc, exists, err := LoadFile(empty); err != nil || !exists || fc != (FileConfig{}) {
		t.Errorf("comment-only file: %+v %v %v", fc, exists, err)
	}
}

func TestExpandHome(t *testing.T) {
	if got := ExpandHome("~/a/b", "/h"); got != "/h/a/b" {
		t.Errorf("got %q", got)
	}
	if got := ExpandHome("/abs", "/h"); got != "/abs" {
		t.Errorf("got %q", got)
	}
}
```

`internal/client/resolve_test.go`:

```go
package client

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resolverFor(t *testing.T, env map[string]string) (Resolver, string) {
	t.Helper()
	home := t.TempDir()
	return Resolver{Getenv: func(k string) string { return env[k] }, Home: home}, home
}

func writeCfg(t *testing.T, home, body string) {
	t.Helper()
	p := DefaultConfigPath(func(string) string { return "" }, home)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	r, home := resolverFor(t, map[string]string{"BLINKENKEYS_URL": "http://env:1", "BLINKENKEYS_TOKEN": "envtok"})
	writeCfg(t, home, "url: http://file:2\ntoken: filetok\n")

	ep, _, err := r.Resolve(Options{URL: "http://flag:3", Token: "flagtok"})
	if err != nil || ep.BaseURL != "http://flag:3" || ep.Token != "flagtok" {
		t.Errorf("flags: %+v %v", ep, err)
	}
	ep, _, err = r.Resolve(Options{})
	if err != nil || ep.BaseURL != "http://env:1" || ep.Token != "envtok" {
		t.Errorf("env: %+v %v", ep, err)
	}
	r.Getenv = func(string) string { return "" }
	ep, fc, err := r.Resolve(Options{})
	if err != nil || ep.BaseURL != "http://file:2" || ep.Token != "filetok" || fc.URL != "http://file:2" || !ep.Remote() {
		t.Errorf("file: %+v %v", ep, err)
	}
}

func TestResolveTokenFileAndMissingToken(t *testing.T) {
	r, home := resolverFor(t, nil)
	tok := filepath.Join(home, "tok")
	_ = os.WriteFile(tok, []byte("  secret\n"), 0o600)
	ep, _, err := r.Resolve(Options{URL: "http://h:1", TokenFile: tok})
	if err != nil || ep.Token != "secret" {
		t.Errorf("token file: %+v %v", ep, err)
	}
	if _, _, err := r.Resolve(Options{URL: "http://h:1"}); !errors.Is(err, ErrAuth) {
		t.Errorf("no token for http: err = %v, want ErrAuth", err)
	}
	if _, _, err := r.Resolve(Options{URL: "not a url"}); !errors.Is(err, ErrUsage) {
		t.Errorf("bad url: err = %v, want ErrUsage", err)
	}
	if _, _, err := r.Resolve(Options{URL: "http://h:1", Socket: "/s"}); !errors.Is(err, ErrUsage) {
		t.Errorf("both url and socket: err = %v, want ErrUsage", err)
	}
}

func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "bk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s")
}

func TestResolveExplicitSocketNoToken(t *testing.T) {
	r, _ := resolverFor(t, nil)
	ep, _, err := r.Resolve(Options{Socket: "/x/api.sock"})
	if err != nil || ep.Kind != "unix" || ep.Socket != "/x/api.sock" || ep.Remote() {
		t.Errorf("%+v %v", ep, err)
	}
}

func TestResolveLocalSocketProbe(t *testing.T) {
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on unix socket: %v", err)
	}
	defer ln.Close()
	r, home := resolverFor(t, nil)
	// the daemon's own config.yaml names the socket
	cfgDir := filepath.Join(home, ".config", "blinkenkeys")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("listeners:\n  socket:\n    path: "+sock+"\n"), 0o600)
	ep, _, err := r.Resolve(Options{})
	if err != nil || ep.Kind != "unix" || ep.Socket != sock {
		t.Errorf("probe: %+v %v", ep, err)
	}
}

func TestResolveDeadSocketIsUnreachable(t *testing.T) {
	r, home := resolverFor(t, nil)
	sock := shortSock(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot listen on unix socket: %v", err)
	}
	_ = ln.Close() // file remains (Go removes it on Close for listeners it created; recreate as plain file)
	_ = os.WriteFile(sock, nil, 0o600)
	cfgDir := filepath.Join(home, ".config", "blinkenkeys")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("listeners:\n  socket:\n    path: "+sock+"\n"), 0o600)
	if _, _, err := r.Resolve(Options{}); !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestResolveNothingConfigured(t *testing.T) {
	r, home := resolverFor(t, nil)
	_, _, err := r.Resolve(Options{})
	if !errors.Is(err, ErrNoEndpoint) {
		t.Fatalf("err = %v, want ErrNoEndpoint", err)
	}
	msg := err.Error()
	for _, want := range []string{"blincli config init", "--interactive", "--url", filepath.Join(home, ".config", "blinkenkeys", "blincli.yaml")} {
		if !strings.Contains(msg, want) {
			t.Errorf("help text lacks %q:\n%s", want, msg)
		}
	}
	// a config file that exists but names no endpoint is still "no endpoint"
	writeCfg(t, home, "# nothing set\n")
	if _, _, err := r.Resolve(Options{}); !errors.Is(err, ErrNoEndpoint) {
		t.Errorf("empty config: err = %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./internal/client/ -v` → FAIL.

- [ ] **Step 3: Implement.**

`internal/client/config.go`:

```go
// Package client is blincli's engine: client-config loading, endpoint
// resolution, the HTTP client for blinkenkeysd, and key planning. It is pure
// Go and must not import the daemon's cgo packages.
package client

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// FileConfig is blincli.yaml. Every field is optional.
type FileConfig struct {
	URL       string `yaml:"url"`
	Socket    string `yaml:"socket"`
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	Device    string `yaml:"device"`
	Slots     int    `yaml:"slots"`
}

// DefaultConfigPath is ${XDG_CONFIG_HOME:-~/.config}/blinkenkeys/blincli.yaml.
func DefaultConfigPath(getenv func(string) string, home string) string {
	base := getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "blinkenkeys", "blincli.yaml")
}

// LoadFile reads path. A missing file is not an error (exists is false);
// unknown keys are.
func LoadFile(path string) (fc FileConfig, exists bool, err error) {
	data, err := os.ReadFile(path) // #nosec G304 -- operator-supplied config location
	if errors.Is(err, fs.ErrNotExist) {
		return FileConfig{}, false, nil
	}
	if err != nil {
		return FileConfig{}, false, fmt.Errorf("blincli config: %w", err)
	}
	if err := yaml.UnmarshalWithOptions(data, &fc, yaml.DisallowUnknownField()); err != nil {
		return FileConfig{}, true, fmt.Errorf("blincli config %s: %w", path, err)
	}
	return fc, true, nil
}

// ExpandHome expands a leading "~/".
func ExpandHome(p, home string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
```
(A comment-only/empty file: goccy may return an error on empty documents — if `TestLoadFile`'s comment-only case fails with an EOF-style error, treat `len(strings.TrimSpace(stripComments)) == 0` as empty: skip unmarshalling when every line is blank or starts with `#`.)

`internal/client/resolve.go`:

```go
package client

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/config"
)

// Sentinels the command layer maps to exit codes.
var (
	ErrNoEndpoint  = errors.New("no blinkenkeysd endpoint configured") // exit 78
	ErrUnreachable = errors.New("blinkenkeysd unreachable")             // exit 69
	ErrAuth        = errors.New("bearer token missing or rejected")     // exit 77
	ErrUsage       = errors.New("usage error")                          // exit 64
)

// Options are the endpoint-related command-line values.
type Options struct {
	Socket, URL, Token, TokenFile, ConfigPath string
}

// Endpoint is a resolved daemon address.
type Endpoint struct {
	Kind    string // "unix" or "http"
	Socket  string
	BaseURL string
	Token   string
	Source  string // where it came from, for -v
}

// Remote reports whether the endpoint is a network address (not the local socket).
func (e Endpoint) Remote() bool { return e.Kind == "http" }

// NoEndpointError carries what was looked at, so its message can say how to fix it.
type NoEndpointError struct {
	ConfigPath   string
	ConfigExists bool
	SocketPath   string
}

func (e *NoEndpointError) Is(target error) bool { return target == ErrNoEndpoint }

func (e *NoEndpointError) Error() string {
	cfg := "no config at " + e.ConfigPath
	if e.ConfigExists {
		cfg = "config " + e.ConfigPath + " sets no url or socket"
	}
	return fmt.Sprintf(`blincli: no blinkenkeysd endpoint: no --url/--socket, %s, and no daemon socket at %s

Create a config with one of:
  blincli config init                          write an annotated template, then edit it
  blincli config init --url http://HOST:49994 --token-file FILE [--device NAME]
  blincli config init --interactive            prompt for the values (requires a terminal)
or pass --url/--socket on each call, or set BLINKENKEYS_URL / BLINKENKEYS_SOCKET.`, cfg, e.SocketPath)
}

// Resolver finds the daemon: flags > environment > client config > local socket.
type Resolver struct {
	Getenv func(string) string
	Home   string
}

// ConfigPath is the client config path in effect.
func (r Resolver) ConfigPath(o Options) string {
	if o.ConfigPath != "" {
		return o.ConfigPath
	}
	return DefaultConfigPath(r.Getenv, r.Home)
}

// Resolve returns the endpoint and the loaded client config (zero if none).
func (r Resolver) Resolve(o Options) (Endpoint, FileConfig, error) {
	path := r.ConfigPath(o)
	fc, exists, err := LoadFile(path)
	if err != nil {
		return Endpoint{}, fc, err
	}
	layers := []struct{ url, socket, src string }{
		{o.URL, o.Socket, "command line"},
		{r.Getenv("BLINKENKEYS_URL"), r.Getenv("BLINKENKEYS_SOCKET"), "environment"},
		{fc.URL, fc.Socket, path},
	}
	for _, l := range layers {
		switch {
		case l.url != "" && l.socket != "":
			return Endpoint{}, fc, fmt.Errorf("%w: %s sets both a url and a socket", ErrUsage, l.src)
		case l.url != "":
			ep, err := r.httpEndpoint(l.url, l.src, o, fc)
			return ep, fc, err
		case l.socket != "":
			return Endpoint{Kind: "unix", Socket: ExpandHome(l.socket, r.Home), Source: l.src}, fc, nil
		}
	}
	return r.probeLocal(path, exists, fc)
}

func (r Resolver) httpEndpoint(raw, src string, o Options, fc FileConfig) (Endpoint, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Endpoint{}, fmt.Errorf("%w: %q is not an http(s) URL", ErrUsage, raw)
	}
	tok, err := r.token(o, fc)
	if err != nil {
		return Endpoint{}, err
	}
	if tok == "" {
		return Endpoint{}, fmt.Errorf("%w: a token is required for %s (use --token-file, $BLINKENKEYS_TOKEN, or token/token_file in the config)", ErrAuth, raw)
	}
	return Endpoint{Kind: "http", BaseURL: strings.TrimRight(raw, "/"), Token: tok, Source: src}, nil
}

func (r Resolver) token(o Options, fc FileConfig) (string, error) {
	if o.Token != "" {
		return o.Token, nil
	}
	for _, f := range []string{o.TokenFile} {
		if f != "" {
			return r.readToken(f)
		}
	}
	if t := r.Getenv("BLINKENKEYS_TOKEN"); t != "" {
		return t, nil
	}
	if fc.Token != "" {
		return fc.Token, nil
	}
	if fc.TokenFile != "" {
		return r.readToken(fc.TokenFile)
	}
	return "", nil
}

func (r Resolver) readToken(path string) (string, error) {
	b, err := os.ReadFile(ExpandHome(path, r.Home)) // #nosec G304 -- operator-supplied token file
	if err != nil {
		return "", fmt.Errorf("%w: reading token file: %v", ErrAuth, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// localSocketPath is the daemon's socket: its config.yaml's
// listeners.socket.path if a readable one exists, else the default. (Mirrors
// cmd/blinkenkeysd's resolveSocketPath.)
func (r Resolver) localSocketPath() string {
	if cfg, err := config.LoadDir(config.Dir(r.Getenv, r.Home)); err == nil && cfg.Listeners.Socket.Path != "" {
		return ExpandHome(cfg.Listeners.Socket.Path, r.Home)
	}
	return filepath.Join(r.Home, ".local", "state", "blinkenkeys", "api.sock")
}

func (r Resolver) probeLocal(cfgPath string, cfgExists bool, fc FileConfig) (Endpoint, FileConfig, error) {
	sock := r.localSocketPath()
	if conn, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		_ = conn.Close()
		return Endpoint{Kind: "unix", Socket: sock, Source: "local socket"}, fc, nil
	}
	if _, err := os.Stat(sock); err == nil {
		return Endpoint{}, fc, fmt.Errorf("%w: socket %s exists but nothing answers (is blinkenkeysd running?)", ErrUnreachable, sock)
	}
	return Endpoint{}, fc, &NoEndpointError{ConfigPath: cfgPath, ConfigExists: cfgExists, SocketPath: sock}
}
```
(`for _, f := range []string{o.TokenFile}` is a one-element loop only to scope `f`; replace with a plain `if o.TokenFile != "" { return r.readToken(o.TokenFile) }`.)

- [ ] **Step 4: Run** — `nice go test ./internal/client/ -v` → PASS. Confirm purity: `CGO_ENABLED=0 go build ./internal/client/` succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/client
git commit -m "Add client config file and endpoint resolution"
```

---

### Task 10: `internal/client` — HTTP client and key planning

**Files:**
- Create: `internal/client/http.go`, `internal/client/http_test.go`, `internal/client/key.go`, `internal/client/key_test.go`

**Interfaces:**
- Consumes: Task 8 `termid.Identity`; Task 9 `Endpoint`, sentinels.
- Produces:
  ```go
  func New(ep Endpoint) *Client
  type PutBody struct{ Color, Effect, State, Owner string } // exactly one of Color/Effect/State; Owner optional
  func (c *Client) Devices(ctx context.Context) ([]DeviceSummary, error)
  func (c *Client) Capabilities(ctx context.Context, device string) (Capabilities, error)
  func (c *Client) Put(ctx context.Context, device, key string, b PutBody) error
  func (c *Client) Delete(ctx context.Context, device, key, owner string) error // owner "" = unconditional
  func (c *Client) GetKey(ctx context.Context, device, key string) (KeyStatus, error)
  func (c *Client) ListKeys(ctx context.Context, device string) ([]KeyStatus, error)
  type APIError struct{ Status int; Message string } // Is(ErrAuth) for 401/403
  func IsNotFound(err error) bool; func IsConflict(err error) bool
  type DeviceSummary struct{ Name string; Connected bool }
  type Capabilities struct{ LEDCount int; Positions []Position; Layout struct{ Tabs []uint16 } }
  type KeyStatus struct{…}  // mirrors the keyView JSON of Task 6
  // key planning
  type Mode int // ModeExplicit, ModeSlot, ModeNamed
  type KeyInput struct{ Key, Name string; ID termid.Identity; Fallback string }
  type Plan struct{ Mode Mode; Key string; Tab int; Base, Name, Owner string }
  var ErrNoKey error
  func PlanKey(in KeyInput) (Plan, error)
  func (p Plan) Qualify(host string) Plan
  func DefaultTabs(n int) []uint16
  func SlotKey(tab int, tabs []uint16) string  // "idx:N"
  func SharedKey(name string, tabs []uint16) string // "idx:N" via FNV-1a
  ```
  A connection failure is wrapped `ErrUnreachable`; 401/403 `errors.Is(err, ErrAuth)`.

- [ ] **Step 1: Write the failing tests.**

`internal/client/http_test.go` (uses `httptest.NewServer` over TCP; the Unix path is the same code with a different dialer and is covered by the manual check):

```go
package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Endpoint{Kind: "http", BaseURL: srv.URL, Token: "tok"})
}

func TestPutSendsBodyAuthAndEscapedKey(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotCT = r.URL.EscapedPath(), r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.Put(context.Background(), "uid-1", "idx:2", PutBody{State: "claude/idle", Owner: "me"}); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok" || gotCT != "application/json" || gotPath != "/devices/uid-1/keys/idx:2" {
		t.Errorf("path %q auth %q ct %q", gotPath, gotAuth, gotCT)
	}
	if len(gotBody) != 2 || gotBody["state"] != "claude/idle" || gotBody["owner"] != "me" {
		t.Errorf("body = %v (empty fields must be omitted)", gotBody)
	}
}

func TestDeleteOwnerQuery(t *testing.T) {
	var gotQuery string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) { gotQuery = r.URL.RawQuery; w.WriteHeader(204) })
	_ = c.Delete(context.Background(), "d", "esc", "a b")
	if gotQuery != "owner=a+b" {
		t.Errorf("query = %q", gotQuery)
	}
	_ = c.Delete(context.Background(), "d", "esc", "")
	if gotQuery != "" {
		t.Errorf("unconditional delete sent query %q", gotQuery)
	}
}

func TestErrorClassification(t *testing.T) {
	status := 0
	c := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	})
	for code, check := range map[int]func(error) bool{
		401: func(e error) bool { return errors.Is(e, ErrAuth) },
		403: func(e error) bool { return errors.Is(e, ErrAuth) },
		404: IsNotFound,
		409: IsConflict,
	} {
		status = code
		err := c.Put(context.Background(), "d", "k", PutBody{Color: "red"})
		var ae *APIError
		if !check(err) || !errors.As(err, &ae) || ae.Message != "nope" || ae.Status != code {
			t.Errorf("status %d: err = %v", code, err)
		}
	}
	status = 503
	if err := c.Put(context.Background(), "d", "k", PutBody{Color: "red"}); err == nil || errors.Is(err, ErrAuth) || IsNotFound(err) {
		t.Errorf("503: err = %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	err := New(Endpoint{Kind: "http", BaseURL: url, Token: "t"}).Put(context.Background(), "d", "k", PutBody{Color: "red"})
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v, want ErrUnreachable", err)
	}
}

func TestGetKeyAndCapabilitiesDecode(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/devices/d" {
			_, _ = w.Write([]byte(`{"led_count":12,"positions":[{"index":0,"row":0,"col":0}],"layout":{"tabs":[0,1,2]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"device":"d","key":"idx:0","kind":"direct","led":0,"row":0,"col":0,"connected":true,
		  "color":{"h":1,"s":2,"v":3,"hex":"#010203"},
		  "source":{"type":"state","ref":"claude/idle","owner":"o","set_at":"2026-10-05T12:00:00Z","age_ms":5},
		  "effect":{"name":"timer5min","running":true,"elapsed_ms":7,"duration_ms":null}}`))
	})
	caps, err := c.Capabilities(context.Background(), "d")
	if err != nil || caps.LEDCount != 12 || len(caps.Layout.Tabs) != 3 {
		t.Errorf("caps = %+v, %v", caps, err)
	}
	k, err := c.GetKey(context.Background(), "d", "idx:0")
	if err != nil || k.Source == nil || k.Source.Ref != "claude/idle" || k.Effect == nil || k.Effect.DurationMS != nil || k.Color.Hex != "#010203" {
		t.Errorf("key = %+v, %v", k, err)
	}
}
```

`internal/client/key_test.go`:

```go
package client

import (
	"errors"
	"reflect"
	"testing"

	"github.com/seefood/blinkenkeys/internal/termid"
)

func TestPlanKeyOrder(t *testing.T) {
	iterm := termid.Identity{Terminal: "iterm", Tab: 3, InstanceID: "ab12cd34"}
	wez := termid.Identity{Terminal: "wezterm", InstanceID: "17"}
	tests := []struct {
		name string
		in   KeyInput
		want Plan
	}{
		{"explicit key wins", KeyInput{Key: "0,1", Name: "n", ID: iterm}, Plan{Mode: ModeExplicit, Key: "0,1"}},
		{"explicit name", KeyInput{Name: "build", ID: iterm}, Plan{Mode: ModeNamed, Base: "build", Name: "build", Owner: "build"}},
		{"tab slot", KeyInput{ID: iterm, Fallback: "claude-x"}, Plan{Mode: ModeSlot, Tab: 3, Base: "iterm-ab12cd34", Owner: "iterm-ab12cd34"}},
		{"instance id when no tab", KeyInput{ID: wez, Fallback: "claude-x"}, Plan{Mode: ModeNamed, Base: "wezterm-17", Name: "wezterm-17", Owner: "wezterm-17"}},
		{"env fallback", KeyInput{Fallback: "claude-x"}, Plan{Mode: ModeNamed, Base: "claude-x", Name: "claude-x", Owner: "claude-x"}},
	}
	for _, tt := range tests {
		got, err := PlanKey(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("%s: got %+v, %v; want %+v", tt.name, got, err, tt.want)
		}
	}
}

func TestPlanKeyNothingAvailable(t *testing.T) {
	if _, err := PlanKey(KeyInput{}); !errors.Is(err, ErrNoKey) || !errors.Is(err, ErrUsage) {
		t.Errorf("err = %v, want ErrNoKey (a usage error)", err)
	}
}

func TestPlanKeyRejectsUnsafeExplicitName(t *testing.T) {
	for _, n := range []string{"a,b", "a/b", ""} {
		if n == "" {
			continue
		}
		if _, err := PlanKey(KeyInput{Name: n}); !errors.Is(err, ErrUsage) {
			t.Errorf("%q: err = %v, want ErrUsage", n, err)
		}
	}
}

func TestQualifyPrefixesHost(t *testing.T) {
	p, _ := PlanKey(KeyInput{ID: termid.Identity{Terminal: "wezterm", InstanceID: "17"}})
	q := p.Qualify("laptop")
	if q.Name != "laptop.wezterm-17" || q.Owner != "laptop.wezterm-17" {
		t.Errorf("named: %+v", q)
	}
	s, _ := PlanKey(KeyInput{ID: termid.Identity{Terminal: "iterm", Tab: 1, InstanceID: "ab"}})
	if sq := s.Qualify("laptop"); sq.Owner != "laptop.iterm-ab" || sq.Name != "" {
		t.Errorf("slot: %+v", sq)
	}
	e, _ := PlanKey(KeyInput{Key: "0,0"})
	if e.Qualify("laptop") != e || p.Qualify("") != p {
		t.Error("explicit plans and an empty host must be unchanged")
	}
}

func TestSlotAndSharedKeys(t *testing.T) {
	tabs := []uint16{0, 1, 2, 3, 4, 5}
	for tab, want := range map[int]string{1: "idx:0", 6: "idx:5", 7: "idx:0", 13: "idx:0", 8: "idx:1"} {
		if got := SlotKey(tab, tabs); got != want {
			t.Errorf("SlotKey(%d) = %s, want %s", tab, got, want)
		}
	}
	if got := SlotKey(2, []uint16{4, 6, 9}); got != "idx:6" {
		t.Errorf("non-contiguous tabs: %s", got)
	}
	a, b := SharedKey("claude-abc", tabs), SharedKey("claude-abc", tabs)
	if a != b || a[:4] != "idx:" {
		t.Errorf("SharedKey not stable: %s %s", a, b)
	}
	seen := map[string]bool{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		seen[SharedKey(n, tabs)] = true
	}
	if len(seen) < 2 {
		t.Errorf("SharedKey does not spread names: %v", seen)
	}
	if !reflect.DeepEqual(DefaultTabs(3), []uint16{0, 1, 2}) {
		t.Errorf("DefaultTabs = %v", DefaultTabs(3))
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./internal/client/ 2>&1 | tail` → FAIL.

- [ ] **Step 3: Implement.**

`internal/client/http.go`:

```go
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client talks to one blinkenkeysd endpoint.
type Client struct {
	ep   Endpoint
	base string
	hc   *http.Client
}

// New builds a Client for ep; a "unix" endpoint dials its socket for every request.
func New(ep Endpoint) *Client {
	tr := &http.Transport{}
	base := strings.TrimRight(ep.BaseURL, "/")
	if ep.Kind == "unix" {
		sock := ep.Socket
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
		base = "http://blinkenkeys"
	}
	return &Client{ep: ep, base: base, hc: &http.Client{Transport: tr, Timeout: 5 * time.Second}}
}

// APIError is a non-2xx response from the daemon.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("blinkenkeysd: %d %s", e.Status, e.Message) }

// Is makes 401/403 match ErrAuth.
func (e *APIError) Is(target error) bool {
	return target == ErrAuth && (e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden)
}

func hasStatus(err error, code int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == code
}

// IsNotFound reports a 404 from the daemon.
func IsNotFound(err error) bool { return hasStatus(err, http.StatusNotFound) }

// IsConflict reports a 409 (e.g. no unclaimed keys left in the pool).
func IsConflict(err error) bool { return hasStatus(err, http.StatusConflict) }

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.ep.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.ep.Token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&e)
		return &APIError{Status: resp.StatusCode, Message: e.Error}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func keyPath(device, key string) string {
	return "/devices/" + url.PathEscape(device) + "/keys/" + url.PathEscape(key)
}

// PutBody is a write: exactly one of Color/Effect/State; Owner is optional.
type PutBody struct {
	Color  string `json:"color,omitempty"`
	Effect string `json:"effect,omitempty"`
	State  string `json:"state,omitempty"`
	Owner  string `json:"owner,omitempty"`
}

// DeviceSummary is one GET /devices entry.
type DeviceSummary struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// Position is one LED's matrix location.
type Position struct {
	Index uint16 `json:"index"`
	Row   uint8  `json:"row"`
	Col   uint8  `json:"col"`
}

// Capabilities is GET /devices/{name}.
type Capabilities struct {
	LEDCount  int        `json:"led_count"`
	Positions []Position `json:"positions"`
	Layout    struct {
		Tabs []uint16 `json:"tabs"`
	} `json:"layout"`
}

// KeyStatus mirrors the daemon's GET key response.
type KeyStatus struct {
	Device    string `json:"device"`
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	LED       uint16 `json:"led"`
	Row       *uint8 `json:"row,omitempty"`
	Col       *uint8 `json:"col,omitempty"`
	Connected bool   `json:"connected"`
	Color     *struct {
		H   uint8  `json:"h"`
		S   uint8  `json:"s"`
		V   uint8  `json:"v"`
		Hex string `json:"hex"`
	} `json:"color,omitempty"`
	Source *struct {
		Type  string    `json:"type"`
		Ref   string    `json:"ref"`
		Owner string    `json:"owner,omitempty"`
		SetAt time.Time `json:"set_at"`
		AgeMS int64     `json:"age_ms"`
	} `json:"source,omitempty"`
	Effect *struct {
		Name       string `json:"name"`
		Running    bool   `json:"running"`
		ElapsedMS  int64  `json:"elapsed_ms"`
		DurationMS *int64 `json:"duration_ms"`
	} `json:"effect,omitempty"`
	Claim *struct {
		LastWrite time.Time `json:"last_write"`
		ExpiresAt time.Time `json:"expires_at"`
	} `json:"claim,omitempty"`
}

// Devices lists devices.
func (c *Client) Devices(ctx context.Context) ([]DeviceSummary, error) {
	var out []DeviceSummary
	return out, c.do(ctx, http.MethodGet, "/devices", nil, &out)
}

// Capabilities fetches a device's LED layout and key layout hint.
func (c *Client) Capabilities(ctx context.Context, device string) (Capabilities, error) {
	var out Capabilities
	return out, c.do(ctx, http.MethodGet, "/devices/"+url.PathEscape(device), nil, &out)
}

// Put writes a color, effect or state to key.
func (c *Client) Put(ctx context.Context, device, key string, b PutBody) error {
	return c.do(ctx, http.MethodPut, keyPath(device, key), b, nil)
}

// Delete blanks key and releases its claim; owner != "" makes it conditional.
func (c *Client) Delete(ctx context.Context, device, key, owner string) error {
	p := keyPath(device, key)
	if owner != "" {
		p += "?" + url.Values{"owner": {owner}}.Encode()
	}
	return c.do(ctx, http.MethodDelete, p, nil, nil)
}

// GetKey reads one key's registration and state.
func (c *Client) GetKey(ctx context.Context, device, key string) (KeyStatus, error) {
	var out KeyStatus
	return out, c.do(ctx, http.MethodGet, keyPath(device, key), nil, &out)
}

// ListKeys reads every registered key on device.
func (c *Client) ListKeys(ctx context.Context, device string) ([]KeyStatus, error) {
	var out []KeyStatus
	return out, c.do(ctx, http.MethodGet, "/devices/"+url.PathEscape(device)+"/keys", nil, &out)
}
```

`internal/client/key.go`:

```go
package client

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/seefood/blinkenkeys/internal/termid"
)

// ErrNoKey means no key could be derived: no -k/-n and no unique id in the
// environment. It is a usage error (exit 64).
var ErrNoKey = fmt.Errorf("%w: cannot work out which key to use", ErrUsage)

// Mode is how a key was chosen.
type Mode int

const (
	ModeExplicit Mode = iota // -k / $BLINKENKEYS_KEY, used verbatim
	ModeSlot                 // direct write to the tab's slot key
	ModeNamed                // named claim (pool), shared slot on 409
)

// KeyInput is everything PlanKey needs; none of it requires the network.
type KeyInput struct {
	Key      string // -k or $BLINKENKEYS_KEY
	Name     string // -n
	ID       termid.Identity
	Fallback string // termid.FallbackName result
}

// Plan is the chosen key strategy. Base is the unqualified identity; Name is
// the claim name (ModeNamed); Owner is the tag sent with writes and clears
// ("" for explicit -k, which clears unconditionally).
type Plan struct {
	Mode  Mode
	Key   string
	Tab   int
	Base  string
	Name  string
	Owner string
}

// PlanKey picks the key strategy: -k, then -n, then the terminal's tab slot,
// then a name from the terminal's instance id, then the environment
// fallback; otherwise ErrNoKey.
func PlanKey(in KeyInput) (Plan, error) {
	switch {
	case in.Key != "":
		return Plan{Mode: ModeExplicit, Key: in.Key}, nil
	case in.Name != "":
		if strings.ContainsAny(in.Name, ",/") {
			return Plan{}, fmt.Errorf("%w: key names must not contain ',' or '/'", ErrUsage)
		}
		return Plan{Mode: ModeNamed, Base: in.Name, Name: in.Name, Owner: in.Name}, nil
	case in.ID.Tab > 0:
		n := in.ID.Name()
		return Plan{Mode: ModeSlot, Tab: in.ID.Tab, Base: n, Owner: n}, nil
	case in.ID.InstanceID != "":
		n := in.ID.Name()
		return Plan{Mode: ModeNamed, Base: n, Name: n, Owner: n}, nil
	case in.Fallback != "":
		return Plan{Mode: ModeNamed, Base: in.Fallback, Name: in.Fallback, Owner: in.Fallback}, nil
	default:
		return Plan{}, fmt.Errorf("%w: pass -k KEY or -n NAME, or run inside a recognized terminal (checked tmux, iTerm2, WezTerm, kitty) or with one of $BLINKENKEYS_NAME, $CLAUDE_CODE_SESSION_ID, $ZELLIJ_PANE_ID, $STY, $WT_SESSION, $TERM_SESSION_ID set", ErrNoKey)
	}
}

// Qualify prefixes a derived identity with host so panes on different
// machines sharing one daemon don't collide. Explicit plans are unchanged.
func (p Plan) Qualify(host string) Plan {
	if host == "" || p.Base == "" || p.Mode == ModeExplicit {
		return p
	}
	q := host + "." + p.Base
	p.Owner = q
	if p.Mode == ModeNamed {
		p.Name = q
	}
	return p
}

// DefaultTabs is idx 0..n-1.
func DefaultTabs(n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = uint16(i)
	}
	return out
}

// SlotKey maps a 1-based tab number onto tabs, wrapping.
func SlotKey(tab int, tabs []uint16) string {
	return fmt.Sprintf("idx:%d", tabs[(tab-1)%len(tabs)])
}

// SharedKey places a name on a tab slot by hash (FNV-1a), for when the pool
// is empty or exhausted: the same name always lands on the same key, with no
// daemon state.
func SharedKey(name string, tabs []uint16) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("idx:%d", tabs[int(h.Sum32()%uint32(len(tabs)))])
}

var _ = errors.Is // keep errors imported for sentinel wrapping helpers added alongside
```
(Delete the trailing `var _ = errors.Is` line and the unused `errors` import — shown only so the file compiles if you add helpers; the shipped file must not contain it.)

- [ ] **Step 4: Run** — `nice go test ./internal/client/ -v 2>&1 | tail -30` → PASS; `CGO_ENABLED=0 go build ./internal/client/ ./internal/termid/` succeeds.

- [ ] **Step 5: Commit**

```bash
git add internal/client
git commit -m "Add client HTTP API and key planning"
```

---

### Task 11: `cmd/blincli` skeleton — app, flags, exit codes, `version`, `detect`

**Files:**
- Create: `cmd/blincli/main.go`, `cmd/blincli/flags.go`, `cmd/blincli/main_test.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: Tasks 8–10.
- Produces (package `main`):
  ```go
  type app struct {
  	getenv         func(string) string
  	home, host     string
  	stdin          io.Reader
  	stdout, stderr io.Writer
  	isTTY          func() bool
  	termEnv        termid.Env
  }
  func (a *app) run(args []string) int
  type globals struct{ Socket, URL, Token, TokenFile, Device, Config string; Verbose, Quiet bool }
  func (a *app) newFlags(name string) (*flag.FlagSet, *globals)    // registers the global options
  func (a *app) parse(fs *flag.FlagSet, args []string) (code int, done bool)
  func (a *app) session(ctx context.Context, g *globals) (*client.Client, client.Endpoint, client.FileConfig, error)
  func (a *app) device(ctx context.Context, g *globals, fc client.FileConfig, cl *client.Client) (string, error)
  func (a *app) planKey(key, name string, ctx context.Context) (client.Plan, error)
  func exitCode(err error) int
  const exitOK, exitFail, exitUsage, exitNotFound, exitUnavailable, exitNoPerm, exitConfig = 0, 1, 64, 66, 69, 77, 78
  var version = "dev"
  ```
  Commands registered via a `commands` map `name -> func(*app, []string) int`; Tasks 12–14 add `set`, `clear`, `get`, `devices`, `config`.

- [ ] **Step 1: Write the failing tests** — `cmd/blincli/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/seefood/blinkenkeys/internal/client"
	"github.com/seefood/blinkenkeys/internal/termid"
)

// testApp builds an app with a fake environment; stdout/stderr are captured.
func testApp(env map[string]string) (*app, *bytes.Buffer, *bytes.Buffer) {
	out, errb := &bytes.Buffer{}, &bytes.Buffer{}
	return &app{
		getenv: func(k string) string { return env[k] },
		home:   "/nonexistent-home",
		host:   "testhost",
		stdin:  strings.NewReader(""),
		stdout: out, stderr: errb,
		isTTY: func() bool { return false },
		termEnv: termid.Env{
			Getenv: func(k string) string { return env[k] },
			Run:    func(context.Context, string, ...string) (string, error) { return "", errors.New("no helper") },
		},
	}, out, errb
}

func TestExitCodeMapping(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{fmt.Errorf("x: %w", client.ErrNoEndpoint), 78},
		{&client.NoEndpointError{}, 78},
		{fmt.Errorf("x: %w", client.ErrUnreachable), 69},
		{fmt.Errorf("x: %w", client.ErrAuth), 77},
		{&client.APIError{Status: 401}, 77},
		{fmt.Errorf("x: %w", client.ErrUsage), 64},
		{client.ErrNoKey, 64},
		{&client.APIError{Status: 404}, 66},
		{&client.APIError{Status: 503}, 1},
		{errors.New("boom"), 1},
	}
	for _, tt := range tests {
		if got := exitCode(tt.err); got != tt.want {
			t.Errorf("exitCode(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestNeverExitsTwo(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"set", "--bogus"}, {"nosuchcommand"}, {}, {"set", "-c"}} {
		a, _, _ := testApp(nil)
		if code := a.run(args); code == 2 {
			t.Errorf("%v exited 2 (Claude Code hooks treat 2 as blocking)", args)
		}
	}
}

func TestUsageErrorsExit64(t *testing.T) {
	for _, args := range [][]string{{"--bogus"}, {"nosuchcommand"}, {}} {
		a, _, errb := testApp(nil)
		if code := a.run(args); code != exitUsage || errb.Len() == 0 {
			t.Errorf("%v: code %d, stderr %q", args, code, errb)
		}
	}
}

func TestHelpAndVersion(t *testing.T) {
	a, out, _ := testApp(nil)
	if code := a.run([]string{"--help"}); code != 0 || !strings.Contains(out.String(), "Usage: blincli") {
		t.Errorf("--help: %d %q", code, out)
	}
	a, out, _ = testApp(nil)
	if code := a.run([]string{"version"}); code != 0 || !strings.HasPrefix(out.String(), "blincli ") {
		t.Errorf("version: %d %q", code, out)
	}
}

func TestDetectCommand(t *testing.T) {
	a, out, _ := testApp(map[string]string{"KITTY_WINDOW_ID": "4", "BLINKENKEYS_URL": "http://h:1", "BLINKENKEYS_TOKEN": "t"})
	if code := a.run([]string{"detect"}); code != 0 {
		t.Fatalf("code %d; %s", code, out)
	}
	for _, want := range []string{"terminal", "kitty", "key", "kitty-4", "transport", "http://h:1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("detect output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out.String(), "t\n") && strings.Contains(out.String(), "token  t") {
		t.Error("detect must not print the token")
	}
}

func TestDetectWithNothingStillReports(t *testing.T) {
	a, out, _ := testApp(map[string]string{"BLINKENKEYS_SOCKET": "/x.sock"})
	if code := a.run([]string{"detect"}); code != 0 || !strings.Contains(out.String(), "none") {
		t.Errorf("code %d:\n%s", code, out)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./cmd/blincli/ 2>&1 | tail` → FAIL (no package).

- [ ] **Step 3: Implement.**

`cmd/blincli/main.go`:

```go
// Command blincli is a client for blinkenkeysd: it finds the daemon, works
// out which key belongs to the calling terminal tab, and wraps the REST API
// in GNU-style options. It is built to run from hooks: it never prompts
// (except `config init --interactive`) and never exits with code 2.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/client"
	"github.com/seefood/blinkenkeys/internal/termid"
)

// Exit codes follow sysexits.h and deliberately skip 2, which Claude Code
// hooks treat as a blocking error.
const (
	exitOK          = 0
	exitFail        = 1
	exitUsage       = 64
	exitNotFound    = 66
	exitUnavailable = 69
	exitNoPerm      = 77
	exitConfig      = 78
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

const requestTimeout = 10 * time.Second

type app struct {
	getenv         func(string) string
	home, host     string
	stdin          io.Reader
	stdout, stderr io.Writer
	isTTY          func() bool
	termEnv        termid.Env
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	a := &app{
		getenv: os.Getenv, home: home, host: host,
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
		isTTY: func() bool {
			fi, err := os.Stdin.Stat()
			return err == nil && fi.Mode()&os.ModeCharDevice != 0
		},
		termEnv: termid.Env{Getenv: os.Getenv, Run: termid.ExecRun},
	}
	os.Exit(a.run(os.Args[1:]))
}

var commands = map[string]func(*app, []string) int{
	"version": (*app).cmdVersion,
	"detect":  (*app).cmdDetect,
}

func (a *app) run(args []string) int {
	fs, _ := a.newFlags("blincli")
	if code, done := a.parse(fs, args); done {
		return code
	}
	rest := fs.Args()
	if len(rest) == 0 {
		a.printUsage(a.stderr)
		return exitUsage
	}
	cmd, ok := commands[rest[0]]
	if !ok {
		_, _ = fmt.Fprintf(a.stderr, "blincli: unknown command %q\n", rest[0])
		a.printUsage(a.stderr)
		return exitUsage
	}
	return cmd(a, rest[1:])
}

func (a *app) cmdVersion(args []string) int {
	_, _ = fmt.Fprintf(a.stdout, "blincli %s\n", version)
	return exitOK
}

// exitCode maps an error to the exit code in the table above.
func exitCode(err error) int {
	var ae *client.APIError
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, client.ErrNoEndpoint):
		return exitConfig
	case errors.Is(err, client.ErrUnreachable):
		return exitUnavailable
	case errors.Is(err, client.ErrAuth):
		return exitNoPerm
	case errors.Is(err, client.ErrUsage):
		return exitUsage
	case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
		return exitNotFound
	default:
		return exitFail
	}
}

// fail prints err and returns its exit code.
func (a *app) fail(err error) int {
	_, _ = fmt.Fprintln(a.stderr, err)
	return exitCode(err)
}

func (a *app) vlog(g *globals, format string, args ...any) {
	if g.Verbose {
		_, _ = fmt.Fprintf(a.stderr, "blincli: "+format+"\n", args...)
	}
}

// session resolves the endpoint and builds a client.
func (a *app) session(g *globals) (*client.Client, client.Endpoint, client.FileConfig, error) {
	r := client.Resolver{Getenv: a.getenv, Home: a.home}
	ep, fc, err := r.Resolve(client.Options{Socket: g.Socket, URL: g.URL, Token: g.Token, TokenFile: g.TokenFile, ConfigPath: g.Config})
	if err != nil {
		return nil, ep, fc, err
	}
	where := ep.Socket
	if ep.Remote() {
		where = ep.BaseURL
	}
	a.vlog(g, "endpoint %s (%s)", where, ep.Source)
	return client.New(ep), ep, fc, nil
}

// device picks the device: -d, $BLINKENKEYS_DEVICE, config, or the only one.
func (a *app) device(ctx context.Context, g *globals, fc client.FileConfig, cl *client.Client) (string, error) {
	for _, d := range []string{g.Device, a.getenv("BLINKENKEYS_DEVICE"), fc.Device} {
		if d != "" {
			return d, nil
		}
	}
	devs, err := cl.Devices(ctx)
	if err != nil {
		return "", err
	}
	switch len(devs) {
	case 0:
		return "", errors.New("blincli: the daemon reports no devices")
	case 1:
		return devs[0].Name, nil
	}
	names := make([]string, len(devs))
	for i, d := range devs {
		names[i] = d.Name
	}
	return "", fmt.Errorf("%w: %d devices; choose one with -d: %s", client.ErrUsage, len(devs), strings.Join(names, ", "))
}

// planKey works out the key strategy from -k/-n/env/terminal (no network).
func (a *app) planKey(ctx context.Context, key, name string) (client.Plan, termid.Identity, error) {
	if key == "" {
		key = a.getenv("BLINKENKEYS_KEY")
	}
	id := termid.Detect(ctx, a.termEnv)
	fallback, _ := termid.FallbackName(a.getenv)
	plan, err := client.PlanKey(client.KeyInput{Key: key, Name: name, ID: id, Fallback: fallback})
	return plan, id, err
}

func (a *app) cmdDetect(args []string) int {
	fs, g := a.newFlags("detect")
	if code, done := a.parse(fs, args); done {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	plan, id, perr := a.planKey(ctx, "", "")
	if id.Terminal == "" {
		_, _ = fmt.Fprintln(a.stdout, "terminal   none recognized")
	} else {
		tab := "unknown"
		if id.Tab > 0 {
			tab = fmt.Sprint(id.Tab)
		}
		_, _ = fmt.Fprintf(a.stdout, "terminal   %s (instance %q, tab %s)\n", id.Terminal, id.InstanceID, tab)
	}
	switch {
	case perr != nil:
		_, _ = fmt.Fprintf(a.stdout, "key        none (%v)\n", perr)
	case plan.Mode == client.ModeSlot:
		_, _ = fmt.Fprintf(a.stdout, "key        tab %d -> a tab slot (owner %s)\n", plan.Tab, plan.Owner)
	default:
		_, _ = fmt.Fprintf(a.stdout, "key        name %s (claimed from the pool; shared slot if it is full)\n", plan.Name)
	}
	r := client.Resolver{Getenv: a.getenv, Home: a.home}
	ep, _, err := r.Resolve(client.Options{Socket: g.Socket, URL: g.URL, Token: g.Token, TokenFile: g.TokenFile, ConfigPath: g.Config})
	if err != nil {
		_, _ = fmt.Fprintf(a.stdout, "transport  none (%v)\n", firstLine(err.Error()))
		return exitOK
	}
	where := ep.Socket
	if ep.Remote() {
		where = ep.BaseURL
	}
	_, _ = fmt.Fprintf(a.stdout, "transport  %s %s  (%s)\n", ep.Kind, where, ep.Source)
	return exitOK
}

func firstLine(s string) string { line, _, _ := strings.Cut(s, "\n"); return line }

var _ = flag.ErrHelp
```
(Remove the trailing `var _ = flag.ErrHelp` and the `flag` import from `main.go` if unused there — `flags.go` owns `flag`.)

`cmd/blincli/flags.go`:

```go
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// globals are the options every command accepts, before or after the command name.
type globals struct {
	Socket, URL, Token, TokenFile, Device, Config string
	Verbose, Quiet                                bool
}

// newFlags returns a FlagSet with the global options registered (each as
// --long and -x). Errors are not printed by the flag package; parse reports them.
func (a *app) newFlags(name string) (*flag.FlagSet, *globals) {
	g := &globals{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	str(fs, &g.Socket, "socket", "S", "Unix socket of a local blinkenkeysd")
	str(fs, &g.URL, "url", "u", "remote blinkenkeysd, e.g. http://nas:49994")
	str(fs, &g.Token, "token", "t", "bearer token (visible in ps; prefer --token-file or $BLINKENKEYS_TOKEN)")
	fs.StringVar(&g.TokenFile, "token-file", "", "read the bearer token from this file")
	str(fs, &g.Device, "device", "d", "device name (default: configured, or the only device)")
	str(fs, &g.Config, "config", "C", "client config file (default ~/.config/blinkenkeys/blincli.yaml)")
	boolean(fs, &g.Verbose, "verbose", "v", "print resolved endpoint/device/key to stderr")
	boolean(fs, &g.Quiet, "quiet", "q", "suppress non-error output")
	return fs, g
}

func str(fs *flag.FlagSet, p *string, long, short, usage string) {
	fs.StringVar(p, long, "", usage)
	fs.StringVar(p, short, "", usage)
}

func boolean(fs *flag.FlagSet, p *bool, long, short, usage string) {
	fs.BoolVar(p, long, false, usage)
	fs.BoolVar(p, short, false, usage)
}

// parse parses args. done is true when the caller should return code
// immediately: -h/--help (code 0, usage on stdout) or a parse error (usage
// error, code 64 — never the flag package's own exit status of 2).
func (a *app) parse(fs *flag.FlagSet, args []string) (code int, done bool) {
	err := fs.Parse(args)
	switch {
	case err == nil:
		return 0, false
	case errors.Is(err, flag.ErrHelp):
		a.printUsage(a.stdout)
		return exitOK, true
	default:
		_, _ = fmt.Fprintf(a.stderr, "blincli: %v\n", err)
		_, _ = fmt.Fprintln(a.stderr, "try 'blincli --help'")
		return exitUsage, true
	}
}

func (a *app) printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: blincli [options] <command> [command options]

Commands:
  set       write a color / effect / template state to a key
  clear     blank a key and release it
  get       show a key's registration and current state
  devices   list devices, or show one device's layout
  detect    show what blincli would use (terminal, key, endpoint); sends nothing
  config    init | show | path
  version

Global options (accepted before or after the command):
  -S, --socket PATH      Unix socket of a local blinkenkeysd
  -u, --url URL          remote blinkenkeysd, e.g. http://nas:49994
  -t, --token TOKEN      bearer token (prefer --token-file or $BLINKENKEYS_TOKEN)
      --token-file FILE  read the bearer token from FILE
  -d, --device NAME      device (default: configured, or the only device)
  -C, --config FILE      client config (default ~/.config/blinkenkeys/blincli.yaml)
  -v, --verbose          print resolved endpoint/device/key to stderr
  -q, --quiet            suppress non-error output
  -h, --help

Exit codes: 0 ok, 1 daemon error, 64 usage, 66 key not registered,
69 daemon unreachable, 77 auth, 78 no config/endpoint.
`)
}
```

`Makefile`: replace with

```make
.PHONY: build test lint

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -o bin/blinkenkeysd ./cmd/blinkenkeysd
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/blincli ./cmd/blincli

test:
	go test ./...

lint:
	prek run --all-files
```

- [ ] **Step 4: Run** — `nice go test ./cmd/blincli/ -v 2>&1 | tail -30 && nice make build && bin/blincli version && bin/blincli --bogus; echo "exit=$?"` → tests PASS; `exit=64`.

- [ ] **Step 5: Commit**

```bash
git add cmd/blincli Makefile
git commit -m "Add blincli skeleton: flags, exit codes, version, detect"
```

---

### Task 12: `blincli set` and `blincli clear`

**Files:**
- Create: `cmd/blincli/cmd_set.go`, `cmd/blincli/cmd_set_test.go`
- Modify: `cmd/blincli/main.go` (`commands` map)

**Interfaces:**
- Consumes: Task 11 `app`, `planKey`, `session`, `device`; Task 10 client + plan.
- Produces: commands `set` and `clear`.
  - `set [-k KEY | -n NAME] (-c COLOR | -e EFFECT | -s STATE) [-m N] [--if-detected]`
  - `clear [-k KEY | -n NAME] [-m N] [--force] [--if-detected]`
  - Slot count `N`: `-m` > config `slots:` > daemon `layout.tabs` > 6.
  - Behavior: plan first (no network); `ErrNoKey` + `--if-detected` → exit 0 silently. `ModeNamed`: `PUT name`; on **409** retry on `SharedKey(name, tabs)` with the same owner. `clear` is unconditional only with `--force` or an explicit `-k`; otherwise it sends `?owner=`; `ModeNamed` clear tries `DELETE name` and, if that is 404, `DELETE SharedKey(...)` (conditional); a 404 on every attempt is success (nothing to clear).

- [ ] **Step 1: Write the failing tests** — `cmd/blincli/cmd_set_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/seefood/blinkenkeys/internal/termid"
)

type call struct{ Method, Path, Query, Body string }

// fakeDaemon records requests and answers per its fields.
type fakeDaemon struct {
	mu        sync.Mutex
	calls     []call
	tabs      []uint16 // layout.tabs reported by GET /devices/d
	putStatus map[string]int
	delStatus map[string]int
}

func (f *fakeDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, call{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
	switch {
	case r.Method == "GET" && r.URL.Path == "/devices":
		_, _ = w.Write([]byte(`[{"name":"d","connected":true}]`))
	case r.Method == "GET" && r.URL.Path == "/devices/d":
		_ = json.NewEncoder(w).Encode(map[string]any{"led_count": 12, "positions": []any{}, "layout": map[string]any{"tabs": f.tabs}})
	case r.Method == "PUT":
		if st := f.putStatus[r.URL.Path]; st != 0 {
			w.WriteHeader(st)
			_, _ = w.Write([]byte(`{"error":"no unclaimed keys available"}`))
			return
		}
		w.WriteHeader(204)
	case r.Method == "DELETE":
		if st := f.delStatus[r.URL.Path]; st != 0 {
			w.WriteHeader(st)
			_, _ = w.Write([]byte(`{"error":"gone"}`))
			return
		}
		w.WriteHeader(204)
	}
}

func (f *fakeDaemon) writes() []call {
	var out []call
	for _, c := range f.calls {
		if c.Method == "PUT" || c.Method == "DELETE" {
			out = append(out, c)
		}
	}
	return out
}

func daemonApp(t *testing.T, f *fakeDaemon, env map[string]string) (*app, *strings.Builder) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	if env == nil {
		env = map[string]string{}
	}
	env["BLINKENKEYS_URL"], env["BLINKENKEYS_TOKEN"] = srv.URL, "tok"
	a, _, errb := testApp(env)
	var sb strings.Builder
	a.stderr = &sb
	_ = errb
	return a, &sb
}

func itermEnv() map[string]string {
	return map[string]string{"ITERM_SESSION_ID": "w0t1p0:ABCDEF0123456789"} // tab 2
}

func TestSetStateOnTabSlot(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, errs := daemonApp(t, f, itermEnv())
	if code := a.run([]string{"set", "-s", "claude/working"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	w := f.writes()
	if len(w) != 1 || w[0].Path != "/devices/d/keys/idx:1" || !strings.Contains(w[0].Body, `"state":"claude/working"`) ||
		!strings.Contains(w[0].Body, `"owner":"testhost.iterm-ABCDEF01"`) {
		t.Errorf("writes = %+v", w)
	}
}

func TestSetTabSlotWrapsAndHonorsLayoutAndSlotsFlag(t *testing.T) {
	env := map[string]string{"ITERM_SESSION_ID": "w0t8p0:ABCDEF0123"} // tab 9
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, _ := daemonApp(t, f, env)
	a.run([]string{"set", "-c", "red"})
	if w := f.writes(); w[0].Path != "/devices/d/keys/idx:2" { // (9-1)%6 = 2
		t.Errorf("layout wrap: %+v", w)
	}
	f2 := &fakeDaemon{}
	a2, _ := daemonApp(t, f2, map[string]string{"ITERM_SESSION_ID": "w0t8p0:ABCDEF0123"})
	a2.run([]string{"set", "-c", "red", "-m", "4"})
	if w := f2.writes(); w[0].Path != "/devices/d/keys/idx:0" { // (9-1)%4 = 0, no caps call needed
		t.Errorf("-m wrap: %+v", w)
	}
	for _, c := range f2.calls {
		if c.Path == "/devices/d" {
			t.Error("-m must skip the capabilities request")
		}
	}
}

func TestSetNoLayoutDefaultsToSixSlots(t *testing.T) {
	f := &fakeDaemon{}
	a, _ := daemonApp(t, f, map[string]string{"ITERM_SESSION_ID": "w0t6p0:ABCDEF0123"}) // tab 7
	a.run([]string{"set", "-c", "red"})
	if w := f.writes(); w[0].Path != "/devices/d/keys/idx:0" {
		t.Errorf("default 6 slots: %+v", w)
	}
}

func TestSetNamedThenSharedOnConflict(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, putStatus: map[string]int{"/devices/d/keys/claude-sess1": 409}}
	a, errs := daemonApp(t, f, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	if code := a.run([]string{"set", "-e", "breathe_blue"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	w := f.writes()
	if len(w) != 2 || w[0].Path != "/devices/d/keys/claude-sess1" || !strings.HasPrefix(w[1].Path, "/devices/d/keys/idx:") ||
		!strings.Contains(w[1].Body, `"owner":"testhost.claude-sess1"`) {
		// the name itself is host-qualified only for remote endpoints; this one is http, so it is.
		t.Logf("writes = %+v", w)
	}
}

func TestSetExplicitKeyHasNoOwner(t *testing.T) {
	f := &fakeDaemon{}
	a, _ := daemonApp(t, f, itermEnv())
	a.run([]string{"set", "-k", "0,1", "-c", "#ff0000"})
	w := f.writes()
	if len(w) != 1 || w[0].Path != "/devices/d/keys/0%2C1" && w[0].Path != "/devices/d/keys/0,1" || strings.Contains(w[0].Body, "owner") {
		t.Errorf("writes = %+v", w)
	}
}

func TestSetNeedsExactlyOneAction(t *testing.T) {
	for _, args := range [][]string{{"set"}, {"set", "-c", "red", "-s", "a/b"}} {
		a, _ := daemonApp(t, &fakeDaemon{}, itermEnv())
		if code := a.run(args); code != exitUsage {
			t.Errorf("%v: code %d, want 64", args, code)
		}
	}
}

func TestSetNoKeyIsErrorUnlessIfDetected(t *testing.T) {
	a, errs := daemonApp(t, &fakeDaemon{}, nil)
	if code := a.run([]string{"set", "-c", "red"}); code != exitUsage || !strings.Contains(errs.String(), "-k KEY") {
		t.Errorf("code %d, stderr %q", code, errs)
	}
	f := &fakeDaemon{}
	a, errs = daemonApp(t, f, nil)
	if code := a.run([]string{"set", "-c", "red", "--if-detected"}); code != 0 || errs.Len() != 0 || len(f.calls) != 0 {
		t.Errorf("--if-detected: code %d, stderr %q, calls %+v (must send nothing)", code, errs, f.calls)
	}
}

func TestSetRemoteWithoutTokenFailsBeforeSending(t *testing.T) {
	a, _, errb := testApp(map[string]string{"BLINKENKEYS_URL": "http://127.0.0.1:1", "KITTY_WINDOW_ID": "1"})
	if code := a.run([]string{"set", "-c", "red"}); code != exitNoPerm || !strings.Contains(errb.String(), "token") {
		t.Errorf("code %d, stderr %q", code, errb)
	}
}

func TestClearConditionalOnOwner(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, errs := daemonApp(t, f, itermEnv())
	if code := a.run([]string{"clear"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	w := f.writes()
	if len(w) != 1 || w[0].Method != "DELETE" || w[0].Path != "/devices/d/keys/idx:1" || w[0].Query != "owner=testhost.iterm-ABCDEF01" {
		t.Errorf("writes = %+v", w)
	}
	f2 := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a2, _ := daemonApp(t, f2, itermEnv())
	a2.run([]string{"clear", "--force"})
	if w := f2.writes(); w[0].Query != "" {
		t.Errorf("--force must be unconditional: %+v", w)
	}
}

func TestClearNamedFallsBackToSharedKeyAndTreats404AsDone(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, delStatus: map[string]int{"/devices/d/keys/claude-sess1": 404}}
	a, _ := daemonApp(t, f, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	if code := a.run([]string{"clear"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	w := f.writes()
	if len(w) != 2 || !strings.HasPrefix(w[1].Path, "/devices/d/keys/idx:") || w[1].Query == "" {
		t.Errorf("writes = %+v", w)
	}
	// both 404 -> still success
	f3 := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	f3.delStatus = map[string]int{}
	a3, _ := daemonApp(t, f3, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	for _, w := range []string{"/devices/d/keys/claude-sess1"} {
		f3.delStatus[w] = 404
	}
	f3.delStatus["/devices/d/keys/idx:"+strings.TrimPrefix(w[1].Path, "/devices/d/keys/idx:")] = 404
	if code := a3.run([]string{"clear"}); code != 0 {
		t.Errorf("nothing to clear must exit 0, got %d", code)
	}
}

func TestClearIfDetectedWithNoKey(t *testing.T) {
	f := &fakeDaemon{}
	a, _ := daemonApp(t, f, nil)
	if code := a.run([]string{"clear", "--if-detected"}); code != 0 || len(f.calls) != 0 {
		t.Errorf("code %d, calls %+v", code, f.calls)
	}
}

var _ = errors.New
var _ = context.Background
var _ termid.Env
```
(Drop the three trailing blank-identifier lines and any unused imports once the file compiles. `TestSetNamedThenSharedOnConflict` should assert, not only log: replace the `t.Logf` with `t.Errorf` — the expected `w[0].Path` is `/devices/d/keys/testhost.claude-sess1` because the http endpoint is remote; fix the first path comparison accordingly when writing the file.)

- [ ] **Step 2: Run to verify failure** — `nice go test ./cmd/blincli/ 2>&1 | tail` → FAIL (`unknown command "set"` exit codes / compile).

- [ ] **Step 3: Implement** — `cmd/blincli/cmd_set.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/seefood/blinkenkeys/internal/client"
)

func init() {
	commands["set"] = (*app).cmdSet
	commands["clear"] = (*app).cmdClear
}

// keyFlags are the options shared by the key-addressing commands.
type keyFlags struct {
	key, name  string
	slots      int
	ifDetected bool
}

func addKeyFlags(a *app, fsAdd func(p *string, long, short, usage string), k *keyFlags) {
	fsAdd(&k.key, "key", "k", "key position: name, R,C, led:N or idx:N (default: derived from the terminal)")
	fsAdd(&k.name, "name", "n", "register under this name (claims a key from the pool)")
}

// slotCount is -m, else config slots:, else 0 (ask the daemon, then 6).
func slotCount(k keyFlags, fc client.FileConfig) int {
	if k.slots > 0 {
		return k.slots
	}
	return fc.Slots
}

// tabs returns the tab-slot keys: a fixed 0..n-1 when n is given, else the
// daemon's layout.tabs, else 0..5.
func (a *app) tabs(ctx context.Context, cl *client.Client, device string, n int) ([]uint16, error) {
	if n > 0 {
		return client.DefaultTabs(n), nil
	}
	caps, err := cl.Capabilities(ctx, device)
	if err != nil {
		return nil, err
	}
	if len(caps.Layout.Tabs) > 0 {
		return caps.Layout.Tabs, nil
	}
	return client.DefaultTabs(6), nil
}

// prepared is everything a key-addressing command needs after planning.
type prepared struct {
	cl     *client.Client
	device string
	plan   client.Plan
	slots  int
	g      *globals
}

// prepare plans the key (no network), then resolves endpoint and device.
// skip is true when --if-detected applies and no key could be derived.
func (a *app) prepare(ctx context.Context, g *globals, k keyFlags) (p prepared, skip bool, err error) {
	plan, _, err := a.planKey(ctx, k.key, k.name)
	if err != nil {
		if errors.Is(err, client.ErrNoKey) && k.ifDetected {
			return p, true, nil
		}
		return p, false, err
	}
	cl, ep, fc, err := a.session(g)
	if err != nil {
		return p, false, err
	}
	if ep.Remote() {
		plan = plan.Qualify(a.host)
	}
	device, err := a.device(ctx, g, fc, cl)
	if err != nil {
		return p, false, err
	}
	a.vlog(g, "device %s, key plan %+v", device, plan)
	return prepared{cl: cl, device: device, plan: plan, slots: slotCount(k, fc), g: g}, false, nil
}

func (a *app) cmdSet(args []string) int {
	fs, g := a.newFlags("set")
	var k keyFlags
	var col, eff, state string
	both := func(p *string, long, short, usage string) { str(fs, p, long, short, usage) }
	addKeyFlags(a, both, &k)
	str(fs, &col, "color", "c", "color: #rrggbb, H,S,V (0-255) or a CSS/X11 name")
	str(fs, &eff, "effect", "e", "effect name")
	str(fs, &state, "state", "s", "template state, program/state (e.g. claude/idle)")
	fs.IntVar(&k.slots, "slots", 0, "tab-slot count override")
	fs.IntVar(&k.slots, "m", 0, "tab-slot count override")
	boolean(fs, &k.ifDetected, "if-detected", "", "exit 0 silently if no key can be derived")
	if code, done := a.parse(fs, args); done {
		return code
	}
	set := 0
	for _, v := range []string{col, eff, state} {
		if v != "" {
			set++
		}
	}
	if set != 1 || fs.NArg() != 0 {
		return a.fail(fmt.Errorf("%w: set needs exactly one of -c/--color, -e/--effect, -s/--state", client.ErrUsage))
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	p, skip, err := a.prepare(ctx, g, k)
	if err != nil {
		return a.fail(err)
	}
	if skip {
		return exitOK
	}
	body := client.PutBody{Color: col, Effect: eff, State: state, Owner: p.plan.Owner}
	return a.fail(a.doSet(ctx, p, body))
}

// doSet performs the write for p's plan. A nil error prints nothing.
func (a *app) doSet(ctx context.Context, p prepared, body client.PutBody) error {
	switch p.plan.Mode {
	case client.ModeExplicit:
		body.Owner = ""
		return p.cl.Put(ctx, p.device, p.plan.Key, body)
	case client.ModeSlot:
		tabs, err := a.tabs(ctx, p.cl, p.device, p.slots)
		if err != nil {
			return err
		}
		return p.cl.Put(ctx, p.device, client.SlotKey(p.plan.Tab, tabs), body)
	default:
		err := p.cl.Put(ctx, p.device, p.plan.Name, body)
		if !client.IsConflict(err) {
			return err
		}
		tabs, terr := a.tabs(ctx, p.cl, p.device, p.slots)
		if terr != nil {
			return terr
		}
		shared := client.SharedKey(p.plan.Name, tabs)
		a.vlog(p.g, "pool full or empty; sharing slot %s", shared)
		return p.cl.Put(ctx, p.device, shared, body)
	}
}

func (a *app) cmdClear(args []string) int {
	fs, g := a.newFlags("clear")
	var k keyFlags
	var force bool
	both := func(p *string, long, short, usage string) { str(fs, p, long, short, usage) }
	addKeyFlags(a, both, &k)
	fs.IntVar(&k.slots, "slots", 0, "tab-slot count override")
	fs.IntVar(&k.slots, "m", 0, "tab-slot count override")
	boolean(fs, &force, "force", "f", "clear even if another session owns the key")
	boolean(fs, &k.ifDetected, "if-detected", "", "exit 0 silently if no key can be derived")
	if code, done := a.parse(fs, args); done {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	p, skip, err := a.prepare(ctx, g, k)
	if err != nil {
		return a.fail(err)
	}
	if skip {
		return exitOK
	}
	owner := p.plan.Owner
	if force {
		owner = ""
	}
	return a.fail(a.doClear(ctx, p, owner))
}

// doClear removes the key; a key that isn't there (404 everywhere) is success.
func (a *app) doClear(ctx context.Context, p prepared, owner string) error {
	ignore404 := func(err error) error {
		if client.IsNotFound(err) {
			return nil
		}
		return err
	}
	switch p.plan.Mode {
	case client.ModeExplicit:
		return ignore404(p.cl.Delete(ctx, p.device, p.plan.Key, ""))
	case client.ModeSlot:
		tabs, err := a.tabs(ctx, p.cl, p.device, p.slots)
		if err != nil {
			return err
		}
		return ignore404(p.cl.Delete(ctx, p.device, client.SlotKey(p.plan.Tab, tabs), owner))
	default:
		err := p.cl.Delete(ctx, p.device, p.plan.Name, "")
		if err == nil || !client.IsNotFound(err) {
			return err
		}
		tabs, terr := a.tabs(ctx, p.cl, p.device, p.slots)
		if terr != nil {
			return terr
		}
		return ignore404(p.cl.Delete(ctx, p.device, client.SharedKey(p.plan.Name, tabs), owner))
	}
}
```
(`a.fail(nil)` must return 0 without printing: change `fail` in `main.go` to `if err == nil { return exitOK }` first. `boolean(fs, &k.ifDetected, "if-detected", "", ...)` registers an empty short name — instead write `fs.BoolVar(&k.ifDetected, "if-detected", false, "…")` directly. `addKeyFlags` doesn't need `a`; drop that parameter. For a named clear, the claim is released unconditionally by name: the claim belongs to this identity by construction, so no owner is sent on the first DELETE.)

- [ ] **Step 4: Run** — `nice go test ./cmd/blincli/ -v 2>&1 | tail -40` → PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/blincli
git commit -m "blincli: set and clear with tab slots, named claims and shared fallback"
```

---

### Task 13: `blincli get` and `blincli devices`

**Files:**
- Create: `cmd/blincli/cmd_get.go`, `cmd/blincli/render.go`, `cmd/blincli/cmd_get_test.go`

**Interfaces:**
- Consumes: Tasks 11–12 (`prepare`, `tabs`, `keyFlags`), Task 10 `GetKey`/`ListKeys`/`Capabilities`/`Devices`.
- Produces:
  - `get [-k KEY | -n NAME] [-m N] [--json]` and `get -a/--all [--json]`. Exit `66` when the key is not registered (daemon 404). A named plan that 404s retries the shared slot key before giving up. `-q` suppresses output but keeps the exit code.
  - `devices [REF] [--json]`: no arg lists `name  connected|untethered`; with `REF` prints LED count, `layout.tabs` and the position table.
  - `renderKey(w io.Writer, k client.KeyStatus, now time.Time)` and `renderCaps`.

- [ ] **Step 1: Write the failing tests** — `cmd/blincli/cmd_get_test.go` (reuses `fakeDaemon`; add handling for GET key/keys by extending `fakeDaemon` with `keys map[string]string` — path → JSON body — and `keyList string`; in `ServeHTTP` add before the `PUT` case: `case r.Method == "GET" && f.keys[r.URL.Path] != "": _, _ = w.Write([]byte(f.keys[r.URL.Path]))`, `case r.Method == "GET" && r.URL.Path == "/devices/d/keys": _, _ = w.Write([]byte(f.keyList))`, `case r.Method == "GET": w.WriteHeader(404); _, _ = w.Write([]byte(`{"error":"not registered"}`))`):

```go
package main

import (
	"strings"
	"testing"
)

const keyJSON = `{"device":"d","key":"idx:1","kind":"direct","led":1,"row":0,"col":1,"connected":true,
 "color":{"h":21,"s":255,"v":255,"hex":"#ff8000"},
 "source":{"type":"state","ref":"claude/working","owner":"testhost.iterm-ABCDEF01","set_at":"2026-10-05T12:00:00Z","age_ms":12400},
 "effect":{"name":"breathe_orange","running":true,"elapsed_ms":12400,"duration_ms":null}}`

func TestGetRendersKey(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, keys: map[string]string{"/devices/d/keys/idx:1": keyJSON}}
	a, _ := daemonApp(t, f, itermEnv())
	var out strings.Builder
	a.stdout = &out
	if code := a.run([]string{"get"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, want := range []string{"idx:1", "led:1", "claude/working", "breathe_orange", "#ff8000", "12.4s", "owner testhost.iterm-ABCDEF01", "loops"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestGetNotRegisteredExits66(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}}
	a, _ := daemonApp(t, f, itermEnv())
	var out strings.Builder
	a.stdout = &out
	if code := a.run([]string{"get", "-q"}); code != exitNotFound || out.Len() != 0 {
		t.Errorf("code %d, stdout %q", code, out.String())
	}
}

func TestGetNamedFallsBackToSharedSlot(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, keys: map[string]string{}}
	a, _ := daemonApp(t, f, map[string]string{"CLAUDE_CODE_SESSION_ID": "sess1"})
	var out strings.Builder
	a.stdout = &out
	// first GET (the name) 404s; second must be an idx: key
	a.run([]string{"get"})
	var gets []string
	for _, c := range f.calls {
		if c.Method == "GET" && strings.Contains(c.Path, "/keys/") {
			gets = append(gets, c.Path)
		}
	}
	if len(gets) != 2 || !strings.Contains(gets[0], "claude-sess1") || !strings.Contains(gets[1], "/keys/idx:") {
		t.Errorf("GETs = %v", gets)
	}
}

func TestGetJSONAndAll(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2, 3, 4, 5}, keys: map[string]string{"/devices/d/keys/idx:1": keyJSON}, keyList: "[" + keyJSON + "]"}
	a, _ := daemonApp(t, f, itermEnv())
	var out strings.Builder
	a.stdout = &out
	if code := a.run([]string{"get", "--json"}); code != 0 || !strings.Contains(out.String(), `"hex": "#ff8000"`) {
		t.Errorf("--json: %d %s", code, out.String())
	}
	out.Reset()
	if code := a.run([]string{"get", "-a"}); code != 0 || !strings.Contains(out.String(), "idx:1") || !strings.Contains(out.String(), "claude/working") {
		t.Errorf("-a: %d %s", code, out.String())
	}
}

func TestDevicesCommand(t *testing.T) {
	f := &fakeDaemon{tabs: []uint16{0, 1, 2}}
	a, _ := daemonApp(t, f, nil)
	var out strings.Builder
	a.stdout = &out
	if code := a.run([]string{"devices"}); code != 0 || !strings.Contains(out.String(), "d") || !strings.Contains(out.String(), "connected") {
		t.Errorf("list: %d %s", code, out.String())
	}
	out.Reset()
	if code := a.run([]string{"devices", "d"}); code != 0 || !strings.Contains(out.String(), "tabs") || !strings.Contains(out.String(), "0,1,2") {
		t.Errorf("show: %d %s", code, out.String())
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./cmd/blincli/ -run 'Get|Devices' 2>&1 | tail` → FAIL.

- [ ] **Step 3: Implement.**

`cmd/blincli/render.go`:

```go
package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/seefood/blinkenkeys/internal/client"
)

func humanDur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func renderKey(w io.Writer, k client.KeyStatus) {
	conn := "connected"
	if !k.Connected {
		conn = "untethered"
	}
	p := func(label, format string, args ...any) {
		_, _ = fmt.Fprintf(w, "%-9s %s\n", label, fmt.Sprintf(format, args...))
	}
	p("device", "%s  (%s)", k.Device, conn)
	loc := fmt.Sprintf("led:%d", k.LED)
	if k.Row != nil && k.Col != nil {
		loc += fmt.Sprintf(" (row %d, col %d)", *k.Row, *k.Col)
	}
	line := fmt.Sprintf("%s  -> %s  [%s]", k.Key, loc, k.Kind)
	if k.Source != nil && k.Source.Owner != "" {
		line += "  owner " + k.Source.Owner
	}
	p("key", "%s", line)
	if k.Claim != nil {
		p("claim", "expires %s", k.Claim.ExpiresAt.Format(time.RFC3339))
	}
	if k.Source != nil {
		p("source", "%s %s", k.Source.Type, k.Source.Ref)
	}
	if k.Effect != nil {
		state := "finished"
		if k.Effect.Running {
			state = "running"
		}
		span := "loops"
		if k.Effect.DurationMS != nil {
			span = "of " + humanDur(*k.Effect.DurationMS)
		}
		p("effect", "%s  %s, %s elapsed, %s", k.Effect.Name, state, humanDur(k.Effect.ElapsedMS), span)
	}
	if k.Color != nil {
		p("color", "%s  (hsv %d,%d,%d)  desired value; keyboard RAM is not readable back", k.Color.Hex, k.Color.H, k.Color.S, k.Color.V)
	}
	if k.Source != nil {
		p("last set", "%s ago  (%s)", humanDur(k.Source.AgeMS), k.Source.SetAt.Format(time.RFC3339))
	}
}

func renderKeyTable(w io.Writer, keys []client.KeyStatus) {
	_, _ = fmt.Fprintf(w, "%-5s %-24s %-8s %-22s %s\n", "LED", "KEY", "KIND", "SOURCE", "AGE")
	for _, k := range keys {
		src, age := "-", "-"
		if k.Source != nil {
			src, age = k.Source.Type+" "+k.Source.Ref, humanDur(k.Source.AgeMS)
		}
		_, _ = fmt.Fprintf(w, "%-5d %-24s %-8s %-22s %s\n", k.LED, k.Key, k.Kind, src, age)
	}
}

func renderCaps(w io.Writer, name string, c client.Capabilities) {
	_, _ = fmt.Fprintf(w, "device    %s\nleds      %d\n", name, c.LEDCount)
	tabs := make([]string, len(c.Layout.Tabs))
	for i, t := range c.Layout.Tabs {
		tabs[i] = fmt.Sprint(t)
	}
	if len(tabs) == 0 {
		tabs = []string{"(none configured; blincli uses idx 0-5)"}
	}
	_, _ = fmt.Fprintf(w, "tabs      %s\n\nLED  ROW  COL\n", strings.Join(tabs, ","))
	for _, p := range c.Positions {
		row, col := fmt.Sprint(p.Row), fmt.Sprint(p.Col)
		if p.Row == 0xFF && p.Col == 0xFF {
			row, col = "-", "-"
		}
		_, _ = fmt.Fprintf(w, "%-4d %-4s %s\n", p.Index, row, col)
	}
}
```

`cmd/blincli/cmd_get.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/seefood/blinkenkeys/internal/client"
)

func init() {
	commands["get"] = (*app).cmdGet
	commands["devices"] = (*app).cmdDevices
}

func (a *app) printJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.stdout, string(b))
	return err
}

func (a *app) cmdGet(args []string) int {
	fs, g := a.newFlags("get")
	var k keyFlags
	var all, asJSON bool
	both := func(p *string, long, short, usage string) { str(fs, p, long, short, usage) }
	addKeyFlags(both, &k)
	fs.IntVar(&k.slots, "slots", 0, "tab-slot count override")
	fs.IntVar(&k.slots, "m", 0, "tab-slot count override")
	boolean(fs, &all, "all", "a", "list every registered key on the device")
	fs.BoolVar(&asJSON, "json", false, "print the daemon's response as JSON")
	if code, done := a.parse(fs, args); done {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if all {
		cl, _, fc, err := a.session(g)
		if err != nil {
			return a.fail(err)
		}
		device, err := a.device(ctx, g, fc, cl)
		if err != nil {
			return a.fail(err)
		}
		keys, err := cl.ListKeys(ctx, device)
		if err != nil {
			return a.fail(err)
		}
		switch {
		case g.Quiet:
		case asJSON:
			return a.fail(a.printJSON(keys))
		default:
			renderKeyTable(a.stdout, keys)
		}
		return exitOK
	}
	p, _, err := a.prepare(ctx, g, k)
	if err != nil {
		return a.fail(err)
	}
	ks, err := a.lookup(ctx, p)
	if err != nil {
		return a.fail(err)
	}
	switch {
	case g.Quiet:
	case asJSON:
		return a.fail(a.printJSON(ks))
	default:
		renderKey(a.stdout, ks)
	}
	return exitOK
}

// lookup reads p's key; a named plan that isn't claimed also tries its shared slot.
func (a *app) lookup(ctx context.Context, p prepared) (client.KeyStatus, error) {
	switch p.plan.Mode {
	case client.ModeExplicit:
		return p.cl.GetKey(ctx, p.device, p.plan.Key)
	case client.ModeSlot:
		tabs, err := a.tabs(ctx, p.cl, p.device, p.slots)
		if err != nil {
			return client.KeyStatus{}, err
		}
		return p.cl.GetKey(ctx, p.device, client.SlotKey(p.plan.Tab, tabs))
	default:
		ks, err := p.cl.GetKey(ctx, p.device, p.plan.Name)
		if err == nil || !client.IsNotFound(err) {
			return ks, err
		}
		tabs, terr := a.tabs(ctx, p.cl, p.device, p.slots)
		if terr != nil {
			return client.KeyStatus{}, terr
		}
		return p.cl.GetKey(ctx, p.device, client.SharedKey(p.plan.Name, tabs))
	}
}

func (a *app) cmdDevices(args []string) int {
	fs, g := a.newFlags("devices")
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print the daemon's response as JSON")
	if code, done := a.parse(fs, args); done {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	cl, _, _, err := a.session(g)
	if err != nil {
		return a.fail(err)
	}
	if fs.NArg() == 0 {
		devs, err := cl.Devices(ctx)
		if err != nil {
			return a.fail(err)
		}
		if asJSON {
			return a.fail(a.printJSON(devs))
		}
		for _, d := range devs {
			state := "connected"
			if !d.Connected {
				state = "untethered"
			}
			_, _ = fmt.Fprintf(a.stdout, "%s  %s\n", d.Name, state)
		}
		return exitOK
	}
	caps, err := cl.Capabilities(ctx, fs.Arg(0))
	if err != nil {
		return a.fail(err)
	}
	if asJSON {
		return a.fail(a.printJSON(caps))
	}
	renderCaps(a.stdout, fs.Arg(0), caps)
	return exitOK
}
```
(`prepare`'s `skip` result is unused for `get`: a missing key for `get` is a usage error, not a skip — but `keyFlags.ifDetected` is false there, so `skip` is always false. Update `addKeyFlags` to drop its unused `a` parameter in both commands. `renderKey` here takes only `(w, k)`; update the test expectation `"12.4s"` — `humanDur(12400)` renders `12.4s`.)

- [ ] **Step 4: Run** — `nice go test ./cmd/blincli/ -v 2>&1 | tail -30` → PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/blincli
git commit -m "blincli: get and devices commands"
```

---

### Task 14: `blincli config init | show | path`

**Files:**
- Create: `cmd/blincli/cmd_config.go`, `cmd/blincli/cmd_config_test.go`

**Interfaces:**
- Consumes: Tasks 9–11.
- Produces: `config` subcommands.
  - `config path` prints the client config path (honors `-C`).
  - `config show` prints the file's effective fields with `token` masked (`token: ****`), or `no config at PATH` (exit 0).
  - `config init [--url URL] [--socket PATH] [--token T] [--token-file F] [-d NAME] [-m N] [-i|--interactive] [-f|--force]`:
    - refuses to overwrite an existing file without `--force` (exit 64);
    - writes mode 0600, creating the directory 0700;
    - no value flags and no `-i` → annotated template, every key commented out, exit 0 with a "now edit it" hint on stderr;
    - value flags → those keys set, the rest commented; if `device` is unset and the endpoint resolves, probe `GET /devices` once and write the device when exactly one exists (a probe failure only prints a note to stderr);
    - `-i`: exit 64 if `isTTY()` is false; otherwise prompt (stdin lines) for endpoint (`url:`/socket path; blank = local socket), token file (only when a URL), device;
    - `--token` writes the token into the file (plaintext, file is 0600); the global `-t` flag is the same option, shared.

- [ ] **Step 1: Write the failing tests** — `cmd/blincli/cmd_config_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seefood/blinkenkeys/internal/client"
)

func cfgApp(t *testing.T, env map[string]string) (*app, string, *strings.Builder, *strings.Builder) {
	t.Helper()
	a, _, _ := testApp(env)
	path := filepath.Join(t.TempDir(), "blinkenkeys", "blincli.yaml")
	var out, errs strings.Builder
	a.stdout, a.stderr = &out, &errs
	return a, path, &out, &errs
}

func TestConfigInitTemplateIsUnconfigured(t *testing.T) {
	a, path, _, errs := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", fi, err)
	}
	fc, exists, err := client.LoadFile(path)
	if err != nil || !exists || fc != (client.FileConfig{}) {
		t.Errorf("template must parse as an empty (unconfigured) config: %+v %v %v", fc, exists, err)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"# url:", "# token_file:", "# device:"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("template lacks %q:\n%s", want, b)
		}
	}
	if !strings.Contains(errs.String(), "edit") {
		t.Errorf("no edit hint: %q", errs)
	}
}

func TestConfigInitFromFlagsAndRefusesOverwrite(t *testing.T) {
	a, path, _, errs := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path, "--url", "http://nas:49994", "--token-file", "~/tok", "-d", "uid-1"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	fc, _, err := client.LoadFile(path)
	if err != nil || fc.URL != "http://nas:49994" || fc.TokenFile != "~/tok" || fc.Device != "uid-1" {
		t.Errorf("%+v %v", fc, err)
	}
	if code := a.run([]string{"config", "init", "-C", path}); code != exitUsage {
		t.Errorf("overwrite without --force: code %d, want 64", code)
	}
	if code := a.run([]string{"config", "init", "-C", path, "--force", "--socket", "/s"}); code != 0 {
		t.Errorf("--force: code %d", code)
	}
	if fc, _, _ := client.LoadFile(path); fc.Socket != "/s" || fc.URL != "" {
		t.Errorf("after --force: %+v", fc)
	}
}

func TestConfigInitProbesForSingleDevice(t *testing.T) {
	f := &fakeDaemon{}
	a, path, _, _ := cfgApp(t, map[string]string{})
	srvApp, _ := daemonApp(t, f, nil)
	url := srvApp.getenv("BLINKENKEYS_URL")
	if code := a.run([]string{"config", "init", "-C", path, "--url", url, "--token", "tok"}); code != 0 {
		t.Fatalf("code %d", code)
	}
	if fc, _, _ := client.LoadFile(path); fc.Device != "d" {
		t.Errorf("single device not written: %+v", fc)
	}
}

func TestConfigInitInteractive(t *testing.T) {
	a, path, _, errs := cfgApp(t, nil)
	if code := a.run([]string{"config", "init", "-C", path, "-i"}); code != exitUsage {
		t.Errorf("non-tty -i: code %d, want 64; %s", code, errs)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("nothing may be written when -i is refused")
	}
	a.isTTY = func() bool { return true }
	a.stdin = strings.NewReader("http://nas:49994\n~/tok\nuid-9\n")
	if code := a.run([]string{"config", "init", "-C", path, "-i"}); code != 0 {
		t.Fatalf("code %d: %s", code, errs)
	}
	if fc, _, _ := client.LoadFile(path); fc.URL != "http://nas:49994" || fc.TokenFile != "~/tok" || fc.Device != "uid-9" {
		t.Errorf("%+v", fc)
	}
}

func TestConfigShowMasksToken(t *testing.T) {
	a, path, out, _ := cfgApp(t, nil)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, []byte("url: http://h:1\ntoken: supersecret\n"), 0o600)
	if code := a.run([]string{"config", "show", "-C", path}); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(out.String(), "supersecret") || !strings.Contains(out.String(), "****") || !strings.Contains(out.String(), "http://h:1") {
		t.Errorf("show output:\n%s", out)
	}
	a2, missing, out2, _ := cfgApp(t, nil)
	if code := a2.run([]string{"config", "show", "-C", missing}); code != 0 || !strings.Contains(out2.String(), "no config") {
		t.Errorf("missing: %d %s", code, out2)
	}
}

func TestVerboseNeverPrintsToken(t *testing.T) {
	f := &fakeDaemon{}
	a, errs := daemonApp(t, f, itermEnv())
	a.run([]string{"set", "-c", "red", "-v"})
	if strings.Contains(errs.String(), "tok") && strings.Contains(errs.String(), "token tok") || strings.Contains(errs.String(), "Bearer") {
		t.Errorf("verbose leaked the token:\n%s", errs)
	}
}

func TestConfigPath(t *testing.T) {
	a, path, out, _ := cfgApp(t, nil)
	a.run([]string{"config", "path", "-C", path})
	if strings.TrimSpace(out.String()) != path {
		t.Errorf("path = %q", out)
	}
}
```

- [ ] **Step 2: Run to verify failure** — `nice go test ./cmd/blincli/ -run Config 2>&1 | tail` → FAIL.

- [ ] **Step 3: Implement** — `cmd/blincli/cmd_config.go`:

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/seefood/blinkenkeys/internal/client"
)

func init() { commands["config"] = (*app).cmdConfig }

func (a *app) cmdConfig(args []string) int {
	if len(args) == 0 {
		return a.fail(fmt.Errorf("%w: config needs a subcommand: init, show or path", client.ErrUsage))
	}
	switch args[0] {
	case "init":
		return a.configInit(args[1:])
	case "show":
		return a.configShow(args[1:])
	case "path":
		return a.configPath(args[1:])
	default:
		return a.fail(fmt.Errorf("%w: unknown config subcommand %q", client.ErrUsage, args[0]))
	}
}

func (a *app) configPathFor(g *globals) string {
	return client.Resolver{Getenv: a.getenv, Home: a.home}.ConfigPath(client.Options{ConfigPath: g.Config})
}

func (a *app) configPath(args []string) int {
	fs, g := a.newFlags("config path")
	if code, done := a.parse(fs, args); done {
		return code
	}
	_, _ = fmt.Fprintln(a.stdout, a.configPathFor(g))
	return exitOK
}

func (a *app) configShow(args []string) int {
	fs, g := a.newFlags("config show")
	if code, done := a.parse(fs, args); done {
		return code
	}
	path := a.configPathFor(g)
	fc, exists, err := client.LoadFile(path)
	if err != nil {
		return a.fail(err)
	}
	if !exists {
		_, _ = fmt.Fprintf(a.stdout, "no config at %s\n", path)
		return exitOK
	}
	_, _ = fmt.Fprintf(a.stdout, "# %s\n%s", path, renderConfig(client.FileConfig{
		URL: fc.URL, Socket: fc.Socket, Token: maskIf(fc.Token), TokenFile: fc.TokenFile, Device: fc.Device, Slots: fc.Slots,
	}))
	return exitOK
}

func maskIf(s string) string {
	if s == "" {
		return ""
	}
	return "****"
}

// renderConfig writes every key; unset ones are commented-out examples, so
// an all-unset config is the template and still parses as "unconfigured".
func renderConfig(fc client.FileConfig) string {
	var b strings.Builder
	line := func(key, val, example string) {
		if val != "" {
			fmt.Fprintf(&b, "%s: %s\n", key, strconv.Quote(val))
		} else {
			fmt.Fprintf(&b, "# %s: %s\n", key, example)
		}
	}
	b.WriteString("# blincli client config. Set url (remote daemon) OR socket (local daemon);\n")
	b.WriteString("# with neither, blincli probes the local daemon socket.\n")
	line("url", fc.URL, `"http://HOST:49994"`)
	line("socket", fc.Socket, `"~/.local/state/blinkenkeys/api.sock"`)
	line("token_file", fc.TokenFile, `"~/.config/blinkenkeys/token"   # required for url; or token: "..."`)
	line("token", fc.Token, `"..."`)
	line("device", fc.Device, `"uid-xxxxxxxxxxxxxxxx"   # default: the only device`)
	if fc.Slots > 0 {
		fmt.Fprintf(&b, "slots: %d\n", fc.Slots)
	} else {
		b.WriteString("# slots: 6   # tab-slot count override; default comes from the daemon's keys.tabs, else 6\n")
	}
	return b.String()
}

func (a *app) configInit(args []string) int {
	fs, g := a.newFlags("config init")
	var interactive, force bool
	var slots int
	boolean(fs, &interactive, "interactive", "i", "prompt for the values (requires a terminal)")
	boolean(fs, &force, "force", "f", "overwrite an existing config")
	fs.IntVar(&slots, "slots", 0, "tab-slot count override")
	fs.IntVar(&slots, "m", 0, "tab-slot count override")
	if code, done := a.parse(fs, args); done {
		return code
	}
	path := a.configPathFor(g)
	if _, err := os.Stat(path); err == nil && !force {
		return a.fail(fmt.Errorf("%w: %s already exists (use --force to overwrite)", client.ErrUsage, path))
	}
	fc := client.FileConfig{URL: g.URL, Socket: g.Socket, Token: g.Token, TokenFile: g.TokenFile, Device: g.Device, Slots: slots}
	if interactive {
		if !a.isTTY() {
			return a.fail(fmt.Errorf("%w: --interactive needs a terminal on stdin", client.ErrUsage))
		}
		a.prompt(&fc)
	}
	if fc.Device == "" && (fc.URL != "" || fc.Socket != "") {
		fc.Device = a.probeDevice(g, fc)
	}
	if err := writeConfig(path, renderConfig(fc)); err != nil {
		return a.fail(err)
	}
	if !g.Quiet {
		_, _ = fmt.Fprintf(a.stderr, "wrote %s\n", path)
		if fc == (client.FileConfig{}) {
			_, _ = fmt.Fprintln(a.stderr, "it is a template: edit it to set url (or socket) and a token, then run 'blincli devices'")
		}
	}
	return exitOK
}

// prompt fills fc from stdin lines: endpoint, token file (remote only), device.
func (a *app) prompt(fc *client.FileConfig) {
	sc := bufio.NewScanner(a.stdin)
	ask := func(q string) string {
		_, _ = fmt.Fprint(a.stderr, q)
		if sc.Scan() {
			return strings.TrimSpace(sc.Text())
		}
		return ""
	}
	if fc.URL == "" && fc.Socket == "" {
		switch ans := ask("daemon URL (http://host:49994) or socket path, blank for the local socket: "); {
		case strings.HasPrefix(ans, "http://") || strings.HasPrefix(ans, "https://"):
			fc.URL = ans
		case ans != "":
			fc.Socket = ans
		}
	}
	if fc.URL != "" && fc.Token == "" && fc.TokenFile == "" {
		fc.TokenFile = ask("file containing the bearer token: ")
	}
	if fc.Device == "" {
		fc.Device = ask("device name, blank to auto-detect the only device: ")
	}
}

// probeDevice asks the daemon for its devices; exactly one is returned, else "".
// Failures only produce a note: init must still write the file.
func (a *app) probeDevice(g *globals, fc client.FileConfig) string {
	r := client.Resolver{Getenv: a.getenv, Home: a.home}
	ep, _, err := r.Resolve(client.Options{Socket: fc.Socket, URL: fc.URL, Token: fc.Token, TokenFile: fc.TokenFile, ConfigPath: os.DevNull})
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "note: could not probe the daemon for devices: %s\n", firstLine(err.Error()))
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	devs, err := client.New(ep).Devices(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "note: could not list devices: %s\n", firstLine(err.Error()))
		return ""
	}
	if len(devs) == 1 {
		return devs[0].Name
	}
	return ""
}

func writeConfig(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- operator-chosen config path
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

var _ = errors.Is
var _ fs.FileMode
```
(Remove the two trailing blank-identifier lines and the unused `errors`/`io/fs` imports. In `probeDevice`, `ConfigPath: os.DevNull` keeps the resolver from reading a pre-existing config; `LoadFile` on `/dev/null` reads an empty file → handle as an empty config (an empty file must not be an error — see the comment-only note in Task 9 Step 3). `g` is unused in `probeDevice`; drop the parameter. `fc == (client.FileConfig{})` compiles because all fields are comparable.)

- [ ] **Step 4: Run** — `nice go test ./cmd/blincli/ -v 2>&1 | tail -30 && CGO_ENABLED=0 nice go build -o /tmp/blincli-check ./cmd/blincli` → PASS; build succeeds (pure Go).

- [ ] **Step 5: Commit**

```bash
git add cmd/blincli
git commit -m "blincli: config init/show/path"
```

---

## Phase C — packaging and docs

### Task 15: Installers, hooks example, docs, manual check, final verification

**Files:**
- Create: `integrations/claude/hooks-blincli.json`
- Modify: `packaging/linux/install.sh`, `packaging/linux/uninstall.sh`, `packaging/macos/install.sh`, `packaging/macos/uninstall.sh`, `integrations/claude/README.md`, `README.md`, `CHANGELOG.md`, `CLAUDE.md`, `docs/superpowers/specs/2026-10-05-blincli-design.md`, `docs/superpowers/manual-checks/blincli.md`

**Interfaces:** none (leaf task).

- [ ] **Step 1: Installers.** In both `install.sh` files, directly **after** the daemon-binary install block (the `if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$SRC_BIN" "$DEST_BIN" ...; fi` block), add:

```bash
SRC_CLI="$SCRIPT_DIR/../../bin/blincli"
if [[ ! -f "$SRC_CLI" ]]; then
	echo "error: $SRC_CLI not found — run 'make build' first" >&2
	exit 1
fi

DEST_CLI="$BIN_DIR/blincli"
cli_changed=0
if [[ "$FORCE" -eq 1 ]] || ! cmp -s "$SRC_CLI" "$DEST_CLI" 2>/dev/null; then
	install -m 0755 "$SRC_CLI" "$DEST_CLI"
	cli_changed=1
fi
```
Add `cli_changed=0` is set inline above (no separate init needed). In the Linux summary add `echo "cli:       $([[ $cli_changed -eq 1 ]] && echo "installed to $DEST_CLI" || echo "already up to date")"` after the `binary:` line; in macOS add `echo "cli:    $([[ $cli_changed -eq 1 ]] && echo "installed to $DEST_CLI" || echo "already up to date")"` after its `binary:` line. (`blincli` changes never restart the service.)

In both `uninstall.sh` files, next to the daemon binary removal add:

```bash
DEST_CLI="$BIN_DIR/blincli"
cli_removed=0
if [[ -f "$DEST_CLI" ]]; then
	rm -f "$DEST_CLI"
	cli_removed=1
fi
```
and a summary line `echo "cli:       $([[ $cli_removed -eq 1 ]] && echo "removed ($DEST_CLI)" || echo "already absent")"` (Linux; use the macOS column alignment `cli:    ` there). Run: `shellcheck packaging/*/*.sh && shfmt -d packaging/` (or `prek run --all-files`) — both clean.

- [ ] **Step 2: `integrations/claude/hooks-blincli.json`:**

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "blincli set -s claude/idle" } ] }
    ],
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "blincli set -s claude/working" } ] }
    ],
    "Stop": [
      { "hooks": [ { "type": "command", "command": "blincli set -s claude/idle" } ] }
    ],
    "PermissionRequest": [
      { "hooks": [ { "type": "command", "command": "blincli set -s claude/waiting" } ] }
    ],
    "SessionEnd": [
      { "hooks": [ { "type": "command", "command": "blincli clear" } ] }
    ]
  }
}
```
Validate: `python3 -c 'import json,sys; json.load(open("integrations/claude/hooks-blincli.json"))'`. (No `--if-detected`: inside Claude Code `$CLAUDE_CODE_SESSION_ID` is always set, so a key is always derivable and a real misconfiguration should surface as a non-blocking hook error rather than be silenced.)

- [ ] **Step 3: Docs.**
  - `integrations/claude/README.md`: change the intro "these are the first two" to "three"; in Prerequisites add step "Install `blincli` (`make build`, then `packaging/<os>/install.sh`) and run `blincli config init` if the daemon is remote; for a local daemon nothing is needed." and add a section **`hooks-blincli.json`** describing: no device name or socket path in the commands (found by `blincli`), key = terminal tab slot (tabs 1–N of iTerm/tmux/WezTerm map onto the device's `keys.tabs`, configure it per `examples/config/README.md`), fallback to a named claim from the terminal pane id / `$CLAUDE_CODE_SESSION_ID`, shared slot when the pool is empty, `blincli clear` only blanks a key you still own, the iTerm env var staleness limitation (tab reorder/close), and `blincli detect` for debugging. Relabel the `hooks-wezterm-pane.json` section's first sentence to: "A raw-`curl` example, kept for reference; `hooks-blincli.json` supersedes it."
  - `README.md`: add a short "## blincli" section after Installation: what it is, `blincli set -s claude/idle`, `blincli get`, link to `integrations/claude/README.md` and the spec.
  - `CHANGELOG.md`: append `## blincli design (docs/superpowers/specs/2026-10-05-blincli-design.md)` with a dated entry listing: `GET` key/keys endpoints, `owner` tag + conditional `DELETE` (and `DELETE` now accepts direct addresses — previously 400), per-device `keys:` layout in `config.yaml`, engine status records (in-memory).
  - `CLAUDE.md`: in **Commands** add `bin/blincli` to the `make build` line comment (`# builds bin/blinkenkeysd and bin/blincli`); add one bullet to **Architecture essentials**: "**`blincli` is a pure-Go client** (`cmd/blincli`, `internal/client`, `internal/termid`): it must never import `internal/hid`/`dispatcher`/`effects`/`api` (cgo). Exit codes are sysexits and never 2 (Claude Code hooks treat 2 as blocking)."
  - Spec `docs/superpowers/specs/2026-10-05-blincli-design.md`: in **Files** change the `hooks-blincli.json` line to say it uses plain `blincli set -s claude/…` and `blincli clear` (no `--if-detected`, for the reason in Step 2); in the terminal table replace the unverified markers with the results in this plan's "Verified ground truth" (iTerm zero-based/stale; WezTerm JSON shape verified, tab ordering unverified; tmux verified; hook exit 2 verified).

- [ ] **Step 4: Manual check (client half)** — append to `docs/superpowers/manual-checks/blincli.md`:

````markdown
## Client half

Build and install: `make build && packaging/linux/install.sh` (or macOS).

1. `blincli detect` in each terminal you use → correct terminal, tab, key; run it in a plain `ssh` shell → "none recognized" and key `none (…)` unless `$CLAUDE_CODE_SESSION_ID`/`$BLINKENKEYS_NAME` is set.
2. **iTerm2**: open tabs 1–3; in each run `blincli set -c red`; keys `idx:0/1/2` light red, matching Option-1/2/3. Reorder tabs, run again: the key follows the *original* tab number (the known staleness limitation) — record the observed behaviour here.
3. **WezTerm** (verifies the unverified list-order assumption): open 3 tabs, drag tab 3 to position 1, run `blincli detect` in it → expect tab 1. If it reports 3, `wezterm cli list` is not in tab order; fix `weztermTab` (`internal/termid/termid.go`) to order by `window_id`'s tab list another way, update the spec table, and note it here.
4. **tmux**: with `base-index 1`, window 2 → `blincli detect` reports tab 2.
5. `blincli set -s claude/working` then `blincli get` → source, effect running, color, age. `blincli get -a` lists it. `blincli get -k idx:5` on a never-written key → exit 66.
6. Collision: tab 7 (or `-m 4` in tab 5) maps onto an occupied slot: the key is taken over; `blincli clear` from the *first* tab afterwards leaves it lit (owner mismatch), `clear --force` blanks it.
7. Named fallback: `env -u ITERM_SESSION_ID -u WEZTERM_PANE -u TMUX_PANE CLAUDE_CODE_SESSION_ID=abc blincli set -c blue` → lands on idx 6+ (pool). With `pool: []` in the daemon config → lands on a tab slot via hash (`-v` shows "sharing slot").
8. Exit codes: stop the daemon → `blincli set …` exits 69; wrong `--token` against a TCP listener → 77; remove the config and socket → 78 with the setup help; `blincli set` with no action → 64.
9. Hooks: merge `integrations/claude/hooks-blincli.json` into `.claude/settings.local.json`, run a Claude Code session: LED goes idle → working → waiting → dark on exit.
````

- [ ] **Step 5: Final verification.**

Run:
```
nice make test
nice make lint
CGO_ENABLED=0 go build ./cmd/blincli ./internal/client ./internal/termid
go list -deps ./cmd/blincli | grep -E 'internal/(hid|dispatcher|effects|api)|go-hid' && echo "FAIL: cgo dependency leaked" || echo "ok: blincli deps are pure"
```
Expected: tests PASS, lint clean, build succeeds, `ok: blincli deps are pure`. Then run `mcp__ide__getDiagnostics` and fix anything on touched files. Execute the manual checks above that your hardware allows and record the iTerm/WezTerm outcomes in `docs/superpowers/manual-checks/blincli.md`; any check you could not run must be listed there as *not run*.

- [ ] **Step 6: Commit** (separate small commits are fine: installers; hooks example; docs)

```bash
git add packaging integrations README.md CHANGELOG.md CLAUDE.md docs
git commit -m "blincli: installers, hooks example, docs and manual checks"
```

---

## Self-Review

**1. Spec coverage**
- Commands/flags (`set`/`clear`/`get`/`devices`/`detect`/`config`/`version`, GNU options, `-m`, `--if-detected`, `--force`) → Tasks 11–14.
- Transport resolution order, config file, error text, exit 78/69/77, token rules → Task 9 (+ Task 11 exit mapping).
- `config init` template / flags / interactive / probing / 0600 / `--force` / `show` mask / `path` → Task 14.
- Key resolution (explicit → name → tab slot → instance id → env fallback → error) → Tasks 8, 10, 12; terminal resolvers table → Task 8; host prefix → Task 10 `Qualify`/Task 12 `prepare`; shared placement on 409 → Task 12.
- Collision policy (owner tag, conditional `clear`, `--force`) → Tasks 5, 12.
- `get`/status API (engine records, `Timeline.Name`, `GET` key/keys, no-claim lookup, `PUT owner`, `DELETE` direct + `?owner=`) → Tasks 3–6.
- Per-device layout (`tabs`/`pool`, list/range syntax, default-pool rules, `pool: []`, validation, capabilities `layout`) → Tasks 1, 2, 6, 7. The spec's "idx beyond the device's key count rejected once capabilities are known" is **not** implemented as a hard validation: out-of-range pool entries are skipped at claim time (Task 2 test) and an out-of-range tab slot write returns 404 from `Canonical`; Task 15's manual check step 1 covers eyeballing it. This is the one deliberate deviation — rejecting at config-load time is impossible (capabilities unknown then), and adding a later check would be a new startup phase; flagged here rather than silently dropped. Update the spec's validation bullet accordingly in Task 15 Step 3.
- Exit codes → Tasks 11, 12 tests; installers + `make build` + hooks example + docs → Task 15.

**2. Placeholder scan:** no TBD/TODO. Several steps contain parenthetical *cleanup notes* about lines to delete (trailing `var _ = …` guards, unused imports); these are instructions to the implementer about code shown, not missing content.

**3. Type consistency:** `effects.Origin`/`Status`/`EffectStatus`, `Writer` methods (`SetColor`, `SetColorFrom(t,c,o,now)`, `StartFrom(t,tl,now,o)`, `Status`, `Statuses`) consistent across Tasks 4–6; `dispatcher.KeyInfo`/`Layout`/`Lookup` consistent across 2, 3, 5, 6, 7; client `PutBody`, `KeyStatus`, `Capabilities.Layout.Tabs`, `Plan`/`PlanKey`/`Qualify`/`SlotKey`/`SharedKey`/`DefaultTabs` consistent across 10, 12, 13; `app.prepare`/`prepared`/`tabs`/`keyFlags` consistent across 12–13. Known fix-ups called out inline: `addKeyFlags` loses its unused `a` parameter, `a.fail(nil)` returns 0, `--if-detected` registered with `fs.BoolVar` (no empty short name), `probeDevice` drops its `g` parameter.

**4. Review Focus coverage:** (1) Task 1 `TestLoadKeyLayout`; (2) Tasks 3 `TestLookupNameDoesNotClaim` + 6 `TestGetKeyRegistration`; (3) Tasks 5 `TestDeleteOwnerMismatchIsNoOp` + 12 `TestClearConditionalOnOwner`; (4) Task 8 `TestDetectHangingHelperIsBounded`, `TestDetectWezTermBadOutputDegrades`, `TestDetectTmuxHelperFailureKeepsInstance`; (5) Tasks 9 `TestResolveTokenFileAndMissingToken`, 12 `TestSetRemoteWithoutTokenFailsBeforeSending`, 14 `TestConfigShowMasksToken` + `TestVerboseNeverPrintsToken`; (6) Task 11 `TestNeverExitsTwo`/`TestUsageErrorsExit64`.
