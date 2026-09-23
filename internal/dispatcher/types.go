// Package dispatcher is blinkenkeysd's single-writer "traffic cop" for the HID
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
