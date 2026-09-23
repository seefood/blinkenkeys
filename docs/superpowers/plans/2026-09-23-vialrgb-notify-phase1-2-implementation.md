# vialrgb-notify Phase 1+2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the Go `connectord`/`restd` daemon pair described in
`docs/superpowers/specs/2026-09-21-vialrgb-notify-phase1-2-design.md`: set a
single key's color on one HID device (Phase 1), and enumerate/report
capabilities of all connected Vial-capable devices with stable names (Phase 2).

**Architecture:** Two long-lived processes. `connectord` (privileged, does
only raw HID I/O via `github.com/sstallion/go-hid`) enumerates Vial-capable
devices, spawns `restd` as a child process, and hands it one end of an
anonymous `socketpair` via `exec.Cmd.ExtraFiles`. `restd` (unprivileged) runs
an HTTP server and a single dispatcher goroutine that owns the socketpair
exclusively, serializing and batching all device operations. See the design
spec for full rationale — this plan implements it as specified without
reopening any of its decisions.

**Tech Stack:** Go 1.27, `github.com/sstallion/go-hid` (raw HID via bundled
hidapi C sources, cgo), `golang.org/x/image/colornames` (CSS/X11 color
names), `gopkg.in/yaml.v3` (config), Go's stdlib `net/http` with the
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
- **`syscall.Socketpair(domain, typ, proto int) (fd [2]int, err error)`** and
  **`exec.Cmd.ExtraFiles []*os.File`** (doc: "entry i becomes file descriptor
  3+i") are both real, current Go stdlib APIs, confirmed against
  `golang.org/x/go`'s source.
- **Current stable Go is 1.27.1** (confirmed via both `brew info go` and
  `https://go.dev/VERSION?m=text`).
- Module path `github.com/seefood/vialrgb-notify` — confirmed with the user
  (matches their `gh` CLI identity; this repo isn't pushed to GitHub yet).

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
go get gopkg.in/yaml.v3@v3.0.1
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

func TestAssignNamesUnique(t *testing.T) {
	devices := []Identity{
		{HasUID: true, UID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}},
		{VendorID: 0x5754, ProductID: 0xC401, Path: "/dev/hidraw3"},
	}
	got := AssignNames(devices)
	if got[0].Name != "uid-0102030405060708" {
		t.Errorf("got[0].Name = %q", got[0].Name)
	}
	if got[1].Name != "5754-c401-/dev/hidraw3" {
		t.Errorf("got[1].Name = %q", got[1].Name)
	}
}

func TestAssignNamesDedup(t *testing.T) {
	devices := []Identity{
		{VendorID: 0x5754, ProductID: 0xC401},
		{VendorID: 0x5754, ProductID: 0xC401},
		{VendorID: 0x5754, ProductID: 0xC401},
	}
	got := AssignNames(devices)
	want := []string{"5754-c401-0", "5754-c401-1", "5754-c401-2"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("got[%d].Name = %q, want %q", i, got[i].Name, w)
		}
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/hid/... -run TestFilterVial -run TestAssignNames
```

Expected: `FAIL` — `Info`, `filterVial`, `Identity`, `AssignNames` undefined.

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

// Controller is the subset of Device's behavior cmd/connectord's RPC layer
// depends on, so tests can substitute a fake without opening real hardware.
type Controller interface {
	SetKeys(keys []KeyColor) error
	GetNumberLEDs() (uint16, error)
	GetLEDInfo(index uint16) (row, col uint8, err error)
	Close() error
}

// Identity is a resolved identity for one attached device, gathered by
// connectord at startup (enumeration path plus a GetKeyboardUID probe).
type Identity struct {
	Path      string
	VendorID  uint16
	ProductID uint16
	UID       [8]byte
	HasUID    bool
}

// NamedIdentity pairs a resolved Identity with connectord's assigned stable
// name.
type NamedIdentity struct {
	Name string
	Identity
}

// AssignNames computes each device's stable name — Vial UID if available,
// else VID/PID + platform path, else VID/PID alone — then deduplicates
// only actually-colliding names (e.g. two identical boards) with -0, -1, ...
// suffixes, in input (enumeration) order.
func AssignNames(devices []Identity) []NamedIdentity {
	base := make([]string, len(devices))
	total := make(map[string]int)
	for i, d := range devices {
		base[i] = baseName(d)
		total[base[i]]++
	}

	next := make(map[string]int)
	out := make([]NamedIdentity, len(devices))
	for i, d := range devices {
		name := base[i]
		if total[name] > 1 {
			name = fmt.Sprintf("%s-%d", name, next[base[i]])
			next[base[i]]++
		}
		out[i] = NamedIdentity{Name: name, Identity: d}
	}
	return out
}

func baseName(d Identity) string {
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
by Task 19's manual/gated hardware check, per the design spec's testing plan.

- [ ] **Step 5: Commit**

```bash
git add internal/hid
git commit -m "Add internal/hid enumeration, Open, Controller, and stable-name resolution"
```

---

## Task 6: `internal/ipc` — length-prefixed JSON framing

**Files:**
- Create: `internal/ipc/frame.go`
- Test: `internal/ipc/frame_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/ipc/frame_test.go`:

```go
package ipc

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	type payload struct {
		Op string `json:"op"`
	}
	var buf bytes.Buffer
	want := payload{Op: "SetKey"}
	if err := WriteFrame(&buf, want); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var got payload
	if err := ReadFrame(&buf, &got); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReadFrameOversized(t *testing.T) {
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], maxFrameSize+1)
	buf.Write(lenBuf[:])

	var v any
	if err := ReadFrame(&buf, &v); err == nil {
		t.Fatal("ReadFrame: want error for oversized frame")
	}
}

func TestReadFrameShort(t *testing.T) {
	buf := bytes.NewBufferString("ab") // shorter than the 4-byte length prefix
	var v any
	if err := ReadFrame(buf, &v); err == nil {
		t.Fatal("ReadFrame: want error for truncated length prefix")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/ipc/...
```

Expected: `FAIL` — `WriteFrame`, `ReadFrame`, `maxFrameSize` undefined.

- [ ] **Step 3: Implement**

`internal/ipc/frame.go`:

```go
// Package ipc implements the connectord<->restd internal link: a
// length-prefixed JSON framing over the anonymous socketpair connectord
// creates at startup, plus the Request/Response types both binaries share.
package ipc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// maxFrameSize is a generous upper bound guarding against a corrupt length
// prefix causing an unbounded allocation.
const maxFrameSize = 1 << 20 // 1 MiB

// WriteFrame writes v as a length-prefixed JSON message: a 4-byte
// big-endian length prefix followed by that many bytes of JSON.
func WriteFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("ipc: marshal: %w", err)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("ipc: write length: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("ipc: write body: %w", err)
	}
	return nil
}

// ReadFrame reads one length-prefixed JSON message written by WriteFrame
// into v.
func ReadFrame(r io.Reader, v any) error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return fmt.Errorf("ipc: read length: %w", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > maxFrameSize {
		return fmt.Errorf("ipc: frame of %d bytes exceeds %d-byte limit", n, maxFrameSize)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return fmt.Errorf("ipc: read body: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("ipc: unmarshal: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/ipc/... -v
```

Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc
git commit -m "Add internal/ipc: length-prefixed JSON framing"
```

---

## Task 7: `internal/ipc` — Request/Response types and `NetConn`

**Files:**
- Create: `internal/ipc/types.go`
- Create: `internal/ipc/conn.go`
- Test: `internal/ipc/types_test.go`

- [ ] **Step 1: Write the failing test**

`internal/ipc/types_test.go`:

```go
package ipc

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRequestResponseRoundTrip(t *testing.T) {
	args := SetKeyArgs{Device: "cxt12e4-0", Index: 5, H: 0, S: 255, V: 255}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	req := Request{Op: OpSetKey, Args: argsJSON}

	var buf bytes.Buffer
	if err := WriteFrame(&buf, req); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	var gotReq Request
	if err := ReadFrame(&buf, &gotReq); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if gotReq.Op != OpSetKey {
		t.Errorf("Op = %q, want %q", gotReq.Op, OpSetKey)
	}
	var gotArgs SetKeyArgs
	if err := json.Unmarshal(gotReq.Args, &gotArgs); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if gotArgs != args {
		t.Errorf("args = %+v, want %+v", gotArgs, args)
	}
}
```

- [ ] **Step 2: Run test, verify it fails**

```bash
go test ./internal/ipc/... -run TestRequestResponseRoundTrip
```

Expected: `FAIL` — `SetKeyArgs`, `Request`, `OpSetKey` undefined.

- [ ] **Step 3: Implement**

`internal/ipc/types.go`:

```go
package ipc

import "encoding/json"

// Op identifies which connectord RPC a Request invokes.
type Op string

const (
	OpSetKey          Op = "SetKey"
	OpSetKeys         Op = "SetKeys"
	OpListDevices     Op = "ListDevices"
	OpGetCapabilities Op = "GetCapabilities"
)

// ErrorCode classifies a failed Response, so restd's HTTP layer can map it
// to the right status code per the design spec's error table.
type ErrorCode string

const (
	ErrBadRequest  ErrorCode = "bad_request"
	ErrNotFound    ErrorCode = "not_found"
	ErrUnavailable ErrorCode = "unavailable"
)

// Request is one call across the connectord<->restd link. Args holds the
// op-specific argument struct (SetKeyArgs, SetKeysArgs, ...), deferred with
// json.RawMessage so the two sides don't need a shared interface type.
type Request struct {
	Op   Op              `json:"op"`
	Args json.RawMessage `json:"args"`
}

// Response is connectord's reply. Data holds the op-specific result struct,
// present only when OK is true.
type Response struct {
	OK    bool            `json:"ok"`
	Code  ErrorCode       `json:"code,omitempty"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// SetKeyArgs sets one LED. Index is a resolved LED index (restd's api layer
// resolves the client-facing row,col into this via a cached
// GetCapabilities result) — connectord's RPC surface deals in LED index
// only, matching what VialRGB's wire protocol actually addresses.
type SetKeyArgs struct {
	Device string `json:"device"`
	Index  uint16 `json:"index"`
	H      uint8  `json:"h"`
	S      uint8  `json:"s"`
	V      uint8  `json:"v"`
}

// SetKeysArgs batches multiple same-device SetKey calls (internal/dispatcher
// coalesces contiguous-index requests into this before sending).
type SetKeysArgs struct {
	Device string     `json:"device"`
	Keys   []KeyColor `json:"keys"`
}

// KeyColor is one LED's target color within a SetKeysArgs batch.
type KeyColor struct {
	Index uint16 `json:"index"`
	H     uint8  `json:"h"`
	S     uint8  `json:"s"`
	V     uint8  `json:"v"`
}

// DeviceSummary is one entry in a ListDevices reply.
type DeviceSummary struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// ListDevicesResult is ListDevices's reply payload.
type ListDevicesResult struct {
	Devices []DeviceSummary `json:"devices"`
}

// GetCapabilitiesArgs names the device to query.
type GetCapabilitiesArgs struct {
	Device string `json:"device"`
}

// LEDPosition is one LED's matrix location, as reported by VialRGB's
// VIALRGB_GET_LED_INFO.
type LEDPosition struct {
	Index uint16 `json:"index"`
	Row   uint8  `json:"row"`
	Col   uint8  `json:"col"`
}

// Capabilities is GetCapabilities's reply payload.
type Capabilities struct {
	LEDCount  int           `json:"led_count"`
	Positions []LEDPosition `json:"positions"`
}
```

`internal/ipc/conn.go`:

```go
package ipc

import "net"

// Conn is anything that can send/receive length-prefixed JSON frames — the
// socketpair connection wrapped by both connectord and restd.
type Conn interface {
	WriteFrame(v any) error
	ReadFrame(v any) error
}

// NetConn adapts a net.Conn (the inherited socketpair fd, wrapped via
// net.FileConn) into Conn. Embedding net.Conn also gives it SetDeadline,
// which internal/dispatcher's Conn interface additionally requires.
type NetConn struct {
	net.Conn
}

func (c NetConn) WriteFrame(v any) error { return WriteFrame(c.Conn, v) }
func (c NetConn) ReadFrame(v any) error  { return ReadFrame(c.Conn, v) }
```

- [ ] **Step 4: Run test, verify it passes**

```bash
go test ./internal/ipc/... -v
```

Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/ipc
git commit -m "Add internal/ipc Request/Response types and NetConn adapter"
```

---

## Task 8: `internal/dispatcher` — single-writer dispatcher with backpressure

**Files:**
- Create: `internal/dispatcher/dispatcher.go`
- Test: `internal/dispatcher/dispatcher_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/dispatcher/dispatcher_test.go`:

```go
package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

type fakeConn struct {
	mu       sync.Mutex
	written  []ipc.Request
	response ipc.Response
}

func (f *fakeConn) WriteFrame(v any) error {
	req, ok := v.(ipc.Request)
	if !ok {
		return fmt.Errorf("fakeConn: WriteFrame got %T, want ipc.Request", v)
	}
	f.mu.Lock()
	f.written = append(f.written, req)
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) ReadFrame(v any) error {
	resp, ok := v.(*ipc.Response)
	if !ok {
		return fmt.Errorf("fakeConn: ReadFrame got %T, want *ipc.Response", v)
	}
	*resp = f.response
	return nil
}

func (f *fakeConn) SetDeadline(time.Time) error { return nil }

func TestDoQueueFull(t *testing.T) {
	d := New(&fakeConn{}, 1, time.Second)
	d.queue <- pending{req: ipc.Request{}, reply: make(chan ipc.Response, 1)}

	_, err := d.Do(context.Background(), ipc.Request{Op: ipc.OpListDevices})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("Do() error = %v, want ErrQueueFull", err)
	}
}

func TestRunSingleRequest(t *testing.T) {
	conn := &fakeConn{response: ipc.Response{OK: true}}
	d := New(conn, 8, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Run(ctx)

	resp, err := d.Do(context.Background(), ipc.Request{Op: ipc.OpListDevices})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !resp.OK {
		t.Error("resp.OK = false, want true")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/dispatcher/...
```

Expected: `FAIL` — package doesn't exist yet.

- [ ] **Step 3: Implement**

`internal/dispatcher/dispatcher.go`:

```go
// Package dispatcher is restd's single-writer "traffic cop" for the
// connectord link: every other goroutine (HTTP handlers now; per-key
// animation timers in later phases) submits a request through Do and never
// touches the connection directly.
package dispatcher

import (
	"context"
	"errors"
	"time"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

// ErrQueueFull is returned by Do when the dispatcher's request queue is at
// capacity — callers (internal/api's HTTP handlers) map this to a 503.
var ErrQueueFull = errors.New("dispatcher: request queue full")

// Conn is the connectord link this Dispatcher owns exclusively.
type Conn interface {
	ipc.Conn
	SetDeadline(t time.Time) error
}

type pending struct {
	req   ipc.Request
	reply chan ipc.Response
}

// Dispatcher serializes all access to a Conn, per the design spec's
// concurrency section: "single dispatcher goroutine owns the socketpair
// connection exclusively."
type Dispatcher struct {
	conn        Conn
	queue       chan pending
	linkTimeout time.Duration
}

// New creates a Dispatcher. queueDepth bounds in-flight requests (spec:
// e.g. 64); linkTimeout bounds each read/write on conn (spec: e.g. 1s), so a
// wedged connectord can't hang the dispatcher forever.
func New(conn Conn, queueDepth int, linkTimeout time.Duration) *Dispatcher {
	return &Dispatcher{
		conn:        conn,
		queue:       make(chan pending, queueDepth),
		linkTimeout: linkTimeout,
	}
}

// Do submits req and blocks for its reply, or returns ctx.Err() or
// ErrQueueFull immediately if the queue is full.
func (d *Dispatcher) Do(ctx context.Context, req ipc.Request) (ipc.Response, error) {
	reply := make(chan ipc.Response, 1)
	select {
	case d.queue <- pending{req: req, reply: reply}:
	default:
		return ipc.Response{}, ErrQueueFull
	}
	select {
	case resp := <-reply:
		return resp, nil
	case <-ctx.Done():
		return ipc.Response{}, ctx.Err()
	}
}

// Run is the single dispatcher goroutine: it owns conn exclusively. On each
// cycle it takes one request, then non-blockingly drains any others already
// queued and batches same-device contiguous SetKeys before sending (see
// batch.go). It runs until ctx is canceled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		var first pending
		select {
		case first = <-d.queue:
		case <-ctx.Done():
			return
		}
		batch := []pending{first}
	drain:
		for {
			select {
			case p := <-d.queue:
				batch = append(batch, p)
			default:
				break drain
			}
		}
		d.dispatchBatch(batch)
	}
}

func (d *Dispatcher) dispatchBatch(batch []pending) {
	for _, group := range groupForSend(batch) {
		if err := d.conn.SetDeadline(time.Now().Add(d.linkTimeout)); err != nil {
			failAll(group, err)
			continue
		}
		req, err := mergeRequest(group)
		if err != nil {
			failAll(group, err)
			continue
		}
		if err := d.conn.WriteFrame(req); err != nil {
			failAll(group, err)
			continue
		}
		var resp ipc.Response
		if err := d.conn.ReadFrame(&resp); err != nil {
			failAll(group, err)
			continue
		}
		for _, p := range group {
			p.reply <- resp
		}
	}
}

func failAll(group []pending, err error) {
	resp := ipc.Response{OK: false, Code: ipc.ErrUnavailable, Error: err.Error()}
	for _, p := range group {
		p.reply <- resp
	}
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/dispatcher/... -v
```

Expected: both `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/dispatcher
git commit -m "Add internal/dispatcher: single-writer Do/Run with backpressure"
```

---

## Task 9: `internal/dispatcher` — contiguous-index batching

**Files:**
- Create: `internal/dispatcher/batch.go`
- Test: `internal/dispatcher/batch_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/dispatcher/batch_test.go`:

```go
package dispatcher

import (
	"encoding/json"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

func mustSetKeyPending(t *testing.T, device string, index uint16) pending {
	t.Helper()
	args, err := json.Marshal(ipc.SetKeyArgs{Device: device, Index: index})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return pending{req: ipc.Request{Op: ipc.OpSetKey, Args: args}, reply: make(chan ipc.Response, 1)}
}

func TestGroupForSendBatchesContiguous(t *testing.T) {
	batch := []pending{
		mustSetKeyPending(t, "a", 0),
		mustSetKeyPending(t, "a", 1),
		mustSetKeyPending(t, "a", 2), // contiguous, same device -> one group
		mustSetKeyPending(t, "b", 5), // different device -> its own group
		mustSetKeyPending(t, "a", 10), // non-contiguous -> its own group
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
	batch := make([]pending, 12)
	for i := range batch {
		batch[i] = mustSetKeyPending(t, "a", uint16(i))
	}
	groups := groupForSend(batch)
	if len(groups) != 2 || len(groups[0]) != 9 || len(groups[1]) != 3 {
		t.Fatalf("got group sizes %v; want [9 3]", groupSizes(groups))
	}
}

func TestGroupForSendNonSetKeyAlwaysSingleton(t *testing.T) {
	batch := []pending{
		{req: ipc.Request{Op: ipc.OpListDevices}, reply: make(chan ipc.Response, 1)},
		{req: ipc.Request{Op: ipc.OpListDevices}, reply: make(chan ipc.Response, 1)},
	}
	groups := groupForSend(batch)
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2 (non-SetKey ops never merge)", len(groups))
	}
}

func groupSizes(groups [][]pending) []int {
	sizes := make([]int, len(groups))
	for i, g := range groups {
		sizes[i] = len(g)
	}
	return sizes
}

func TestMergeRequestSingleton(t *testing.T) {
	p := mustSetKeyPending(t, "a", 3)
	req, err := mergeRequest([]pending{p})
	if err != nil {
		t.Fatalf("mergeRequest: %v", err)
	}
	if req.Op != ipc.OpSetKey {
		t.Errorf("Op = %q, want %q (singleton groups forward unchanged)", req.Op, ipc.OpSetKey)
	}
}

func TestMergeRequestBatch(t *testing.T) {
	group := []pending{
		mustSetKeyPending(t, "a", 0),
		mustSetKeyPending(t, "a", 1),
	}
	req, err := mergeRequest(group)
	if err != nil {
		t.Fatalf("mergeRequest: %v", err)
	}
	if req.Op != ipc.OpSetKeys {
		t.Fatalf("Op = %q, want %q", req.Op, ipc.OpSetKeys)
	}
	var args ipc.SetKeysArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if args.Device != "a" || len(args.Keys) != 2 {
		t.Errorf("args = %+v", args)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/dispatcher/... -run "GroupForSend|MergeRequest"
```

Expected: `FAIL` — `groupForSend`, `mergeRequest` undefined.

- [ ] **Step 3: Implement**

`internal/dispatcher/batch.go`:

```go
package dispatcher

import (
	"encoding/json"
	"fmt"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

// maxBatch is VialRGB's own per-packet ceiling (see internal/hid's
// maxKeysPerReport) — batches larger than this become multiple groups.
const maxBatch = 9

// groupForSend partitions a drained batch of pending requests into groups
// that become one wire request each: non-SetKey ops are always singleton
// groups; SetKey ops are grouped per the design spec — same device,
// contiguous Index, up to maxBatch — preserving arrival order.
func groupForSend(batch []pending) [][]pending {
	var out [][]pending
	var run []pending
	var runDevice string
	var runNextIndex uint16

	flush := func() {
		if len(run) > 0 {
			out = append(out, run)
			run = nil
		}
	}

	for _, p := range batch {
		device, index, isSetKey := setKeyKey(p.req)
		if !isSetKey {
			flush()
			out = append(out, []pending{p})
			continue
		}
		if len(run) > 0 && device == runDevice && index == runNextIndex && len(run) < maxBatch {
			run = append(run, p)
			runNextIndex++
			continue
		}
		flush()
		run = []pending{p}
		runDevice = device
		runNextIndex = index + 1
	}
	flush()
	return out
}

// setKeyKey reports whether req is a SetKey request, and if so its device
// and LED index.
func setKeyKey(req ipc.Request) (device string, index uint16, ok bool) {
	if req.Op != ipc.OpSetKey {
		return "", 0, false
	}
	var args ipc.SetKeyArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return "", 0, false
	}
	return args.Device, args.Index, true
}

// mergeRequest converts one group into the wire request to send: a
// singleton group forwards its original request unchanged; a larger
// SetKey group coalesces into one SetKeys.
func mergeRequest(group []pending) (ipc.Request, error) {
	if len(group) == 1 {
		return group[0].req, nil
	}
	var device string
	keys := make([]ipc.KeyColor, 0, len(group))
	for i, p := range group {
		var args ipc.SetKeyArgs
		if err := json.Unmarshal(p.req.Args, &args); err != nil {
			return ipc.Request{}, fmt.Errorf("dispatcher: unmarshal batched SetKeyArgs: %w", err)
		}
		if i == 0 {
			device = args.Device
		}
		keys = append(keys, ipc.KeyColor{Index: args.Index, H: args.H, S: args.S, V: args.V})
	}
	argsJSON, err := json.Marshal(ipc.SetKeysArgs{Device: device, Keys: keys})
	if err != nil {
		return ipc.Request{}, fmt.Errorf("dispatcher: marshal SetKeysArgs: %w", err)
	}
	return ipc.Request{Op: ipc.OpSetKeys, Args: argsJSON}, nil
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./internal/dispatcher/... -v
```

Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/dispatcher
git commit -m "Add internal/dispatcher batching: coalesce contiguous SetKey into SetKeys"
```

---

## Task 10: `internal/api` — `PUT /devices/{name}/keys/{row},{col}`

**Files:**
- Create: `internal/api/handlers.go`
- Test: `internal/api/handlers_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/api/handlers_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

type fakeClient struct {
	do func(ctx context.Context, req ipc.Request) (ipc.Response, error)
}

func (f *fakeClient) Do(ctx context.Context, req ipc.Request) (ipc.Response, error) {
	return f.do(ctx, req)
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
	var gotReq ipc.Request
	client := &fakeClient{do: func(_ context.Context, req ipc.Request) (ipc.Response, error) {
		gotReq = req
		return ipc.Response{OK: true}, nil
	}}
	h := NewHandler(client, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	var args ipc.SetKeyArgs
	if err := json.Unmarshal(gotReq.Args, &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args.Device != "cxt12e4-0" || args.Index != 5 || args.H != 0 || args.S != 255 || args.V != 255 {
		t.Errorf("args = %+v", args)
	}
}

func TestSetKeyBadColor(t *testing.T) {
	client := &fakeClient{do: func(context.Context, ipc.Request) (ipc.Response, error) {
		t.Fatal("dispatcher should not be called for a malformed color")
		return ipc.Response{}, nil
	}}
	h := NewHandler(client, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"not-a-color"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSetKeyUnknownKeyPosition(t *testing.T) {
	client := &fakeClient{do: func(context.Context, ipc.Request) (ipc.Response, error) {
		t.Fatal("dispatcher should not be called when the key position isn't found")
		return ipc.Response{}, nil
	}}
	h := NewHandler(client, &fakeCaps{found: false})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/99,99", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSetKeyUnknownDevice(t *testing.T) {
	h := NewHandler(&fakeClient{}, &fakeCaps{err: errDeviceNotFound})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/nope/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSetKeyDispatcherUnavailable(t *testing.T) {
	client := &fakeClient{do: func(context.Context, ipc.Request) (ipc.Response, error) {
		return ipc.Response{}, errors.New("boom")
	}}
	h := NewHandler(client, &fakeCaps{index: 5, found: true})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/devices/cxt12e4-0/keys/2,2", strings.NewReader(`{"color":"#ff0000"}`))
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/api/...
```

Expected: `FAIL` — package doesn't exist yet.

- [ ] **Step 3: Implement**

`internal/api/handlers.go`:

```go
// Package api implements restd's HTTP surface: the PUT/GET routes from the
// design spec, the color parser's integration point, and bearer-token auth.
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
	"github.com/seefood/vialrgb-notify/internal/ipc"
)

// DispatchClient is the subset of *dispatcher.Dispatcher the HTTP handlers
// need, so tests can substitute a fake.
type DispatchClient interface {
	Do(ctx context.Context, req ipc.Request) (ipc.Response, error)
}

// CapabilitiesSource resolves row,col to a LED index for a named device.
// Returns (0, false, errDeviceNotFound)-shaped errors for an unknown
// device, (_, false, nil) for a known device with no key at that position,
// and a non-nil err for a genuine connectord round-trip failure.
type CapabilitiesSource interface {
	IndexFor(ctx context.Context, device string, row, col uint8) (uint16, bool, error)
}

// Handler holds restd's dependencies and builds its route table.
type Handler struct {
	client DispatchClient
	caps   CapabilitiesSource
}

// NewHandler constructs a Handler.
func NewHandler(client DispatchClient, caps CapabilitiesSource) *Handler {
	return &Handler{client: client, caps: caps}
}

// Routes builds restd's route table (Go 1.22+ ServeMux method+wildcard
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
	if errors.Is(err, errDeviceNotFound) {
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

	args, err := json.Marshal(ipc.SetKeyArgs{Device: device, Index: index, H: hue, S: sat, V: val})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	resp, err := h.client.Do(r.Context(), ipc.Request{Op: ipc.OpSetKey, Args: args})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !resp.OK {
		writeError(w, statusForResponse(resp), errors.New(resp.Error))
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

func statusForResponse(resp ipc.Response) int {
	switch resp.Code {
	case ipc.ErrNotFound:
		return http.StatusNotFound
	case ipc.ErrBadRequest:
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
```

Note: this step references `errDeviceNotFound`, defined in Task 11's
`caps.go` (the next task). Task 11 must land before this compiles — the
tests above are written first per TDD, but don't run `go build`/`go test`
successfully until Task 11's file exists too. This is expected; run both
tasks' Step 2/Step 4 back to back if executing them in the same session.

- [ ] **Step 4: Run tests — expect them to still fail until Task 11 lands**

```bash
go vet ./internal/api/...
```

Expected: `errDeviceNotFound undefined` — this is expected at this point;
proceed directly to Task 11, then return and run the full test suite.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handlers.go internal/api/handlers_test.go
git commit -m "Add internal/api PUT /devices/{name}/keys/{pos} handler"
```

---

## Task 11: `internal/api` — capabilities cache, `GET /devices`, `GET /devices/{name}`

**Files:**
- Create: `internal/api/caps.go`
- Test: `internal/api/caps_test.go`
- Test: `internal/api/handlers_list_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/api/caps_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

func TestCapabilitiesCacheIndexFor(t *testing.T) {
	calls := 0
	client := &fakeClient{do: func(_ context.Context, req ipc.Request) (ipc.Response, error) {
		calls++
		data, err := json.Marshal(ipc.Capabilities{
			LEDCount:  2,
			Positions: []ipc.LEDPosition{{Index: 0, Row: 1, Col: 1}, {Index: 1, Row: 2, Col: 2}},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return ipc.Response{OK: true, Data: data}, nil
	}}
	cache := NewCapabilitiesCache(client)

	index, found, err := cache.IndexFor(context.Background(), "cxt12e4-0", 2, 2)
	if err != nil || !found || index != 1 {
		t.Fatalf("IndexFor = %d,%v,%v; want 1,true,nil", index, found, err)
	}
	if _, _, err := cache.IndexFor(context.Background(), "cxt12e4-0", 1, 1); err != nil {
		t.Fatalf("second IndexFor: %v", err)
	}
	if calls != 1 {
		t.Errorf("connectord called %d times, want 1 (second call should hit the cache)", calls)
	}
}

func TestCapabilitiesCacheUnknownDevice(t *testing.T) {
	client := &fakeClient{do: func(context.Context, ipc.Request) (ipc.Response, error) {
		return ipc.Response{OK: false, Code: ipc.ErrNotFound, Error: "unknown device"}, nil
	}}
	cache := NewCapabilitiesCache(client)

	_, _, err := cache.IndexFor(context.Background(), "nope", 0, 0)
	if err == nil {
		t.Fatal("IndexFor: want error for unknown device")
	}
}

func TestCapabilitiesCacheNotPresent(t *testing.T) {
	client := &fakeClient{do: func(context.Context, ipc.Request) (ipc.Response, error) {
		data, _ := json.Marshal(ipc.Capabilities{LEDCount: 1, Positions: []ipc.LEDPosition{{Index: 0, Row: 0, Col: 0}}})
		return ipc.Response{OK: true, Data: data}, nil
	}}
	cache := NewCapabilitiesCache(client)

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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

func TestListDevices(t *testing.T) {
	client := &fakeClient{do: func(_ context.Context, req ipc.Request) (ipc.Response, error) {
		if req.Op != ipc.OpListDevices {
			t.Fatalf("Op = %q, want %q", req.Op, ipc.OpListDevices)
		}
		data, _ := json.Marshal(ipc.ListDevicesResult{Devices: []ipc.DeviceSummary{{Name: "cxt12e4-0", Connected: true}}})
		return ipc.Response{OK: true, Data: data}, nil
	}}
	h := NewHandler(client, &fakeCaps{})

	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var result ipc.ListDevicesResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Devices) != 1 || result.Devices[0].Name != "cxt12e4-0" {
		t.Errorf("result = %+v", result)
	}
}

func TestGetCapabilitiesUnknownDevice(t *testing.T) {
	client := &fakeClient{do: func(context.Context, ipc.Request) (ipc.Response, error) {
		return ipc.Response{OK: false, Code: ipc.ErrNotFound, Error: "unknown device"}, nil
	}}
	h := NewHandler(client, &fakeCaps{})

	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/devices/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./internal/api/...
```

Expected: `FAIL` — `NewCapabilitiesCache`, `errDeviceNotFound` undefined.

- [ ] **Step 3: Implement**

`internal/api/caps.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/seefood/vialrgb-notify/internal/ipc"
)

var errDeviceNotFound = errors.New("api: unknown device")

// CapabilitiesCache implements CapabilitiesSource by calling
// GetCapabilities through client on first use per device, caching the
// row,col->index mapping for subsequent PUTs. Phase 1+2 has no cache
// invalidation on device reconnect/hotplug — a later phase's job, not this
// one's.
type CapabilitiesCache struct {
	client DispatchClient
	mu     sync.Mutex
	byDev  map[string]ipc.Capabilities
}

// NewCapabilitiesCache constructs a CapabilitiesCache.
func NewCapabilitiesCache(client DispatchClient) *CapabilitiesCache {
	return &CapabilitiesCache{client: client, byDev: make(map[string]ipc.Capabilities)}
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

func (c *CapabilitiesCache) capabilities(ctx context.Context, device string) (ipc.Capabilities, error) {
	c.mu.Lock()
	caps, ok := c.byDev[device]
	c.mu.Unlock()
	if ok {
		return caps, nil
	}

	args, err := json.Marshal(ipc.GetCapabilitiesArgs{Device: device})
	if err != nil {
		return ipc.Capabilities{}, err
	}
	resp, err := c.client.Do(ctx, ipc.Request{Op: ipc.OpGetCapabilities, Args: args})
	if err != nil {
		return ipc.Capabilities{}, err
	}
	if !resp.OK {
		if resp.Code == ipc.ErrNotFound {
			return ipc.Capabilities{}, errDeviceNotFound
		}
		return ipc.Capabilities{}, errors.New(resp.Error)
	}
	if err := json.Unmarshal(resp.Data, &caps); err != nil {
		return ipc.Capabilities{}, err
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
	resp, err := h.client.Do(r.Context(), ipc.Request{Op: ipc.OpListDevices})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !resp.OK {
		writeError(w, statusForResponse(resp), errors.New(resp.Error))
		return
	}
	var result ipc.ListDevicesResult
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) getCapabilities(w http.ResponseWriter, r *http.Request) {
	device := r.PathValue("name")
	args, err := json.Marshal(ipc.GetCapabilitiesArgs{Device: device})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	resp, err := h.client.Do(r.Context(), ipc.Request{Op: ipc.OpGetCapabilities, Args: args})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if !resp.OK {
		writeError(w, statusForResponse(resp), errors.New(resp.Error))
		return
	}
	var caps ipc.Capabilities
	if err := json.Unmarshal(resp.Data, &caps); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, caps)
}
```

- [ ] **Step 4: Run the full `internal/api` suite, verify it passes**

```bash
go test ./internal/api/... -v
```

Expected: all `PASS`, including Task 10's tests that were pending on
`errDeviceNotFound`.

- [ ] **Step 5: Commit**

```bash
git add internal/api
git commit -m "Add internal/api capabilities cache and GET /devices routes"
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
    address: 0.0.0.0:8080
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
// Package config loads connectord's on-disk configuration
// (~/.config/vialrgb-notify/config.yaml). connectord owns loading this
// whole file and hands restd only the fields it needs over the socketpair
// at startup, per the design spec's Config section.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is connectord's on-disk configuration.
type Config struct {
	Naming    NamingRule `yaml:"naming"`
	Listeners Listeners  `yaml:"listeners"`
}

// NamingRule selects connectord's preferred device-naming strategy; it
// still falls back automatically per device if a device can't answer
// GetKeyboardUID.
type NamingRule struct {
	Prefer string `yaml:"prefer"` // "uid" (default), "path", or "vidpid"
}

// Listeners configures restd's listeners.
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
// never allowed without one.
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

## Task 14: `cmd/connectord` — RPC server loop

**Files:**
- Create: `cmd/connectord/rpc.go`
- Test: `cmd/connectord/rpc_test.go`

- [ ] **Step 1: Write the failing tests**

`cmd/connectord/rpc_test.go`:

```go
package main

import (
	"encoding/json"
	"net"
	"testing"

	"github.com/seefood/vialrgb-notify/internal/hid"
	"github.com/seefood/vialrgb-notify/internal/ipc"
)

// fakeController substitutes for *hid.Device in tests — no real hardware
// needed.
type fakeController struct {
	numLEDs   uint16
	positions map[uint16][2]uint8
	lastSet   []hid.KeyColor
}

func (f *fakeController) SetKeys(keys []hid.KeyColor) error {
	f.lastSet = keys
	return nil
}

func (f *fakeController) GetNumberLEDs() (uint16, error) { return f.numLEDs, nil }

func (f *fakeController) GetLEDInfo(index uint16) (uint8, uint8, error) {
	pos := f.positions[index]
	return pos[0], pos[1], nil
}

func (f *fakeController) Close() error { return nil }

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestHandleSetKeyUnknownDevice(t *testing.T) {
	resp := handle(ipc.Request{Op: ipc.OpSetKey, Args: mustJSON(t, ipc.SetKeyArgs{Device: "missing"})}, devices{})
	if resp.OK {
		t.Fatal("want OK=false for unknown device")
	}
	if resp.Code != ipc.ErrNotFound {
		t.Errorf("Code = %q, want %q", resp.Code, ipc.ErrNotFound)
	}
}

func TestHandleSetKey(t *testing.T) {
	fc := &fakeController{}
	devs := devices{"cxt12e4-0": fc}
	resp := handle(ipc.Request{
		Op:   ipc.OpSetKey,
		Args: mustJSON(t, ipc.SetKeyArgs{Device: "cxt12e4-0", Index: 5, H: 0, S: 255, V: 255}),
	}, devs)
	if !resp.OK {
		t.Fatalf("resp not OK: %+v", resp)
	}
	if len(fc.lastSet) != 1 || fc.lastSet[0].Index != 5 {
		t.Errorf("SetKeys called with %+v", fc.lastSet)
	}
}

func TestHandleGetCapabilities(t *testing.T) {
	fc := &fakeController{numLEDs: 2, positions: map[uint16][2]uint8{0: {1, 1}, 1: {2, 2}}}
	devs := devices{"cxt12e4-0": fc}
	resp := handle(ipc.Request{
		Op:   ipc.OpGetCapabilities,
		Args: mustJSON(t, ipc.GetCapabilitiesArgs{Device: "cxt12e4-0"}),
	}, devs)
	if !resp.OK {
		t.Fatalf("resp not OK: %+v", resp)
	}
	var caps ipc.Capabilities
	if err := json.Unmarshal(resp.Data, &caps); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if caps.LEDCount != 2 || len(caps.Positions) != 2 {
		t.Errorf("caps = %+v", caps)
	}
}

func TestServeConnOverPipe(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()

	devs := devices{"cxt12e4-0": &fakeController{}}
	go func() { _ = serveConn(ipc.NetConn{Conn: server}, devs) }()

	req := ipc.Request{Op: ipc.OpSetKey, Args: mustJSON(t, ipc.SetKeyArgs{Device: "cxt12e4-0", Index: 0})}
	if err := ipc.WriteFrame(client, req); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var resp ipc.Response
	if err := ipc.ReadFrame(client, &resp); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !resp.OK {
		t.Fatalf("resp not OK: %+v", resp)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

```bash
go test ./cmd/connectord/...
```

Expected: `FAIL` — package doesn't exist yet.

- [ ] **Step 3: Implement**

`cmd/connectord/rpc.go`:

```go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/seefood/vialrgb-notify/internal/hid"
	"github.com/seefood/vialrgb-notify/internal/ipc"
)

// devices is connectord's view of currently known devices: name -> open
// controller. Populated at startup from hid.Enumerate + hid.AssignNames.
type devices map[string]hid.Controller

// serveConn runs connectord's side of the internal link: read one Request,
// dispatch it against devs, write one Response, repeat until a frame read
// errors (peer closed). Strictly synchronous per the design spec — one
// request in flight, no concurrent access to any hid.Controller.
func serveConn(conn ipc.Conn, devs devices) error {
	for {
		var req ipc.Request
		if err := conn.ReadFrame(&req); err != nil {
			return err
		}
		if err := conn.WriteFrame(handle(req, devs)); err != nil {
			return err
		}
	}
}

func handle(req ipc.Request, devs devices) ipc.Response {
	switch req.Op {
	case ipc.OpSetKey:
		return handleSetKey(req, devs)
	case ipc.OpSetKeys:
		return handleSetKeys(req, devs)
	case ipc.OpListDevices:
		return handleListDevices(devs)
	case ipc.OpGetCapabilities:
		return handleGetCapabilities(req, devs)
	default:
		return errResponse(ipc.ErrBadRequest, fmt.Sprintf("connectord: unknown op %q", req.Op))
	}
}

func handleSetKey(req ipc.Request, devs devices) ipc.Response {
	var args ipc.SetKeyArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errResponse(ipc.ErrBadRequest, err.Error())
	}
	dev, ok := devs[args.Device]
	if !ok {
		return errResponse(ipc.ErrNotFound, fmt.Sprintf("connectord: unknown device %q", args.Device))
	}
	if err := dev.SetKeys([]hid.KeyColor{{Index: args.Index, H: args.H, S: args.S, V: args.V}}); err != nil {
		return errResponse(ipc.ErrUnavailable, err.Error())
	}
	return ipc.Response{OK: true}
}

func handleSetKeys(req ipc.Request, devs devices) ipc.Response {
	var args ipc.SetKeysArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errResponse(ipc.ErrBadRequest, err.Error())
	}
	dev, ok := devs[args.Device]
	if !ok {
		return errResponse(ipc.ErrNotFound, fmt.Sprintf("connectord: unknown device %q", args.Device))
	}
	keys := make([]hid.KeyColor, len(args.Keys))
	for i, k := range args.Keys {
		keys[i] = hid.KeyColor{Index: k.Index, H: k.H, S: k.S, V: k.V}
	}
	if err := dev.SetKeys(keys); err != nil {
		return errResponse(ipc.ErrUnavailable, err.Error())
	}
	return ipc.Response{OK: true}
}

func handleListDevices(devs devices) ipc.Response {
	result := ipc.ListDevicesResult{}
	for name := range devs {
		result.Devices = append(result.Devices, ipc.DeviceSummary{Name: name, Connected: true})
	}
	data, err := json.Marshal(result)
	if err != nil {
		return errResponse(ipc.ErrUnavailable, err.Error())
	}
	return ipc.Response{OK: true, Data: data}
}

func handleGetCapabilities(req ipc.Request, devs devices) ipc.Response {
	var args ipc.GetCapabilitiesArgs
	if err := json.Unmarshal(req.Args, &args); err != nil {
		return errResponse(ipc.ErrBadRequest, err.Error())
	}
	dev, ok := devs[args.Device]
	if !ok {
		return errResponse(ipc.ErrNotFound, fmt.Sprintf("connectord: unknown device %q", args.Device))
	}
	n, err := dev.GetNumberLEDs()
	if err != nil {
		return errResponse(ipc.ErrUnavailable, err.Error())
	}
	caps := ipc.Capabilities{LEDCount: int(n)}
	for i := uint16(0); i < n; i++ {
		row, col, err := dev.GetLEDInfo(i)
		if err != nil {
			return errResponse(ipc.ErrUnavailable, err.Error())
		}
		caps.Positions = append(caps.Positions, ipc.LEDPosition{Index: i, Row: row, Col: col})
	}
	data, err := json.Marshal(caps)
	if err != nil {
		return errResponse(ipc.ErrUnavailable, err.Error())
	}
	return ipc.Response{OK: true, Data: data}
}

func errResponse(code ipc.ErrorCode, msg string) ipc.Response {
	return ipc.Response{OK: false, Code: code, Error: msg}
}
```

- [ ] **Step 4: Run tests, verify they pass**

```bash
go test ./cmd/connectord/... -v
```

Expected: all `PASS`.

- [ ] **Step 5: Commit**

```bash
git add cmd/connectord/rpc.go cmd/connectord/rpc_test.go
git commit -m "Add cmd/connectord RPC server loop"
```

---

## Task 15: `cmd/connectord` — `main()`

**Files:**
- Create: `cmd/connectord/main.go`

This is wiring code tying together real HID hardware, process spawning, and
OS file descriptors — not unit-testable without hardware and a real child
process. It's verified by `go build` here and by Task 19's manual/gated
hardware check, per the design spec's testing plan ("Manual/gated:
real-hardware round-trip check, documented but not automated").

- [ ] **Step 1: Implement**

`cmd/connectord/main.go`:

```go
package main

import (
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	goHid "github.com/sstallion/go-hid"

	"github.com/seefood/vialrgb-notify/internal/hid"
	"github.com/seefood/vialrgb-notify/internal/ipc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if err := goHid.Init(); err != nil {
		logger.Error("hid init failed", "err", err)
		os.Exit(1)
	}
	defer func() { _ = goHid.Exit() }()

	infos, err := hid.Enumerate()
	if err != nil {
		logger.Error("enumerate failed", "err", err)
		os.Exit(1)
	}

	identities := make([]hid.Identity, 0, len(infos))
	for _, info := range infos {
		uid, hasUID := probeUID(info.Path, logger)
		identities = append(identities, hid.Identity{
			Path: info.Path, VendorID: info.VendorID, ProductID: info.ProductID,
			UID: uid, HasUID: hasUID,
		})
	}
	named := hid.AssignNames(identities)

	devs := make(devices, len(named))
	for _, n := range named {
		dev, err := hid.Open(n.Path)
		if err != nil {
			logger.Warn("could not open device", "name", n.Name, "err", err)
			continue
		}
		if err := dev.SetDirectMode(); err != nil {
			logger.Warn("could not set direct mode", "name", n.Name, "err", err)
		}
		devs[n.Name] = dev
	}

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		logger.Error("socketpair failed", "err", err)
		os.Exit(1)
	}
	parentFile := os.NewFile(uintptr(fds[0]), "connectord-ipc")
	childFile := os.NewFile(uintptr(fds[1]), "restd-ipc")

	cmd := exec.Command(restdPath())
	cmd.ExtraFiles = []*os.File{childFile}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		logger.Error("failed to spawn restd", "err", err)
		os.Exit(1)
	}
	_ = childFile.Close() // connectord's copy of restd's end; restd holds its own via inherited fd 3

	netConn, err := net.FileConn(parentFile)
	if err != nil {
		logger.Error("could not wrap ipc fd", "err", err)
		os.Exit(1)
	}

	// Phase 1 supervision: restart-together-on-exit. When the link errors
	// (restd died), fall through and exit; the OS-level process manager
	// (LaunchAgent/systemd) is responsible for restarting connectord, which
	// then spawns a fresh restd. Smarter in-process supervision is future
	// work, not required here.
	if err := serveConn(ipc.NetConn{Conn: netConn}, devs); err != nil {
		logger.Error("ipc connection closed", "err", err)
	}
	if err := cmd.Wait(); err != nil {
		logger.Warn("restd exited", "err", err)
	}
}

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

func restdPath() string {
	dir, err := os.Executable()
	if err != nil {
		return "restd"
	}
	return filepath.Join(filepath.Dir(dir), "restd")
}
```

- [ ] **Step 2: Verify it builds**

```bash
go build ./cmd/connectord
```

Expected: exit 0, produces a `connectord` binary (remove it or build to
`bin/` per Task 17's Makefile — don't commit the binary).

- [ ] **Step 3: Commit**

```bash
rm -f connectord
git add cmd/connectord/main.go
git commit -m "Add cmd/connectord main(): enumerate, spawn restd, serve IPC"
```

---

## Task 16: `cmd/restd` — `main()`

**Files:**
- Create: `cmd/restd/main.go`

Also wiring code (real listeners, real inherited fd) — verified by `go
build` and Task 19's manual check, same rationale as Task 15.

- [ ] **Step 1: Implement**

`cmd/restd/main.go`:

```go
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/seefood/vialrgb-notify/internal/api"
	"github.com/seefood/vialrgb-notify/internal/dispatcher"
	"github.com/seefood/vialrgb-notify/internal/ipc"
)

const (
	dispatcherQueueDepth = 64
	linkTimeout          = 1 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	// fd 3 is connectord's end of the socketpair, inherited via
	// exec.Cmd.ExtraFiles (os/exec's doc: "entry i becomes file descriptor
	// 3+i").
	ipcFile := os.NewFile(3, "connectord-ipc")
	netConn, err := net.FileConn(ipcFile)
	if err != nil {
		logger.Error("could not wrap inherited ipc fd", "err", err)
		os.Exit(1)
	}
	conn := ipc.NetConn{Conn: netConn}

	disp := dispatcher.New(conn, dispatcherQueueDepth, linkTimeout)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go disp.Run(ctx)

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

	logger.Info("restd listening", "socket", socketPath)
	if err := http.Serve(listener, handler.Routes()); err != nil {
		logger.Error("http server exited", "err", err)
		os.Exit(1)
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

Note: the optional TCP listener (mandatory-token-when-enabled) and reading
`config.Config` at startup are deferred to a follow-up task once Phase 1+2's
core path is proven end to end — flagging this explicitly rather than
silently shipping a stub, per the plan's scope: the design spec requires TCP
be off by default, and this main() satisfies that by simply not offering it
yet. Wiring config.Load and the TCP listener in is a small, mechanical
addition once the Unix-socket path is confirmed working over real hardware
(Task 19) — do not consider Phase 1+2 complete until that follow-up lands.

- [ ] **Step 2: Verify it builds**

```bash
go build ./cmd/restd
```

Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
rm -f restd
git add cmd/restd/main.go
git commit -m "Add cmd/restd main(): dispatcher, unix socket listener, routes"
```

---

## Task 17: Build tooling and doc updates

**Files:**
- Create: `Makefile`
- Modify: `CLAUDE.md`
- Modify: `README.md`

- [ ] **Step 1: Add a Makefile**

`Makefile`:

```makefile
.PHONY: build test lint

build:
	go build -o bin/connectord ./cmd/connectord
	go build -o bin/restd ./cmd/restd

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
make build                        # builds bin/connectord and bin/restd
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

- [ ] **Step 3: Commit**

```bash
git add Makefile CLAUDE.md README.md
git commit -m "Add Makefile; update CLAUDE.md with Go build/test commands"
```

---

## Task 18: Full integration pass

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

## Task 19: Manual/gated hardware round-trip check

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
change touching `internal/hid`, `cmd/connectord`, or the IPC/dispatcher
path, before considering that change verified end-to-end.

Prerequisites: `cxt_studio/12e4` attached, `personal/vialrgb-direct/001-enable`
firmware flashed (see `../../../README.md` and the parent `CXT-studio`
tree's `../qmk_vial`). On macOS, the built `connectord` binary needs the
Input Monitoring TCC grant on first run (see `README.md`'s "Why not Python"
section for why this requires a stable code-signing identity).

1. `make build`
2. Run `./bin/connectord` in a terminal you can watch logs in. Confirm it
   logs enumerating the board and (on macOS, first run only) grant Input
   Monitoring when prompted, then re-run.
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
7. Kill `restd`'s process directly (not `connectord`) and confirm
   `connectord` also exits shortly after (Phase 1's restart-together
   supervision) rather than hanging indefinitely.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/manual-checks/phase1-2-hardware-roundtrip.md
git commit -m "Document the manual/gated Phase 1+2 hardware round-trip check"
```

---

## Explicitly deferred past this plan

Matches the design spec's own "Explicitly out of scope for Phase 1+2"
section, plus two items this plan itself deferred (flagged inline above,
not silently dropped):

- The optional TCP listener and its mandatory bearer token (Task 16's note).
- Wiring `config.Load` into `cmd/restd`/`cmd/connectord` main()s (Task 16's
  note) — both mains currently use hardcoded/env-var defaults instead.
- Packaging/installation mechanics for the privilege model described in the
  design spec's Background section: the macOS LaunchAgent plist and the
  Linux udev rule file. This plan implements `connectord`/`restd` as plain
  binaries invoked directly (per Task 19's manual check) — it does not
  create or install a `launchd`/`systemd`/udev unit. Doing so is a
  packaging task, not a coding gap in Phase 1+2's architecture itself.
- Effects/animation (Phase 3), YAML status templates (Phase 4), per-client
  key allocation (Phase 5), server-owned timers (Phase 6), Windows support,
  TLS on the TCP listener.
