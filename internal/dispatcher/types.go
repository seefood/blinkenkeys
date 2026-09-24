// Package dispatcher is blinkenkeysd's single-writer "traffic cop" for the HID
// handle: every other goroutine (HTTP handlers; the periodic/reconnect
// redraw loops) submits work through the Dispatcher and never touches a
// hid.Controller directly, since hidapi is not guaranteed safe under
// concurrent access to the same handle.
package dispatcher

import (
	"errors"

	"github.com/seefood/blinkenkeys/internal/color"
	"github.com/seefood/blinkenkeys/internal/keyaddr"
)

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

// LEDPosition is one LED's matrix location — see keyaddr.Position.
type LEDPosition = keyaddr.Position

// Capabilities is GetCapabilities's result.
type Capabilities struct {
	LEDCount  int           `json:"led_count"`
	Positions []LEDPosition `json:"positions"`
}

// PendingWrite is a write to a device whose capabilities aren't known yet
// (pre-declared, never connected), kept by literal address until they are.
type PendingWrite struct {
	Addr  keyaddr.Address
	Color color.HSV
}

// ErrCapsUnknown is returned by GetCapabilities for a device that isn't
// connected and whose capabilities were never fetched (a pre-declared,
// never-seen device) — internal/api maps this to a 503.
var ErrCapsUnknown = errors.New("dispatcher: device capabilities not yet known")

// ErrKeyNotFound is returned for a key address not on the device's matrix
// (capabilities known) — internal/api maps this to a 404.
var ErrKeyNotFound = errors.New("dispatcher: key not on device")

// ErrNamedKeyUnsupported is returned for a key-name address, reserved for
// Phase 5's named-key allocation — internal/api maps this to a 501.
var ErrNamedKeyUnsupported = errors.New("dispatcher: named keys are not implemented yet")
