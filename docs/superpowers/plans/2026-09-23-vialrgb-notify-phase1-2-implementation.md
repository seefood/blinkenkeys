# vialrgb-notify Phase 1+2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Go `vialrgbd` daemon described in
`docs/superpowers/specs/2026-09-21-vialrgb-notify-phase1-2-design.md`: set a
single key's color on one HID device (Phase 1), and enumerate/report
capabilities of all connected Vial-capable devices with stable names (Phase 2).

**Architecture:** One long-lived process, `vialrgbd`. It enumerates
Vial-capable devices directly via `github.com/sstallion/go-hid` (no
privilege separation — the design spec's revision history explains why the
original `connectord`/`restd` split was dropped: macOS's Input Monitoring
TCC grant, the reason for the split, turns out not to gate raw HID access to
a vendor-defined usage page at all), runs an HTTP server, and owns a single
dispatcher goroutine that serializes and batches all device operations
against the open HID handles. The dispatcher also maintains an in-memory
last-known-good color cache per device and periodically (plus on
reconnect) redraws it, since VialRGB Direct-mode colors don't survive a
device reset and can't be read back. See the design spec for full
rationale — this plan implements it as specified without reopening any of
its decisions.

**Tech Stack:** Go 1.27, `github.com/sstallion/go-hid` (raw HID via bundled
hidapi C sources, cgo), `golang.org/x/image/colornames` (CSS/X11 color
names), `github.com/goccy/go-yaml` (config), Go's stdlib `net/http` with the
Go 1.22+ method+wildcard `ServeMux` (no external router dependency needed).

---

## Verified ground truth this plan relies on

Everything below was checked against real sources before writing any code
(GitHub source at the pinned tag, or this repo's own `qmk_vial` checkout) —
none of it is guessed:

- **`github.com/sstallion/go-hid` v0.15.0** (latest tag as of writing) bundles
  its own hidapi C sources per platform (`hid_darwin.c`, `hid_linux.c`) — no
  system `hidapi` library needed. Requires cgo (GCC on `PATH`) everywhere,
  and on Linux, the `libudev-dev` headers (hidraw backend, default) — per
  upstream hidapi's `BUILD.md`. Exact verified API surface used below:
  `Init() error`, `Exit() error`, `Enumerate(vid, pid uint16, enumFn EnumFunc) error`,
  `VendorIDAny = 0`, `ProductIDAny = 0`, `DeviceInfo{Path, VendorID, ProductID, UsagePage, Usage, ...}`,
  `OpenPath(path string) (*Device, error)`,
  `(*Device) Write(p []byte) (int, error)`,
  `(*Device) ReadWithTimeout(p []byte, timeout time.Duration) (int, error)`,
  `(*Device) Close() error`.
- **`golang.org/x/image/colornames` v0.46.0**: `Map map[string]color.RGBA`
  keyed by lowercase SVG 1.1 color keyword, confirmed `Map["green"] ==
  color.RGBA{0x00, 0x80, 0x00, 0xff}` — matching the design spec's explicit
  callout of CSS `"green"` being unusually dark.
- **Vial UID protocol**, read directly from this repo's `../qmk_vial` checkout:
  `quantum/via.c` dispatches `data[0] == id_vial_prefix (0xFE)` to
  `vial_handle_cmd(data, length)`; `quantum/vial.h` defines
  `vial_get_keyboard_id = 0x00`; `quantum/vial.c`'s handler for it replies
  with `msg[0..3]` = protocol version (unused here) and `msg[4..11]` = the
  8-byte `VIAL_KEYBOARD_UID`. Combined with `set_key_color.py`'s
  already-hardware-validated framing (write buffer = `0x00` report-ID byte +
  32-byte report; read = 32 bytes, no report-ID prefix), the full request for
  the UID is: write `[0x00, 0xFE, 0x00, 0,0,...]` (33 bytes), read 32 bytes,
  UID is `resp[4:12]`.
- **VialRGB `VIALRGB_DIRECT_FASTSET` batch wire format**, reverse-derived
  from `set_key_color.py`'s working single-LED call
  (`[CMD_VIA_LIGHTING_SET_VALUE, VIALRGB_DIRECT_FASTSET, led&0xFF, led>>8, 1, 0, 255, 255]`,
  which sets one LED to HSV `(0,255,255)` = red) as: after the two command
  bytes, `[start_led_lo, start_led_hi, count, H0,S0,V0, H1,S1,V1, ...]`, up to
  9 LEDs (27 usable bytes ÷ 3 bytes/LED) per the design spec's own citation of
  `vialrgb.c`'s `fast_set_leds` comment.
- **Current stable Go is 1.27.1** (confirmed via both `brew info go` and
  `https://go.dev/VERSION?m=text`).
- Module path `github.com/seefood/vialrgb-notify` — confirmed with the user
  (matches their `gh` CLI identity; this repo isn't pushed to GitHub yet).
- **`github.com/goccy/go-yaml` v1.19.2**: actively maintained (unlike
  `gopkg.in/yaml.v3`, which is archived/unmaintained upstream), zero
  non-stdlib dependencies, requires Go 1.21+. Its top-level `Marshal`/
  `Unmarshal` API and `yaml:"..."` struct-tag conventions are drop-in
  compatible with `yaml.v3` for the plain-struct usage in Task 13's
  `config.Load` — no call-site changes needed beyond the import path.
- **No privilege drop is needed in the single-binary `vialrgbd` design**: there
  is no spawned child to drop, and the design spec's explicit ban on calling
  `syscall.Setuid`/`Setgid` on an already-running process (broken across Go's
  OS threads, `golang/go#1435`) means `vialrgbd` itself can never safely drop
  out of a root fallback either — so Task 14's `warnIfRootFallback` only logs
  a warning, it does not attempt to de-escalate.

---

## Task 1: Prerequisites and module init

**Files:**
- Create: `go.mod`, `go.sum` (via `go` tooling, not hand-authored)
- Create: `.gitignore`

- [ ] **Step 1: Install Go 1.27 and verify**

```bash
brew install go
go version
```

Expected: `go version go1.27.1 darwin/arm64` (or newer patch — any 1.27.x is
fine; this is the toolchain, not a pinned go.mod requirement).

- [ ] **Step 2: Initialize the module**

Run from the repo root (`/Users/ira/src/CXT-studio/vialrgb-notify`):

```bash
go mod init github.com/seefood/vialrgb-notify
```

Expected: creates `go.mod` with `module github.com/seefood/vialrgb-notify`
and a `go` directive matching the installed toolchain.

- [ ] **Step 3: Add the three verified dependencies, pinned**

```bash
go get github.com/sstallion/go-hid@v0.15.0
go get golang.org/x/image@v0.46.0
go get github.com/goccy/go-yaml@v1.19.2
go mod tidy
```

Expected: `go.mod` now lists all three as direct requires; `go.sum` is
populated. `go mod tidy` should exit 0 with no changes beyond formatting
(nothing to prune yet, since no source files import them).

- [ ] **Step 4: Add `.gitignore` for Go build output**

```gitignore
/bin/
```

- [ ] **Step 5: Verify the module builds (trivially, with no source yet)**

```bash
go build ./...
```

Expected: no output, exit 0 (an empty module with no packages is a valid,
trivially-successful build).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum .gitignore
git commit -m "Initialize Go module, pin go-hid/colornames/yaml dependencies"
```

---

## Task 2: `internal/color` — color string parsing

**Files:**
- Create: `internal/color/color.go`
- Test: `internal/color/color_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/color/color_test.go`:

```go
package color

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		h, s, v uint8
		wantErr bool
	}{
		{"hex red", "#ff0000", 0, 255, 255, false},
		{"hex green (css dark green)", "#008000", 85, 255, 128, false},
		{"hex white", "#ffffff", 0, 0, 255, false},
		{"hex black", "#000000", 0, 0, 0, false},
		{"hsv triple", "0,128,255", 0, 128, 255, false},
		{"hsv triple spaced", "10, 20, 30", 10, 20, 30, false},
		{"named green", "green", 85, 255, 128, false},
		{"named case-insensitive", "GREEN", 85, 255, 128, false},
		{"unknown name", "not-a-color", 0, 0, 0, true},
		{"malformed hex", "#zzzzzz", 0, 0, 0, true},
		{"short hex", "#fff", 0, 0, 0, true},
		{"triple value out of range", "256,0,0", 0, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, s, v, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = nil error, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if h != tt.h || s != tt.s || v != tt.v {
				t.Errorf("Parse(%q) = %d,%d,%d; want %d,%d,%d", tt.in, h, s, v, tt.h, tt.s, tt.v)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests, verify they fail (package doesn't exist yet)**

```bash
go test ./internal/color/...
```

Expected: `FAIL` — `Parse` undefined (or "no Go files" if the directory is
empty at this point — either is the expected pre-implementation failure).

- [ ] **Step 3: Implement**

`internal/color/color.go`:

```go
// Package color parses the REST API's color field into QMK-native HSV
// bytes, per the design spec's three accepted forms.
package color

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/image/colornames"
)

// Parse converts a color string into QMK-native HSV: each of H, S, V is
// 0-255, not the more common 0-360°/0-100%/0-100% convention. Accepted
// forms, in this detection order: "#rrggbb" hex; an "H,S,V" triple (each
// 0-255, passed through unchanged — it's already QMK-native); a CSS/X11
// color name (e.g. "green"). Anything else is an error.
func Parse(s string) (h, sat, v uint8, err error) {
	if strings.HasPrefix(s, "#") {
		return parseHex(s)
	}
	if h, sat, v, ok := tryParseTriple(s); ok {
		return h, sat, v, nil
	}
	if h, sat, v, ok := tryParseName(s); ok {
		return h, sat, v, nil
	}
	return 0, 0, 0, fmt.Errorf("color: cannot parse %q", s)
}

func parseHex(s string) (h, sat, v uint8, err error) {
	if len(s) != 7 {
		return 0, 0, 0, fmt.Errorf("color: invalid hex %q: want #rrggbb", s)
	}
	r, err1 := strconv.ParseUint(s[1:3], 16, 8)
	g, err2 := strconv.ParseUint(s[3:5], 16, 8)
	b, err3 := strconv.ParseUint(s[5:7], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, fmt.Errorf("color: invalid hex %q", s)
	}
	h, sat, v = rgbToHSV(uint8(r), uint8(g), uint8(b))
	return h, sat, v, nil
}

func tryParseTriple(s string) (h, sat, v uint8, ok bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	vals := make([]uint8, 3)
	for i, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 8)
		if err != nil {
			return 0, 0, 0, false
		}
		vals[i] = uint8(n)
	}
	return vals[0], vals[1], vals[2], true
}

func tryParseName(s string) (h, sat, v uint8, ok bool) {
	c, found := colornames.Map[strings.ToLower(s)]
	if !found {
		return 0, 0, 0, false
	}
	h, sat, v = rgbToHSV(c.R, c.G, c.B)
	return h, sat, v, true
}

// rgbToHSV converts 8-bit RGB to QMK-native HSV: H, S, and V all scaled to
// 0-255 (not 360°/100%/100%), matching quantum/color.h's HSV type.
func rgbToHSV(r, g, b uint8) (h, s, v uint8) {
	max := maxu8(r, g, b)
	min := minu8(r, g, b)
	delta := int(max) - int(min)

	v = max
	if max == 0 {
		return 0, 0, 0
	}
	s = uint8(delta * 255 / int(max))
	if delta == 0 {
		return 0, s, v
	}

	var hDeg float64
	switch max {
	case r:
		hDeg = 60 * (float64(int(g)-int(b)) / float64(delta))
		if hDeg < 0 {
			hDeg += 360
		}
	case g:
		hDeg = 60*(float64(int(b)-int(r))/float64(delta)) + 120
	default: // b
		hDeg = 60*(float64(int(r)-int(g))/float64(delta)) + 240
	}
	h = uint8(hDeg/360*255 + 0.5)
	return h, s, v
}

func maxu8(a, b, c uint8) uint8 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

func minu8(a, b, c uint8) uint8 {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/color/... -v
```

Expected: all subtests `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/color
git commit -m "Add internal/color: hex/HSV-triple/named color parsing"
```

---

## Task 3: `internal/hid` — protocol constants and the low-level report round-trip

**Files:**
- Create: `internal/hid/protocol.go`
- Test: `internal/hid/protocol_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/hid/protocol_test.go`:

```go
package hid

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// fakeDevice substitutes for *goHid.Device in tests, recording writes and
// replaying queued responses — no real hardware needed.
type fakeDevice struct {
	writes   [][]byte
	replies  [][]byte
	i        int
	writeErr error
	readErr  error
}

func (f *fakeDevice) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	cp := make([]byte, len(p))
	copy(cp, p)
	f.writes = append(f.writes, cp)
	return len(p), nil
}

func (f *fakeDevice) ReadWithTimeout(p []byte, _ time.Duration) (int, error) {
	if f.readErr != nil {
		return 0, f.readErr
	}
	if f.i >= len(f.replies) {
		return 0, fmt.Errorf("fakeDevice: no more replies queued")
	}
	reply := f.replies[f.i]
	f.i++
	return copy(p, reply), nil
}

func (f *fakeDevice) Close() error { return nil }

func TestSendReport(t *testing.T) {
	reply := make([]byte, ReportLen)
	reply[0] = 0xAB
	dev := &fakeDevice{replies: [][]byte{reply}}

	got, err := sendReport(dev, []byte{cmdViaLightingGetValue, valVialRGBGetNumberLEDs})
	if err != nil {
		t.Fatalf("sendReport: %v", err)
	}
	if !bytes.Equal(got, reply) {
		t.Errorf("sendReport reply = %x, want %x", got, reply)
	}
	if len(dev.writes) != 1 {
		t.Fatalf("got %d writes, want 1", len(dev.writes))
	}
	wantWrite := make([]byte, 1+ReportLen)
	wantWrite[1] = cmdViaLightingGetValue
	wantWrite[2] = valVialRGBGetNumberLEDs
	if !bytes.Equal(dev.writes[0], wantWrite) {
		t.Errorf("wrote %x, want %x", dev.writes[0], wantWrite)
	}
}

func TestSendReportShortRead(t *testing.T) {
	dev := &fakeDevice{replies: [][]byte{make([]byte, 10)}} // too short
	if _, err := sendReport(dev, []byte{0x01}); err == nil {
		t.Fatal("sendReport: want error on short read, got nil")
	}
}

func TestSendReportWriteError(t *testing.T) {
	dev := &fakeDevice{writeErr: fmt.Errorf("boom")}
	if _, err := sendReport(dev, []byte{0x01}); err == nil {
		t.Fatal("sendReport: want error when Write fails, got nil")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/hid/...
```

Expected: `FAIL` — `sendReport`, `ReportLen`, etc. undefined.

- [ ] **Step 3: Implement**

`internal/hid/protocol.go`:

```go
// Package hid implements the raw-HID VIA/Vial/VialRGB protocol used to
// enumerate Vial-capable keyboards and set their LED colors, verified
// against quantum/via.h, quantum/vial.h, quantum/vial.c, and quantum/vialrgb.c
// in vial-qmk, and against the working reference in set_key_color.py.
package hid

import (
	"fmt"
	"time"
)

const (
	// ReportLen is the fixed HID report size this device uses.
	ReportLen = 32

	// UsagePageVial and UsageVial identify the Vial raw-HID vendor
	// interface among a device's other HID interfaces (keyboard, mouse).
	UsagePageVial = 0xFF60
	UsageVial     = 0x61

	cmdViaLightingSetValue = 0x07
	cmdViaLightingGetValue = 0x08

	valVialRGBSetMode       = 0x41
	valVialRGBDirectFastSet = 0x42
	valVialRGBGetNumberLEDs = 0x43
	valVialRGBGetLEDInfo    = 0x44

	effectDirect = 1

	idVialPrefix      = 0xFE
	vialGetKeyboardID = 0x00

	// maxKeysPerReport is VialRGB's own per-packet ceiling: 27 usable
	// payload bytes ÷ 3 bytes/LED (vialrgb.c's fast_set_leds comment).
	maxKeysPerReport = 9

	reportTimeout = 1 * time.Second
)

// rawDevice is the subset of *goHid.Device this package depends on, so
// tests can substitute a fake without opening real hardware.
type rawDevice interface {
	Write(p []byte) (int, error)
	ReadWithTimeout(p []byte, timeout time.Duration) (int, error)
	Close() error
}

// sendReport writes a 32-byte HID report (payload, zero-padded) prefixed
// with the mandatory report-ID byte (0x00 — this device has no numbered
// reports), then reads the 32-byte reply. This mirrors set_key_color.py's
// already hardware-validated send() exactly.
func sendReport(dev rawDevice, payload []byte) ([]byte, error) {
	if len(payload) > ReportLen {
		return nil, fmt.Errorf("hid: payload of %d bytes exceeds %d-byte report", len(payload), ReportLen)
	}
	buf := make([]byte, 1+ReportLen)
	copy(buf[1:], payload)

	if _, err := dev.Write(buf); err != nil {
		return nil, fmt.Errorf("hid: write: %w", err)
	}
	resp := make([]byte, ReportLen)
	n, err := dev.ReadWithTimeout(resp, reportTimeout)
	if err != nil {
		return nil, fmt.Errorf("hid: read: %w", err)
	}
	if n != ReportLen {
		return nil, fmt.Errorf("hid: short read: got %d bytes, want %d", n, ReportLen)
	}
	return resp, nil
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/hid/... -v -run TestSendReport
```

Expected: all three `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/hid
git commit -m "Add internal/hid: protocol constants and low-level report round-trip"
```

---

## Task 4: `internal/hid` — `Device` methods (UID, LED count/info, direct mode, batched set)

**Files:**
- Modify: `internal/hid/protocol.go` (add `Device` type and methods)
- Modify: `internal/hid/protocol_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/hid/protocol_test.go`:

```go
func TestGetKeyboardUID(t *testing.T) {
	reply := make([]byte, ReportLen)
	copy(reply[4:12], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	d := newDevice(&fakeDevice{replies: [][]byte{reply}})

	uid, err := d.GetKeyboardUID()
	if err != nil {
		t.Fatalf("GetKeyboardUID: %v", err)
	}
	want := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	if uid != want {
		t.Errorf("GetKeyboardUID = %v, want %v", uid, want)
	}
}

func TestGetNumberLEDs(t *testing.T) {
	reply := make([]byte, ReportLen)
	reply[2], reply[3] = 0x2C, 0x01 // 300 little-endian
	d := newDevice(&fakeDevice{replies: [][]byte{reply}})

	n, err := d.GetNumberLEDs()
	if err != nil {
		t.Fatalf("GetNumberLEDs: %v", err)
	}
	if n != 300 {
		t.Errorf("GetNumberLEDs = %d, want 300", n)
	}
}

func TestGetLEDInfo(t *testing.T) {
	reply := make([]byte, ReportLen)
	reply[5], reply[6] = 2, 3 // row, col
	d := newDevice(&fakeDevice{replies: [][]byte{reply}})

	row, col, err := d.GetLEDInfo(7)
	if err != nil {
		t.Fatalf("GetLEDInfo: %v", err)
	}
	if row != 2 || col != 3 {
		t.Errorf("GetLEDInfo = %d,%d, want 2,3", row, col)
	}
}

func TestSetDirectMode(t *testing.T) {
	fake := &fakeDevice{replies: [][]byte{make([]byte, ReportLen)}}
	d := newDevice(fake)
	if err := d.SetDirectMode(); err != nil {
		t.Fatalf("SetDirectMode: %v", err)
	}
	want := make([]byte, 1+ReportLen)
	want[1] = cmdViaLightingSetValue
	want[2] = valVialRGBSetMode
	want[3] = effectDirect
	if !bytes.Equal(fake.writes[0], want) {
		t.Errorf("wrote %x, want %x", fake.writes[0], want)
	}
}

func TestSetKeys(t *testing.T) {
	fake := &fakeDevice{replies: [][]byte{make([]byte, ReportLen)}}
	d := newDevice(fake)

	if err := d.SetKeys([]KeyColor{{Index: 5, H: 0, S: 255, V: 255}}); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	want := make([]byte, 1+ReportLen)
	want[1] = cmdViaLightingSetValue
	want[2] = valVialRGBDirectFastSet
	want[3] = 5 // start index low
	want[4] = 0 // start index high
	want[5] = 1 // count
	want[6], want[7], want[8] = 0, 255, 255
	if !bytes.Equal(fake.writes[0], want) {
		t.Errorf("wrote %x, want %x", fake.writes[0], want)
	}
}

func TestSetKeysNonContiguous(t *testing.T) {
	d := newDevice(&fakeDevice{})
	err := d.SetKeys([]KeyColor{{Index: 5}, {Index: 7}})
	if err == nil {
		t.Fatal("SetKeys: want error for non-contiguous indices")
	}
}

func TestSetKeysTooMany(t *testing.T) {
	d := newDevice(&fakeDevice{})
	keys := make([]KeyColor, maxKeysPerReport+1)
	for i := range keys {
		keys[i].Index = uint16(i)
	}
	if err := d.SetKeys(keys); err == nil {
		t.Fatal("SetKeys: want error for more than maxKeysPerReport keys")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/hid/...
```

Expected: `FAIL` — `newDevice`, `Device`, `KeyColor`, etc. undefined.

- [ ] **Step 3: Implement**

Append to `internal/hid/protocol.go`:

```go
// Device is an open connection to one Vial-capable raw-HID interface.
type Device struct {
	raw rawDevice
}

func newDevice(raw rawDevice) *Device { return &Device{raw: raw} }

// Close releases the underlying HID handle.
func (d *Device) Close() error { return d.raw.Close() }

// GetKeyboardUID returns the firmware's compile-time VIAL_KEYBOARD_UID, the
// most stable device-identity signal available (survives USB port changes).
// Per quantum/vial.c's vial_get_keyboard_id handler, the reply layout is:
// bytes 0-3 protocol version (unused here), bytes 4-11 the 8-byte UID.
func (d *Device) GetKeyboardUID() ([8]byte, error) {
	var uid [8]byte
	resp, err := sendReport(d.raw, []byte{idVialPrefix, vialGetKeyboardID})
	if err != nil {
		return uid, err
	}
	copy(uid[:], resp[4:12])
	return uid, nil
}

// GetNumberLEDs returns the device's total LED count, via VialRGB's
// VIALRGB_GET_NUMBER_LEDS.
func (d *Device) GetNumberLEDs() (uint16, error) {
	resp, err := sendReport(d.raw, []byte{cmdViaLightingGetValue, valVialRGBGetNumberLEDs})
	if err != nil {
		return 0, err
	}
	return uint16(resp[2]) | uint16(resp[3])<<8, nil
}

// GetLEDInfo returns the matrix row/col position of the given LED index, via
// VialRGB's VIALRGB_GET_LED_INFO.
func (d *Device) GetLEDInfo(index uint16) (row, col uint8, err error) {
	resp, err := sendReport(d.raw, []byte{
		cmdViaLightingGetValue, valVialRGBGetLEDInfo,
		byte(index), byte(index >> 8),
	})
	if err != nil {
		return 0, 0, err
	}
	return resp[5], resp[6], nil
}

// SetDirectMode switches the device into VialRGB's Direct mode (host-
// controlled, RAM-only colors), required before SetKeys has any effect.
func (d *Device) SetDirectMode() error {
	_, err := sendReport(d.raw, []byte{
		cmdViaLightingSetValue, valVialRGBSetMode, effectDirect, 0x00,
	})
	return err
}

// KeyColor is one LED's target color, in QMK-native HSV (each 0-255).
type KeyColor struct {
	Index   uint16
	H, S, V uint8
}

// SetKeys sets up to maxKeysPerReport (9) LEDs in one HID report, via
// VialRGB's VIALRGB_DIRECT_FASTSET. keys must have contiguous, ascending
// Index values — the wire format encodes only a start index and a count.
// internal/dispatcher is responsible for chunking accordingly before
// calling this.
func (d *Device) SetKeys(keys []KeyColor) error {
	if len(keys) == 0 {
		return nil
	}
	if len(keys) > maxKeysPerReport {
		return fmt.Errorf("hid: SetKeys got %d keys, max %d per report", len(keys), maxKeysPerReport)
	}
	start := keys[0].Index
	payload := []byte{
		cmdViaLightingSetValue, valVialRGBDirectFastSet,
		byte(start), byte(start >> 8), byte(len(keys)),
	}
	for i, k := range keys {
		if k.Index != start+uint16(i) {
			return fmt.Errorf("hid: SetKeys: index %d not contiguous from %d", k.Index, start)
		}
		payload = append(payload, k.H, k.S, k.V)
	}
	_, err := sendReport(d.raw, payload)
	return err
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/hid/... -v
```

Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/hid
git commit -m "Add internal/hid Device methods: UID, LED info, direct mode, batched set"
```

---

## Task 5: `internal/hid` — enumeration, `Open`, `Controller`, and stable-name resolution

**Files:**
- Create: `internal/hid/enumerate.go`
- Test: `internal/hid/enumerate_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/hid/enumerate_test.go`:

```go
package hid

import "testing"

func TestFilterVial(t *testing.T) {
	infos := []Info{
		{Path: "/dev/hidraw0", UsagePage: 0x0001, Usage: 0x0006}, // a keyboard interface, not ours
		{Path: "/dev/hidraw1", UsagePage: UsagePageVial, Usage: UsageVial},
	}
	got := filterVial(infos)
	if len(got) != 1 || got[0].Path != "/dev/hidraw1" {
		t.Errorf("filterVial = %+v, want only /dev/hidraw1", got)
	}
}

func TestBaseName(t *testing.T) {
	got := BaseName(Identity{HasUID: true, UID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}})
	if got != "uid-0102030405060708" {
		t.Errorf("BaseName(uid) = %q", got)
	}
	got = BaseName(Identity{VendorID: 0x5754, ProductID: 0xC401, Path: "/dev/hidraw3"})
	if got != "5754-c401-/dev/hidraw3" {
		t.Errorf("BaseName(vid/pid+path) = %q", got)
	}
	got = BaseName(Identity{VendorID: 0x5754, ProductID: 0xC401})
	if got != "5754-c401" {
		t.Errorf("BaseName(vid/pid only) = %q", got)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/hid/... -run TestFilterVial -run TestBaseName
```

Expected: `FAIL` — `Info`, `filterVial`, `Identity`, `BaseName` undefined.

- [ ] **Step 3: Implement**

`internal/hid/enumerate.go`:

```go
package hid

import (
	"fmt"

	goHid "github.com/sstallion/go-hid"
)

// Info is one attached HID interface as reported by enumeration — the
// subset of go-hid's DeviceInfo this package needs, kept as our own type so
// identity resolution can be unit-tested without go-hid's cgo-backed
// Enumerate.
type Info struct {
	Path      string
	VendorID  uint16
	ProductID uint16
	UsagePage uint16
	Usage     uint16
}

func enumerateRaw() ([]Info, error) {
	var out []Info
	err := goHid.Enumerate(goHid.VendorIDAny, goHid.ProductIDAny, func(info *goHid.DeviceInfo) error {
		out = append(out, Info{
			Path:      info.Path,
			VendorID:  info.VendorID,
			ProductID: info.ProductID,
			UsagePage: info.UsagePage,
			Usage:     info.Usage,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hid: enumerate: %w", err)
	}
	return out, nil
}

// filterVial keeps only the raw-HID vendor interface (usage page 0xFF60,
// usage 0x61) that Vial firmware exposes for VIA/Vial/VialRGB commands —
// not a device's other HID interfaces (keyboard, mouse, etc).
func filterVial(infos []Info) []Info {
	var out []Info
	for _, info := range infos {
		if info.UsagePage == UsagePageVial && info.Usage == UsageVial {
			out = append(out, info)
		}
	}
	return out
}

// Enumerate lists every currently attached Vial-capable raw-HID interface,
// vendor/product-ID agnostic (this must generalize to any Vial-capable
// device, not one hardcoded board). Callers must have already called
// goHid.Init() at process startup (and goHid.Exit() at shutdown).
func Enumerate() ([]Info, error) {
	infos, err := enumerateRaw()
	if err != nil {
		return nil, err
	}
	return filterVial(infos), nil
}

// Open opens the Vial-capable raw-HID interface at path (as returned by
// Enumerate) for VIA/Vial/VialRGB commands.
func Open(path string) (*Device, error) {
	raw, err := goHid.OpenPath(path)
	if err != nil {
		return nil, fmt.Errorf("hid: open %s: %w", path, err)
	}
	return newDevice(raw), nil
}

// Controller is the subset of Device's behavior internal/dispatcher depends
// on, so tests can substitute a fake without opening real hardware.
type Controller interface {
	SetKeys(keys []KeyColor) error
	GetNumberLEDs() (uint16, error)
	GetLEDInfo(index uint16) (row, col uint8, err error)
	Close() error
}

// Identity is a resolved identity for one attached device, gathered by
// vialrgbd's startup enumeration (and re-gathered on each hotplug poll —
// see internal/dispatcher's Registry) via the enumeration path plus a
// GetKeyboardUID probe.
type Identity struct {
	Path      string
	VendorID  uint16
	ProductID uint16
	UID       [8]byte
	HasUID    bool
}

// BaseName computes d's pre-dedup identity key: Vial UID if available, else
// VID/PID + platform path, else VID/PID alone. Exported so
// internal/dispatcher's Registry (Task 6) can use it directly as the sole
// naming/identity-matching primitive for its stateful Reconcile — dedup
// suffixing (-0, -1, ...) and reconnect/"untethered" rewire matching both
// live in Registry now, since both need state that persists across polls
// (which name is already taken; which slot is untethered) rather than a
// stateless one-shot pass over a single enumeration snapshot. Two Identity
// values are considered the same device iff BaseName(a) == BaseName(b).
func BaseName(d Identity) string {
	switch {
	case d.HasUID:
		return fmt.Sprintf("uid-%x", d.UID)
	case d.Path != "":
		return fmt.Sprintf("%04x-%04x-%s", d.VendorID, d.ProductID, d.Path)
	default:
		return fmt.Sprintf("%04x-%04x", d.VendorID, d.ProductID)
	}
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/hid/... -v
```

Expected: all `PASS`. Note `Enumerate`/`Open` themselves are not unit tested
here (they call cgo-backed go-hid against real hardware) — they're exercised
by Task 17's manual/gated hardware check, per the design spec's testing plan.

- [ ] **Step 5: Commit**

```bash
git add internal/hid
git commit -m "Add internal/hid enumeration, Open, Controller, and stable-name resolution"
```

---
## Task 6: `internal/dispatcher` — core types, device registry, color cache

**Files:**
- Create: `internal/dispatcher/types.go`
- Create: `internal/dispatcher/registry.go`
- Create: `internal/dispatcher/registry_test.go`
- Create: `internal/dispatcher/cache.go`
- Create: `internal/dispatcher/cache_test.go`

This task lays down `internal/dispatcher`'s data structures before the
single-writer goroutine (Task 7) that operates on them. `Registry` tracks
every device `vialrgbd` has ever seen by name, connected or not — replacing
the old `connectord`/`restd` split's need for anything wire-level, since
`vialrgbd` calls `hid.Controller` methods directly, in-process. Its
`Reconcile` method is both the naming authority (using `hid.BaseName` for
identity plus its own `-0`/`-1`/`-2` dedup suffixing, scoped across every
name Registry has ever assigned rather than one enumeration snapshot, since
rewire/eviction need cross-poll state a stateless per-snapshot naming pass
can't hold) and how reconnect detection works, per the design
spec's "State persistence & refresh" section, which this task's step 2 also
amends with the "untethered" refinement below. `Cache` is the last-known-good
color store that same section requires, since VialRGB Direct-mode colors
live in the keyboard's RAM only and can't be read back after a
reset/replug/brownout.

**Untethered rewire semantics.** A device that disconnects doesn't lose its
name or cache immediately: its slot transitions from `connected` to
`untethered` (cache retained, disconnect timestamp recorded) rather than
being freed. On the next `Reconcile`, a present device is matched against
existing slots by `hid.BaseName(identity)` equality:
- If the match is `untethered`, it's a rewire: the slot goes back to
  `connected` under the *same* name, attached to the new (post-replug)
  `hid.Controller`, and is reported in `Reconnected` so Task 9's redraw loop
  replays its cached colors onto the reconnected hardware.
- If the match is already `connected` — a second, simultaneously-present
  device that happens to compute the same base identity — it is never
  stolen; the newly-seen device gets the next dedup-suffixed name instead,
  with a fresh, empty cache, exactly as if it were the first time that
  identity had ever collided.

Any `untethered` slot older than `UntetheredMaxAge` (24h) is deleted on the
`Reconcile` call that first notices the age, reported in `Evicted` so the
caller can also forget its `Cache` entry — a later device presenting that
same identity is then a fresh allocation, not a rewire, and starts with an
empty cache. `Reconcile` runs on `cmd/vialrgbd`'s existing 1s hotplug-poll
cadence (Task 14), so eviction is checked at least that often — tighter than
"the same 5s ticker that drives periodic redraw" would give, and without
needing a third, separate ticker.

One known, accepted limitation: the VID/PID-alone identity tier (used only
when a device's platform path is unavailable) can't distinguish two
physically distinct devices at all, so which of two such simultaneously-open
devices rewires into a given pre-existing slot on a later poll is
unspecified — this is an existing limitation of that fallback tier's
ambiguity (see Task 5), not something Reconcile's rewire logic introduces.

- [ ] **Step 1: Write the failing tests**

`internal/dispatcher/registry_test.go`:
```go
package dispatcher

import (
	"testing"
	"time"

	"github.com/seefood/vialrgb-notify/internal/hid"
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
```

`internal/dispatcher/cache_test.go`:
```go
package dispatcher

import (
	"reflect"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/hid"
)

func TestCacheUpdateAndSnapshot(t *testing.T) {
	c := NewCache()
	if got := c.Snapshot("a"); got != nil {
		t.Fatalf("Snapshot before any Update = %v, want nil", got)
	}

	c.Update("a", []hid.KeyColor{{Index: 2, H: 1, S: 2, V: 3}})
	c.Update("a", []hid.KeyColor{{Index: 0, H: 4, S: 5, V: 6}})
	c.Update("a", []hid.KeyColor{{Index: 2, H: 9, S: 9, V: 9}}) // overwrite index 2

	got := c.Snapshot("a")
	want := []hid.KeyColor{{Index: 0, H: 4, S: 5, V: 6}, {Index: 2, H: 9, S: 9, V: 9}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot = %+v, want %+v", got, want)
	}
}

func TestCacheSnapshotOtherDeviceUnaffected(t *testing.T) {
	c := NewCache()
	c.Update("a", []hid.KeyColor{{Index: 0}})
	if got := c.Snapshot("b"); got != nil {
		t.Errorf("Snapshot(b) = %v, want nil", got)
	}
}

func TestCacheForget(t *testing.T) {
	c := NewCache()
	c.Update("a", []hid.KeyColor{{Index: 0}})
	c.Forget("a")
	if got := c.Snapshot("a"); got != nil {
		t.Errorf("Snapshot after Forget = %v, want nil", got)
	}
}
```

Run: `go test ./internal/dispatcher/...` — fails, package doesn't exist yet.

- [ ] **Step 2: Implement**

`internal/dispatcher/types.go`:
```go
// Package dispatcher is vialrgbd's single-writer "traffic cop" for the HID
// handle: every other goroutine (HTTP handlers; the periodic/reconnect
// redraw loops) submits work through the Dispatcher and never touches a
// hid.Controller directly, since hidapi is not guaranteed safe under
// concurrent access to the same handle.
package dispatcher

import "errors"

// ErrDeviceNotFound is returned for a device name not present in the
// registry — internal/api maps this to a 404.
var ErrDeviceNotFound = errors.New("dispatcher: unknown device")

// ErrQueueFull is returned when the dispatcher's request queue is at
// capacity — internal/api maps this to a 503.
var ErrQueueFull = errors.New("dispatcher: request queue full")

// DeviceSummary is one entry in a ListDevices result.
type DeviceSummary struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// LEDPosition is one LED's matrix location, as reported by VialRGB's
// VIALRGB_GET_LED_INFO.
type LEDPosition struct {
	Index uint16 `json:"index"`
	Row   uint8  `json:"row"`
	Col   uint8  `json:"col"`
}

// Capabilities is GetCapabilities's result.
type Capabilities struct {
	LEDCount  int           `json:"led_count"`
	Positions []LEDPosition `json:"positions"`
}
```

`internal/dispatcher/registry.go`:
```go
package dispatcher

import (
	"fmt"
	"sync"
	"time"

	"github.com/seefood/vialrgb-notify/internal/hid"
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

// Registry is vialrgbd's persistent name-assignment and presence state: name
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
```

`internal/dispatcher/cache.go`:
```go
package dispatcher

import (
	"sort"
	"sync"

	"github.com/seefood/vialrgb-notify/internal/hid"
)

// Cache remembers the last color successfully written to each key of each
// device, since VialRGB Direct-mode colors live in the keyboard's RAM only
// and are lost on any reset/replug/brownout with no way to read them back
// (design spec's "State persistence & refresh"). Not persisted to disk — on
// a vialrgbd restart there's nothing more trustworthy to reload than an
// empty cache.
type Cache struct {
	mu       sync.Mutex
	byDevice map[string]map[uint16]hid.KeyColor
}

// NewCache creates an empty Cache.
func NewCache() *Cache {
	return &Cache{byDevice: make(map[string]map[uint16]hid.KeyColor)}
}

// Update records keys as the last color successfully written to device.
func (c *Cache) Update(device string, keys []hid.KeyColor) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.byDevice[device]
	if !ok {
		m = make(map[uint16]hid.KeyColor)
		c.byDevice[device] = m
	}
	for _, k := range keys {
		m[k.Index] = k
	}
}

// Snapshot returns every cached key/color for device, in ascending index
// order (so callers can batch contiguous runs the same way live SetKey
// traffic does), or nil if the device has never had a color set.
func (c *Cache) Snapshot(device string) []hid.KeyColor {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.byDevice[device]
	if !ok || len(m) == 0 {
		return nil
	}
	out := make([]hid.KeyColor, 0, len(m))
	for _, k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// Forget deletes device's cache entry entirely — called when Registry's
// Reconcile reports device as Evicted (untethered longer than
// UntetheredMaxAge), so a later device reconnecting under that identity
// starts with an empty cache rather than replaying stale colors.
func (c *Cache) Forget(device string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byDevice, device)
}
```

- [ ] **Step 3: Verify**

Run: `go test ./internal/dispatcher/...` — passes.

- [ ] **Step 4: Commit**

```bash
git add internal/dispatcher
git commit -m "Add internal/dispatcher types, device registry, and color cache"
```

---

## Task 7: `internal/dispatcher` — single-writer dispatcher core

**Files:**
- Create: `internal/dispatcher/dispatcher.go`
- Create: `internal/dispatcher/dispatcher_test.go`

This is the "traffic cop" itself: one goroutine (`Run`) that owns every
`hid.Controller` in the `Registry` exclusively, per the design spec's
concurrency section. `SetKey`/`ListDevices`/`GetCapabilities` submit a `job`
and block for its `jobResult`. `Run`'s batching call, `groupForSend`, is
Task 8's job — this task's `dispatchGroup`/`Run` already call it, so
**Task 7 and Task 8 must be committed together**; `go build` fails between
them (same pattern as Tasks 10/11 later in this plan). Note there's no
separate per-request deadline here: `internal/hid`'s own per-report timeout
(`protocol.go`'s `reportTimeout`) already bounds each individual write/read,
so a wedged/unplugged device can't hang `Run` — nothing else needs to. These
tests use Task 6's `registryWithConnected`/`registryWithConnectedMulti` test
helpers (same package) to seed a `Registry` with a known device name directly,
rather than going through `Reconcile`'s identity-based naming, since naming
itself is out of scope here.

- [ ] **Step 1: Write the failing tests**

`internal/dispatcher/dispatcher_test.go`:
```go
package dispatcher

import (
	"context"
	"errors"
	"testing"
)

func TestDispatcherSetKeyQueueFull(t *testing.T) {
	reg := registryWithConnected("a", &fakeController{})
	d := New(reg, NewCache(), 0) // zero-depth queue: never has room, and Run is never even started

	err := d.SetKey(context.Background(), "a", 0, 0, 255, 255)
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("SetKey error = %v, want ErrQueueFull", err)
	}
}

func TestDispatcherSetKeyUnknownDevice(t *testing.T) {
	reg := NewRegistry()
	d := New(reg, NewCache(), 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	err := d.SetKey(context.Background(), "missing", 0, 0, 255, 255)
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("SetKey error = %v, want ErrDeviceNotFound", err)
	}
}

func TestDispatcherSetKeyUpdatesCache(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	cache := NewCache()
	d := New(reg, cache, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	if err := d.SetKey(context.Background(), "a", 3, 1, 2, 3); err != nil {
		t.Fatalf("SetKey: %v", err)
	}
	if len(fc.lastSet) != 1 || fc.lastSet[0].Index != 3 {
		t.Errorf("controller.SetKeys called with %+v", fc.lastSet)
	}
	if snap := cache.Snapshot("a"); len(snap) != 1 || snap[0].Index != 3 {
		t.Errorf("cache.Snapshot = %+v", snap)
	}
}

func TestDispatcherListDevices(t *testing.T) {
	reg := registryWithConnected("a", &fakeController{})
	d := New(reg, NewCache(), 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	devices, err := d.ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "a" || !devices[0].Connected {
		t.Errorf("ListDevices = %+v", devices)
	}
}

func TestDispatcherGetCapabilities(t *testing.T) {
	reg := registryWithConnected("a", &fakeController{
		numLEDs: 2, positions: map[uint16][2]uint8{0: {1, 1}, 1: {2, 2}},
	})
	d := New(reg, NewCache(), 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	caps, err := d.GetCapabilities(context.Background(), "a")
	if err != nil {
		t.Fatalf("GetCapabilities: %v", err)
	}
	if caps.LEDCount != 2 || len(caps.Positions) != 2 {
		t.Errorf("caps = %+v", caps)
	}
}
```

Run: `go test ./internal/dispatcher/...` — fails to compile (`groupForSend`
undefined; land alongside Task 8).

- [ ] **Step 2: Implement**

`internal/dispatcher/dispatcher.go`:
```go
package dispatcher

import (
	"context"

	"github.com/seefood/vialrgb-notify/internal/hid"
)

type opKind int

const (
	opSetKey opKind = iota
	opListDevices
	opGetCapabilities
)

type job struct {
	kind   opKind
	device string
	key    hid.KeyColor // valid when kind == opSetKey
	reply  chan jobResult
}

type jobResult struct {
	err  error
	list []DeviceSummary
	caps Capabilities
}

// Dispatcher serializes all access to registry's open hid.Controllers.
type Dispatcher struct {
	registry *Registry
	cache    *Cache
	queue    chan job
}

// New creates a Dispatcher. queueDepth bounds in-flight requests (spec:
// e.g. 64) — a full queue fails fast with ErrQueueFull rather than growing
// goroutines/memory without bound.
func New(registry *Registry, cache *Cache, queueDepth int) *Dispatcher {
	return &Dispatcher{registry: registry, cache: cache, queue: make(chan job, queueDepth)}
}

func (d *Dispatcher) submit(ctx context.Context, j job) (jobResult, error) {
	select {
	case d.queue <- j:
	default:
		return jobResult{}, ErrQueueFull
	}
	select {
	case res := <-j.reply:
		return res, nil
	case <-ctx.Done():
		return jobResult{}, ctx.Err()
	}
}

// SetKey submits one LED color change and blocks for its result.
func (d *Dispatcher) SetKey(ctx context.Context, device string, index uint16, h, s, v uint8) error {
	res, err := d.submit(ctx, job{
		kind:  opSetKey,
		device: device,
		key:   hid.KeyColor{Index: index, H: h, S: s, V: v},
		reply: make(chan jobResult, 1),
	})
	if err != nil {
		return err
	}
	return res.err
}

// ListDevices returns every currently registered device and whether it's
// presently connected.
func (d *Dispatcher) ListDevices(ctx context.Context) ([]DeviceSummary, error) {
	res, err := d.submit(ctx, job{kind: opListDevices, reply: make(chan jobResult, 1)})
	if err != nil {
		return nil, err
	}
	return res.list, res.err
}

// GetCapabilities returns device's LED count and matrix positions.
func (d *Dispatcher) GetCapabilities(ctx context.Context, device string) (Capabilities, error) {
	res, err := d.submit(ctx, job{kind: opGetCapabilities, device: device, reply: make(chan jobResult, 1)})
	if err != nil {
		return Capabilities{}, err
	}
	return res.caps, res.err
}

// Run is the single dispatcher goroutine: it owns every hid.Controller in
// registry exclusively. On each cycle it takes one job, then
// non-blockingly drains any others already queued and batches same-device
// contiguous SetKey jobs (see batch.go) before issuing them. It runs until
// ctx is canceled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		var first job
		select {
		case first = <-d.queue:
		case <-ctx.Done():
			return
		}
		batch := []job{first}
	drain:
		for {
			select {
			case j := <-d.queue:
				batch = append(batch, j)
			default:
				break drain
			}
		}
		for _, group := range groupForSend(batch) {
			d.dispatchGroup(group)
		}
	}
}

func (d *Dispatcher) dispatchGroup(group []job) {
	switch group[0].kind {
	case opSetKey:
		d.dispatchSetKeys(group)
	case opListDevices:
		group[0].reply <- jobResult{list: d.registry.Summaries()}
	case opGetCapabilities:
		caps, err := d.getCapabilities(group[0].device)
		group[0].reply <- jobResult{caps: caps, err: err}
	}
}

func (d *Dispatcher) dispatchSetKeys(group []job) {
	device := group[0].device
	ctrl, ok := d.registry.Get(device)
	if !ok {
		failAll(group, ErrDeviceNotFound)
		return
	}
	keys := make([]hid.KeyColor, len(group))
	for i, j := range group {
		keys[i] = j.key
	}
	err := ctrl.SetKeys(keys)
	if err == nil {
		d.cache.Update(device, keys)
	}
	for _, j := range group {
		j.reply <- jobResult{err: err}
	}
}

func (d *Dispatcher) getCapabilities(device string) (Capabilities, error) {
	ctrl, ok := d.registry.Get(device)
	if !ok {
		return Capabilities{}, ErrDeviceNotFound
	}
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

func failAll(group []job, err error) {
	for _, j := range group {
		j.reply <- jobResult{err: err}
	}
}
```

- [ ] **Step 3: Verify** — after landing Task 8's `groupForSend` in the same
commit, `go test ./internal/dispatcher/...` passes.

- [ ] **Step 4: Commit** — see Task 8 (combined commit).

---

## Task 8: `internal/dispatcher` — contiguous-index batching

**Files:**
- Create: `internal/dispatcher/batch.go`
- Create: `internal/dispatcher/batch_test.go`

`groupForSend` partitions a drained batch of jobs into groups that become one
`hid.Controller.SetKeys` call each, per the design spec: same device,
contiguous LED index, up to VialRGB's own 9-LED-per-packet ceiling
(`internal/hid`'s `maxKeysPerReport`). Non-`SetKey` jobs (`ListDevices`,
`GetCapabilities`) never merge with anything. Land this together with Task 7
— `go build ./...` doesn't pass until both are present.

- [ ] **Step 1: Write the failing tests**

`internal/dispatcher/batch_test.go`:
```go
package dispatcher

import (
	"testing"

	"github.com/seefood/vialrgb-notify/internal/hid"
)

func setKeyJob(device string, index uint16) job {
	return job{kind: opSetKey, device: device, key: hid.KeyColor{Index: index}, reply: make(chan jobResult, 1)}
}

func TestGroupForSendBatchesContiguous(t *testing.T) {
	batch := []job{
		setKeyJob("a", 0),
		setKeyJob("a", 1),
		setKeyJob("a", 2),  // contiguous, same device -> one group
		setKeyJob("b", 5),  // different device -> its own group
		setKeyJob("a", 10), // non-contiguous -> its own group
	}
	groups := groupForSend(batch)
	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}
	if len(groups[0]) != 3 {
		t.Errorf("group 0 has %d items, want 3", len(groups[0]))
	}
	if len(groups[1]) != 1 || len(groups[2]) != 1 {
		t.Errorf("groups 1,2 want size 1 each, got %d,%d", len(groups[1]), len(groups[2]))
	}
}

func TestGroupForSendCapsAtNine(t *testing.T) {
	batch := make([]job, 12)
	for i := range batch {
		batch[i] = setKeyJob("a", uint16(i))
	}
	groups := groupForSend(batch)
	if len(groups) != 2 || len(groups[0]) != 9 || len(groups[1]) != 3 {
		t.Fatalf("got group sizes %v; want [9 3]", groupSizes(groups))
	}
}

func TestGroupForSendNonSetKeyAlwaysSingleton(t *testing.T) {
	batch := []job{
		{kind: opListDevices, reply: make(chan jobResult, 1)},
		{kind: opListDevices, reply: make(chan jobResult, 1)},
	}
	groups := groupForSend(batch)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (non-SetKey ops never merge)", len(groups))
	}
}

func groupSizes(groups [][]job) []int {
	sizes := make([]int, len(groups))
	for i, g := range groups {
		sizes[i] = len(g)
	}
	return sizes
}
```

Run: `go test ./internal/dispatcher/...` — fails, `groupForSend` undefined.

- [ ] **Step 2: Implement**

`internal/dispatcher/batch.go`:
```go
package dispatcher

// maxBatch is VialRGB's own per-packet ceiling (see internal/hid's
// maxKeysPerReport) — batches larger than this become multiple groups.
const maxBatch = 9

// groupForSend partitions a drained batch of jobs into groups that become
// one hid.Controller.SetKeys call each: non-SetKey jobs are always
// singleton groups; SetKey jobs are grouped per the design spec — same
// device, contiguous LED index, up to maxBatch — preserving arrival order.
func groupForSend(batch []job) [][]job {
	var out [][]job
	var run []job
	var runDevice string
	var runNextIndex uint16

	flush := func() {
		if len(run) > 0 {
			out = append(out, run)
			run = nil
		}
	}

	for _, j := range batch {
		if j.kind != opSetKey {
			flush()
			out = append(out, []job{j})
			continue
		}
		if len(run) > 0 && j.device == runDevice && j.key.Index == runNextIndex && len(run) < maxBatch {
			run = append(run, j)
			runNextIndex++
			continue
		}
		flush()
		run = []job{j}
		runDevice = j.device
		runNextIndex = j.key.Index + 1
	}
	flush()
	return out
}
```

- [ ] **Step 3: Verify**

Run: `go test ./internal/dispatcher/...` — all of Task 6/7/8's tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/dispatcher
git commit -m "Add internal/dispatcher single-writer core and contiguous-index batching"
```

---

## Task 9: `internal/dispatcher` — state persistence: reconnect and periodic redraw

**Files:**
- Create: `internal/dispatcher/redraw.go`
- Create: `internal/dispatcher/redraw_test.go`

This is the design spec's "State persistence & refresh" behavior:
`Dispatcher.Redraw` replays a device's cached colors through the normal
`SetKey` path (so it's serialized/batched exactly like any other write, and
updates the same cache it read from — a harmless no-op re-write).
`RunPeriodicRedraw` does this unconditionally for every currently *Connected*
device every `interval` (Registry's `Summaries` now also lists `Untethered`
devices per Task 6, which this deliberately skips — there's no controller to
write to until a rewire reconnects one), regardless of reconnect state — the
safety net for a firmware-side soft reset that never drops the USB
connection, so no reconnect is ever detected for it. `RedrawReconnected` does
it immediately for devices `Registry.Reconcile` just reported as
`Reconnected` (a rewire), so a replug doesn't have to wait for the next
periodic tick. Task 14's `main()` wires both: `RedrawReconnected` off the
existing hotplug-poll loop that already calls `Reconcile`, `RunPeriodicRedraw`
off its own independent ticker — two genuinely independent triggers, per the
design spec, not one mechanism wearing two names. This task also adds a test
proving the untethered-rewire-regains-cache behavior end to end within this
package (Registry + Cache + Dispatcher composed, no `cmd/vialrgbd` needed).

- [ ] **Step 1: Write the failing tests**

`internal/dispatcher/redraw_test.go`:
```go
package dispatcher

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/seefood/vialrgb-notify/internal/hid"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRedrawEmptyCacheIsNoop(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	d := New(reg, NewCache(), 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	if err := d.Redraw(context.Background(), "a"); err != nil {
		t.Fatalf("Redraw: %v", err)
	}
	if fc.lastSet != nil {
		t.Errorf("SetKeys called on empty cache: %+v", fc.lastSet)
	}
}

func TestRedrawReplaysCache(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0, H: 1, S: 2, V: 3}})
	d := New(reg, cache, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	if err := d.Redraw(context.Background(), "a"); err != nil {
		t.Fatalf("Redraw: %v", err)
	}
	if len(fc.lastSet) != 1 || fc.lastSet[0].H != 1 {
		t.Errorf("SetKeys called with %+v", fc.lastSet)
	}
}

func TestRedrawReconnectedOnlyTouchesGivenDevices(t *testing.T) {
	fcA, fcB := &fakeController{}, &fakeController{}
	reg := registryWithConnectedMulti(map[string]hid.Controller{"a": fcA, "b": fcB})
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0}})
	cache.Update("b", []hid.KeyColor{{Index: 0}})
	d := New(reg, cache, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	d.RedrawReconnected(context.Background(), []string{"a"}, discardLogger())

	if fcA.lastSet == nil {
		t.Error("device a (reconnected) was not redrawn")
	}
	if fcB.lastSet != nil {
		t.Error("device b (not in reconnected list) should not have been redrawn")
	}
}

func TestRunPeriodicRedrawFiresOnTick(t *testing.T) {
	fc := &fakeController{}
	reg := registryWithConnected("a", fc)
	cache := NewCache()
	cache.Update("a", []hid.KeyColor{{Index: 0}})
	d := New(reg, cache, 8)
	dispatchCtx, cancelDispatch := context.WithCancel(context.Background())
	defer cancelDispatch()
	go d.Run(dispatchCtx)

	redrawCtx, cancelRedraw := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelRedraw()
	d.RunPeriodicRedraw(redrawCtx, 10*time.Millisecond, discardLogger())

	if fc.lastSet == nil {
		t.Error("periodic redraw never fired within the test window")
	}
}

func TestReconnectRewireRegainsCache(t *testing.T) {
	reg := NewRegistry()
	cache := NewCache()
	d := New(reg, cache, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	id := hid.Identity{HasUID: true, UID: [8]byte{7}}
	res := reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: &fakeController{}}}, time.Now(), UntetheredMaxAge)
	var name string
	for n := range res.Devices {
		name = n
	}

	if err := d.SetKey(context.Background(), name, 0, 1, 2, 3); err != nil {
		t.Fatalf("SetKey: %v", err)
	}

	reg.Reconcile(nil, time.Now(), UntetheredMaxAge) // disconnect: slot goes untethered, cache retained

	// Replug: same identity, a fresh hid.Controller instance, as a real
	// replug would produce.
	fc2 := &fakeController{}
	res = reg.Reconcile([]PresentDevice{{Identity: id, Ctrl: fc2}}, time.Now(), UntetheredMaxAge)
	if len(res.Reconnected) != 1 || res.Reconnected[0] != name {
		t.Fatalf("Reconnected = %v, want [%s] (rewire)", res.Reconnected, name)
	}

	d.RedrawReconnected(context.Background(), res.Reconnected, discardLogger())

	if len(fc2.lastSet) != 1 || fc2.lastSet[0].H != 1 {
		t.Errorf("new controller's SetKeys = %+v, want the pre-disconnect color replayed via rewire", fc2.lastSet)
	}
}
```

Run: `go test ./internal/dispatcher/...` — fails, `Redraw`/`RedrawReconnected`/`RunPeriodicRedraw` undefined.

- [ ] **Step 2: Implement**

`internal/dispatcher/redraw.go`:
```go
package dispatcher

import (
	"context"
	"log/slog"
	"time"
)

// RedrawInterval is the unconditional periodic redraw cadence from the
// design spec's "State persistence & refresh": every registered device's
// full cached color set is replayed every 5 seconds, always, regardless of
// reconnect detection.
const RedrawInterval = 5 * time.Second

// Redraw replays device's full cached color set through the normal SetKey
// dispatch path (so it's serialized/batched exactly like any other write).
// A device with an empty cache (never had a color set) is a no-op.
func (d *Dispatcher) Redraw(ctx context.Context, device string) error {
	for _, k := range d.cache.Snapshot(device) {
		if err := d.SetKey(ctx, device, k.Index, k.H, k.S, k.V); err != nil {
			return err
		}
	}
	return nil
}

// RunPeriodicRedraw redraws every currently Connected device's cache every
// interval, independent of any reconnect detection, until ctx is canceled.
// Untethered devices (Registry.Summaries now reports those too, per Task 6)
// are skipped — there's no live controller to write to until a rewire
// reconnects one.
func (d *Dispatcher) RunPeriodicRedraw(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for _, dev := range d.registry.Summaries() {
				if !dev.Connected {
					continue
				}
				if err := d.Redraw(ctx, dev.Name); err != nil {
					logger.Warn("periodic redraw failed", "device", dev.Name, "err", err)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// RedrawReconnected redraws every name in reconnected — the set returned by
// Registry.Reconcile when an Untethered slot rewires back to Connected — so
// a replug doesn't have to wait for RunPeriodicRedraw's next tick.
func (d *Dispatcher) RedrawReconnected(ctx context.Context, reconnected []string, logger *slog.Logger) {
	for _, name := range reconnected {
		if err := d.Redraw(ctx, name); err != nil {
			logger.Warn("reconnect redraw failed", "device", name, "err", err)
		}
	}
}
```

- [ ] **Step 3: Verify**

Run: `go test ./internal/dispatcher/...` — passes.

- [ ] **Step 4: Commit**

```bash
git add internal/dispatcher
git commit -m "Add internal/dispatcher reconnect and periodic state-persistence redraw"
```

---

## Task 10: `internal/api` — `PUT /devices/{name}/keys/{row,col}` handler

**Files:**
- Create: `internal/api/handlers.go`
- Create: `internal/api/handlers_test.go`

`internal/api` implements vialrgbd's HTTP surface. `Handler` depends on a
`Dispatcher` interface (the subset of `*dispatcher.Dispatcher`'s methods it
needs) so tests can substitute a fake without a real dispatcher goroutine,
and a `CapabilitiesSource` interface (Task 11 supplies the real
`CapabilitiesCache` implementation) so tests can substitute a fake there too
— that means **this task and Task 11 must land together**; `go build`
doesn't pass with only `fakeCaps` referenced and no `CapabilitiesCache`
defined, but the reverse split (defining `CapabilitiesCache` with no
handlers using it) would be equally incomplete, so committing them as one
unit is simplest.

- [ ] **Step 1: Write the failing tests**

`internal/api/handlers_test.go`:
```go
package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/dispatcher"
)

// fakeDispatcher and fakeCaps are reused by every test file in this package.
type fakeDispatcher struct {
	setKey     func(ctx context.Context, device string, index uint16, h, s, v uint8) error
	listResult []dispatcher.DeviceSummary
	listErr    error
	caps       dispatcher.Capabilities
	capsErr    error
	capsCalls  int
}

func (f *fakeDispatcher) SetKey(ctx context.Context, device string, index uint16, h, s, v uint8) error {
	return f.setKey(ctx, device, index, h, s, v)
}

func (f *fakeDispatcher) ListDevices(context.Context) ([]dispatcher.DeviceSummary, error) {
	return f.listResult, f.listErr
}

func (f *fakeDispatcher) GetCapabilities(context.Context, string) (dispatcher.Capabilities, error) {
	f.capsCalls++
	return f.caps, f.capsErr
}

type fakeCaps struct {
	index uint16
	found bool
	err   error
}

func (f *fakeCaps) IndexFor(context.Context, string, uint8, uint8) (uint16, bool, error) {
	return f.index, f.found, f.err
}

func TestSetKeySuccess(t *testing.T) {
	var gotDevice string
	var gotIndex uint16
	var gotH, gotS, gotV uint8
	disp := &fakeDispatcher{setKey: func(_ context.Context, device string, index uint16, h, s, v uint8) error {
		gotDevice, gotIndex, gotH, gotS, gotV = device, index, h, s, v
		return nil
	}}
	h := NewHandler(disp, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if gotDevice != "cxt12e4-0" || gotIndex != 5 || gotH != 0 || gotS != 255 || gotV != 255 {
		t.Errorf("SetKey called with device=%q index=%d h=%d s=%d v=%d", gotDevice, gotIndex, gotH, gotS, gotV)
	}
}

func TestSetKeyBadColor(t *testing.T) {
	disp := &fakeDispatcher{setKey: func(context.Context, string, uint16, uint8, uint8, uint8) error {
		t.Fatal("dispatcher should not be called for a malformed color")
		return nil
	}}
	h := NewHandler(disp, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"not-a-color"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSetKeyUnknownKeyPosition(t *testing.T) {
	disp := &fakeDispatcher{setKey: func(context.Context, string, uint16, uint8, uint8, uint8) error {
		t.Fatal("dispatcher should not be called when the key position isn't found")
		return nil
	}}
	h := NewHandler(disp, &fakeCaps{found: false})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/99,99", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSetKeyUnknownDevice(t *testing.T) {
	h := NewHandler(&fakeDispatcher{}, &fakeCaps{err: dispatcher.ErrDeviceNotFound})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/nope/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSetKeyDispatcherUnavailable(t *testing.T) {
	disp := &fakeDispatcher{setKey: func(context.Context, string, uint16, uint8, uint8, uint8) error {
		return errors.New("boom")
	}}
	h := NewHandler(disp, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
```

Run: `go test ./internal/api/...` — fails, package doesn't exist yet.

- [ ] **Step 2: Implement**

`internal/api/handlers.go`:
```go
// Package api implements vialrgbd's HTTP surface: the PUT/GET routes from
// the design spec, backed directly by an in-process internal/dispatcher.Dispatcher
// (no RPC layer — single binary, single process).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/seefood/vialrgb-notify/internal/color"
	"github.com/seefood/vialrgb-notify/internal/dispatcher"
)

// Dispatcher is the subset of *dispatcher.Dispatcher the HTTP handlers need,
// so tests can substitute a fake without a real dispatcher goroutine.
type Dispatcher interface {
	SetKey(ctx context.Context, device string, index uint16, h, s, v uint8) error
	ListDevices(ctx context.Context) ([]dispatcher.DeviceSummary, error)
	GetCapabilities(ctx context.Context, device string) (dispatcher.Capabilities, error)
}

// CapabilitiesSource resolves row,col to a LED index for a named device.
// Returns (_, false, dispatcher.ErrDeviceNotFound) for an unknown device,
// (_, false, nil) for a known device with no key at that position, and a
// non-nil err for a genuine dispatch failure.
type CapabilitiesSource interface {
	IndexFor(ctx context.Context, device string, row, col uint8) (uint16, bool, error)
}

// Handler holds vialrgbd's HTTP dependencies and builds its route table.
type Handler struct {
	disp Dispatcher
	caps CapabilitiesSource
}

// NewHandler constructs a Handler.
func NewHandler(disp Dispatcher, caps CapabilitiesSource) *Handler {
	return &Handler{disp: disp, caps: caps}
}

// Routes builds vialrgbd's route table (Go 1.22+ ServeMux method+wildcard
// patterns — no external router dependency needed).
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /devices/{name}/keys/{pos}", h.setKey)
	mux.HandleFunc("GET /devices", h.listDevices)
	mux.HandleFunc("GET /devices/{name}", h.getCapabilities)
	return mux
}

type setKeyBody struct {
	Color string `json:"color"`
}

func (h *Handler) setKey(w http.ResponseWriter, r *http.Request) {
	device := r.PathValue("name")
	row, col, err := parsePos(r.PathValue("pos"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	var body setKeyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("api: invalid request body: %w", err))
		return
	}
	hue, sat, val, err := color.Parse(body.Color)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	index, found, err := h.caps.IndexFor(r.Context(), device, row, col)
	if errors.Is(err, dispatcher.ErrDeviceNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("api: device %q has no key at %d,%d", device, row, col))
		return
	}

	if err := h.disp.SetKey(r.Context(), device, index, hue, sat, val); err != nil {
		writeError(w, statusForDispatchErr(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parsePos(pos string) (row, col uint8, err error) {
	parts := strings.Split(pos, ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("api: invalid key position %q: want row,col", pos)
	}
	r, err1 := strconv.ParseUint(parts[0], 10, 8)
	c, err2 := strconv.ParseUint(parts[1], 10, 8)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("api: invalid key position %q: want row,col", pos)
	}
	return uint8(r), uint8(c), nil
}

func statusForDispatchErr(err error) int {
	if errors.Is(err, dispatcher.ErrDeviceNotFound) {
		return http.StatusNotFound
	}
	return http.StatusServiceUnavailable
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
```

- [ ] **Step 3: Verify** — after landing Task 11's `CapabilitiesCache` and
`listDevices`/`getCapabilities` handlers in the same commit, `go test
./internal/api/...` passes.

- [ ] **Step 4: Commit** — see Task 11 (combined commit).

---

## Task 11: `internal/api` — capabilities cache, `GET /devices`, `GET /devices/{name}`

**Files:**
- Create: `internal/api/caps.go`
- Create: `internal/api/caps_test.go`
- Modify: `internal/api/handlers.go` (add `listDevices`/`getCapabilities`)
- Create: `internal/api/handlers_list_test.go`

`CapabilitiesCache` implements `CapabilitiesSource` by calling
`Dispatcher.GetCapabilities` once per device and caching the row,col->index
mapping, since each `GetCapabilities` call is a full HID round trip (one
`VIALRGB_GET_NUMBER_LEDS` plus one `VIALRGB_GET_LED_INFO` per LED) — every
`PUT` would otherwise re-walk the whole matrix before setting one key. Land
this together with Task 10 (see that task's note).

- [ ] **Step 1: Write the failing tests**

`internal/api/caps_test.go`:
```go
package api

import (
	"context"
	"errors"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/dispatcher"
)

func TestCapabilitiesCacheIndexFor(t *testing.T) {
	disp := &fakeDispatcher{caps: dispatcher.Capabilities{
		LEDCount:  2,
		Positions: []dispatcher.LEDPosition{{Index: 0, Row: 1, Col: 1}, {Index: 1, Row: 2, Col: 2}},
	}}
	cache := NewCapabilitiesCache(disp)

	index, found, err := cache.IndexFor(context.Background(), "cxt12e4-0", 2, 2)
	if err != nil || !found || index != 1 {
		t.Fatalf("IndexFor = %d,%v,%v; want 1,true,nil", index, found, err)
	}
	if _, _, err := cache.IndexFor(context.Background(), "cxt12e4-0", 1, 1); err != nil {
		t.Fatalf("second IndexFor: %v", err)
	}
	if disp.capsCalls != 1 {
		t.Errorf("GetCapabilities called %d times, want 1 (second call should hit the cache)", disp.capsCalls)
	}
}

func TestCapabilitiesCacheUnknownDevice(t *testing.T) {
	disp := &fakeDispatcher{capsErr: dispatcher.ErrDeviceNotFound}
	cache := NewCapabilitiesCache(disp)

	_, _, err := cache.IndexFor(context.Background(), "nope", 0, 0)
	if !errors.Is(err, dispatcher.ErrDeviceNotFound) {
		t.Fatalf("IndexFor error = %v, want ErrDeviceNotFound", err)
	}
}

func TestCapabilitiesCacheNotPresent(t *testing.T) {
	disp := &fakeDispatcher{caps: dispatcher.Capabilities{
		LEDCount: 1, Positions: []dispatcher.LEDPosition{{Index: 0, Row: 0, Col: 0}},
	}}
	cache := NewCapabilitiesCache(disp)

	_, found, err := cache.IndexFor(context.Background(), "cxt12e4-0", 9, 9)
	if err != nil || found {
		t.Fatalf("IndexFor = _,%v,%v; want false,nil", found, err)
	}
}
```

`internal/api/handlers_list_test.go`:
```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/dispatcher"
)

func TestListDevices(t *testing.T) {
	disp := &fakeDispatcher{listResult: []dispatcher.DeviceSummary{{Name: "cxt12e4-0", Connected: true}}}
	h := NewHandler(disp, &fakeCaps{})

	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var result []dispatcher.DeviceSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result) != 1 || result[0].Name != "cxt12e4-0" {
		t.Errorf("result = %+v", result)
	}
}

func TestGetCapabilitiesUnknownDevice(t *testing.T) {
	disp := &fakeDispatcher{capsErr: dispatcher.ErrDeviceNotFound}
	h := NewHandler(disp, &fakeCaps{})

	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
```

Run: `go test ./internal/api/...` — fails, `NewCapabilitiesCache` undefined
and `listDevices`/`getCapabilities` routes 404 (unregistered).

- [ ] **Step 2: Implement**

`internal/api/caps.go`:
```go
package api

import (
	"context"
	"sync"

	"github.com/seefood/vialrgb-notify/internal/dispatcher"
)

// CapabilitiesCache implements CapabilitiesSource by calling
// Dispatcher.GetCapabilities on first use per device and caching the
// row,col->index mapping for subsequent PUTs. Phase 1+2 has no cache
// invalidation on device reconnect/hotplug — a later phase's job, not this
// one's; a device's physical matrix doesn't change across a replug anyway.
type CapabilitiesCache struct {
	disp  Dispatcher
	mu    sync.Mutex
	byDev map[string]dispatcher.Capabilities
}

// NewCapabilitiesCache constructs a CapabilitiesCache.
func NewCapabilitiesCache(disp Dispatcher) *CapabilitiesCache {
	return &CapabilitiesCache{disp: disp, byDev: make(map[string]dispatcher.Capabilities)}
}

// IndexFor implements CapabilitiesSource.
func (c *CapabilitiesCache) IndexFor(ctx context.Context, device string, row, col uint8) (uint16, bool, error) {
	caps, err := c.capabilities(ctx, device)
	if err != nil {
		return 0, false, err
	}
	for _, pos := range caps.Positions {
		if pos.Row == row && pos.Col == col {
			return pos.Index, true, nil
		}
	}
	return 0, false, nil
}

func (c *CapabilitiesCache) capabilities(ctx context.Context, device string) (dispatcher.Capabilities, error) {
	c.mu.Lock()
	caps, ok := c.byDev[device]
	c.mu.Unlock()
	if ok {
		return caps, nil
	}

	caps, err := c.disp.GetCapabilities(ctx, device)
	if err != nil {
		return dispatcher.Capabilities{}, err
	}

	c.mu.Lock()
	c.byDev[device] = caps
	c.mu.Unlock()
	return caps, nil
}
```

Append to `internal/api/handlers.go`:
```go
func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	result, err := h.disp.ListDevices(r.Context())
	if err != nil {
		writeError(w, statusForDispatchErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request) {
	device := r.PathValue("name")
	caps, err := h.disp.GetCapabilities(r.Context(), device)
	if err != nil {
		writeError(w, statusForDispatchErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, caps)
}
```

- [ ] **Step 3: Verify**

Run: `go test ./internal/api/...` — all of Task 10/11's tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/api
git commit -m "Add internal/api handlers backed directly by internal/dispatcher"
```

---

## Task 12: `internal/api` — bearer token auth middleware

**Files:**
- Create: `internal/api/auth.go`
- Test: `internal/api/auth_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/api/auth_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRequireTokenNotRequiredPassesThrough(t *testing.T) {
	h := RequireToken("", false, okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestRequireTokenMissing(t *testing.T) {
	h := RequireToken("secret", true, okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRequireTokenWrong(t *testing.T) {
	h := RequireToken("secret", true, okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRequireTokenCorrect(t *testing.T) {
	h := RequireToken("secret", true, okHandler())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/api/... -run TestRequireToken
```

Expected: `FAIL` — `RequireToken` undefined.

- [ ] **Step 3: Implement**

`internal/api/auth.go`:

```go
package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
)

var (
	errMissingToken = errors.New("api: missing bearer token")
	errWrongToken   = errors.New("api: invalid bearer token")
)

// RequireToken wraps next with bearer-token auth. If required is false,
// requests pass through unchecked (the Unix-socket listener case, where
// filesystem permissions already gate access). If required is true — always
// the case for the optional TCP listener, never user-configurable to
// disable — a missing or mismatched token fails closed.
func RequireToken(token string, required bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !required {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := bearerToken(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, errMissingToken)
			return
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeError(w, http.StatusForbidden, errWrongToken)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	return strings.TrimPrefix(h, prefix), true
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/api/... -v -run TestRequireToken
```

Expected: all four `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/api/auth.go internal/api/auth_test.go
git commit -m "Add internal/api bearer token auth middleware"
```

---

## Task 13: `config` — YAML config loading

**Files:**
- Create: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 1: Write the failing tests**

`config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeConfig(t, `
naming:
  prefer: uid
listeners:
  socket:
    path: ~/.local/state/vialrgb-notify/api.sock
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listeners.Socket.Path == "" {
		t.Error("socket path not populated")
	}
	if cfg.Listeners.TCP != nil {
		t.Error("TCP should be nil when absent from YAML")
	}
}

func TestLoadTCPWithoutTokenRejected(t *testing.T) {
	path := writeConfig(t, `
listeners:
  socket:
    path: /tmp/api.sock
  tcp:
    address: 0.0.0.0:49994
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for TCP listener without a token")
	}
}

func TestLoadMissingSocketPathRejected(t *testing.T) {
	path := writeConfig(t, `listeners: {}`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load: want error for missing socket path")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./config/...
```

Expected: `FAIL` — package doesn't exist yet.

- [ ] **Step 3: Implement**

`config/config.go`:

```go
// Package config loads vialrgbd's on-disk configuration
// (~/.config/vialrgb-notify/config.yaml), read once at startup, per the
// design spec's Config section.
package config

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

// Config is vialrgbd's on-disk configuration.
type Config struct {
	Naming    NamingRule `yaml:"naming"`
	Listeners Listeners  `yaml:"listeners"`
}

// NamingRule selects vialrgbd's preferred device-naming strategy; it still
// falls back automatically per device if a device can't answer
// GetKeyboardUID.
type NamingRule struct {
	Prefer string `yaml:"prefer"` // "uid" (default), "path", or "vidpid"
}

// Listeners configures vialrgbd's listeners.
type Listeners struct {
	Socket SocketListener `yaml:"socket"`
	TCP    *TCPListener   `yaml:"tcp,omitempty"` // nil = disabled (off by default)
}

// SocketListener is the always-on, $HOME-owned Unix socket.
type SocketListener struct {
	Path string `yaml:"path"`
}

// TCPListener is only present in the config when explicitly enabled; Load
// rejects one with no Token, per the spec's hard requirement that TCP is
// never allowed without one. There's no in-code default — actually binding
// this listener is deferred past this plan (see "Explicitly deferred past
// this plan" below) — but wherever a concrete example is needed (docs,
// example config.yaml), the chosen default port is :49994.
type TCPListener struct {
	Address string `yaml:"address"`
	Token   string `yaml:"token"`
}

// Load reads and validates a config.yaml.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if cfg.Listeners.Socket.Path == "" {
		return nil, fmt.Errorf("config: listeners.socket.path is required")
	}
	if cfg.Listeners.TCP != nil && cfg.Listeners.TCP.Token == "" {
		return nil, fmt.Errorf("config: listeners.tcp.token is required whenever listeners.tcp is set")
	}
	return &cfg, nil
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./config/... -v
```

Expected: all three `PASS`.

- [ ] **Step 5: Commit**

```bash
git add config
git commit -m "Add config package: YAML loading with TCP-requires-token validation"
```

---

## Task 14: `cmd/vialrgbd` — `main()`

**Files:**
- Create: `cmd/vialrgbd/main.go`
- Create: `cmd/vialrgbd/main_test.go`

This replaces the old `connectord`/`restd` split entirely: one binary
enumerates devices, owns the `internal/dispatcher.Dispatcher`, runs the
hotplug-poll and periodic-redraw loops from Task 9, and serves
`internal/api`'s HTTP handlers over a Unix socket. Most of this is wiring
code tying together real HID hardware (`deviceState.refresh`/`probeUID`
call cgo-backed `go-hid` against actual devices) — not unit-testable
without hardware, and exercised instead by Task 17's manual/gated hardware
check, per the design spec's testing plan. The one piece of pure logic —
whether to log a root-fallback warning — is unit tested here.

Per the design spec's macOS/Linux privilege sections: `vialrgbd` never drops
privilege. There's no spawned child left to drop it for (unlike the old
`connectord`/`restd` split's `credentialForRestd`, which this plan no longer
needs), and the design spec bans `syscall.Setuid`/`Setgid` on an
already-running process outright (`golang/go#1435`), so `vialrgbd` couldn't
safely de-escalate itself even if it wanted to. Running as root is purely
the Linux udev-rule-unavailable fallback; `warnIfRootFallback` just makes
that posture loud, since now the *entire* HTTP surface — not just raw HID
I/O — would be running with it.

- [ ] **Step 1: Write the failing test**

`cmd/vialrgbd/main_test.go`:
```go
package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestWarnIfRootFallback_NonLinux(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("darwin", 0, slog.New(slog.NewTextHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("expected no log output on non-Linux, got %q", buf.String())
	}
}

func TestWarnIfRootFallback_LinuxNonRoot(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("linux", 1000, slog.New(slog.NewTextHandler(&buf, nil)))
	if buf.Len() != 0 {
		t.Errorf("expected no log output for non-root, got %q", buf.String())
	}
}

func TestWarnIfRootFallback_LinuxRoot(t *testing.T) {
	var buf bytes.Buffer
	warnIfRootFallback("linux", 0, slog.New(slog.NewTextHandler(&buf, nil)))
	if !strings.Contains(buf.String(), "running as root") {
		t.Errorf("expected a root-fallback warning, got %q", buf.String())
	}
}
```

Run: `go test ./cmd/vialrgbd/...` — fails, package doesn't exist yet.

- [ ] **Step 2: Implement**

`cmd/vialrgbd/main.go`:
```go
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	goHid "github.com/sstallion/go-hid"

	"github.com/seefood/vialrgb-notify/internal/api"
	"github.com/seefood/vialrgb-notify/internal/dispatcher"
	"github.com/seefood/vialrgb-notify/internal/hid"
)

const (
	dispatcherQueueDepth  = 64
	enumeratePollInterval = 1 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	warnIfRootFallback(runtime.GOOS, os.Geteuid(), logger)

	if err := goHid.Init(); err != nil {
		logger.Error("hid init failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = goHid.Exit() }()

	registry := dispatcher.NewRegistry()
	cache := dispatcher.NewCache()
	disp := dispatcher.New(registry, cache, dispatcherQueueDepth)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

	state := newDeviceState()
	// Initial enumeration, synchronous, so devices are known before HTTP
	// traffic starts.
	registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)

	go pollForDevices(ctx, state, registry, cache, disp, logger)
	go disp.RunPeriodicRedraw(ctx, dispatcher.RedrawInterval, logger)

	caps := api.NewCapabilitiesCache(disp)
	handler := api.NewHandler(disp, caps)

	socketPath := socketPathFromEnv()
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		logger.Error("could not create socket dir", "err", err)
		os.Exit(1)
	}
	_ = os.Remove(socketPath) // clear a stale socket left by a previous crashed run
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		logger.Error("could not listen on unix socket", "err", err)
		os.Exit(1)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		logger.Error("could not chmod socket", "err", err)
		os.Exit(1)
	}

	logger.Info("vialrgbd listening", "socket", socketPath)
	if err := http.Serve(listener, handler.Routes()); err != nil {
		logger.Error("http server exited", "err", err)
		os.Exit(1)
	}
}

// warnIfRootFallback logs a prominent warning when vialrgbd is running as
// root on Linux — the design spec's fallback path for when no udev rule can
// be installed. Unlike the old connectord/restd split, there is no
// unprivileged process left to isolate the HTTP surface behind: root here
// means the whole daemon, including any future TCP listener, runs as root.
// Per the design spec's ban on syscall.Setuid/Setgid on a running process
// (golang/go#1435), vialrgbd cannot safely de-escalate itself even if it
// wanted to — this function only warns, it never attempts to drop privilege.
func warnIfRootFallback(goos string, euid int, logger *slog.Logger) {
	if goos != "linux" || euid != 0 {
		return
	}
	logger.Warn("vialrgbd is running as root — this is the udev-rule-unavailable " +
		"fallback and runs the entire HTTP surface (including any future TCP " +
		"listener) as root too; install a udev rule granting the logged-in user " +
		"access to the device instead, per the design spec's Background section")
}

// openDevice pairs an open hid.Controller with the Identity it was opened
// with, so deviceState can rebuild its dispatcher.PresentDevice set on every
// poll without re-probing (and therefore re-opening) a path it already holds
// open — HID opens are exclusive on macOS.
type openDevice struct {
	identity hid.Identity
	ctrl     hid.Controller
}

// deviceState is cmd/vialrgbd's bookkeeping of currently open HID paths,
// independent of internal/dispatcher.Registry's name-keyed view (a single
// physical device's path is stable across polls; its assigned name is not
// recomputed unless the whole present-device set changes composition).
type deviceState struct {
	openByPath map[string]openDevice
}

func newDeviceState() *deviceState {
	return &deviceState{openByPath: make(map[string]openDevice)}
}

// refresh re-enumerates raw HID paths, opens newly-appeared ones, closes
// controllers for paths that disappeared, and returns every currently open
// device as a dispatcher.PresentDevice, ready for Registry.Reconcile — naming
// itself (including dedup suffixing and reconnect/"untethered" rewire
// matching) is entirely Reconcile's job now (Task 6), not this function's. A
// transient enumerate error leaves the previous open set untouched rather
// than treating every device as disconnected.
func (ds *deviceState) refresh(logger *slog.Logger) []dispatcher.PresentDevice {
	infos, err := hid.Enumerate()
	if err != nil {
		logger.Warn("enumerate failed", "err", err)
		infos = nil
	}

	present := make(map[string]bool, len(infos))
	for _, info := range infos {
		present[info.Path] = true
		if _, alreadyOpen := ds.openByPath[info.Path]; alreadyOpen {
			continue
		}
		uid, hasUID := probeUID(info.Path, logger)
		dev, err := hid.Open(info.Path)
		if err != nil {
			logger.Warn("could not open device", "path", info.Path, "err", err)
			continue
		}
		if err := dev.SetDirectMode(); err != nil {
			logger.Warn("could not set direct mode", "path", info.Path, "err", err)
		}
		ds.openByPath[info.Path] = openDevice{
			identity: hid.Identity{
				Path: info.Path, VendorID: info.VendorID, ProductID: info.ProductID,
				UID: uid, HasUID: hasUID,
			},
			ctrl: dev,
		}
	}
	for path, od := range ds.openByPath {
		if !present[path] {
			_ = od.ctrl.Close()
			delete(ds.openByPath, path)
		}
	}

	devices := make([]openDevice, 0, len(ds.openByPath))
	for _, od := range ds.openByPath {
		devices = append(devices, od)
	}
	// Map iteration order is randomized; sort by Path so Registry.Reconcile's
	// dedup-suffix assignment (-0, -1, ...) for any never-before-seen
	// colliding identity is stable across polls, instead of depending on Go's
	// randomized map iteration order.
	sort.Slice(devices, func(i, j int) bool { return devices[i].identity.Path < devices[j].identity.Path })

	out := make([]dispatcher.PresentDevice, len(devices))
	for i, od := range devices {
		out[i] = dispatcher.PresentDevice{Identity: od.identity, Ctrl: od.ctrl}
	}
	return out
}

// probeUID opens its own short-lived handle to query the Vial keyboard UID
// and closes it before the caller opens the real long-lived handle on the
// same path — sequential, not concurrent, so it doesn't violate macOS's
// exclusive-HID-open semantics.
func probeUID(path string, logger *slog.Logger) (uid [8]byte, hasUID bool) {
	dev, err := hid.Open(path)
	if err != nil {
		logger.Warn("could not open candidate device", "path", path, "err", err)
		return uid, false
	}
	defer func() { _ = dev.Close() }()
	uid, err = dev.GetKeyboardUID()
	if err != nil {
		return uid, false
	}
	return uid, true
}

// pollForDevices re-enumerates every enumeratePollInterval, reconciles
// registry (rewiring reconnected devices, evicting stale-untethered ones per
// Task 6), immediately redraws any device Reconcile reports as Reconnected —
// the design spec's reconnect-triggered redraw, independent of (and faster
// than) RunPeriodicRedraw's unconditional 5s sweep started separately in
// main() — and forgets cache's entry for any device Reconcile reports as
// Evicted, so a later device presenting that identity starts fresh.
func pollForDevices(ctx context.Context, state *deviceState, registry *dispatcher.Registry, cache *dispatcher.Cache, disp *dispatcher.Dispatcher, logger *slog.Logger) {
	ticker := time.NewTicker(enumeratePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			res := registry.Reconcile(state.refresh(logger), time.Now(), dispatcher.UntetheredMaxAge)
			disp.RedrawReconnected(ctx, res.Reconnected, logger)
			for _, name := range res.Evicted {
				cache.Forget(name)
			}
		case <-ctx.Done():
			return
		}
	}
}

func socketPathFromEnv() string {
	if p := os.Getenv("VIALRGB_NOTIFY_SOCKET"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".local", "state", "vialrgb-notify", "api.sock")
}
```

- [ ] **Step 3: Verify**

Run: `go build ./...` (verifies the whole tree compiles) and `go test
./cmd/vialrgbd/...` (the three `warnIfRootFallback` cases pass).

- [ ] **Step 4: Commit**

```bash
git add cmd/vialrgbd
git commit -m "Add cmd/vialrgbd main: enumeration, hotplug/redraw loops, HTTP server"
```

---

## Task 15: Build tooling and doc updates

**Files:**
- Create: `Makefile`
- Modify: `CLAUDE.md`
- Modify: `README.md`

- [ ] **Step 1: Add a Makefile**

`Makefile`:
```makefile
.PHONY: build test lint

build:
	go build -o bin/vialrgbd ./cmd/vialrgbd

test:
	go test ./...

lint:
	prek run --all-files
```

- [ ] **Step 2: Update `CLAUDE.md`'s "Commands" section**

Replace the existing "Commands (current Python scaffold only)" section's
content with build/test commands for the real implementation, keeping the
Python smoke-test commands too (still valid as a protocol reference):

```markdown
## Commands

```
make build                        # builds bin/vialrgbd
make test                         # go test ./...
make lint                         # prek run --all-files
```

Requires Go 1.27+, a C compiler (cgo — `github.com/sstallion/go-hid` bundles
its own hidapi C sources), and on Linux, the `libudev-dev` headers (hidraw
backend, the default). See the plan's "Verified ground truth" section
(`docs/superpowers/plans/2026-09-23-vialrgb-notify-phase1-2-implementation.md`)
for exactly what was checked before relying on it.

Python scaffold commands (still valid — `set_key_color.py` remains the
protocol reference, not superseded):

```
uv sync --frozen                  # install hidapi dependency
uv run python set_key_color.py    # protocol smoke test against real hardware
```
```

- [ ] **Step 3: Update the project state/architecture description in `CLAUDE.md`**

Also update `CLAUDE.md`'s prose describing the `connectord`/`restd` two-process
architecture to describe the single `vialrgbd` binary instead, matching the
revised design spec (this plan's own header section has the up-to-date
wording to reuse). In particular, the bullet points about the anonymous
`socketpair`/`exec.Cmd.ExtraFiles` internal link, the "traffic cop"
dispatcher description, and the privilege-drop-at-spawn-time rule no longer
apply as written — replace them with: single dispatcher goroutine owns the
open HID handles directly (no internal link at all), and
`warnIfRootFallback` (Task 14) replaces the privilege-drop mechanism, since
there is no spawned child left to drop privilege for.

- [ ] **Step 4: Commit**

```bash
git add Makefile CLAUDE.md README.md
git commit -m "Add Makefile; update CLAUDE.md for the single-binary vialrgbd architecture"
```

---

## Task 16: Full integration pass

**Files:** none (verification only)

- [ ] **Step 1: Build everything**

```bash
go build ./...
```

Expected: exit 0.

- [ ] **Step 2: Run the full test suite**

```bash
go test ./... -v
```

Expected: all packages `PASS`.

- [ ] **Step 3: Run `go vet`**

```bash
go vet ./...
```

Expected: no output, exit 0.

- [ ] **Step 4: Run the full pre-commit/prek suite**

```bash
prek run --all-files
```

Expected: all hooks pass, including `golangci-lint`, `go-sec-mod`,
`go-mod-tidy`, `go-build-mod`, `go-fmt`. Fix any findings — this step is not
optional; per this repo's own `CLAUDE.md` rule, a task isn't complete until
its commit passes hooks cleanly. If `golangci-lint` or `gosec` flag
something not addressed by earlier tasks (e.g. an intentional unchecked
error), fix it in place rather than suppressing it, unless the finding is a
false positive — in which case use a narrowly-scoped `//nolint` with a
comment explaining why, not a blanket suppression.

- [ ] **Step 5: Commit any fixes from Step 4**

```bash
git add -A
git commit -m "Fix lint findings from full prek run"
```

(Skip this step if Step 4 was already clean.)

---

## Task 17: Manual/gated hardware round-trip check

**Files:**
- Create: `docs/superpowers/manual-checks/phase1-2-hardware-roundtrip.md`

Per the design spec's Testing section: "Manual/gated: real-hardware
round-trip check, documented but not automated (CI has no physical keyboard
attached)." This task documents that check; it is not run as part of this
plan's automated steps, since it requires the physical `cxt_studio/12e4`
board attached with the `personal/vialrgb-direct/001-enable` firmware
flashed.

- [ ] **Step 1: Write the checklist**

`docs/superpowers/manual-checks/phase1-2-hardware-roundtrip.md`:
```markdown
# Phase 1+2 hardware round-trip check

Manual, gated on physical hardware — not run in CI. Run this after any
change touching `internal/hid`, `internal/dispatcher`, or `cmd/vialrgbd`,
before considering that change verified end-to-end.

Prerequisites: `cxt_studio/12e4` attached, `personal/vialrgb-direct/001-enable`
firmware flashed (see `../../../README.md` and the parent `CXT-studio`
tree's `../qmk_vial`).

1. `make build`
2. Run `./bin/vialrgbd` in a terminal you can watch logs in. Confirm it logs
   enumerating the board.
3. In a second terminal, list devices:
   ```bash
   curl --unix-socket ~/.local/state/vialrgb-notify/api.sock http://localhost/devices
   ```
   Confirm the response includes one entry whose name matches the board
   (a `uid-...` name if `VIAL_KEYBOARD_UID` is compiled in, else a
   `5754-c401-...` fallback name).
4. Fetch capabilities for that device name:
   ```bash
   curl --unix-socket ~/.local/state/vialrgb-notify/api.sock http://localhost/devices/<name>
   ```
   Confirm `led_count` and `positions` look plausible for the board's known
   matrix.
5. Set key `2,2` to red and confirm it visibly lights up red on the board:
   ```bash
   curl --unix-socket ~/.local/state/vialrgb-notify/api.sock \
     -X PUT -d '{"color":"#ff0000"}' \
     http://localhost/devices/<name>/keys/2,2
   ```
6. Repeat step 5 with a named color (`"color":"green"`) and an HSV triple
   (`"color":"0,255,128"`), confirming visibly distinct results each time.
7. With both keys from steps 5–6 still colored, physically unplug and
   replug the board. Confirm both colors reappear automatically within
   ~1-2 seconds, with no new API call — the reconnect-triggered redraw
   (`internal/dispatcher`'s `RedrawReconnected`, driven by `cmd/vialrgbd`'s
   1s hotplug-poll loop). Also confirm `GET /devices` shows the *same* name
   as step 3 (not a new `-0`-suffixed name) — this is the untethered-rewire
   path (Task 6), not a fresh allocation.
8. Set one key to a new color, then (without unplugging) wait at least 5
   seconds and confirm nothing visibly changes — the unconditional
   periodic redraw (`RunPeriodicRedraw`, every `dispatcher.RedrawInterval`)
   re-asserting the same color is expected to be a no-op to the eye, not a
   flicker or a reversion.
```

The 24h untethered-eviction behavior (Task 6) is not practically checkable
manually on this timescale — it's covered by
`TestRegistryReconcileEvictsStaleUntethered` instead. Not this checklist's
job to re-verify.

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/manual-checks/phase1-2-hardware-roundtrip.md
git commit -m "Document the manual/gated Phase 1+2 hardware round-trip check"
```

---

## Explicitly deferred past this plan

Matches the design spec's own "Explicitly out of scope for Phase 1+2"
section, plus items this plan itself deferred (flagged inline above, not
silently dropped):

- The optional TCP listener and its mandatory bearer token (Task 14's
  `main()` only binds the Unix socket listener) — when it is wired up, its
  documented default port is `:49994` (Task 13's note on `TCPListener`).
- Wiring `config.Load` into `cmd/vialrgbd`'s `main()` (Task 14's note) — it
  currently uses hardcoded/env-var defaults instead.
- Packaging/installation mechanics for the privilege model described in the
  design spec's Background section: the macOS LaunchAgent plist and the
  Linux udev rule file. This plan implements `vialrgbd` as a plain binary
  invoked directly (per Task 17's manual check) — it does not create or
  install a `launchd`/`systemd`/udev unit. Doing so is a packaging task, not
  a coding gap in Phase 1+2's architecture itself.
- Capabilities-cache invalidation on device reconnect (Task 11's note) — a
  device's physical matrix doesn't change across a replug, so this isn't a
  correctness gap, just an unexploited simplification.
- Effects/animation (Phase 3), YAML status templates (Phase 4), per-client
  key allocation (Phase 5), server-owned timers (Phase 6), Windows support,
  TLS on the TCP listener.
